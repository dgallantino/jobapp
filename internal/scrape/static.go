package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// StaticAdapter uses net/http + goquery for server-rendered pages.
type StaticAdapter struct {
	Client *http.Client
}

// NewStaticAdapter returns the default static HTML adapter.
func NewStaticAdapter() *StaticAdapter {
	return &StaticAdapter{Client: DefaultHTTPClient()}
}

func (a *StaticAdapter) Name() string { return "static" }

// Scrape fetches pageURL. If the page looks like a listing (many similar links),
// it returns one JobAd per discovered absolute job-like link (title from link text).
// Otherwise it treats the URL as a single job detail page.
func (a *StaticAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	doc, finalURL, err := a.fetchDoc(ctx, pageURL)
	if err != nil {
		return nil, err
	}

	links := discoverJobLinks(doc, finalURL)
	if len(links) >= 3 {
		out := make([]JobAd, 0, len(links))
		for _, l := range links {
			out = append(out, JobAd{
				SourceURL:   l.href,
				Title:       l.title,
				Company:     "",
				Description: "",
			})
		}
		return out, nil
	}

	ad := extractDetail(doc, finalURL)
	return []JobAd{ad}, nil
}

type linkHit struct {
	href  string
	title string
}

func (a *StaticAdapter) fetchDoc(ctx context.Context, pageURL string) (*goquery.Document, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "jobapp/1.0 (+personal job scraper)")

	client := a.Client
	if client == nil {
		client = DefaultHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, pageURL)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, "", err
	}
	final := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return doc, final, nil
}

func discoverJobLinks(doc *goquery.Document, base string) []linkHit {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []linkHit
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return
		}
		u, err := baseURL.Parse(href)
		if err != nil {
			return
		}
		abs := u.String()
		if _, ok := seen[abs]; ok {
			return
		}
		path := strings.ToLower(u.Path)
		if !looksLikeJobPath(path) {
			return
		}
		title := strings.TrimSpace(s.Text())
		if title == "" {
			title = abs
		}
		seen[abs] = struct{}{}
		out = append(out, linkHit{href: abs, title: collapseWS(title)})
	})
	return out
}

func looksLikeJobPath(path string) bool {
	needles := []string{
		"/job/", "/jobs/", "/position/", "/positions/",
		"/vacancy/", "/vacancies/", "/career/", "/careers/",
		"/opening/", "/openings/", "/role/", "/roles/",
	}
	for _, n := range needles {
		if strings.Contains(path, n) {
			return true
		}
	}
	return false
}

func extractDetail(doc *goquery.Document, pageURL string) JobAd {
	title := strings.TrimSpace(doc.Find("h1").First().Text())
	if title == "" {
		title = strings.TrimSpace(doc.Find("title").First().Text())
	}
	title = collapseWS(title)

	company := ""
	doc.Find(`meta[property="og:site_name"]`).Each(func(_ int, s *goquery.Selection) {
		if content, ok := s.Attr("content"); ok && company == "" {
			company = strings.TrimSpace(content)
		}
	})

	var desc strings.Builder
	selectors := []string{"article", "main", "[role=main]", ".job-description", "#job-description", "body"}
	for _, sel := range selectors {
		node := doc.Find(sel).First()
		if node.Length() == 0 {
			continue
		}
		text := collapseWS(node.Text())
		if len(text) < 40 {
			continue
		}
		desc.WriteString(text)
		break
	}

	if title == "" {
		title = pageURL
	}
	return JobAd{
		SourceURL:   pageURL,
		Title:       title,
		Company:     company,
		Description: desc.String(),
	}
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
