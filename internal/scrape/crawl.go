package scrape

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"jobapp/internal/models"
)

// CrawlResult summarizes one crawl pass.
type CrawlResult struct {
	Sources int
	NewAds  int
	Skipped int
	Errors  int
}

// RunCrawl loads enabled sources, scrapes each, upserts job_ads.
func RunCrawl(ctx context.Context, db *sql.DB, reg *Registry) (CrawlResult, error) {
	sources, err := models.ListSources(ctx, db, true)
	if err != nil {
		return CrawlResult{}, err
	}

	var res CrawlResult
	res.Sources = len(sources)

	for _, src := range sources {
		if src.Adapter == "telegram" {
			// Telegram source is a marker for ads ingested via telegram-check, not a crawl target.
			continue
		}
		adapter, err := reg.Get(src.Adapter)
		if err != nil {
			log.Printf("source %q (%d): %v", src.Name, src.ID, err)
			res.Errors++
			continue
		}
		ads, err := adapter.Scrape(ctx, src.URL)
		if err != nil {
			log.Printf("source %q (%d) scrape error: %v", src.Name, src.ID, err)
			res.Errors++
			continue
		}
		sid := src.ID
		for _, ad := range ads {
			m := models.JobAd{
				SourceID:    &sid,
				SourceURL:   ad.SourceURL,
				Title:       ad.Title,
				Company:     ad.Company,
				Salary:      ad.Salary,
				Description: ad.Description,
				PostedAt:    ad.PostedAt,
				Status:      models.StatusNew,
			}
			_, inserted, err := models.InsertJobAdIfNew(ctx, db, m)
			if err != nil {
				log.Printf("source %q insert %s: %v", src.Name, ad.SourceURL, err)
				res.Errors++
				continue
			}
			if inserted {
				res.NewAds++
			} else {
				res.Skipped++
			}
		}
		log.Printf("source %q: scraped %d ads", src.Name, len(ads))
	}

	log.Printf("crawl summary: sources=%d new=%d skipped=%d errors=%d",
		res.Sources, res.NewAds, res.Skipped, res.Errors)
	return res, nil
}

// ScrapeAndStore scrapes a single URL via Resolve and inserts into job_ads.
func ScrapeAndStore(ctx context.Context, db *sql.DB, reg *Registry, rawURL string, sourceID *int64) (models.JobAd, bool, error) {
	adapter := reg.Resolve(rawURL)
	ads, err := adapter.Scrape(ctx, rawURL)
	if err != nil {
		return models.JobAd{}, false, err
	}
	if len(ads) == 0 {
		return models.JobAd{}, false, fmt.Errorf("no job data extracted from %s", rawURL)
	}
	// Prefer the ad matching the requested URL; otherwise take the first.
	picked := ads[0]
	for _, ad := range ads {
		if ad.SourceURL == rawURL {
			picked = ad
			break
		}
	}
	// If listing returned many and none match, scrape as detail via static.
	if picked.SourceURL != rawURL && picked.Description == "" {
		static := NewStaticAdapter(nil)
		detail, err := static.Scrape(ctx, rawURL)
		if err != nil {
			return models.JobAd{}, false, err
		}
		if len(detail) > 0 {
			picked = detail[0]
			picked.SourceURL = rawURL
		}
	}

	m := models.JobAd{
		SourceID:    sourceID,
		SourceURL:   picked.SourceURL,
		Title:       picked.Title,
		Company:     picked.Company,
		Salary:      picked.Salary,
		Description: picked.Description,
		PostedAt:    picked.PostedAt,
		Status:      models.StatusNew,
	}
	id, inserted, err := models.InsertJobAdIfNew(ctx, db, m)
	if err != nil {
		return models.JobAd{}, false, err
	}
	stored, err := models.GetJobAd(ctx, db, id)
	return stored, inserted, err
}
