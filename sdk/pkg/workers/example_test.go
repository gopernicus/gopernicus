package workers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type reportJob struct{ name string }

func (j reportJob) ID() string { return j.name }

// A custom queue needs only the SDK contracts, not the jobs pocket. This
// single-job example synchronizes claims, retries and optional deferral. It has
// no durability or leases: abandoned running work requires manual recovery.
type reportQueue struct {
	mu            sync.Mutex
	status        string
	due           time.Time
	failures      int
	failureReason string
}

func (q *reportQueue) Claim(_ context.Context, _ string, now time.Time) (reportJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.status != "pending" || now.Before(q.due) {
		return reportJob{}, workers.ErrNoWork
	}
	q.status = "running"
	return reportJob{name: "report-1"}, nil
}

func (q *reportQueue) Complete(_ context.Context, id string, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id != "report-1" {
		return sdk.ErrNotFound
	}
	if q.status != "running" {
		return sdk.ErrConflict
	}
	q.status = "completed"
	fmt.Println("completed", id)
	return nil
}

func (q *reportQueue) Fail(_ context.Context, id string, now time.Time, reason string, maxAttempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id != "report-1" {
		return sdk.ErrNotFound
	}
	if q.status != "running" {
		return sdk.ErrConflict
	}
	q.failures++
	q.failureReason = reason
	if q.failures >= maxAttempts {
		q.status = "dead_letter"
	} else {
		q.status = "pending"
		q.due = now
	}
	return nil
}

func (q *reportQueue) Defer(_ context.Context, id string, until time.Time, reason string, now time.Time) error {
	if !until.After(now) {
		return sdk.ErrInvalidInput
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if id != "report-1" {
		return sdk.ErrNotFound
	}
	if q.status != "running" {
		return sdk.ErrConflict
	}
	q.status = "pending"
	q.due = until
	q.failureReason = reason
	fmt.Println("deferred")
	return nil
}

func ExampleChainJobMiddleware() {
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	ready := false
	gate := func(next workers.ProcessFunc[reportJob]) workers.ProcessFunc[reportJob] {
		return func(ctx context.Context, j reportJob) error {
			if !ready {
				return workers.DeferUntil(now.Add(time.Minute), "report inputs pending")
			}
			return next(ctx, j)
		}
	}
	process := workers.ChainJobMiddleware(func(context.Context, reportJob) error { return nil }, gate)
	queue := &reportQueue{status: "pending"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := workers.NewRunner(queue, process, workers.WithRunnerLogger(log), workers.WithClock(func() time.Time { return now }))
	// A host normally passes runner.WorkFunc() to workers.NewPool. Calling one
	// iteration directly here lets the example advance its clock deterministically.
	if err := runner.WorkFunc()(ctx); err != nil {
		panic(err)
	}
	ready = true
	now = now.Add(time.Minute)
	if err := runner.WorkFunc()(ctx); err != nil {
		panic(err)
	}
	// Output:
	// deferred
	// completed report-1
}
