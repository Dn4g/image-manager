package storage

import (
    "database/sql"
    "fmt"

    _ "github.com/mattn/go-sqlite3" // Импортируем драйвер
)

type Storage struct {
    db *sql.DB
}

func New(storagePath string) (*Storage, error) {
    const op = "storage.sqlite.New" // Имя операции для логов

    // Открываем подключение
    db, err := sql.Open("sqlite3", storagePath)
    if err != nil {
        return nil, fmt.Errorf("%s: %w", op, err)
    }

    // Проверяем, что база реально доступна (ping)
    if err := db.Ping(); err != nil {
        return nil, fmt.Errorf("%s: %w", op, err)
    }

    // === TUNING ===
    // Включаем WAL-режим для скорости и конкурентности
    if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
        return nil, fmt.Errorf("%s: enable WAL: %w", op, err)
    }
    // Включаем поддержку внешних ключей (на будущее)
    if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
        return nil, fmt.Errorf("%s: enable FK: %w", op, err)
    }

    return &Storage{db: db}, nil
}

// Init создает таблицы, если их нет.
// В нормальном проде это делают миграторы (Goose), но для MVP сойдет и так.
func (s *Storage) Init() error {
    query := `
    CREATE TABLE IF NOT EXISTS builds (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        image_name TEXT NOT NULL,
        status TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `
    _, err := s.db.Exec(query)
    if err != nil {
        return fmt.Errorf("storage.Init: %w", err)
    }
    return nil
}

func (s *Storage) Close() error {
    return s.db.Close()
}


// CreateBuild создает запись о новой сборке и возвращает её ID.
func (s *Storage) CreateBuild(imageName string) (int64, error) {
    query := `INSERT INTO builds (image_name, status) VALUES (?, ?) RETURNING id`

    var id int64
    // Используем QueryRow, так как мы ждем возврата ID (RETURNING id)
    err := s.db.QueryRow(query, imageName, "PENDING").Scan(&id)
    if err != nil {
        return 0, fmt.Errorf("storage.CreateBuild: %w", err)
    }

    return id, nil
}

// UpdateBuildStatus обновляет статус сборки по ID.
func (s *Storage) UpdateBuildStatus(id int64, status string) error {
    query := `UPDATE builds SET status = ? WHERE id = ?`

    _, err := s.db.Exec(query, status, id)
    if err != nil {
        return fmt.Errorf("storage.UpdateBuildStatus: %w", err)
    }

    return nil
}
