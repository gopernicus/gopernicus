package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/memstore"
)

// errDeadLetterProbe is a permanent-looking handler failure used to drive the
// dead-letter path.
var errDeadLetterProbe = errors.New("delivery: probe failure")

// waitFor polls cond until it is true or the deadline elapses.
func waitFor(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", within)
}

// newFencedService builds a jobs Service over the in-core fenced queue reference.
func newFencedService(t *testing.T) (*Service, *memstore.FencedQueue) {
	t.Helper()
	fq := memstore.NewFencedQueue()
	svc, err := NewService(Repositories{FencedQueue: fq}, Config{})
	if err != nil {
		t.Fatalf("NewService (fenced-only): %v", err)
	}
	return svc, fq
}

// TestFencedPrimitives proves the stdlib-typed fenced seams a consuming feature
// matches structurally: submit-once idempotency, replace supersession, and
// latest-by-key status.
func TestFencedPrimitives(t *testing.T) {
	ctx := context.Background()
	svc, _ := newFencedService(t)

	id1, err := svc.EnqueueOnce(ctx, "k", "key-1", json.RawMessage(`"a"`))
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	id2, err := svc.EnqueueOnce(ctx, "k", "key-1", json.RawMessage(`"b"`))
	if err != nil {
		t.Fatalf("EnqueueOnce (dup): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("EnqueueOnce not idempotent: %q != %q", id1, id2)
	}

	status, err := svc.LatestStatusByKey(ctx, "key-1")
	if err != nil {
		t.Fatalf("LatestStatusByKey: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	id3, err := svc.Replace(ctx, "k", "key-1", json.RawMessage(`"c"`))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if id3 == id1 {
		t.Fatalf("Replace returned the superseded id %q", id3)
	}
}

// TestFencedRuntimeCompletes proves the fenced runtime claims a submitted job, hands
// the handler a checkpoint-capable claim, and completes it — end to end, on a
// host-run pool.
func TestFencedRuntimeCompletes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newFencedService(t)

	var handled atomic.Int64
	var gotAfterCheckpoint atomic.Bool
	rt, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Workers:      1,
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Handlers: map[string]FencedHandlerFunc{
			"delivery": func(ctx context.Context, claim FencedClaim) error {
				handled.Add(1)
				// The claim carries the lease-fenced checkpoint; a checkpoint under the
				// current claim must succeed.
				if err := claim.Checkpoint(ctx, json.RawMessage(`"checkpointed"`)); err != nil {
					t.Errorf("checkpoint under current claim: %v", err)
					return err
				}
				gotAfterCheckpoint.Store(true)
				return nil // complete
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}

	// NewFencedRuntime starts NOTHING: no handler runs before the host calls Run.
	if _, err := svc.EnqueueOnce(ctx, "delivery", "key-run", json.RawMessage(`"opaque"`)); err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if handled.Load() != 0 {
		t.Fatalf("handler ran before Run: count=%d", handled.Load())
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(runCtx) }()

	waitFor(t, time.Second, func() bool {
		s, err := svc.LatestStatusByKey(ctx, "key-run")
		return err == nil && s == "completed"
	})
	cancel()
	<-done

	if handled.Load() == 0 {
		t.Fatal("handler never ran")
	}
	if !gotAfterCheckpoint.Load() {
		t.Fatal("checkpoint under the current claim did not succeed")
	}
}

// TestFencedRuntimeDeadLetters proves an always-failing handler dead-letters at the
// attempt cap and fires the per-kind terminal hook after the transition is recorded.
func TestFencedRuntimeDeadLetters(t *testing.T) {
	ctx := context.Background()
	svc, fq := newFencedService(t)

	var mu sync.Mutex
	var hookStatus string
	rt, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Workers:      1,
		MaxAttempts:  1, // first failure dead-letters
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Backoff:      func(int) time.Duration { return time.Millisecond },
		Handlers: map[string]FencedHandlerFunc{
			"delivery": func(ctx context.Context, claim FencedClaim) error {
				return errDeadLetterProbe
			},
		},
		DeadLetters: map[string]DeadLetterFunc{
			"delivery": func(ctx context.Context, j job.Job) error {
				// Re-read the persisted status: the hook fires AFTER the dead-letter
				// transition is recorded, so the store already shows dead_letter.
				stored, err := fq.Get(ctx, j.JobID)
				mu.Lock()
				if err == nil {
					hookStatus = string(stored.JobStatus)
				}
				mu.Unlock()
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}

	if _, err := svc.EnqueueOnce(ctx, "delivery", "key-dl", json.RawMessage(`"x"`)); err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(runCtx) }()

	waitFor(t, time.Second, func() bool {
		s, err := svc.LatestStatusByKey(ctx, "key-dl")
		return err == nil && s == "dead_letter"
	})
	cancel()
	<-done

	mu.Lock()
	got := hookStatus
	mu.Unlock()
	if got != "dead_letter" {
		t.Fatalf("dead-letter hook observed status %q, want dead_letter (fired after the transition is recorded)", got)
	}
}

// TestFencedRuntimePermanentDeadLettersImmediately proves a handler that returns the
// Permanent disposition dead-letters on the FIRST attempt — no retry — even when the
// attempt cap is far higher, while a plain (transient) error would retry (AV3D-3.4).
func TestFencedRuntimePermanentDeadLettersImmediately(t *testing.T) {
	ctx := context.Background()
	svc, fq := newFencedService(t)

	var attempts atomic.Int64
	rt, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Workers:      1,
		MaxAttempts:  50, // a high cap: a transient error would retry many times
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Backoff:      func(int) time.Duration { return time.Millisecond },
		Handlers: map[string]FencedHandlerFunc{
			"delivery": func(ctx context.Context, claim FencedClaim) error {
				attempts.Add(1)
				return Permanent("structurally dead")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}

	if _, err := svc.EnqueueOnce(ctx, "delivery", "key-perm", json.RawMessage(`"x"`)); err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(runCtx) }()

	waitFor(t, time.Second, func() bool {
		s, err := svc.LatestStatusByKey(ctx, "key-perm")
		return err == nil && s == "dead_letter"
	})
	cancel()
	<-done

	if got := attempts.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want exactly 1 (permanent dead-letters immediately, no retry)", got)
	}
	stored, err := fq.GetLatestByKey(ctx, "key-perm")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if stored.FailureReason != "structurally dead" {
		t.Fatalf("FailureReason = %q, want the permanent reason", stored.FailureReason)
	}
}

// TestNewFencedRuntime_ProcessTimeoutInsideLease proves the jobs-mode config validation
// fails loudly when the per-attempt provider timeout is not safely shorter than the
// claim lease, and accepts a timeout that sits inside it (AV3D-3.4).
func TestNewFencedRuntime_ProcessTimeoutInsideLease(t *testing.T) {
	svc, _ := newFencedService(t)
	handlers := map[string]FencedHandlerFunc{"k": func(context.Context, FencedClaim) error { return nil }}

	// Inverted: timeout >= lease → loud construction failure.
	if _, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Handlers:       handlers,
		LeaseFor:       time.Second,
		ProcessTimeout: time.Second,
	}); !errors.Is(err, ErrProcessTimeoutExceedsLease) {
		t.Fatalf("err = %v, want ErrProcessTimeoutExceedsLease for timeout == lease", err)
	}
	if _, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Handlers:       handlers,
		LeaseFor:       time.Second,
		ProcessTimeout: 2 * time.Second,
	}); !errors.Is(err, ErrProcessTimeoutExceedsLease) {
		t.Fatalf("err = %v, want ErrProcessTimeoutExceedsLease for timeout > lease", err)
	}
	// Valid: timeout safely inside the lease.
	if _, err := NewFencedRuntime(svc, FencedRuntimeConfig{
		Handlers:       handlers,
		LeaseFor:       time.Second,
		ProcessTimeout: 200 * time.Millisecond,
	}); err != nil {
		t.Fatalf("timeout inside lease should construct: %v", err)
	}
}

// TestServicePurgeTerminal proves the Service exposes the bounded terminal purge a host
// drives for retention, and that it requires the fenced queue (AV3D-3.4).
func TestServicePurgeTerminal(t *testing.T) {
	ctx := context.Background()
	svc, fq := newFencedService(t)

	// Seed a terminal (canceled) job in the past.
	id, err := svc.EnqueueOnce(ctx, "k", "key-purge", json.RawMessage(`"x"`))
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	if err := fq.Cancel(ctx, id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	n, err := svc.PurgeTerminal(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}

	// A non-fenced Service refuses.
	unfenced, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := unfenced.PurgeTerminal(ctx, time.Now(), 10); !errors.Is(err, ErrFencedQueueRequired) {
		t.Fatalf("err = %v, want ErrFencedQueueRequired", err)
	}
}

// TestNewFencedRuntime_RequiresFencedQueue proves the fenced runtime refuses to build
// without a fenced queue.
func TestNewFencedRuntime_RequiresFencedQueue(t *testing.T) {
	svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := NewFencedRuntime(svc, FencedRuntimeConfig{Handlers: map[string]FencedHandlerFunc{"k": func(context.Context, FencedClaim) error { return nil }}}); err != ErrFencedQueueRequired {
		t.Fatalf("err = %v, want ErrFencedQueueRequired", err)
	}
}
