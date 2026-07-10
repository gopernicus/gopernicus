package turso

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestRetry_ExhaustsAttempts proves the attempt count is honored and the last
// error is returned once attempts run out.
func TestRetry_ExhaustsAttempts(t *testing.T) {
	boom := errors.New("boom")
	var calls int
	err := retry(context.Background(),
		RetryPolicy{Attempts: 3, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error {
			calls++
			return boom
		})
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// TestRetry_StopsOnSuccess proves a mid-loop success returns nil and stops.
func TestRetry_StopsOnSuccess(t *testing.T) {
	var calls int
	err := retry(context.Background(),
		RetryPolicy{Attempts: 5, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
		func(context.Context) error {
			calls++
			if calls == 2 {
				return nil
			}
			return errors.New("boom")
		})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// TestRetry_SingleAttempt proves Attempts <= 1 runs fn exactly once (no retry).
func TestRetry_SingleAttempt(t *testing.T) {
	boom := errors.New("boom")
	for _, attempts := range []int{0, 1} {
		var calls int
		err := retry(context.Background(),
			RetryPolicy{Attempts: attempts, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
			func(context.Context) error {
				calls++
				return boom
			})
		if calls != 1 {
			t.Fatalf("Attempts=%d: calls = %d, want 1", attempts, calls)
		}
		if !errors.Is(err, boom) {
			t.Fatalf("Attempts=%d: err = %v, want boom", attempts, err)
		}
	}
}

// TestRetry_BackoffWithinBounds proves each inter-attempt gap stays within
// [MinBackoff, MaxBackoff] (generous upper slack for scheduler jitter — no
// flaky timing).
func TestRetry_BackoffWithinBounds(t *testing.T) {
	const (
		lo = 5 * time.Millisecond
		hi = 20 * time.Millisecond
	)
	var times []time.Time
	_ = retry(context.Background(),
		RetryPolicy{Attempts: 5, MinBackoff: lo, MaxBackoff: hi},
		func(context.Context) error {
			times = append(times, time.Now())
			return errors.New("boom")
		})
	if len(times) != 5 {
		t.Fatalf("calls = %d, want 5", len(times))
	}
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap+time.Millisecond < lo {
			t.Fatalf("gap[%d] = %v is below MinBackoff %v", i, gap, lo)
		}
		if gap > hi+500*time.Millisecond {
			t.Fatalf("gap[%d] = %v exceeds MaxBackoff %v + slack", i, gap, hi)
		}
	}
}

// TestRetry_ContextCancelledUpFront proves an already-dead ctx aborts before fn
// runs, returning ctx.Err().
func TestRetry_ContextCancelledUpFront(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	err := retry(ctx,
		RetryPolicy{Attempts: 3, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		func(context.Context) error {
			calls++
			return errors.New("boom")
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0 (ctx dead before first try)", calls)
	}
}

// TestOpen_RetryAgainstUnreachable wires the retry helper into Open against a
// refused loopback port (a closed local listener — no DNS, no egress, so it is
// fast and non-flaky). With Attempts=2 and a 30ms backoff, Open must run the
// eager StatusCheck round-trip, retry once, and then error. Asserting elapsed >=
// the single backoff proves a second attempt was actually made (a lone attempt
// would return in ~1ms).
func TestOpen_RetryAgainstUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	const backoff = 30 * time.Millisecond
	start := time.Now()
	db, err := Open(Config{
		URL:            "http://" + addr,
		ConnectTimeout: 2 * time.Second,
		Retry:          RetryPolicy{Attempts: 2, MinBackoff: backoff, MaxBackoff: backoff},
	})
	elapsed := time.Since(start)
	if db != nil {
		db.Close()
		t.Fatal("Open returned a DB against a refused port; want nil")
	}
	if err == nil {
		t.Fatal("Open against a refused port returned nil error; want a connectivity error")
	}
	if elapsed < backoff {
		t.Fatalf("Open returned in %v (< one %v backoff) — the retry did not run a second attempt", elapsed, backoff)
	}
}

// TestRetry_ContextCancelledDuringBackoff proves a cancel interrupts the backoff
// sleep and aborts with ctx.Err() well before the (1s) backoff would elapse.
func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := retry(ctx,
		RetryPolicy{Attempts: 5, MinBackoff: time.Second, MaxBackoff: time.Second},
		func(context.Context) error {
			return errors.New("boom")
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed %v — cancel must interrupt the 1s backoff sleep", elapsed)
	}
}
