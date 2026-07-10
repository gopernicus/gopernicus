// Package ratelimiter provides rate limiting for the facilities layer.
// Memory is the in-package default Limiter; Acquire is the blocking helper
// that waits for quota instead of rejecting.
package ratelimiter

import (
	"context"
	"log/slog"
	"time"
)

// =============================================================================
// Limiter port (what backend implementations satisfy)
// =============================================================================

// Limiter is the rate-limit backend port. Memory satisfies it in-package;
// goredis.Limiter (integrations/kvstores/goredis) is the Redis-backed
// implementation.
type Limiter interface {
	// Allow checks if a request should be allowed for the given key.
	// Returns the result with remaining quota and timing information.
	Allow(ctx context.Context, key string, limit Limit) (Result, error)

	// Reset clears the rate limit state for a key (for admin override).
	Reset(ctx context.Context, key string) error

	// Close releases any resources held by the backend.
	Close() error
}

// =============================================================================
// RateLimiter Service (wraps Limiter + LimitResolver)
// =============================================================================

// RateLimiter is the service that combines a store with a limit resolver.
//
// Usage:
//
//	store := ratelimiter.NewMemory()
//	resolver := ratelimiter.NewDefaultResolver()
//	limiter := ratelimiter.New(store, resolver, ratelimiter.WithLogger(log))
//
//	// Use resolver to determine limit
//	result, err := limiter.Allow(ctx, key, resolveReq)
//
//	// Or use explicit limit for route-specific overrides
//	result, err := limiter.AllowWithLimit(ctx, key, ratelimiter.PerMinute(5))
type RateLimiter struct {
	limiter  Limiter
	resolver LimitResolver
	log      *slog.Logger
}

// Option configures a RateLimiter during construction.
type Option func(*RateLimiter)

// WithLogger enables structured logging for rate limiter operations.
func WithLogger(log *slog.Logger) Option {
	return func(r *RateLimiter) {
		r.log = log
	}
}

// New creates a RateLimiter service that wraps a limiter backend and resolver.
// Use WithLogger to enable structured logging.
func New(limiter Limiter, resolver LimitResolver, opts ...Option) *RateLimiter {
	r := &RateLimiter{
		limiter:  limiter,
		resolver: resolver,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Allow checks if a request should be allowed, using the resolver to determine the limit.
// The ResolveRequest is used to look up subject-specific limits (user tier, API key override, etc.).
func (r *RateLimiter) Allow(ctx context.Context, key string, req ResolveRequest) (Result, error) {
	limit := r.resolver.Resolve(ctx, req)
	return r.limiter.Allow(ctx, key, limit)
}

// AllowWithLimit checks if a request should be allowed using an explicit limit.
// Use this for route-specific overrides (e.g., stricter limits for login endpoints).
func (r *RateLimiter) AllowWithLimit(ctx context.Context, key string, limit Limit) (Result, error) {
	return r.limiter.Allow(ctx, key, limit)
}

// Reset clears the rate limit state for a key.
func (r *RateLimiter) Reset(ctx context.Context, key string) error {
	return r.limiter.Reset(ctx, key)
}

// Close releases resources held by the underlying backend.
func (r *RateLimiter) Close() error {
	return r.limiter.Close()
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
