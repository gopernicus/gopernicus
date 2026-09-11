package ratelimiter

import (
	"context"
	"fmt"
	"time"
)

const minRetryWait = time.Millisecond

// Acquire waits for admission with the caller's context. It consumes one unit
// when allowed and returns backend errors without hiding their cause or adding
// keys to diagnostics. Concurrent waiters are not a fair queue or reservations.
// Cancellation racing an accepted backend write does not refund that write.
func Acquire(ctx context.Context, limiter Allower, key string, limit Limit) error {
	if err := checkKey(ctx, key); err != nil {
		return err
	}
	limit, err := limit.Normalize()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := limiter.Allow(ctx, key, limit)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("ratelimiter: acquire: %w", err)
		}
		if result.Allowed {
			return nil
		}
		timer := time.NewTimer(max(result.RetryAfter, minRetryWait))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
