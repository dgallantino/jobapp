package models

import (
	"context"
	"database/sql"
)

// ListProfile returns all profile key/value pairs ordered by key.
func ListProfile(ctx context.Context, db *sql.DB) ([]ProfileEntry, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM profile ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProfileEntry
	for rows.Next() {
		var e ProfileEntry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ProfileMap returns profile as a map.
func ProfileMap(ctx context.Context, db *sql.DB) (map[string]string, error) {
	entries, err := ListProfile(ctx, db)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Key] = e.Value
	}
	return m, nil
}

// UpsertProfile sets multiple profile keys.
func UpsertProfile(ctx context.Context, db *sql.DB, values map[string]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO profile (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range values {
		if _, err := stmt.ExecContext(ctx, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
