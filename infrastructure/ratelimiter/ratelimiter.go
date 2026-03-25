// Package ratelimiter provides rate limiting for the facilities layer.
// Store implementations are in memorylimiter/ subpackage.
package ratelimiter

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Common errors.
var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrLimiterClosed     = errors.New("rate limiter closed")
)

// =============================================================================
// Storer Interface (what store implementations satisfy)
// =============================================================================

// Storer defines the interface for rate limit store implementations.
// Memory, Redis, and SQLite stores satisfy this interface.
type Storer interface {
	// Allow checks if a request should be allowed for the given key.
	// Returns the result with remaining quota and timing information.
	Allow(ctx context.Context, key string, limit Limit) (Result, error)

	// Reset clears the rate limit state for a key (for admin override).
	Reset(ctx context.Context, key string) error

	// Close releases any resources held by the store.
	Close() error
}

// =============================================================================
// RateLimiter Service (wraps Storer + LimitResolver)
// =============================================================================

// RateLimiter is the service that combines a store with a limit resolver.
//
// Usage:
//
//	store := memorylimiter.New()
//	resolver := ratelimiter.NewDefaultResolver()
//	limiter := ratelimiter.New(store, resolver, log)
//
//	// Use resolver to determine limit
//	result, err := limiter.Allow(ctx, key, resolveReq)
//
//	// Or use explicit limit for route-specific overrides
//	result, err := limiter.AllowWithLimit(ctx, key, ratelimiter.PerMinute(5))
type RateLimiter struct {
	store    Storer
	resolver LimitResolver
	log      *slog.Logger
}

// New creates a RateLimiter service that wraps a store and resolver.
func New(store Storer, resolver LimitResolver, log *slog.Logger) *RateLimiter {
	return &RateLimiter{
		store:    store,
		resolver: resolver,
		log:      log,
	}
}

// Allow checks if a request should be allowed, using the resolver to determine the limit.
// The ResolveRequest is used to look up subject-specific limits (user tier, API key override, etc.).
func (r *RateLimiter) Allow(ctx context.Context, key string, req ResolveRequest) (Result, error) {
	limit := r.resolver.Resolve(ctx, req)
	return r.store.Allow(ctx, key, limit)
}

// AllowWithLimit checks if a request should be allowed using an explicit limit.
// Use this for route-specific overrides (e.g., stricter limits for login endpoints).
func (r *RateLimiter) AllowWithLimit(ctx context.Context, key string, limit Limit) (Result, error) {
	return r.store.Allow(ctx, key, limit)
}

// Reset clears the rate limit state for a key.
func (r *RateLimiter) Reset(ctx context.Context, key string) error {
	return r.store.Reset(ctx, key)
}

// Close releases resources held by the underlying store.
func (r *RateLimiter) Close() error {
	return r.store.Close()
}

// Resolver returns the underlying LimitResolver.
func (r *RateLimiter) Resolver() LimitResolver {
	return r.resolver
}

// =============================================================================
// Types
// =============================================================================

// Limit defines rate limiting parameters.
type Limit struct {
	// Requests is the maximum number of requests allowed in the window.
	Requests int

	// Window is the time duration for the rate limit window.
	Window time.Duration

	// Burst is the optional burst allowance above the base limit.
	// If zero, no burst is allowed.
	Burst int
}

// Result contains the outcome of a rate limit check.
type Result struct {
	// Allowed indicates whether the request should proceed.
	Allowed bool

	// Remaining is the number of requests remaining in the current window.
	Remaining int

	// ResetAt is when the current window resets.
	ResetAt time.Time

	// RetryAfter is how long to wait before retrying (for 429 responses).
	// Only meaningful when Allowed is false.
	RetryAfter time.Duration
}

// =============================================================================
// Helper Functions
// =============================================================================

// DefaultLimit returns a sensible default rate limit.
func DefaultLimit() Limit {
	return Limit{
		Requests: 100,
		Window:   time.Minute,
	}
}

// PerSecond creates a limit of n requests per second.
func PerSecond(n int) Limit {
	return Limit{
		Requests: n,
		Window:   time.Second,
	}
}

// PerMinute creates a limit of n requests per minute.
func PerMinute(n int) Limit {
	return Limit{
		Requests: n,
		Window:   time.Minute,
	}
}

// PerHour creates a limit of n requests per hour.
func PerHour(n int) Limit {
	return Limit{
		Requests: n,
		Window:   time.Hour,
	}
}

// WithBurst adds burst allowance to a limit.
func (l Limit) WithBurst(burst int) Limit {
	l.Burst = burst
	return l
}
