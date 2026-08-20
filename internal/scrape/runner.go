package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"jobapp/internal/llm"
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
