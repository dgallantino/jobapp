package scrape

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Limiter paces outbound crawl requests. A nil Limiter is a no-op.
type Limiter interface {
	Wait(ctx context.Context, rawURL string) error
}

// HostLimiter is a capacity-1 token bucket per host. After each take, the next
// token refills after a fresh random duration in [min, max].
type HostLimiter struct {
	min, max time.Duration

	mu      sync.Mutex
	buckets map[string]*hostBucket
}

type hostBucket struct {
	mu        sync.Mutex
	available bool
	nextReady time.Time
}

// NewHostLimiter returns a per-host limiter. min must be > 0 and max >= min.
func NewHostLimiter(min, max time.Duration) (*HostLimiter, error) {
	if min <= 0 {
		return nil, fmt.Errorf("rate min must be > 0, got %s", min)
	}
	if max < min {
		return nil, fmt.Errorf("rate max %s must be >= min %s", max, min)
	}
	return &HostLimiter{
		min:     min,
		max:     max,
		buckets: make(map[string]*hostBucket),
	}, nil
}

func (l *HostLimiter) Wait(ctx context.Context, rawURL string) error {
	if l == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	host := hostKey(rawURL)
	b := l.bucket(host)

	for {
		b.mu.Lock()
		now := time.Now()
		if b.available || !now.Before(b.nextReady) {
			b.available = false
			b.nextReady = now.Add(l.refillDelay())
			b.mu.Unlock()
			return nil
		}
		wait := time.Until(b.nextReady)
		b.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *HostLimiter) bucket(host string) *hostBucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[host]
	if !ok {
		b = &hostBucket{available: true}
		l.buckets[host] = b
	}
	return b
}

func (l *HostLimiter) refillDelay() time.Duration {
	if l.max == l.min {
		return l.min
	}
	span := l.max - l.min
	return l.min + time.Duration(rand.Int63n(int64(span)+1))
}

func hostKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return strings.ToLower(strings.TrimSpace(rawURL))
	}
	return strings.ToLower(u.Hostname())
}

// limitingTransport waits on Limiter before each request.
type limitingTransport struct {
	limiter Limiter
	base    http.RoundTripper
}

func (t *limitingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.limiter != nil {
		raw := ""
		if req.URL != nil {
			raw = req.URL.String()
		}
		if err := t.limiter.Wait(req.Context(), raw); err != nil {
			return nil, err
		}
	}
	return base.RoundTrip(req)
}

// RateLimitedHTTPClient returns an HTTP client that paces requests via limiter.
// If limiter is nil, returns DefaultHTTPClient().
func RateLimitedHTTPClient(limiter Limiter) *http.Client {
	if limiter == nil {
		return DefaultHTTPClient()
	}
	return &http.Client{
		Timeout: 45 * time.Second,
		Transport: &limitingTransport{
			limiter: limiter,
			base:    http.DefaultTransport,
		},
	}
}
