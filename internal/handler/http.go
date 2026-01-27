package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	// Библиотека для маршрутизации (Router)
	"github.com/go-chi/chi/v5"

	// Наши пакеты
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
}

// New — конструктор
func New(log *slog.Logger, b *service.Builder, s *storage.Storage, osc *openstack.Client) *Handler {
	return &Handler{
		log:      log,
		builder:  b,
		store:    s,
		osClient: osc,
	}
}

// RegisterRoutes настраивает маршруты
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/build", h.StartBuild)
}

// StartBuild обрабатывает запрос на сборку
func (h *Handler) StartBuild(w http.ResponseWriter, r *http.Request) {
	// 1. Парсинг запроса
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

	// 2. Создание записи в БД
	id, err := h.store.CreateBuild(req.ImageName)
	if err != nil {
		h.log.Error("failed to save build to db", slog.String("error", err.Error()))
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	// 3. Фоновый процесс (Goroutine)
	go func() {
		targetFilename := req.ImageName + ".qcow2"
		h.log.Info("background: starting build", slog.Int64("id", id))

		// === CLEANUP (Сработает при выходе из этой функции) ===
		defer func() {
			h.log.Info("background: cleanup started")
			if err := h.builder.Cleanup(req.ImageName); err != nil {
				h.log.Warn("cleanup warning", slog.String("err", err.Error()))
			}
		}()
		// ======================================================

		// ШАГ А: Сборка (Build)
		err := h.builder.BuildImage(req.ImageName, req.Distro)
		if err != nil {
			h.log.Error("background: build failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_BUILD")
			return
		}

		// ШАГ Б: Загрузка (Upload)
		_ = h.store.UpdateBuildStatus(id, "UPLOADING")
		h.log.Info("background: starting upload", slog.String("file", targetFilename))

		glanceID, err := h.osClient.UploadImage(targetFilename, req.ImageName)
		if err != nil {
			h.log.Error("background: upload failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_UPLOAD")
			return
		}

		h.log.Info("background: image uploaded", slog.String("glance_id", glanceID))

		// ШАГ В: Создание VM (Create VM)
		_ = h.store.UpdateBuildStatus(id, "BOOTING_VM")
		h.log.Info("background: creating test vm...")

		// --- КОНФИГУРАЦИЯ ТЕСТОВОЙ VM ---
		flavorID := "2" // m1.small
		
		// Твоя Public сеть
		netID := "47a414d9-6db9-4694-b976-d41b1c48a46e"
		// --------------------------------

		vmName := req.ImageName + "-test-agent"

		// UserData пустой, ключ прокидывается автоматически через Client
		vmID, err := h.osClient.CreateVM(vmName, glanceID, flavorID, netID, "")

		if err != nil {
			h.log.Error("background: vm create failed", slog.String("error", err.Error()))
			_ = h.store.UpdateBuildStatus(id, "ERROR_VM_BOOT")
			return
		}

		// ШАГ Г: Ожидание агента
		h.log.Info("background: vm created, waiting for agent...", slog.String("vm_id", vmID))
		_ = h.store.UpdateBuildStatus(id, "WAITING_AGENT")

		// На этом месте процесс завершается (для этой горутины).
		// Дальше работу подхватит gRPC сервер, когда агент позвонит.
	}()

	// 4. Ответ клиенту
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)

	response := map[string]any{
		"status":   "started",
		"build_id": id,
		"message":  "Build started. VM will be launched after upload.",
	}
	json.NewEncoder(w).Encode(response)
}
