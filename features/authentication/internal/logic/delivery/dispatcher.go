package delivery

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Auth-facing delivery status vocabulary (AV3D-2.3). These are the ONLY lifecycle
// words the session-gated receipt flow ever sees — the stable, secret-free
// projection the overview's "Public-boundary direction" pins ("map generic job
// lifecycle to the existing secret-free projection"). They are deliberately the
// same strings the bespoke outbox already surfaced, so the observable receipt
// contract does not move as the transport underneath does. A status read reveals a
// lifecycle word and nothing else: never a worker name, a failure message, a
// destination, a secret, or the raw logical key.
const (
	// StatusPending: delivery is still in flight (admitted, running, or in a bounded
	// retry). The requester should keep polling.
	StatusPending = "pending"
	// StatusSucceeded: a terminal, non-failed outcome — a delivered message OR a
	// skipped unknown/ineligible identifier. The two are indistinguishable to the
	// requester by design (enumeration safety).
	StatusSucceeded = "succeeded"
	// StatusFailed: a terminal delivery failure (retry budget exhausted or permanent).
	StatusFailed = "failed"
	// StatusCanceled: a terminal cancellation — the generation was superseded by a
	// resend or explicitly canceled. Non-failed.
	StatusCanceled = "canceled"
)

// JobKind is the single generic-jobs kind an authentication delivery command is
// submitted under (phase 3): one kind, handled by the one transport-neutral
// delivery processor (command.Engine), regardless of the message rail — the rail
// (email/SMS) and the purpose travel INSIDE the sealed envelope, not as the queue's
// routing kind. A generic-jobs Dispatcher uses it to select the delivery handler.
const JobKind = "authentication.delivery"

// Dispatcher is the transport-neutral outbound seam (AV3D-2.3). It supports the
// three operations every producer needs — submit-once, replace, and latest status —
// over STDLIB TYPES only, so:
//
//   - the authentication feature core imports no jobs (or any sibling) feature; and
//   - a phase-3 composition adapter that lives OUTSIDE both features can implement
//     it and bridge each call to the sdk keyed-work protocol
//     (work.Enqueuer/work.Replacer/work.StatusReader in sdk/capabilities/work)
//     (dropping the rail/purpose params, which a generic-jobs payload already carries
//     inside its encrypted envelope, and mapping the lifecycle string).
//
// payload is the opaque sealed envelope the queue stores and never interprets; kind
// and purpose are the secret-free routing metadata; logicalKey is the PII-free
// receipt key that makes a duplicate submit idempotent and lets a resend supersede
// exactly the prior active generation. LatestStatus returns a lifecycle string drawn
// from the sdk/capabilities/work vocabulary (work.Status); the service normalizes it
// into the stable auth Status. An unknown key is sdk.ErrNotFound.
//
// Submit-once vs replace semantics are the queue's, not the caller's: Submit admits
// work under logicalKey exactly once (a second submit returns the existing active
// execution), while Replace supersedes every active generation and admits a fresh
// one.
type Dispatcher interface {
	Submit(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (executionID string, err error)
	Replace(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (executionID string, err error)
	LatestStatus(ctx context.Context, logicalKey string) (state string, err error)
}

// normalizeStatus folds a work.Status lifecycle state into the stable, secret-free
// auth Status the receipt flow consumes. It is TOTAL: every canonical
// sdk/capabilities/work state maps to a stable auth state, and an UNRECOGNIZED state
// maps SAFELY to a non-terminal pending — never a false success or a false failure,
// and never a leak of the raw string — so a future or transport-specific state a
// caller polls just keeps it polling harmlessly rather than mis-signaling a terminal
// outcome.
//
// The attempt count is deliberately NOT carried: the transport-neutral status seam
// is lifecycle-only (the sdk work.StatusReader returns a lifecycle Status and nothing
// more), and an attempt counter is executor-internal retry bookkeeping, not a stable
// lifecycle signal. Status.Attempt therefore reads 0 through this seam; the field is
// retained so the public projection type is unchanged.
func normalizeStatus(state string) Status {
	switch work.Status(state) {
	case work.StatusPending, work.StatusRunning, work.StatusFailed:
		// pending/running/failed(=retryable, rescheduled to pending) are all still in
		// flight from the requester's point of view.
		return Status{State: StatusPending, Pending: true}
	case work.StatusCompleted:
		return Status{State: StatusSucceeded}
	case work.StatusDeadLetter:
		return Status{State: StatusFailed, Failed: true}
	case work.StatusCanceled, work.StatusSuperseded:
		return Status{State: StatusCanceled}
	default:
		return Status{State: StatusPending, Pending: true}
	}
}
