
package logger

import (
    "io"
    "log/slog"
    "os"
)

func SetupLogger(env string, logFilePath string) *slog.Logger {
    var log *slog.Logger

    // Открываем файл для логов
    // O_APPEND - дописывать в конец
    // O_CREATE - создать, если нет
    // O_WRONLY - только запись
    file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        // Если файл открыть не удалось, пишем в stderr панику, так как это критично
        panic(err)
    }

    // Пишем и в файл, и в консоль
    w := io.MultiWriter(os.Stdout, file)

    opts := &slog.HandlerOptions{
        Level: slog.LevelDebug, // Оставляем Debug, чтобы видеть всё
    }

    switch env {
    case "local":
        // TextHandler читаемее для человека
        log = slog.New(slog.NewTextHandler(w, opts))
    default:
        // JSON лучше для парсеров (Kibana/Loki)
        log = slog.New(slog.NewJSONHandler(w, opts))
    }

    return log
}
