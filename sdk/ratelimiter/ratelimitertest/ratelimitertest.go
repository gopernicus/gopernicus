// Package ratelimitertest is a conformance suite for ratelimiter.Limiter
// implementations: every backend that satisfies the port should pass Run
// against a fresh instance. Modeled on the net/http/httptest /
// go/analysis/analysistest pattern. Imports stdlib + sdk/ratelimiter only
// (sdk stays dependency-free per the constitution).
package ratelimitertest

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// shortWindow and the sleep margins below are generous (order of tens of
// milliseconds) to avoid flakiness on a loaded CI box while still finishing
// quickly. The Limiter port exposes no clock seam, so window/refill behavior
// is verified with real sleeps rather than an injected clock.
const shortWindow = 40 * time.Millisecond

// Run exercises the ratelimiter.Limiter contract against a fresh instance
// obtained from newLimiter for each subtest.
func Run(t *testing.T, newLimiter func(t *testing.T) ratelimiter.Limiter) {
	t.Helper()

	t.Run("AllowUnderLimit", func(t *testing.T) { testAllowUnderLimit(t, newLimiter(t)) })
	t.Run("DenyOverLimit", func(t *testing.T) { testDenyOverLimit(t, newLimiter(t)) })
	t.Run("Reset", func(t *testing.T) { testReset(t, newLimiter(t)) })
	t.Run("WindowRefill", func(t *testing.T) { testWindowRefill(t, newLimiter(t)) })
	t.Run("IndependentKeys", func(t *testing.T) { testIndependentKeys(t, newLimiter(t)) })
	t.Run("CloseIdempotent", func(t *testing.T) { testCloseIdempotent(t, newLimiter(t)) })
}

func testAllowUnderLimit(t *testing.T, l ratelimiter.Limiter) {
	ctx := context.Background()
	limit := ratelimiter.PerMinute(3)

	for i := 0; i < 3; i++ {
		res, err := l.Allow(ctx, "k", limit)
		if err != nil {
			t.Fatalf("Allow() call %d error = %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("Allow() call %d Allowed = false, want true (within limit)", i+1)
		}
		wantRemaining := 3 - (i + 1)
		if res.Remaining != wantRemaining {
			t.Errorf("Allow() call %d Remaining = %d, want %d", i+1, res.Remaining, wantRemaining)
		}
	}
}

func testDenyOverLimit(t *testing.T, l ratelimiter.Limiter) {
	ctx := context.Background()
	limit := ratelimiter.PerMinute(2)

	for i := 0; i < 2; i++ {
		if res, err := l.Allow(ctx, "k", limit); err != nil || !res.Allowed {
			t.Fatalf("warm-up Allow() call %d = %+v, err %v, want Allowed=true", i+1, res, err)
		}
	}

	res, err := l.Allow(ctx, "k", limit)
	if err != nil {
		t.Fatalf("Allow() over limit error = %v", err)
	}
	if res.Allowed {
		t.Error("Allow() over limit Allowed = true, want false")
	}
	if res.Remaining != 0 {
		t.Errorf("Allow() over limit Remaining = %d, want 0", res.Remaining)
	}
	if res.RetryAfter <= 0 {
		t.Errorf("Allow() over limit RetryAfter = %v, want > 0", res.RetryAfter)
	}
}

func testReset(t *testing.T, l ratelimiter.Limiter) {
	ctx := context.Background()
	limit := ratelimiter.PerMinute(1)

	if res, err := l.Allow(ctx, "k", limit); err != nil || !res.Allowed {
		t.Fatalf("first Allow() = %+v, err %v, want Allowed=true", res, err)
	}
	if res, err := l.Allow(ctx, "k", limit); err != nil || res.Allowed {
		t.Fatalf("second Allow() (over limit) = %+v, err %v, want Allowed=false", res, err)
	}

	if err := l.Reset(ctx, "k"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	res, err := l.Allow(ctx, "k", limit)
	if err != nil {
		t.Fatalf("Allow() after Reset() error = %v", err)
	}
	if !res.Allowed {
		t.Error("Allow() after Reset() Allowed = false, want true")
	}
}

func testWindowRefill(t *testing.T, l ratelimiter.Limiter) {
	ctx := context.Background()
	limit := ratelimiter.Limit{Requests: 1, Window: shortWindow}

	if res, err := l.Allow(ctx, "k", limit); err != nil || !res.Allowed {
		t.Fatalf("first Allow() = %+v, err %v, want Allowed=true", res, err)
	}
	if res, err := l.Allow(ctx, "k", limit); err != nil || res.Allowed {
		t.Fatalf("second Allow() (over limit) = %+v, err %v, want Allowed=false", res, err)
	}

	time.Sleep(shortWindow * 4)

	res, err := l.Allow(ctx, "k", limit)
	if err != nil {
		t.Fatalf("Allow() after window elapsed error = %v", err)
	}
	if !res.Allowed {
		t.Error("Allow() after window elapsed Allowed = false, want true (new window)")
	}
}

func testIndependentKeys(t *testing.T, l ratelimiter.Limiter) {
	ctx := context.Background()
	limit := ratelimiter.PerMinute(1)

	if res, err := l.Allow(ctx, "a", limit); err != nil || !res.Allowed {
		t.Fatalf("Allow(a) = %+v, err %v, want Allowed=true", res, err)
	}
	if res, err := l.Allow(ctx, "a", limit); err != nil || res.Allowed {
		t.Fatalf("second Allow(a) = %+v, err %v, want Allowed=false", res, err)
	}
	// A different key must have its own budget, unaffected by "a" above.
	if res, err := l.Allow(ctx, "b", limit); err != nil || !res.Allowed {
		t.Fatalf("Allow(b) = %+v, err %v, want Allowed=true", res, err)
	}
}

func testCloseIdempotent(t *testing.T, l ratelimiter.Limiter) {
	if err := l.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil (Close must be idempotent)", err)
	}
}
