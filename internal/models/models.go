package models

import "time"

// Valid job ad statuses.
const (
	StatusNew      = "new"
	StatusApplied  = "applied"
	StatusRejected = "rejected"
	StatusIgnored  = "ignored"
)

// Source is a configured crawl target.
type Source struct {
	ID        int64
	Name      string
	URL       string
	Adapter   string
	Enabled   bool
	CreatedAt time.Time
}

// JobAd is a scraped job listing.
type JobAd struct {
	ID          int64
	SourceID    *int64
	SourceURL   string
	Title       string
	Company     string
	Description string
	PostedAt    *time.Time
	ScrapedAt   time.Time
	Status      string
	SourceName  string // joined, optional
}

// CoverLetter is a generated application letter for a job ad.
type CoverLetter struct {
	ID        int64
	JobAdID   int64
	Content   string
	Model     string
	CreatedAt time.Time
}

// ProfileEntry is one key/value row in the profile table.
type ProfileEntry struct {
	Key   string
	Value string
}

// TelegramState tracks short-poll progress.
type TelegramState struct {
	LastUpdateID int64
	UpdatedAt    time.Time
}
