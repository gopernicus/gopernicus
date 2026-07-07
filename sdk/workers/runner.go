package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/gopernicus/gopernicus/sdk/tracing"
)

const defaultMaxAttempts = 3

// Job is the constraint for items a Runner processes. Durable job entities
// satisfy it.
type Job interface {
	ID() string
	Status() string
	RetryCount() int
}

// JobStore is the durable queue a Runner drives. Adapters (memory, turso,
// postgres) implement it; a SQL adapter typically claims with FOR UPDATE SKIP
// LOCKED.
type JobStore[T Job] interface {
	// Claim atomically leases the next available job to workerID, returning
	// ErrNoWork when the queue is empty. now is the runner's clock reading, so
	// the store can compute lease deadlines and recover stale claims against a
	// single time source.
	Claim(ctx context.Context, workerID string, now time.Time) (T, error)

	// Complete marks the job done.
	Complete(ctx context.Context, jobID string, now time.Time) error

	// Fail records a processing failure. The store owns retry policy: increment
	// the attempt count, requeue while it is below maxAttempts, and dead-letter
	// once it reaches maxAttempts. reason is the human-readable cause.
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
}

// ProcessFunc is the business logic for one job. It receives the job the store
// returned and returns the (possibly updated) job or an error.
type ProcessFunc[T Job] func(ctx context.Context, job T) (T, error)

// PreProcessHook runs after Claim and before ProcessFunc.
type PreProcessHook[T Job] func(ctx context.Context, job T) error

// PostProcessHook runs after ProcessFunc and before Complete/Fail. err is the
// processing result: nil on success, otherwise the failure (including a
// recovered panic).
type PostProcessHook[T Job] func(ctx context.Context, job T, err error) error

// Runner composes a JobStore and a ProcessFunc into a WorkFunc the pool can
// execute. Each iteration is:
//
//	Claim -> pre-hooks -> process -> post-hooks -> Complete (ok) / Fail (err)
//
// An empty Claim propagates ErrNoWork so the pool backs off. Durable retry is
// the store's job: a failed process calls Fail with maxAttempts, and the job is
// re-claimed on a later iteration until the store dead-letters it. Panics in
// ProcessFunc are recovered and treated as a processing error so the job is
// still failed rather than lost.
//
// Two independent retry axes multiply. WithMaxAttempts is the durable axis: the
// store re-claims a failed job across iterations until it dead-letters. Opting
// into WithRetryWithinClaim adds a second, in-memory axis: the runner retries a
// failing process a few times inside the single claim before it ever calls
// Fail. Total ProcessFunc invocations for one job are bounded by
// durable-attempts × within-claim-attempts.
type Runner[T Job] struct {
	store            JobStore[T]
	processFunc      ProcessFunc[T]
	maxAttempts      int
	clock            func() time.Time
	log              *slog.Logger
	tracer           tracing.Tracer
	retryWithinClaim bool
	retryAttempts    int
	retryInitial     time.Duration
	retryMaxElapsed  time.Duration
	preHooks         []PreProcessHook[T]
	postHooks        []PostProcessHook[T]
}

// runnerConfig carries functional-option values. Generics and functional
// options mix poorly, so options set non-generic fields here and hooks are
// added via methods on Runner.
type runnerConfig struct {
	maxAttempts      int
	clock            func() time.Time
	tracer           tracing.Tracer
	retryWithinClaim bool
	retryAttempts    int
	retryInitial     time.Duration
	retryMaxElapsed  time.Duration
}

// RunnerOption configures a Runner.
type RunnerOption func(*runnerConfig)

// WithMaxAttempts sets the attempt ceiling passed to JobStore.Fail. Default 3.
// Values below 1 are clamped to 1. This is the durable axis: the store owns the
// retry count and dead-letters across claims. It is orthogonal to
// WithRetryWithinClaim.
func WithMaxAttempts(n int) RunnerOption {
	return func(c *runnerConfig) { c.maxAttempts = n }
}

// WithClock overrides the time source, primarily for deterministic tests. The
// reading is passed to Claim/Complete/Fail.
func WithClock(fn func() time.Time) RunnerOption {
	return func(c *runnerConfig) { c.clock = fn }
}

// WithTracer wraps the claim -> process -> complete/fail lifecycle in spans on
// the given Tracer: a job.claim span, a job.process span per attempt (with
// RecordError on a failed attempt), and job.complete / job.fail spans. It is
// opt-in; the default is tracing.Noop, so an unconfigured runner behaves exactly
// as before with no span overhead. A nil tracer is treated as tracing.Noop.
func WithTracer(tracer tracing.Tracer) RunnerOption {
	return func(c *runnerConfig) { c.tracer = tracer }
}

// WithRetryWithinClaim enables in-memory retry of a failing ProcessFunc inside a
// single claim, before the durable Fail path is taken. It is opt-in and default
// OFF: without it, a claim makes exactly one process attempt (jobs-v1 behavior).
//
// The runner retries up to attempts times under an exponential ctx-cancellable
// backoff — initialDelay, doubling each retry (initialDelay, 2×, 4×, …). On the
// first success it Completes; on exhaustion it calls JobStore.Fail exactly once
// with the unchanged durable maxAttempts, so the store's dead-letter bookkeeping
// is untouched. This axis multiplies with WithMaxAttempts: total ProcessFunc
// invocations are bounded by durable-attempts × attempts.
//
// maxElapsed is mandatory and must be > 0; wiring a non-positive value panics at
// construction. It caps the cumulative in-claim time — processing plus backoff
// sleeps — so the retry window can never outlive the store's claim lease. This
// is the lease contract, and it is load-bearing: the store reclaims a running
// job whose lease has expired (features/jobs memstore reclaims when
// claimed_at < now - lease; SQL stores do the same), so a retry window that
// overran the lease would let a second worker re-claim the same job mid-retry
// and run it concurrently. Set maxElapsed well below the store's lease.
//
// Cost, stated plainly: within-claim retries block the pool worker for the whole
// window (head-of-line blocking — that worker processes nothing else until the
// retries resolve). Keep attempts small and maxElapsed short.
func WithRetryWithinClaim(attempts int, initialDelay, maxElapsed time.Duration) RunnerOption {
	return func(c *runnerConfig) {
		if maxElapsed <= 0 {
			panic("workers: WithRetryWithinClaim requires maxElapsed > 0 (it must sit well below the store's claim lease)")
		}
		if attempts < 1 {
			attempts = 1
		}
		c.retryWithinClaim = true
		c.retryAttempts = attempts
		c.retryInitial = initialDelay
		c.retryMaxElapsed = maxElapsed
	}
}

// NewRunner builds a Runner over store and process. Use WorkFunc to hand it to
// a Pool. A nil logger falls back to slog.Default().
func NewRunner[T Job](
	store JobStore[T],
	process ProcessFunc[T],
	log *slog.Logger,
	opts ...RunnerOption,
) *Runner[T] {
	cfg := &runnerConfig{
		maxAttempts: defaultMaxAttempts,
		clock:       func() time.Time { return time.Now().UTC() },
		tracer:      tracing.Noop{},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if cfg.tracer == nil {
		cfg.tracer = tracing.Noop{}
	}
	if log == nil {
		log = slog.Default()
	}

	return &Runner[T]{
		store:            store,
		processFunc:      process,
		maxAttempts:      cfg.maxAttempts,
		clock:            cfg.clock,
		log:              log,
		tracer:           cfg.tracer,
		retryWithinClaim: cfg.retryWithinClaim,
		retryAttempts:    cfg.retryAttempts,
		retryInitial:     cfg.retryInitial,
		retryMaxElapsed:  cfg.retryMaxElapsed,
	}
}

// AddPreProcessHooks appends hooks that run after Claim and before ProcessFunc,
// in the order added.
func (r *Runner[T]) AddPreProcessHooks(hooks ...PreProcessHook[T]) {
	r.preHooks = append(r.preHooks, hooks...)
}

// AddPostProcessHooks appends hooks that run after ProcessFunc and before
// Complete/Fail, in the order added.
func (r *Runner[T]) AddPostProcessHooks(hooks ...PostProcessHook[T]) {
	r.postHooks = append(r.postHooks, hooks...)
}

// WorkFunc returns the lifecycle as a WorkFunc for a Pool.
func (r *Runner[T]) WorkFunc() WorkFunc {
	return r.work
}

func (r *Runner[T]) work(ctx context.Context) error {
	workerID := WorkerIDFromContext(ctx)

	job, err := r.claim(ctx, workerID)
	if err != nil {
		// ErrNoWork rides through so the pool backs off; a real store error
		// rides through so the pool counts it and stays active.
		return err
	}

	for _, hook := range r.preHooks {
		if hookErr := hook(ctx, job); hookErr != nil {
			r.log.ErrorContext(ctx, "pre-process hook failed", "job_id", job.ID(), "error", hookErr)
		}
	}

	r.log.InfoContext(ctx, "processing job", "worker_id", workerID, "job_id", job.ID())
	start := time.Now()

	processed, procErr := r.process(ctx, job)

	hookJob := processed
	if procErr != nil {
		hookJob = job
	}
	for _, hook := range r.postHooks {
		if hookErr := hook(ctx, hookJob, procErr); hookErr != nil {
			r.log.ErrorContext(ctx, "post-process hook failed", "job_id", job.ID(), "error", hookErr)
		}
	}

	if procErr != nil {
		r.fail(ctx, job, procErr)
		r.log.ErrorContext(ctx, "job failed",
			"worker_id", workerID, "job_id", job.ID(), "error", procErr, "duration", time.Since(start))
	} else {
		r.complete(ctx, processed)
		r.log.InfoContext(ctx, "job completed",
			"worker_id", workerID, "job_id", job.ID(), "duration", time.Since(start))
	}

	// A claimed-and-handled iteration is a success from the pool's view, even
	// when the job itself failed durably: keep polling actively for more work.
	return nil
}

// claim leases the next job from the store inside a job.claim span. ErrNoWork is
// not recorded as a span error; a real store error is.
func (r *Runner[T]) claim(ctx context.Context, workerID string) (T, error) {
	var zero T

	ctx, span := r.tracer.StartSpan(ctx, "job.claim")
	defer span.Finish()

	job, err := r.store.Claim(ctx, workerID, r.clock())
	if err != nil {
		if !errors.Is(err, ErrNoWork) {
			span.RecordError(err)
		}
		return zero, err
	}
	span.SetAttributes(tracing.StringAttribute("job.id", job.ID()))
	return job, nil
}

// process runs the ProcessFunc. Without WithRetryWithinClaim it makes exactly
// one panic-recovered, traced attempt (the jobs-v1 default). With it, it retries
// inside the single claim under an exponential ctx-cancellable backoff bounded by
// maxElapsed, returning the last error on exhaustion for the caller's single Fail.
func (r *Runner[T]) process(ctx context.Context, job T) (T, error) {
	if !r.retryWithinClaim {
		return r.processOnce(ctx, job)
	}
	return r.processWithRetry(ctx, job)
}

// processOnce runs a single ProcessFunc attempt inside a job.process span,
// converting a panic into a processing error so the job is failed instead of
// crashing the worker. A failed attempt (including a recovered panic) is
// recorded on the span.
func (r *Runner[T]) processOnce(ctx context.Context, job T) (out T, err error) {
	ctx, span := r.tracer.StartSpan(ctx, "job.process")
	span.SetAttributes(
		tracing.StringAttribute("job.id", job.ID()),
		tracing.StringAttribute("job.status", job.Status()),
	)
	defer span.Finish()
	defer func() {
		if rec := recover(); rec != nil {
			r.log.ErrorContext(ctx, "panic recovered in job",
				"job_id", job.ID(), "panic", rec, "stack", string(debug.Stack()))
			out = job
			err = fmt.Errorf("workers: panic in process: %v", rec)
		}
		if err != nil {
			span.RecordError(err)
		}
	}()
	return r.processFunc(ctx, job)
}

// processWithRetry retries a failing ProcessFunc inside one claim. retryCtx caps
// the whole window at maxElapsed (a child deadline of ctx), so both the backoff
// selects and a ctx-respecting ProcessFunc stop before the lease can expire. On
// success it returns nil; on exhaustion — attempts spent, maxElapsed reached, or
// ctx cancelled — it returns the last processing error for the caller's single
// Fail.
func (r *Runner[T]) processWithRetry(ctx context.Context, job T) (T, error) {
	retryCtx, cancel := context.WithTimeout(ctx, r.retryMaxElapsed)
	defer cancel()

	var out T
	var err error
	delay := r.retryInitial

	for attempt := 1; attempt <= r.retryAttempts; attempt++ {
		if attempt > 1 {
			r.log.InfoContext(ctx, "retrying job within claim",
				"job_id", job.ID(), "attempt", attempt, "within_claim_attempts", r.retryAttempts)

			timer := time.NewTimer(delay)
			select {
			case <-retryCtx.Done():
				timer.Stop()
				return out, err
			case <-timer.C:
			}
			delay *= 2
		}

		out, err = r.processOnce(retryCtx, job)
		if err == nil {
			return out, nil
		}
		// Stop early once the budget is spent or ctx is cancelled: the last
		// error rides out to a single Fail rather than sleeping into the lease.
		if retryCtx.Err() != nil {
			return out, err
		}
		r.log.ErrorContext(ctx, "within-claim process attempt failed",
			"job_id", job.ID(), "attempt", attempt, "error", err)
	}
	return out, err
}

func (r *Runner[T]) complete(ctx context.Context, job T) {
	ctx, span := r.tracer.StartSpan(ctx, "job.complete")
	defer span.Finish()
	span.SetAttributes(tracing.StringAttribute("job.id", job.ID()))

	if err := r.store.Complete(ctx, job.ID(), r.clock()); err != nil {
		span.RecordError(err)
		r.log.ErrorContext(ctx, "failed to mark job complete", "job_id", job.ID(), "error", err)
	}
}

func (r *Runner[T]) fail(ctx context.Context, job T, procErr error) {
	reason := "unknown error"
	if procErr != nil {
		reason = procErr.Error()
	}

	ctx, span := r.tracer.StartSpan(ctx, "job.fail")
	defer span.Finish()
	span.SetAttributes(
		tracing.StringAttribute("job.id", job.ID()),
		tracing.StringAttribute("job.status", job.Status()),
	)

	if err := r.store.Fail(ctx, job.ID(), r.clock(), reason, r.maxAttempts); err != nil {
		span.RecordError(err)
		r.log.ErrorContext(ctx, "failed to mark job failed", "job_id", job.ID(), "error", err)
	}
}
