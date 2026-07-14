package command

import (
	"context"
	"time"
)

// Outcome is the explicit disposition a Processor returns for one claimed delivery
// command. The transport (durable jobs mode or bounded in-process mode) OWNS the
// resulting queue transition — the processor only classifies; it never claims a
// job, records a terminal state, or runs a polling loop.
type Outcome int

const (
	// OutcomeCompleted: the command was delivered (or an opaque start was resolved to
	// "nothing to deliver" and finished). The transport completes the claim.
	OutcomeCompleted Outcome = iota
	// OutcomeSkipped: initialization found nothing deliverable (an unknown/ineligible
	// identifier). It is a NON-FAILED terminal — indistinguishable to the requester
	// from a delivered message — so the transport completes the claim without a send.
	OutcomeSkipped
	// OutcomeRetry: a transient failure. The transport reschedules the claim to
	// Result.RetryAt (bounded backoff) without minting a new secret.
	OutcomeRetry
	// OutcomePermanent: a permanent failure or an exhausted retry budget. The
	// transport dead-letters the claim; the per-kind terminal hook then discards the
	// undeliverable challenge.
	OutcomePermanent
)

// String renders the outcome as a stable, secret-free token for logs and tests.
func (o Outcome) String() string {
	switch o {
	case OutcomeCompleted:
		return "completed"
	case OutcomeSkipped:
		return "skipped"
	case OutcomeRetry:
		return "retry"
	case OutcomePermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// Result is the explicit disposition of one Process call. Reason is a COARSE,
// secret-free classification (e.g. the failed phase and transport kind) suitable for
// a log line or a status projection — never the provider's raw error, a destination,
// or a secret. RetryAt is set only for OutcomeRetry. Kind and Purpose are the opened
// envelope's bounded, secret-free routing enums, carried so a transport can emit a
// lifecycle observation without re-opening the sealed payload; both are empty when the
// payload could not be opened.
type Result struct {
	Outcome Outcome
	RetryAt time.Time
	Reason  string
	Kind    string
	Purpose string
}

// Completed reports a delivered (or resolved-to-nothing-and-finished) command.
func Completed() Result { return Result{Outcome: OutcomeCompleted} }

// Skipped reports a non-failed terminal for an unknown/ineligible identifier. reason
// must be secret-free.
func Skipped(reason string) Result { return Result{Outcome: OutcomeSkipped, Reason: reason} }

// Retry reports a transient failure to be retried at t. reason must be secret-free.
func Retry(t time.Time, reason string) Result {
	return Result{Outcome: OutcomeRetry, RetryAt: t, Reason: reason}
}

// Permanent reports a terminal failure. reason must be secret-free.
func Permanent(reason string) Result { return Result{Outcome: OutcomePermanent, Reason: reason} }

// Checkpointer is the narrow, claim-scoped payload checkpoint the processor uses to
// durably persist a freshly rendered, resealed envelope BEFORE any provider send, so
// a retry (or an at-least-once crash replay) resends the byte-identical secret rather
// than minting a new one. The transport binds it to the CURRENT claim's fence
// (execution + lease); a stale or superseded claim's checkpoint fails without
// clobbering the current generation, which the processor treats as a signal to abort
// the send. It is deliberately claim-scoped and stdlib-typed — a LOCAL structural
// interface this package declares, not an imported seam — so a jobs-mode adapter can
// satisfy it by closing over the durable claim's fenced checkpoint without this
// package importing the jobs feature. Checkpointing is executor-side and out of the
// consumer-facing sdk/capabilities/work protocol, so it stays a local declaration
// here rather than a promoted sdk contract.
type Checkpointer interface {
	// Checkpoint persists the sealed rendered payload for the current claim. A
	// fenced/stale claim returns a non-nil error and the processor MUST NOT send.
	Checkpoint(ctx context.Context, sealed []byte) error
}

// Initializer resolves an OPAQUE start command into a rendered command off the
// request path — the account resolution, challenge issuance, and rendering that must
// never run on an unauthenticated start. deliver=false is the unknown/ineligible
// identifier ("nothing to deliver"): the processor finishes the command with no send
// (OutcomeSkipped). It is invoked at most once per command that becomes deliverable;
// the processor checkpoints the rendered result so retries never re-resolve. A
// returned error is treated as transient. Discard voids the challenge minted for the
// command, called once after a recorded terminal failure (best-effort, idempotent).
type Initializer interface {
	Initialize(ctx context.Context, opaque Envelope) (rendered Envelope, deliver bool, err error)
	Discard(ctx context.Context, env Envelope) error
}

// Deliverer performs one bounded provider send of an already-rendered command. The
// processor wraps the call in a provider deadline that sits inside the claim lease
// and honors context cancellation, so a graceful shutdown returns promptly and leaves
// the command reclaimable rather than burned. A returned error is classified into a
// transient retry or a permanent failure; the error and its classification carry no
// destination or secret.
type Deliverer interface {
	Deliver(ctx context.Context, env Envelope) error
}

// Claim is what a transport hands the processor for one unit of already-claimed work.
// The processor never claims work itself: the durable jobs runtime and the bounded
// pool each own claiming, fencing, and applying the returned Result. Payload is the
// sealed envelope to Open; Attempt is the number of delivery attempts already spent
// (for retry classification); Checkpoint is the claim-scoped, fenced payload
// checkpoint the processor uses before a send.
type Claim struct {
	Payload    []byte
	Attempt    int
	Checkpoint Checkpointer
}

// Processor opens a claimed delivery command and drives it to an explicit Result —
// completed, skipped, retry-at, or permanent — without owning claiming or a polling
// loop. The concrete implementation (AV3D-2.2) is built from the narrow Initializer,
// Deliverer, and per-claim Checkpointer collaborators above and executes the
// load-bearing sequence: open the envelope; if opaque, resolve/issue/render off the
// request path; seal and successfully checkpoint the rendered envelope; call the
// provider under a bounded deadline; return the completion/retry/permanent result;
// and discard only after a recorded terminal transition. A transport ACCEPTS this
// interface so the same policy runs unchanged behind durable jobs and the bounded
// in-process runtime.
type Processor interface {
	Process(ctx context.Context, claim Claim) Result
}
