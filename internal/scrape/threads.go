package scrape

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

const threadsCanonicalHost = "www.threads.com"

var (
	threadsPostPathRE  = regexp.MustCompile(`(?i)^/@([^/]+)/post/([^/]+)/?$`)
	threadsPlaintextRE = regexp.MustCompile(`"plaintext"\s*:\s*"((?:\\.|[^"\\])*)"`)

	// Labeled Indonesian minimum-wage tokens; numeric ranges use salaryTextRE.
	threadsUMRSalaryRE = regexp.MustCompile(`(?i)(?:salary|compensation|pay|gaji|upah)\s*[:\-]?\s*(UMR|UMK)\b`)

	threadsHiringSepRE = regexp.MustCompile(`(?i)(?:we['’]?re\s+hiring|we\s+are\s+hiring|now\s+hiring|\bhiring\b|lowongan(?:\s+kerja)?)\s*[-–—|:]\s*(.+)`)

	threadsCompanyPTRE     = regexp.MustCompile(`(?i)\bP\.?\s*T\.?\s+[A-Za-z][A-Za-z0-9&.,'’\- ]{1,80}`)
	threadsCompanySuffixRE = regexp.MustCompile(`(?i)(?:Pte\.?\s*Ltd\.?|Ltd\.?|Limited|Inc\.?|Incorporated|LLC|GmbH)\b`)

	threadsRoleWordRE = regexp.MustCompile(`(?i)\b(staff|programmer|developer|engineer|manager|operator|assistant|analyst|designer|intern|officer|admin|administrasi|warehouse|production|packing|quality|marketing|finance|accounting)\b`)
)

// FieldExtractor fills empty job fields from unstructured text (e.g. an LLM).
type FieldExtractor interface {
	ExtractJobFields(ctx context.Context, postText string, missing []string) (title, company, salary string, err error)
}

// ThreadsAdapter parses a single Meta Threads post URL into one JobAd.
// It is link-only: no listing crawl, no follow of links inside the caption.
type ThreadsAdapter struct {
	Client    *Client
	Extractor FieldExtractor
}

// NewThreadsAdapter returns a Threads adapter using client (or NewClient defaults if nil).
func NewThreadsAdapter(client *Client, extractor FieldExtractor) *ThreadsAdapter {
	if client == nil {
		client = NewClient(ClientOptions{})
	}
	return &ThreadsAdapter{Client: client, Extractor: extractor}
}

func (a *ThreadsAdapter) Name() string { return "threads" }

// Scrape fetches pageURL as a single Threads post and returns one JobAd.
func (a *ThreadsAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	html, finalURL, fetchErr := a.Client.FetchBytes(ctx, pageURL)
	var (
		ad      JobAd
		caption string
		err     error
	)
	if fetchErr == nil {
		ad, caption, err = parseThreadsPost(string(html), firstNonEmpty(finalURL, pageURL))
	}
	if fetchErr != nil || err != nil {
		rendered, loc, rerr := a.Client.Render(ctx, pageURL, `meta[property="og:description"]`)
		if rerr != nil {
			if fetchErr != nil {
				return nil, fmt.Errorf("threads: %w", fetchErr)
			}
			return nil, fmt.Errorf("threads: %w", err)
		}
		ad, caption, err = parseThreadsPost(rendered, firstNonEmpty(loc, pageURL))
		if err != nil {
			return nil, err
		}
	}

	a.fillEmptyFields(ctx, &ad, caption)
	return []JobAd{ad}, nil
}

func (a *ThreadsAdapter) fillEmptyFields(ctx context.Context, ad *JobAd, caption string) {
	if a == nil || a.Extractor == nil || strings.TrimSpace(caption) == "" {
		return
	}
	var missing []string
	if strings.TrimSpace(ad.Title) == "" {
		missing = append(missing, "title")
	}
	if strings.TrimSpace(ad.Company) == "" {
		missing = append(missing, "company")
	}
	if strings.TrimSpace(ad.Salary) == "" {
		missing = append(missing, "salary")
	}
	if len(missing) == 0 {
		return
	}
	title, company, salary, err := a.Extractor.ExtractJobFields(ctx, caption, missing)
	if err != nil {
		log.Printf("threads llm extract: %v", err)
		return
	}
	if strings.TrimSpace(ad.Title) == "" {
		ad.Title = strings.TrimSpace(title)
	}
	if strings.TrimSpace(ad.Company) == "" {
		ad.Company = strings.TrimSpace(company)
	}
	if strings.TrimSpace(ad.Salary) == "" {
		ad.Salary = strings.TrimSpace(salary)
	}
}

func parseThreadsPost(html, pageURL string) (JobAd, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return JobAd{}, "", fmt.Errorf("threads: parse html: %w", err)
	}

	caption := extractThreadsCaption(html, doc)
	if strings.TrimSpace(caption) == "" {
		return JobAd{}, "", fmt.Errorf("threads: no post text")
	}

	canonical := canonicalThreadsURL(pageURL, metaContent(doc, "og:url"))
	title, company, salary := extractThreadsFields(caption)
	return JobAd{
		SourceURL:   canonical,
		Title:       title,
		Company:     company,
		Salary:      salary,
		Description: caption,
	}, caption, nil
}

func extractThreadsCaption(html string, doc *goquery.Document) string {
	og := strings.TrimSpace(metaContent(doc, "og:description"))
	seed := og
	if seed == "" {
		seed = strings.TrimSpace(metaContent(doc, "twitter:description"))
	}

	best := seed
	for _, p := range extractRelayPlaintexts(html) {
		if !captionMatchesSeed(p, seed) {
			continue
		}
		if len(p) > len(best) {
			best = p
		}
	}
	return strings.TrimSpace(best)
}

func captionMatchesSeed(plain, seed string) bool {
	if strings.TrimSpace(seed) == "" {
		return false
	}
	p := collapseWS(plain)
	s := collapseWS(seed)
	if s == "" || p == "" {
		return false
	}
	return strings.HasPrefix(p, s) || strings.HasPrefix(s, p)
}

func extractRelayPlaintexts(html string) []string {
	matches := threadsPlaintextRE.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		decoded, err := strconv.Unquote(`"` + m[1] + `"`)
		if err != nil {
			continue
		}
		decoded = strings.TrimSpace(decoded)
		if decoded != "" {
			out = append(out, decoded)
		}
	}
	return out
}

func extractThreadsFields(caption string) (title, company, salary string) {
	company = extractThreadsCompany(caption)
	title = extractThreadsTitle(caption)
	if company != "" && strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(company)) {
		title = ""
	}
	salary = extractThreadsSalary(caption)
	return title, company, salary
}

func extractThreadsTitle(caption string) string {
	for _, line := range captionLines(caption, 8) {
		m := threadsHiringSepRE.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		title := cleanThreadsTitle(m[1])
		if title == "" || looksLikeOrgName(title) {
			continue
		}
		return title
	}
	return ""
}

func extractThreadsCompany(caption string) string {
	if m := threadsCompanyPTRE.FindString(caption); m != "" {
		if c := cleanCompanyName(m); c != "" {
			return c
		}
	}
	if c := extractCompanyBySuffix(caption); c != "" {
		return c
	}
	for _, line := range captionLines(caption, 8) {
		m := threadsHiringSepRE.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		name := cleanCompanyName(m[1])
		if looksLikeOrgName(name) {
			return name
		}
	}
	return ""
}

func extractCompanyBySuffix(caption string) string {
	locs := threadsCompanySuffixRE.FindAllStringIndex(caption, -1)
	for _, loc := range locs {
		suffix := strings.TrimSpace(caption[loc[0]:loc[1]])
		words := strings.FieldsFunc(caption[:loc[0]], func(r rune) bool {
			return unicode.IsSpace(r) || r == ',' || r == ';'
		})
		var name []string
		for i := len(words) - 1; i >= 0; i-- {
			w := strings.Trim(words[i], ".,:;!?\"'")
			if w == "" {
				break
			}
			if isCompanyNameStop(w) {
				break
			}
			r, _ := utf8.DecodeRuneInString(w)
			if !unicode.IsUpper(r) {
				break
			}
			name = append([]string{w}, name...)
			if len(name) >= 6 {
				break
			}
		}
		if len(name) == 0 {
			continue
		}
		if c := cleanCompanyName(strings.Join(name, " ") + " " + suffix); c != "" {
			return c
		}
	}
	return ""
}

var companyNameStops = map[string]struct{}{
	"join": {}, "joining": {}, "at": {}, "with": {}, "our": {}, "the": {},
	"a": {}, "an": {}, "for": {}, "and": {}, "from": {}, "by": {},
	"di": {}, "ke": {}, "yang": {}, "dari": {},
	"hiring": {}, "lowongan": {}, "kerja": {}, "we": {}, "now": {},
	"looking": {}, "need": {}, "needed": {},
}

func isCompanyNameStop(word string) bool {
	_, ok := companyNameStops[strings.ToLower(strings.Trim(word, ".,:;!'’"))]
	return ok
}

func extractThreadsSalary(caption string) string {
	if m := salaryTextRE.FindString(caption); m != "" {
		return truncateSalary(collapseWS(m))
	}
	if m := threadsUMRSalaryRE.FindString(caption); m != "" {
		return truncateSalary(collapseWS(m))
	}
	return ""
}

func cleanThreadsTitle(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"📍", "💰", "🕒", "📞", "📧"} {
		if i := strings.Index(s, sep); i > 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return strings.TrimSpace(s)
}

func cleanCompanyName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRightFunc(s, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
		return r != '.' && r != ')'
	})
	return collapseWS(s)
}

func looksLikeOrgName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if threadsRoleWordRE.MatchString(s) {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "pt ") || strings.HasPrefix(lower, "pt.") ||
		strings.Contains(lower, " ltd") || strings.Contains(lower, " inc") ||
		strings.Contains(lower, " pte") || strings.Contains(lower, " llc") {
		return true
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return false
	}
	upper := 0
	for _, w := range words {
		letters, ups := 0, 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					ups++
				}
			}
		}
		if letters > 0 && ups*2 >= letters {
			upper++
		}
	}
	return upper == len(words)
}

func captionLines(caption string, limit int) []string {
	raw := strings.Split(strings.ReplaceAll(caption, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func canonicalThreadsURL(pageURL, ogURL string) string {
	for _, candidate := range []string{ogURL, pageURL} {
		u, err := url.Parse(strings.TrimSpace(candidate))
		if err != nil {
			continue
		}
		if !isThreadsHost(u.Hostname()) {
			continue
		}
		m := threadsPostPathRE.FindStringSubmatch(u.Path)
		if m == nil {
			continue
		}
		return "https://" + threadsCanonicalHost + "/@" + m[1] + "/post/" + m[2]
	}
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return strings.TrimSpace(pageURL)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func isThreadsHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	return host == "threads.com" || host == "threads.net"
}

func metaContent(doc *goquery.Document, property string) string {
	var found string
	sel := fmt.Sprintf(`meta[property=%q], meta[name=%q]`, property, property)
	doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
		if found != "" {
			return
		}
		if content, ok := s.Attr("content"); ok {
			found = content
		}
	})
	return found
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
