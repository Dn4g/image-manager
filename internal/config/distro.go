package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DistroConfig описывает параметры сборки конкретного дистрибутива
type DistroConfig struct {
	ID        string            `yaml:"id"`
	Name      string            `yaml:"name"`
	OSElement string            `yaml:"os_element"`
	Env       map[string]string `yaml:"env"`
	Elements  []string          `yaml:"elements"`
}

// LoadDistroConfig ищет и загружает конфиг для указанного дистрибутива.
// Ищет файл configs/distros/{name}.yaml
func LoadDistroConfig(distroName string) (*DistroConfig, error) {
	// Безопасность: очищаем имя от путей
	safeName := filepath.Base(distroName)
	path := filepath.Join("configs", "distros", safeName+".yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read distro config %s: %w", path, err)
	}

	var cfg DistroConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse distro config: %w", err)
	}

	return &cfg, nil
}
