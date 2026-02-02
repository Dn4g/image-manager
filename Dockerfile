# === Stage 1: Builder ===
FROM golang:1.24 AS builder

WORKDIR /src

# Кешируем зависимости
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходники
COPY . .

# 1. Собираем Агента (он должен лежать в elements/agent-install/agent)
# Важно: собираем его именно по тому пути, откуда его потом заберет финальный образ
RUN GOOS=linux GOARCH=amd64 go build -o elements/agent-install/agent cmd/agent/main.go

# 2. Собираем Основной Сервер
RUN go build -o image-manager cmd/image-manager/main.go

FROM debian:bookworm-slim

WORKDIR /app

# Установка  зависимостей для disk-image-builder

RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    qemu-utils \
    kpartx \
    git \
    sudo \
    curl \
    ca-certificates \
    procps \
    squashfs-tools \
    dosfstools \
    gdisk \
    debootstrap \
    && rm -rf /var/lib/apt/lists/*

# Установка diskimage-builder
RUN pip3 install diskimage-builder --break-system-packages

# Копируем бинарник сервера
COPY --from=builder /src/image-manager /usr/local/bin/image-manager

# Копируем структуру elements (вместе со скомпилированным агентом внутри!)
COPY --from=builder /src/elements /app/elements

# Копируем веб-интерфейс
COPY --from=builder /src/web /app/web

# Создаем папку для логов и базы данных
RUN mkdir -p /app/data

# Переменные окружения по умолчанию
ENV WORK_DIR=/app

# Порт
EXPOSE 8080

# Запуск
CMD ["image-manager"]
