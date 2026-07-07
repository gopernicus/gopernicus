// External test package (ratelimiter_test) so this file can import
// ratelimitertest, which itself imports ratelimiter — an in-package test
// file (package ratelimiter) importing ratelimitertest would be an import
// cycle (see sdk/cacher/memory_conformance_test.go for the same pattern).
package ratelimiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter/ratelimitertest"
)

func TestMemory_Conformance(t *testing.T) {
	ratelimitertest.Run(t, func(t *testing.T) ratelimiter.Limiter { return ratelimiter.NewMemory() })
}

// TestMemory_BurstIgnored documents that Memory implements plain fixed-window
// counting: Limit.Burst has no effect (see memory.go's doc comment on why).
func TestMemory_BurstIgnored(t *testing.T) {
	m := ratelimiter.NewMemory()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(1).WithBurst(10)

	if res, err := m.Allow(ctx, "k", limit); err != nil || !res.Allowed {
		t.Fatalf("first Allow() = %+v, err %v, want Allowed=true", res, err)
	}
	res, err := m.Allow(ctx, "k", limit)
	if err != nil {
		t.Fatalf("second Allow() error = %v", err)
	}
	if res.Allowed {
		t.Error("second Allow() with Burst=10 Allowed = true, want false (Memory ignores Burst)")
	}
}

// TestMemory_FixedWindowResetsAtWindowBoundary confirms the window is
// anchored at the first request in the window, not a rolling window.
func TestMemory_FixedWindowResetsAtWindowBoundary(t *testing.T) {
	m := ratelimiter.NewMemory()
	ctx := context.Background()
	limit := ratelimiter.Limit{Requests: 2, Window: 30 * time.Millisecond}

	for i := 0; i < 2; i++ {
		if res, err := m.Allow(ctx, "k", limit); err != nil || !res.Allowed {
			t.Fatalf("Allow() call %d = %+v, err %v, want Allowed=true", i+1, res, err)
		}
	}
	if res, err := m.Allow(ctx, "k", limit); err != nil || res.Allowed {
		t.Fatalf("Allow() call 3 = %+v, err %v, want Allowed=false", res, err)
	}

	time.Sleep(limit.Window * 4)

	for i := 0; i < 2; i++ {
		res, err := m.Allow(ctx, "k", limit)
		if err != nil {
			t.Fatalf("post-window Allow() call %d error = %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("post-window Allow() call %d Allowed = false, want true", i+1)
		}
	}
}
