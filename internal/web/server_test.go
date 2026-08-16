package web

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"jobapp/internal/db"
	"jobapp/internal/models"
)

func testServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	hash, err := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	srv, err := New(database, string(hash), "test-secret", nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, database
}

func authedRequest(t *testing.T, srv *Server, method, target string, body io.Reader) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, body)
	w := httptest.NewRecorder()
	if err := srv.setSession(w); err != nil {
		t.Fatalf("set session: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			req.AddCookie(c)
			return req
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func insertJobAd(t *testing.T, database *sql.DB, sourceURL, title, status string) {
	t.Helper()

	_, err := database.ExecContext(t.Context(), `
		INSERT INTO job_ads (source_url, title, company, status)
		VALUES (?, ?, 'Acme', ?)`,
		sourceURL, title, status,
	)
	if err != nil {
		t.Fatalf("insert job ad: %v", err)
	}
}

func TestJobsListPath(t *testing.T) {
	tests := []struct {
		filter string
		page   int
		want   string
	}{
		{filter: models.StatusNew, page: 1, want: "/?status=new"},
		{filter: jobsStatusAll, page: 1, want: "/?status=all"},
		{filter: models.StatusApplied, page: 2, want: "/?status=applied&page=2"},
		{filter: "", page: 1, want: "/?status=new"},
	}

	for _, tc := range tests {
		if got := jobsListPath(tc.filter, tc.page); got != tc.want {
			t.Errorf("jobsListPath(%q, %d) = %q, want %q", tc.filter, tc.page, got, tc.want)
		}
	}
}

func TestHandleJobsRedirectsBareRootToNew(t *testing.T) {
	srv, _ := testServer(t)

	req := authedRequest(t, srv, "GET", "/", nil)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/?status=new" {
		t.Fatalf("Location %q, want /?status=new", got)
	}
}

func TestHandleJobsRedirectsPreservesPage(t *testing.T) {
	srv, _ := testServer(t)

	req := authedRequest(t, srv, "GET", "/?page=2", nil)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/?page=2&status=new" {
		t.Fatalf("Location %q, want /?page=2&status=new", got)
	}
}

func TestHandleJobsFiltersByStatus(t *testing.T) {
	srv, database := testServer(t)
	insertJobAd(t, database, "https://example.com/new", "New role", models.StatusNew)
	insertJobAd(t, database, "https://example.com/applied", "Applied role", models.StatusApplied)

	t.Run("new", func(t *testing.T) {
		req := authedRequest(t, srv, "GET", "/?status=new", nil)
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want %d", w.Code, http.StatusOK)
		}
		body := w.Body.String()
		if !strings.Contains(body, "New role") {
			t.Fatalf("expected new job in response body")
		}
		if strings.Contains(body, "Applied role") {
			t.Fatalf("did not expect applied job in new-filtered response")
		}
		if !strings.Contains(body, `href="/?status=new" class="active"`) {
			t.Fatalf("expected New tab to be active")
		}
	})

	t.Run("all", func(t *testing.T) {
		req := authedRequest(t, srv, "GET", "/?status=all", nil)
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want %d", w.Code, http.StatusOK)
		}
		body := w.Body.String()
		if !strings.Contains(body, "New role") || !strings.Contains(body, "Applied role") {
			t.Fatalf("expected both jobs in all-filtered response")
		}
		if !strings.Contains(body, `href="/?status=all" class="active"`) {
			t.Fatalf("expected All tab to be active")
		}
	})
}

func TestHandleJobsBulkStatusRedirectsToFilter(t *testing.T) {
	srv, database := testServer(t)
	insertJobAd(t, database, "https://example.com/new", "New role", models.StatusNew)

	req := authedRequest(t, srv, "POST", "/jobs/bulk-status", strings.NewReader("filter=new&status=ignored&id=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/?status=new" {
		t.Fatalf("Location %q, want /?status=new", got)
	}
}

func TestLoginRedirectsToNewJobs(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("POST", "/login", strings.NewReader("password=test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != jobsDefaultListURL {
		t.Fatalf("Location %q, want %q", got, jobsDefaultListURL)
	}
}

func TestRequireAuthRedirectsWithNext(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/jobs/1", nil)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/login?next=%2Fjobs%2F1" {
		t.Fatalf("Location %q, want /login?next=%%2Fjobs%%2F1", got)
	}
}

func TestRequireAuthRedirectsPreservesQuery(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/?status=all&page=2", nil)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	want := "/login?next=" + url.QueryEscape("/?status=all&page=2")
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("Location %q, want %q", got, want)
	}
}

func TestLoginRedirectsToNext(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("POST", "/login", strings.NewReader("password=test&next=/sources"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/sources" {
		t.Fatalf("Location %q, want /sources", got)
	}
}

func TestLoginRejectsUnsafeNext(t *testing.T) {
	srv, _ := testServer(t)

	tests := []string{
		"https://evil.example/",
		"//evil.example/",
		"/login",
		"/logout",
	}
	for _, next := range tests {
		body := "password=test&next=" + url.QueryEscape(next)
		req := httptest.NewRequest("POST", "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, req)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("next %q: status %d, want %d", next, w.Code, http.StatusSeeOther)
		}
		if got := w.Header().Get("Location"); got != jobsDefaultListURL {
			t.Fatalf("next %q: Location %q, want %q", next, got, jobsDefaultListURL)
		}
	}
}

func TestSafeRedirect(t *testing.T) {
	tests := []struct {
		next string
		want string
	}{
		{"", jobsDefaultListURL},
		{"/jobs/1", "/jobs/1"},
		{"/?status=all&page=2", "/?status=all&page=2"},
		{"https://evil.example/", jobsDefaultListURL},
		{"//evil.example/", jobsDefaultListURL},
		{"/login", jobsDefaultListURL},
		{"/logout", jobsDefaultListURL},
		{"/path:with:colon", jobsDefaultListURL},
	}
	for _, tc := range tests {
		if got := safeRedirect(tc.next); got != tc.want {
			t.Errorf("safeRedirect(%q) = %q, want %q", tc.next, got, tc.want)
		}
	}
}

func TestHandleSourceToggleFlipsEnabled(t *testing.T) {
	srv, database := testServer(t)

	id, err := models.CreateSource(t.Context(), database, "Example", "https://example.com/jobs", "static", true)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	req := authedRequest(t, srv, "POST", "/sources/"+strconv.FormatInt(id, 10)+"/toggle", nil)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/sources" {
		t.Fatalf("Location %q, want /sources", got)
	}

	src, err := models.GetSource(t.Context(), database, id)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if src.Enabled {
		t.Fatal("expected source to be disabled after toggle")
	}
}
