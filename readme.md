**Image Manager** — это инструмент автоматизации жизненного цикла облачных образов (Cloud Images) для OpenStack.
Он собирает образ ОС с нуля, загружает его в облако, создает тестовую виртуальную машину, проверяет её работоспособность и убирает за собой.

## Возможности

*   **Сборка:** Использует `disk-image-builder` (DIB) для создания образов (Debian, Ubuntu и др.).
*   **Автоматизация:** Полный пайплайн: Build -> Upload (Glance) -> Boot (Nova) -> Test -> Cleanup.
*   **Тестирование:** Внедряет **gRPC Агента** внутрь образа, который рапортует о состоянии сети и дисков.
*   **Безопасность:** Автоматическая настройка SSH-ключей и пользователей.
*   **Web UI:** Простой дашборд для запуска сборок.

---

## Структура проекта

```text
image-manager/
├── cmd/
│   ├── image-manager/      # Точка входа сервера (Manager)
│   └── agent/              # Исходный код Агента (запускается внутри VM)
├── internal/
│   ├── adapter/openstack/  # Клиент Gophercloud (Nova, Glance)
│   ├── config/             # Чтение ENV и конфигов
│   ├── handler/            # HTTP API и Web UI логика
│   ├── logger/             # Настройка slog (JSON/Text)
│   ├── server/grpc/        # gRPC-сервер
│   ├── service/            # Обертка над DIB
│   └── storage/            # Работа с SQLite (история сборок)
├── pkg/
│   └── pb/                 # Сгенерированный gRPC код (Protobuf)
├── elements/               # Кастомные элементы disk-image-builder
│   ├── agent-install/      # Установка бинарника агента и systemd сервиса
│   └── cloud-init-custom/  # Хардкод настроек SSH (PasswordAuth)
├── web/                    # Статика (index.html)
└── config.env.example      # Пример конфигурации
```

---

##  Установка и Запуск

### 1. Требования
*   Linux (Ubuntu/Debian)
*   Go 1.21+
*   `disk-image-builder` (`pip install diskimage-builder`)
*   `qemu-utils`
*   Доступ к OpenStack

### 2. Подготовка Агента
Перед запуском сервера необходимо скомпилировать агента и положить его в элемент DIB:

```bash
# Компиляция под Linux AMD64
GOOS=linux GOARCH=amd64 go build -o elements/agent-install/agent cmd/agent/main.go
```

### 3. Конфигурация
Создайте файл `config.env` на основе примера:

```bash
cp config.env.example config.env
# Редактируйте параметры OpenStack (Auth URL, Password, Project ID)
```

### 4. Запуск Менеджера

```bash
# Загрузка переменных и старт
source config.env
go run cmd/image-manager/main.go
```

Веб-интерфейс будет доступен по адресу: `http://localhost:8080`

---

## Как это работает (Workflow)

1.  **POST /build:** Пользователь нажимает кнопку в UI.
2.  **DB:** Создается запись о сборке (`PENDING`).
3.  **DIB:** Запускается процесс сборки `.qcow2` файла. В образ внедряется:
    *   `cloud-init` и настройки сети.
    *   `qemu-guest-agent`.
    *   Бинарник `agent` и systemd-юнит для его автозапуска.
4.  **Glance:** Образ загружается в OpenStack.
5.  **Nova:** Создается VM из этого образа (с SSH-ключом из конфига).
6.  **Agent:** VM загружается, агент стартует и звонит на gRPC порт Менеджера (`:50051`).
    *   Агент проверяет диск и интернет.
    *   Агент отправляет отчет (`SUCCESS/FAIL`).
    *   Агент самоуничтожается.
7.  **Cleanup:** Менеджер получает отчет, удаляет тестовую VM и обновляет статус в БД.

---

##  Отладка

*   **Логи сервера:** Пишутся в `stdout` и файл `app.log`.
*   **Логи сборки:** Видны в логе сервера (префикс `[DIB]`).
*   **VM не звонит?**
    *   Проверьте Security Groups (разрешен ли исходящий трафик на порт 50051).
    *   Проверьте `config.env`: правильно ли указан IP менеджера для агента.
