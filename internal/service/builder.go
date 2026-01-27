package service

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

type Builder struct {
	log *slog.Logger
}

func NewBuilder(log *slog.Logger) *Builder {
	return &Builder{log: log}
}

// BuildImage запускает реальный процесс сборки.
func (b *Builder) BuildImage(imageName string, distro string) error {
	const op = "service.Builder.BuildImage"

	b.log.Info("starting build process",
		slog.String("image", imageName),
		slog.String("distro", distro),
	)

	args := []string{
		distro,
		"vm",
		"simple-init",
		"cloud-init",
		"cloud-init-custom",
		"openssh-server",
		"enable-serial-console",
		"block-device-efi",
		"bootloader",
		"journal-to-console",
		"cloud-init-datasources",
		"package-installs",
		"dhcp-all-interfaces",
		"sysprep",
		"agent-install",
		"-p", "iputils-ping,curl,qemu-guest-agent,vim",
		"-o", imageName,
	}

	cmd := exec.Command("disk-image-create", args...)

	// ENV
	cmd.Env = os.Environ()
	wd, _ := os.Getwd()
	localElementsPath := filepath.Join(wd, "elements")
	cmd.Env = append(cmd.Env, "ELEMENTS_PATH="+localElementsPath)
	cmd.Env = append(cmd.Env, "DIB_CLOUD_INIT_DATASOURCES=OpenStack,ConfigDrive,None")

	if distro == "debian" {
		cmd.Env = append(cmd.Env, "DIB_RELEASE=bookworm")
	}

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: failed to start command: %w", op, err)
	}

	// Логи в фоне
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			b.log.Debug("[DIB]", slog.String("msg", scanner.Text()))
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			b.log.Info("[DIB]", slog.String("msg", scanner.Text()))
		}
	}()

	// Ждем завершения (ЭТО ВСЁ ЕЩЕ ВНУТРИ BuildImage)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: build failed: %w", op, err)
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
