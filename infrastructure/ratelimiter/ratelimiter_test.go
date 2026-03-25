package ratelimiter_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// =============================================================================
// Mock Store
// =============================================================================

type mockStore struct {
	result   ratelimiter.Result
	err      error
	resetErr error
	closeErr error

	lastKey   string
	lastLimit ratelimiter.Limit
}

func (m *mockStore) Allow(_ context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	m.lastKey = key
	m.lastLimit = limit
	return m.result, m.err
}

func (m *mockStore) Reset(_ context.Context, key string) error {
	m.lastKey = key
	return m.resetErr
}

func (m *mockStore) Close() error {
	return m.closeErr
}

// =============================================================================
// RateLimiter Service Tests
// =============================================================================

func TestAllow_UsesResolver(t *testing.T) {
	store := &mockStore{
		result: ratelimiter.Result{Allowed: true, Remaining: 99},
	}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()

	limiter := ratelimiter.New(store, resolver, log)

	result, err := limiter.Allow(context.Background(), "user:123", ratelimiter.ResolveRequest{
		SubjectType: "user",
		SubjectID:   "123",
	})
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !result.Allowed {
		t.Error("Allow() should be allowed")
	}
	// Resolver should provide user limit (100 req/min + 10 burst).
	if store.lastLimit.Requests != 100 {
		t.Errorf("resolved limit = %d, want 100", store.lastLimit.Requests)
	}
}

func TestAllow_APIKeyOverride(t *testing.T) {
	store := &mockStore{
		result: ratelimiter.Result{Allowed: true},
	}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(store, resolver, log)

	customLimit := 42
	_, _ = limiter.Allow(context.Background(), "key:abc", ratelimiter.ResolveRequest{
		SubjectType: "service_account",
		APIKey: &ratelimiter.APIKeyInfo{
			APIKeyID:           "abc",
			ServiceAccountID:   "sa-1",
			RateLimitPerMinute: &customLimit,
		},
	})

	if store.lastLimit.Requests != 42 {
		t.Errorf("API key override limit = %d, want 42", store.lastLimit.Requests)
	}
}

func TestAllowWithLimit_ExplicitLimit(t *testing.T) {
	store := &mockStore{
		result: ratelimiter.Result{Allowed: true, Remaining: 4},
	}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(store, resolver, log)

	limit := ratelimiter.PerSecond(5)
	result, err := limiter.AllowWithLimit(context.Background(), "login:ip", limit)
	if err != nil {
		t.Fatalf("AllowWithLimit() error = %v", err)
	}
	if !result.Allowed {
		t.Error("AllowWithLimit() should be allowed")
	}
	if store.lastLimit.Requests != 5 {
		t.Errorf("explicit limit = %d, want 5", store.lastLimit.Requests)
	}
	if store.lastLimit.Window != time.Second {
		t.Errorf("explicit window = %v, want %v", store.lastLimit.Window, time.Second)
	}
}

func TestAllow_PropagatesError(t *testing.T) {
	store := &mockStore{err: errors.New("store error")}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(store, resolver, log)

	_, err := limiter.Allow(context.Background(), "key", ratelimiter.ResolveRequest{})
	if err == nil {
		t.Error("Allow() should propagate store error")
	}
}

func TestReset(t *testing.T) {
	store := &mockStore{}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(store, resolver, log)

	err := limiter.Reset(context.Background(), "key")
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if store.lastKey != "key" {
		t.Errorf("Reset() key = %q, want %q", store.lastKey, "key")
	}
}

func TestClose(t *testing.T) {
	store := &mockStore{closeErr: errors.New("close error")}
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(store, resolver, log)

	err := limiter.Close()
	if err == nil {
		t.Error("Close() should propagate store error")
	}
}

func TestResolver_Accessor(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	log := slog.Default()
	limiter := ratelimiter.New(&mockStore{}, resolver, log)

	if limiter.Resolver() == nil {
		t.Error("Resolver() should return non-nil")
	}
}

// =============================================================================
// Limit Helper Tests
// =============================================================================

func TestPerSecond(t *testing.T) {
	l := ratelimiter.PerSecond(10)
	if l.Requests != 10 {
		t.Errorf("Requests = %d, want 10", l.Requests)
	}
	if l.Window != time.Second {
		t.Errorf("Window = %v, want %v", l.Window, time.Second)
	}
}

func TestPerMinute(t *testing.T) {
	l := ratelimiter.PerMinute(100)
	if l.Requests != 100 {
		t.Errorf("Requests = %d, want 100", l.Requests)
	}
	if l.Window != time.Minute {
		t.Errorf("Window = %v, want %v", l.Window, time.Minute)
	}
}

func TestPerHour(t *testing.T) {
	l := ratelimiter.PerHour(1000)
	if l.Requests != 1000 {
		t.Errorf("Requests = %d, want 1000", l.Requests)
	}
	if l.Window != time.Hour {
		t.Errorf("Window = %v, want %v", l.Window, time.Hour)
	}
}

func TestWithBurst(t *testing.T) {
	l := ratelimiter.PerMinute(100).WithBurst(20)
	if l.Burst != 20 {
		t.Errorf("Burst = %d, want 20", l.Burst)
	}
	if l.Requests != 100 {
		t.Errorf("Requests = %d, want 100", l.Requests)
	}
}

func TestDefaultLimit(t *testing.T) {
	l := ratelimiter.DefaultLimit()
	if l.Requests != 100 {
		t.Errorf("Requests = %d, want 100", l.Requests)
	}
	if l.Window != time.Minute {
		t.Errorf("Window = %v, want %v", l.Window, time.Minute)
	}
}
