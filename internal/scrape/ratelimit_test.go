package scrape

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewHostLimiterValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewHostLimiter(0, time.Second); err == nil {
		t.Fatal("expected error for min <= 0")
	}
	if _, err := NewHostLimiter(-time.Second, time.Second); err == nil {
		t.Fatal("expected error for negative min")
	}
	if _, err := NewHostLimiter(2*time.Second, time.Second); err == nil {
		t.Fatal("expected error for max < min")
	}
	if _, err := NewHostLimiter(20*time.Millisecond, 40*time.Millisecond); err != nil {
		t.Fatalf("valid limiter: %v", err)
	}
}

func TestHostLimiterSameHostBlocks(t *testing.T) {
	t.Parallel()

	lim, err := NewHostLimiter(40*time.Millisecond, 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	url := "https://example.com/a"

	start := time.Now()
	if err := lim.Wait(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := lim.Wait(ctx, url); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 35*time.Millisecond {
		t.Fatalf("second wait too fast: %v", elapsed)
	}
}

func TestHostLimiterDifferentHostsIndependent(t *testing.T) {
	t.Parallel()

	lim, err := NewHostLimiter(80*time.Millisecond, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	start := time.Now()
	if err := lim.Wait(ctx, "https://a.example/x"); err != nil {
		t.Fatal(err)
	}
	if err := lim.Wait(ctx, "https://b.example/y"); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed >= 50*time.Millisecond {
		t.Fatalf("different hosts should not share bucket, elapsed %v", elapsed)
	}
}

func TestHostLimiterContextCancel(t *testing.T) {
	t.Parallel()

	lim, err := NewHostLimiter(500*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	url := "https://example.com/cancel"

	if err := lim.Wait(ctx, url); err != nil {
		t.Fatal(err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = lim.Wait(cancelCtx, url)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestHostLimiterRefillInRange(t *testing.T) {
	t.Parallel()

	const minD = 20 * time.Millisecond
	const maxD = 40 * time.Millisecond
	lim, err := NewHostLimiter(minD, maxD)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	url := "https://example.com/range"

	if err := lim.Wait(ctx, url); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 8; i++ {
		start := time.Now()
		if err := lim.Wait(ctx, url); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		// Allow small timer skew below min; reject clearly too-early or far-over-max.
		if elapsed < minD-10*time.Millisecond {
			t.Fatalf("refill %d too short: %v", i, elapsed)
		}
		if elapsed > maxD+30*time.Millisecond {
			t.Fatalf("refill %d too long: %v", i, elapsed)
		}
	}
}

func TestRateLimitedHTTPClientNil(t *testing.T) {
	t.Parallel()
	c := RateLimitedHTTPClient(nil)
	if c == nil || c.Timeout == 0 {
		t.Fatal("expected default client")
	}
}
