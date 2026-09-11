package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Scheduler supplies the ordinary runtime's optional occurrence-processing loop.
// A schedules.Service satisfies it. Nil omits the scheduler pool.
type Scheduler interface {
	WorkFunc(kinds []string) workers.WorkFunc
}

// Runtime runs the queue and (optional) scheduler pools. Build it from a
// constructed Service so the wake channel is shared by construction.
type Runtime struct {
	queuePool     *workers.Pool
	schedulerPool *workers.Pool
}

// NewRuntime builds workers over an existing queue Service and optional scheduler.
// It validates and snapshots inputHandlers and shares the Service's wake signal.
// Complete staged wiring before construction; do not mutate the input map or
// middleware slices concurrently. Each runtime owns its handler and kind snapshot.
func NewRuntime(svc *Service, inputHandlers map[string]HandlerFunc, opts ...RuntimeOption) (*Runtime, error) {
	cfg := runtimeConfig{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("jobs: nil runtime option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	schedules := cfg.scheduler
	if svc == nil {
		return nil, ErrQueueRequired
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if svc.repo == nil {
		return nil, ErrQueueRequired
	}
	if len(inputHandlers) == 0 {
		return nil, ErrHandlersRequired
	}

	handlers := make(map[string]HandlerFunc, len(inputHandlers))
	for kind, h := range inputHandlers {
		if kind == "" || h == nil {
			return nil, ErrInvalidHandler
		}
		handlers[kind] = h
	}
	kinds := handlerKinds(handlers)
	if cfg.Logger != nil {
		cfg.Logger.Info("jobs runtime: claiming kinds", "pool", "jobs-queue", "kinds", kinds)
	} else {
		slog.Info("jobs runtime: claiming kinds", "pool", "jobs-queue", "kinds", kinds)
	}

	var scheduler workers.WorkFunc
	if schedules != nil {
		scheduler = schedules.WorkFunc(kinds)
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	process := func(ctx context.Context, j Job) error {
		h, ok := handlers[j.Kind]
		if !ok {
			return fmt.Errorf("jobs: no handler registered for kind %q: %w", j.Kind, sdk.ErrInvalidInput)
		}
		return h(ctx, j)
	}

	runner := workers.NewRunner[Job](kindScopedQueue{repo: svc.repo, kinds: kinds}, workers.ChainJobMiddleware(process, cfg.JobMiddleware...), workers.WithRunnerLogger(log), workers.WithMaxAttempts(svc.maxAttempts))
	queuePool := workers.NewPool(drainInFlight(runner.WorkFunc()),
		workers.WithMiddleware(cfg.WorkerMiddleware...),
		workers.WithName("jobs-queue"),
		workers.WithWorkerCount(cfg.Workers),
		workers.WithPollInterval(cfg.PollInterval),
		workers.WithIdleInterval(cfg.IdleInterval),
		workers.WithWakeChannel(svc.wake),
		workers.WithHeartbeat(cfg.Heartbeat),
		workers.WithLogger(log),
	)

	var schedulerPool *workers.Pool
	if scheduler != nil {
		schedulerPool = workers.NewPool(drainInFlight(scheduler),
			workers.WithName("jobs-scheduler"),
			workers.WithWorkerCount(1),
			workers.WithPollInterval(cfg.PollInterval),
			workers.WithIdleInterval(cfg.IdleInterval),
			workers.WithHeartbeat(cfg.Heartbeat),
			workers.WithLogger(log),
		)
	}

	return &Runtime{queuePool: queuePool, schedulerPool: schedulerPool}, nil
}

// Run blocks running the queue pool and, when present, the scheduler pool.
// Cancelling ctx stops new iterations and drains both gracefully — in-flight
// jobs finish and persist Complete/Fail — then Run returns the joined pool
// errors (nil on a clean drain).
func (r *Runtime) Run(ctx context.Context) error {
	if r.schedulerPool == nil {
		return r.queuePool.Run(ctx)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var schedErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		schedErr = r.schedulerPool.Run(ctx)
		if schedErr != nil {
			cancel()
		}
	}()

	queueErr := r.queuePool.Run(ctx)
	if queueErr != nil {
		cancel()
	}
	wg.Wait()
	return errors.Join(queueErr, schedErr)
}

// kindScopedQueue adapts the pocket's QueueRepository (whose Claim takes a kinds
// filter) to the kernel's workers.JobStore by closing over the runtime's
// registered kinds: the pool never claims a job it has no handler for, so a job
// of another kind waits for the binary that owns it instead of being failed and
// eventually dead-lettered here. Complete/Fail delegate unchanged.
type kindScopedQueue struct {
	repo  QueueRepository
	kinds []string
}

var _ workers.JobStore[Job] = kindScopedQueue{}

func (q kindScopedQueue) Claim(ctx context.Context, workerID string, now time.Time) (Job, error) {
	return q.repo.Claim(ctx, workerID, now, q.kinds)
}

func (q kindScopedQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	return q.repo.Complete(ctx, jobID, now)
}

func (q kindScopedQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return q.repo.Fail(ctx, jobID, now, reason, maxAttempts)
}

func (q kindScopedQueue) Defer(ctx context.Context, id string, availableAt time.Time, reason string, now time.Time) error {
	repo, ok := q.repo.(QueueDeferrer)
	if !ok {
		return workers.ErrDeferralUnsupported
	}
	return repo.Defer(ctx, id, availableAt, reason, now)
}

// drainInFlight preserves context values (including the pool's worker id) but
// detaches cancellation once an iteration has begun. Pool.Run checks
// cancellation before starting each iteration, so shutdown still prevents new
// claim iterations; an iteration already in progress is allowed to finish its
// handler and persist its outcome before the pool returns. Hosts must bound
// handler execution and store I/O separately; a handler timeout does not bound
// Claim or persistence. Without those bounds an iteration can drain forever.
// The fenced runtime intentionally does not use this wrapper: its
// shutdown contract cancels processing and leaves the fenced lease reclaimable.
func drainInFlight(work workers.WorkFunc) workers.WorkFunc {
	return func(ctx context.Context) error {
		return work(context.WithoutCancel(ctx))
	}
}

// handlerKinds is the sorted, deduplicated key set of a handler registry: the
// kinds a runtime claims and the kinds its scheduler fires (#37). Sorted so log
// lines and store arguments are deterministic across runs. Nil for an empty map.
func handlerKinds[H any](handlers map[string]H) []string {
	if len(handlers) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(handlers))
	for kind := range handlers {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}
