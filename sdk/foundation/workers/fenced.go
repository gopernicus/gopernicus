package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// defaultLeaseFor is the claim lease a FencedRunner stamps when no WithLeaseDuration
// option is passed. It must sit safely above a job's expected processing time so
// the lease does not lapse mid-process, and safely below the store's reclaim
// cadence.
const defaultLeaseFor = 30 * time.Second

// FencedStore is the lease-fenced durable queue a FencedRunner drives — the kernel
// contract that hardens JobStore so a stale worker cannot clobber a reclaimed job.
//
// It differs from JobStore in one load-bearing way: Claim stamps a fresh per-claim
// lease token (caller-supplied), and Complete/Fail require that token, so a worker
// whose lease was reclaimed (its lease lapsed and another worker took the job) or
// superseded is fenced out with sdk.ErrConflict and cannot mutate the current
// execution. WorkerName-style reusable identity cannot fence a reclaim under the
// same worker; a per-claim lease can.
//
// features/jobs' job.FencedQueueRepository is a strict superset of this port (it
// adds enqueue/checkpoint/keyed/purge surface); the compile assertion lives there
// so a FencedQueueRepository is directly usable as the store a FencedRunner drives.
type FencedStore[T Job] interface {
	// Claim atomically leases the next due job under the caller-supplied fresh
	// leaseID for leaseFor and returns it, or ErrNoWork when none is due. now is
	// the runner's clock reading so the store computes the lease deadline against a
	// single time source. A given job is claimed by at most one concurrent caller.
	Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (T, error)

	// Complete marks the leaseID-held job done. A reclaimed or superseded lease
	// yields sdk.ErrConflict; the last holder's repeat Complete is idempotent nil;
	// an unknown id yields sdk.ErrNotFound.
	Complete(ctx context.Context, id, leaseID string, now time.Time) error

	// Fail records terminal failure of the leaseID-held job with reason. A reclaimed
	// or superseded lease yields sdk.ErrConflict; the last holder's repeat Fail is
	// idempotent nil; an unknown id yields sdk.ErrNotFound.
	Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error

	// Reschedule moves the leaseID-held job to a future availableAt (retry-at) and
	// clears the lease, so the store does not re-serve the job before then — a
	// durable backoff with no busy-loop and no in-runner sleep. A reclaimed or
	// superseded lease yields sdk.ErrConflict; an unknown id yields sdk.ErrNotFound.
	Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error
}

// FencedRetryFunc computes the retry-at backoff for a failed fenced job. It returns
// the delay before the next attempt and true to reschedule the job at clock()+delay
// (a durable retry-at, not an in-runner sleep), or false to dead-letter it
// permanently. attempt is the job's RetryCount after the claim increment (1 on the
// first attempt), so a policy caps retries by comparing it to a ceiling. A
// FencedRunner without WithFencedRetry dead-letters on the first failure — the
// AV3D-1.1 default, unchanged.
type FencedRetryFunc func(attempt int) (delay time.Duration, retry bool)

// FencedRetryDecider is the error-aware retry policy: unlike FencedRetryFunc it sees
// the process error, so a consumer can classify a failure as permanent (dead-letter
// NOW, regardless of attempt) or transient (retry-at) rather than deciding by attempt
// count alone (AV3D-3.4). It returns the delay before the next attempt and true to
// reschedule at clock()+delay, or false to dead-letter permanently. When set (via
// WithFencedRetryDecider) it SUPERSEDES FencedRetryFunc; a consuming feature routes a
// processor's explicit retry/permanent verdict onto the runner's reschedule/fail path
// through it.
type FencedRetryDecider func(err error, attempt int) (delay time.Duration, retry bool)

// FencedDeadLetterFunc runs AFTER a fenced job's permanent dead-letter transition is
// durably recorded (store.Fail returned nil). Its error is logged and reported but
// never propagated: a hook failure can never resurrect the job. It never runs on a
// retry-at reschedule (not a terminal transition) or when Fail is fenced by a
// reclaimed/superseded lease (this worker did not record the transition).
// features/jobs' jobs.DeadLetterFunc is this hook with T = job.Job.
type FencedDeadLetterFunc[T Job] func(ctx context.Context, job T) error

// FencedRunner composes a FencedStore and a ProcessFunc into a WorkFunc, driving
// the lease-fenced lifecycle:
//
//	Claim(fresh lease) -> process -> Complete(lease) (ok) / Fail(lease) (err)
//
// It differs from Runner in the two ways the lease fence demands:
//
//   - It mints a fresh per-claim lease token and threads it through Complete/Fail.
//     A Complete or Fail that returns sdk.ErrConflict is recognized as a
//     reclaimed/superseded claim and logged as fenced — it is NEVER reported as a
//     successful completion, so a stale worker cannot make the pool believe it
//     finished work another worker now owns.
//   - On context cancellation (pool shutdown) it leaves the in-flight job
//     reclaimable: it does not Complete or Fail, so the lease simply lapses and
//     another worker reclaims the work. A late Complete/Fail from this worker would
//     in any case be fenced out by the store.
//
// Retry, timeout, and dead-letter behavior (AV3D-1.4):
//
//   - Without WithFencedRetry a process error takes the terminal Fail path (the
//     AV3D-1.1 default, unchanged). With it, a process error consults the
//     FencedRetryFunc: on retry it Reschedules the job to clock()+delay (a durable
//     retry-at that the store will not re-serve before then — no busy-loop, no
//     in-runner backoff sleep) and on exhaustion it Fails permanently.
//   - WithFencedRetryDecider supersedes WithFencedRetry with an error-aware policy:
//     the FencedRetryDecider sees the process error, so a consumer routes a
//     processor's explicit verdict — permanent (dead-letter now) versus transient
//     (retry-at) — onto the reschedule/fail path instead of deciding by attempt alone.
//   - WithFencedProcessTimeout bounds each ProcessFunc attempt with a child context
//     deadline that must sit safely inside the claim lease, so a stuck provider call
//     is cancelled before the lease can lapse. The bound is context-cancellable:
//     pool shutdown cancels it and the attempt returns promptly.
//   - SetDeadLetterHook registers a FencedDeadLetterFunc that runs ONLY after a
//     permanent Fail transition is recorded; its failure is logged, never resurrects
//     the job, and it never runs on a retry-at reschedule or a fenced Fail.
//
// Within-claim retry and pre/post hooks remain Runner features not duplicated onto
// the fenced path until a consumer needs them.
type FencedRunner[T Job] struct {
	store          FencedStore[T]
	process        ProcessFunc[T]
	clock          func() time.Time
	leaseFor       time.Duration
	processTimeout time.Duration
	retry          FencedRetryFunc
	retryDecider   FencedRetryDecider
	newLease       func() string
	deadLetter     FencedDeadLetterFunc[T]
	log            *slog.Logger
}

// fencedRunnerConfig carries functional-option values (generics and functional
// options mix poorly, so non-generic options set fields here; the generic
// dead-letter hook is set via SetDeadLetterHook).
type fencedRunnerConfig struct {
	clock          func() time.Time
	leaseFor       time.Duration
	processTimeout time.Duration
	retry          FencedRetryFunc
	retryDecider   FencedRetryDecider
	newLease       func() string
}

// FencedRunnerOption configures a FencedRunner.
type FencedRunnerOption func(*fencedRunnerConfig)

// WithFencedClock overrides the time source, primarily for deterministic tests.
// The reading is passed to Claim/Complete/Fail.
func WithFencedClock(fn func() time.Time) FencedRunnerOption {
	return func(c *fencedRunnerConfig) { c.clock = fn }
}

// WithLeaseDuration sets the claim lease the runner requests on each Claim.
// Non-positive values are ignored and defaultLeaseFor is kept. Set it safely above
// a job's processing time and below the store's reclaim cadence.
func WithLeaseDuration(d time.Duration) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if d > 0 {
			c.leaseFor = d
		}
	}
}

// WithLeaseTokenFunc overrides the per-claim lease-token generator, primarily so a
// test can mint deterministic tokens. Production uses the default crypto-random
// generator.
func WithLeaseTokenFunc(fn func() string) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if fn != nil {
			c.newLease = fn
		}
	}
}

// WithFencedRetry enables retry-at rescheduling on process failure. Without it a
// failed process is dead-lettered on the first failure (the AV3D-1.1 default). With
// it, the FencedRetryFunc decides per attempt whether to Reschedule the job to a
// future retry-at (durable backoff, no in-runner sleep, so the store will not
// re-serve it before then) or to Fail it permanently. A nil fn is ignored.
func WithFencedRetry(fn FencedRetryFunc) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if fn != nil {
			c.retry = fn
		}
	}
}

// WithFencedRetryDecider enables the error-aware retry policy. When set it SUPERSEDES
// WithFencedRetry: on a process failure the FencedRetryDecider sees the error and the
// attempt count and decides whether to Reschedule (durable retry-at, no in-runner
// sleep) or Fail permanently — so a consumer can dead-letter a permanent error at once
// while retrying a transient one with bounded backoff. A nil fn is ignored.
func WithFencedRetryDecider(fn FencedRetryDecider) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if fn != nil {
			c.retryDecider = fn
		}
	}
}

// WithFencedProcessTimeout bounds each ProcessFunc attempt with a child-context
// deadline. Set it safely below the claim lease so a stuck provider call is
// cancelled before the lease can lapse and a second worker reclaims the job. The
// bound is context-cancellable: pool shutdown cancels the parent and the attempt
// returns promptly. Non-positive values are ignored (no per-attempt timeout, the
// default).
func WithFencedProcessTimeout(d time.Duration) FencedRunnerOption {
	return func(c *fencedRunnerConfig) {
		if d > 0 {
			c.processTimeout = d
		}
	}
}

// NewFencedRunner builds a FencedRunner over store and process. Use WorkFunc to
// hand it to a Pool. A nil logger falls back to slog.Default().
func NewFencedRunner[T Job](
	store FencedStore[T],
	process ProcessFunc[T],
	log *slog.Logger,
	opts ...FencedRunnerOption,
) *FencedRunner[T] {
	cfg := &fencedRunnerConfig{
		clock:    func() time.Time { return time.Now().UTC() },
		leaseFor: defaultLeaseFor,
		newLease: newLeaseToken,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if log == nil {
		log = slog.Default()
	}

	return &FencedRunner[T]{
		store:          store,
		process:        process,
		clock:          cfg.clock,
		leaseFor:       cfg.leaseFor,
		processTimeout: cfg.processTimeout,
		retry:          cfg.retry,
		retryDecider:   cfg.retryDecider,
		newLease:       cfg.newLease,
		log:            log,
	}
}

// SetDeadLetterHook registers the hook that runs after a permanent dead-letter
// transition is recorded. It is a method rather than an option because the hook is
// generic over T (mirroring Runner.AddPostProcessHooks). A nil hook clears it.
func (r *FencedRunner[T]) SetDeadLetterHook(hook FencedDeadLetterFunc[T]) {
	r.deadLetter = hook
}

// WorkFunc returns the lease-fenced lifecycle as a WorkFunc for a Pool.
func (r *FencedRunner[T]) WorkFunc() WorkFunc { return r.work }

func (r *FencedRunner[T]) work(ctx context.Context) error {
	leaseID := r.newLease()

	job, err := r.store.Claim(ctx, r.clock(), leaseID, r.leaseFor)
	if err != nil {
		// ErrNoWork rides through so the pool backs off; a real store error rides
		// through so the pool counts it and stays active.
		return err
	}

	r.log.InfoContext(ctx, "processing fenced job", "job_id", job.ID(), "lease_id", leaseID)
	start := time.Now()

	processed, procErr := r.processOnce(ctx, job)

	// Shutdown/cancellation: leave the job reclaimable. Completing or failing it now
	// would either clobber work a later worker must redo or dead-letter work the
	// shutdown never finished — instead the lease lapses and another worker reclaims
	// it, and any late Complete/Fail from this worker is fenced out by the store.
	if ctx.Err() != nil {
		r.log.InfoContext(ctx, "fenced job left reclaimable on shutdown",
			"job_id", job.ID(), "lease_id", leaseID)
		return nil
	}

	if procErr != nil {
		r.handleFailure(ctx, job, leaseID, procErr, start)
	} else {
		r.complete(ctx, processed, leaseID, start)
	}

	// A claimed-and-handled iteration is a success from the pool's view (keep
	// polling actively), even when the job failed durably or the completion was
	// fenced by a reclaim.
	return nil
}

// processOnce runs a single ProcessFunc attempt, converting a panic into a
// processing error so the job is failed instead of crashing the worker. When a
// process timeout is configured the attempt runs under a child-context deadline
// bounded inside the claim lease, so a stuck provider call is cancelled before the
// lease can lapse; pool shutdown cancels the parent and the attempt returns promptly.
func (r *FencedRunner[T]) processOnce(ctx context.Context, job T) (out T, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.ErrorContext(ctx, "panic recovered in fenced job",
				"job_id", job.ID(), "panic", rec, "stack", string(debug.Stack()))
			out = job
			err = fmt.Errorf("workers: panic in process: %v", rec)
		}
	}()

	if r.processTimeout > 0 {
		tctx, cancel := context.WithTimeout(ctx, r.processTimeout)
		defer cancel()
		return r.process(tctx, job)
	}
	return r.process(ctx, job)
}

func (r *FencedRunner[T]) complete(ctx context.Context, job T, leaseID string, start time.Time) {
	err := r.store.Complete(ctx, job.ID(), leaseID, r.clock())
	switch {
	case err == nil:
		r.log.InfoContext(ctx, "fenced job completed",
			"job_id", job.ID(), "lease_id", leaseID, "duration", time.Since(start))
	case errors.Is(err, sdk.ErrConflict):
		// The lease was reclaimed or superseded: another worker owns the current
		// execution. This is NOT a completion — do not report success.
		r.log.WarnContext(ctx, "fenced completion superseded by a reclaimed lease",
			"job_id", job.ID(), "lease_id", leaseID)
	default:
		r.log.ErrorContext(ctx, "failed to complete fenced job",
			"job_id", job.ID(), "lease_id", leaseID, "error", err)
	}
}

// handleFailure resolves a process failure: with a retry policy that elects to
// retry it reschedules the job to a durable retry-at; otherwise it dead-letters the
// job permanently and fires the dead-letter hook only after that transition is
// recorded.
func (r *FencedRunner[T]) handleFailure(ctx context.Context, job T, leaseID string, procErr error, start time.Time) {
	reason := "unknown error"
	if procErr != nil {
		reason = procErr.Error()
	}

	// The error-aware decider supersedes the attempt-only policy: it can dead-letter a
	// permanent failure immediately while retrying a transient one with bounded backoff.
	if r.retryDecider != nil {
		if delay, retry := r.retryDecider(procErr, job.RetryCount()); retry {
			r.reschedule(ctx, job, leaseID, delay, reason)
			return
		}
		r.deadLetterFail(ctx, job, leaseID, procErr, reason, start)
		return
	}
	if r.retry != nil {
		if delay, retry := r.retry(job.RetryCount()); retry {
			r.reschedule(ctx, job, leaseID, delay, reason)
			return
		}
	}
	r.deadLetterFail(ctx, job, leaseID, procErr, reason, start)
}

// reschedule requeues the failed job to clock()+delay (retry-at). A fenced or errored
// Reschedule is logged, never retried in a loop: the job stays owned by whoever holds
// the current lease.
func (r *FencedRunner[T]) reschedule(ctx context.Context, job T, leaseID string, delay time.Duration, reason string) {
	availableAt := r.clock().Add(delay)
	err := r.store.Reschedule(ctx, job.ID(), leaseID, availableAt, reason, r.clock())
	switch {
	case err == nil:
		r.log.InfoContext(ctx, "fenced job rescheduled for retry",
			"job_id", job.ID(), "lease_id", leaseID, "retry_at", availableAt, "attempt", job.RetryCount())
	case errors.Is(err, sdk.ErrConflict):
		r.log.WarnContext(ctx, "fenced reschedule superseded by a reclaimed lease",
			"job_id", job.ID(), "lease_id", leaseID)
	default:
		r.log.ErrorContext(ctx, "failed to reschedule fenced job",
			"job_id", job.ID(), "lease_id", leaseID, "error", err)
	}
}

// deadLetterFail permanently dead-letters the job and, ONLY when that transition is
// recorded (Fail returned nil), fires the dead-letter hook. A fenced Fail does not
// fire the hook — this worker did not record the transition. The hook's own error is
// logged and swallowed so it can never resurrect the job.
func (r *FencedRunner[T]) deadLetterFail(ctx context.Context, job T, leaseID string, procErr error, reason string, start time.Time) {
	err := r.store.Fail(ctx, job.ID(), leaseID, reason, r.clock())
	switch {
	case err == nil:
		r.log.ErrorContext(ctx, "fenced job failed",
			"job_id", job.ID(), "lease_id", leaseID, "error", procErr, "duration", time.Since(start))
		r.fireDeadLetter(ctx, job, leaseID)
	case errors.Is(err, sdk.ErrConflict):
		r.log.WarnContext(ctx, "fenced failure superseded by a reclaimed lease",
			"job_id", job.ID(), "lease_id", leaseID)
	default:
		r.log.ErrorContext(ctx, "failed to fail fenced job",
			"job_id", job.ID(), "lease_id", leaseID, "error", err)
	}
}

// fireDeadLetter runs the registered dead-letter hook after the transition is
// recorded, logging (never propagating) a hook error.
func (r *FencedRunner[T]) fireDeadLetter(ctx context.Context, job T, leaseID string) {
	if r.deadLetter == nil {
		return
	}
	if err := r.deadLetter(ctx, job); err != nil {
		r.log.ErrorContext(ctx, "fenced dead-letter hook failed",
			"job_id", job.ID(), "lease_id", leaseID, "error", err)
	}
}

// newLeaseToken returns a random, collision-free per-claim lease token.
func newLeaseToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "lease_" + hex.EncodeToString(b[:])
}
