package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func loadDeallsFixture(t *testing.T, name string) *goquery.Document {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc
}

func TestParseDeallsDetail_WithSalary(t *testing.T) {
	doc := loadDeallsFixture(t, "dealls_detail.html")
	pageURL := "https://dealls.com/loker/senior-compliance-manager~fintureid?utm=x#frag"

	ad, err := parseDeallsDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseDeallsDetail: %v", err)
	}

	if got, want := ad.Title, "Senior Compliance Manager"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "Finture"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, "Rp10.000.000 – Rp15.000.000"; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if got, want := ad.SourceURL, "https://dealls.com/loker/senior-compliance-manager~fintureid"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "Deskripsi Pekerjaan\n") {
		t.Errorf("Description missing Deskripsi heading: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\nKualifikasi\n") {
		t.Errorf("Description missing Kualifikasi heading: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "• Develop and maintain compliance policies") {
		t.Errorf("Description missing responsibility bullet: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "• Bachelor's or Master's degree in Law") {
		t.Errorf("Description missing requirement bullet: %q", ad.Description)
	}
	if strings.Contains(ad.Description, "Flexible working environment") {
		t.Errorf("Description should omit Benefit Perusahaan: %q", ad.Description)
	}
}

func TestParseDeallsDetail_NoSalary(t *testing.T) {
	doc := loadDeallsFixture(t, "dealls_detail_no_salary.html")
	pageURL := "https://dealls.com/loker/full-stack-developer-golang~pt-lawencon-internasional"

	ad, err := parseDeallsDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseDeallsDetail: %v", err)
	}

	if got, want := ad.Title, "Full Stack Developer (Golang)"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT Lawencon Internasional"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, deallsSalaryNegotiable; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "• Build Golang services") {
		t.Errorf("Description missing responsibility: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "• Experience with Odoo is not required here but Go is") {
		t.Errorf("Description missing requirement: %q", ad.Description)
	}
}

func TestParseDeallsDetail_MissingTitle(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<html><body><p>no title</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseDeallsDetail(doc, "https://dealls.com/loker/x~y")
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestParseDeallsListing(t *testing.T) {
	doc := loadDeallsFixture(t, "dealls_listing.html")
	base := "https://dealls.com/?searchJob=developer"

	ads := parseDeallsListing(doc, base)
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2 (deduped, reserved skipped)", len(ads))
	}

	if got, want := ads[0].SourceURL, "https://dealls.com/loker/senior-compliance-manager~fintureid"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Senior Compliance Manager"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if got, want := ads[0].Company, "Finture"; got != want {
		t.Errorf("ads[0].Company = %q, want %q", got, want)
	}
	if got, want := ads[0].Salary, "Rp10.000.000 – Rp15.000.000"; got != want {
		t.Errorf("ads[0].Salary = %q, want %q", got, want)
	}

	if got, want := ads[1].SourceURL, "https://dealls.com/loker/full-stack-developer-golang~pt-lawencon-internasional"; got != want {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, deallsSalaryNegotiable; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if ads[1].Description != "" {
		t.Errorf("listing description should be empty, got %q", ads[1].Description)
	}
}

func TestDeallsAdapter_ScrapeListingEnrichesDetails(t *testing.T) {
	listingHTML, err := os.ReadFile(filepath.Join("testdata", "dealls_listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailWithSalary, err := os.ReadFile(filepath.Join("testdata", "dealls_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailNoSalary, err := os.ReadFile(filepath.Join("testdata", "dealls_detail_no_salary.html"))
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(listingHTML)
	})
	mux.HandleFunc("/v1/explore-job/job", func(w http.ResponseWriter, r *http.Request) {
		// Fixture listing is the only page; stop pagination.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"docs":[],"page":2,"totalPages":1,"totalDocs":2}}`))
	})
	mux.HandleFunc("/loker/senior-compliance-manager~fintureid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailWithSalary)
	})
	mux.HandleFunc("/loker/full-stack-developer-golang~pt-lawencon-internasional", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailNoSalary)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)
	a.APIBase = srv.URL

	ads, err := a.Scrape(context.Background(), srv.URL+"/?searchJob=developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2", len(ads))
	}

	if got, want := ads[0].SourceURL, srv.URL+"/loker/senior-compliance-manager~fintureid"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Senior Compliance Manager"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if !strings.Contains(ads[0].Description, "Develop and maintain compliance policies") {
		t.Errorf("ads[0].Description missing detail snippet: %q", ads[0].Description)
	}

	if got, want := ads[1].SourceURL, srv.URL+"/loker/full-stack-developer-golang~pt-lawencon-internasional"; got != want {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[1].Title, "Full Stack Developer (Golang)"; got != want {
		t.Errorf("ads[1].Title = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, deallsSalaryNegotiable; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ads[1].Description, "Build Golang services") {
		t.Errorf("ads[1].Description missing detail snippet: %q", ads[1].Description)
	}
}

func TestDeallsAdapter_ScrapeListingWalksAPIPages(t *testing.T) {
	listingHTML, err := os.ReadFile(filepath.Join("testdata", "dealls_listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	page2JSON, err := os.ReadFile(filepath.Join("testdata", "dealls_explore_job_page2.json"))
	if err != nil {
		t.Fatal(err)
	}
	detailWithSalary, err := os.ReadFile(filepath.Join("testdata", "dealls_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailNoSalary, err := os.ReadFile(filepath.Join("testdata", "dealls_detail_no_salary.html"))
	if err != nil {
		t.Fatal(err)
	}

	var page2Hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(listingHTML)
	})
	mux.HandleFunc("/v1/explore-job/job", func(w http.ResponseWriter, r *http.Request) {
		page2Hits++
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("unexpected page=%q", r.URL.Query().Get("page"))
		}
		if r.URL.Query().Get("search") != "developer" {
			t.Errorf("unexpected search=%q", r.URL.Query().Get("search"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(page2JSON)
	})
	mux.HandleFunc("/loker/senior-compliance-manager~fintureid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailWithSalary)
	})
	mux.HandleFunc("/loker/full-stack-developer-golang~pt-lawencon-internasional", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailNoSalary)
	})
	mux.HandleFunc("/loker/android-developer-sr-specialist~example-mobile-co", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html><body>
				<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"dehydratedState":{"queries":[{"queryKey":["/v1/job-portal/job/slug","android-developer-sr-specialist"],"state":{"data":{"role":"Android Developer Sr. Specialist","slug":"android-developer-sr-specialist","salaryType":"paid","salaryRange":{"start":12000000,"end":18000000},"responsibilities":"<ul><li>Ship Android apps.</li></ul>","requirements":"<ul><li>Kotlin.</li></ul>","company":{"name":"Example Mobile Co","slug":"example-mobile-co"}},"status":"success"}}]}}},"page":"/loker/[slug]"}</script>
			</body></html>
		`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)
	a.APIBase = srv.URL

	ads, err := a.Scrape(context.Background(), srv.URL+"/?searchJob=developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 3 {
		t.Fatalf("got %d ads, want 3 (page1+page2)", len(ads))
	}
	if page2Hits != 1 {
		t.Fatalf("page2 hits = %d, want 1", page2Hits)
	}
	if got, want := ads[2].SourceURL, srv.URL+"/loker/android-developer-sr-specialist~example-mobile-co"; got != want {
		t.Errorf("ads[2].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[2].Title, "Android Developer Sr. Specialist"; got != want {
		t.Errorf("ads[2].Title = %q, want %q", got, want)
	}
	if !strings.Contains(ads[2].Description, "Ship Android apps") {
		t.Errorf("ads[2].Description missing detail snippet: %q", ads[2].Description)
	}
}

func TestDeallsAdapter_ScrapeListingCapsAt100(t *testing.T) {
	// SSR page with 60 jobs; API page 2 with 60 more. Cap at 100.
	listingHTML := deallsListingHTML(1, 60)
	page2Body := deallsExploreAPIJSON(61, 60, 2, 10)

	var page2Hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(listingHTML))
	})
	mux.HandleFunc("/v1/explore-job/job", func(w http.ResponseWriter, r *http.Request) {
		page2Hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page2Body))
	})
	mux.HandleFunc("/loker/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><p>not a detail</p></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 4)
	a.APIBase = srv.URL

	ads, err := a.Scrape(context.Background(), srv.URL+"/?searchJob=developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != deallsMaxListingJobs {
		t.Fatalf("got %d ads, want %d", len(ads), deallsMaxListingJobs)
	}
	if page2Hits != 1 {
		t.Fatalf("page2 hits = %d, want 1", page2Hits)
	}
	seen := map[string]struct{}{}
	for _, ad := range ads {
		if _, ok := seen[ad.SourceURL]; ok {
			t.Fatalf("duplicate SourceURL %q", ad.SourceURL)
		}
		seen[ad.SourceURL] = struct{}{}
	}
}

func TestParseDeallsExploreAPIResponse(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "dealls_explore_job_page2.json"))
	if err != nil {
		t.Fatal(err)
	}
	docs, page, totalPages, ok := parseDeallsExploreAPIResponse(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if page != 2 || totalPages != 2 {
		t.Fatalf("page=%d totalPages=%d", page, totalPages)
	}
	if len(docs) != 1 || docs[0].Slug != "android-developer-sr-specialist" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestDeallsExploreAPIURL(t *testing.T) {
	got := deallsExploreAPIURL("https://api.example.test", "developer", 3)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/v1/explore-job/job" {
		t.Fatalf("path = %q", u.Path)
	}
	q := u.Query()
	if q.Get("page") != "3" || q.Get("search") != "developer" || q.Get("limit") != "18" {
		t.Fatalf("query = %v", q)
	}
}

func deallsListingHTML(startID, count int) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body><div id="jobs-container">`)
	b.WriteString(`<script id="__NEXT_DATA__" type="application/json">`)
	b.WriteString(`{"props":{"pageProps":{"dehydratedState":{"queries":[{"queryKey":["/v1/explore-job/job",{"search":"developer","limit":18}],"state":{"data":{"pages":[{"docs":[`)
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		id := startID + i
		fmt.Fprintf(&b, `{"id":"%d","slug":"job-%d","role":"Job %d","salaryType":"paid","salaryRange":null,"company":{"name":"Company %d","slug":"company-%d"}}`, id, id, id, id, id)
	}
	// Root shape matches dealls_listing.html: props + page + query siblings.
	b.WriteString(`],"totalDocs":100,"totalPages":10,"page":1}],"pageParams":[1]},"status":"success"}}]}}},"page":"/","query":{"searchJob":"developer"}}`)
	b.WriteString(`</script></div></body></html>`)
	return b.String()
}

func deallsExploreAPIJSON(startID, count, page, totalPages int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"code":200,"data":{"docs":[`)
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		id := startID + i
		fmt.Fprintf(&b, `{"id":"%d","slug":"job-%d","role":"Job %d","salaryType":"paid","salaryRange":null,"company":{"name":"Company %d","slug":"company-%d"}}`, id, id, id, id, id)
	}
	fmt.Fprintf(&b, `],"page":%d,"totalPages":%d,"totalDocs":%d}}`, page, totalPages, startID+count-1)
	return b.String()
}

func TestDeallsAdapter_ScrapeDetailOnly(t *testing.T) {
	detailHTML, err := os.ReadFile(filepath.Join("testdata", "dealls_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/loker/senior-compliance-manager~fintureid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)

	ads, err := a.Scrape(context.Background(), srv.URL+"/loker/senior-compliance-manager~fintureid")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 1 {
		t.Fatalf("got %d ads, want 1", len(ads))
	}
	if got, want := ads[0].Company, "Finture"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
}

func TestIsDeallsDetailURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://dealls.com/loker/senior-compliance-manager~fintureid", true},
		{"https://dealls.com/loker/senior-compliance-manager~fintureid?utm=x#y", true},
		{"https://dealls.com/loker/saved", false},
		{"https://dealls.com/loker/applied", false},
		{"https://dealls.com/?searchJob=developer", false},
		{"https://dealls.com/", false},
	}
	for _, tc := range cases {
		if got := isDeallsDetailURL(tc.url); got != tc.want {
			t.Errorf("isDeallsDetailURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestCanonicalizeDeallsURL(t *testing.T) {
	got := canonicalizeDeallsURL("https://dealls.com/loker/senior-compliance-manager~fintureid?utm=x#frag")
	want := "https://dealls.com/loker/senior-compliance-manager~fintureid"
	if got != want {
		t.Errorf("canonicalizeDeallsURL = %q, want %q", got, want)
	}
}

func TestFormatDeallsSalary(t *testing.T) {
	cases := []struct {
		salaryType string
		rng        *deallsSalaryRange
		want       string
	}{
		{"unpaid", nil, deallsSalaryUnpaid},
		{"paid", nil, deallsSalaryNegotiable},
		{"paid", &deallsSalaryRange{Start: 6000000, End: 7500000}, "Rp6.000.000 – Rp7.500.000"},
		{"paid", &deallsSalaryRange{Start: 10000000, End: 10000000}, "Rp10.000.000"},
	}
	for _, tc := range cases {
		if got := formatDeallsSalary(tc.salaryType, tc.rng); got != tc.want {
			t.Errorf("formatDeallsSalary(%q, %+v) = %q, want %q", tc.salaryType, tc.rng, got, tc.want)
		}
	}
}
