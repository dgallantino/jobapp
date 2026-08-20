package scrape

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"jobapp/internal/llm"
	"jobapp/internal/models"
)

// JobAd is a scraped listing result (before DB insert).
type JobAd struct {
	SourceURL   string
	Title       string
	Company     string
	Salary      string
	Description string
	PostedAt    *time.Time
}

// adapter scrapes a listing (or detail) URL into job ads.
type adapter interface {
	// Name must match the adapter column value in sources.
	Name() string
	// Scrape fetches the page at pageURL and returns discovered job ads.
	Scrape(ctx context.Context, pageURL string) ([]JobAd, error)
}

// Runner owns built-in adapters and runs crawl/ingest.
type Runner struct {
	byName map[string]adapter
}

// Options configures built-in adapters.
type Options struct {
	ScrapeConcurrency int         // max concurrent detail fetches per listing scrape
	ChromePath        string      // Chromium/Chrome binary for chromedp (Glints listing only)
	Limiter           Limiter     // optional per-host rate limiter for crawl outbound work
	LLM               *llm.Client // optional; used by the Threads adapter to fill empty fields
}

// CrawlResult summarizes one crawl pass.
type CrawlResult struct {
	Sources int
	NewAds  int
	Skipped int
	Errors  int
}

// New returns a runner with built-in adapters.
func New(opts Options) *Runner {
	if opts.ScrapeConcurrency < 1 {
		opts.ScrapeConcurrency = 1
	}
	client := NewClient(ClientOptions{
		Limiter:    opts.Limiter,
		ChromePath: opts.ChromePath,
	})
	var extractor fieldExtractor
	if opts.LLM != nil {
		extractor = opts.LLM
	}

	r := &Runner{byName: map[string]adapter{}}
	for _, a := range []adapter{
		newStaticAdapter(client),
		newJobstreetAdapter(client, opts.ScrapeConcurrency),
		newGlintsAdapter(client, opts.ScrapeConcurrency),
		newDeallsAdapter(client, opts.ScrapeConcurrency),
		newThreadsAdapter(client, extractor),
	} {
		r.byName[a.Name()] = a
	}
	return r
}

var _ fieldExtractor = (*llm.Client)(nil)

func (r *Runner) get(name string) (adapter, error) {
	a, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown adapter %q", name)
	}
	return a, nil
}

// resolve picks an adapter for a bare job URL (e.g. Telegram links).
// Matches known hostnames; falls back to static.
func (r *Runner) resolve(rawURL string) adapter {
	u, err := url.Parse(rawURL)
	if err != nil {
		return r.byName["static"]
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "jobstreet"):
		if a, ok := r.byName["jobstreet"]; ok {
			return a
		}
	case strings.Contains(host, "glints"):
		if a, ok := r.byName["glints"]; ok {
			return a
		}
	case strings.Contains(host, "dealls"):
		if a, ok := r.byName["dealls"]; ok {
			return a
		}
	case isThreadsHost(host):
		if a, ok := r.byName["threads"]; ok {
			return a
		}
	}
	return r.byName["static"]
}

// RunCrawl loads enabled sources, scrapes each, upserts job_ads.
func (r *Runner) RunCrawl(ctx context.Context, db *sql.DB) (CrawlResult, error) {
	sources, err := models.ListSources(ctx, db, true)
	if err != nil {
		return CrawlResult{}, err
	}

	var res CrawlResult
	res.Sources = len(sources)

	for _, src := range sources {
		if src.Adapter == "telegram" || src.Adapter == "threads" {
			// Telegram/Threads sources are markers for link-only ingest, not crawl targets.
			continue
		}
		adapter, err := r.get(src.Adapter)
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

// ScrapeAndStore scrapes a single URL via resolve and inserts into job_ads.
func (r *Runner) ScrapeAndStore(ctx context.Context, db *sql.DB, rawURL string, sourceID *int64) (models.JobAd, bool, error) {
	adapter := r.resolve(rawURL)
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
		static, err := r.get("static")
		if err != nil {
			return models.JobAd{}, false, err
		}
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
