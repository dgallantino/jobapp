package models

import (
	"context"
	"database/sql"
	"time"
)

// ListCoverLetters returns cover letters for a job ad, newest first.
func ListCoverLetters(ctx context.Context, db *sql.DB, jobAdID int64) ([]CoverLetter, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_ad_id, content, model, created_at
		FROM cover_letters WHERE job_ad_id = ?
		ORDER BY created_at DESC, id DESC`, jobAdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CoverLetter
	for rows.Next() {
		var c CoverLetter
		var created string
		if err := rows.Scan(&c.ID, &c.JobAdID, &c.Content, &c.Model, &created); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertCoverLetter stores a generated letter.
func InsertCoverLetter(ctx context.Context, db *sql.DB, jobAdID int64, content, model string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO cover_letters (job_ad_id, content, model) VALUES (?, ?, ?)`,
		jobAdID, content, model,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
