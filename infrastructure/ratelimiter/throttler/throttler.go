// Package throttler provides blocking rate limiting for background workers.
//
// Unlike HTTP rate limiting (which rejects requests with 429), a Throttler blocks
// the caller until capacity is available. This is designed for workers that need
// to respect external API rate limits without dropping work.
//
// Two implementations are provided:
//   - New: wraps any ratelimiter.Storer, blocks on RetryAfter. Good for most cases.
//   - NewTokenBucket: Redis-backed token bucket that meters requests evenly over time.
//     Use this when you need smooth throughput rather than burst-then-starve behavior.
package throttler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// Throttler blocks until rate limit capacity is available.
type Throttler interface {
	// Acquire blocks until the rate limit allows the request.
	// Returns nil when allowed, or an error if the context is cancelled.
	Acquire(ctx context.Context, key string, limit ratelimiter.Limit) error

	// Close releases resources.
	Close() error
}

type throttler struct {
	store ratelimiter.Storer
	log   *slog.Logger
}

// New creates a Throttler backed by any ratelimiter.Storer.
// On each denied request it sleeps for Result.RetryAfter before retrying.
func New(store ratelimiter.Storer, log *slog.Logger) Throttler {
	return &throttler{store: store, log: log}
}

// Acquire blocks until the rate limit allows the request.
func (t *throttler) Acquire(ctx context.Context, key string, limit ratelimiter.Limit) error {
	for {
		result, err := t.store.Allow(ctx, key, limit)
		if err != nil {
			return fmt.Errorf("throttler: rate limit check: %w", err)
		}

		if result.Allowed {
			return nil
		}

		wait := result.RetryAfter
		if wait < time.Millisecond {
			wait = time.Millisecond
		}

		t.log.DebugContext(ctx, "throttler: rate limited, waiting",
			"key", key,
			"retry_after", wait,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (t *throttler) Close() error {
	return t.store.Close()
}

// Compile-time interface check.
var _ Throttler = (*throttler)(nil)
