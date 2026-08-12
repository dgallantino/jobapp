package scrape

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// salaryTextRE matches common salary / compensation snippets in free text.
var salaryTextRE = regexp.MustCompile(`(?i)(?:` +
	`(?:salary|compensation|pay|gaji|upah)\s*[:\-]?\s*` +
	`(?:(?:sgd|usd|idr|rp\.?|rm|myr|\$|s\$)\s*)?` +
	`[\d.,]+\s*(?:k|jt|juta|million|m)?` +
	`(?:\s*[-–—to]+\s*(?:(?:sgd|usd|idr|rp\.?|rm|myr|\$|s\$)\s*)?[\d.,]+\s*(?:k|jt|juta|million|m)?)?` +
	`(?:\s*(?:/\s*(?:mo(?:nth)?|yr|year|hour|hr)|per\s+(?:month|year|hour)))?` +
	`|` +
	`(?:sgd|usd|idr|rp\.?|rm|myr|s\$|\$)\s*[\d.,]+\s*(?:k|jt|juta|million|m)?` +
	`(?:\s*[-–—to]+\s*(?:(?:sgd|usd|idr|rp\.?|rm|myr|\$|s\$)\s*)?[\d.,]+\s*(?:k|jt|juta|million|m)?)?` +
	`(?:\s*(?:/\s*(?:mo(?:nth)?|yr|year|hour|hr)|per\s+(?:month|year|hour)))?` +
	`)`)

// StaticAdapter uses net/http + goquery for server-rendered pages.
type StaticAdapter struct {
	Client *http.Client
}

// NewStaticAdapter returns a static HTML adapter using client (or DefaultHTTPClient if nil).
func NewStaticAdapter(client *http.Client) *StaticAdapter {
	if client == nil {
		client = DefaultHTTPClient()
	}
	return &StaticAdapter{Client: client}
}

func (a *StaticAdapter) Name() string { return "static" }

// Scrape fetches pageURL. If the page looks like a listing (many similar links),
// it returns one JobAd per discovered absolute job-like link (title from link text).
// Otherwise it treats the URL as a single job detail page.
func (a *StaticAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	doc, finalURL, err := fetchDocument(ctx, a.Client, pageURL)
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
	descStr := desc.String()
	return JobAd{
		SourceURL:   pageURL,
		Title:       title,
		Company:     company,
		Salary:      extractSalary(doc, title, descStr),
		Description: descStr,
	}
}

func extractSalary(doc *goquery.Document, title, description string) string {
	if s := salaryFromMeta(doc); s != "" {
		return s
	}
	selectors := []string{
		`[itemprop="baseSalary"]`,
		`[itemprop="salary"]`,
		`[class*="salary"]`,
		`[class*="Salary"]`,
		`[id*="salary"]`,
		`[id*="Salary"]`,
		`[data-automation*="salary"]`,
		`.job-salary`,
		`#job-salary`,
	}
	for _, sel := range selectors {
		node := doc.Find(sel).First()
		if node.Length() == 0 {
			continue
		}
		text := collapseWS(node.Text())
		if text == "" {
			if content, ok := node.Attr("content"); ok {
				text = collapseWS(content)
			}
		}
		if text != "" && looksLikeSalary(text) {
			return truncateSalary(text)
		}
	}
	for _, blob := range []string{title, description} {
		if m := salaryTextRE.FindString(blob); m != "" {
			return truncateSalary(collapseWS(m))
		}
	}
	return ""
}

func salaryFromMeta(doc *goquery.Document) string {
	var found string
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		if found != "" {
			return
		}
		name, _ := s.Attr("name")
		prop, _ := s.Attr("property")
		key := strings.ToLower(name + " " + prop)
		if !strings.Contains(key, "salary") && !strings.Contains(key, "compensation") {
			return
		}
		content, _ := s.Attr("content")
		content = collapseWS(content)
		if content != "" {
			found = truncateSalary(content)
		}
	})
	return found
}

func looksLikeSalary(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "salary") || strings.Contains(lower, "compensation") ||
		strings.Contains(lower, "gaji") || strings.Contains(lower, "negotiable") {
		return true
	}
	return salaryTextRE.MatchString(s)
}

func truncateSalary(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
