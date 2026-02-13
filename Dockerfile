# === Stage 1: Builder ===
FROM golang:1.24 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Агент — статический бинарник без GLIBC
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o elements/agent-install/agent cmd/agent/main.go

# Основной сервер
RUN go build -o image-manager cmd/image-manager/main.go

# === Stage 2: Runtime (pre-built base with DIB + system deps) ===
FROM docker-registry.default.svc.cluster.local:5000/image-manager-base:latest

WORKDIR /app

COPY --from=builder /src/image-manager /usr/local/bin/image-manager
COPY --from=builder /src/elements /app/elements
COPY --from=builder /src/web /app/web
COPY --from=builder /src/configs /app/configs

RUN mkdir -p /app/data

ENV WORK_DIR=/app

EXPOSE 8080

CMD ["image-manager"]
