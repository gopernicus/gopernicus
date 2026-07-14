package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// Fenced-runtime tuning defaults. Each zero field in FencedRuntimeConfig selects
// its default so a consumer wires only the knobs it cares about.
const (
	// defaultFencedWorkers is the fenced pool size when FencedRuntimeConfig.Workers is 0.
	defaultFencedWorkers = 2
	// defaultFencedLeaseFor bounds one claim's exclusive hold; a crashed worker's job
	// is reclaimable after it lapses.
	defaultFencedLeaseFor = 30 * time.Second
	// defaultFencedMaxAttempts caps process attempts before a fenced job dead-letters.
	defaultFencedMaxAttempts = 5
	// defaultFencedBackoffBase is the first retry delay; each further attempt doubles it.
	defaultFencedBackoffBase = 5 * time.Second
	// defaultFencedBackoffCap ceilings the exponential retry backoff.
	defaultFencedBackoffCap = 5 * time.Minute
)

// Compile-time seams: the Service implements the canonical keyed-work submission
// protocol (sdk/capabilities/work) — the jobs feature is its implementation of
// record. A consuming feature depends on the sdk ports, never on this package
// (constitution rule 6). The protocol methods are backed by the lease-fenced queue
// (Repositories.FencedQueue).
var (
	_ work.Enqueuer     = (*Service)(nil)
	_ work.Replacer     = (*Service)(nil)
	_ work.StatusReader = (*Service)(nil)
)

// EnqueueOnce admits payload under logicalKey exactly once (idempotent while an
// active execution holds the key), returning the unique execution ID. It is the
// producer half of the work.Enqueuer protocol, backed by the fenced queue. payload
// is opaque bytes the queue never interprets; the Service deep-copies it at the
// protocol boundary so a later caller mutation cannot alter the admitted work.
func (s *Service) EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.EnqueueOnce(ctx, job.Enqueue{Kind: kind, LogicalKey: logicalKey, Payload: json.RawMessage(bytes.Clone(payload))})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// Replace supersedes every active execution holding logicalKey and inserts one
// fresh execution, returning its ID — the user-requested resend. It is the
// work.Replacer protocol. payload is opaque bytes the queue never interprets; the
// Service deep-copies it at the protocol boundary so a later caller mutation cannot
// alter the admitted work.
func (s *Service) Replace(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.Replace(ctx, job.Enqueue{Kind: kind, LogicalKey: logicalKey, Payload: json.RawMessage(bytes.Clone(payload))})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// LatestStatusByKey returns the lifecycle status of the most-recent execution
// holding logicalKey, or sdk.ErrNotFound when the key names no work. It is the
// work.StatusReader protocol: it reveals only the lifecycle status, never payload,
// destination, or secret, and never leases or mutates.
func (s *Service) LatestStatusByKey(ctx context.Context, logicalKey string) (work.Status, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.GetLatestByKey(ctx, logicalKey)
	if err != nil {
		return "", err
	}
	return j.JobStatus, nil
}

// Checkpoint atomically replaces the payload of the running execution while the
// caller still holds the current lease (executionID + leaseID), preserving job
// identity and status. A stale or superseded lease fails with sdk.ErrConflict. It
// is the executor-side checkpoint an opaque delivery records BEFORE its side effect
// so every retry resends the identical rendered bytes. Executor-side, out of the
// work protocol (D3): a consuming processor redeclares this method structurally.
func (s *Service) Checkpoint(ctx context.Context, executionID, leaseID string, payload json.RawMessage) error {
	if s.fencedQueue == nil {
		return ErrFencedQueueRequired
	}
	return s.fencedQueue.Checkpoint(ctx, executionID, leaseID, payload, time.Now().UTC())
}

// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or before
// before, returning the number removed. It is the bounded-retention seam a host (or a
// composition adapter) drives to bound terminal-status retention WITHOUT any
// consumer-specific SQL: the retention window (before) and batch size (limit) are the
// caller's policy, and only terminal generations are ever removed (AV3D-3.4). A
// non-fenced Service returns ErrFencedQueueRequired.
func (s *Service) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	if s.fencedQueue == nil {
		return 0, ErrFencedQueueRequired
	}
	return s.fencedQueue.PurgeTerminal(ctx, before, limit)
}

// Permanent wraps reason as a handler disposition that dead-letters the fenced job
// IMMEDIATELY — no retry, regardless of the attempt count. A FencedHandlerFunc returns
// it when a processor classifies a failure as permanent (a structurally-dead payload
// or an exhausted budget), and the FencedRuntime's error-aware decider routes it onto
// the runner's Fail path so the per-kind dead-letter hook runs (AV3D-3.4). reason must
// be secret-free; it becomes the job's FailureReason.
func Permanent(reason string) error { return &dispositionError{reason: reason, permanent: true} }

// dispositionError carries a handler's explicit retry/permanent verdict as an error so
// it crosses the stdlib-typed FencedHandlerFunc seam. A consuming feature need not
// import this package to signal permanence — it returns any error and lets the host's
// composition adapter re-wrap it via Permanent — but the fenced runtime recognizes this
// concrete type via errors.As.
type dispositionError struct {
	reason    string
	permanent bool
}

func (e *dispositionError) Error() string { return e.reason }

// isPermanent reports whether a handler error carries the permanent disposition.
func isPermanent(err error) bool {
	var d *dispositionError
	if errors.As(err, &d) {
		return d.permanent
	}
	return false
}

// FencedClaim is one claimed fenced job handed to a FencedHandlerFunc. It is
// stdlib-typed so a consuming feature's handler matches it structurally with no
// import of the jobs domain. Checkpoint persists a fresh payload under the current
// claim's fence (execution + lease); a stale/superseded claim's checkpoint fails
// and the handler MUST NOT perform its side effect.
type FencedClaim struct {
	ExecutionID string
	LeaseID     string
	Payload     json.RawMessage
	// Attempt is the number of process attempts already spent for retry
	// classification (the claim increments it).
	Attempt    int
	Checkpoint func(ctx context.Context, payload json.RawMessage) error
}

// FencedHandlerFunc processes one claimed fenced job of a registered kind.
// Returning nil COMPLETES the job (a delivered message or a non-failed skip); a
// non-nil error triggers the runtime's retry/dead-letter policy. It is
// stdlib-typed so a consuming feature's delivery handler plugs in without importing
// the jobs domain.
type FencedHandlerFunc func(ctx context.Context, claim FencedClaim) error

// Compile-time seam: the frozen per-kind DeadLetterFunc is exactly the kernel's
// generic fenced dead-letter hook specialized to job.Job, so a host registers it on
// a workers.FencedRunner[job.Job] with a direct conversion and no adapter — the
// runner fires it only after the permanent dead-letter transition is recorded
// (AV3D-1.4). The conversion below fails to compile if the two shapes ever drift.
var _ = func(f DeadLetterFunc) workers.FencedDeadLetterFunc[job.Job] {
	return workers.FencedDeadLetterFunc[job.Job](f)
}

// DeadLetterFunc is the FROZEN (AV3D-0.3) per-kind terminal hook a host registers
// for permanent-failure cleanup — e.g. discarding an undeliverable challenge. It
// runs ONLY AFTER the dead-letter transition is durably recorded, and its failure
// is logged/reported but never resurrects the job. As of AV3D-1.4 it is exactly the
// kernel's workers.FencedDeadLetterFunc[job.Job] (the compile seam above), which a
// fenced runtime sets via FencedRunner.SetDeadLetterHook and fires post-transition.
// It carries the domain job.Job because it is a host-registered hook, not a
// cross-feature structural seam.
type DeadLetterFunc func(ctx context.Context, j job.Job) error

// FencedRuntimeConfig configures the FencedRuntime. Handlers is required non-empty;
// every zero sizing field selects its package default.
type FencedRuntimeConfig struct {
	// Handlers maps a job kind to its fenced handler; required non-empty.
	Handlers map[string]FencedHandlerFunc
	// DeadLetters maps a job kind to its per-kind terminal hook, fired only AFTER the
	// dead-letter transition is durably recorded (never before, never on a retry, and
	// never on a lease-fenced failure). Optional; a kind with no entry runs no hook.
	DeadLetters map[string]DeadLetterFunc
	// Workers is the fenced pool size; 0 → defaultFencedWorkers.
	Workers int
	// PollInterval / IdleInterval tune the pool cadence; 0 → the pool defaults.
	PollInterval time.Duration
	IdleInterval time.Duration
	// LeaseFor is the claim lease each Claim requests; 0 → defaultFencedLeaseFor. Set
	// it above a job's expected processing time and below the store's reclaim cadence.
	LeaseFor time.Duration
	// ProcessTimeout bounds each handler attempt with a child-context deadline that
	// sits inside the lease; 0 → no per-attempt timeout.
	ProcessTimeout time.Duration
	// MaxAttempts caps process attempts before a job dead-letters; 0 →
	// defaultFencedMaxAttempts.
	MaxAttempts int
	// Backoff maps a just-spent attempt (1-based) to the retry-at delay; nil selects a
	// capped exponential.
	Backoff func(attempt int) time.Duration
	// Clock overrides the time source (primarily for deterministic tests); nil →
	// time.Now().UTC().
	Clock func() time.Time
	// Logger is the pool's operational logger; nil → slog.Default().
	Logger *slog.Logger
}

// FencedRuntime runs the lease-fenced pool that drives a consuming feature's
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
func NewFencedRuntime(svc *Service, cfg FencedRuntimeConfig) (*FencedRuntime, error) {
	if svc.fencedQueue == nil {
		return nil, ErrFencedQueueRequired
	}
	if len(cfg.Handlers) == 0 {
		return nil, ErrHandlersRequired
	}
	for kind, h := range cfg.Handlers {
		if kind == "" || h == nil {
			return nil, ErrInvalidHandler
		}
	}

	handlers := make(map[string]FencedHandlerFunc, len(cfg.Handlers))
	for k, v := range cfg.Handlers {
		handlers[k] = v
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
	process := func(ctx context.Context, j job.Job) (job.Job, error) {
		handler, ok := handlers[j.Kind]
		if !ok {
			// An unregistered kind cannot be processed here; returning an error takes the
			// dead-letter path so it never wedges the pool.
			return j, errUnhandledKind
		}
		claim := FencedClaim{
			ExecutionID: j.JobID,
			LeaseID:     j.LeaseID,
			Payload:     j.Payload,
			Attempt:     j.Retries,
			Checkpoint: func(ctx context.Context, payload json.RawMessage) error {
				return store.Checkpoint(ctx, j.JobID, j.LeaseID, payload, clock())
			},
		}
		return j, handler(ctx, claim)
	}

	runner := workers.NewFencedRunner(
		store,
		process,
		logger,
		workers.WithFencedClock(clock),
		workers.WithLeaseDuration(leaseFor),
		workers.WithFencedProcessTimeout(cfg.ProcessTimeout),
		// Error-aware retry policy (AV3D-3.4): a handler that returns a Permanent
		// disposition dead-letters IMMEDIATELY (the consuming processor's permanent
		// verdict — a structurally-dead payload or an exhausted budget); any other
		// failure is a transient retry rescheduled to clock()+backoff(attempt) (a durable
		// retry-at, no busy-loop) until the bounded attempt cap, where it dead-letters.
		workers.WithFencedRetryDecider(func(err error, attempt int) (time.Duration, bool) {
			if isPermanent(err) {
				return 0, false
			}
			if attempt >= maxAttempts {
				return 0, false
			}
			return backoff(attempt), true
		}),
	)
	if len(deadLetters) > 0 {
		runner.SetDeadLetterHook(func(ctx context.Context, j job.Job) error {
			if hook, ok := deadLetters[j.Kind]; ok {
				return hook(ctx, j)
			}
			return nil
		})
	}

	pool := workers.NewPool(runner.WorkFunc(),
		workers.WithName("fenced-delivery"),
		workers.WithWorkerCount(workerCount),
		poolInterval(workers.WithPollInterval, cfg.PollInterval),
		poolInterval(workers.WithIdleInterval, cfg.IdleInterval),
		workers.WithLogger(logger),
	)

	return &FencedRuntime{pool: pool}, nil
}

// Run blocks running the fenced pool; cancel ctx to drain gracefully.
func (r *FencedRuntime) Run(ctx context.Context) error { return r.pool.Run(ctx) }

// errUnhandledKind is the sentinel a fenced process returns for a job whose kind
// has no registered handler.
var errUnhandledKind = errUnhandled{}

type errUnhandled struct{}

func (errUnhandled) Error() string { return "jobs: no fenced handler registered for job kind" }

// poolInterval applies a pool cadence option only when d is positive, so a zero
// value falls through to the pool's own default rather than clamping.
func poolInterval(opt func(time.Duration) workers.PoolOption, d time.Duration) workers.PoolOption {
	if d <= 0 {
		// A no-op option: WithMiddleware() adds nothing.
		return workers.WithMiddleware()
	}
	return opt(d)
}

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
