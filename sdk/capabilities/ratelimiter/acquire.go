package ratelimiter

import (
	"context"
	"fmt"
	"time"
)

// minRetryWait floors the sleep between denied checks so a backend reporting
// a zero (or sub-millisecond) RetryAfter cannot spin Acquire into a busy loop.
const minRetryWait = time.Millisecond

// Acquire blocks until limiter allows key under limit, sleeping each denied
// check's RetryAfter before checking again. It returns nil once allowed, the
// backend's error (wrapped) if a check fails, or ctx.Err() when the context
// is cancelled first.
//
// This is the waiting counterpart to the rejecting Allow: an HTTP surface
// rejects with 429 + RetryAfter, while a background worker that must respect
// an external budget (an upstream API's rate limit) calls Acquire and runs
// late instead of dropping work. It composes with any Limiter backend; there
// is no separate throttler port.
func Acquire(ctx context.Context, limiter Limiter, key string, limit Limit) error {
	for {
		result, err := limiter.Allow(ctx, key, limit)
		if err != nil {
			return fmt.Errorf("ratelimiter: acquire %q: %w", key, err)
		}
		if result.Allowed {
			return nil
		}

		wait := max(result.RetryAfter, minRetryWait)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
