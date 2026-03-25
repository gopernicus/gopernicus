package memorylimiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/ratelimitertest"
)

func newTestStore() *memorylimiter.Store {
	return memorylimiter.New(
		memorylimiter.WithMaxEntries(1000),
		memorylimiter.WithCleanupInterval(time.Hour), // Don't interfere with tests.
	)
}

func TestCompliance(t *testing.T) {
	store := newTestStore()
	defer store.Close()
	ratelimitertest.RunSuite(t, store)
}

// =============================================================================
// Basic Allow/Deny
// =============================================================================

func TestAllow_FirstRequest(t *testing.T) {
	s := newTestStore()
	defer s.Close()

	result, err := s.Allow(context.Background(), "key", ratelimiter.PerMinute(10))
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !result.Allowed {
		t.Error("first request should be allowed")
	}
	if result.Remaining != 9 {
		t.Errorf("Remaining = %d, want 9", result.Remaining)
	}
}

func TestAllow_ExceedsLimit(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(3)

	for i := 0; i < 3; i++ {
		result, _ := s.Allow(ctx, "key", limit)
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	result, err := s.Allow(ctx, "key", limit)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if result.Allowed {
		t.Error("4th request should be denied")
	}
	if result.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0", result.Remaining)
	}
	if result.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive when denied")
	}
}

func TestAllow_WithBurst(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(3).WithBurst(2) // Effective limit = 5

	for i := 0; i < 5; i++ {
		result, _ := s.Allow(ctx, "key", limit)
		if !result.Allowed {
			t.Fatalf("request %d should be allowed (burst)", i+1)
		}
	}

	result, _ := s.Allow(ctx, "key", limit)
	if result.Allowed {
		t.Error("6th request should be denied (burst exhausted)")
	}
}

// =============================================================================
// Window Reset
// =============================================================================

func TestAllow_WindowReset(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.Limit{
		Requests: 2,
		Window:   50 * time.Millisecond,
	}

	// Exhaust the limit.
	s.Allow(ctx, "key", limit)
	s.Allow(ctx, "key", limit)

	result, _ := s.Allow(ctx, "key", limit)
	if result.Allowed {
		t.Error("should be denied when limit exhausted")
	}

	// Wait for window to pass.
	time.Sleep(100 * time.Millisecond)

	result, _ = s.Allow(ctx, "key", limit)
	if !result.Allowed {
		t.Error("should be allowed after window reset")
	}
}

// =============================================================================
// Reset
// =============================================================================

func TestReset(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(2)

	s.Allow(ctx, "key", limit)
	s.Allow(ctx, "key", limit)

	result, _ := s.Allow(ctx, "key", limit)
	if result.Allowed {
		t.Error("should be denied before reset")
	}

	s.Reset(ctx, "key")

	result, _ = s.Allow(ctx, "key", limit)
	if !result.Allowed {
		t.Error("should be allowed after reset")
	}
}

// =============================================================================
// Close
// =============================================================================

func TestClose_StopsAccepting(t *testing.T) {
	s := newTestStore()
	s.Close()

	_, err := s.Allow(context.Background(), "key", ratelimiter.PerMinute(10))
	if err == nil {
		t.Error("Allow() after Close should return error")
	}
	if err != ratelimiter.ErrLimiterClosed {
		t.Errorf("error = %v, want ErrLimiterClosed", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	s := newTestStore()
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

// =============================================================================
// Context Cancellation
// =============================================================================

func TestAllow_CancelledContext(t *testing.T) {
	s := newTestStore()
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Allow(ctx, "key", ratelimiter.PerMinute(10))
	if err == nil {
		t.Error("Allow() with cancelled context should return error")
	}
}

func TestReset_CancelledContext(t *testing.T) {
	s := newTestStore()
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Reset(ctx, "key")
	if err == nil {
		t.Error("Reset() with cancelled context should return error")
	}
}

// =============================================================================
// LRU Eviction
// =============================================================================

func TestMaxEntries_Eviction(t *testing.T) {
	s := memorylimiter.New(
		memorylimiter.WithMaxEntries(3),
		memorylimiter.WithCleanupInterval(time.Hour),
	)
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(100)

	s.Allow(ctx, "a", limit)
	s.Allow(ctx, "b", limit)
	s.Allow(ctx, "c", limit)
	s.Allow(ctx, "d", limit) // Should evict "a"

	stats := s.Stats()
	if stats.EntryCount != 3 {
		t.Errorf("EntryCount = %d, want 3", stats.EntryCount)
	}
}

// =============================================================================
// Stats
// =============================================================================

func TestStats(t *testing.T) {
	s := memorylimiter.New(
		memorylimiter.WithMaxEntries(500),
		memorylimiter.WithCleanupInterval(time.Hour),
	)
	defer s.Close()

	stats := s.Stats()
	if stats.EntryCount != 0 {
		t.Errorf("initial EntryCount = %d, want 0", stats.EntryCount)
	}
	if stats.MaxEntries != 500 {
		t.Errorf("MaxEntries = %d, want 500", stats.MaxEntries)
	}
}

// =============================================================================
// Multiple Keys
// =============================================================================

func TestAllow_IndependentKeys(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()
	limit := ratelimiter.PerMinute(2)

	// Exhaust limit for key1.
	s.Allow(ctx, "key1", limit)
	s.Allow(ctx, "key1", limit)
	result, _ := s.Allow(ctx, "key1", limit)
	if result.Allowed {
		t.Error("key1 should be denied")
	}

	// key2 should still work.
	result, _ = s.Allow(ctx, "key2", limit)
	if !result.Allowed {
		t.Error("key2 should be allowed (independent)")
	}
}
