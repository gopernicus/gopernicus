package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
)

// fakePurger stands in for the jobs Service's bounded terminal purge. It removes at most the
// requested limit per call from a finite pool of terminal rows, records the limits and cutoffs
// it saw, and can be made to error. It is mutex-guarded for the loop goroutine.
type fakePurger struct {
	mu        sync.Mutex
	remaining int
	limits    []int
	befores   []time.Time
	calls     int
	err       error
}

func (p *fakePurger) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return 0, p.err
	}
	p.limits = append(p.limits, limit)
	p.befores = append(p.befores, before)
	n := limit
	if n > p.remaining {
		n = p.remaining
	}
	p.remaining -= n
	return n, nil
}

func (p *fakePurger) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestDeliveryPurgeBoundedBatch proves one purge pass removes at most the configured batch and
// the retention cutoff is now−Retention, and that a backlog larger than the batch drains over
// successive passes (the next tick continues). It also proves the purged count reaches the
// runtime's observation hook.
func TestDeliveryPurgeBoundedBatch(t *testing.T) {
	t.Parallel()
	const batch = 500
	const retention = 24 * time.Hour
	fixedNow := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	purger := &fakePurger{remaining: 1200}
	var observed []int
	rt := auth.DeliveryJobRuntime{Purged: func(_ context.Context, n int) { observed = append(observed, n) }}

	purge := newDeliveryPurge(purger, rt, deliveryPurgeConfig{Retention: retention, Batch: batch}, func() time.Time { return fixedNow })

	// Three successive passes drain 1200 rows in bounded 500/500/200 batches.
	wantPerPass := []int{500, 500, 200}
	for i, want := range wantPerPass {
		n, err := purge(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if n != want {
			t.Fatalf("pass %d purged %d, want %d (bounded batch)", i, n, want)
		}
	}
	if purger.remaining != 0 {
		t.Fatalf("remaining terminal rows = %d, want 0 after draining", purger.remaining)
	}
	for i, lim := range purger.limits {
		if lim != batch {
			t.Fatalf("pass %d requested limit %d, want the configured batch %d", i, lim, batch)
		}
	}
	wantBefore := fixedNow.Add(-retention)
	for i, b := range purger.befores {
		if !b.Equal(wantBefore) {
			t.Fatalf("pass %d cutoff = %v, want now-retention %v", i, b, wantBefore)
		}
	}
	if len(observed) != 3 || observed[0] != 500 || observed[2] != 200 {
		t.Fatalf("purged observations = %v, want the per-pass counts reported to the runtime hook", observed)
	}
}

// TestDeliveryPurgeLoopContinuesAfterError proves a purge-pass error is logged and the loop
// continues (the scheduler and host are unaffected) rather than exiting.
func TestDeliveryPurgeLoopContinuesAfterError(t *testing.T) {
	t.Parallel()
	purger := &fakePurger{err: errors.New("purge backend unavailable")}
	purge := newDeliveryPurge(purger, auth.DeliveryJobRuntime{}, deliveryPurgeConfig{Retention: time.Hour, Batch: 10}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDeliveryPurgeLoop(ctx, 5*time.Millisecond, purge, quietLog())
	}()

	// The loop must keep invoking purge despite every pass erroring.
	deadline := time.Now().Add(2 * time.Second)
	for purger.callCount() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("purge loop stopped calling after errors (calls=%d)", purger.callCount())
		}
		time.Sleep(2 * time.Millisecond)
	}
	// It must still be running (not returned) until we cancel.
	select {
	case <-done:
		t.Fatal("purge loop exited on a purge error; it must continue")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("purge loop did not return after context cancel")
	}
}

// TestDeliveryPurgeLoopCleanShutdown proves the loop returns promptly when its context is
// canceled (host-owned shutdown).
func TestDeliveryPurgeLoopCleanShutdown(t *testing.T) {
	t.Parallel()
	purger := &fakePurger{remaining: 5}
	purge := newDeliveryPurge(purger, auth.DeliveryJobRuntime{}, deliveryPurgeConfig{Retention: time.Hour, Batch: 10}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDeliveryPurgeLoop(ctx, time.Hour, purge, quietLog()) // long interval: only cancel ends it
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("purge loop did not return promptly on cancel")
	}
}
