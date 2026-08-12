package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

const (
	defaultUserAgent     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultRenderTimeout = 45 * time.Second
)

// Client wraps net/http and chromedp for scrape outbound work, pacing both via Limiter.
type Client struct {
	http       *http.Client
	limiter    Limiter
	chromePath string
}

// ClientOptions configures NewClient.
type ClientOptions struct {
	HTTP       *http.Client // optional; default Timeout 45s
	Limiter    Limiter      // optional; nil = no pacing
	ChromePath string       // optional ExecPath for Render
}

// NewClient returns a scrape client. A nil *Client receiver is not used; callers
// should pass the returned value. If opts.HTTP is nil, a default client is used.
func NewClient(opts ClientOptions) *Client {
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{
		http:       httpClient,
		limiter:    opts.Limiter,
		chromePath: strings.TrimSpace(opts.ChromePath),
	}
}

// FetchDocument GETs pageURL and parses the response body with goquery.
// The returned finalURL is the post-redirect request URL when available.
func (c *Client) FetchDocument(ctx context.Context, pageURL string) (*goquery.Document, string, error) {
	if err := c.wait(ctx, pageURL); err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.http.Do(req)
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

// Render navigates to pageURL with headless Chromium, waits until waitReadySelector
// is ready, and returns the rendered outer HTML plus the final location URL.
func (c *Client) Render(ctx context.Context, pageURL, waitReadySelector string) (html string, finalURL string, err error) {
	if err := c.wait(ctx, pageURL); err != nil {
		return "", "", err
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRenderTimeout)
		defer cancel()
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.UserAgent(defaultUserAgent),
	)
	if c.chromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(c.chromePath))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer allocCancel()

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	var outerHTML, loc string
	err = chromedp.Run(tabCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady(waitReadySelector, chromedp.ByQuery),
		chromedp.OuterHTML("html", &outerHTML, chromedp.ByQuery),
		chromedp.Location(&loc),
	)
	if err != nil {
		return "", "", fmt.Errorf("chromedp render %s: %w", pageURL, err)
	}
	if loc == "" {
		loc = pageURL
	}
	return outerHTML, loc, nil
}

func (c *Client) wait(ctx context.Context, pageURL string) error {
	if c.limiter == nil {
		return nil
	}
	return c.limiter.Wait(ctx, pageURL)
}
