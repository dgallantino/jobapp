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

// Adapter scrapes a listing (or detail) URL into job ads.
type Adapter interface {
	// Name must match the adapter column value in sources.
	Name() string
	// Scrape fetches the page at pageURL and returns discovered job ads.
	Scrape(ctx context.Context, pageURL string) ([]JobAd, error)
}

// Registry maps adapter name -> implementation.
type Registry struct {
	byName map[string]Adapter
}

// RegistryOptions configures built-in adapters.
type RegistryOptions struct {
	ScrapeConcurrency int         // max concurrent detail fetches per listing scrape
	ChromePath        string      // Chromium/Chrome binary for chromedp (Glints listing only)
	Limiter           Limiter     // optional per-host rate limiter for crawl outbound work
	LLM               *llm.Client // optional; used by the Threads adapter to fill empty fields
}

// NewRegistry returns a registry with built-in adapters.
func NewRegistry(opts RegistryOptions) *Registry {
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

	r := &Registry{byName: map[string]Adapter{}}
	for _, a := range []Adapter{
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

// Get returns an adapter by name.
func (r *Registry) Get(name string) (Adapter, error) {
	a, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown adapter %q", name)
	}
	return a, nil
}

// Resolve picks an adapter for a bare job URL (e.g. Telegram links).
// Matches known hostnames; falls back to static.
func (r *Registry) Resolve(rawURL string) Adapter {
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
