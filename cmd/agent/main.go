package main

import (
	"context"
	"encoding/json" // <-- Нужно для разбора JSON от OpenStack
	"io"            // <-- Нужно для чтения ответа
	"log"
	"net/http" // <-- Нужно для запроса к Metadata
	"os"
	"os/exec"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "image-manager/pkg/pb"
)

// getVMID стучится в OpenStack Metadata Service и узнает свой UUID.
func getVMID() string {
	// Адрес сервиса метаданных в OpenStack стандартный:
	url := "http://169.254.169.254/openstack/latest/meta_data.json"

	// Делаем GET запрос с таймаутом (чтобы не висеть вечно)
	client := http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Failed to get metadata from %s: %v", url, err)
		return "unknown-id" // Если не ssудалось, вернем заглушку
	}
	defer resp.Body.Close()

	// Читаем тело ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read metadata body: %v", err)
		return "unknown-id"
	}

	// Парсим JSON. Нам нужно поле "uuid".
	var meta struct {
		UUID string `json:"uuid"`
	}

	if err := json.Unmarshal(body, &meta); err != nil {
		log.Printf("Failed to parse metadata JSON: %v. Body: %s", err, string(body))
		return "unknown-id"
	}

	return meta.UUID
}

func main() {
	log.Println("Agent started...")

	managerAddress := os.Getenv("MANAGER_ADDRESS")
	if managerAddress == "" {
		// Fallback для локальной отладки, если забыли прокинуть
		managerAddress = "127.0.0.1:50051"
		log.Printf("MANAGER_ADDRESS not set, defaulting to %s", managerAddress)
	}

	// 1. Узнаем, кто мы (получаем ID)
	vmID := getVMID()
	log.Printf("Detected VM ID: %s", vmID)

	// 2. Подключаемся к Менеджеру
	conn, err := grpc.Dial(managerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect to manager: %v", err)
	}
	defer conn.Close()

	client := pb.NewAgentServiceClient(conn)

	// 3. Выполняем проверки (Smoke Tests)

	// Проверка Диска (Root mounted)
	diskCheck := "OK"
	if _, err := exec.Command("ls", "/").Output(); err != nil {
		diskCheck = "FAIL: " + err.Error()
	}

	// TODO: Добавить проверку размера диска (Disk Resize check).
	// Цель: Убедиться, что cloud-init (growpart) отработал и корневой раздел расширился до размера флейвора.
	// Логика:
	// 1. Выполнить `df -BG /`
	// 2. Проверить, что Size > 10G (или соответствует ожиданиям flavor m1.small).
	// 3. Если меньше (например 2G), значит ресайз не сработал -> FAIL.

	// Проверка Сети (Ping Google)
	netCheck := "OK"
	if err := exec.Command("ping", "-c", "1", "8.8.8.8").Run(); err != nil {
		netCheck = "FAIL: No Internet"
	}

	// 4. Отправляем отчет
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	log.Println("Reporting status to Manager...")
	resp, err := client.ReportStatus(ctx, &pb.StatusRequest{
		VmId:    vmID, // <-- ИСПОЛЬЗУЕМ НАСТОЯЩИЙ ID
		Phase:   "BOOT_CHECK",
		Success: (netCheck == "OK" && diskCheck == "OK"),
		Details: "Disk: " + diskCheck + "; Net: " + netCheck,
	})

	if err != nil {
		log.Fatalf("could not report status: %v", err)
	}

	log.Printf("Manager replied: %s", resp.Command)

	// 5. Самоуничтожение (если Менеджер дал добро)
	if resp.Command == "OK" || resp.Command == "SHUTDOWN" {
		log.Println("Mission complete. Self-destructing...")

		// Выключаем сервис
		exec.Command("systemctl", "disable", "image-agent").Run()
		os.Remove("/etc/systemd/system/image-agent.service")
		os.Remove("/usr/local/bin/agent")

		// Останавливаемся
		exec.Command("systemctl", "stop", "image-agent").Run()
		os.Exit(0)
	}

	// Если что-то пошло не так, висим 10 сек и выходим
	time.Sleep(10 * time.Second)
}
