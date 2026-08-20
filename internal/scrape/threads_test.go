package scrape

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadThreadsFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

type stubExtractor struct {
	title, company, salary string
	err                    error
	gotMissing             []string
	gotText                string
}

func (s *stubExtractor) ExtractJobFields(_ context.Context, postText string, missing []string) (string, string, string, error) {
	s.gotText = postText
	s.gotMissing = append([]string(nil), missing...)
	return s.title, s.company, s.salary, s.err
}

func TestParseThreadsPost_PrefersMatchingPlaintextOverDecoy(t *testing.T) {
	html := loadThreadsFixture(t, "threads_post.html")
	pageURL := "https://www.threads.com/@job.board/post/AbC123xyz?xmt=tracking"

	ad, caption, err := parseThreadsPost(html, pageURL)
	if err != nil {
		t.Fatalf("parseThreadsPost: %v", err)
	}

	if got, want := ad.SourceURL, "https://www.threads.com/@job.board/post/AbC123xyz"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(caption, "Email hr@example.com") {
		t.Errorf("caption should use longer matching plaintext, got %q", caption)
	}
	if strings.Contains(caption, "horoscope") {
		t.Errorf("caption picked related-thread decoy: %q", caption)
	}
	if !strings.Contains(ad.Description, ad.SourceURL) {
		t.Errorf("Description should start with canonical URL, got %q", ad.Description)
	}
	if !strings.Contains(ad.Description, caption) {
		t.Errorf("Description should include caption, got %q", ad.Description)
	}
	if got, want := ad.Title, "Warehouse Clerk"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if ad.Company != "" {
		t.Errorf("Company = %q, want empty (handle is not the employer)", ad.Company)
	}
	if !strings.Contains(strings.ToUpper(ad.Salary), "UMR") {
		t.Errorf("Salary = %q, want UMR", ad.Salary)
	}
}

func TestParseThreadsPost_NoCaption(t *testing.T) {
	_, _, err := parseThreadsPost(`<html><head></head><body>login wall</body></html>`, "https://www.threads.com/@x/post/y")
	if err == nil {
		t.Fatal("expected error for empty caption")
	}
}

func TestCanonicalThreadsURL(t *testing.T) {
	got := canonicalThreadsURL(
		"https://www.threads.net/@Acme/post/Zz9?xmt=abc#frag",
		"https://www.threads.com/@Acme/post/Zz9",
	)
	if want := "https://www.threads.com/@Acme/post/Zz9"; got != want {
		t.Errorf("canonical = %q, want %q", got, want)
	}
}

func TestExtractThreadsFields(t *testing.T) {
	tests := []struct {
		name            string
		caption         string
		wantTitle       string
		wantCompany     string
		wantSalarySub   string
		wantEmptySalary bool
		wantEmptyTitle  bool
		wantEmptyCo     bool
	}{
		{
			name:          "hiring dash role with UMR",
			caption:       "We're Hiring – Warehouse Clerk\nJoin our team in Batam.\nGaji: UMR",
			wantTitle:     "Warehouse Clerk",
			wantSalarySub: "UMR",
			wantEmptyCo:   true,
		},
		{
			name:            "hiring pipe role strips location emoji",
			caption:         "We’re Hiring | Odoo Programmer 📍 Surabaya\nKualifikasi:\n* Python\nKirim CV ke recruitment@vendor.co.id",
			wantTitle:       "Odoo Programmer",
			wantEmptyCo:     true,
			wantEmptySalary: true,
		},
		{
			name:          "rupiah range without company",
			caption:       "Urgent Needed\nLokasi : Jakarta Selatan\n• Gaji Perbulan Rp. 3.500.000 - Rp. 9.000.000\n📌 POSISI\n• Staff Office",
			wantSalarySub: "Rp",
			wantEmptyCo:   true,
		},
		{
			name:           "hiring emdash org and SGD range",
			caption:        "WE’RE HIRING — NESTLÉ SINGAPORE\nFactory & Production Staff\n📍 Singapore\n💰 SGD 2,300–3,500/month\nApply: https://example.com/apply",
			wantCompany:    "NESTLÉ SINGAPORE",
			wantSalarySub:  "SGD",
			wantEmptyTitle: true, // org captured from hiring line; role is on the next line
		},
		{
			name:            "PT company multi-role no numeric salary",
			caption:         "LOWONGAN KERJA PT PARAGON TECHNOLOGY AND INNOVATION\nPosisi: Marketing, R&D, IT\nBenefit: Gaji kompetitif, BPJS",
			wantCompany:     "PT PARAGON TECHNOLOGY AND INNOVATION",
			wantEmptySalary: true,
		},
		{
			name:            "ltd suffix company",
			caption:         "Now hiring | Line Cook\nJoin Harbor Bites Pte Ltd in Tanjong Pagar.",
			wantTitle:       "Line Cook",
			wantCompany:     "Harbor Bites Pte Ltd",
			wantEmptySalary: true,
		},
		{
			name:            "aggregator handle is not company",
			caption:         "Hiring | Night Auditor\nDrop CV on WhatsApp +62 811-0000-0000",
			wantTitle:       "Night Auditor",
			wantEmptyCo:     true,
			wantEmptySalary: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, company, salary := extractThreadsFields(tc.caption)
			if tc.wantTitle != "" && title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", title, tc.wantTitle)
			}
			if tc.wantEmptyTitle && title != "" {
				t.Errorf("Title = %q, want empty", title)
			}
			if tc.wantCompany != "" && company != tc.wantCompany {
				t.Errorf("Company = %q, want %q", company, tc.wantCompany)
			}
			if tc.wantEmptyCo && company != "" {
				t.Errorf("Company = %q, want empty", company)
			}
			if tc.wantSalarySub != "" && !strings.Contains(strings.ToUpper(salary), strings.ToUpper(tc.wantSalarySub)) {
				t.Errorf("Salary = %q, want substring %q", salary, tc.wantSalarySub)
			}
			if tc.wantEmptySalary && salary != "" {
				t.Errorf("Salary = %q, want empty", salary)
			}
		})
	}
}

func TestThreadsAdapter_ScrapeMergesLLMForEmptyFieldsOnly(t *testing.T) {
	html := loadThreadsFixture(t, "threads_post.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	stub := &stubExtractor{company: "Batam Goods", title: "should-not-overwrite", salary: "should-not-overwrite"}
	adapter := newThreadsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), stub)

	ads, err := adapter.Scrape(t.Context(), srv.URL+"/@job.board/post/AbC123xyz")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 1 {
		t.Fatalf("got %d ads, want 1", len(ads))
	}
	ad := ads[0]
	if ad.Title != "Warehouse Clerk" {
		t.Errorf("Title overwritten by LLM = %q", ad.Title)
	}
	if ad.Company != "Batam Goods" {
		t.Errorf("Company = %q, want LLM fill", ad.Company)
	}
	if !strings.Contains(strings.ToUpper(ad.Salary), "UMR") {
		t.Errorf("Salary overwritten by LLM = %q", ad.Salary)
	}
	if !strings.Contains(stub.gotText, "Email hr@example.com") {
		t.Errorf("extractor should receive caption, got %q", stub.gotText)
	}
	if strings.Join(stub.gotMissing, ",") != "company" {
		t.Errorf("missing = %v, want [company]", stub.gotMissing)
	}
	if !strings.HasPrefix(ad.Description, ad.SourceURL) {
		t.Errorf("Description should store URL + caption, got %q", ad.Description)
	}
}

func TestThreadsAdapter_LLMErrorKeepsRegex(t *testing.T) {
	html := loadThreadsFixture(t, "threads_post.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	stub := &stubExtractor{err: errors.New("boom"), company: "Nope"}
	adapter := newThreadsAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), stub)
	ads, err := adapter.Scrape(t.Context(), srv.URL+"/@job.board/post/AbC123xyz")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if ads[0].Company != "" {
		t.Errorf("Company = %q after LLM error, want regex empty", ads[0].Company)
	}
}

func TestRegistryResolveThreads(t *testing.T) {
	r := NewRegistry(RegistryOptions{})
	tests := []struct {
		raw  string
		want string
	}{
		{"https://www.threads.com/@x/post/abc", "threads"},
		{"https://threads.net/@x/post/abc", "threads"},
		{"https://www.threads.net/@x/post/abc?xmt=1", "threads"},
		{"https://id.jobstreet.com/job/1", "jobstreet"},
		{"https://example.com/jobs/1", "static"},
	}
	for _, tc := range tests {
		got := r.Resolve(tc.raw).Name()
		if got != tc.want {
			t.Errorf("Resolve(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestIsThreadsHost(t *testing.T) {
	if !isThreadsHost("www.threads.com") || !isThreadsHost("THREADS.NET") {
		t.Fatal("expected threads hosts")
	}
	if isThreadsHost("threadless.com") || isThreadsHost("example.com") {
		t.Fatal("non-threads host matched")
	}
}
