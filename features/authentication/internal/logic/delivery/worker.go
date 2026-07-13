package delivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Worker tuning defaults. All are overridable through WorkerConfig; the zero
// value of each field selects the default so a host can wire only what it cares
// about.
const (
	// defaultLeaseFor bounds one claim's exclusive hold. A worker that crashes
	// mid-attempt loses its lease after this, and the still-pending job is
	// reclaimable (at-least-once lease recovery).
	defaultLeaseFor = 30 * time.Second
	// defaultProviderTimeout bounds one provider send. The worker owns this deadline
	// (design §6.1.1: a bounded, cancellable provider context); a notifier that
	// honors cancellation returns promptly, so no goroutine leaks past shutdown.
	defaultProviderTimeout = 15 * time.Second
	// defaultMaxAttempts caps delivery attempts before a job fails terminally and its
	// challenge is canceled.
	defaultMaxAttempts = 5
	// defaultPollInterval is how long Run idles when no job is due before polling
	// again.
	defaultPollInterval = time.Second
	// defaultPurgeInterval is how often Run purges terminal rows.
	defaultPurgeInterval = time.Hour
	// defaultPurgeRetention keeps terminal rows this long after they finish (the
	// documented retention policy) before they are eligible for purge.
	defaultPurgeRetention = 24 * time.Hour
	// defaultPurgeBatch bounds one purge sweep.
	defaultPurgeBatch = 500
	// defaultBackoffBase is the first retry delay; each further attempt doubles it up
	// to defaultBackoffCap.
	defaultBackoffBase = 5 * time.Second
	// defaultBackoffCap ceilings the exponential retry backoff.
	defaultBackoffCap = 5 * time.Minute
)

// Delivery outcomes recorded to the log and the optional Observer. They are
// deliberately coarse and secret/destination-free: a host classifies worker
// behavior by (job ID, kind, purpose, outcome, attempt) and never sees a
// recipient address or a rendered secret through this seam (design §6.1.1).
const (
	// OutcomeInitialized: an opaque start job was resolved+rendered and its encrypted
	// rendered payload persisted; the next claim delivers it.
	OutcomeInitialized = "initialized"
	// OutcomeSkipped: initialization found nothing to deliver (e.g. an unknown
	// identifier); the job terminates successfully with no send, so known and unknown
	// identifiers are indistinguishable.
	OutcomeSkipped = "skipped"
	// OutcomeDelivered: the provider accepted the message and the job succeeded.
	OutcomeDelivered = "delivered"
	// OutcomeRetried: a transient failure rescheduled the job with backoff.
	OutcomeRetried = "retried"
	// OutcomeFailed: the retry budget was exhausted (or a permanent error occurred);
	// the job failed terminally and its challenge was canceled.
	OutcomeFailed = "failed"
	// OutcomeShutdown: the parent context was canceled mid-attempt; the job is left
	// leased for reclaim rather than burned.
	OutcomeShutdown = "shutdown"
	// OutcomePurged: terminal rows were deleted under the retention policy.
	OutcomePurged = "purged"
)

// Initializer resolves an opaque start job into a rendered delivery envelope, off
// the request path (design §6.1.1). It is injected by the host composition (never
// imported by this package, which stays sdk/domain-only) because resolution and
// challenge issuance live in the auth services. A job needs initialization exactly
// once — when it first becomes deliverable — and the worker persists the rendered
// result so retries resend the identical secret rather than minting a chain of
// invalid links.
type Initializer interface {
	// Initialize resolves the account for cmd (the opened opaque envelope, carrying
	// ResolutionInput), issues/renders the challenge, and returns the fully rendered
	// Envelope with deliver=true. deliver=false means "nothing to deliver" (unknown/
	// ineligible identifier): the worker terminates the job successfully with no send.
	// A returned error is treated as transient and retried with backoff.
	Initialize(ctx context.Context, job deliveryjob.Job, cmd Envelope) (env Envelope, deliver bool, err error)
	// Discard cancels the challenge minted for job during a prior Initialize, called
	// once when the job fails terminally (design §6.1.1: a terminal send failure
	// deletes that challenge). It is idempotent and best-effort; env is the current
	// opened payload so the implementation can re-derive the challenge identity.
	Discard(ctx context.Context, job deliveryjob.Job, env Envelope) error
}

// Observer receives one secret/destination-free record per worker action so a host
// can drive metrics/health without the worker owning a metrics dependency. Nil is
// a no-op. Every field is safe to export to a dashboard: no recipient address and
// no rendered secret ever reach it.
type Observer interface {
	JobObserved(ctx context.Context, ev JobEvent)
}

// JobEvent is one observed worker action. Outcome is one of the Outcome* constants.
type JobEvent struct {
	JobID   string
	Kind    string
	Purpose string
	Outcome string
	Attempt int
	Count   int
}

// WorkerConfig tunes the claim/retry/purge loop. Every zero field selects its
// package default, so a host may wire only the knobs it wants.
type WorkerConfig struct {
	LeaseFor        time.Duration
	ProviderTimeout time.Duration
	MaxAttempts     int
	PollInterval    time.Duration
	PurgeInterval   time.Duration
	PurgeRetention  time.Duration
	PurgeBatch      int
	// Backoff maps a just-failed attempt number (1-based) to the delay before the
	// next attempt. Nil selects a capped exponential (defaultBackoffBase doubling to
	// defaultBackoffCap).
	Backoff func(attempt int) time.Duration
}

// WorkerDeps are the collaborators NewWorker needs. Repo, Encrypter, and Router
// are required; Initializer is required only for opaque start jobs (a nil
// Initializer fails such a job loudly rather than sending an unrendered message);
// LeaseID defaults to a fresh random per-worker identity; Now defaults to
// time.Now.
type WorkerDeps struct {
	Repo        deliveryjob.Repository
	Encrypter   cryptids.Encrypter
	Router      *Router
	Initializer Initializer
	Observer    Observer
	LeaseID     string
	Now         func() time.Time
	Logger      *slog.Logger
	Config      WorkerConfig
}

// Worker drains the durable outbox: it claims the oldest due job, initializes an
// opaque start job (once) or delivers an already-rendered one, and applies retry,
// terminal-failure, and purge policy — the at-least-once consumer of the
// deliveryjob.Repository (design §6.1.1).
//
// At-least-once delivery: because a claim is leased and a crash after the provider
// accepts a message but before the job is marked succeeded leaves the job
// reclaimable, a provider MAY receive the same message twice. That is safe — the
// rendered secret is persisted once, so a replay resends the identical valid
// secret, and redemption stays single-use because the challenge repository
// consumes atomically (design §3). The worker never mints a new secret on retry.
type Worker struct {
	repo        deliveryjob.Repository
	enc         cryptids.Encrypter
	router      *Router
	initializer Initializer
	observer    Observer
	leaseID     string
	now         func() time.Time
	logger      *slog.Logger
	cfg         WorkerConfig
}

// NewWorker builds a Worker, filling defaults. A nil Repo, Encrypter, or Router is
// a construction error.
func NewWorker(d WorkerDeps) (*Worker, error) {
	if d.Repo == nil {
		return nil, ErrRepositoryRequired
	}
	if d.Encrypter == nil {
		return nil, ErrEncrypterRequired
	}
	if d.Router == nil {
		return nil, fmt.Errorf("delivery: router is required: %w", sdk.ErrInvalidInput)
	}
	leaseID := d.LeaseID
	if leaseID == "" {
		leaseID = randomLeaseID()
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cfg := d.Config
	if cfg.LeaseFor <= 0 {
		cfg.LeaseFor = defaultLeaseFor
	}
	if cfg.ProviderTimeout <= 0 {
		cfg.ProviderTimeout = defaultProviderTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.PurgeInterval <= 0 {
		cfg.PurgeInterval = defaultPurgeInterval
	}
	if cfg.PurgeRetention <= 0 {
		cfg.PurgeRetention = defaultPurgeRetention
	}
	if cfg.PurgeBatch <= 0 {
		cfg.PurgeBatch = defaultPurgeBatch
	}
	if cfg.Backoff == nil {
		cfg.Backoff = defaultBackoff
	}
	return &Worker{
		repo:        d.Repo,
		enc:         d.Encrypter,
		router:      d.Router,
		initializer: d.Initializer,
		observer:    d.Observer,
		leaseID:     leaseID,
		now:         now,
		logger:      logger,
		cfg:         cfg,
	}, nil
}

// Run drains the outbox until ctx is canceled, then returns nil (graceful
// shutdown). It is the host-owned lifecycle hook: a host runs it in a goroutine
// and cancels ctx to stop — no jobs feature is imported and no worker goroutine
// outlives ctx. When jobs are due it drains without idling; when none are due it
// waits PollInterval, and it purges terminal rows on the purge tick.
func (w *Worker) Run(ctx context.Context) error {
	purge := time.NewTicker(w.cfg.PurgeInterval)
	defer purge.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker claim failed", slog.String("error", err.Error()))
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-purge.C:
			if _, err := w.Purge(ctx); err != nil && ctx.Err() == nil {
				w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker purge failed", slog.String("error", err.Error()))
			}
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

// RunOnce claims and processes at most one due job. It reports whether a job was
// claimed (so Run can drain a backlog before idling) and returns an error only for
// an unexpected repository failure — per-job dispositions (retry, fail, skip) are
// handled internally, never returned.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.repo.Claim(ctx, w.now(), w.leaseID, w.cfg.LeaseFor)
	if errors.Is(err, sdk.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	w.process(ctx, job)
	return true, nil
}

// process opens a claimed job's payload and routes it to initialization or
// delivery. A payload that cannot be decrypted is a permanent error — the job is
// failed terminally with a secret-free reason.
func (w *Worker) process(ctx context.Context, job deliveryjob.Job) {
	env, err := Open(w.enc, job.Payload)
	if err != nil {
		w.observe(ctx, slog.LevelWarn, job, OutcomeFailed, 0)
		_ = w.repo.Fail(ctx, job.ID, w.leaseID, "payload could not be opened", w.now())
		return
	}
	if needsInit(env) {
		w.initialize(ctx, job, env)
		return
	}
	w.deliver(ctx, job, env)
}

// initialize resolves+renders an opaque start job once and persists the encrypted
// rendered payload as a fresh pending job under the same idempotency key. The
// current opaque job is superseded (Replace cancels it); the next claim delivers
// the rendered job. Because the rendered payload is durable before any send, a
// crash between render and delivery re-renders cleanly (no message was sent), and
// a crash after the first send replays the identical persisted secret.
func (w *Worker) initialize(ctx context.Context, job deliveryjob.Job, cmd Envelope) {
	if w.initializer == nil {
		w.observe(ctx, slog.LevelWarn, job, OutcomeFailed, 0)
		_ = w.repo.Fail(ctx, job.ID, w.leaseID, "no initializer wired for opaque job", w.now())
		return
	}
	env, deliver, err := w.initializer.Initialize(ctx, job, cmd)
	if err != nil {
		w.handleFailure(ctx, job, cmd, "initialize", err)
		return
	}
	if !deliver {
		w.observe(ctx, slog.LevelInfo, job, OutcomeSkipped, 0)
		_ = w.repo.Succeed(ctx, job.ID, w.leaseID, w.now())
		return
	}
	sealed, err := Seal(w.enc, env)
	if err != nil {
		w.observe(ctx, slog.LevelWarn, job, OutcomeFailed, 0)
		_ = w.repo.Fail(ctx, job.ID, w.leaseID, "rendered payload could not be sealed", w.now())
		return
	}
	now := w.now().UTC()
	if _, err := w.repo.Replace(ctx, deliveryjob.Job{
		Kind:           job.Kind,
		Purpose:        job.Purpose,
		IdempotencyKey: job.IdempotencyKey,
		Payload:        sealed,
		AvailableAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		// Persisting the rendered payload failed; the opaque job stays leased and is
		// reclaimed for another initialize attempt. No message was sent.
		if ctx.Err() == nil {
			w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker persist-rendered failed",
				slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.String("error", err.Error()))
		}
		return
	}
	w.observe(ctx, slog.LevelInfo, job, OutcomeInitialized, 0)
}

// deliver performs one bounded provider send of an already-rendered job. Success
// completes the job; a shutdown cancellation leaves it leased for reclaim; any
// other failure is retried with backoff (or fails terminally at the attempt cap).
func (w *Worker) deliver(ctx context.Context, job deliveryjob.Job, env Envelope) {
	dctx, cancel := context.WithTimeout(ctx, w.cfg.ProviderTimeout)
	defer cancel()

	if err := w.router.Deliver(dctx, job.Kind, env); err != nil {
		if ctx.Err() != nil {
			// The parent context (not the per-send deadline) was canceled: a graceful
			// shutdown. Do not burn the job — leave the lease to expire and be reclaimed.
			w.observe(ctx, slog.LevelInfo, job, OutcomeShutdown, job.AttemptCount)
			return
		}
		w.handleFailure(ctx, job, env, "deliver", err)
		return
	}
	if err := w.repo.Succeed(ctx, job.ID, w.leaseID, w.now()); err != nil && ctx.Err() == nil {
		w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker mark-succeeded failed",
			slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.String("error", err.Error()))
	}
	w.observe(ctx, slog.LevelInfo, job, OutcomeDelivered, job.AttemptCount)
}

// handleFailure applies retry/terminal policy to a failed attempt. Below the
// attempt cap it reschedules with backoff; at the cap it fails the job terminally
// and cancels the challenge. The recorded reason is coarse and secret-free — never
// the provider's raw error, which could name the destination.
func (w *Worker) handleFailure(ctx context.Context, job deliveryjob.Job, env Envelope, phase string, cause error) {
	reason := failureReason(phase, cause)
	now := w.now()
	if job.AttemptCount >= w.cfg.MaxAttempts {
		if err := w.repo.Fail(ctx, job.ID, w.leaseID, reason, now); err != nil {
			if ctx.Err() == nil {
				w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker mark-failed failed",
					slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.String("error", err.Error()))
			}
			return
		}
		if w.initializer != nil {
			if err := w.initializer.Discard(ctx, job, env); err != nil && ctx.Err() == nil {
				w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker discard-challenge failed",
					slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.String("error", err.Error()))
			}
		}
		w.observe(ctx, slog.LevelWarn, job, OutcomeFailed, job.AttemptCount)
		return
	}
	availableAt := now.Add(w.cfg.Backoff(job.AttemptCount))
	if err := w.repo.Retry(ctx, job.ID, w.leaseID, availableAt, reason, now); err != nil && ctx.Err() == nil {
		w.logger.LogAttrs(ctx, slog.LevelWarn, "delivery worker reschedule failed",
			slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.String("error", err.Error()))
	}
	w.observe(ctx, slog.LevelInfo, job, OutcomeRetried, job.AttemptCount)
}

// Purge deletes terminal rows older than the retention window (bounded to
// PurgeBatch) and reports the count removed. Hosts may call it directly; Run calls
// it on the purge tick.
func (w *Worker) Purge(ctx context.Context) (int, error) {
	before := w.now().Add(-w.cfg.PurgeRetention)
	n, err := w.repo.PurgeTerminal(ctx, before, w.cfg.PurgeBatch)
	if err != nil {
		return 0, err
	}
	if n > 0 && w.observer != nil {
		w.observer.JobObserved(ctx, JobEvent{Outcome: OutcomePurged, Count: n})
	}
	return n, nil
}

// observe writes one secret/destination-free log line and forwards an event to the
// optional Observer.
func (w *Worker) observe(ctx context.Context, level slog.Level, job deliveryjob.Job, outcome string, attempt int) {
	w.logger.LogAttrs(ctx, level, "delivery job",
		slog.String("job_id", job.ID),
		slog.String("kind", job.Kind),
		slog.String("purpose", job.Purpose),
		slog.String("outcome", outcome),
		slog.Int("attempt", attempt),
	)
	if w.observer != nil {
		w.observer.JobObserved(ctx, JobEvent{
			JobID:   job.ID,
			Kind:    job.Kind,
			Purpose: job.Purpose,
			Outcome: outcome,
			Attempt: attempt,
		})
	}
}

// needsInit reports whether env is an opaque start command (no rendered content
// yet) rather than a ready-to-send message. A rendered message always has a Body
// (SMS/text) or HTML; an opaque command carries only the account-resolution input.
func needsInit(env Envelope) bool {
	return env.Body == "" && env.HTML == ""
}

// failureReason renders a coarse, secret-free failure summary for the job's
// last_error column and diagnostics. It surfaces only the phase and, for a
// transport error, the kind — never the wrapped cause, which may name a
// destination.
func failureReason(phase string, cause error) string {
	var de *DeliveryError
	if errors.As(cause, &de) {
		return fmt.Sprintf("%s failed via %s", phase, de.Kind)
	}
	return phase + " failed"
}

// defaultBackoff is a capped exponential: base * 2^(attempt-1), ceilinged at the
// cap. attempt is 1-based (the count already spent).
func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := defaultBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= defaultBackoffCap {
			return defaultBackoffCap
		}
	}
	if d > defaultBackoffCap {
		return defaultBackoffCap
	}
	return d
}

// randomLeaseID mints a per-worker lease identity so concurrent workers' claims
// never collide. It uses crypto/rand; a failure falls back to a timestamp, which
// is still unique enough to distinguish this process's leases in practice.
func randomLeaseID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
