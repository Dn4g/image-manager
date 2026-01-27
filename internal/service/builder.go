package service

import (
	"bufio"
	"context"
	"fmt"
	"io" // <-- Добавляем io
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Builder struct {
	log *slog.Logger
}

func NewBuilder(log *slog.Logger) *Builder {
	return &Builder{log: log}
}

// ensureScriptsExecutable делает скрипты элементов исполняемыми.
// DIB молча игнорирует скрипты без chmod +x.
func (b *Builder) ensureScriptsExecutable() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	elementsDir := filepath.Join(wd, "elements")

	return filepath.Walk(elementsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Ищем файлы внутри папок install.d, pre-install.d, post-install.d и т.д.
		// Но для простоты - делаем +x всем файлам, у которых нет расширения или .sh/.bash
		// ИЛИ просто всем файлам внутри install.d
		
		if info.IsDir() {
			return nil
		}

		// Если файл лежит в папке *.d (например install.d), он должен быть исполняемым
		dir := filepath.Base(filepath.Dir(path))
		if filepath.Ext(dir) == ".d" {
			if err := os.Chmod(path, 0755); err != nil {
				return fmt.Errorf("failed to chmod %s: %w", path, err)
			}
			// b.log.Debug("chmod +x", slog.String("file", path))
		}
		return nil
	})
}

// BuildImage запускает реальный процесс сборки.
// logWriter - куда писать сырые логи (в БД)
func (b *Builder) BuildImage(imageName string, distro string, logWriter io.Writer) error {
	const op = "service.Builder.BuildImage"
    
    // 0. CHECK PERMISSIONS
    if err := b.ensureScriptsExecutable(); err != nil {
        b.log.Warn("failed to ensure executable permissions", slog.String("err", err.Error()))
        // Не падаем, пробуем собрать так
    }
    
    // ... (context init)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	b.log.Info("starting build process",
		slog.String("image", imageName),
		slog.String("distro", distro),
		slog.String("timeout", "10m"),
	)
    // ... (переменные elements/env) ...
    
	var elements []string
	var extraEnv []string

	// БАЗОВЫЕ ЭЛЕМЕНТЫ (Общие для всех, но лучше контролировать явно)
	commonElements := []string{
		"vm",
		"simple-init",
		"cloud-init",
		"cloud-init-custom", // Наши настройки SSH (PasswordAuth)
		"openssh-server",    // SSH сервер
		"enable-serial-console",
		"block-device-efi",
		"bootloader",
		"journal-to-console",
		"dhcp-all-interfaces",
		"agent-install", // Наш агент
	}

	switch distro {
	case "debian":
		// === КОНФИГУРАЦИЯ DEBIAN ===
		elements = append([]string{"debian"}, commonElements...)
		elements = append(elements, "cloud-init-datasources", "package-installs", "sysprep")
		extraEnv = append(extraEnv, "DIB_RELEASE=bookworm")

	case "ubuntu":
		// === КОНФИГУРАЦИЯ UBUNTU ===
		elements = append([]string{"ubuntu"}, commonElements...)
		elements = append(elements, "cloud-init-datasources", "package-installs", "sysprep")
		extraEnv = append(extraEnv, "DIB_RELEASE=noble")

	default:
		return fmt.Errorf("%s: unsupported distro: %s", op, distro)
	}

	// Формируем итоговые аргументы для disk-image-create
	args := elements
	args = append(args,
		"-p", "iputils-ping,curl,qemu-guest-agent,vim",
		"-o", imageName,
	)

	// ИСПОЛЬЗУЕМ CommandContext
	cmd := exec.CommandContext(ctx, "disk-image-create", args...)

	// ENV
	cmd.Env = os.Environ()
	wd, _ := os.Getwd()
	localElementsPath := filepath.Join(wd, "elements")
	
	cmd.Env = append(cmd.Env, "ELEMENTS_PATH="+localElementsPath)
	cmd.Env = append(cmd.Env, "DIB_CLOUD_INIT_DATASOURCES=OpenStack,ConfigDrive,None")
	cmd.Env = append(cmd.Env, extraEnv...)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: failed to start command: %w", op, err)
	}

	// Канал для сбора ошибок из stderr (последние N строк)
	errLogChan := make(chan []string, 1)

	// Логи в фоне (Stderr)
	go func() {
		scanner := bufio.NewScanner(stderr)
		var buffer []string
		const maxLines = 20

		for scanner.Scan() {
			line := scanner.Text()
			b.log.Debug("[DIB]", slog.String("msg", line))
			
			// Пишем в БД (если врайтер передан)
			if logWriter != nil {
				// Добавляем префикс ERR для красоты, или просто пишем как есть
				logWriter.Write([]byte(line)) 
			}

			// Сохраняем в буфер
			buffer = append(buffer, line)
			if len(buffer) > maxLines {
				buffer = buffer[1:]
			}
		}
		errLogChan <- buffer
	}()

	// Логи в фоне (Stdout)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			b.log.Info("[DIB]", slog.String("msg", line))
            
            // Пишем в БД
			if logWriter != nil {
				logWriter.Write([]byte(line))
			}
		}
	}()

	// Ждем завершения
	if err := cmd.Wait(); err != nil {
		// Достаем последние логи ошибок
		lastLogs := <-errLogChan
		
		errMsg := fmt.Sprintf("build failed: %v. Last logs:\n", err)
		for _, l := range lastLogs {
			errMsg += fmt.Sprintf("  > %s\n", l)
		}
		
		return fmt.Errorf("%s: %s", op, errMsg)
	}

	b.log.Info("build completed successfully", slog.String("image", imageName))
	return nil
} // <--- ВОТ ТУТ КОНЕЦ BuildImage

// Cleanup удаляет артефакты.
func (b *Builder) Cleanup(imageName string) error {
	b.log.Info("cleaning up artifacts", slog.String("image", imageName))

	_ = os.Remove(imageName)
	_ = os.Remove(imageName + ".qcow2")
	_ = os.RemoveAll(imageName + ".d")

	matches, _ := filepath.Glob("dib-manifest-*" + imageName + "*")
	for _, f := range matches {
		_ = os.Remove(f)
	}

	return nil
}
