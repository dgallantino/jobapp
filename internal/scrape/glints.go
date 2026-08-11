package scrape

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

const (
	glintsSalaryUndisclosed   = "Salary Undisclosed"
	glintsListingRenderTimeout = 45 * time.Second
	glintsJobCardSelector      = `[data-gtm-job-id]`
)

// glintsDetailPathRE matches Glints job detail paths such as:
// /opportunities/jobs/{slug}/{uuid}
// /id/opportunities/jobs/{slug}/{uuid}
// /id/en/opportunities/jobs/{slug}/{uuid}
var glintsDetailPathRE = regexp.MustCompile(`(?i)^(?:/[a-z]{2}){0,2}/opportunities/jobs/([^/]+)/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/?$`)

var glintsReservedJobSegments = map[string]struct{}{
	"explore":     {},
	"bookmarked":  {},
	"recommended": {},
	"id":          {},
}

// GlintsAdapter scrapes Glints listing and detail pages.
//
// Investigation notes (2026-08):
// Detail pages SSR job content (h1, data-gtm-company-name, JobOverview salary,
// JobDescriptionsc__DescriptionContainer) — plain HTTP + goquery is enough.
// Explore listing pages hydrate job cards client-side; GraphQL is login-gated /
// 403 for anonymous clients, so listing uses chromedp and scrapes whatever cards
// appear after hydration (no session cookies).
type GlintsAdapter struct {
	Client            *http.Client
	ScrapeConcurrency int    // max concurrent detail fetches per listing scrape
	ChromePath        string // optional Chromium/Chrome binary for chromedp
}

// NewGlintsAdapter returns a Glints adapter using the default HTTP client.
// scrapeConcurrency caps concurrent detail-page fetches (minimum 1).
// chromePath, when set, is passed to chromedp.ExecPath for listing renders.
func NewGlintsAdapter(scrapeConcurrency int, chromePath string) *GlintsAdapter {
	if scrapeConcurrency < 1 {
		scrapeConcurrency = 1
	}
	return &GlintsAdapter{
		Client:            DefaultHTTPClient(),
		ScrapeConcurrency: scrapeConcurrency,
		ChromePath:        strings.TrimSpace(chromePath),
	}
}

func (a *GlintsAdapter) Name() string { return "glints" }

func (a *GlintsAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	if isGlintsDetailURL(pageURL) {
		doc, finalURL, err := fetchDocument(ctx, a.Client, pageURL)
		if err != nil {
			return nil, fmt.Errorf("glints: %w", err)
		}
		ad, err := parseGlintsDetail(doc, finalURL)
		if err != nil {
			return nil, err
		}
		return []JobAd{ad}, nil
	}

	html, finalURL, err := a.renderListingPage(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("glints: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("glints: parse listing HTML: %w", err)
	}
	ads := parseGlintsListing(doc, finalURL)
	if len(ads) == 0 {
		return nil, fmt.Errorf("glints: no jobs found at %s", finalURL)
	}
	return a.enrichListingDetails(ctx, ads), nil
}

// renderListingPage navigates to an explore listing URL with headless Chromium
// and returns the rendered outer HTML plus the final location URL.
func (a *GlintsAdapter) renderListingPage(ctx context.Context, pageURL string) (html string, finalURL string, err error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, glintsListingRenderTimeout)
		defer cancel()
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.UserAgent(defaultUserAgent),
	)
	if a.ChromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(a.ChromePath))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer allocCancel()

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	var outerHTML, loc string
	err = chromedp.Run(tabCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady(glintsJobCardSelector, chromedp.ByQuery),
		chromedp.OuterHTML("html", &outerHTML, chromedp.ByQuery),
		chromedp.Location(&loc),
	)
	if err != nil {
		return "", "", fmt.Errorf("chromedp listing %s: %w", pageURL, err)
	}
	if loc == "" {
		loc = pageURL
	}
	return outerHTML, loc, nil
}

// enrichListingDetails fetches each listing stub's detail page concurrently
// and replaces stubs with full ads. On failure, keeps the listing-card fields.
func (a *GlintsAdapter) enrichListingDetails(ctx context.Context, ads []JobAd) []JobAd {
	if len(ads) == 0 {
		return ads
	}
	concurrency := a.ScrapeConcurrency
	if concurrency < 1 {
		concurrency = 1
	}

	out := make([]JobAd, len(ads))
	copy(out, ads)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, stub := range ads {
		if err := ctx.Err(); err != nil {
			break
		}
		wg.Add(1)
		go func(i int, stub JobAd) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			if err := ctx.Err(); err != nil {
				return
			}
			doc, finalURL, err := fetchDocument(ctx, a.Client, stub.SourceURL)
			if err != nil {
				log.Printf("glints: detail %s: %v", stub.SourceURL, err)
				return
			}
			ad, err := parseGlintsDetail(doc, finalURL)
			if err != nil {
				log.Printf("glints: detail %s: %v", stub.SourceURL, err)
				return
			}
			out[i] = ad
		}(i, stub)
	}
	wg.Wait()
	return out
}

func parseGlintsDetail(doc *goquery.Document, pageURL string) (JobAd, error) {
	root := doc.Selection
	title := normalizeGlintsText(root.Find("h1").First().Text())
	if title == "" {
		return JobAd{}, fmt.Errorf("glints: missing job title at %s", pageURL)
	}

	company := ""
	root.Find("[data-gtm-company-name]").Each(func(_ int, s *goquery.Selection) {
		if company != "" {
			return
		}
		if v, ok := s.Attr("data-gtm-company-name"); ok {
			v = normalizeGlintsText(v)
			if v != "" {
				company = v
			}
		}
	})

	salary := glintsSalaryFromDoc(root)
	if salary == "" {
		salary = glintsSalaryUndisclosed
	}

	desc := ""
	descNode := root.Find(`[class*="JobDescriptionsc__DescriptionContainer"]`).First()
	if descNode.Length() > 0 {
		desc = textWithBreaks(descNode)
	}

	return JobAd{
		SourceURL:   canonicalizeGlintsURL(pageURL),
		Title:       title,
		Company:     company,
		Salary:      salary,
		Description: desc,
	}, nil
}

func parseGlintsListing(doc *goquery.Document, pageURL string) []JobAd {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	var out []JobAd

	doc.Find(glintsJobCardSelector).Each(func(_ int, card *goquery.Selection) {
		href := glintsCardJobHref(card)
		if href == "" {
			return
		}
		abs, err := base.Parse(href)
		if err != nil {
			return
		}
		canonical := canonicalizeGlintsURL(abs.String())
		if canonical == "" || !isGlintsDetailURL(canonical) {
			return
		}
		if _, ok := seen[canonical]; ok {
			return
		}
		seen[canonical] = struct{}{}

		title := normalizeGlintsText(card.Find(`[class*="JobTitle"]`).First().Text())
		if title == "" {
			title = normalizeGlintsText(card.Find("a").First().Text())
		}
		if title == "" {
			title = canonical
		}

		company := ""
		if v, ok := card.Attr("data-gtm-company-name"); ok {
			company = normalizeGlintsText(v)
		}
		if company == "" {
			card.Find("[data-gtm-company-name]").Each(func(_ int, s *goquery.Selection) {
				if company != "" {
					return
				}
				if v, ok := s.Attr("data-gtm-company-name"); ok {
					company = normalizeGlintsText(v)
				}
			})
		}
		if company == "" {
			company = normalizeGlintsText(card.Find(`[data-cy="company_name_job_card"]`).First().Text())
		}

		salary := glintsSalaryFromDoc(card)
		if salary == "" {
			salary = glintsSalaryUndisclosed
		}

		out = append(out, JobAd{
			SourceURL: canonical,
			Title:     title,
			Company:   company,
			Salary:    salary,
		})
	})
	return out
}

func glintsCardJobHref(card *goquery.Selection) string {
	var href string
	card.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if href != "" {
			return
		}
		h, _ := a.Attr("href")
		h = strings.TrimSpace(h)
		if looksLikeGlintsJobPath(h) {
			href = h
		}
	})
	return href
}

func looksLikeGlintsJobPath(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	path := u.Path
	if path == "" {
		path = href
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
	}
	m := glintsDetailPathRE.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	slug := strings.ToLower(m[1])
	_, reserved := glintsReservedJobSegments[slug]
	return !reserved
}

func glintsSalaryFromDoc(root *goquery.Selection) string {
	var best string
	root.Find(`[class*="Salary"]`).Each(func(_ int, s *goquery.Selection) {
		text := normalizeGlintsText(s.Text())
		if !looksLikeGlintsSalary(text) {
			return
		}
		if best == "" || len(text) < len(best) {
			best = text
		}
	})
	if best != "" {
		return best
	}
	root.Find(`[class*="JobOverview"]`).Each(func(_ int, s *goquery.Selection) {
		text := normalizeGlintsText(s.Text())
		if !looksLikeGlintsSalary(text) {
			return
		}
		if best == "" || len(text) < len(best) {
			best = text
		}
	})
	return best
}

func looksLikeGlintsSalary(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "undisclosed") {
		return true
	}
	hasCurrency := strings.Contains(s, "Rp") ||
		strings.Contains(s, "IDR") ||
		strings.Contains(s, "SGD") ||
		strings.Contains(s, "USD") ||
		strings.Contains(s, "$")
	hasDigit := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			hasDigit = true
			break
		}
	}
	return hasCurrency && hasDigit
}

func normalizeGlintsText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return collapseWS(s)
}

func isGlintsDetailURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	m := glintsDetailPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return false
	}
	slug := strings.ToLower(m[1])
	if _, reserved := glintsReservedJobSegments[slug]; reserved {
		return false
	}
	return true
}

// canonicalizeGlintsURL drops query/fragment and trailing slash.
func canonicalizeGlintsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
