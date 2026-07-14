package delivery

import (
	"context"
	"errors"
	"time"

	deliverycmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// JobsProcessor is the jobs-mode transport wrapper around the transport-neutral
// deliverycmd.Engine (AV3D-3.1). A generic-jobs FencedRuntime registers Handle as the
// per-kind handler and Discard as the per-kind terminal hook; the Engine opens the
// claimed deliverycmd.Envelope, resolves an opaque start off the request path,
// checkpoints the rendered sealed payload under the claim fence BEFORE any send,
// delivers under a bounded timeout, and classifies the outcome. It owns no polling
// loop and never claims work — the jobs runtime does.
//
// The seams are stdlib-typed on purpose: the authentication public surface re-exports
// Handle/Discard as plain funcs so the composition adapter (which imports both
// features) wires them onto the jobs runtime without the authentication core ever
// importing the jobs feature.
type JobsProcessor struct {
	engine   *deliverycmd.Engine
	enc      cryptids.Encrypter
	observer deliverycmd.Observer
}

// JobsProcessorDeps are the collaborators NewJobsProcessor needs. Encrypter and
// Router are required; Initializer is required only for opaque start commands (a nil
// Initializer fails such a command permanently rather than sending an unrendered
// message). Now defaults to time.Now; Config tunes the retry/timeout policy. Observer
// is the OPTIONAL, best-effort lifecycle observer the transport emits on each
// transition; it is passed through deliverycmd.SafeObserve, so a nil, erroring, or
// panicking observer never changes a delivery outcome.
type JobsProcessorDeps struct {
	Encrypter   cryptids.Encrypter
	Router      *Router
	Initializer Initializer
	Now         func() time.Time
	Config      deliverycmd.Config
	Observer    deliverycmd.Observer
}

// NewJobsProcessor builds the jobs-mode processor, adapting the bespoke
// Router/Initializer collaborators to the deliverycmd.Engine's deliverycmd.Envelope-typed
// Deliverer/Initializer. A nil Encrypter or Router is a construction error.
func NewJobsProcessor(d JobsProcessorDeps) (*JobsProcessor, error) {
	if d.Router == nil {
		return nil, ErrRouterRequired
	}
	var initializer deliverycmd.Initializer
	if d.Initializer != nil {
		initializer = commandInitializer{inner: d.Initializer}
	}
	engine, err := deliverycmd.NewProcessor(deliverycmd.ProcessorDeps{
		Encrypter:   d.Encrypter,
		Initializer: initializer,
		Deliverer:   commandDeliverer{router: d.Router},
		Now:         d.Now,
		Config:      d.Config,
	})
	if err != nil {
		return nil, err
	}
	return &JobsProcessor{engine: engine, enc: d.Encrypter, observer: d.Observer}, nil
}

// jobFailure carries the Engine's explicit retry/permanent verdict across the
// stdlib-typed handler seam as an error, so a composition adapter maps it onto the
// generic jobs runtime's retry-at / dead-letter policy without the authentication core
// importing the jobs feature. Its Error() is the coarse, secret-free reason.
type jobFailure struct {
	reason    string
	permanent bool
}

func (e *jobFailure) Error() string { return e.reason }

// HandleErrorPermanent reports whether a delivery handle error carries the permanent
// disposition — the signal a jobs-mode transport uses to dead-letter IMMEDIATELY
// rather than retry. It is the classifier the composition adapter consults; a plain
// (transient) error reports false and is retried with bounded backoff.
func HandleErrorPermanent(err error) bool {
	var f *jobFailure
	if errors.As(err, &f) {
		return f.permanent
	}
	return false
}

// Handle processes one claimed delivery command. It returns nil for a completed or
// skipped (non-failed terminal) outcome so the runtime completes the claim; for a
// transient failure a plain retryable error; and for a permanent failure a jobFailure
// carrying the permanent disposition (HandleErrorPermanent reports true) so the
// transport dead-letters it immediately. checkpoint persists the rendered sealed
// payload under the current claim fence; the Engine calls it before any send.
// executionID is the opaque unit-of-work ID, used only for the best-effort lifecycle
// observation (never a recipient or logical key).
func (p *JobsProcessor) Handle(ctx context.Context, executionID string, payload []byte, attempt int, checkpoint func(ctx context.Context, sealed []byte) error) error {
	res := p.engine.Process(ctx, deliverycmd.Claim{
		Payload:    payload,
		Attempt:    attempt,
		Checkpoint: checkpointFn(checkpoint),
	})
	p.observe(ctx, executionID, res, attempt)
	switch res.Outcome {
	case deliverycmd.OutcomeCompleted, deliverycmd.OutcomeSkipped:
		return nil
	case deliverycmd.OutcomePermanent:
		// The reason is a coarse, static, secret-free token (the failed phase) — never
		// the provider's raw error, a destination, or a secret.
		return &jobFailure{reason: res.Reason, permanent: true}
	default: // OutcomeRetry: a transient failure retried with bounded backoff.
		return &jobFailure{reason: res.Reason, permanent: false}
	}
}

// observe emits the best-effort lifecycle transition for a completed Result through
// deliverycmd.SafeObserve. A permanent outcome is NOT observed here: the dead_lettered
// transition is emitted from Discard, AFTER the runtime records the terminal state, so
// the observation never precedes the recorded dead-letter.
func (p *JobsProcessor) observe(ctx context.Context, executionID string, res deliverycmd.Result, attempt int) {
	if p.observer == nil {
		return
	}
	var tr deliverycmd.Transition
	switch res.Outcome {
	case deliverycmd.OutcomeCompleted:
		tr = deliverycmd.TransitionDelivered
	case deliverycmd.OutcomeSkipped:
		tr = deliverycmd.TransitionSkipped
	case deliverycmd.OutcomeRetry:
		tr = deliverycmd.TransitionRetried
	default: // OutcomePermanent handled in Discard
		return
	}
	deliverycmd.SafeObserve(ctx, p.observer, deliverycmd.LifecycleEvent{
		ExecutionID: executionID,
		Kind:        res.Kind,
		Purpose:     res.Purpose,
		Transition:  tr,
		Attempt:     attempt,
	})
}

// Discard is the per-kind terminal hook: after the runtime records a dead-letter it
// opens the checkpointed payload and voids the challenge minted for the command
// (best-effort, idempotent). A nil Initializer (pre-rendered command) is a no-op. It
// then emits the dead_lettered lifecycle observation — AFTER the recorded terminal
// transition — and does so even if the discard itself failed (the dead-letter was
// still recorded). The discard error is returned so the runtime can log it; it never
// resurrects the job.
func (p *JobsProcessor) Discard(ctx context.Context, executionID string, payload []byte) error {
	discardErr := p.engine.Discard(ctx, deliverycmd.Claim{Payload: payload})
	if p.observer != nil {
		kind, purpose := p.meta(payload)
		deliverycmd.SafeObserve(ctx, p.observer, deliverycmd.LifecycleEvent{
			ExecutionID: executionID,
			Kind:        kind,
			Purpose:     purpose,
			Transition:  deliverycmd.TransitionDeadLettered,
		})
	}
	return discardErr
}

// ObservePurge emits the batch purged lifecycle observation after a host drives the
// generic terminal purge. count is the number of terminal generations removed. It is
// best-effort (deliverycmd.SafeObserve) and carries no per-execution ID or secret.
func (p *JobsProcessor) ObservePurge(ctx context.Context, count int) {
	if p.observer == nil {
		return
	}
	deliverycmd.SafeObserve(ctx, p.observer, deliverycmd.LifecycleEvent{
		Transition: deliverycmd.TransitionPurged,
		Count:      count,
	})
}

// meta opens payload for its bounded, secret-free routing enums so a dead_lettered
// observation carries Kind/Purpose. An unopenable payload yields empty enums (the
// observation is still emitted with the execution ID).
func (p *JobsProcessor) meta(payload []byte) (kind, purpose string) {
	env, err := deliverycmd.Open(p.enc, payload)
	if err != nil {
		return "", ""
	}
	return env.Kind, env.Purpose
}

// checkpointFn adapts a stdlib checkpoint closure to deliverycmd.Checkpointer, so the
// jobs runtime's claim-scoped, lease-fenced checkpoint reaches the Engine without the
// command package learning the jobs vocabulary.
type checkpointFn func(ctx context.Context, sealed []byte) error

func (f checkpointFn) Checkpoint(ctx context.Context, sealed []byte) error {
	if f == nil {
		return nil
	}
	return f(ctx, sealed)
}

// commandInitializer adapts the bespoke delivery.Initializer (the auth service, which
// resolves accounts and issues challenges) to the deliverycmd.Envelope-typed
// deliverycmd.Initializer the Engine drives. It hands the initializer the envelope's
// secret-free routing metadata (kind/purpose) so it routes its resolve/issue/render
// policy without re-parsing the payload.
type commandInitializer struct {
	inner Initializer
}

func (c commandInitializer) Initialize(ctx context.Context, opaque deliverycmd.Envelope) (deliverycmd.Envelope, bool, error) {
	rendered, deliver, err := c.inner.Initialize(ctx, opaque.Kind, opaque.Purpose, commandToEnvelope(opaque))
	if err != nil || !deliver {
		return deliverycmd.Envelope{}, deliver, err
	}
	out, err := deliverycmd.NewRendered(opaque.Kind, opaque.Purpose, opaque.Key,
		rendered.Destination, rendered.Subject, rendered.Body, rendered.HTML, rendered.Secret)
	if err != nil {
		return deliverycmd.Envelope{}, false, err
	}
	return out, true, nil
}

func (c commandInitializer) Discard(ctx context.Context, env deliverycmd.Envelope) error {
	return c.inner.Discard(ctx, env.Kind, env.Purpose, commandToEnvelope(env))
}

// commandDeliverer adapts the bespoke kind-aware Router to the deliverycmd.Envelope-typed
// deliverycmd.Deliverer the Engine calls for one bounded provider send.
type commandDeliverer struct {
	router *Router
}

func (d commandDeliverer) Deliver(ctx context.Context, env deliverycmd.Envelope) error {
	return d.router.Deliver(ctx, env.Kind, commandToEnvelope(env))
}
