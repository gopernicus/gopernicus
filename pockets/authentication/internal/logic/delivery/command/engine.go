package command

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Engine tuning defaults. Each zero field in Config selects its default so a
// transport wires only the knobs it cares about; the values mirror the bespoke
// worker (design §6.1.1) so the processor path preserves its retry/timeout budget.
const (
	// defaultMaxAttempts caps delivery attempts before a command fails permanently
	// and its challenge is discarded.
	defaultMaxAttempts = 5
	// defaultProviderTimeout bounds one provider send. The Engine owns this deadline
	// (a bounded, cancellable provider context), so a notifier that honors
	// cancellation returns promptly and no send outlives the claim lease.
	defaultProviderTimeout = 15 * time.Second
	// defaultBackoffBase is the first retry delay; each further attempt doubles it up
	// to defaultBackoffCap.
	defaultBackoffBase = 5 * time.Second
	// defaultBackoffCap ceilings the exponential retry backoff.
	defaultBackoffCap = 5 * time.Minute
)

// Coarse, secret-free reasons the Engine attaches to a Result. Each names only the
// phase (and never the provider's raw error, a destination, or a secret), so a
// Result is safe to log or project into status. They are the processor-side analog
// of the worker's failureReason.
const (
	reasonOpenFailed       = "payload could not be opened"
	reasonNoInitializer    = "no initializer wired for opaque command"
	reasonSealFailed       = "rendered payload could not be sealed"
	reasonInitialize       = "initialize failed"
	reasonCheckpoint       = "checkpoint failed"
	reasonDeliver          = "deliver failed"
	reasonInterrupted      = "interrupted before completion"
	reasonNothingToDeliver = "nothing to deliver"
)

// Discarder voids the challenge minted for a terminally-failed command. A transport
// invokes it as the per-kind terminal hook the OutcomePermanent contract names —
// AFTER it records the dead-letter, never before, so a crash between the two leaves
// the challenge un-discarded (best-effort) rather than voiding a challenge for work
// that could still be reclaimed. It is idempotent. The concrete Engine implements it
// alongside Processor so the same policy owns both the deliver classification and the
// discard it triggers.
type Discarder interface {
	Discard(ctx context.Context, claim Claim) error
}

// Config tunes the retry/timeout policy the Engine applies to one claimed command.
// Every zero field selects its package default.
type Config struct {
	MaxAttempts     int
	ProviderTimeout time.Duration
	// Backoff maps a just-spent attempt number (1-based) to the delay before the next
	// attempt. Nil selects a capped exponential (defaultBackoffBase doubling to
	// defaultBackoffCap).
	Backoff func(attempt int) time.Duration
}

// ProcessorDeps are the collaborators NewProcessor needs. Encrypter and Deliverer
// are required; Initializer is required only for opaque start commands (a nil
// Initializer fails such a command permanently rather than sending an unrendered
// message); Now defaults to time.Now.
type ProcessorDeps struct {
	Encrypter   cryptids.Encrypter
	Initializer Initializer
	Deliverer   Deliverer
	Now         func() time.Time
	Config      Config
}

// Engine is the concrete, transport-neutral delivery processor (AV3D-2.2): it opens
// one claimed command, initializes an opaque start off the request path, checkpoints
// the rendered result before any send, performs one bounded provider call, and
// classifies the outcome — returning an explicit Result the transport applies. It
// implements Processor (Process) and Discarder (the terminal hook). It never claims
// work, records a queue transition, or runs a polling loop; the durable jobs runtime
// and the bounded in-process pool each own those.
//
// The load-bearing sequence, held by Process:
//
//  1. open the envelope;
//  2. if opaque, resolve/issue/render off the request path (Initializer);
//  3. seal and SUCCESSFULLY checkpoint the rendered envelope (no send may precede a
//     successful checkpoint, so every retry — and an at-least-once crash replay —
//     resends the byte-identical secret rather than minting a new one);
//  4. call the provider under a bounded deadline (Deliverer);
//  5. return completion / retry-at / permanent (or skipped) as a Result.
//
// Step 6 — discard the challenge — is NOT part of Process: the transport records the
// dead-letter for an OutcomePermanent result and only then invokes Discard, so the
// discard cannot precede the recorded terminal transition.
type Engine struct {
	enc         cryptids.Encrypter
	initializer Initializer
	deliverer   Deliverer
	now         func() time.Time
	maxAttempts int
	timeout     time.Duration
	backoff     func(attempt int) time.Duration
}

// NewProcessor builds an Engine, filling defaults. A nil Encrypter or Deliverer is a
// construction error. The returned *Engine satisfies both Processor and Discarder.
func NewProcessor(d ProcessorDeps) (*Engine, error) {
	if d.Encrypter == nil {
		return nil, ErrEncrypterRequired
	}
	if d.Deliverer == nil {
		return nil, ErrDelivererRequired
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	cfg := d.Config
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.ProviderTimeout <= 0 {
		cfg.ProviderTimeout = defaultProviderTimeout
	}
	if cfg.Backoff == nil {
		cfg.Backoff = defaultBackoff
	}
	return &Engine{
		enc:         d.Encrypter,
		initializer: d.Initializer,
		deliverer:   d.Deliverer,
		now:         now,
		maxAttempts: cfg.MaxAttempts,
		timeout:     cfg.ProviderTimeout,
		backoff:     cfg.Backoff,
	}, nil
}

// Process drives one claimed command through the load-bearing sequence and returns
// its explicit disposition. It never sends before a successful checkpoint of a
// freshly rendered command, and it bounds the provider call with a deadline that
// sits inside the claim lease.
func (e *Engine) Process(ctx context.Context, claim Claim) Result {
	env, err := Open(e.enc, claim.Payload)
	if err != nil {
		// An unopenable payload can never be rendered or resent; failing it permanently
		// avoids an unbounded retry of a structurally dead command.
		return Permanent(reasonOpenFailed)
	}

	if env.Opaque() {
		rendered, ok, res := e.initialize(ctx, claim, env)
		if !ok {
			// The opaque envelope still carries the bounded routing enums for observation.
			return tag(res, env)
		}
		env = rendered
	}

	return tag(e.deliver(ctx, claim, env), env)
}

// tag stamps the bounded, secret-free routing enums onto a Result so a transport can
// emit a lifecycle observation without re-opening the sealed payload.
func tag(res Result, env Envelope) Result {
	res.Kind = env.Kind
	res.Purpose = env.Purpose
	return res
}

// initialize resolves an opaque start off the request path, seals the rendered
// result, and checkpoints it BEFORE any send. It returns ok=false with the Result
// the caller must return directly (a skip, a bounded retry, or a permanent failure);
// ok=true yields the rendered envelope to deliver. No provider call happens here.
func (e *Engine) initialize(ctx context.Context, claim Claim, opaque Envelope) (Envelope, bool, Result) {
	if e.initializer == nil {
		return Envelope{}, false, Permanent(reasonNoInitializer)
	}
	rendered, deliver, err := e.initializer.Initialize(ctx, opaque)
	if err != nil {
		return Envelope{}, false, e.classify(claim.Attempt, reasonInitialize)
	}
	if !deliver {
		// Unknown/ineligible identifier: terminate successfully with no send, so a known
		// and an unknown identifier are indistinguishable to the requester.
		return Envelope{}, false, Skipped(reasonNothingToDeliver)
	}
	sealed, err := Seal(e.enc, rendered)
	if err != nil {
		return Envelope{}, false, Permanent(reasonSealFailed)
	}
	if err := claim.Checkpoint.Checkpoint(ctx, sealed); err != nil {
		// The checkpoint is claim-fenced: a stale or superseded claim (or a transient
		// store error) fails here and the processor MUST NOT send. Reclassify as a
		// bounded retry — a superseded claim's reschedule no-ops against the fence, and a
		// transient store error is retried within the attempt budget.
		return Envelope{}, false, e.classify(claim.Attempt, reasonCheckpoint)
	}
	return rendered, true, Result{}
}

// deliver performs one bounded provider send of an already-rendered command. Success
// is Completed; a parent-context cancellation is a graceful interruption left for
// reclaim (never burned, even at the attempt cap); any other failure is a bounded
// retry that becomes Permanent once the attempt budget is spent.
func (e *Engine) deliver(ctx context.Context, claim Claim, env Envelope) Result {
	dctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	if err := e.deliverer.Deliver(dctx, env); err != nil {
		if ctx.Err() != nil {
			// The PARENT context (not the per-send deadline) was canceled: a graceful
			// shutdown. Reschedule immediately for reclaim rather than spending the budget.
			return Retry(e.now(), reasonInterrupted)
		}
		return e.classify(claim.Attempt, reasonDeliver)
	}
	return Completed()
}

// Discard is the per-kind terminal hook: after the transport records a dead-letter
// for an OutcomePermanent result, it opens the (checkpointed) payload and voids the
// challenge minted for the command. It is best-effort and idempotent, and a nil
// Initializer (a pre-rendered command with no minted challenge) is a no-op.
func (e *Engine) Discard(ctx context.Context, claim Claim) error {
	if e.initializer == nil {
		return nil
	}
	env, err := Open(e.enc, claim.Payload)
	if err != nil {
		return err
	}
	return e.initializer.Discard(ctx, env)
}

// classify maps a transient failure to a bounded retry, or to a permanent failure
// once the attempt budget is spent. attempt is the number already claimed (1-based).
func (e *Engine) classify(attempt int, reason string) Result {
	if attempt >= e.maxAttempts {
		return Permanent(reason)
	}
	return Retry(e.now().Add(e.backoff(attempt)), reason)
}

// defaultBackoff is a capped exponential: base * 2^(attempt-1), ceilinged at the cap.
// attempt is 1-based (the count already spent).
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
