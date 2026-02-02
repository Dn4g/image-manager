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
        vm_id TEXT,  -- Добавили колонку для связки VM и Сборки
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        logs TEXT DEFAULT ''
    );
    `
	_, err := s.db.Exec(query)
	if err != nil {
		return fmt.Errorf("storage.Init: %w", err)
	}

	return nil
}

// ... (CreateBuild, SetVMID без изменений)

// AppendLog добавляет строку в лог сборки
func (s *Storage) AppendLog(id int64, text string) error {
	query := `UPDATE builds SET logs = coalesce(logs, '') || ? || char(10) WHERE id = ?`
	_, err := s.db.Exec(query, text, id)
	return err
}

// GetBuilds возвращает список последних сборок (для истории).
func (s *Storage) GetBuilds() ([]map[string]any, error) {
	query := `SELECT id, image_name, status, created_at FROM builds ORDER BY id DESC LIMIT 50`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id int64
		var name, status, created string
		if err := rows.Scan(&id, &name, &status, &created); err != nil {
			continue
		}
		result = append(result, map[string]any{
			"id":         id,
			"image_name": name,
			"status":     status,
			"created_at": created,
		})
	}
	return result, nil
}

// GetBuildStatus возвращает текущий статус сборки и логи.
func (s *Storage) GetBuildStatus(id int64) (string, string, error) {
	query := `SELECT status, coalesce(logs, '') FROM builds WHERE id = ?`
	var status, logs string
	err := s.db.QueryRow(query, id).Scan(&status, &logs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", fmt.Errorf("build not found")
		}
		return "", "", fmt.Errorf("storage.GetBuildStatus: %w", err)
	}
	return status, logs, nil
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

// SetVMID привязывает VM к сборке.
func (s *Storage) SetVMID(id int64, vmID string) error {
	query := `UPDATE builds SET vm_id = ? WHERE id = ?`
	_, err := s.db.Exec(query, vmID, id)
	return err
}

// UpdateBuildStatusByVMID обновляет статус сборки, зная ID виртуалки.
func (s *Storage) UpdateBuildStatusByVMID(vmID string, status string) error {
	query := `UPDATE builds SET status = ? WHERE vm_id = ?`
	res, err := s.db.Exec(query, status, vmID)
	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("no build found with vm_id=%s", vmID)
	}
	return nil
}
