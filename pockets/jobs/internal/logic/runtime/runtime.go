// Package runtime assembles the jobs pocket's worker pools: a queue pool (a
// workers.Runner over the QueueRepository, dispatching each job by Kind to a
// host handler) and an optional single-worker scheduler pool. It is internal;
// the host-facing surface is package jobs (jobs.Runtime wraps this).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// HandlerFunc executes one job of a registered kind.
type HandlerFunc func(ctx context.Context, j job.Job) error

// Deps are the pool-assembly inputs jobs.NewRuntime builds from the Service.
type Deps struct {
	Queue    job.QueueRepository
	Handlers map[string]HandlerFunc
	// Kinds is the sorted, deduplicated key set of Handlers: the ONLY kinds the
	// queue pool claims (kindScopedQueue closes over it). jobs.NewRuntime derives
	// it; a nil Kinds claims every kind (tests only — a Runtime always has one).
	Kinds        []string
	Scheduler    workers.WorkFunc // nil = no scheduler pool
	Wake         <-chan struct{}  // the Service's wake channel
	Workers      int
	PollInterval time.Duration
	IdleInterval time.Duration
	Heartbeat    time.Duration // 0 = no heartbeat (workers.WithHeartbeat)
	MaxAttempts  int
	Logger       *slog.Logger
}

// Runtime holds the assembled pools.
type Runtime struct {
	queuePool     *workers.Pool
	schedulerPool *workers.Pool
}

// New assembles the queue pool (and the scheduler pool when Deps.Scheduler is
// non-nil). The queue pool receives the Service's wake channel so an enqueue
// runs promptly. An unknown job kind fails the job with a clear reason (the
// store then retries/dead-letters it) rather than silently dropping it.
func New(d Deps) *Runtime {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}

	process := func(ctx context.Context, j job.Job) (job.Job, error) {
		h, ok := d.Handlers[j.Kind]
		if !ok {
			return j, fmt.Errorf("jobs: no handler registered for kind %q: %w", j.Kind, sdk.ErrInvalidInput)
		}
		return j, h(ctx, j)
	}

	runner := workers.NewRunner[job.Job](kindScopedQueue{repo: d.Queue, kinds: d.Kinds}, process, log, workers.WithMaxAttempts(d.MaxAttempts))
	queuePool := workers.NewPool(drainInFlight(runner.WorkFunc()),
		workers.WithName("jobs-queue"),
		workers.WithWorkerCount(d.Workers),
		workers.WithPollInterval(d.PollInterval),
		workers.WithIdleInterval(d.IdleInterval),
		workers.WithWakeChannel(d.Wake),
		workers.WithHeartbeat(d.Heartbeat),
		workers.WithLogger(log),
	)

	var schedulerPool *workers.Pool
	if d.Scheduler != nil {
		schedulerPool = workers.NewPool(drainInFlight(d.Scheduler),
			workers.WithName("jobs-scheduler"),
			workers.WithWorkerCount(1),
			workers.WithPollInterval(d.PollInterval),
			workers.WithIdleInterval(d.IdleInterval),
			workers.WithHeartbeat(d.Heartbeat),
			workers.WithLogger(log),
		)
	}

	return &Runtime{queuePool: queuePool, schedulerPool: schedulerPool}
}

// kindScopedQueue adapts the pocket's QueueRepository (whose Claim takes a kinds
// filter) to the kernel's workers.JobStore by closing over the runtime's
// registered kinds: the pool never claims a job it has no handler for, so a job
// of another kind waits for the binary that owns it instead of being failed and
// eventually dead-lettered here. Complete/Fail delegate unchanged.
type kindScopedQueue struct {
	repo  job.QueueRepository
	kinds []string
}

var _ workers.JobStore[job.Job] = kindScopedQueue{}

func (q kindScopedQueue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	return q.repo.Claim(ctx, workerID, now, q.kinds)
}

func (q kindScopedQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	return q.repo.Complete(ctx, jobID, now)
}

func (q kindScopedQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return q.repo.Fail(ctx, jobID, now, reason, maxAttempts)
}

// drainInFlight preserves context values (including the pool's worker id) but
// detaches cancellation once an iteration has begun. Pool.Run checks
// cancellation before starting each iteration, so shutdown still prevents new
// claim iterations; an iteration already in progress is allowed to finish its
// handler and persist Complete/Fail before the pool returns. Handlers must own a
// timeout shorter than their queue lease so a stuck iteration cannot drain
// forever. The fenced runtime intentionally does not use this wrapper: its
// shutdown contract cancels processing and leaves the fenced lease reclaimable.
func drainInFlight(work workers.WorkFunc) workers.WorkFunc {
	return func(ctx context.Context) error {
		return work(context.WithoutCancel(ctx))
	}
}

// Run blocks running the queue pool and, when present, the scheduler pool.
// Cancelling ctx stops new iterations and drains both gracefully — in-flight
// jobs finish and persist Complete/Fail — then Run returns the joined pool
// errors (nil on a clean drain).
func (r *Runtime) Run(ctx context.Context) error {
	if r.schedulerPool == nil {
		return r.queuePool.Run(ctx)
	}

	var wg sync.WaitGroup
	var schedErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		schedErr = r.schedulerPool.Run(ctx)
	}()

	queueErr := r.queuePool.Run(ctx)
	wg.Wait()
	return errors.Join(queueErr, schedErr)
}
