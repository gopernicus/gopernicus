package queuesvc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/logic/job"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// fakeQueue is an in-test QueueRepository. Only Enqueue carries behavior; the
// rest satisfy the port so the compile-time seam holds.
type fakeQueue struct {
	mu    sync.Mutex
	jobs  map[string]job.Job
	seq   int
	calls []job.Enqueue
}

func newFakeQueue() *fakeQueue { return &fakeQueue{jobs: map[string]job.Job{}} }

func (f *fakeQueue) Enqueue(ctx context.Context, in job.Enqueue) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	id := in.ID
	if id == "" {
		id = "gen-" + strconv.Itoa(f.seq)
		f.seq++
	}
	if _, ok := f.jobs[id]; ok {
		return job.Job{}, fmt.Errorf("duplicate %s: %w", id, errs.ErrAlreadyExists)
	}
	j := job.Job{
		JobID:        id,
		Kind:         in.Kind,
		Payload:      in.Payload,
		JobStatus:    job.StatusPending,
		MaxAttempts:  in.MaxAttempts,
		ScheduledFor: in.ScheduledFor,
	}
	f.jobs[id] = j
	return j, nil
}

func (f *fakeQueue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	return job.Job{}, workers.ErrNoWork
}
func (f *fakeQueue) Complete(ctx context.Context, jobID string, now time.Time) error { return nil }
func (f *fakeQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return nil
}
func (f *fakeQueue) Get(ctx context.Context, id string) (job.Job, error) {
	return job.Job{}, errs.ErrNotFound
}
func (f *fakeQueue) List(ctx context.Context, _ job.ListFilter, _ crud.ListRequest) (crud.Page[job.Job], error) {
	return crud.Page[job.Job]{}, nil
}

func drained(wake <-chan struct{}) bool {
	select {
	case <-wake:
		return true
	default:
		return false
	}
}

func TestEnqueueJob_Idempotency(t *testing.T) {
	q := newFakeQueue()
	svc := NewService(q, 3, func() time.Time { return time.Unix(1000, 0).UTC() })

	first, err := svc.EnqueueJob(context.Background(), job.Enqueue{ID: "dup", Kind: "demo"})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if first.ID() != "dup" {
		t.Fatalf("id = %q, want dup", first.ID())
	}
	// Drain the wake from the first (successful) enqueue.
	if !drained(svc.Wake()) {
		t.Fatal("expected wake after first enqueue")
	}

	_, err = svc.EnqueueJob(context.Background(), job.Enqueue{ID: "dup", Kind: "demo"})
	if !errors.Is(err, errs.ErrAlreadyExists) {
		t.Fatalf("second enqueue err = %v, want ErrAlreadyExists", err)
	}
	// A rejected duplicate must NOT signal the wake (nothing new ran).
	if drained(svc.Wake()) {
		t.Fatal("duplicate enqueue must not signal wake")
	}
}

func TestEnqueue_SignalsWakeAndCoalesces(t *testing.T) {
	q := newFakeQueue()
	svc := NewService(q, 3, nil)

	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// A second, distinct enqueue before draining: the cap-1 buffer coalesces.
	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}

	if !drained(svc.Wake()) {
		t.Fatal("expected a buffered wake")
	}
	if drained(svc.Wake()) {
		t.Fatal("two enqueues must coalesce into one buffered wake")
	}
}

func TestEnqueueJob_KindRequired(t *testing.T) {
	q := newFakeQueue()
	svc := NewService(q, 3, nil)

	_, err := svc.EnqueueJob(context.Background(), job.Enqueue{Kind: ""})
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if drained(svc.Wake()) {
		t.Fatal("invalid enqueue must not signal wake")
	}
	if len(q.calls) != 0 {
		t.Fatalf("store must not be called on invalid input, got %d calls", len(q.calls))
	}
}

func TestEnqueueJob_AppliesDefaults(t *testing.T) {
	q := newFakeQueue()
	fixed := time.Unix(5000, 0).UTC()
	svc := NewService(q, 7, func() time.Time { return fixed })

	if _, err := svc.EnqueueJob(context.Background(), job.Enqueue{Kind: "demo"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got := q.calls[0]
	if got.MaxAttempts != 7 {
		t.Fatalf("MaxAttempts = %d, want 7 (default)", got.MaxAttempts)
	}
	if !got.ScheduledFor.Equal(fixed) {
		t.Fatalf("ScheduledFor = %v, want %v (now)", got.ScheduledFor, fixed)
	}
}
