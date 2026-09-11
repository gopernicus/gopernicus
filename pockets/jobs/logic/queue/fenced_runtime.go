package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

	pool := workers.NewPool(runner.WorkFunc(),
		workers.WithMiddleware(cfg.WorkerMiddleware...),
		workers.WithName("fenced-delivery"),
		workers.WithWorkerCount(workerCount),
		workers.WithPollInterval(cfg.PollInterval),
		workers.WithIdleInterval(cfg.IdleInterval),
		workers.WithLogger(logger),
	)

	return &FencedRuntime{pool: pool}, nil
}

// Run blocks running the fenced pool; cancel ctx to drain gracefully.
func (r *FencedRuntime) Run(ctx context.Context) error { return r.pool.Run(ctx) }

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
	return q.repo.Claim(ctx, now, leaseID, leaseFor, q.kinds)
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
