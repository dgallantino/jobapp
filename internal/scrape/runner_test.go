package scrape

import (
	"strings"
	"testing"
)

func TestPickAdapter(t *testing.T) {
	r := New(Options{})

	tests := []struct {
		name        string
		pageURL     string
		adapterName string
		want        string
		wantErr     string
	}{
		{name: "threads.net", pageURL: "https://www.threads.net/@x/post/y", want: "threads"},
		{name: "threads.com", pageURL: "https://www.threads.com/@x/post/y", want: "threads"},
		{name: "jobstreet", pageURL: "https://id.jobstreet.com/jobs", want: "jobstreet"},
		{name: "glints", pageURL: "https://glints.com/id/opportunities", want: "glints"},
		{name: "dealls", pageURL: "https://dealls.com/?searchJob=developer", want: "dealls"},
		{name: "kalibrr", pageURL: "https://www.kalibrr.id/id-ID/home/te/developer", want: "kalibrr"},
		{name: "unknown host", pageURL: "https://example.com/job", want: "static"},
		{name: "explicit wins", pageURL: "https://www.threads.net/@x/post/y", adapterName: "static", want: "static"},
		{name: "explicit other host", pageURL: "https://example.com/job", adapterName: "threads", want: "threads"},
		{name: "unknown adapter", pageURL: "https://example.com/job", adapterName: "nope", wantErr: `unknown adapter "nope"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := r.pickAdapter(tt.pageURL, tt.adapterName)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("pickAdapter: got adapter %q, want error %q", a.Name(), tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("pickAdapter: %v", err)
			}
			if got := a.Name(); got != tt.want {
				t.Errorf("adapter = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScrapeOne_UnknownAdapter(t *testing.T) {
	r := New(Options{})
	name, ads, err := r.ScrapeOne(t.Context(), "https://example.com/job", "nope")
	if err == nil {
		t.Fatalf("ScrapeOne: got adapter %q ads=%d, want error", name, len(ads))
	}
	if !strings.Contains(err.Error(), `unknown adapter "nope"`) {
		t.Fatalf("error = %q, want unknown adapter", err.Error())
	}
	if name != "" || ads != nil {
		t.Errorf("name=%q ads=%v, want empty", name, ads)
	}
}
