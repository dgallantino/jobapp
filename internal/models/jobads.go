package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ListJobAds returns job ads, optionally filtered by status, newest first.
func ListJobAds(ctx context.Context, db *sql.DB, status string) ([]JobAd, error) {
	q := `
		SELECT j.id, j.source_id, j.source_url, j.title, j.company, j.salary, j.description,
		       j.posted_at, j.scraped_at, j.status, COALESCE(s.name, '')
		FROM job_ads j
		LEFT JOIN sources s ON s.id = j.source_id`
	args := []any{}
	if status != "" {
		q += ` WHERE j.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY j.scraped_at DESC, j.id DESC`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []JobAd
	for rows.Next() {
		ad, err := scanJobAd(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ad)
	}
	return out, rows.Err()
}

// GetJobAd returns a single job ad by id.
func GetJobAd(ctx context.Context, db *sql.DB, id int64) (JobAd, error) {
	row := db.QueryRowContext(ctx, `
		SELECT j.id, j.source_id, j.source_url, j.title, j.company, j.salary, j.description,
		       j.posted_at, j.scraped_at, j.status, COALESCE(s.name, '')
		FROM job_ads j
		LEFT JOIN sources s ON s.id = j.source_id
		WHERE j.id = ?`, id)
	return scanJobAd(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanJobAd(row scannable) (JobAd, error) {
	var ad JobAd
	var sourceID sql.NullInt64
	var postedAt sql.NullString
	var scrapedAt string
	err := row.Scan(
		&ad.ID, &sourceID, &ad.SourceURL, &ad.Title, &ad.Company, &ad.Salary, &ad.Description,
		&postedAt, &scrapedAt, &ad.Status, &ad.SourceName,
	)
	if err != nil {
		return JobAd{}, err
	}
	if sourceID.Valid {
		id := sourceID.Int64
		ad.SourceID = &id
	}
	if postedAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", postedAt.String)
		if err == nil {
			ad.PostedAt = &t
		}
	}
	ad.ScrapedAt, _ = time.Parse("2006-01-02 15:04:05", scrapedAt)
	return ad, nil
}

// InsertJobAdIfNew inserts a job ad; returns (id, inserted, error).
// On UNIQUE(source_url) conflict, returns existing id and inserted=false.
func InsertJobAdIfNew(ctx context.Context, db *sql.DB, ad JobAd) (id int64, inserted bool, err error) {
	var sourceID any
	if ad.SourceID != nil {
		sourceID = *ad.SourceID
	}
	var postedAt any
	if ad.PostedAt != nil {
		postedAt = ad.PostedAt.UTC().Format("2006-01-02 15:04:05")
	}
	if ad.Status == "" {
		ad.Status = StatusNew
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO job_ads (source_id, source_url, title, company, salary, description, posted_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_url) DO NOTHING`,
		sourceID, ad.SourceURL, ad.Title, ad.Company, ad.Salary, ad.Description, postedAt, ad.Status,
	)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n > 0 {
		id, err = res.LastInsertId()
		return id, true, err
	}
	err = db.QueryRowContext(ctx, `SELECT id FROM job_ads WHERE source_url = ?`, ad.SourceURL).Scan(&id)
	return id, false, err
}

// UpdateJobAdStatus sets the status of a job ad.
func UpdateJobAdStatus(ctx context.Context, db *sql.DB, id int64, status string) error {
	switch status {
	case StatusNew, StatusApplied, StatusRejected, StatusIgnored:
	default:
		return fmt.Errorf("invalid status %q", status)
	}
	res, err := db.ExecContext(ctx, `UPDATE job_ads SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("job ad %d not found", id)
	}
	return nil
}
