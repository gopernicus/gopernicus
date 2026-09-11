package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Fenced-runtime tuning defaults. Each zero field in FencedRuntimePolicy selects
// its default so a consumer wires only the knobs it cares about.
const (
	// defaultFencedWorkers is the fenced pool size when FencedRuntimePolicy.Workers is 0.
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

// FencedClaim is one claimed fenced job handed to a host-registered handler.
// Hosts adapt its fields to any consuming pocket's own handler contract;
// consuming pockets do not import this named type. Checkpoint persists a fresh
// payload under the current claim's fence (execution + lease); a stale/superseded claim's checkpoint fails
// and the handler MUST NOT perform its side effect.
type FencedClaim struct {
	ExecutionID string
	LeaseID     string
	Payload     json.RawMessage
	// TenantID is the claimed execution's OPTIONAL host-defined boundary (see
	// Job.TenantID), so a handler sees the scope of what it claimed. Empty =
	// the execution carries no tenant.
	TenantID string
	// Attempt is the number of process attempts already spent for retry
	// classification (the claim increments it).
	Attempt    int
	Checkpoint func(ctx context.Context, payload json.RawMessage) error
}

// FencedHandlerFunc processes one claimed fenced job of a registered kind.
// Returning nil COMPLETES the job (a delivered message or a non-failed skip); a
// deferred/rejected disposition selects that transition; other errors use the
// runtime's retry/dead-letter policy. Hosts adapt consuming pocket handlers here.
type FencedHandlerFunc func(ctx context.Context, claim FencedClaim) error

// DeadLetterFunc is the FROZEN (AV3D-0.3) per-kind terminal hook a host registers
// for permanent-failure cleanup — e.g. discarding an undeliverable challenge. It
// runs ONLY AFTER the dead-letter transition is durably recorded, and its failure
// is logged/reported but never resurrects the job. j.FailureReason carries the
// terminal reason exactly as the transition recorded it: the kernel's
// workers.FencedDeadLetterFunc[Job] hands the reason alongside the as-claimed
// job value (it cannot mutate an arbitrary T), and the runtime's dispatch closure
// stamps it onto j before the per-kind hook runs — that closure is the adapter
// between the two shapes, which intentionally differ since the reason threading.
// It carries the domain Job because it is a host-registered hook, not a
// cross-pocket structural seam.
type DeadLetterFunc func(ctx context.Context, j Job) error

// FencedRuntimePolicy controls pool sizing, leases and retry timing. Nonpositive
// sizing fields select defaults. A positive ProcessTimeout must be shorter than
// the resolved LeaseFor. Backoff nil selects a capped exponential.
// Env keys intentionally match RuntimePolicy; ParseEnvTags can add a namespace.
type FencedRuntimePolicy struct {
	Workers        int           `env:"JOBS_WORKERS"`
	PollInterval   time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval   time.Duration `env:"JOBS_IDLE_INTERVAL"`
	LeaseFor       time.Duration `env:"JOBS_LEASE_FOR"`
	ProcessTimeout time.Duration `env:"JOBS_PROCESS_TIMEOUT"`
	MaxAttempts    int           `env:"JOBS_MAX_ATTEMPTS"`
	Backoff        func(attempt int) time.Duration
}

// FencedRuntimeOption configures a fenced runtime before workers are built.
type FencedRuntimeOption func(*fencedRuntimeConfig)

type fencedRuntimeConfig struct {
	FencedRuntimePolicy
	WorkerMiddleware []workers.Middleware
	JobMiddleware    []workers.JobMiddleware[FencedClaim]
	DeadLetters      map[string]DeadLetterFunc
	Clock            func() time.Time
	Logger           *slog.Logger
}

// WithFencedRuntimePolicy replaces all sizing, lease, timeout and retry settings.
func WithFencedRuntimePolicy(policy FencedRuntimePolicy) FencedRuntimeOption {
	return func(c *fencedRuntimeConfig) { c.FencedRuntimePolicy = policy }
}

// WithFencedRuntimeLogger replaces the pool logger. Nil selects slog.Default.
func WithFencedRuntimeLogger(logger *slog.Logger) FencedRuntimeOption {
	return func(c *fencedRuntimeConfig) { c.Logger = logger }
}

// WithFencedRuntimeClock replaces the time source. Nil selects time.Now in UTC.
func WithFencedRuntimeClock(clock func() time.Time) FencedRuntimeOption {
	return func(c *fencedRuntimeConfig) { c.Clock = clock }
}

// WithDeadLetters replaces the per-kind hooks run after a persisted terminal failure.
// Nil hooks are ignored. The map is copied; captured callback state stays host-owned.
func WithDeadLetters(hooks map[string]DeadLetterFunc) FencedRuntimeOption {
	snapshot := maps.Clone(hooks)
	return func(c *fencedRuntimeConfig) { c.DeadLetters = maps.Clone(snapshot) }
}

// WithFencedWorkerMiddleware replaces polling middleware; first is outermost.
// The supplied slice is copied.
func WithFencedWorkerMiddleware(middleware ...workers.Middleware) FencedRuntimeOption {
	snapshot := append([]workers.Middleware(nil), middleware...)
	return func(c *fencedRuntimeConfig) { c.WorkerMiddleware = append([]workers.Middleware(nil), snapshot...) }
}

// WithFencedJobMiddleware replaces claimed-job middleware; first is outermost.
// A temporary gate returns workers.DeferUntil. The supplied slice is copied.
func WithFencedJobMiddleware(middleware ...workers.JobMiddleware[FencedClaim]) FencedRuntimeOption {
	snapshot := append([]workers.JobMiddleware[FencedClaim](nil), middleware...)
	return func(c *fencedRuntimeConfig) {
		c.JobMiddleware = append([]workers.JobMiddleware[FencedClaim](nil), snapshot...)
	}
}
