package service

import (
	"bufio"
	"context"
	"fmt"
	"image-manager/internal/config"
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
	cfg *config.Config
}

func NewBuilder(log *slog.Logger, cfg *config.Config) *Builder {
	return &Builder{log: log, cfg: cfg}
}

// ensureScriptsExecutable делает скрипты элементов исполняемыми.
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
		if info.IsDir() {
			return nil
		}
		dir := filepath.Base(filepath.Dir(path))
		if filepath.Ext(dir) == ".d" {
			if err := os.Chmod(path, 0755); err != nil {
				return fmt.Errorf("failed to chmod %s: %w", path, err)
			}
		}
		return nil
	})
}

// BuildImage запускает реальный процесс сборки.
func (b *Builder) BuildImage(imageName string, distro string, logWriter io.Writer) error {
	const op = "service.Builder.BuildImage"
    
    // 0. CHECK PERMISSIONS
    if err := b.ensureScriptsExecutable(); err != nil {
        b.log.Warn("failed to ensure executable permissions", slog.String("err", err.Error()))
    }
    
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	b.log.Info("starting build process",
		slog.String("image", imageName),
		slog.String("distro", distro),
		slog.String("timeout", "10m"),
	)
    
    // --- ЗАГРУЗКА КОНФИГА ОС ---
    configName := distro
    if distro == "debian" { configName = "debian-12" }
    if distro == "ubuntu" { configName = "ubuntu-24" }

    // Используем loadErr, чтобы избежать конфликтов имен
    distroCfg, loadErr := config.LoadDistroConfig(configName)
    if loadErr != nil {
        return fmt.Errorf("%s: unknown distro '%s' (config load failed): %w", op, distro, loadErr)
    }

	var elements []string
	var extraEnv []string

    if distroCfg.OSElement != "" {
        elements = append(elements, distroCfg.OSElement)
    }
    
    elements = append(elements, distroCfg.Elements...)

    for k, v := range distroCfg.Env {
        extraEnv = append(extraEnv, fmt.Sprintf("%s=%s", k, v))
    }

	// Формируем итоговые аргументы для disk-image-create
	args := elements
	args = append(args,
		"-p", "iputils-ping,curl,qemu-guest-agent,vim",
		"-o", imageName,
	)

	cmd := exec.CommandContext(ctx, "disk-image-create", args...)

	cmd.Env = os.Environ()
	wd, _ := os.Getwd()
	localElementsPath := filepath.Join(wd, "elements")
	
	cmd.Env = append(cmd.Env, "ELEMENTS_PATH="+localElementsPath)
	cmd.Env = append(cmd.Env, "DIB_CLOUD_INIT_DATASOURCES=OpenStack,ConfigDrive,None")
	
	if b.cfg.GRPCServer.PublicAddress != "" {
		cmd.Env = append(cmd.Env, "MANAGER_ADDRESS="+b.cfg.GRPCServer.PublicAddress)
	} else {
		b.log.Warn("GRPC_PUBLIC_ADDRESS is empty! Agent might not connect back.")
	}

    if b.cfg.OpenStack.SSHInjectKey != "" {
        cmd.Env = append(cmd.Env, "SSH_INJECT_KEY="+b.cfg.OpenStack.SSHInjectKey)
    }

	cmd.Env = append(cmd.Env, extraEnv...)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: failed to start command: %w", op, err)
	}

	logChan := make(chan string, 100)
	logBufferChan := make(chan []string, 1)

	go func() {
		var buffer []string
		const maxLines = 50 
		for line := range logChan {
			buffer = append(buffer, line)
			if len(buffer) > maxLines {
				buffer = buffer[1:]
			}
		}
		logBufferChan <- buffer
	}()

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

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			b.log.Info("[DIB-OUT]", slog.String("msg", line))
			if logWriter != nil {
				if strings.Contains(line, "Converting image") {
					logWriter.Write([]byte("\n>>> [STATUS] Build logic finished. Converting raw image to QCOW2 (Final Step)...\n\n"))
				}
				logWriter.Write([]byte(line + "\n"))
			}
			logChan <- fmt.Sprintf("[STDOUT] %s", line)
		}
	}()

	err := cmd.Wait()
	
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
}

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