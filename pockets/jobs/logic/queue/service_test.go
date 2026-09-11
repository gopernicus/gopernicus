package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// fakeQueue is an in-test QueueRepository. Only Enqueue carries behavior; the
// rest satisfy the port so the compile-time seam holds.
type fakeQueue struct {
	mu    sync.Mutex
	jobs  map[string]Job
	seq   int
	calls []Enqueue
}

func newFakeQueue() *fakeQueue { return &fakeQueue{jobs: map[string]Job{}} }

func (f *fakeQueue) Enqueue(ctx context.Context, in Enqueue) (Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	id := in.ID
	if id == "" {
		id = "gen-" + strconv.Itoa(f.seq)
		f.seq++
	}
	if _, ok := f.jobs[id]; ok {
		return Job{}, fmt.Errorf("duplicate %s: %w", id, sdk.ErrAlreadyExists)
	}
	j := Job{
		JobID:        id,
		Kind:         in.Kind,
		Payload:      in.Payload,
		JobStatus:    StatusPending,
		MaxAttempts:  in.MaxAttempts,
		ScheduledFor: in.ScheduledFor,
	}
	f.jobs[id] = j
	return j, nil
}

func (f *fakeQueue) Claim(ctx context.Context, workerID string, now time.Time, _ []string) (Job, error) {
	return Job{}, workers.ErrNoWork
}
func (f *fakeQueue) Complete(ctx context.Context, jobID string, now time.Time) error { return nil }
func (f *fakeQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return nil
}
func (f *fakeQueue) Get(ctx context.Context, id string) (Job, error) {
	return Job{}, sdk.ErrNotFound
}
func (f *fakeQueue) List(ctx context.Context, _ ListFilter, _ list.Request) (list.Page[Job], error) {
	return list.Page[Job]{}, nil
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
	svc := newQueueService(t, Repositories{Queue: q}, WithMaxAttempts(3), WithClock(func() time.Time { return time.Unix(1000, 0).UTC() }))

	first, err := svc.EnqueueJob(context.Background(), Enqueue{ID: "dup", Kind: "demo"})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if first.ID() != "dup" {
		t.Fatalf("id = %q, want dup", first.ID())
	}
	// Drain the wake from the first (successful) enqueue.
	if !drained(svc.wake) {
		t.Fatal("expected wake after first enqueue")
	}

	_, err = svc.EnqueueJob(context.Background(), Enqueue{ID: "dup", Kind: "demo"})
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("second enqueue err = %v, want ErrAlreadyExists", err)
	}
	// A rejected duplicate must NOT signal the wake (nothing new ran).
	if drained(svc.wake) {
		t.Fatal("duplicate enqueue must not signal wake")
	}
}

func TestEnqueue_SignalsWakeAndCoalesces(t *testing.T) {
	q := newFakeQueue()
	svc := newQueueService(t, Repositories{Queue: q}, WithMaxAttempts(3), WithClock(nil))

	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// A second, distinct enqueue before draining: the cap-1 buffer coalesces.
	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}

	if !drained(svc.wake) {
		t.Fatal("expected a buffered wake")
	}
	if drained(svc.wake) {
		t.Fatal("two enqueues must coalesce into one buffered wake")
	}
}

func TestEnqueueJob_KindRequired(t *testing.T) {
	q := newFakeQueue()
	svc := newQueueService(t, Repositories{Queue: q}, WithMaxAttempts(3), WithClock(nil))

	_, err := svc.EnqueueJob(context.Background(), Enqueue{Kind: ""})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if drained(svc.wake) {
		t.Fatal("invalid enqueue must not signal wake")
	}
	if len(q.calls) != 0 {
		t.Fatalf("store must not be called on invalid input, got %d calls", len(q.calls))
	}
}

func TestEnqueueJob_AppliesDefaults(t *testing.T) {
	q := newFakeQueue()
	fixed := time.Unix(5000, 0).UTC()
	svc := newQueueService(t, Repositories{Queue: q}, WithMaxAttempts(7), WithClock(func() time.Time { return fixed }))

	if _, err := svc.EnqueueJob(context.Background(), Enqueue{Kind: "demo"}); err != nil {
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

func newQueueService(t *testing.T, repos Repositories, opts ...Option) *Service {
	t.Helper()
	svc, err := NewService(repos, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestConstructionOptionsResolveBeforeAdmission(t *testing.T) {
	fixed := time.Unix(1700000000, 0).UTC()
	option := WithClock(func() time.Time { return fixed })
	for _, tc := range []struct {
		name     string
		options  []Option
		attempts int
	}{
		{"defaults", []Option{option}, 3},
		{"last value", []Option{WithMaxAttempts(7), WithMaxAttempts(4), option}, 4},
		{"reset default", []Option{WithMaxAttempts(7), WithMaxAttempts(0), option}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newQueueService(t, Repositories{Queue: newFakeQueue()}, tc.options...)
			admitted, err := svc.EnqueueJob(context.Background(), Enqueue{Kind: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if admitted.MaxAttempts != tc.attempts || !admitted.ScheduledFor.Equal(fixed) {
				t.Fatalf("admission = %+v; want %d attempts at %v", admitted, tc.attempts, fixed)
			}
		})
	}
	if _, err := NewService(Repositories{Queue: newFakeQueue()}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
}
