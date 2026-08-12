package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
)

const (
	deallsSalaryNegotiable = "Negotiable"
	deallsSalaryUnpaid     = "Unpaid"
)

// deallsDetailPathRE matches /loker/{job-slug}~{company-slug}.
var deallsDetailPathRE = regexp.MustCompile(`(?i)^/loker/([^/~]+)~([^/?#]+)/?$`)

var deallsReservedJobSlugs = map[string]struct{}{
	"saved":   {},
	"applied": {},
}

// DeallsAdapter scrapes Dealls listing and detail pages.
//
// Investigation notes (2026-08):
// Dealls (dealls.com) is Next.js with React Query. First-page listing cards and
// detail content are SSR'd into HTML and __NEXT_DATA__ — plain HTTP + goquery
// is enough (no chromedp). Listing "Lebih Banyak" loads further pages via
// api.sejutacita.id; this adapter only scrapes the first page (~18 jobs).
//
// Detail path: /loker/{job-slug}~{company-slug}.
// Description: Deskripsi Pekerjaan (responsibilities) + Kualifikasi (requirements).
type DeallsAdapter struct {
	Client            *http.Client
	ScrapeConcurrency int
}

// NewDeallsAdapter returns a Dealls adapter using client (or DefaultHTTPClient if nil).
// scrapeConcurrency caps concurrent detail-page fetches (minimum 1).
func NewDeallsAdapter(client *http.Client, scrapeConcurrency int) *DeallsAdapter {
	if scrapeConcurrency < 1 {
		scrapeConcurrency = 1
	}
	if client == nil {
		client = DefaultHTTPClient()
	}
	return &DeallsAdapter{
		Client:            client,
		ScrapeConcurrency: scrapeConcurrency,
	}
}

func (a *DeallsAdapter) Name() string { return "dealls" }

func (a *DeallsAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	doc, finalURL, err := fetchDocument(ctx, a.Client, pageURL)
	if err != nil {
		return nil, fmt.Errorf("dealls: %w", err)
	}

	if isDeallsDetailURL(finalURL) {
		ad, err := parseDeallsDetail(doc, finalURL)
		if err != nil {
			return nil, err
		}
		return []JobAd{ad}, nil
	}

	ads := parseDeallsListing(doc, finalURL)
	if len(ads) == 0 {
		return nil, fmt.Errorf("dealls: no jobs found at %s", finalURL)
	}
	return a.enrichListingDetails(ctx, ads), nil
}

// enrichListingDetails fetches each listing stub's detail page concurrently
// and replaces stubs with full ads. On failure, keeps the listing-card fields.
func (a *DeallsAdapter) enrichListingDetails(ctx context.Context, ads []JobAd) []JobAd {
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
				log.Printf("dealls: detail %s: %v", stub.SourceURL, err)
				return
			}
			ad, err := parseDeallsDetail(doc, finalURL)
			if err != nil {
				log.Printf("dealls: detail %s: %v", stub.SourceURL, err)
				return
			}
			out[i] = ad
		}(i, stub)
	}
	wg.Wait()
	return out
}

func parseDeallsDetail(doc *goquery.Document, pageURL string) (JobAd, error) {
	if job, ok := deallsJobFromNextData(doc); ok {
		title := normalizeDeallsText(job.Role)
		if title == "" {
			return JobAd{}, fmt.Errorf("dealls: missing job title at %s", pageURL)
		}
		company := ""
		if job.Company != nil {
			company = normalizeDeallsText(job.Company.Name)
		}
		return JobAd{
			SourceURL:   canonicalizeDeallsURL(pageURL),
			Title:       title,
			Company:     company,
			Salary:      formatDeallsSalary(job.SalaryType, job.SalaryRange),
			Description: deallsDescriptionFromHTML(job.Responsibilities, job.Requirements),
		}, nil
	}

	root := doc.Selection
	title := normalizeDeallsText(root.Find("h1").First().Text())
	if title == "" {
		return JobAd{}, fmt.Errorf("dealls: missing job title at %s", pageURL)
	}

	company := ""
	root.Find(`a[href*="/karir/"]`).Each(func(_ int, s *goquery.Selection) {
		if company != "" {
			return
		}
		company = normalizeDeallsText(s.Text())
	})
	if company == "" {
		company = normalizeDeallsText(root.Find("h2").First().Text())
	}

	salary := deallsSalaryFromDOM(root)
	if salary == "" {
		salary = deallsSalaryNegotiable
	}

	return JobAd{
		SourceURL:   canonicalizeDeallsURL(pageURL),
		Title:       title,
		Company:     company,
		Salary:      salary,
		Description: deallsDescriptionFromDOM(root),
	}, nil
}

func parseDeallsListing(doc *goquery.Document, pageURL string) []JobAd {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	if ads := parseDeallsListingFromNextData(doc, base); len(ads) > 0 {
		return ads
	}
	return parseDeallsListingFromDOM(doc, base)
}

func parseDeallsListingFromNextData(doc *goquery.Document, base *url.URL) []JobAd {
	docs, ok := deallsListingDocsFromNextData(doc)
	if !ok {
		return nil
	}

	seen := map[string]struct{}{}
	var out []JobAd
	for _, job := range docs {
		href := deallsJobHref(job.Slug, job.Company)
		if href == "" {
			continue
		}
		abs, err := base.Parse(href)
		if err != nil {
			continue
		}
		canonical := canonicalizeDeallsURL(abs.String())
		if canonical == "" || !isDeallsDetailURL(canonical) {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}

		title := normalizeDeallsText(job.Role)
		if title == "" {
			title = canonical
		}
		company := ""
		if job.Company != nil {
			company = normalizeDeallsText(job.Company.Name)
		}

		out = append(out, JobAd{
			SourceURL: canonical,
			Title:     title,
			Company:   company,
			Salary:    formatDeallsSalary(job.SalaryType, job.SalaryRange),
		})
	}
	return out
}

func parseDeallsListingFromDOM(doc *goquery.Document, base *url.URL) []JobAd {
	seen := map[string]struct{}{}
	var out []JobAd

	doc.Find(`#jobs-container a[href*="/loker/"]`).Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" || !looksLikeDeallsJobPath(href) {
			return
		}
		abs, err := base.Parse(href)
		if err != nil {
			return
		}
		canonical := canonicalizeDeallsURL(abs.String())
		if canonical == "" || !isDeallsDetailURL(canonical) {
			return
		}
		if _, ok := seen[canonical]; ok {
			return
		}
		seen[canonical] = struct{}{}

		title := normalizeDeallsText(a.Find("h2, h3, [class*='JobTitle'], [class*='job-title']").First().Text())
		if title == "" {
			// Card link text is noisy; prefer first substantial line if present.
			text := normalizeDeallsText(a.Text())
			if text != "" {
				title = text
			} else {
				title = canonical
			}
		}

		out = append(out, JobAd{
			SourceURL: canonical,
			Title:     title,
			Salary:    deallsSalaryNegotiable,
		})
	})
	return out
}

func deallsJobHref(jobSlug string, company *deallsCompanyJSON) string {
	jobSlug = strings.TrimSpace(jobSlug)
	if jobSlug == "" || company == nil {
		return ""
	}
	companySlug := strings.TrimSpace(company.Slug)
	if companySlug == "" {
		return ""
	}
	return "/loker/" + jobSlug + "~" + companySlug
}

// --- __NEXT_DATA__ helpers ---

type deallsNextData struct {
	Props struct {
		PageProps struct {
			DehydratedState struct {
				Queries []deallsRQQuery `json:"queries"`
			} `json:"dehydratedState"`
		} `json:"pageProps"`
	} `json:"props"`
}

type deallsRQQuery struct {
	QueryKey []any          `json:"queryKey"`
	State    deallsRQState  `json:"state"`
}

type deallsRQState struct {
	Data json.RawMessage `json:"data"`
}

type deallsJobJSON struct {
	Role             string             `json:"role"`
	Slug             string             `json:"slug"`
	SalaryType       string             `json:"salaryType"`
	SalaryRange      *deallsSalaryRange `json:"salaryRange"`
	Responsibilities string             `json:"responsibilities"`
	Requirements     string             `json:"requirements"`
	Company          *deallsCompanyJSON `json:"company"`
}

type deallsCompanyJSON struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type deallsSalaryRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type deallsListingPages struct {
	Pages []struct {
		Docs []deallsJobJSON `json:"docs"`
	} `json:"pages"`
}

func deallsNextDataRaw(doc *goquery.Document) ([]byte, bool) {
	raw := strings.TrimSpace(doc.Find(`script#__NEXT_DATA__`).First().Text())
	if raw == "" {
		return nil, false
	}
	return []byte(raw), true
}

func deallsJobFromNextData(doc *goquery.Document) (deallsJobJSON, bool) {
	raw, ok := deallsNextDataRaw(doc)
	if !ok {
		return deallsJobJSON{}, false
	}
	var next deallsNextData
	if err := json.Unmarshal(raw, &next); err != nil {
		return deallsJobJSON{}, false
	}
	for _, q := range next.Props.PageProps.DehydratedState.Queries {
		if !deallsQueryKeyHas(q.QueryKey, "/v1/job-portal/job/slug") {
			continue
		}
		var job deallsJobJSON
		if err := json.Unmarshal(q.State.Data, &job); err != nil {
			continue
		}
		if strings.TrimSpace(job.Role) != "" {
			return job, true
		}
	}
	return deallsJobJSON{}, false
}

func deallsListingDocsFromNextData(doc *goquery.Document) ([]deallsJobJSON, bool) {
	raw, ok := deallsNextDataRaw(doc)
	if !ok {
		return nil, false
	}
	var next deallsNextData
	if err := json.Unmarshal(raw, &next); err != nil {
		return nil, false
	}
	for _, q := range next.Props.PageProps.DehydratedState.Queries {
		if !deallsQueryKeyHas(q.QueryKey, "/v1/explore-job/job") {
			continue
		}
		var pages deallsListingPages
		if err := json.Unmarshal(q.State.Data, &pages); err != nil {
			continue
		}
		if len(pages.Pages) == 0 || len(pages.Pages[0].Docs) == 0 {
			continue
		}
		return pages.Pages[0].Docs, true
	}
	return nil, false
}

func deallsQueryKeyHas(key []any, needle string) bool {
	if len(key) == 0 {
		return false
	}
	s, ok := key[0].(string)
	return ok && strings.Contains(s, needle)
}

// --- description / salary ---

func deallsDescriptionFromHTML(responsibilities, requirements string) string {
	var parts []string
	if s := deallsSectionText("Deskripsi Pekerjaan", responsibilities); s != "" {
		parts = append(parts, s)
	}
	if s := deallsSectionText("Kualifikasi", requirements); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

func deallsSectionText(heading, htmlFrag string) string {
	htmlFrag = strings.TrimSpace(htmlFrag)
	if htmlFrag == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div>" + htmlFrag + "</div>"))
	if err != nil {
		return ""
	}
	body := textWithBreaks(doc.Find("div").First())
	if body == "" {
		return ""
	}
	return heading + "\n" + body
}

func deallsDescriptionFromDOM(root *goquery.Selection) string {
	var responsibilities, requirements string
	root.Find("h3").Each(func(_ int, h *goquery.Selection) {
		label := normalizeDeallsText(h.Text())
		content := h.Next()
		if content.Length() == 0 {
			parent := h.Parent()
			if parent.Length() > 0 {
				content = parent.Children().Not("h3").First()
			}
		}
		html, err := content.Html()
		if err != nil || strings.TrimSpace(html) == "" {
			return
		}
		switch {
		case strings.EqualFold(label, "Deskripsi Pekerjaan"):
			responsibilities = html
		case strings.EqualFold(label, "Kualifikasi"):
			requirements = html
		}
	})
	return deallsDescriptionFromHTML(responsibilities, requirements)
}

func formatDeallsSalary(salaryType string, rng *deallsSalaryRange) string {
	if strings.EqualFold(strings.TrimSpace(salaryType), "unpaid") {
		return deallsSalaryUnpaid
	}
	if rng == nil || (rng.Start <= 0 && rng.End <= 0) {
		return deallsSalaryNegotiable
	}
	start := formatDeallsIDR(rng.Start)
	end := formatDeallsIDR(rng.End)
	if rng.Start > 0 && rng.End > 0 && rng.Start != rng.End {
		return start + " – " + end
	}
	if rng.Start > 0 {
		return start
	}
	return end
}

func formatDeallsIDR(n int64) string {
	if n < 0 {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	b.WriteString("Rp")
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func deallsSalaryFromDOM(root *goquery.Selection) string {
	var best string
	root.Find("span, li, p, div").Each(func(_ int, s *goquery.Selection) {
		if s.Children().Length() > 0 {
			return
		}
		text := normalizeDeallsText(s.Text())
		lower := strings.ToLower(text)
		switch {
		case strings.EqualFold(text, deallsSalaryUnpaid):
			best = deallsSalaryUnpaid
		case strings.EqualFold(text, deallsSalaryNegotiable):
			if best == "" {
				best = deallsSalaryNegotiable
			}
		case strings.Contains(text, "Rp") && strings.ContainsAny(text, "0123456789"):
			if best == "" || (strings.Contains(best, "Rp") && len(text) < len(best)) || !strings.Contains(best, "Rp") {
				best = text
			}
		case strings.Contains(lower, "negotiable"):
			if best == "" {
				best = deallsSalaryNegotiable
			}
		}
	})
	return best
}

func normalizeDeallsText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return collapseWS(s)
}

func isDeallsDetailURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return looksLikeDeallsJobPath(u.Path)
}

func looksLikeDeallsJobPath(href string) bool {
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
	m := deallsDetailPathRE.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	jobSlug := strings.ToLower(m[1])
	if _, reserved := deallsReservedJobSlugs[jobSlug]; reserved {
		return false
	}
	return true
}

// canonicalizeDeallsURL drops query/fragment and trailing slash.
func canonicalizeDeallsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
