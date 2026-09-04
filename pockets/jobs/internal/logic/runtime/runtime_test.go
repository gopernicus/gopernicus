package runtime

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// oneShotQueue yields its pending jobs once each via Claim, then ErrNoWork, and
// records Complete/Fail outcomes.
type oneShotQueue struct {
	mu        sync.Mutex
	kinds     [][]string // the kinds each Claim was handed
	pending   []job.Job
	completed map[string]bool
	failed    map[string]string
	done      chan string // job id signalled after Complete or Fail
}

func newOneShotQueue(jobs ...job.Job) *oneShotQueue {
	return &oneShotQueue{
		pending:   jobs,
		completed: map[string]bool{},
		failed:    map[string]string{},
		done:      make(chan string, 8),
	}
}

func (q *oneShotQueue) Enqueue(ctx context.Context, in job.Enqueue) (job.Job, error) {
	return job.Job{}, sdk.ErrInvalidInput
}
func (q *oneShotQueue) Claim(ctx context.Context, workerID string, now time.Time, kinds []string) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.kinds = append(q.kinds, kinds)
	if len(q.pending) == 0 {
		return job.Job{}, workers.ErrNoWork
	}
	j := q.pending[0]
	q.pending = q.pending[1:]
	return j, nil
}
func (q *oneShotQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	q.completed[jobID] = true
	q.mu.Unlock()
	q.done <- jobID
	return nil
}
func (q *oneShotQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	q.failed[jobID] = reason
	q.mu.Unlock()
	q.done <- jobID
	return nil
}
func (q *oneShotQueue) Get(ctx context.Context, id string) (job.Job, error) {
	return job.Job{}, sdk.ErrNotFound
}
func (q *oneShotQueue) List(ctx context.Context, _ job.ListFilter, _ crud.ListRequest) (crud.Page[job.Job], error) {
	return crud.Page[job.Job]{}, nil
}

func runBriefly(t *testing.T, rt *Runtime, q *oneShotQueue) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- rt.Run(ctx) }()

	select {
	case <-q.done:
	case <-time.After(2 * time.Second):
		t.Fatal("job was not processed within 2s")
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRun_UnknownKind_Fails(t *testing.T) {
	q := newOneShotQueue(job.Job{JobID: "j1", Kind: "mystery.kind", JobStatus: job.StatusPending})
	rt := New(Deps{
		Queue:        q,
		Handlers:     map[string]HandlerFunc{"known.kind": func(context.Context, job.Job) error { return nil }},
		Workers:      1,
		PollInterval: 20 * time.Millisecond,
		IdleInterval: 20 * time.Millisecond,
	})

	runBriefly(t, rt, q)

	reason, ok := q.failed["j1"]
	if !ok {
		t.Fatal("unknown-kind job must be failed")
	}
	if !strings.Contains(reason, "mystery.kind") {
		t.Fatalf("fail reason %q should name the kind", reason)
	}
	if q.completed["j1"] {
		t.Fatal("unknown-kind job must not be completed")
	}
}

// TestRun_ClaimsWithRegisteredKinds proves the kind-scoped adapter hands
// Deps.Kinds to every Claim (#37): the store, not the handler lookup, is what
// keeps a runtime off jobs it cannot process.
func TestRun_ClaimsWithRegisteredKinds(t *testing.T) {
	q := newOneShotQueue(job.Job{JobID: "j3", Kind: "known.kind", JobStatus: job.StatusPending})
	rt := New(Deps{
		Queue:        q,
		Handlers:     map[string]HandlerFunc{"known.kind": func(context.Context, job.Job) error { return nil }},
		Kinds:        []string{"known.kind"},
		Workers:      1,
		PollInterval: 20 * time.Millisecond,
		IdleInterval: 20 * time.Millisecond,
	})

	runBriefly(t, rt, q)

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.kinds) == 0 {
		t.Fatal("Claim was never called")
	}
	for i, got := range q.kinds {
		if len(got) != 1 || got[0] != "known.kind" {
			t.Fatalf("Claim %d kinds = %v, want [known.kind]", i, got)
		}
	}
}

func TestRun_KnownKind_Completes(t *testing.T) {
	q := newOneShotQueue(job.Job{JobID: "j2", Kind: "known.kind", JobStatus: job.StatusPending})
	var called bool
	rt := New(Deps{
		Queue: q,
		Handlers: map[string]HandlerFunc{"known.kind": func(context.Context, job.Job) error {
			called = true
			return nil
		}},
		Workers:      1,
		PollInterval: 20 * time.Millisecond,
		IdleInterval: 20 * time.Millisecond,
	})

	runBriefly(t, rt, q)

	if !called {
		t.Fatal("handler was not invoked")
	}
	if !q.completed["j2"] {
		t.Fatal("known-kind job must be completed")
	}
}

func TestRun_CancelDrainsInFlightHandlerAndPersistsCompletion(t *testing.T) {
	q := newOneShotQueue(job.Job{JobID: "j-drain", Kind: "known.kind", JobStatus: job.StatusPending})
	started := make(chan struct{})
	release := make(chan struct{})

	rt := New(Deps{
		Queue: q,
		Handlers: map[string]HandlerFunc{"known.kind": func(ctx context.Context, _ job.Job) error {
			close(started)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		}},
		Workers:      1,
		PollInterval: 20 * time.Millisecond,
		IdleInterval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rt.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()

	select {
	case err := <-errCh:
		t.Fatalf("Run returned before the in-flight handler drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	select {
	case id := <-q.done:
		if id != "j-drain" {
			t.Fatalf("completed id = %q, want j-drain", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight completion was not persisted")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error after clean drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after in-flight completion persisted")
	}

	q.mu.Lock()
	completed := q.completed["j-drain"]
	q.mu.Unlock()
	if !completed {
		t.Fatal("cancellation prevented durable completion")
	}
}

// lockedBuffer is a goroutine-safe log sink for capturing pool output.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestHeartbeatReachesThePools proves Deps.Heartbeat is passed through to the
// worker pools (workers.WithHeartbeat, sdk v0.7.1): an IDLE queue pool with a
// heartbeat set beats "pool alive" — an all-zero beat from a workless pool is
// the liveness signal the option exists for. Zero stays off upstream (no
// clamp), so the passthrough is the only thing this package has to prove.
func TestHeartbeatReachesThePools(t *testing.T) {
	buf := &lockedBuffer{}
	log := slog.New(slog.NewTextHandler(buf, nil))

	rt := New(Deps{
		Queue:        newOneShotQueue(), // empty: every claim is ErrNoWork
		Handlers:     map[string]HandlerFunc{"noop": func(context.Context, job.Job) error { return nil }},
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Heartbeat:    20 * time.Millisecond,
		Logger:       log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "pool alive") {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "pool alive") {
		t.Fatalf("no \"pool alive\" heartbeat line reached the logger; Deps.Heartbeat is not passed to the pool")
	}
}
