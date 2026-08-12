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
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const jobstreetSalaryUndisclosed = "Salary Undisclosed"

// jobDetailPathRE matches Seek/JobStreet detail paths:
// /job/{id} and locale-prefixed /id/job/{id} (and similar country codes).
var jobDetailPathRE = regexp.MustCompile(`(?i)^(?:/[a-z]{2})?/job/(\d+)/?$`)

// JobstreetAdapter scrapes JobStreet listing and detail pages.
//
// Investigation notes (2026-08):
// JobStreet (Seek Asia) id.jobstreet.com SSR HTML includes the job payload via
// stable data-automation attributes — no GraphQL/XHR client or chromedp needed
// for detail or search listing pages in this environment.
//
// Detail: job-detail-title, advertiser-name, job-detail-salary, jobAdDetails.
// Listing cards: normalJob wrappers with jobTitle, jobCompany, jobSalary and
// job-list-view-job-link hrefs under /id/job/{id}.
//
// Prefer these selectors over StaticAdapter heuristics (og:site_name is the
// board name "Jobstreet Indonesia", not the employer).
type JobstreetAdapter struct {
	Client            *http.Client
	ScrapeConcurrency int // max concurrent detail fetches per listing scrape
}

// NewJobstreetAdapter returns a JobStreet adapter using client (or DefaultHTTPClient if nil).
// scrapeConcurrency caps concurrent detail-page fetches (minimum 1).
func NewJobstreetAdapter(client *http.Client, scrapeConcurrency int) *JobstreetAdapter {
	if scrapeConcurrency < 1 {
		scrapeConcurrency = 1
	}
	if client == nil {
		client = DefaultHTTPClient()
	}
	return &JobstreetAdapter{
		Client:            client,
		ScrapeConcurrency: scrapeConcurrency,
	}
}

func (a *JobstreetAdapter) Name() string { return "jobstreet" }

func (a *JobstreetAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	doc, finalURL, err := fetchDocument(ctx, a.Client, pageURL)
	if err != nil {
		return nil, fmt.Errorf("jobstreet: %w", err)
	}

	if isJobstreetDetailURL(finalURL) {
		ad, err := parseJobstreetDetail(doc, finalURL)
		if err != nil {
			return nil, err
		}
		return []JobAd{ad}, nil
	}

	ads := parseJobstreetListing(doc, finalURL)
	if len(ads) == 0 {
		return nil, fmt.Errorf("jobstreet: no jobs found at %s", finalURL)
	}
	return a.enrichListingDetails(ctx, ads), nil
}

// enrichListingDetails fetches each listing card's detail page concurrently
// and replaces stubs with full ads. On failure, keeps the listing-card fields.
func (a *JobstreetAdapter) enrichListingDetails(ctx context.Context, ads []JobAd) []JobAd {
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
				log.Printf("jobstreet: detail %s: %v", stub.SourceURL, err)
				return
			}
			ad, err := parseJobstreetDetail(doc, finalURL)
			if err != nil {
				log.Printf("jobstreet: detail %s: %v", stub.SourceURL, err)
				return
			}
			out[i] = ad
		}(i, stub)
	}
	wg.Wait()
	return out
}

func parseJobstreetDetail(doc *goquery.Document, pageURL string) (JobAd, error) {
	root := doc.Selection
	title := textByAutomation(root, "job-detail-title")
	if title == "" {
		return JobAd{}, fmt.Errorf("jobstreet: missing job title at %s", pageURL)
	}
	company := textByAutomation(root, "advertiser-name")
	salary := textByAutomation(root, "job-detail-salary")
	if salary == "" {
		salary = jobstreetSalaryUndisclosed
	}
	desc := descriptionByAutomation(root, "jobAdDetails")

	return JobAd{
		SourceURL:   canonicalizeJobstreetURL(pageURL),
		Title:       title,
		Company:     company,
		Salary:      salary,
		Description: desc,
	}, nil
}

func parseJobstreetListing(doc *goquery.Document, pageURL string) []JobAd {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	var out []JobAd

	doc.Find(`[data-automation="normalJob"]`).Each(func(_ int, card *goquery.Selection) {
		href := ""
		card.Find(`a[data-automation="job-list-view-job-link"]`).Each(func(_ int, a *goquery.Selection) {
			if href != "" {
				return
			}
			if h, ok := a.Attr("href"); ok {
				href = strings.TrimSpace(h)
			}
		})
		if href == "" {
			card.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
				if href != "" {
					return
				}
				h, _ := a.Attr("href")
				h = strings.TrimSpace(h)
				if looksLikeJobstreetJobPath(h) {
					href = h
				}
			})
		}
		if href == "" {
			return
		}

		abs, err := base.Parse(href)
		if err != nil {
			return
		}
		canonical := canonicalizeJobstreetURL(abs.String())
		if canonical == "" {
			return
		}
		if _, ok := seen[canonical]; ok {
			return
		}
		seen[canonical] = struct{}{}

		title := textByAutomation(card, "jobTitle")
		if title == "" {
			title = collapseWS(card.Find("a").First().Text())
		}
		if title == "" {
			title = canonical
		}
		company := textByAutomation(card, "jobCompany")
		salary := textByAutomation(card, "jobSalary")
		if salary == "" {
			salary = jobstreetSalaryUndisclosed
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

func textByAutomation(root *goquery.Selection, name string) string {
	sel := fmt.Sprintf(`[data-automation="%s"]`, name)
	node := root.Find(sel).First()
	if node.Length() == 0 {
		return ""
	}
	return normalizeJobstreetText(node.Text())
}

func descriptionByAutomation(root *goquery.Selection, name string) string {
	sel := fmt.Sprintf(`[data-automation="%s"]`, name)
	node := root.Find(sel).First()
	if node.Length() == 0 {
		return ""
	}
	return textWithBreaks(node)
}

func normalizeJobstreetText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return collapseWS(s)
}

// textWithBreaks extracts visible text while preserving structure from
// <br> and common block elements (unlike Selection.Text + collapseWS).
func textWithBreaks(sel *goquery.Selection) string {
	var b strings.Builder
	for _, n := range sel.Nodes {
		writeHTMLTextWithBreaks(&b, n)
	}
	return cleanupStructuredText(b.String())
}

func writeHTMLTextWithBreaks(b *strings.Builder, n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		// Keep "• text" on one line when <li> wraps content in <p>/<div>
		// and the markup has leading whitespace after the bullet.
		if hasOpenBulletPrefix(b) && strings.TrimSpace(n.Data) == "" {
			return
		}
		b.WriteString(n.Data)
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		switch tag {
		case "script", "style", "noscript":
			return
		case "br":
			b.WriteByte('\n')
			return
		}
		if tag == "li" {
			ensureTrailingNewline(b)
			// Prefix a bullet only when the list item text does not already start with one.
			if !listItemHasBulletPrefix(n) {
				b.WriteString("• ")
			}
		} else if isBlockElement(tag) {
			// Do not break before the first block child of <li>; that would put
			// the bullet alone on a line above the item text.
			if !hasOpenBulletPrefix(b) {
				ensureTrailingNewline(b)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTMLTextWithBreaks(b, c)
		}
		if tag == "li" || isBlockElement(tag) {
			ensureTrailingNewline(b)
		}
	}
}

// hasOpenBulletPrefix reports whether the current line is exactly our list
// bullet marker with no item text written yet.
func hasOpenBulletPrefix(b *strings.Builder) bool {
	s := b.String()
	i := strings.LastIndexByte(s, '\n')
	line := s
	if i >= 0 {
		line = s[i+1:]
	}
	return line == "• "
}

func listItemHasBulletPrefix(n *html.Node) bool {
	text := strings.TrimLeftFunc(directAndDescendantText(n), unicode.IsSpace)
	if text == "" {
		return false
	}
	switch r := []rune(text)[0]; r {
	case '•', '·', '‣', '▪', '○', '-', '*', '–', '—':
		return true
	default:
		return false
	}
}

func directAndDescendantText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func isBlockElement(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "div", "dl", "dt", "dd",
		"fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3",
		"h4", "h5", "h6", "header", "hr", "main", "nav", "ol", "p",
		"pre", "section", "table", "tr", "ul":
		return true
	default:
		return false
	}
}

func ensureTrailingNewline(b *strings.Builder) {
	if b.Len() == 0 {
		return
	}
	s := b.String()
	if s[len(s)-1] != '\n' {
		b.WriteByte('\n')
	}
}

func cleanupStructuredText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		line = collapseWS(line)
		if line == "" {
			if prevBlank || len(out) == 0 {
				continue
			}
			out = append(out, "")
			prevBlank = true
			continue
		}
		out = append(out, line)
		prevBlank = false
	}
	// Trim trailing blank line.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func isJobstreetDetailURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return jobDetailPathRE.MatchString(u.Path)
}

func looksLikeJobstreetJobPath(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	path := u.Path
	if path == "" {
		// relative path without scheme
		path = href
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
	}
	return jobDetailPathRE.MatchString(path)
}

// canonicalizeJobstreetURL drops query/fragment and trailing slash.
func canonicalizeJobstreetURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
