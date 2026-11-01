package workers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// backlogWork returns nil for the first n calls, then ErrNoWork.
func backlogWork(n int64, calls *atomic.Int64) WorkFunc {
	return func(context.Context) error {
		if calls.Add(1) <= n {
			return nil
		}
		return ErrNoWork
	}
}

func runPoolFor(t *testing.T, p *Pool, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(d + 2*time.Second):
		t.Fatal("pool did not stop after its context ended")
	}
}

// TestPool_DrainBacklogRunsBackToBack: with WithDrainBacklog, a backlog of 50
// successful iterations runs without waiting PollInterval (1h here).
func TestPool_DrainBacklogRunsBackToBack(t *testing.T) {
	var calls atomic.Int64
	p := NewPool(backlogWork(50, &calls), WithLogger(silentLogger()),
		WithPollInterval(time.Hour), WithIdleInterval(time.Hour), WithDrainBacklog(true))
	runPoolFor(t, p, 300*time.Millisecond)
	if got := calls.Load(); got != 51 {
		t.Fatalf("iterations = %d, want 51 (50 successes back to back, then one ErrNoWork before the idle wait)", got)
	}
}

// TestPool_DefaultWaitsPollIntervalAfterSuccess pins the default: without the
// option a success waits PollInterval, so a 50-item backlog runs one item.
func TestPool_DefaultWaitsPollIntervalAfterSuccess(t *testing.T) {
	var calls atomic.Int64
	p := NewPool(backlogWork(50, &calls), WithLogger(silentLogger()),
		WithPollInterval(time.Hour), WithIdleInterval(time.Hour))
	runPoolFor(t, p, 300*time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("iterations = %d, want 1 (the default waits PollInterval after a success)", got)
	}
}

// TestPool_DrainBacklogErrorsStillWait: under drain an error waits PollInterval.
func TestPool_DrainBacklogErrorsStillWait(t *testing.T) {
	var calls atomic.Int64
	p := NewPool(func(context.Context) error { calls.Add(1); return errors.New("boom") },
		WithLogger(silentLogger()), WithPollInterval(time.Hour), WithIdleInterval(time.Hour), WithDrainBacklog(true))
	runPoolFor(t, p, 300*time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("iterations = %d, want 1 (an error waits PollInterval)", got)
	}
}

// TestPool_DrainBacklogCancelsPromptly: a worker draining an endless backlog
// still stops when the context is cancelled.
func TestPool_DrainBacklogCancelsPromptly(t *testing.T) {
	var calls atomic.Int64
	p := NewPool(func(context.Context) error { calls.Add(1); return nil },
		WithLogger(silentLogger()), WithPollInterval(time.Hour), WithDrainBacklog(true))
	start := time.Now()
	runPoolFor(t, p, 100*time.Millisecond)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Run took %s to stop, want prompt cancellation", took)
	}
	if calls.Load() < 100 {
		t.Fatalf("iterations = %d, want many (drain runs back to back)", calls.Load())
	}
}
