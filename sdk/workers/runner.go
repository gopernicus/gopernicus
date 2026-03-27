package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// Job is the constraint for work items in a job queue.
// Domain models implement this interface.
type Job interface {
	GetID() string
	GetStatus() string
	GetRetryCount() int
}

// JobStore defines the database operations for a job queue.
// Domain stores implement this interface. The typical implementation uses
// FOR UPDATE SKIP LOCKED for atomic checkout.
type JobStore[T Job] interface {
	// Checkout atomically claims the next available job.
	// Returns ErrNoWork if no jobs are available.
	Checkout(ctx context.Context, workerID string, now time.Time) (T, error)

	// Complete marks a job as successfully completed.
	Complete(ctx context.Context, jobID string, now time.Time) error

	// Fail marks a job as failed. Implementation should handle retry logic
	// (increment retry_count, check against max attempts, dead-letter if exhausted).
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
}

// ProcessFunc is the business logic for processing a job.
// It receives the job and returns the (possibly modified) job or an error.
type ProcessFunc[T Job] func(ctx context.Context, job T) (T, error)

// PreProcessHook runs after Checkout but before Process.
type PreProcessHook[T Job] func(ctx context.Context, job T) error

// PostProcessHook runs after Process but before Complete/Fail.
// The error parameter is the result of processing (nil on success).
type PostProcessHook[T Job] func(ctx context.Context, job T, err error) error

// Runner orchestrates the job lifecycle:
//
//	Checkout -> PreHooks -> Process (with retry) -> PostHooks -> Complete/Fail
//
// It composes a JobStore + ProcessFunc into a WorkFunc that the
// non-generic WorkerPool can execute.
type Runner[T Job] struct {
	store       JobStore[T]
	processFunc ProcessFunc[T]
	maxAttempts int
	clock       func() time.Time
	log         *slog.Logger
	tracer      Tracer
	preHooks    []PreProcessHook[T]
	postHooks   []PostProcessHook[T]
}

// runnerConfig holds options applied via functional options.
// Go generics and functional options don't mix cleanly, so we use
// a non-generic config struct and add hooks via methods on Runner.
type runnerConfig struct {
	maxAttempts int
	clock      func() time.Time
	tracer     Tracer
}

// RunnerOption configures a Runner.
type RunnerOption func(*runnerConfig)

// WithMaxAttempts sets the total number of processing attempts (including the first).
// WithMaxAttempts(3) means try up to 3 times before failing. Default is 3.
func WithMaxAttempts(n int) RunnerOption {
	return func(c *runnerConfig) { c.maxAttempts = n }
}

// WithClock sets a custom clock function for testing.
func WithClock(fn func() time.Time) RunnerOption {
	return func(c *runnerConfig) { c.clock = fn }
}

// WithTracer sets the tracer for job lifecycle spans.
// When nil (default), no spans are created (zero overhead).
func WithTracer(tracer Tracer) RunnerOption {
	return func(c *runnerConfig) { c.tracer = tracer }
}

// NewRunner creates a Runner that handles job lifecycle.
// Use runner.WorkFunc() to get a WorkFunc for the pool.
func NewRunner[T Job](
	store JobStore[T],
	processFunc ProcessFunc[T],
	log *slog.Logger,
	opts ...RunnerOption,
) *Runner[T] {
	cfg := &runnerConfig{
		maxAttempts: 3,
		clock:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if log == nil {
		log = slog.Default()
	}

	return &Runner[T]{
		store:       store,
		processFunc: processFunc,
		maxAttempts:  cfg.maxAttempts,
		clock:       cfg.clock,
		log:         log,
		tracer:      cfg.tracer,
	}
}

// AddPreProcessHooks adds functions that run after Checkout but before Process.
func (r *Runner[T]) AddPreProcessHooks(hooks ...PreProcessHook[T]) {
	r.preHooks = append(r.preHooks, hooks...)
}

// AddPostProcessHooks adds functions that run after Process but before Complete/Fail.
func (r *Runner[T]) AddPostProcessHooks(hooks ...PostProcessHook[T]) {
	r.postHooks = append(r.postHooks, hooks...)
}

// WorkFunc returns a WorkFunc that executes the full job lifecycle.
// Pass this to NewPool to connect the runner to a worker pool.
func (r *Runner[T]) WorkFunc() WorkFunc {
	return r.work
}

// work implements the job lifecycle as a WorkFunc.
func (r *Runner[T]) work(ctx context.Context) error {
	workerID := GetWorkerID(ctx)

	// 1. Checkout
	job, err := r.checkout(ctx, workerID)
	if err != nil {
		return err
	}

	// Track processing state for the deferred Complete/Fail
	var processErr error
	var processedJob T
	startTime := time.Now()

	defer func() {
		duration := time.Since(startTime)

		// Recover from panics in process
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			r.log.ErrorContext(ctx, "panic recovered in job",
				"worker_id", workerID,
				"job_id", job.GetID(),
				"panic", rec,
				"stack", string(stack))
			processErr = fmt.Errorf("panic: %v", rec)
		}

		// Run post-process hooks.
		// On error use original job; on success use processed job.
		hookJob := processedJob
		if processErr != nil {
			hookJob = job
		}
		for _, hook := range r.postHooks {
			if hookErr := hook(ctx, hookJob, processErr); hookErr != nil {
				r.log.ErrorContext(ctx, "post-process hook failed",
					"job_id", job.GetID(),
					"error", hookErr)
			}
		}

		// Complete or Fail
		if processErr != nil {
			r.fail(ctx, job, processErr)
			r.log.ErrorContext(ctx, "job failed",
				"worker_id", workerID,
				"job_id", job.GetID(),
				"error", processErr,
				"duration", duration)
		} else {
			r.complete(ctx, job)
			r.log.InfoContext(ctx, "job completed",
				"worker_id", workerID,
				"job_id", job.GetID(),
				"duration", duration)
		}
	}()

	// 2. Run pre-process hooks
	for _, hook := range r.preHooks {
		if hookErr := hook(ctx, job); hookErr != nil {
			r.log.ErrorContext(ctx, "pre-process hook failed",
				"job_id", job.GetID(),
				"error", hookErr)
		}
	}

	r.log.InfoContext(ctx, "processing job",
		"worker_id", workerID,
		"job_id", job.GetID())

	// 3. Process with retry
	processedJob, processErr = r.processWithRetry(ctx, job)

	if processErr != nil {
		return fmt.Errorf("job processing error: %w", processErr)
	}

	return nil
}

// maybeSpan starts a tracing span if a Tracer is configured, otherwise returns
// a no-op finisher so call sites need no nil checks.
func (r *Runner[T]) maybeSpan(ctx context.Context, name string) (context.Context, SpanFinisher) {
	if r.tracer == nil {
		return ctx, noopSpanFinisher{}
	}
	return r.tracer.StartSpan(ctx, name)
}

// checkout claims the next available job from the store.
func (r *Runner[T]) checkout(ctx context.Context, workerID string) (T, error) {
	var zero T

	ctx, span := r.maybeSpan(ctx, "job.checkout")
	defer span.Finish()

	job, err := r.store.Checkout(ctx, workerID, r.clock())
	if err != nil {
		if !errors.Is(err, ErrNoWork) {
			span.RecordError(err)
		}
		return zero, err
	}
	span.SetAttributes(StringAttribute("job.id", job.GetID()))
	return job, nil
}

// complete marks a job as successfully completed in the store.
func (r *Runner[T]) complete(ctx context.Context, job T) {
	ctx, span := r.maybeSpan(ctx, "job.complete")
	defer span.Finish()
	span.SetAttributes(StringAttribute("job.id", job.GetID()))

	if err := r.store.Complete(ctx, job.GetID(), r.clock()); err != nil {
		span.RecordError(err)
		r.log.ErrorContext(ctx, "failed to mark job as complete",
			"job_id", job.GetID(),
			"error", err)
	}
}

// fail marks a job as failed in the store.
func (r *Runner[T]) fail(ctx context.Context, job T, processErr error) {
	reason := "unknown error"
	if processErr != nil {
		reason = processErr.Error()
	}

	ctx, span := r.maybeSpan(ctx, "job.fail")
	defer span.Finish()
	span.SetAttributes(
		StringAttribute("job.id", job.GetID()),
		StringAttribute("job.status", job.GetStatus()),
	)

	if err := r.store.Fail(ctx, job.GetID(), r.clock(), reason, r.maxAttempts); err != nil {
		span.RecordError(err)
		r.log.ErrorContext(ctx, "failed to mark job as failed",
			"job_id", job.GetID(),
			"error", err)
	}
}

// processWithRetry handles retry logic with exponential backoff.
func (r *Runner[T]) processWithRetry(ctx context.Context, job T) (T, error) {
	maxAttempts := r.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	initialDelay := 1 * time.Second

	var lastErr error
	var processedJob T

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			r.log.InfoContext(ctx, "retrying job",
				"job_id", job.GetID(),
				"attempt", attempt,
				"max_attempts", maxAttempts)

			delay := initialDelay * time.Duration(1<<(attempt-2))
			select {
			case <-ctx.Done():
				return processedJob, ctx.Err()
			case <-time.After(delay):
			}
		}

		spanCtx, span := r.maybeSpan(ctx, "job.process")
		span.SetAttributes(
			StringAttribute("job.id", job.GetID()),
			StringAttribute("job.status", job.GetStatus()),
		)
		processedJob, lastErr = r.processFunc(spanCtx, job)
		if lastErr != nil {
			span.RecordError(lastErr)
		}
		span.Finish()

		if lastErr == nil {
			return processedJob, nil
		}

		if ctx.Err() != nil {
			return processedJob, ctx.Err()
		}

		r.log.ErrorContext(ctx, "job processing attempt failed",
			"job_id", job.GetID(),
			"attempt", attempt,
			"error", lastErr)
	}

	if maxAttempts > 1 {
		return processedJob, fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
	}

	return processedJob, lastErr
}
