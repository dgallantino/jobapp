package models

import (
	"context"
	"database/sql"
	"time"
)

// GetTelegramState returns the single telegram_state row (defaults to 0).
func GetTelegramState(ctx context.Context, db *sql.DB) (TelegramState, error) {
	var st TelegramState
	var updated string
	err := db.QueryRowContext(ctx,
		`SELECT last_update_id, updated_at FROM telegram_state WHERE id = 1`,
	).Scan(&st.LastUpdateID, &updated)
	if err == sql.ErrNoRows {
		return TelegramState{}, nil
	}
	if err != nil {
		return TelegramState{}, err
	}
	st.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	return st, nil
}

// SetTelegramLastUpdateID updates last_update_id for the singleton row.
func SetTelegramLastUpdateID(ctx context.Context, db *sql.DB, lastUpdateID int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO telegram_state (id, last_update_id, updated_at)
		VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			last_update_id = excluded.last_update_id,
			updated_at = excluded.updated_at`, lastUpdateID)
	return err
}
