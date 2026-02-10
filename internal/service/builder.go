package service

import (
	"bufio"
	"context"
	"fmt"
	"image-manager/internal/config" // Добавляем импорт
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Builder struct {
	log *slog.Logger
	cfg *config.Config // Добавляем конфиг
}

func NewBuilder(log *slog.Logger, cfg *config.Config) *Builder {
	return &Builder{log: log, cfg: cfg}
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
    
    // --- ЗАГРУЗКА КОНФИГА ОС ---
    // Хак совместимости для старого фронтенда
    configName := distro
    if distro == "debian" { configName = "debian-12" }
    if distro == "ubuntu" { configName = "ubuntu-24" }

    distroCfg, err := config.LoadDistroConfig(configName)
    if err != nil {
        return fmt.Errorf("%s: unknown distro '%s' (config load failed): %w", op, distro, err)
    }

	var elements []string
	var extraEnv []string

    // Добавляем OS элемент (debian, ubuntu)
    if distroCfg.OSElement != "" {
        elements = append(elements, distroCfg.OSElement)
    }
    
    // Добавляем остальные элементы из конфига
    elements = append(elements, distroCfg.Elements...)

    // Добавляем ENV из конфига
    for k, v := range distroCfg.Env {
        extraEnv = append(extraEnv, fmt.Sprintf("%s=%s", k, v))
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
	
	// Передаем адрес менеджера в DIB.
	// Элемент agent-install должен подхватить эту переменную и "запечь" её в образ.
	if b.cfg.GRPCServer.PublicAddress != "" {
		cmd.Env = append(cmd.Env, "MANAGER_ADDRESS="+b.cfg.GRPCServer.PublicAddress)
	} else {
		b.log.Warn("GRPC_PUBLIC_ADDRESS is empty! Agent might not connect back.")
	}

    // Передаем SSH ключ (если есть)
    if b.cfg.OpenStack.SSHInjectKey != "" {
        cmd.Env = append(cmd.Env, "SSH_INJECT_KEY="+b.cfg.OpenStack.SSHInjectKey)
    }

	cmd.Env = append(cmd.Env, extraEnv...)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: failed to start command: %w", op, err)
	}

	// Канал для сбора всех логов (Stdout + Stderr) чтобы показать последние строки при ошибке
	logChan := make(chan string, 100)
	// Буфер для последних N строк
	logBufferChan := make(chan []string, 1)

	// Goroutine-сборщик логов в буфер
	go func() {
		var buffer []string
		const maxLines = 50 // Увеличим буфер

		for line := range logChan {
			buffer = append(buffer, line)
			if len(buffer) > maxLines {
				buffer = buffer[1:]
			}
		}
		logBufferChan <- buffer
	}()

	// Логи в фоне (Stderr)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			b.log.Debug("[DIB-ERR]", slog.String("msg", line))
			
			if logWriter != nil {
				logWriter.Write([]byte(line + "\n")) 
			}
			logChan <- fmt.Sprintf("[STDERR] %s", line)
		}
	}()

	// Логи в фоне (Stdout)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			b.log.Info("[DIB-OUT]", slog.String("msg", line))
            
			if logWriter != nil {
				// Улучшение UX: перехватываем важное сообщение о конвертации
				if strings.Contains(line, "Converting image") {
					logWriter.Write([]byte("\n>>> [STATUS] Build logic finished. Converting raw image to QCOW2 (Final Step)...\n\n"))
				}
				logWriter.Write([]byte(line + "\n"))
			}
			logChan <- fmt.Sprintf("[STDOUT] %s", line)
		}
	}()

	// Ждем завершения
	err := cmd.Wait()
	
	// Закрываем канал, чтобы сборщик завершил работу и отдал буфер
	// (Важно: cmd.Wait() гарантирует, что пайпы закрыты и сканеры дочитали)
	// Но нам нужно убедиться, что горутины сканеров закончили писать в logChan.
	// Проще всего: дать небольшую паузу или использовать WaitGroup, но для простоты:
	// Сканеры выходят когда пайпы закрываются (при смерти процесса).
	
	// Небольшой хак: даем время сканерам докинуть остатки
	time.Sleep(100 * time.Millisecond)
	close(logChan)
	
	lastLogs := <-logBufferChan

	if err != nil {
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
