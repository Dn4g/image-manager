package main

import (
    "log/slog"
    "net/http"
    "os"
    "path/filepath"
    "strings"

    // Библиотеки для роутинга и middleware
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"

    // Наши пакеты
    "image-manager/internal/adapter/openstack"
    "image-manager/internal/config"
    "image-manager/internal/handler"
    "image-manager/internal/logger"
    "image-manager/internal/service"
    "image-manager/internal/storage"
    grpcServer "image-manager/internal/server/grpc" // Алиас, чтобы не путать с пакетом grpc
)

func main() {
    // 1. Загрузка Конфигурации
    // Читаем ENV переменные и конфиг-файлы. Если чего-то важного нет, программа может упасть тут.
    cfg := config.MustLoad()

    // 2. Настройка Логгера
    // В зависимости от среды (local/prod) вывод будет разным (текст/json).
    log := logger.SetupLogger(cfg.Env, cfg.LogFilePath)

    log.Info("initializing application...", slog.String("env", cfg.Env))

    // 3. Подключение к Базе Данных (SQLite)
    store, err := storage.New(cfg.StoragePath)
    if err != nil {
        // Если базы нет или прав нет — работать нельзя. Выходим с ошибкой (код 1).
        log.Error("failed to init storage", slog.String("error", err.Error()))
        os.Exit(1)
    }
    // defer гарантирует, что база закроется корректно, когда main() завершится (при выключении).
    defer store.Close()

    // Применяем простую "миграцию" (создаем таблицу, если её нет).
    if err := store.Init(); err != nil {
        log.Error("failed to create tables", slog.String("error", err.Error()))
        os.Exit(1)
    }

    // Создаем Клиента OpenStack.
    // креды в конфиге, мы не сможем работать.
    if cfg.OpenStack.AuthURL == "" {
        log.Warn("OpenStack Auth URL is empty! Uploads will fail.")
    }

    osClient, err := openstack.NewClient(
        log,
        cfg.OpenStack.AuthURL,
        cfg.OpenStack.Username,
        cfg.OpenStack.Password,
        cfg.OpenStack.ProjectID,
	cfg.OpenStack.ProjectName,
        cfg.OpenStack.DomainName,
	cfg.OpenStack.Region,
	cfg.OpenStack.SSHKeyName,
    )
    if err != nil {
        log.Error("failed to connect to openstack", slog.String("error", err.Error()))
        os.Exit(1) // Падаем, так как без облака нам делать нечего
    }

     // 4. Инициализация Сервисов
    // Создаем "Сборщика" (Builder).
    builder := service.NewBuilder(log)

    go func() {
        agentSrv := grpcServer.NewAgentServer(log, store,osClient)
        if err := agentSrv.Run(cfg.GRPCServer.Port); err != nil {
            log.Error("gRPC server failed", slog.String("err", err.Error()))
        }
    }()

    // 5. Настройка HTTP Роутера
    r := chi.NewRouter()

    // Middleware — это функции, которые выполняются для каждого запроса ДО хендлера.
    r.Use(middleware.Logger)    // Логирует каждый запрос (метод, путь, время выполнения)
    r.Use(middleware.Recoverer) // Спасает сервер от падения (panic), если в хендлере произойдет ошибка в коде.

    // 6. Сборка всего вместе (Dependency Injection)
    // Создаем Handler и передаем ему все инструменты: логгер, билдер, базу, ос-клиент.
    h := handler.New(log, builder, store, osClient, cfg.OpenStack.FlavorID, cfg.OpenStack.NetworkID)

    // Регистрируем пути (/build -> h.StartBuild)
    h.RegisterRoutes(r)

     workDir, _ := os.Getwd()
     filesDir := http.Dir(filepath.Join(workDir, "web"))
     
    // Хендлер для статики
    FileServer(r, "/", filesDir)
   
    // 7. Запуск Сервера
    log.Info("starting http server", slog.String("address", cfg.HTTPServer.Address))

    // ListenAndServe блокирует выполнение программы и слушает порт.
    // Программа будет висеть тут, пока её не убьют (Ctrl+C).
    if err := http.ListenAndServe(cfg.HTTPServer.Address, r); err != nil {
        log.Error("server crashed", slog.String("error", err.Error()))
        os.Exit(1)
    }
}

// FileServer удобно настраивает раздачу статики для Chi
func FileServer(r chi.Router, path string, root http.FileSystem) {
    if strings.ContainsAny(path, "{}*") {
        panic("FileServer does not permit any URL parameters.")
    }

    if path != "/" && path[len(path)-1] != '/' {
        r.Get(path, http.RedirectHandler(path+"/", 301).ServeHTTP)
        path += "/"
    }
    path += "*"

    r.Get(path, func(w http.ResponseWriter, r *http.Request) {
        rctx := chi.RouteContext(r.Context())
        pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
        fs := http.StripPrefix(pathPrefix, http.FileServer(root))
        fs.ServeHTTP(w, r)
    })
}
