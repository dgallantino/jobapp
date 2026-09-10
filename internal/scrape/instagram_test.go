package scrape

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseInstagramPost_PrefersMatchingCaptionOverDecoy(t *testing.T) {
	html := loadThreadsFixture(t, "instagram_post.html")
	pageURL := "https://www.instagram.com/p/Dcn_WghoDUT/?igsi=tracking"

	ad, caption, err := parseInstagramPost(html, pageURL)
	if err != nil {
		t.Fatalf("parseInstagramPost: %v", err)
	}

	if got, want := ad.SourceURL, "https://www.instagram.com/p/Dcn_WghoDUT"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(caption, "Email hr@example.com") {
		t.Errorf("caption should use longer matching caption.text, got %q", caption)
	}
	if strings.Contains(caption, "Etive") {
		t.Errorf("caption picked related-post decoy: %q", caption)
	}
	if strings.Contains(ad.Description, "Frontend Developer Backend Developer") {
		t.Errorf("Description used image alt text: %q", ad.Description)
	}
	if ad.Description != caption {
		t.Errorf("Description should be original caption, got %q", ad.Description)
	}
	if got, want := ad.Title, "INTERN & FREELANCE POSITIONS!"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if ad.Company != "" {
		t.Errorf("Company = %q, want empty (handle is not the employer)", ad.Company)
	}
	if !strings.Contains(strings.ToUpper(ad.Salary), "UMR") {
		t.Errorf("Salary = %q, want UMR", ad.Salary)
	}
}

func TestParseInstagramPost_NoCaption(t *testing.T) {
	_, _, err := parseInstagramPost(`<html><head></head><body>login wall</body></html>`, "https://www.instagram.com/p/y")
	if err == nil {
		t.Fatal("expected error for empty caption")
	}
}

func TestParseInstagramPost_OgOnlyNoJSON(t *testing.T) {
	html := `<html><head>
<meta property="og:description" content='2,573 likes, 1 comments - maganginfo_ on August 27, 2026: "Info loker dan magang dari ALTO

Your Next Chapter Starts at ALTO!
Apply: https://www.alto.id/career"'>
<meta property="og:url" content="https://www.instagram.com/maganginfo_/p/DciNqTSFBTg/">
</head><body>
<script type="application/json">
{"caption":{"text":"Info internship dari Etive Studio\nUnrelated related-post that must not win."}}
</script>
</body></html>`

	ad, caption, err := parseInstagramPost(html, "https://www.instagram.com/p/DciNqTSFBTg/?img_index=1")
	if err != nil {
		t.Fatalf("parseInstagramPost: %v", err)
	}
	if got, want := ad.SourceURL, "https://www.instagram.com/p/DciNqTSFBTg"; got != want {
		t.Errorf("SourceURL = %q, want %q", got, want)
	}
	if !strings.Contains(caption, "Info loker dan magang dari ALTO") {
		t.Errorf("caption missing og text, got %q", caption)
	}
	if strings.Contains(caption, "Etive") {
		t.Errorf("caption picked related-post JSON: %q", caption)
	}
	if ad.Title != "" {
		t.Errorf("Title = %q, want empty", ad.Title)
	}
	if ad.Company != "" {
		t.Errorf("Company = %q, want empty (aggregator copy)", ad.Company)
	}
	if ad.Salary != "" {
		t.Errorf("Salary = %q, want empty", ad.Salary)
	}
}

func TestCanonicalInstagramURL(t *testing.T) {
	tests := []struct {
		page, og, want string
	}{
		{
			"https://www.instagram.com/p/AbC123/?igsi=abc#frag",
			"https://www.instagram.com/user.name/p/AbC123/",
			"https://www.instagram.com/p/AbC123",
		},
		{
			"https://www.instagram.com/reel/Zz9/?img_index=1",
			"",
			"https://www.instagram.com/p/Zz9",
		},
		{
			"https://instagr.am/p/Tv1/",
			"https://www.instagram.com/acct/tv/Tv1/",
			"https://www.instagram.com/p/Tv1",
		},
	}
	for _, tc := range tests {
		got := canonicalInstagramURL(tc.page, tc.og)
		if got != tc.want {
			t.Errorf("canonicalInstagramURL(%q, %q) = %q, want %q", tc.page, tc.og, got, tc.want)
		}
	}
}

func TestUnstylizeHiringFields(t *testing.T) {
	caption := "𝐖𝐄 𝐀𝐑𝐄 𝐇𝐈𝐑𝐈𝐍𝐆: 𝐈𝐍𝐓𝐄𝐑𝐍 & 𝐅𝐑𝐄𝐄𝐋𝐀𝐍𝐂𝐄 𝐏𝐎𝐒𝐈𝐓𝐈𝐎𝐍𝐒!\nPT Ashira Group\nOpen Positions:\nFront-end Intern"
	title, company, salary := extractThreadsFields(unstylizeLetters(caption))
	if got, want := title, "INTERN & FREELANCE POSITIONS!"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := company, "PT Ashira Group"; got != want {
		t.Errorf("Company = %q, want %q", got, want)
	}
	if salary != "" {
		t.Errorf("Salary = %q, want empty", salary)
	}
}

func TestInstagramAdapter_ScrapeMergesLLMForEmptyFieldsOnly(t *testing.T) {
	html := loadThreadsFixture(t, "instagram_post.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	stub := &stubExtractor{company: "Ashira Group", title: "should-not-overwrite", salary: "should-not-overwrite"}
	adapter := newInstagramAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), stub)

	ads, err := adapter.Scrape(t.Context(), srv.URL+"/p/Dcn_WghoDUT")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(ads) != 1 {
		t.Fatalf("got %d ads, want 1", len(ads))
	}
	ad := ads[0]
	if ad.Title != "INTERN & FREELANCE POSITIONS!" {
		t.Errorf("Title overwritten by LLM = %q", ad.Title)
	}
	if ad.Company != "Ashira Group" {
		t.Errorf("Company = %q, want LLM fill", ad.Company)
	}
	if !strings.Contains(strings.ToUpper(ad.Salary), "UMR") {
		t.Errorf("Salary overwritten by LLM = %q", ad.Salary)
	}
	if !strings.Contains(stub.gotText, "Email hr@example.com") {
		t.Errorf("extractor should receive original caption, got %q", stub.gotText)
	}
	if strings.Join(stub.gotMissing, ",") != "company" {
		t.Errorf("missing = %v, want [company]", stub.gotMissing)
	}
}

func TestInstagramAdapter_LLMErrorKeepsRegex(t *testing.T) {
	html := loadThreadsFixture(t, "instagram_post.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	stub := &stubExtractor{err: errors.New("boom"), company: "Nope"}
	adapter := newInstagramAdapter(NewClient(ClientOptions{HTTP: srv.Client()}), stub)
	ads, err := adapter.Scrape(t.Context(), srv.URL+"/p/Dcn_WghoDUT")
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if ads[0].Company != "" {
		t.Errorf("Company = %q after LLM error, want regex empty", ads[0].Company)
	}
}

func TestIsInstagramHost(t *testing.T) {
	if !isInstagramHost("www.instagram.com") || !isInstagramHost("INSTAGR.AM") {
		t.Fatal("expected instagram hosts")
	}
	if isInstagramHost("instagrammers.com") || isInstagramHost("example.com") {
		t.Fatal("non-instagram host matched")
	}
}
