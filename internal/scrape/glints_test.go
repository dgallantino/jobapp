package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func loadGlintsFixture(t *testing.T, name string) *goquery.Document {
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

func TestParseGlintsDetail_WithSalary(t *testing.T) {
	doc := loadGlintsFixture(t, "glints_detail.html")
	pageURL := "https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163?utm_referrer=explore&traceInfo=abc"

	ad, err := parseGlintsDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseGlintsDetail: %v", err)
	}

	if got, want := ad.Title, "Senior Backend Developer"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT. Bukuku Solusi Kreatif"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, "Rp 7,000,000 - 10,000,000 / Month"; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "memperkuat tim engineering") {
		t.Errorf("Description missing expected snippet: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\nTanggung Jawab Utama\n") &&
		!strings.HasPrefix(ad.Description, "Tanggung Jawab Utama\n") {
		t.Errorf("Description missing section break before Tanggung Jawab Utama: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\n• Merancang, mengembangkan, dan memelihara RESTful/GraphQL API") {
		t.Errorf("Description missing bulleted list item: %q", ad.Description)
	}
	if got, want := ad.SourceURL, "https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
}

func TestParseGlintsDetail_NoSalary(t *testing.T) {
	doc := loadGlintsFixture(t, "glints_detail_no_salary.html")
	pageURL := "https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163"

	ad, err := parseGlintsDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseGlintsDetail: %v", err)
	}

	if got, want := ad.Title, "Senior Backend Developer"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT. Bukuku Solusi Kreatif"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, glintsSalaryUndisclosed; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "\n• Merancang, mengembangkan") {
		t.Errorf("Description missing bulleted list item: %q", ad.Description)
	}
}

func TestParseGlintsDetail_MissingTitle(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<html><body><p>no title</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseGlintsDetail(doc, "https://glints.com/id/en/opportunities/jobs/x/c4cb6d60-050b-4710-9092-4e8cef9d2163")
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestParseGlintsListing(t *testing.T) {
	doc := loadGlintsFixture(t, "glints_listing.html")
	base := "https://glints.com/id/en/opportunities/jobs/explore?keyword=backend&country=ID"

	ads := parseGlintsListing(doc, base)
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2 (deduped)", len(ads))
	}

	if got, want := ads[0].SourceURL, "https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Senior Backend Developer"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if got, want := ads[0].Company, "PT. Bukuku Solusi Kreatif"; got != want {
		t.Errorf("ads[0].Company = %q, want %q", got, want)
	}
	if got, want := ads[0].Salary, "Rp 7,000,000 - 10,000,000 / Month"; got != want {
		t.Errorf("ads[0].Salary = %q, want %q", got, want)
	}

	if got, want := ads[1].SourceURL, "https://glints.com/id/en/opportunities/jobs/backend-engineer/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"; got != want {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[1].Title, "Backend Engineer"; got != want {
		t.Errorf("ads[1].Title = %q, want %q", got, want)
	}
	if got, want := ads[1].Company, "Example Corp"; got != want {
		t.Errorf("ads[1].Company = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, glintsSalaryUndisclosed; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if ads[1].Description != "" {
		t.Errorf("listing description should be empty, got %q", ads[1].Description)
	}
}

func TestGlintsAdapter_ListingEnrichesDetails(t *testing.T) {
	detailWithSalary, err := os.ReadFile(filepath.Join("testdata", "glints_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailNoSalary, err := os.ReadFile(filepath.Join("testdata", "glints_detail_no_salary.html"))
	if err != nil {
		t.Fatal(err)
	}

	id1 := "c4cb6d60-050b-4710-9092-4e8cef9d2163"
	id2 := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	mux := http.NewServeMux()
	mux.HandleFunc("/id/en/opportunities/jobs/senior-backend-developer/"+id1, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailWithSalary)
	})
	mux.HandleFunc("/id/en/opportunities/jobs/backend-engineer/"+id2, func(w http.ResponseWriter, r *http.Request) {
		html := string(detailNoSalary)
		html = strings.ReplaceAll(html, "Senior Backend Developer", "Backend Engineer")
		html = strings.ReplaceAll(html, "PT. Bukuku Solusi Kreatif", "Example Corp")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	listingDoc := loadGlintsFixture(t, "glints_listing.html")
	stubs := parseGlintsListing(listingDoc, srv.URL+"/id/en/opportunities/jobs/explore")
	if len(stubs) != 2 {
		t.Fatalf("got %d stubs, want 2", len(stubs))
	}

	a := NewGlintsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)
	ads := a.enrichListingDetails(context.Background(), stubs)

	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2", len(ads))
	}

	want0 := srv.URL + "/id/en/opportunities/jobs/senior-backend-developer/" + id1
	if got := ads[0].SourceURL; got != want0 {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want0)
	}
	if got, want := ads[0].Title, "Senior Backend Developer"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if !strings.Contains(ads[0].Description, "memperkuat tim engineering") {
		t.Errorf("ads[0].Description missing detail snippet: %q", ads[0].Description)
	}
	if got, want := ads[0].Salary, "Rp 7,000,000 - 10,000,000 / Month"; got != want {
		t.Errorf("ads[0].Salary = %q, want %q", got, want)
	}

	want1 := srv.URL + "/id/en/opportunities/jobs/backend-engineer/" + id2
	if got := ads[1].SourceURL; got != want1 {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want1)
	}
	if got, want := ads[1].Title, "Backend Engineer"; got != want {
		t.Errorf("ads[1].Title = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, glintsSalaryUndisclosed; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ads[1].Description, "Merancang, mengembangkan") {
		t.Errorf("ads[1].Description missing detail snippet: %q", ads[1].Description)
	}
}

func TestGlintsAdapter_ScrapeDetailOnly(t *testing.T) {
	detailHTML, err := os.ReadFile(filepath.Join("testdata", "glints_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	path := "/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163"
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewGlintsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)

	ads, err := a.Scrape(context.Background(), srv.URL+path+"?utm_referrer=explore")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 1 {
		t.Fatalf("got %d ads, want 1", len(ads))
	}
	if got, want := ads[0].Title, "Senior Backend Developer"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ads[0].SourceURL, srv.URL+path; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
}

func TestIsGlintsDetailURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163", true},
		{"https://glints.com/id/en/opportunities/jobs/senior-backend-developer/c4cb6d60-050b-4710-9092-4e8cef9d2163?utm=1", true},
		{"https://glints.com/id/opportunities/jobs/backend/c4cb6d60-050b-4710-9092-4e8cef9d2163", true},
		{"https://glints.com/opportunities/jobs/backend/c4cb6d60-050b-4710-9092-4e8cef9d2163", true},
		{"https://glints.com/id/en/opportunities/jobs/explore?keyword=x", false},
		{"https://glints.com/id/en/opportunities/jobs/bookmarked", false},
		{"https://glints.com/", false},
	}
	for _, tc := range cases {
		if got := isGlintsDetailURL(tc.url); got != tc.want {
			t.Errorf("isGlintsDetailURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestParseGlintsListing_CapsAtMax(t *testing.T) {
	// Cap is applied in Scrape after parse; verify parse dedupes and we can trim.
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body>`)
	for i := 0; i < glintsMaxListingJobs+5; i++ {
		id := fmt.Sprintf("%08x-bbbb-cccc-dddd-%012x", i, i)
		fmt.Fprintf(&b, `
<article data-gtm-job-id="%d" data-gtm-company-name="Co %d">
  <a href="/id/en/opportunities/jobs/role-%d/%s"><span class="JobTitle">Job %d</span></a>
</article>`, i, i, i, id, i)
	}
	b.WriteString(`</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	ads := parseGlintsListing(doc, "https://glints.com/id/en/opportunities/jobs/explore")
	if len(ads) != glintsMaxListingJobs+5 {
		t.Fatalf("got %d ads before cap, want %d", len(ads), glintsMaxListingJobs+5)
	}
	if len(ads) > glintsMaxListingJobs {
		ads = ads[:glintsMaxListingJobs]
	}
	if len(ads) != glintsMaxListingJobs {
		t.Fatalf("got %d ads after cap, want %d", len(ads), glintsMaxListingJobs)
	}
}
