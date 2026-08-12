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

	a := NewDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)

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

	a := NewDeallsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)

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
