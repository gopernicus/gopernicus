package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const defaultMaxAttempts = 3

// Job identifies a claimed execution. Custom queues need not expose status or
// retry metadata to use Runner.
type Job interface{ ID() string }

// RetryLimitedJob optionally supplies a persisted per-job failure ceiling.
// Positive values override Runner's default; zero or negative values keep it.
// Permanent rejection still dead-letters on the current failure.
type RetryLimitedJob interface {
	Job
	RetryLimit() int
}

// JobStore owns atomic claim and persistence. Claim returns ErrNoWork when none
// is due. Fail spends one failure attempt, requeues below maxAttempts and
// dead-letters at the ceiling. Recovery/stale-worker protection is store-owned;
// use FencedRunner for transitions guarded by a per-claim lease.
type JobStore[T Job] interface {
	Claim(ctx context.Context, workerID string, now time.Time) (T, error)
	Complete(ctx context.Context, jobID string, now time.Time) error
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
}

// JobDeferrer optionally releases a running execution to availableAt without
// spending a failure attempt. It preserves prior attempts and rejects jobs no
// longer running. Like JobStore, this port provides no per-claim fencing.
type JobDeferrer interface {
	Defer(ctx context.Context, jobID string, availableAt time.Time, reason string, now time.Time) error
}

// Runner drives Claim -> process -> Complete/Fail, or Defer for a temporary gate.
// Processing, including job middleware, runs once per claim. Durable retries
// belong to the store. Unexpected persistence failures reach the Pool.
type Runner[T Job] struct {
	store       JobStore[T]
	process     ProcessFunc[T]
	maxAttempts int
	clock       func() time.Time
	log         *slog.Logger
}
type runnerConfig struct {
	maxAttempts int
	clock       func() time.Time
	logger      *slog.Logger
}

// RunnerOption configures a Runner.
type RunnerOption func(*runnerConfig)

// WithRunnerLogger sets the runner's logger. Nil selects slog.Default.
// Repeated options replace the logger.
func WithRunnerLogger(logger *slog.Logger) RunnerOption {
	return func(c *runnerConfig) { c.logger = logger }
}

// WithMaxAttempts sets the failure ceiling. Default 3; values below 1 become 1.
func WithMaxAttempts(n int) RunnerOption {
	return func(c *runnerConfig) { c.maxAttempts = n }
}

// WithClock supplies the runner clock. Nil keeps the default UTC clock.
func WithClock(fn func() time.Time) RunnerOption {
	return func(c *runnerConfig) {
		if fn != nil {
			c.clock = fn
		}
	}
}

// NewRunner starts no goroutines. Wrap process with ChainJobMiddleware to
// compose gates or other behavior. Logging defaults to slog.Default.
// A nil option panics.
func NewRunner[T Job](store JobStore[T], process ProcessFunc[T], opts ...RunnerOption) *Runner[T] {
	cfg := runnerConfig{maxAttempts: defaultMaxAttempts, clock: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		if opt == nil {
			panic("workers.NewRunner: nil option")
		}
		opt(&cfg)
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	return &Runner[T]{store: store, process: process, maxAttempts: cfg.maxAttempts, clock: cfg.clock, log: cfg.logger}
}

// WorkFunc returns the iteration to pass to a Pool.
func (r *Runner[T]) WorkFunc() WorkFunc { return r.work }

func (r *Runner[T]) work(ctx context.Context) error {
	job, err := r.store.Claim(ctx, WorkerIDFromContext(ctx), r.clock())
	if err != nil {
		return err
	}
	id := job.ID()
	r.log.InfoContext(ctx, "processing job", "job_id", id)
	procErr := runProcess(ctx, job, id, r.process, r.log)
	now := r.clock()
	if procErr == nil {
		if err := r.store.Complete(ctx, id, now); err != nil {
			return fmt.Errorf("workers: complete job %s: %w", id, err)
		}
		r.log.InfoContext(ctx, "job completed", "job_id", id)
		return nil
	}

	var permanent *PermanentError
	var deferred *DeferredError
	maxAttempts := r.maxAttempts
	if limited, ok := any(job).(RetryLimitedJob); ok {
		if limit := limited.RetryLimit(); limit > 0 {
			maxAttempts = limit
		}
	}
	switch {
	case errors.As(procErr, &permanent):
		maxAttempts = 1
	case errors.As(procErr, &deferred):
		if !deferred.Until.After(now) {
			return fmt.Errorf("workers: defer job %s: %w", id, ErrInvalidDeferral)
		}
		store, ok := r.store.(JobDeferrer)
		if !ok {
			return fmt.Errorf("workers: defer job %s: %w", id, ErrDeferralUnsupported)
		}
		if err := store.Defer(ctx, id, deferred.Until, deferred.Reason, now); err != nil {
			return fmt.Errorf("workers: defer job %s: %w", id, err)
		}
		r.log.InfoContext(ctx, "job deferred", "job_id", id, "available_at", deferred.Until)
		return nil
	}
	if err := r.store.Fail(ctx, id, now, procErr.Error(), maxAttempts); err != nil {
		return fmt.Errorf("workers: fail job %s: %w", id, err)
	}
	r.log.ErrorContext(ctx, "job failed", "job_id", id, "error", procErr)
	return nil
}
