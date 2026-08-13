package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_FetchBytes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(ClientOptions{HTTP: srv.Client()})

	t.Run("ok", func(t *testing.T) {
		body, final, err := c.FetchBytes(context.Background(), srv.URL+"/api/jobs")
		if err != nil {
			t.Fatalf("FetchBytes: %v", err)
		}
		if !strings.Contains(string(body), `"ok":true`) {
			t.Fatalf("body = %q", body)
		}
		if final != srv.URL+"/api/jobs" {
			t.Fatalf("finalURL = %q", final)
		}
	})

	t.Run("non-2xx", func(t *testing.T) {
		_, _, err := c.FetchBytes(context.Background(), srv.URL+"/fail")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "HTTP 403") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestClient_FetchDocument_UsesFetchBytes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1 id="t">hi</h1></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(ClientOptions{HTTP: srv.Client()})
	doc, _, err := c.FetchDocument(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("FetchDocument: %v", err)
	}
	if got := doc.Find("#t").Text(); got != "hi" {
		t.Fatalf("text = %q", got)
	}
}
