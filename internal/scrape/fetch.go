package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/PuerkitoBio/goquery"
)

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// fetchDocument GETs pageURL and parses the response body with goquery.
// The returned finalURL is the post-redirect request URL when available.
func fetchDocument(ctx context.Context, client *http.Client, pageURL string) (*goquery.Document, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	if client == nil {
		client = DefaultHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, pageURL)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, "", err
	}
	final := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return doc, final, nil
}
