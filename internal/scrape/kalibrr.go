package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	kalibrrSalaryUndisclosed = "Gaji Tidak Diumumkan"
	kalibrrMaxListingJobs    = 100
	kalibrrDefaultAPIBase    = "https://www.kalibrr.id"
	kalibrrSearchPageSize    = 15
)

// kalibrrDetailPathRE matches /c/{company}/jobs/{id}/{slug} with optional locale prefix.
var kalibrrDetailPathRE = regexp.MustCompile(`(?i)^(?:/[a-z]{2}(?:-[a-z]{2})?)?/c/([^/]+)/jobs/(\d+)/([^/?#]+)/?$`)

// kalibrrSearchPathRE extracts the keyword from /home/te/{text} (optional locale).
var kalibrrSearchPathRE = regexp.MustCompile(`(?i)^(?:/[a-z]{2}(?:-[a-z]{2})?)?/home/te/([^/?#]+)/?$`)

// kalibrrAdapter scrapes Kalibrr listing and detail pages.
//
// Investigation notes (2026-08):
// Kalibrr (kalibrr.id) is Next.js. Listing "Load more jobs" calls anonymous
// GET /kjs/job_board/search?limit=15&offset=N&text=... — HTML pagination is
// not used. Search API jobs already include description + qualifications, so
// listing crawls map API JSON directly (no detail enrichment). Detail URLs
// parse __NEXT_DATA__.props.pageProps.job (camelCase). Do not page past the
// first response's count: offset >= count returns a broader unrelated set.
//
// Detail path: /{locale}/c/{companyCode}/jobs/{id}/{slug}.
type kalibrrAdapter struct {
	Client            *Client
	ScrapeConcurrency int
	APIBase           string // optional; default https://www.kalibrr.id (tests override)
}

// newKalibrrAdapter returns a Kalibrr adapter using client (or NewClient defaults if nil).
// scrapeConcurrency is kept for constructor parity with other board adapters.
func newKalibrrAdapter(client *Client, scrapeConcurrency int) *kalibrrAdapter {
	if scrapeConcurrency < 1 {
		scrapeConcurrency = 1
	}
	if client == nil {
		client = NewClient(ClientOptions{})
	}
	return &kalibrrAdapter{
		Client:            client,
		ScrapeConcurrency: scrapeConcurrency,
	}
}

func (a *kalibrrAdapter) apiBase() string {
	base := strings.TrimSpace(a.APIBase)
	if base == "" {
		return kalibrrDefaultAPIBase
	}
	return strings.TrimRight(base, "/")
}

func (a *kalibrrAdapter) Name() string { return "kalibrr" }

func (a *kalibrrAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	if isKalibrrDetailURL(pageURL) {
		doc, finalURL, err := a.Client.FetchDocument(ctx, pageURL)
		if err != nil {
			return nil, fmt.Errorf("kalibrr: %w", err)
		}
		ad, err := parseKalibrrDetail(doc, finalURL)
		if err != nil {
			return nil, err
		}
		return []JobAd{ad}, nil
	}

	return a.scrapeListingAPI(ctx, pageURL)
}

// scrapeListingAPI walks /kjs/job_board/search until the job cap, empty page,
// or offset reaches the first response's count. Deduplicates by SourceURL.
func (a *kalibrrAdapter) scrapeListingAPI(ctx context.Context, pageURL string) ([]JobAd, error) {
	search := kalibrrSearchFromListingURL(pageURL)
	siteBase, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("kalibrr: %w", err)
	}
	siteOrigin := siteBase.Scheme + "://" + siteBase.Host

	seen := map[string]struct{}{}
	var out []JobAd
	totalCount := -1

	for offset := 0; ; offset += kalibrrSearchPageSize {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if totalCount >= 0 && offset >= totalCount {
			break
		}
		if len(out) >= kalibrrMaxListingJobs {
			return out[:kalibrrMaxListingJobs], nil
		}

		apiURL := kalibrrSearchAPIURL(a.apiBase(), search, offset)
		body, _, err := a.Client.FetchBytes(ctx, apiURL)
		if err != nil {
			if len(out) == 0 {
				return nil, fmt.Errorf("kalibrr: %w", err)
			}
			log.Printf("kalibrr: search %s: %v", apiURL, err)
			break
		}
		jobs, count, ok := parseKalibrrSearchAPIResponse(body)
		if !ok {
			if len(out) == 0 {
				return nil, fmt.Errorf("kalibrr: unparseable search response at %s", apiURL)
			}
			log.Printf("kalibrr: search %s: unparseable response", apiURL)
			break
		}
		if totalCount < 0 {
			totalCount = count
		}
		if len(jobs) == 0 {
			break
		}

		added := 0
		for _, ad := range kalibrrAdsFromJobs(jobs, siteOrigin) {
			if _, ok := seen[ad.SourceURL]; ok {
				continue
			}
			seen[ad.SourceURL] = struct{}{}
			out = append(out, ad)
			added++
			if len(out) >= kalibrrMaxListingJobs {
				return out[:kalibrrMaxListingJobs], nil
			}
		}
		if added == 0 {
			break
		}
		if totalCount >= 0 && offset+kalibrrSearchPageSize >= totalCount {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("kalibrr: no jobs found at %s", pageURL)
	}
	return out, nil
}

func kalibrrSearchFromListingURL(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	m := kalibrrSearchPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	text, err := url.PathUnescape(m[1])
	if err != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(text)
}

func kalibrrSearchAPIURL(apiBase, search string, offset int) string {
	u, err := url.Parse(apiBase + "/kjs/job_board/search")
	if err != nil {
		return apiBase + "/kjs/job_board/search"
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(kalibrrSearchPageSize))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("text", search)
	u.RawQuery = q.Encode()
	return u.String()
}

type kalibrrSearchAPIEnvelope struct {
	Count int              `json:"count"`
	Jobs  []kalibrrJobJSON `json:"jobs"`
}

func parseKalibrrSearchAPIResponse(body []byte) (jobs []kalibrrJobJSON, count int, ok bool) {
	var env kalibrrSearchAPIEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, 0, false
	}
	return env.Jobs, env.Count, true
}

type kalibrrNextData struct {
	Props struct {
		PageProps struct {
			Job *kalibrrJobJSON `json:"job"`
		} `json:"pageProps"`
	} `json:"props"`
}

// kalibrrJobJSON accepts both snake_case (search API) and camelCase (__NEXT_DATA__).
type kalibrrJobJSON struct {
	ID             int64
	Name           string
	Slug           string
	CompanyName    string
	Description    string
	Qualifications string
	BaseSalary     *int64
	MaximumSalary  *int64
	SalaryCurrency string
	SalaryInterval string
	SalaryShown    *bool
	Company        *kalibrrCompanyJSON
}

type kalibrrCompanyJSON struct {
	Code string
	Name string
}

func (j *kalibrrJobJSON) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	j.ID = kalibrrRawInt64(raw, "id")
	j.Name = kalibrrRawString(raw, "name")
	j.Slug = kalibrrRawString(raw, "slug")
	j.CompanyName = kalibrrRawString(raw, "company_name", "companyName")
	j.Description = kalibrrRawString(raw, "description")
	j.Qualifications = kalibrrRawString(raw, "qualifications")
	j.SalaryCurrency = kalibrrRawString(raw, "salary_currency", "salaryCurrency")
	j.SalaryInterval = kalibrrRawString(raw, "salary_interval", "salaryInterval")
	if v, ok := kalibrrRawInt64Ptr(raw, "base_salary", "baseSalary"); ok {
		j.BaseSalary = v
	}
	if v, ok := kalibrrRawInt64Ptr(raw, "maximum_salary", "maximumSalary"); ok {
		j.MaximumSalary = v
	}
	if v, ok := kalibrrRawBoolPtr(raw, "salary_shown", "salaryShown"); ok {
		j.SalaryShown = v
	}
	if b, ok := raw["company"]; ok && string(b) != "null" {
		var c kalibrrCompanyJSON
		if err := json.Unmarshal(b, &c); err == nil {
			j.Company = &c
		}
	}
	if j.Company == nil {
		if b, ok := raw["company_info"]; ok && string(b) != "null" {
			var c kalibrrCompanyJSON
			if err := json.Unmarshal(b, &c); err == nil {
				j.Company = &c
			}
		} else if b, ok := raw["companyInfo"]; ok && string(b) != "null" {
			var c kalibrrCompanyJSON
			if err := json.Unmarshal(b, &c); err == nil {
				j.Company = &c
			}
		}
	}
	return nil
}

func (c *kalibrrCompanyJSON) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Code = kalibrrRawString(raw, "code")
	c.Name = kalibrrRawString(raw, "name")
	return nil
}

func kalibrrRawString(raw map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		b, ok := raw[k]
		if !ok || string(b) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return s
		}
	}
	return ""
}

func kalibrrRawInt64(raw map[string]json.RawMessage, keys ...string) int64 {
	if v, ok := kalibrrRawInt64Ptr(raw, keys...); ok && v != nil {
		return *v
	}
	return 0
}

func kalibrrRawInt64Ptr(raw map[string]json.RawMessage, keys ...string) (*int64, bool) {
	for _, k := range keys {
		b, ok := raw[k]
		if !ok || string(b) == "null" {
			continue
		}
		var n int64
		if err := json.Unmarshal(b, &n); err == nil {
			return &n, true
		}
		var f float64
		if err := json.Unmarshal(b, &f); err == nil {
			n = int64(f)
			return &n, true
		}
	}
	return nil, false
}

func kalibrrRawBoolPtr(raw map[string]json.RawMessage, keys ...string) (*bool, bool) {
	for _, k := range keys {
		b, ok := raw[k]
		if !ok || string(b) == "null" {
			continue
		}
		var v bool
		if err := json.Unmarshal(b, &v); err == nil {
			return &v, true
		}
	}
	return nil, false
}

func kalibrrAdsFromJobs(jobs []kalibrrJobJSON, siteOrigin string) []JobAd {
	base, err := url.Parse(siteOrigin + "/")
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []JobAd
	for _, job := range jobs {
		ad, ok := kalibrrAdFromJob(job, base)
		if !ok {
			continue
		}
		if _, dup := seen[ad.SourceURL]; dup {
			continue
		}
		seen[ad.SourceURL] = struct{}{}
		out = append(out, ad)
	}
	return out
}

func kalibrrAdFromJob(job kalibrrJobJSON, siteBase *url.URL) (JobAd, bool) {
	href := kalibrrJobHref(job)
	if href == "" {
		return JobAd{}, false
	}
	abs, err := siteBase.Parse(href)
	if err != nil {
		return JobAd{}, false
	}
	canonical := canonicalizeKalibrrURL(abs.String())
	if canonical == "" || !isKalibrrDetailURL(canonical) {
		return JobAd{}, false
	}

	title := normalizeKalibrrText(job.Name)
	if title == "" {
		title = canonical
	}
	company := normalizeKalibrrText(job.CompanyName)
	if company == "" && job.Company != nil {
		company = normalizeKalibrrText(job.Company.Name)
	}

	return JobAd{
		SourceURL:   canonical,
		Title:       title,
		Company:     company,
		Salary:      formatKalibrrSalary(job),
		Description: kalibrrDescriptionFromHTML(job.Description, job.Qualifications),
	}, true
}

func kalibrrJobHref(job kalibrrJobJSON) string {
	if job.ID <= 0 || strings.TrimSpace(job.Slug) == "" {
		return ""
	}
	code := ""
	if job.Company != nil {
		code = strings.TrimSpace(job.Company.Code)
	}
	if code == "" {
		return ""
	}
	return fmt.Sprintf("/id-ID/c/%s/jobs/%d/%s", code, job.ID, strings.TrimSpace(job.Slug))
}

func parseKalibrrDetail(doc *goquery.Document, pageURL string) (JobAd, error) {
	if job, ok := kalibrrJobFromNextData(doc); ok {
		base, err := url.Parse(pageURL)
		if err != nil {
			return JobAd{}, fmt.Errorf("kalibrr: %w", err)
		}
		siteBase, err := url.Parse(base.Scheme + "://" + base.Host + "/")
		if err != nil {
			return JobAd{}, fmt.Errorf("kalibrr: %w", err)
		}
		// Prefer canonical URL from the job object when company/slug present;
		// otherwise canonicalize the requested page URL.
		if ad, ok := kalibrrAdFromJob(job, siteBase); ok {
			return ad, nil
		}
		title := normalizeKalibrrText(job.Name)
		if title == "" {
			return JobAd{}, fmt.Errorf("kalibrr: missing job title at %s", pageURL)
		}
		company := normalizeKalibrrText(job.CompanyName)
		if company == "" && job.Company != nil {
			company = normalizeKalibrrText(job.Company.Name)
		}
		return JobAd{
			SourceURL:   canonicalizeKalibrrURL(pageURL),
			Title:       title,
			Company:     company,
			Salary:      formatKalibrrSalary(job),
			Description: kalibrrDescriptionFromHTML(job.Description, job.Qualifications),
		}, nil
	}
	return JobAd{}, fmt.Errorf("kalibrr: missing job data at %s", pageURL)
}

func kalibrrJobFromNextData(doc *goquery.Document) (kalibrrJobJSON, bool) {
	raw, ok := kalibrrNextDataRaw(doc)
	if !ok {
		return kalibrrJobJSON{}, false
	}
	var next kalibrrNextData
	if err := json.Unmarshal(raw, &next); err != nil {
		return kalibrrJobJSON{}, false
	}
	if next.Props.PageProps.Job == nil {
		return kalibrrJobJSON{}, false
	}
	return *next.Props.PageProps.Job, true
}

func kalibrrNextDataRaw(doc *goquery.Document) ([]byte, bool) {
	script := doc.Find(`script#__NEXT_DATA__`).First()
	if script.Length() == 0 {
		return nil, false
	}
	raw := strings.TrimSpace(script.Text())
	if raw == "" {
		return nil, false
	}
	return []byte(raw), true
}

func kalibrrDescriptionFromHTML(description, qualifications string) string {
	var parts []string
	if s := kalibrrSectionText("Deskripsi Pekerjaan", description); s != "" {
		parts = append(parts, s)
	}
	if s := kalibrrSectionText("Kualifikasi Minimum", qualifications); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

func kalibrrSectionText(heading, htmlFrag string) string {
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

func formatKalibrrSalary(job kalibrrJobJSON) string {
	if job.SalaryShown != nil && !*job.SalaryShown {
		return kalibrrSalaryUndisclosed
	}
	base := int64(0)
	max := int64(0)
	if job.BaseSalary != nil {
		base = *job.BaseSalary
	}
	if job.MaximumSalary != nil {
		max = *job.MaximumSalary
	}
	if base <= 0 && max <= 0 {
		return kalibrrSalaryUndisclosed
	}

	var amount string
	switch {
	case base > 0 && max > 0 && base != max:
		amount = formatKalibrrIDR(base) + " – " + formatKalibrrIDR(max)
	case base > 0:
		amount = formatKalibrrIDR(base)
	default:
		amount = formatKalibrrIDR(max)
	}
	if suffix := kalibrrIntervalSuffix(job.SalaryInterval); suffix != "" {
		return amount + " " + suffix
	}
	return amount
}

func formatKalibrrIDR(n int64) string {
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

func kalibrrIntervalSuffix(interval string) string {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "month", "monthly":
		return "/ bulan"
	case "year", "yearly", "annual":
		return "/ tahun"
	case "week", "weekly":
		return "/ minggu"
	case "day", "daily":
		return "/ hari"
	case "hour", "hourly":
		return "/ jam"
	default:
		return ""
	}
}

func normalizeKalibrrText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return collapseWS(s)
}

func isKalibrrDetailURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return looksLikeKalibrrJobPath(u.Path)
}

func looksLikeKalibrrJobPath(href string) bool {
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
	return kalibrrDetailPathRE.MatchString(path)
}

// canonicalizeKalibrrURL drops query/fragment and trailing slash.
func canonicalizeKalibrrURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
