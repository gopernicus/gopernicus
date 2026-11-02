package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Permanent forwards to workers.Reject for immediate dead-lettering. The runner
// classifies it before retry policy; fenced dead-letter hooks run only after the
// failure is persisted. The reason is stored as FailureReason and must be
// suitable for operational logs/storage.
func Permanent(reason string) error { return workers.Reject(reason) }

// FencedRuntime runs the lease-fenced pool that drives a consuming pocket's
// durable delivery on the FencedQueue: it claims due jobs, hands each registered
// handler a checkpoint-capable FencedClaim, and applies retry-at / dead-letter
// policy with the per-kind terminal hook. It is built from a constructed Service
// (which owns the FencedQueue); Register starts no goroutine — the host runs this
// runtime explicitly.
type FencedRuntime struct {
	pool *workers.Pool
}

// NewFencedRuntime builds the fenced runtime from the built Service. It requires
// Repositories.FencedQueue and at least one handler. It starts nothing; the host
// runs Run.
func NewFencedRuntime(svc *Service, inputHandlers map[string]FencedHandlerFunc, opts ...FencedRuntimeOption) (*FencedRuntime, error) {
	cfg := fencedRuntimeConfig{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("jobs: nil fenced runtime option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if svc == nil || svc.fencedQueue == nil {
		return nil, ErrFencedQueueRequired
	}
	if len(inputHandlers) == 0 {
		return nil, ErrHandlersRequired
	}
	for kind, h := range inputHandlers {
		if kind == "" || h == nil {
			return nil, ErrInvalidHandler
		}
	}

	handlers := make(map[string]FencedHandlerFunc, len(inputHandlers))
	for k, v := range inputHandlers {
		handlers[k] = FencedHandlerFunc(workers.ChainJobMiddleware(workers.ProcessFunc[FencedClaim](v), cfg.JobMiddleware...))
	}
	deadLetters := make(map[string]DeadLetterFunc, len(cfg.DeadLetters))
	for k, v := range cfg.DeadLetters {
		if v != nil {
			deadLetters[k] = v
		}
	}

	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultFencedMaxAttempts
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = fencedBackoff
	}
	leaseFor := cfg.LeaseFor
	if leaseFor <= 0 {
		leaseFor = defaultFencedLeaseFor
	}
	// Provider timeout inside the lease (standing invariant): a per-attempt timeout at
	// or beyond the lease could leave a bounded provider call still running after the
	// lease lapses and a second worker reclaims the job. Fail loudly at construction.
	if cfg.ProcessTimeout > 0 && cfg.ProcessTimeout >= leaseFor {
		return nil, ErrProcessTimeoutExceedsLease
	}
	drainTimeout := cfg.DrainTimeout
	if drainTimeout > leaseFor {
		return nil, ErrDrainTimeoutExceedsLease
	}
	if drainTimeout == 0 {
		drainTimeout = leaseFor
	}
	margin := leaseMargin(leaseFor)
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	workerCount := cfg.Workers
	if workerCount <= 0 {
		workerCount = defaultFencedWorkers
	}

	store := svc.fencedQueue
	kinds := handlerKinds(handlers)
	logger.Info("jobs runtime: claiming kinds", "pool", "fenced-delivery", "kinds", kinds)
	process := func(ctx context.Context, j Job) error {
		handler, ok := handlers[j.Kind]
		if !ok {
			// An unregistered kind cannot be processed here; returning an error takes the
			// dead-letter path so it never wedges the pool.
			return errUnhandledKind
		}
		if st, ok := ctx.Value(drainStateKey{}).(*drainState); ok {
			st.jobID.Store(j.JobID)
		}
		// Stop the handler before the lease lapses: past LeasedUntil another
		// worker may reclaim the job and send it again. LeasedUntil was stamped
		// from this runtime's clock (the store uses the now Claim was given).
		if !j.LeasedUntil.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, j.LeasedUntil.Sub(clock())-margin)
			defer cancel()
		}
		claim := FencedClaim{
			ExecutionID: j.JobID,
			LeaseID:     j.LeaseID,
			Payload:     j.Payload,
			TenantID:    j.TenantID,
			Attempt:     j.Retries,
			Checkpoint: func(ctx context.Context, payload json.RawMessage) error {
				return store.Checkpoint(ctx, j.JobID, j.LeaseID, payload, clock())
			},
		}
		return handler(ctx, claim)
	}

	runner := workers.NewFencedRunner(
		kindScopedFenced{repo: store, kinds: kinds},
		process,
		workers.WithFencedLogger(logger),
		workers.WithFencedClock(clock),
		workers.WithLeaseDuration(leaseFor),
		workers.WithFencedProcessTimeout(cfg.ProcessTimeout),
		// The SDK handles permanent rejection and deferral first. Ordinary errors
		// use this durable retry delay until the host's attempt ceiling.
		workers.WithFencedRetryDecider(func(err error, attempt int) (time.Duration, bool) {
			if attempt >= maxAttempts {
				return 0, false
			}
			return backoff(attempt), true
		}),
	)
	if len(deadLetters) > 0 {
		runner.SetDeadLetterHook(func(ctx context.Context, j Job, reason string) error {
			if hook, ok := deadLetters[j.Kind]; ok {
				// j is the value as claimed; stamp the recorded terminal reason so the
				// per-kind hook sees the same FailureReason the store persisted.
				j.FailureReason = reason
				return hook(ctx, j)
			}
			return nil
		})
	}

	work := runner.WorkFunc()
	if drainTimeout > 0 {
		work = drainFenced(work, drainTimeout, logger)
	}
	pool := workers.NewPool(work,
		workers.WithMiddleware(cfg.WorkerMiddleware...),
		workers.WithName("fenced-delivery"),
		workers.WithWorkerCount(workerCount),
		workers.WithPollInterval(cfg.PollInterval),
		workers.WithDrainBacklog(true),
		workers.WithIdleInterval(cfg.IdleInterval),
		workers.WithLogger(logger),
	)

	return &FencedRuntime{pool: pool}, nil
}

// Run blocks running the fenced pool. Cancelling ctx stops new claims; claims
// already in flight keep processing and settle (complete, fail, reschedule or
// defer) on a context the cancel does not reach. Each claim's handler still
// stops before its lease lapses, and Run waits at most
// FencedRuntimePolicy.DrainTimeout (default: the lease) after the cancel. A
// claim still running at that point is cancelled and left reclaimable, so it
// may be sent again; one cut off between a successful send and settlement is
// reclaimed the same way. A negative DrainTimeout disables the drain: cancel
// aborts in-flight claims immediately, as before v0.9.0.
func (r *FencedRuntime) Run(ctx context.Context) error { return r.pool.Run(ctx) }

// drainStateKey carries the iteration's drain state through the detached
// context: the parent (Run's) context for the claim gate, and the claimed job
// id for the drain log lines.
type drainStateKey struct{}

type drainState struct {
	parent context.Context
	jobID  atomic.Value // string
}

// errDrainDeadline is the cancel cause when DrainTimeout elapses.
var errDrainDeadline = errors.New("jobs: fenced drain deadline")

// drainFenced runs each iteration on a context detached from Run's
// cancellation (values, including the worker id, are kept). When Run's ctx is
// cancelled mid-iteration it arms a bound timer; if the timer fires first, the
// detached context is cancelled and the sdk runner leaves the claim reclaimable.
// It sits inside the worker middleware, as drainInFlight does for Runtime.
func drainFenced(work workers.WorkFunc, bound time.Duration, log *slog.Logger) workers.WorkFunc {
	return func(ctx context.Context) error {
		st := &drainState{parent: ctx}
		dctx, cancel := context.WithCancelCause(context.WithValue(context.WithoutCancel(ctx), drainStateKey{}, st))
		defer cancel(nil)
		// The timer is armed even while the iteration is still claiming: a
		// claim that wins the race with the cancel is bounded too. A claim the
		// gate refuses returns at once and dctx.Done ends the wait.
		stop := context.AfterFunc(ctx, func() {
			if jobID, _ := st.jobID.Load().(string); jobID != "" {
				log.Info("fenced drain started", "pool", "fenced-delivery", "job_id", jobID, "drain_timeout", bound)
			}
			t := time.NewTimer(bound)
			defer t.Stop()
			select {
			case <-t.C:
				jobID, _ := st.jobID.Load().(string)
				log.Warn("fenced drain deadline hit; claim left reclaimable and may be sent again",
					"pool", "fenced-delivery", "job_id", jobID, "drain_timeout", bound)
				cancel(errDrainDeadline)
			case <-dctx.Done():
			}
		})
		defer stop()
		err := work(dctx)
		if err != nil && errors.Is(context.Cause(dctx), errDrainDeadline) {
			// The deadline cut settlement short. The Warn above already names
			// the job; report the shutdown, not an iteration failure.
			return ctx.Err()
		}
		return err
	}
}

// leaseMargin is the slack left between a handler's deadline and its lease
// expiry for settlement I/O: a tenth of the lease, at most one second.
func leaseMargin(leaseFor time.Duration) time.Duration {
	return min(time.Second, leaseFor/10)
}

// kindScopedFenced adapts the FencedQueueRepository (whose Claim takes a kinds
// filter) to the kernel's workers.FencedStore by closing over the runtime's
// registered kinds, so the fenced pool never leases — or spends a retry on — a
// job it has no handler for. Complete/Fail/Reschedule delegate unchanged.
type kindScopedFenced struct {
	repo  FencedQueueRepository
	kinds []string
}

var _ workers.FencedStore[Job] = kindScopedFenced{}

func (q kindScopedFenced) Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (Job, error) {
	st, ok := ctx.Value(drainStateKey{}).(*drainState)
	if !ok {
		return q.repo.Claim(ctx, now, leaseID, leaseFor, q.kinds)
	}
	// Draining: never claim after Run's ctx is cancelled. The claim itself runs
	// under that cancellation; a claim cut off mid-commit holds a lease that
	// simply expires.
	if st.parent.Err() != nil {
		return Job{}, workers.ErrNoWork
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(st.parent, cancel)()
	j, err := q.repo.Claim(cctx, now, leaseID, leaseFor, q.kinds)
	if err != nil && st.parent.Err() != nil {
		return Job{}, workers.ErrNoWork
	}
	return j, err
}

func (q kindScopedFenced) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	return q.repo.Complete(ctx, id, leaseID, now)
}

func (q kindScopedFenced) Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error {
	return q.repo.Fail(ctx, id, leaseID, reason, now)
}

func (q kindScopedFenced) Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	return q.repo.Reschedule(ctx, id, leaseID, availableAt, reason, now)
}

func (q kindScopedFenced) Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	repo, ok := q.repo.(FencedQueueDeferrer)
	if !ok {
		return workers.ErrDeferralUnsupported
	}
	return repo.Defer(ctx, id, leaseID, availableAt, reason, now)
}

// errUnhandledKind is the sentinel a fenced process returns for a job whose kind
// has no registered handler.
var errUnhandledKind = errUnhandled{}

type errUnhandled struct{}

func (errUnhandled) Error() string { return "jobs: no fenced handler registered for job kind" }

// fencedBackoff is a capped exponential: base * 2^(attempt-1), ceilinged at the cap.
func fencedBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := defaultFencedBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= defaultFencedBackoffCap {
			return defaultFencedBackoffCap
		}
	}
	if d > defaultFencedBackoffCap {
		return defaultFencedBackoffCap
	}
	return d
}
