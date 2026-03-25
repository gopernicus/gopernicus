// Package ratelimitertest provides compliance tests for ratelimiter.Storer implementations.
//
// Example:
//
//	func TestCompliance(t *testing.T) {
//	    store := memorylimiter.New()
//	    defer store.Close()
//	    ratelimitertest.RunSuite(t, store)
//	}
package ratelimitertest

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// RunSuite runs the standard compliance tests against any Storer implementation.
func RunSuite(t *testing.T, s ratelimiter.Storer) {
	t.Helper()

	limit := ratelimiter.Limit{
		Requests: 5,
		Window:   time.Minute,
	}

	t.Run("AllowUnderLimit", func(t *testing.T) {
		ctx := context.Background()
		result, err := s.Allow(ctx, "test:under", limit)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !result.Allowed {
			t.Fatal("first request should be allowed")
		}
		if result.Remaining < 0 {
			t.Fatalf("remaining should be >= 0, got %d", result.Remaining)
		}
	})

	t.Run("AllowExceedsLimit", func(t *testing.T) {
		ctx := context.Background()
		key := "test:exceed"
		for i := 0; i < 5; i++ {
			_, err := s.Allow(ctx, key, limit)
			if err != nil {
				t.Fatalf("Allow %d: %v", i, err)
			}
		}

		result, err := s.Allow(ctx, key, limit)
		if err != nil {
			t.Fatalf("Allow over limit: %v", err)
		}
		if result.Allowed {
			t.Fatal("request over limit should be denied")
		}
	})

	t.Run("Reset", func(t *testing.T) {
		ctx := context.Background()
		key := "test:reset"
		for i := 0; i < 5; i++ {
			s.Allow(ctx, key, limit)
		}

		if err := s.Reset(ctx, key); err != nil {
			t.Fatalf("Reset: %v", err)
		}

		result, err := s.Allow(ctx, key, limit)
		if err != nil {
			t.Fatalf("Allow after Reset: %v", err)
		}
		if !result.Allowed {
			t.Fatal("request should be allowed after reset")
		}
	})

	t.Run("IndependentKeys", func(t *testing.T) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			s.Allow(ctx, "test:indep:a", limit)
		}

		result, err := s.Allow(ctx, "test:indep:b", limit)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !result.Allowed {
			t.Fatal("different key should have independent limit")
		}
	})
}
