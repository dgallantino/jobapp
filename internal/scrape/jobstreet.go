package scrape

import (
	"context"
	"fmt"
)

// JobstreetAdapter scrapes JobStreet listing/detail pages.
//
// Investigation notes (STUB — revisit before production selectors):
// JobStreet (Seek Asia) listing pages are heavily JS-driven. In browser Network
// tabs, listing UIs commonly call JSON endpoints under paths resembling
// `/api/` or GraphQL rather than shipping full HTML job cards server-side.
// Exact endpoint URLs, query params, and auth/cookie requirements were not
// confirmed in this environment (no live Network capture against jobstreet.com
// /sg.jobstreet.com during implementation).
//
// If a stable public JSON/XHR endpoint is identified, prefer that over HTML
// scraping. If content is only available after JS execution and no JSON API
// works, fall back to chromedp for this adapter only (host Chromium; do not
// pull chromedp in as a blanket dependency for other adapters).
//
// Current behavior: delegates to StaticAdapter heuristics so crawl/telegram
// keep working; replace with real selectors or API client once investigated.
type JobstreetAdapter struct {
	fallback *StaticAdapter
}

// NewJobstreetAdapter returns a stub JobStreet adapter.
func NewJobstreetAdapter() *JobstreetAdapter {
	return &JobstreetAdapter{fallback: NewStaticAdapter()}
}

func (a *JobstreetAdapter) Name() string { return "jobstreet" }

func (a *JobstreetAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	ads, err := a.fallback.Scrape(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("jobstreet (static fallback): %w", err)
	}
	return ads, nil
}
