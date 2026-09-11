package ratelimitertest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func runContract(t *testing.T, newLimiter func(*testing.T) ratelimiter.Limiter) {
	t.Run("Burst", func(t *testing.T) {
		l := newLimiter(t)
		limit := ratelimiter.PerMinute(2).WithBurst(2)
		for i := 0; i < 5; i++ {
			r, e := l.Allow(context.Background(), "burst", limit)
			if e != nil || r.Allowed != (i < 4) || r.Remaining != max(3-i, 0) {
				t.Fatalf("call %d: %+v %v", i, r, e)
			}
		}
	})
	t.Run("InvalidInputs", func(t *testing.T) {
		l := newLimiter(t)
		ctx := context.Background()
		for _, limit := range []ratelimiter.Limit{{}, {Requests: -1, Window: time.Minute}, {Requests: 1, Window: -1}, ratelimiter.PerMinute(1).WithBurst(-1), ratelimiter.PerMinute(ratelimiter.MaxCeiling).WithBurst(1), {Requests: ratelimiter.MaxCeiling, Window: ratelimiter.MaxWindow}} {
			if _, e := l.Allow(ctx, "invalid", limit); !errors.Is(e, sdk.ErrInvalidInput) {
				t.Fatalf("%+v: %v", limit, e)
			}
		}
		if _, e := l.Allow(ctx, "", ratelimiter.PerMinute(1)); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatal(e)
		}
		if e := l.Reset(ctx, ""); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatal(e)
		}
		r, e := l.Allow(ctx, "invalid", ratelimiter.PerMinute(1))
		if e != nil || !r.Allowed {
			t.Fatalf("invalid input consumed quota: %+v %v", r, e)
		}
	})
	t.Run("Cancellation", func(t *testing.T) {
		l := newLimiter(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e := l.Allow(ctx, "k", ratelimiter.PerMinute(1)); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		r, e := l.Allow(context.Background(), "k", ratelimiter.PerMinute(1))
		if e != nil || !r.Allowed {
			t.Fatal(r, e)
		}
		if e := l.Reset(ctx, "k"); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		r, e = l.Allow(context.Background(), "k", ratelimiter.PerMinute(1))
		if e != nil || r.Allowed {
			t.Fatalf("canceled reset restored quota: %+v %v", r, e)
		}
	})
	t.Run("ConcurrentAdmission", func(t *testing.T) {
		l := newLimiter(t)
		var allowed atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Go(func() {
				r, e := l.Allow(context.Background(), "concurrent", ratelimiter.PerMinute(7))
				if e != nil {
					t.Error(e)
				}
				if r.Allowed {
					allowed.Add(1)
				}
			})
		}
		wg.Wait()
		if allowed.Load() != 7 {
			t.Fatalf("admitted %d, want 7", allowed.Load())
		}
	})
	t.Run("CeilingAndWindowChanges", func(t *testing.T) {
		l := newLimiter(t)
		ctx := context.Background()
		r, e := l.Allow(ctx, "policy", ratelimiter.PerMinute(1))
		if e != nil || !r.Allowed {
			t.Fatal(r, e)
		}
		r, e = l.Allow(ctx, "policy", ratelimiter.PerMinute(3))
		if e != nil || !r.Allowed || r.Remaining != 1 {
			t.Fatal(r, e)
		}
		if _, e := l.Allow(ctx, "policy", ratelimiter.PerHour(3)); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatalf("live window change: %v", e)
		}
		r, e = l.Allow(ctx, "policy", ratelimiter.PerMinute(3))
		if e != nil || !r.Allowed || r.Remaining != 0 {
			t.Fatalf("window error mutated state: %+v %v", r, e)
		}
		if e := l.Reset(ctx, "policy"); e != nil {
			t.Fatal(e)
		}
		r, e = l.Allow(ctx, "policy", ratelimiter.PerHour(1))
		if e != nil || !r.Allowed {
			t.Fatal(r, e)
		}
	})
	t.Run("ExpiredWindowChange", func(t *testing.T) {
		l := newLimiter(t)
		ctx := context.Background()
		r, e := l.Allow(ctx, "expired", ratelimiter.Limit{Requests: 1, Window: shortWindow})
		if e != nil || !r.Allowed {
			t.Fatal(r, e)
		}
		time.Sleep(4 * shortWindow)
		r, e = l.Allow(ctx, "expired", ratelimiter.PerHour(1))
		if e != nil || !r.Allowed {
			t.Fatalf("expired state affected new policy: %+v %v", r, e)
		}
	})
	t.Run("FractionalMilliseconds", func(t *testing.T) {
		l := newLimiter(t)
		r, e := l.Allow(context.Background(), "tiny", ratelimiter.Limit{Requests: 1, Window: time.Nanosecond})
		if e != nil || !r.Allowed || r.ResetAt.IsZero() {
			t.Fatal(r, e)
		}
	})
}
