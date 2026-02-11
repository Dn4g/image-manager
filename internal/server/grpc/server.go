package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"

	// Импортируем сгенерированный код и наши пакеты
	"image-manager/internal/adapter/openstack"
	"image-manager/internal/storage"
	pb "image-manager/pkg/pb"
)

// AgentServer реализует интерфейс, описанный в proto-файле.
type AgentServer struct {
	pb.UnimplementedAgentServiceServer // Обязательная встройка

	log      *slog.Logger
	store    *storage.Storage
	osClient *openstack.Client // Исправили опечатку (было ocClient)
}

// NewAgentServer - конструктор
// Добавили аргумент osc (OpenStack Client)
func NewAgentServer(log *slog.Logger, store *storage.Storage, osc *openstack.Client) *AgentServer {
	return &AgentServer{
		log:      log,
		store:    store,
		osClient: osc,
	}
}

// Run запускает gRPC сервер на указанном порту (блокирующая функция)
func (s *AgentServer) Run(port string) error {
	s.log.Info("starting gRPC server", slog.String("port", port))

	lis, err := net.Listen("tcp", port)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	grpcServer := grpc.NewServer()

	// Регистрируем нашу реализацию сервера
	pb.RegisterAgentServiceServer(grpcServer, s)

	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}

// ReportStatus - это метод, который вызовет Агент.
func (s *AgentServer) ReportStatus(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	s.log.Info("gRPC: received report",
		slog.String("vm_id", req.VmId),
		slog.Bool("success", req.Success),
		slog.String("details", req.Details),
	)

	// По умолчанию говорим: "Жди"
	command := "WAIT"

	if req.Success {
		s.log.Info("Test PASSED. Promoting image...", slog.String("id", req.VmId))
		
		// 1. PROMOTE IMAGE
		// Получаем данные о билде, чтобы знать ID кандидата и целевое имя
		buildInfo, err := s.store.GetBuildInfoByVMID(req.VmId)
		if err != nil {
			s.log.Error("failed to get build info for promotion", slog.String("err", err.Error()))
		} else {
			// Подменяем образ
			if err := s.osClient.PromoteImage(buildInfo.GlanceID, buildInfo.ImageName); err != nil {
				s.log.Error("CRITICAL: PROMOTION FAILED", slog.String("err", err.Error()))
				// TODO: Возможно, стоит пометить статус как ERROR_PROMOTE?
			} else {
				s.log.Info("Image promoted to production", slog.String("name", buildInfo.ImageName))
			}
		}
		
		// ОБНОВЛЯЕМ СТАТУС В БАЗЕ
		if err := s.store.UpdateBuildStatusByVMID(req.VmId, "SUCCESS"); err != nil {
			s.log.Error("failed to update db status", slog.String("err", err.Error()))
		}

		// Удаляем VM через наш клиент
		if err := s.osClient.DeleteVM(req.VmId); err != nil {
			s.log.Error("failed to delete vm", slog.String("err", err.Error()))
		} else {
			s.log.Info("VM deleted successfully")
		}

		// Говорим агенту выключиться (он сделает самоуничтожение)
		command = "SHUTDOWN"
	} else {
		s.log.Warn("Test FAILED. Keeping VM for debug.", slog.String("details", req.Details))
		
		// ОБНОВЛЯЕМ СТАТУС НА ОШИБКУ
		_ = s.store.UpdateBuildStatusByVMID(req.VmId, "ERROR_TEST")
		
		// Не удаляем VM, чтобы админ мог зайти и посмотреть.
	}

	return &pb.StatusResponse{
		Command: command,
	}, nil
}
