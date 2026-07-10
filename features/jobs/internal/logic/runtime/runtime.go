// Package runtime assembles the jobs feature's worker pools: a queue pool (a
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

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// HandlerFunc executes one job of a registered kind.
type HandlerFunc func(ctx context.Context, j job.Job) error

// Deps are the pool-assembly inputs jobs.NewRuntime builds from the Service.
type Deps struct {
	Queue        job.QueueRepository
	Handlers     map[string]HandlerFunc
	Scheduler    workers.WorkFunc // nil = no scheduler pool
	Wake         <-chan struct{}  // the Service's wake channel
	Workers      int
	PollInterval time.Duration
	IdleInterval time.Duration
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

	runner := workers.NewRunner[job.Job](d.Queue, process, log, workers.WithMaxAttempts(d.MaxAttempts))
	queuePool := workers.NewPool(runner.WorkFunc(),
		workers.WithName("jobs-queue"),
		workers.WithWorkerCount(d.Workers),
		workers.WithPollInterval(d.PollInterval),
		workers.WithIdleInterval(d.IdleInterval),
		workers.WithWakeChannel(d.Wake),
		workers.WithLogger(log),
	)

	var schedulerPool *workers.Pool
	if d.Scheduler != nil {
		schedulerPool = workers.NewPool(d.Scheduler,
			workers.WithName("jobs-scheduler"),
			workers.WithWorkerCount(1),
			workers.WithPollInterval(d.PollInterval),
			workers.WithIdleInterval(d.IdleInterval),
			workers.WithLogger(log),
		)
	}

	return &Runtime{queuePool: queuePool, schedulerPool: schedulerPool}
}

// Run blocks running the queue pool and, when present, the scheduler pool.
// Cancelling ctx drains both gracefully — in-flight jobs finish and persist
// Complete/Fail — then Run returns the joined pool errors (nil on a clean drain).
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
