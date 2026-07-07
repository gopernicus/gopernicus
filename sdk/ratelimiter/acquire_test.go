// External test package, matching memory_test.go (see its header comment).
package ratelimiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// denyingLimiter denies with the configured RetryAfter until allowAfter
// checks have happened, then allows. It counts calls so tests can assert the
// retry loop actually looped rather than busy-spun or gave up.
type denyingLimiter struct {
	allowAfter int
	retryAfter time.Duration
	err        error
	calls      int
}

func (d *denyingLimiter) Allow(_ context.Context, _ string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	d.calls++
	if d.err != nil {
		return ratelimiter.Result{}, d.err
	}
	if d.calls > d.allowAfter {
		return ratelimiter.Result{Allowed: true}, nil
	}
	return ratelimiter.Result{Allowed: false, RetryAfter: d.retryAfter}, nil
}

func (d *denyingLimiter) Reset(_ context.Context, _ string) error { return nil }
func (d *denyingLimiter) Close() error                            { return nil }

func TestAcquireReturnsImmediatelyWhenAllowed(t *testing.T) {
	t.Parallel()

	mem := ratelimiter.NewMemory()
	defer mem.Close()

	if err := ratelimiter.Acquire(context.Background(), mem, "k", ratelimiter.PerMinute(10)); err != nil {
		t.Fatalf("Acquire on an unexhausted limit: %v", err)
	}
}

func TestAcquireBlocksUntilWindowResets(t *testing.T) {
	t.Parallel()

	mem := ratelimiter.NewMemory()
	defer mem.Close()

	limit := ratelimiter.Limit{Requests: 1, Window: 50 * time.Millisecond}
	if err := ratelimiter.Acquire(context.Background(), mem, "k", limit); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	start := time.Now()
	if err := ratelimiter.Acquire(context.Background(), mem, "k", limit); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("second Acquire returned after %v — expected it to block toward the window reset", elapsed)
	}
}

func TestAcquireHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	mem := ratelimiter.NewMemory()
	defer mem.Close()

	limit := ratelimiter.Limit{Requests: 1, Window: time.Hour}
	if err := ratelimiter.Acquire(context.Background(), mem, "k", limit); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := ratelimiter.Acquire(ctx, mem, "k", limit)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire on an exhausted hour window = %v, want context.DeadlineExceeded", err)
	}
}

func TestAcquirePropagatesBackendError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("backend down")
	d := &denyingLimiter{err: sentinel}

	err := ratelimiter.Acquire(context.Background(), d, "k", ratelimiter.DefaultLimit())
	if !errors.Is(err, sentinel) {
		t.Fatalf("Acquire = %v, want wrapped sentinel", err)
	}
}

func TestAcquireClampsZeroRetryAfter(t *testing.T) {
	t.Parallel()

	d := &denyingLimiter{allowAfter: 3, retryAfter: 0}

	if err := ratelimiter.Acquire(context.Background(), d, "k", ratelimiter.DefaultLimit()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if d.calls != 4 {
		t.Fatalf("Allow called %d times, want 4 (3 denials + 1 grant)", d.calls)
	}
}
