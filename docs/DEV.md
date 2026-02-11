# Гайд разработчика

## Локальная разработка

Для работы над бэкендом (Go) и фронтендом не обязательно запускать полный цикл сборки образов.

### Запуск
1.  Убедиться, что есть файл `config.env`.
2.  Запустить сервер:
    ```bash
    go run cmd/image-manager/main.go
    ```
3.  Веб-интерфейс: `http://localhost:8080`.

*Примечание:* Сборка образов (нажатие кнопки "Собрать") требует наличия Linux и утилит `disk-image-builder`. На macOS/Windows сборка упадет с ошибкой.

## Добавление новой ОС

Система поддерживает добавление новых дистрибутивов через конфигурационные файлы.

1.  Создать файл в `configs/distros/`, например `rocky-9.yaml`.
    ```yaml
    id: "rocky-9"
    name: "Rocky Linux 9"
    os_element: "rocky-container" # Имя элемента DIB
    env:
      DIB_RELEASE: "9"
    elements:
      - "vm"
      - "simple-init"
      ...
    ```
2.  Добавить запись в `web/index.html` (массив `distros`), указав `type: 'rocky-9'`.

## CI/CD Пайплайн

В репозитории настроен `Jenkinsfile`.
При пуше в ветку `main`:
1.  Jenkins запускает под с Kaniko.
2.  Собирается Docker-образ `image-manager`.
3.  Образ пушится в локальный Registry (`registry.dn4g.ru`).
4.  Выполняется `kubectl rollout restart`, обновляя приложение в кластере.
