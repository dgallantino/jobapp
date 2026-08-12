package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func loadJobstreetFixture(t *testing.T, name string) *goquery.Document {
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

func TestTextWithBreaks_ListItemWrappedInParagraph(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<div>
			<ul>
				<li><p>Confirms project requirements by reviewing program objective, input data, and output requirements</p></li>
				<li>
					<p>Second item with leading whitespace before the paragraph</p>
				</li>
			</ul>
		</div>
	`))
	if err != nil {
		t.Fatal(err)
	}
	got := textWithBreaks(doc.Find("div").First())
	if !strings.Contains(got, "• Confirms project requirements by reviewing program objective, input data, and output requirements\n") {
		t.Errorf("bullet should sit on same line as <li><p> text, got:\n%s", got)
	}
	if !strings.Contains(got, "• Second item with leading whitespace before the paragraph") {
		t.Errorf("bullet should sit on same line when whitespace precedes <p>, got:\n%s", got)
	}
	if strings.Contains(got, "•\n") {
		t.Errorf("orphan bullet line should not appear, got:\n%s", got)
	}
}

func TestParseJobstreetDetail_WithSalary(t *testing.T) {
	doc := loadJobstreetFixture(t, "jobstreet_detail_with_salary.html")
	pageURL := "https://id.jobstreet.com/job/93782463?ref=recom-homepage&origin=showNewTab#sol=abc"

	ad, err := parseJobstreetDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseJobstreetDetail: %v", err)
	}

	if got, want := ad.Title, "Backend Developer — Support (Project)"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT. KIMIA FARMA (PERSERO) TBK."; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, "Rp 15.000.000 – Rp 22.000.000 per month"; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "backup teknis") {
		t.Errorf("Description missing expected snippet: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\nTanggung Jawab :\n") {
		t.Errorf("Description missing section break before Tanggung Jawab: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\n• Mengimplementasikan service backend") {
		t.Errorf("Description missing bulleted list item: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\n• Minimal 3 tahun pengalaman") {
		t.Errorf("Description should keep existing bullet prefix: %q", ad.Description)
	}
	if strings.Contains(ad.Description, "• •") {
		t.Errorf("Description double-prefixed bullets: %q", ad.Description)
	}
	if got, want := ad.SourceURL, "https://id.jobstreet.com/job/93782463"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
}

func TestParseJobstreetDetail_NoSalary(t *testing.T) {
	doc := loadJobstreetFixture(t, "jobstreet_detail_no_salary.html")
	pageURL := "https://id.jobstreet.com/job/93515969?ref=recom-homepage#sol=xyz"

	ad, err := parseJobstreetDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseJobstreetDetail: %v", err)
	}

	if got, want := ad.Title, "Full Stack Engineer"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT. Bangkit Lakuliner Indonesia"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, jobstreetSalaryUndisclosed; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if got, want := ad.SourceURL, "https://id.jobstreet.com/job/93515969"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "\nKey Responsibilities\n") &&
		!strings.HasPrefix(ad.Description, "Key Responsibilities\n") {
		t.Errorf("Description missing structured headings: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "\n• Experience with Odoo\n") {
		t.Errorf("Description missing bulleted requirement: %q", ad.Description)
	}
}

func TestParseJobstreetDetail_MissingTitle(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<html><body><p>no title</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseJobstreetDetail(doc, "https://id.jobstreet.com/job/1")
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestParseJobstreetListing(t *testing.T) {
	doc := loadJobstreetFixture(t, "jobstreet_listing.html")
	base := "https://id.jobstreet.com/id/backend-developer-jobs"

	ads := parseJobstreetListing(doc, base)
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2 (deduped)", len(ads))
	}

	if got, want := ads[0].SourceURL, "https://id.jobstreet.com/id/job/93713877"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Backend Developer"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if got, want := ads[0].Company, "PT Intersolusi Teknologi Asia"; got != want {
		t.Errorf("ads[0].Company = %q, want %q", got, want)
	}
	if got, want := ads[0].Salary, "Rp 5.000.000 – Rp 7.500.000 per month"; got != want {
		t.Errorf("ads[0].Salary = %q, want %q", got, want)
	}

	if got, want := ads[1].SourceURL, "https://id.jobstreet.com/id/job/93828612"; got != want {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, jobstreetSalaryUndisclosed; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if ads[1].Description != "" {
		t.Errorf("listing description should be empty, got %q", ads[1].Description)
	}
}

func TestJobstreetAdapter_ScrapeListingEnrichesDetails(t *testing.T) {
	listingHTML, err := os.ReadFile(filepath.Join("testdata", "jobstreet_listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailWithSalary, err := os.ReadFile(filepath.Join("testdata", "jobstreet_detail_with_salary.html"))
	if err != nil {
		t.Fatal(err)
	}
	detailNoSalary, err := os.ReadFile(filepath.Join("testdata", "jobstreet_detail_no_salary.html"))
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/id/backend-developer-jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(listingHTML)
	})
	mux.HandleFunc("/id/job/93713877", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailWithSalary)
	})
	mux.HandleFunc("/id/job/93828612", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailNoSalary)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewJobstreetAdapter(nil, 2)
	a.Client = srv.Client()

	ads, err := a.Scrape(context.Background(), srv.URL+"/id/backend-developer-jobs")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2", len(ads))
	}

	if got, want := ads[0].SourceURL, srv.URL+"/id/job/93713877"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Backend Developer — Support (Project)"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if !strings.Contains(ads[0].Description, "backup teknis") {
		t.Errorf("ads[0].Description missing detail snippet: %q", ads[0].Description)
	}

	if got, want := ads[1].SourceURL, srv.URL+"/id/job/93828612"; got != want {
		t.Errorf("ads[1].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[1].Title, "Full Stack Engineer"; got != want {
		t.Errorf("ads[1].Title = %q, want %q", got, want)
	}
	if got, want := ads[1].Salary, jobstreetSalaryUndisclosed; got != want {
		t.Errorf("ads[1].Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ads[1].Description, "Experience with Odoo") {
		t.Errorf("ads[1].Description missing detail snippet: %q", ads[1].Description)
	}
}

func TestIsJobstreetDetailURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://id.jobstreet.com/job/93782463", true},
		{"https://id.jobstreet.com/job/93782463?ref=x#sol=y", true},
		{"https://id.jobstreet.com/id/job/93713877", true},
		{"https://id.jobstreet.com/id/backend-developer-jobs", false},
		{"https://id.jobstreet.com/", false},
	}
	for _, tc := range cases {
		if got := isJobstreetDetailURL(tc.url); got != tc.want {
			t.Errorf("isJobstreetDetailURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestCanonicalizeJobstreetURL(t *testing.T) {
	got := canonicalizeJobstreetURL("https://id.jobstreet.com/job/93782463?ref=recom-homepage&origin=showNewTab#sol=abc")
	want := "https://id.jobstreet.com/job/93782463"
	if got != want {
		t.Errorf("canonicalizeJobstreetURL = %q, want %q", got, want)
	}
}
