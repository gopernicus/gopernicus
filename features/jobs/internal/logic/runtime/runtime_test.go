package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// oneShotQueue yields its pending jobs once each via Claim, then ErrNoWork, and
// records Complete/Fail outcomes.
type oneShotQueue struct {
	mu        sync.Mutex
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
	return job.Job{}, errs.ErrInvalidInput
}
func (q *oneShotQueue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return job.Job{}, workers.ErrNoWork
	}
	j := q.pending[0]
	q.pending = q.pending[1:]
	return j, nil
}
func (q *oneShotQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	q.mu.Lock()
	q.completed[jobID] = true
	q.mu.Unlock()
	q.done <- jobID
	return nil
}
func (q *oneShotQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	q.mu.Lock()
	q.failed[jobID] = reason
	q.mu.Unlock()
	q.done <- jobID
	return nil
}
func (q *oneShotQueue) Get(ctx context.Context, id string) (job.Job, error) {
	return job.Job{}, errs.ErrNotFound
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
