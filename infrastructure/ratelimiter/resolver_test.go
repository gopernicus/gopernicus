package ratelimiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

func TestDefaultResolver_UserLimit(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "user",
		SubjectID:   "user-123",
	})

	if limit.Requests != 100 {
		t.Errorf("user limit = %d, want 100", limit.Requests)
	}
	if limit.Window != time.Minute {
		t.Errorf("user window = %v, want %v", limit.Window, time.Minute)
	}
	if limit.Burst != 10 {
		t.Errorf("user burst = %d, want 10", limit.Burst)
	}
}

func TestDefaultResolver_ServiceAccountLimit(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "service_account",
		SubjectID:   "sa-123",
	})

	if limit.Requests != 500 {
		t.Errorf("service account limit = %d, want 500", limit.Requests)
	}
	if limit.Burst != 50 {
		t.Errorf("service account burst = %d, want 50", limit.Burst)
	}
}

func TestDefaultResolver_AnonymousLimit(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "anonymous",
		ClientIP:    "192.168.1.1",
	})

	if limit.Requests != 60 {
		t.Errorf("anonymous limit = %d, want 60", limit.Requests)
	}
	if limit.Burst != 5 {
		t.Errorf("anonymous burst = %d, want 5", limit.Burst)
	}
}

func TestDefaultResolver_UnknownSubjectType(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "unknown",
	})

	// Unknown types should get anonymous limits.
	if limit.Requests != 60 {
		t.Errorf("unknown type limit = %d, want 60", limit.Requests)
	}
}

func TestDefaultResolver_APIKeyOverride(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	customLimit := 42

	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "service_account",
		SubjectID:   "sa-123",
		APIKey: &ratelimiter.APIKeyInfo{
			APIKeyID:           "key-abc",
			ServiceAccountID:   "sa-123",
			RateLimitPerMinute: &customLimit,
		},
	})

	if limit.Requests != 42 {
		t.Errorf("API key override limit = %d, want 42", limit.Requests)
	}
	if limit.Window != time.Minute {
		t.Errorf("API key override window = %v, want %v", limit.Window, time.Minute)
	}
}

func TestDefaultResolver_APIKeyWithoutOverride(t *testing.T) {
	resolver := ratelimiter.NewDefaultResolver()
	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "service_account",
		SubjectID:   "sa-123",
		APIKey: &ratelimiter.APIKeyInfo{
			APIKeyID:           "key-abc",
			ServiceAccountID:   "sa-123",
			RateLimitPerMinute: nil, // No override.
		},
	})

	// Should fall back to service account default.
	if limit.Requests != 500 {
		t.Errorf("limit without override = %d, want 500", limit.Requests)
	}
}

func TestDefaultResolver_CustomDefaults(t *testing.T) {
	resolver := &ratelimiter.DefaultLimitResolver{
		UserLimit:           ratelimiter.PerMinute(200).WithBurst(20),
		ServiceAccountLimit: ratelimiter.PerMinute(1000).WithBurst(100),
		AnonymousLimit:      ratelimiter.PerMinute(30).WithBurst(3),
	}

	limit := resolver.Resolve(context.Background(), ratelimiter.ResolveRequest{
		SubjectType: "user",
	})
	if limit.Requests != 200 {
		t.Errorf("custom user limit = %d, want 200", limit.Requests)
	}
	if limit.Burst != 20 {
		t.Errorf("custom user burst = %d, want 20", limit.Burst)
	}
}
