package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"image-manager/internal/adapter/openstack"
	"image-manager/internal/service"
	"image-manager/internal/storage"
)

// Handler группирует зависимости
type Handler struct {
	log      *slog.Logger
	builder  *service.Builder
	store    *storage.Storage
	osClient *openstack.Client
	flavorID string
	netID    string
}

// New — конструктор
func New(log *slog.Logger, b *service.Builder, s *storage.Storage, osc *openstack.Client, flavorID, netID string) *Handler {
	return &Handler{
		log:      log,
		builder:  b,
		store:    s,
		osClient: osc,
		flavorID: flavorID,
		netID:    netID,
	}
}

// Helper struct to write logs to DB
type DBLogWriter struct {
	store *storage.Storage
	id    int64
}

func (w *DBLogWriter) Write(p []byte) (n int, err error) {
	err = w.store.AppendLog(w.id, string(p))
	return len(p), err
}

// RegisterRoutes настраивает маршруты
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/build", h.StartBuild)
	r.Get("/api/images", h.GetCloudImages)
	r.Get("/api/build/{id}", h.GetBuildStatus)
	r.Get("/api/history", h.GetBuildHistory)
}

// GetBuildHistory возвращает список последних сборок
func (h *Handler) GetBuildHistory(w http.ResponseWriter, r *http.Request) {
	builds, err := h.store.GetBuilds()
	if err != nil {
		h.log.Error("failed to get history", slog.String("err", err.Error()))
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(builds)
}

// GetBuildStatus возвращает статус и логи конкретной сборки
func (h *Handler) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	status, logs, err := h.store.GetBuildStatus(id)
	if err != nil {
		h.log.Warn("build not found", slog.Int64("id", id))
		http.Error(w, "build not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":     idStr,
		"status": status,
		"logs":   logs,
	})
}

// GetCloudImages возвращает список образов из OpenStack
func (h *Handler) GetCloudImages(w http.ResponseWriter, r *http.Request) {
	images, err := h.osClient.ListImages()
	if err != nil {
		h.log.Error("failed to list images", slog.String("error", err.Error()))
		http.Error(w, "upstream error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(images)
}

// StartBuild обрабатывает запрос на сборку
func (h *Handler) StartBuild(w http.ResponseWriter, r *http.Request) {
	type Request struct {
		ImageName string `json:"image_name"`
		Distro    string `json:"distro"`
	}
	var req Request

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json format", http.StatusBadRequest)
		return
	}

	if req.ImageName == "" || req.Distro == "" {
		http.Error(w, "image_name and distro fields are required", http.StatusBadRequest)
		return
	}

	h.log.Info("received build request", slog.String("image", req.ImageName))

	id, err := h.store.CreateBuild(req.ImageName)
	if err != nil {
		h.log.Error("failed to save build to db", slog.String("error", err.Error()))
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	_ = h.store.AppendLog(id, fmt.Sprintf("Build request received for %s (%s)", req.ImageName, req.Distro))

	go func() {
		targetFilename := req.ImageName + ".qcow2"
		h.log.Info("background: starting build", slog.Int64("id", id))
		_ = h.store.AppendLog(id, "Starting disk-image-builder...")

		defer func() {
			h.log.Info("background: cleanup started")
			if err := h.builder.Cleanup(req.ImageName); err != nil {
				h.log.Warn("cleanup warning", slog.String("err", err.Error()))
			}
		}()

		logWriter := &DBLogWriter{store: h.store, id: id}

		// ШАГ А: Сборка
		_ = h.store.UpdateBuildStatus(id, "BUILDING")
		
		err := h.builder.BuildImage(req.ImageName, req.Distro, logWriter)
		if err != nil {
			h.log.Error("background: build failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_BUILD")
			_ = h.store.AppendLog(id, fmt.Sprintf("Build failed: %s", err.Error()))
			return
		}
		_ = h.store.AppendLog(id, "Build successful. Image size optimized.")

		// ШАГ Б: Загрузка
		_ = h.store.UpdateBuildStatus(id, "UPLOADING")
		_ = h.store.AppendLog(id, "Uploading to OpenStack Glance (Candidate)...")
		
		candidateName := req.ImageName + "-candidate"
		
		h.log.Info("background: starting upload", slog.String("file", targetFilename))

		// Очищаем старых кандидатов перед загрузкой нового
		if err := h.osClient.DeleteImageByName(candidateName); err != nil {
			h.log.Warn("failed to delete old candidate (ignoring)", slog.String("err", err.Error()))
		}

		glanceID, err := h.osClient.UploadImage(targetFilename, candidateName)
		if err != nil {
			h.log.Error("background: upload failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_UPLOAD")
			_ = h.store.AppendLog(id, fmt.Sprintf("Upload failed: %s", err.Error()))
			return
		}
		
		_ = h.store.SetGlanceID(id, glanceID)

		h.log.Info("background: image uploaded", slog.String("glance_id", glanceID))
		_ = h.store.AppendLog(id, fmt.Sprintf("Candidate uploaded. ID: %s", glanceID))

		// ШАГ В: Создание VM
		_ = h.store.UpdateBuildStatus(id, "BOOTING_VM")
		_ = h.store.AppendLog(id, "Creating Test VM...")
		h.log.Info("background: creating test vm...")

		vmName := req.ImageName + "-test-agent"
		vmID, err := h.osClient.CreateVM(vmName, glanceID, h.flavorID, h.netID, "")

		if err != nil {
			h.log.Error("background: vm create failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_VM_BOOT")
			_ = h.store.AppendLog(id, fmt.Sprintf("VM boot failed: %s", err.Error()))
			return
		}
		
		_ = h.store.SetVMID(id, vmID)
		_ = h.store.AppendLog(id, fmt.Sprintf("VM created. ID: %s. Waiting for ACTIVE status...", vmID))

		// Ждем, пока VM станет ACTIVE
		if err := h.osClient.WaitForVMActive(vmID, 5*time.Minute); err != nil {
			h.log.Error("background: vm failed to become active", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_VM_BOOT")
			_ = h.store.AppendLog(id, fmt.Sprintf("VM boot failed (not active): %s", err.Error()))
			// Пытаемся удалить сломанную VM
			_ = h.osClient.DeleteVM(vmID)
			return
		}

		_ = h.store.AppendLog(id, "VM is ACTIVE. Waiting for agent report...")

		// ШАГ Г: Ожидание агента
		h.log.Info("background: vm active, waiting for agent...", slog.String("vm_id", vmID))
		_ = h.store.UpdateBuildStatus(id, "WAITING_AGENT")

		// WATCHDOG (8 min total)
		go func(bid int64, vid string) {
            // Intermediate check (3 min)
            time.Sleep(3 * time.Minute)
			status, _, _ := h.store.GetBuildStatus(bid)
            if status == "WAITING_AGENT" {
                _ = h.store.AppendLog(bid, "WARNING: Agent is silent for 3 minutes. Check server logs. Will terminate in 5 minutes.")
            }

            // Final check (remaining 5 min)
			time.Sleep(5 * time.Minute)
			status, _, _ = h.store.GetBuildStatus(bid)
			if status == "WAITING_AGENT" {
				h.log.Warn("WATCHDOG: Timeout reached", slog.Int64("id", bid))
				_ = h.store.UpdateBuildStatus(bid, "ERROR_TIMEOUT")
				_ = h.store.AppendLog(bid, "TIMEOUT: Agent did not report in 8 minutes. Terminating.")
				_ = h.osClient.DeleteVM(vid)
			}
		}(id, vmID)

	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)

	response := map[string]any{
		"status":   "started",
		"build_id": id,
		"message":  "Build started. VM will be launched after upload.",
	}
	json.NewEncoder(w).Encode(response)
}