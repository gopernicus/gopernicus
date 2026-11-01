package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// startFenced builds a one-worker fenced runtime with the given handler and
// policy, enqueues one "delivery" job under key, and runs it. It returns the
// Run cancel func and a channel that yields Run's error.
func startFenced(t *testing.T, key string, policy queueapi.FencedRuntimePolicy, handler queueapi.FencedHandlerFunc, opts ...queueapi.FencedRuntimeOption) (func() string, context.CancelFunc, <-chan error) {
	t.Helper()
	svc, _ := newFencedService(t)
	policy.Workers = 1
	policy.PollInterval = 5 * time.Millisecond
	policy.IdleInterval = 5 * time.Millisecond
	opts = append([]queueapi.FencedRuntimeOption{queueapi.WithFencedRuntimePolicy(policy)}, opts...)
	rt, err := queueapi.NewFencedRuntime(svc.Queue, map[string]queueapi.FencedHandlerFunc{"delivery": handler}, opts...)
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}
	if _, err := svc.Queue.EnqueueOnce(context.Background(), "delivery", key, json.RawMessage(`"x"`)); err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- rt.Run(ctx) }()
	status := func() string {
		s, err := svc.Queue.LatestStatusByKey(context.Background(), key)
		if err != nil {
			t.Fatalf("LatestStatusByKey: %v", err)
		}
		return string(s)
	}
	return status, cancel, errCh
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
}

func waitRun(t *testing.T, errCh <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(within):
		t.Fatalf("Run did not return within %s", within)
		return nil
	}
}

// TestFencedRuntimeDrainCompletesInFlight: cancelling Run lets the in-flight
// handler finish on a live context and persists Complete before Run returns.
func TestFencedRuntimeDrainCompletesInFlight(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var ctxDoneEarly atomic.Bool
	status, cancel, errCh := startFenced(t, "drain-ok", queueapi.FencedRuntimePolicy{}, func(ctx context.Context, _ queueapi.FencedClaim) error {
		close(started)
		select {
		case <-ctx.Done():
			ctxDoneEarly.Store(true)
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	waitStarted(t, started)
	cancel()
	select {
	case err := <-errCh:
		t.Fatalf("Run returned before the in-flight claim drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := waitRun(t, errCh, 2*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil after a clean drain", err)
	}
	if ctxDoneEarly.Load() {
		t.Fatal("handler context was cancelled by Run's cancel; want it to drain")
	}
	if got := status(); got != "completed" {
		t.Fatalf("status = %q, want completed", got)
	}
}

// TestFencedRuntimeDrainDeadline: a claim still running when DrainTimeout
// elapses is cancelled, left unsettled (reclaimable), and Run returns.
func TestFencedRuntimeDrainDeadline(t *testing.T) {
	started := make(chan struct{})
	status, cancel, errCh := startFenced(t, "drain-cap", queueapi.FencedRuntimePolicy{LeaseFor: 5 * time.Second, DrainTimeout: 100 * time.Millisecond}, func(ctx context.Context, _ queueapi.FencedClaim) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	waitStarted(t, started)
	begin := time.Now()
	cancel()
	if err := waitRun(t, errCh, 2*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if took := time.Since(begin); took < 90*time.Millisecond {
		t.Fatalf("Run returned after %s, before the 100ms drain bound", took)
	}
	if got := status(); got != "running" {
		t.Fatalf("status = %q, want running (left reclaimable, not settled)", got)
	}
}

// TestFencedRuntimeLeaseAnchorsHandlerDeadline: the handler is stopped before
// its lease lapses even when the shutdown cap would allow longer, so a drain
// can never overlap another worker's reclaim.
func TestFencedRuntimeLeaseAnchorsHandlerDeadline(t *testing.T) {
	const lease = 500 * time.Millisecond // margin = 50ms
	started := make(chan struct{})
	var claimedAt time.Time
	ended := make(chan time.Duration, 1)
	_, cancel, errCh := startFenced(t, "drain-lease", queueapi.FencedRuntimePolicy{LeaseFor: lease, DrainTimeout: lease}, func(ctx context.Context, _ queueapi.FencedClaim) error {
		claimedAt = time.Now()
		close(started)
		<-ctx.Done()
		ended <- time.Since(claimedAt)
		return workers.Reject("stop") // settle without a retry loop
	})
	waitStarted(t, started)
	time.Sleep(300 * time.Millisecond)
	cancel() // a signal-anchored cap would allow running until ~800ms
	select {
	case d := <-ended:
		if d > lease {
			t.Fatalf("handler ran %s after its claim; want it stopped before the %s lease", d, lease)
		}
		if d < 400*time.Millisecond {
			t.Fatalf("handler stopped after %s; want it to run until about lease minus margin (450ms)", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never stopped")
	}
	if err := waitRun(t, errCh, 2*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

// TestFencedRuntimeNegativeDrainAborts: a negative DrainTimeout keeps the
// pre-v0.9.0 behaviour: cancel aborts the in-flight claim at once.
func TestFencedRuntimeNegativeDrainAborts(t *testing.T) {
	started := make(chan struct{})
	status, cancel, errCh := startFenced(t, "drain-off", queueapi.FencedRuntimePolicy{LeaseFor: 5 * time.Second, DrainTimeout: -1}, func(ctx context.Context, _ queueapi.FencedClaim) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	waitStarted(t, started)
	cancel()
	if err := waitRun(t, errCh, 500*time.Millisecond); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := status(); got != "running" {
		t.Fatalf("status = %q, want running (aborted, left reclaimable)", got)
	}
}

// TestFencedRuntimeNoClaimAfterCancel: an iteration that began before the
// cancel but has not claimed yet claims nothing.
func TestFencedRuntimeNoClaimAfterCancel(t *testing.T) {
	var handled atomic.Int64
	ready := make(chan struct{})
	var cancelRun context.CancelFunc
	status, cancel, errCh := startFenced(t, "drain-gate", queueapi.FencedRuntimePolicy{}, func(context.Context, queueapi.FencedClaim) error {
		handled.Add(1)
		return nil
	}, queueapi.WithFencedWorkerMiddleware(func(next workers.WorkFunc) workers.WorkFunc {
		return func(ctx context.Context) error {
			<-ready
			cancelRun() // the pool already admitted this iteration
			return next(ctx)
		}
	}))
	cancelRun = cancel
	close(ready)
	_ = waitRun(t, errCh, 2*time.Second)
	if handled.Load() != 0 {
		t.Fatalf("handler ran %d times after Run was cancelled", handled.Load())
	}
	if got := status(); got != "pending" {
		t.Fatalf("status = %q, want pending (never claimed)", got)
	}
}

func TestNewFencedRuntime_DrainTimeoutInsideLease(t *testing.T) {
	svc, _ := newFencedService(t)
	handlers := map[string]queueapi.FencedHandlerFunc{"k": func(context.Context, queueapi.FencedClaim) error { return nil }}
	build := func(p queueapi.FencedRuntimePolicy) error {
		_, err := queueapi.NewFencedRuntime(svc.Queue, handlers, queueapi.WithFencedRuntimePolicy(p))
		return err
	}
	if err := build(queueapi.FencedRuntimePolicy{LeaseFor: time.Second, DrainTimeout: 2 * time.Second}); !errors.Is(err, queueapi.ErrDrainTimeoutExceedsLease) {
		t.Fatalf("err = %v, want ErrDrainTimeoutExceedsLease", err)
	}
	for _, d := range []time.Duration{0, -1, time.Second, 200 * time.Millisecond} {
		if err := build(queueapi.FencedRuntimePolicy{LeaseFor: time.Second, DrainTimeout: d}); err != nil {
			t.Fatalf("DrainTimeout %s: %v", d, err)
		}
	}
}
