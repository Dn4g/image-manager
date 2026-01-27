package config

import (
    "log"
    "os"

    "github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
    Env         string `yaml:"env" env:"ENV" env-default:"local"`
    StoragePath string `yaml:"storage_path" env:"STORAGE_PATH" env-default:"./image-manager.db"`
    LogFilePath string `yaml:"log_file_path" env:"LOG_FILE_PATH" env-default:"app.log"`
    HTTPServer  struct {
        Address string `yaml:"address" env:"HTTP_ADDRESS" env-default:"0.0.0.0:8080"`
    }
    GRPCServer struct {
	Port string `yaml:"port" env:"GRPC_PORT" env-default:":50051"`
   }


     OpenStack struct {
   AuthURL    string `yaml:"auth_url" env:"OS_AUTH_URL"`
   Username   string `yaml:"username" env:"OS_USERNAME"`
   Password   string `yaml:"password" env:"OS_PASSWORD"`
   ProjectID  string `yaml:"project_id" env:"OS_PROJECT_ID"`
   ProjectName string `yaml:"project_name" env:"OS_PROJECT_NAME"`
   DomainName string `yaml:"domain_name" env:"OS_DOMAIN_NAME" env-default:"Default"`
   Region     string `yaml:"region" env:"OS_REGION_NAME" env-default:"RegionOne"`
   SSHKeyName string `yaml:"ssh_key_name" env:"OS_SSH_KEY_NAME" env-default:"master-key"`
   FlavorID   string `yaml:"flavor_id" env:"OS_FLAVOR_ID" env-default:"2"`
   NetworkID  string `yaml:"network_id" env:"OS_NETWORK_ID" env-required:"true"`
    }

}

func MustLoad() *Config {
    configPath := os.Getenv("CONFIG_PATH")
    if configPath == "" {
        configPath = "./config.env"
    }

    // Проверяем наличие файла, но не падаем, если его нет (может быть только ENV)
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        // Файла нет, пробуем читать чисто ENV
        var cfg Config
        if err := cleanenv.ReadEnv(&cfg); err != nil {
            log.Fatalf("cannot read config from env: %s", err)
        }
        return &cfg
    }

    var cfg Config
    if err := cleanenv.ReadConfig(configPath, &cfg); err != nil {
        log.Fatalf("cannot read config file: %s", err)
    }

    return &cfg
}
