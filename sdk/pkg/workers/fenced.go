package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const defaultLeaseFor = 30 * time.Second

// FencedJob adds the attempt count spent by Claim to an execution's identity.
type FencedJob interface {
	Job
	RetryCount() int
}

// FencedStore guards transitions with a fresh per-claim lease. Claim returns
// ErrNoWork when none is due and increments RetryCount. Transitions from expired,
// reclaimed or superseded leases return sdk.ErrConflict. Jobs adapts its richer
// repository's kind-filtered Claim to this generic port.
type FencedStore[T FencedJob] interface {
	Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (T, error)
	Complete(ctx context.Context, id, leaseID string, now time.Time) error
	Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error
	Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error
}

// FencedDeferrer optionally defers gated work without spending an attempt.
// In one atomic operation it verifies the live lease, schedules pending work,
// releases the lease and refunds only this claim's increment. Prior spent
// attempts are preserved. Repeated/stale deferrals conflict without mutation.
type FencedDeferrer interface {
	Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error
}

// FencedRetryDecider returns a durable retry delay or false to dead-letter.
// attempt includes the current claim. Permanent and deferred dispositions bypass
// this policy. Without a decider, an ordinary processing error dead-letters.
type FencedRetryDecider func(err error, attempt int) (delay time.Duration, retry bool)

// FencedDeadLetterFunc runs only after permanent Fail is persisted. Its job is
// the claimed value; reason is the recorded cause. A hook error is logged and
// never resurrects work. Deferral/retry/conflict do not invoke this hook.
type FencedDeadLetterFunc[T FencedJob] func(ctx context.Context, job T, reason string) error

// FencedRunner drives a lease-protected lifecycle. Cancellation leaves the claim
// for recovery; it never records completion/failure after observing shutdown.
// Unexpected persistence errors reach its caller; lease conflicts are logged as
// lost ownership. Processing and middleware must cooperate with cancellation.
type FencedRunner[T FencedJob] struct {
	store          FencedStore[T]
	process        ProcessFunc[T]
	clock          func() time.Time
	leaseFor       time.Duration
	processTimeout time.Duration
	retryDecider   FencedRetryDecider
	newLease       func() string
	deadLetter     FencedDeadLetterFunc[T]
	log            *slog.Logger
}

type fencedRunnerConfig struct {
	clock          func() time.Time
	leaseFor       time.Duration
	processTimeout time.Duration
	retryDecider   FencedRetryDecider
	newLease       func() string
	logger         *slog.Logger
}

// FencedRunnerOption configures a fenced runner.
type FencedRunnerOption func(*fencedRunnerConfig)

// WithFencedLogger sets the fenced runner's logger. Nil selects slog.Default.
// Repeated options replace the logger.
func WithFencedLogger(logger *slog.Logger) FencedRunnerOption {
	return func(c *fencedRunnerConfig) { c.logger = logger }
}

// WithFencedClock supplies the clock passed to store operations. Nil is ignored.
func WithFencedClock(fn func() time.Time) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if fn != nil {
			c.clock = fn
		}
	}
}

// WithLeaseDuration sets the requested claim lease. Nonpositive values keep 30s.
func WithLeaseDuration(d time.Duration) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if d > 0 {
			c.leaseFor = d
		}
	}
}

// WithLeaseTokenFunc replaces the random per-claim token generator, for tests.
// Production generators must return a fresh token for every claim.
func WithLeaseTokenFunc(fn func() string) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if fn != nil {
			c.newLease = fn
		}
	}
}

// WithFencedRetryDecider sets the error-aware durable retry policy.
func WithFencedRetryDecider(fn FencedRetryDecider) FencedRunnerOption {
	return func(c *fencedRunnerConfig) { c.retryDecider = fn }
}

// WithFencedProcessTimeout applies a cooperative timeout around the whole
// processor middleware chain. Nonpositive values are ignored (default: disabled). It must be shorter
// than the lease; allow additional margin for Claim latency and persistence.
func WithFencedProcessTimeout(d time.Duration) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if d > 0 {
			c.processTimeout = d
		}
	}
}

// NewFencedRunner starts no goroutines. Logging defaults to slog.Default. Invalid
// configuration (process timeout at/beyond the lease) or a nil option panics at construction.
func NewFencedRunner[T FencedJob](store FencedStore[T], process ProcessFunc[T], opts ...FencedRunnerOption) *FencedRunner[T] {
	cfg := fencedRunnerConfig{clock: func() time.Time { return time.Now().UTC() }, leaseFor: defaultLeaseFor, newLease: newLeaseToken}
	for _, opt := range opts {
		if opt == nil {
			panic("workers.NewFencedRunner: nil option")
		}
		opt(&cfg)
	}
	if cfg.processTimeout >= cfg.leaseFor {
		panic("workers: process timeout must be shorter than the claim lease")
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	return &FencedRunner[T]{store: store, process: process, clock: cfg.clock, leaseFor: cfg.leaseFor, processTimeout: cfg.processTimeout, retryDecider: cfg.retryDecider, newLease: cfg.newLease, log: cfg.logger}
}

// SetDeadLetterHook registers a post-persistence hook. Configure before running;
// concurrent mutation is unsupported. Nil clears it.
func (r *FencedRunner[T]) SetDeadLetterHook(hook FencedDeadLetterFunc[T]) { r.deadLetter = hook }

// WorkFunc returns an iteration to pass to a Pool.
func (r *FencedRunner[T]) WorkFunc() WorkFunc { return r.work }

func (r *FencedRunner[T]) work(ctx context.Context) error {
	leaseID := r.newLease()
	job, err := r.store.Claim(ctx, r.clock(), leaseID, r.leaseFor)
	if err != nil {
		return err
	}
	id := job.ID()
	r.log.InfoContext(ctx, "processing fenced job", "job_id", id, "lease_id", leaseID)
	processCtx := ctx
	var cancel context.CancelFunc
	if r.processTimeout > 0 {
		processCtx, cancel = context.WithTimeout(ctx, r.processTimeout)
	}
	procErr := runProcess(processCtx, job, id, r.process, r.log)
	if cancel != nil {
		cancel()
	}
	if ctx.Err() != nil {
		r.log.InfoContext(ctx, "fenced job left reclaimable on shutdown", "job_id", id, "lease_id", leaseID)
		return nil
	}
	now := r.clock()
	if procErr == nil {
		return r.transition(ctx, id, leaseID, "completed", r.store.Complete(ctx, id, leaseID, now))
	}
	var permanent *PermanentError
	var deferred *DeferredError
	switch {
	case errors.As(procErr, &permanent):
		// Permanent rejection takes precedence over deferral and retry policy.
	case errors.As(procErr, &deferred):
		if !deferred.Until.After(now) {
			return fmt.Errorf("workers: defer job %s: %w", id, ErrInvalidDeferral)
		}
		store, ok := r.store.(FencedDeferrer)
		if !ok {
			return fmt.Errorf("workers: defer job %s: %w", id, ErrDeferralUnsupported)
		}
		return r.transition(ctx, id, leaseID, "deferred", store.Defer(ctx, id, leaseID, deferred.Until, deferred.Reason, now))
	default:
		if r.retryDecider != nil {
			if delay, retry := r.retryDecider(procErr, job.RetryCount()); retry {
				return r.transition(ctx, id, leaseID, "rescheduled", r.store.Reschedule(ctx, id, leaseID, now.Add(delay), procErr.Error(), now))
			}
		}
	}
	err = r.store.Fail(ctx, id, leaseID, procErr.Error(), now)
	if err != nil {
		return r.transition(ctx, id, leaseID, "failed", err)
	}
	r.log.ErrorContext(ctx, "fenced job failed", "job_id", id, "lease_id", leaseID, "error", procErr)
	if r.deadLetter != nil {
		if err := r.deadLetter(ctx, job, procErr.Error()); err != nil {
			r.log.ErrorContext(ctx, "fenced dead-letter hook failed", "job_id", id, "lease_id", leaseID, "error", err)
		}
	}
	return nil
}

func (r *FencedRunner[T]) transition(ctx context.Context, id, leaseID, action string, err error) error {
	if errors.Is(err, sdk.ErrConflict) {
		r.log.WarnContext(ctx, "fenced transition lost lease", "job_id", id, "lease_id", leaseID, "transition", action)
		return nil
	}
	if err != nil {
		return fmt.Errorf("workers: fenced job %s transition %s: %w", id, action, err)
	}
	r.log.InfoContext(ctx, "fenced job "+action, "job_id", id, "lease_id", leaseID)
	return nil
}

func newLeaseToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "lease_" + hex.EncodeToString(b[:])
}
