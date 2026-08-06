package scrape

import (
	"context"
	"fmt"
)

// GlintsAdapter scrapes Glints listing/detail pages.
//
// Investigation notes (STUB — revisit before production selectors):
// Glints job boards typically hydrate listings via client-side API calls
// (often REST under `/api/` or similar). Exact endpoints, pagination, and
// whether responses are usable without browser cookies were not confirmed
// here (no live Network capture against glints.com during implementation).
//
// Prefer a JSON endpoint if one is stable and unauthenticated (or cookie-light).
// Only add chromedp for this adapter if content is genuinely JS-only and no
// JSON path works — keep chromedp off the critical path for static sites.
//
// Current behavior: delegates to StaticAdapter heuristics.
type GlintsAdapter struct {
	fallback *StaticAdapter
}

// NewGlintsAdapter returns a stub Glints adapter.
func NewGlintsAdapter() *GlintsAdapter {
	return &GlintsAdapter{fallback: NewStaticAdapter()}
}

func (a *GlintsAdapter) Name() string { return "glints" }

func (a *GlintsAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	ads, err := a.fallback.Scrape(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("glints (static fallback): %w", err)
	}
	return ads, nil
}
