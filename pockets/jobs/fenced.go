package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/domain/job"
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
// protocol (sdk/capabilities/work) — the jobs pocket is its implementation of
// record. A consuming pocket depends on the sdk ports, never on this package
// (constitution rule 6). The protocol methods are backed by the lease-fenced queue
// (Repositories.FencedQueue).
var (
	_ work.Enqueuer     = (*Service)(nil)
	_ work.Replacer     = (*Service)(nil)
	_ work.StatusReader = (*Service)(nil)
)

// EnqueueOnceInput is the full-fidelity input for EnqueueOnceIn: the work
// protocol's vocabulary plus the pocket's optional TenantID slot. It is the
// struct-input sibling of the frozen positional EnqueueOnce, matching the
// EnqueueJob/EnsureSchedule convention so a later optional field costs no
// signature change.
type EnqueueOnceInput struct {
	Kind       string
	LogicalKey string
	Payload    []byte
	// TenantID is the OPTIONAL host-defined boundary to stamp on the execution
	// (see job.Job.TenantID). Empty = no tenant.
	TenantID string
}

// ReplaceInput is the full-fidelity input for ReplaceIn — EnqueueOnceInput's
// supersession counterpart.
type ReplaceInput struct {
	Kind       string
	LogicalKey string
	Payload    []byte
	// TenantID is the OPTIONAL host-defined boundary to stamp on the fresh
	// execution (see job.Job.TenantID). Empty = no tenant.
	TenantID string
}

// EnqueueOnceIn admits in.Payload under in.LogicalKey exactly once (idempotent
// while an active execution holds the key), returning the unique execution ID. It
// is EnqueueOnce with the pocket's tenant slot: the work protocol's vocabulary is
// unchanged, so a consuming pocket that depends on work.Enqueuer keeps using the
// positional form. Payload is opaque bytes the queue never interprets; the Service
// deep-copies it so a later caller mutation cannot alter the admitted work.
func (s *Service) EnqueueOnceIn(ctx context.Context, in EnqueueOnceInput) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.EnqueueOnce(ctx, job.Enqueue{
		Kind:       in.Kind,
		TenantID:   in.TenantID,
		LogicalKey: in.LogicalKey,
		Payload:    json.RawMessage(bytes.Clone(in.Payload)),
	})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// ReplaceIn supersedes every active execution holding in.LogicalKey and inserts
// one fresh execution, returning its ID — the user-requested resend with the
// pocket's tenant slot. See EnqueueOnceIn for the payload-copy note.
func (s *Service) ReplaceIn(ctx context.Context, in ReplaceInput) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.Replace(ctx, job.Enqueue{
		Kind:       in.Kind,
		TenantID:   in.TenantID,
		LogicalKey: in.LogicalKey,
		Payload:    json.RawMessage(bytes.Clone(in.Payload)),
	})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// EnqueueOnce admits payload under logicalKey exactly once (idempotent while an
// active execution holds the key), returning the unique execution ID. It is the
// producer half of the work.Enqueuer protocol, backed by the fenced queue; its
// signature is FROZEN by that protocol, so it delegates to EnqueueOnceIn with no
// tenant. A caller that needs the tenant slot calls EnqueueOnceIn.
func (s *Service) EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	return s.EnqueueOnceIn(ctx, EnqueueOnceInput{Kind: kind, LogicalKey: logicalKey, Payload: payload})
}

// Replace supersedes every active execution holding logicalKey and inserts one
// fresh execution, returning its ID — the user-requested resend. It is the
// work.Replacer protocol; its signature is FROZEN by that protocol, so it
// delegates to ReplaceIn with no tenant.
func (s *Service) Replace(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	return s.ReplaceIn(ctx, ReplaceInput{Kind: kind, LogicalKey: logicalKey, Payload: payload})
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
// it crosses the stdlib-typed FencedHandlerFunc seam. A consuming pocket need not
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
// stdlib-typed so a consuming pocket's handler matches it structurally with no
// import of the jobs domain. Checkpoint persists a fresh payload under the current
// claim's fence (execution + lease); a stale/superseded claim's checkpoint fails
// and the handler MUST NOT perform its side effect.
type FencedClaim struct {
	ExecutionID string
	LeaseID     string
	Payload     json.RawMessage
	// TenantID is the claimed execution's OPTIONAL host-defined boundary (see
	// job.Job.TenantID), so a handler sees the scope of what it claimed. Empty =
	// the execution carries no tenant.
	TenantID string
	// Attempt is the number of process attempts already spent for retry
	// classification (the claim increments it).
	Attempt    int
	Checkpoint func(ctx context.Context, payload json.RawMessage) error
}

// FencedHandlerFunc processes one claimed fenced job of a registered kind.
// Returning nil COMPLETES the job (a delivered message or a non-failed skip); a
// non-nil error triggers the runtime's retry/dead-letter policy. It is
// stdlib-typed so a consuming pocket's delivery handler plugs in without importing
// the jobs domain.
type FencedHandlerFunc func(ctx context.Context, claim FencedClaim) error

// DeadLetterFunc is the FROZEN (AV3D-0.3) per-kind terminal hook a host registers
// for permanent-failure cleanup — e.g. discarding an undeliverable challenge. It
// runs ONLY AFTER the dead-letter transition is durably recorded, and its failure
// is logged/reported but never resurrects the job. j.FailureReason carries the
// terminal reason exactly as the transition recorded it: the kernel's
// workers.FencedDeadLetterFunc[job.Job] hands the reason alongside the as-claimed
// job value (it cannot mutate an arbitrary T), and the runtime's dispatch closure
// stamps it onto j before the per-kind hook runs — that closure is the adapter
// between the two shapes, which intentionally differ since the reason threading.
// It carries the domain job.Job because it is a host-registered hook, not a
// cross-pocket structural seam.
type DeadLetterFunc func(ctx context.Context, j job.Job) error

// FencedRuntimeConfig configures the FencedRuntime. Handlers is required non-empty;
// every zero sizing field selects its package default.
//
// Its env keys are the JOBS_* keys Config uses, deliberately: a host running both
// runtimes in one process disambiguates with ParseEnvTags' namespace argument
// (ParseEnvTags("FENCED", &cfg) reads FENCED_JOBS_WORKERS).
type FencedRuntimeConfig struct {
	// Handlers maps a job kind to its fenced handler; required non-empty.
	Handlers map[string]FencedHandlerFunc
	// DeadLetters maps a job kind to its per-kind terminal hook, fired only AFTER the
	// dead-letter transition is durably recorded (never before, never on a retry, and
	// never on a lease-fenced failure). Optional; a kind with no entry runs no hook.
	DeadLetters map[string]DeadLetterFunc
	// Workers is the fenced pool size; 0 → defaultFencedWorkers. (env: JOBS_WORKERS)
	Workers int `env:"JOBS_WORKERS"`
	// PollInterval / IdleInterval tune the pool cadence; 0 → the pool defaults.
	// (env: JOBS_POLL_INTERVAL, JOBS_IDLE_INTERVAL)
	PollInterval time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval time.Duration `env:"JOBS_IDLE_INTERVAL"`
	// LeaseFor is the claim lease each Claim requests; 0 → defaultFencedLeaseFor. Set
	// it above a job's expected processing time and below the store's reclaim cadence.
	// (env: JOBS_LEASE_FOR)
	LeaseFor time.Duration `env:"JOBS_LEASE_FOR"`
	// ProcessTimeout bounds each handler attempt with a child-context deadline that
	// sits inside the lease; 0 → no per-attempt timeout. (env: JOBS_PROCESS_TIMEOUT)
	ProcessTimeout time.Duration `env:"JOBS_PROCESS_TIMEOUT"`
	// MaxAttempts caps process attempts before a job dead-letters; 0 →
	// defaultFencedMaxAttempts. (env: JOBS_MAX_ATTEMPTS)
	MaxAttempts int `env:"JOBS_MAX_ATTEMPTS"`
	// Backoff maps a just-spent attempt (1-based) to the retry-at delay; nil selects a
	// capped exponential.
	Backoff func(attempt int) time.Duration
	// Clock overrides the time source (primarily for deterministic tests); nil →
	// time.Now().UTC().
	Clock func() time.Time
	// Logger is the pool's operational logger; nil → slog.Default().
	Logger *slog.Logger
}

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
	kinds := handlerKinds(handlers)
	logger.Info("jobs runtime: claiming kinds", "pool", "fenced-delivery", "kinds", kinds)
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
			TenantID:    j.TenantID,
			Attempt:     j.Retries,
			Checkpoint: func(ctx context.Context, payload json.RawMessage) error {
				return store.Checkpoint(ctx, j.JobID, j.LeaseID, payload, clock())
			},
		}
		return j, handler(ctx, claim)
	}

	runner := workers.NewFencedRunner(
		kindScopedFenced{repo: store, kinds: kinds},
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
		runner.SetDeadLetterHook(func(ctx context.Context, j job.Job, reason string) error {
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

// kindScopedFenced adapts the FencedQueueRepository (whose Claim takes a kinds
// filter) to the kernel's workers.FencedStore by closing over the runtime's
// registered kinds, so the fenced pool never leases — or spends a retry on — a
// job it has no handler for. Complete/Fail/Reschedule delegate unchanged.
type kindScopedFenced struct {
	repo  job.FencedQueueRepository
	kinds []string
}

var _ workers.FencedStore[job.Job] = kindScopedFenced{}

func (q kindScopedFenced) Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (job.Job, error) {
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
