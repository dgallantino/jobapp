package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ListSources returns all sources, optionally only enabled ones.
func ListSources(ctx context.Context, db *sql.DB, enabledOnly bool) ([]Source, error) {
	q := `SELECT id, name, url, adapter, enabled, created_at FROM sources`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY id`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Source
	for rows.Next() {
		var s Source
		var enabled int
		var created string
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Adapter, &enabled, &created); err != nil {
			return nil, err
		}
		s.Enabled = enabled != 0
		s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSource returns a source by id.
func GetSource(ctx context.Context, db *sql.DB, id int64) (Source, error) {
	var s Source
	var enabled int
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, name, url, adapter, enabled, created_at FROM sources WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.URL, &s.Adapter, &enabled, &created)
	if err != nil {
		return Source{}, err
	}
	s.Enabled = enabled != 0
	s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	return s, nil
}

// CreateSource inserts a new source.
func CreateSource(ctx context.Context, db *sql.DB, name, url, adapter string, enabled bool) (int64, error) {
	en := 0
	if enabled {
		en = 1
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO sources (name, url, adapter, enabled) VALUES (?, ?, ?, ?)`,
		name, url, adapter, en,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSource updates an existing source.
func UpdateSource(ctx context.Context, db *sql.DB, id int64, name, url, adapter string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	res, err := db.ExecContext(ctx,
		`UPDATE sources SET name = ?, url = ?, adapter = ?, enabled = ? WHERE id = ?`,
		name, url, adapter, en, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("source %d not found", id)
	}
	return nil
}

// DeleteSource removes a source by id.
func DeleteSource(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	return err
}

// EnsureTelegramSource returns the id of a sources row with adapter=telegram, creating one if needed.
// Assumption: Telegram-discovered ads use a dedicated source row rather than nullable source_id.
func EnsureTelegramSource(ctx context.Context, db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM sources WHERE adapter = 'telegram' LIMIT 1`,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	return CreateSource(ctx, db, "Telegram", "telegram://group", "telegram", true)
}
