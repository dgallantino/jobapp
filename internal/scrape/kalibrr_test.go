package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func loadKalibrrFixture(t *testing.T, name string) *goquery.Document {
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

func TestParseKalibrrDetail_WithSalary(t *testing.T) {
	doc := loadKalibrrFixture(t, "kalibrr_detail_with_salary.html")
	pageURL := "https://www.kalibrr.id/id-ID/c/pt-agung-putra-niaga-mandiri/jobs/270223/accounting-and-tax-manager?utm=x#frag"

	ad, err := parseKalibrrDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseKalibrrDetail: %v", err)
	}

	if got, want := ad.Title, "Accounting and Tax Manager"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "PT Agung Putra Niaga Mandiri"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, "Rp14.000.000 – Rp17.000.000 / bulan"; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if got, want := ad.SourceURL, "https://www.kalibrr.id/id-ID/c/pt-agung-putra-niaga-mandiri/jobs/270223/accounting-and-tax-manager"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "Deskripsi Pekerjaan\n") {
		t.Errorf("Description missing Deskripsi heading: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "Kualifikasi Minimum\n") {
		t.Errorf("Description missing Kualifikasi heading: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "Mengembangkan strategi accounting") {
		t.Errorf("Description missing responsibility: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "Pendidikan minimal S1 Akuntansi") {
		t.Errorf("Description missing qualification: %q", ad.Description)
	}
}

func TestParseKalibrrDetail_NoSalary(t *testing.T) {
	doc := loadKalibrrFixture(t, "kalibrr_detail_no_salary.html")
	pageURL := "https://www.kalibrr.id/id-ID/c/ntt-indonesia/jobs/266442/software-developer-2"

	ad, err := parseKalibrrDetail(doc, pageURL)
	if err != nil {
		t.Fatalf("parseKalibrrDetail: %v", err)
	}

	if got, want := ad.Title, "Software Developer"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ad.Company, "NTT INDONESIA TECHNOLOGY"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if got, want := ad.Salary, kalibrrSalaryUndisclosed; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ad.Description, "Develop quality software") {
		t.Errorf("Description missing responsibility: %q", ad.Description)
	}
	if !strings.Contains(ad.Description, "2 year experience as Developer") {
		t.Errorf("Description missing qualification: %q", ad.Description)
	}
}

func TestParseKalibrrDetail_MissingJob(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<html><body><p>no data</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseKalibrrDetail(doc, "https://www.kalibrr.id/id-ID/c/x/jobs/1/y")
	if err == nil {
		t.Fatal("expected error for missing job data")
	}
}

func TestKalibrrAdapter_ScrapeDetail(t *testing.T) {
	detailHTML, err := os.ReadFile(filepath.Join("testdata", "kalibrr_detail_with_salary.html"))
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/id-ID/c/pt-agung-putra-niaga-mandiri/jobs/270223/accounting-and-tax-manager", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(detailHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newKalibrrAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)
	pageURL := srv.URL + "/id-ID/c/pt-agung-putra-niaga-mandiri/jobs/270223/accounting-and-tax-manager?utm=1"
	ads, err := a.Scrape(context.Background(), pageURL)
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 1 {
		t.Fatalf("got %d ads, want 1", len(ads))
	}
	if got, want := ads[0].Title, "Accounting and Tax Manager"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := ads[0].Salary, "Rp14.000.000 – Rp17.000.000 / bulan"; got != want {
		t.Errorf("Salary = %q, want %q", got, want)
	}
}

func TestKalibrrAdapter_ScrapeListingAPIOnly(t *testing.T) {
	page1, err := os.ReadFile(filepath.Join("testdata", "kalibrr_search_page1.json"))
	if err != nil {
		t.Fatal(err)
	}

	var searchHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/kjs/job_board/search", func(w http.ResponseWriter, r *http.Request) {
		searchHits++
		if r.URL.Query().Get("text") != "developer" {
			t.Errorf("unexpected text=%q", r.URL.Query().Get("text"))
		}
		if r.URL.Query().Get("limit") != strconv.Itoa(kalibrrSearchPageSize) {
			t.Errorf("unexpected limit=%q", r.URL.Query().Get("limit"))
		}
		if r.URL.Query().Get("offset") != "0" {
			t.Errorf("unexpected offset=%q (count=3 should stop after first page)", r.URL.Query().Get("offset"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(page1)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected non-API request: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newKalibrrAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 2)
	a.APIBase = srv.URL

	ads, err := a.Scrape(context.Background(), srv.URL+"/id-ID/home/te/developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 3 {
		t.Fatalf("got %d ads, want 3", len(ads))
	}
	if searchHits != 1 {
		t.Fatalf("searchHits=%d, want 1", searchHits)
	}

	if got, want := ads[0].SourceURL, srv.URL+"/id-ID/c/pt-agung-putra-niaga-mandiri/jobs/270223/accounting-and-tax-manager"; got != want {
		t.Errorf("ads[0].SourceURL = %q, want %q", got, want)
	}
	if got, want := ads[0].Title, "Accounting and Tax Manager"; got != want {
		t.Errorf("ads[0].Title = %q, want %q", got, want)
	}
	if got, want := ads[0].Salary, "Rp14.000.000 – Rp17.000.000 / bulan"; got != want {
		t.Errorf("ads[0].Salary = %q, want %q", got, want)
	}
	if !strings.Contains(ads[0].Description, "Mengembangkan strategi accounting") {
		t.Errorf("ads[0].Description missing API snippet: %q", ads[0].Description)
	}

	if got, want := ads[1].Salary, kalibrrSalaryUndisclosed; got != want {
		t.Errorf("ads[1].Salary = %q, want %q (salary_shown=false)", got, want)
	}
	if !strings.Contains(ads[1].Description, "Develop quality software") {
		t.Errorf("ads[1].Description missing API snippet: %q", ads[1].Description)
	}

	if got, want := ads[2].Title, "Developer"; got != want {
		t.Errorf("ads[2].Title = %q, want %q", got, want)
	}
	if got, want := ads[2].Salary, kalibrrSalaryUndisclosed; got != want {
		t.Errorf("ads[2].Salary = %q, want %q", got, want)
	}
}

func TestKalibrrAdapter_ScrapeListingWalksPages(t *testing.T) {
	page2, err := os.ReadFile(filepath.Join("testdata", "kalibrr_search_page2.json"))
	if err != nil {
		t.Fatal(err)
	}

	var offsets []string
	mux := http.NewServeMux()
	mux.HandleFunc("/kjs/job_board/search", func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			// 15 stub jobs, count high enough to allow offset=15.
			_, _ = w.Write([]byte(kalibrrSearchAPIJSON(1, 15, 30)))
		case "15":
			_, _ = w.Write(page2)
		default:
			_, _ = w.Write([]byte(`{"count":30,"jobs":[]}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newKalibrrAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)
	a.APIBase = srv.URL
	ads, err := a.Scrape(context.Background(), srv.URL+"/id-ID/home/te/developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 16 {
		t.Fatalf("got %d ads, want 16", len(ads))
	}
	if len(offsets) != 2 || offsets[0] != "0" || offsets[1] != "15" {
		t.Fatalf("offsets=%v, want [0 15]", offsets)
	}
	if got, want := ads[15].Title, "Backend Engineer"; got != want {
		t.Errorf("ads[15].Title = %q, want %q", got, want)
	}
}

func TestKalibrrAdapter_ScrapeListingStopsAtCount(t *testing.T) {
	// count=2 with page size 15 means only offset=0 should be fetched.
	var offsets []string
	mux := http.NewServeMux()
	mux.HandleFunc("/kjs/job_board/search", func(w http.ResponseWriter, r *http.Request) {
		offsets = append(offsets, r.URL.Query().Get("offset"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"count": 2,
			"jobs": [
				{"id":1,"name":"A","slug":"a","company_name":"Co","description":"<p>d</p>","qualifications":"","salary_shown":false,"company":{"code":"co","name":"Co"}},
				{"id":2,"name":"B","slug":"b","company_name":"Co","description":"<p>d</p>","qualifications":"","salary_shown":false,"company":{"code":"co","name":"Co"}}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newKalibrrAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)
	a.APIBase = srv.URL
	ads, err := a.Scrape(context.Background(), srv.URL+"/id-ID/home/te/developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 2 {
		t.Fatalf("got %d ads, want 2", len(ads))
	}
	if len(offsets) != 1 || offsets[0] != "0" {
		t.Fatalf("offsets=%v, want only [0]", offsets)
	}
}

func TestKalibrrAdapter_ScrapeListingCapsAt100(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kjs/job_board/search", func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(kalibrrSearchAPIJSON(offset+1, 15, 500)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newKalibrrAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), 1)
	a.APIBase = srv.URL
	ads, err := a.Scrape(context.Background(), srv.URL+"/id-ID/home/te/developer")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != kalibrrMaxListingJobs {
		t.Fatalf("got %d ads, want %d", len(ads), kalibrrMaxListingJobs)
	}
}

func TestIsKalibrrDetailURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://www.kalibrr.id/id-ID/c/ntt-indonesia/jobs/266442/software-developer-2", true},
		{"https://www.kalibrr.id/id-ID/c/ntt-indonesia/jobs/266442/software-developer-2?utm=x#y", true},
		{"https://www.kalibrr.id/c/ntt-indonesia/jobs/266442/software-developer-2", true},
		{"https://www.kalibrr.id/id-ID/home/te/developer", false},
		{"https://www.kalibrr.id/", false},
		{"https://www.kalibrr.id/id-ID/c/ntt-indonesia/jobs/not-a-number/slug", false},
	}
	for _, tc := range tests {
		if got := isKalibrrDetailURL(tc.raw); got != tc.want {
			t.Errorf("isKalibrrDetailURL(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestCanonicalizeKalibrrURL(t *testing.T) {
	got := canonicalizeKalibrrURL("https://www.kalibrr.id/id-ID/c/x/jobs/1/y/?utm=x#frag")
	want := "https://www.kalibrr.id/id-ID/c/x/jobs/1/y"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestKalibrrSearchFromListingURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://www.kalibrr.id/id-ID/home/te/developer", "developer"},
		{"https://www.kalibrr.id/home/te/golang", "golang"},
		{"https://www.kalibrr.id/id-ID/home/te/full%20stack", "full stack"},
		{"https://www.kalibrr.id/id-ID/c/x/jobs/1/y", ""},
	}
	for _, tc := range tests {
		if got := kalibrrSearchFromListingURL(tc.raw); got != tc.want {
			t.Errorf("kalibrrSearchFromListingURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestFormatKalibrrSalary(t *testing.T) {
	shown := true
	hidden := false
	base := int64(14000000)
	max := int64(17000000)
	only := int64(10000000)

	tests := []struct {
		name string
		job  kalibrrJobJSON
		want string
	}{
		{
			name: "hidden even with amounts",
			job:  kalibrrJobJSON{SalaryShown: &hidden, BaseSalary: &base, MaximumSalary: &max, SalaryInterval: "month"},
			want: kalibrrSalaryUndisclosed,
		},
		{
			name: "range monthly",
			job:  kalibrrJobJSON{SalaryShown: &shown, BaseSalary: &base, MaximumSalary: &max, SalaryInterval: "month"},
			want: "Rp14.000.000 – Rp17.000.000 / bulan",
		},
		{
			name: "single amount",
			job:  kalibrrJobJSON{SalaryShown: &shown, BaseSalary: &only, MaximumSalary: &only, SalaryInterval: "year"},
			want: "Rp10.000.000 / tahun",
		},
		{
			name: "shown but no amounts",
			job:  kalibrrJobJSON{SalaryShown: &shown},
			want: kalibrrSalaryUndisclosed,
		},
		{
			name: "nil shown and no amounts",
			job:  kalibrrJobJSON{},
			want: kalibrrSalaryUndisclosed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatKalibrrSalary(tc.job); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKalibrrSearchAPIURL(t *testing.T) {
	got := kalibrrSearchAPIURL("https://api.example.test", "developer", 30)
	want := "https://api.example.test/kjs/job_board/search?limit=15&offset=30&text=developer"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func kalibrrSearchAPIJSON(startID, count, totalCount int) string {
	var b strings.Builder
	b.WriteString(`{"count":`)
	b.WriteString(strconv.Itoa(totalCount))
	b.WriteString(`,"jobs":[`)
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		id := startID + i
		fmt.Fprintf(&b, `{"id":%d,"name":"Job %d","slug":"job-%d","company_name":"Co","description":"<p>d</p>","qualifications":"","salary_shown":false,"company":{"code":"co","name":"Co"}}`, id, id, id)
	}
	b.WriteString(`]}`)
	return b.String()
}
