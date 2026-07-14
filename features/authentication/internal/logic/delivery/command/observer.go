package command

import "context"

// Transition names a point in a delivery command's lifecycle the transport may
// notify. It is a bounded enum: the set is fixed, every value is safe to log or
// project to a dashboard, and it never carries a recipient, destination, secret, or
// the raw logical key. The processor classifies (Outcome/Result); the TRANSPORT
// (durable jobs runtime or bounded pool) owns the queue transition and is the sole
// caller of an Observer, mapping each Result and each admission/purge into one of
// these transitions.
type Transition int

const (
	// TransitionAccepted: a command was admitted to the queue (submit-once or the new
	// generation of a replace). The work is now recorded; observation of it is not.
	TransitionAccepted Transition = iota
	// TransitionInitialized: an opaque start was resolved, rendered, and its encrypted
	// rendered payload checkpointed. The next attempt delivers it.
	TransitionInitialized
	// TransitionSkipped: initialization found nothing to deliver (an unknown/ineligible
	// identifier); the command terminated successfully with no send.
	TransitionSkipped
	// TransitionDelivered: the provider accepted the message and the command completed.
	TransitionDelivered
	// TransitionRetried: a transient failure rescheduled the command with bounded
	// backoff; the identical secret is resent, never a new one.
	TransitionRetried
	// TransitionDeadLettered: the retry budget was exhausted (or a permanent failure
	// occurred); the command failed terminally and its challenge is discarded.
	TransitionDeadLettered
	// TransitionSuperseded: a replace canceled this still-active command in favor of a
	// newer generation under the same logical key.
	TransitionSuperseded
	// TransitionPurged: terminal rows were deleted under the retention policy. This is a
	// batch transition carrying a Count and no single execution ID.
	TransitionPurged
)

// String renders the transition as a stable, secret-free token for events, logs,
// and tests.
func (t Transition) String() string {
	switch t {
	case TransitionAccepted:
		return "accepted"
	case TransitionInitialized:
		return "initialized"
	case TransitionSkipped:
		return "skipped"
	case TransitionDelivered:
		return "delivered"
	case TransitionRetried:
		return "retried"
	case TransitionDeadLettered:
		return "dead_lettered"
	case TransitionSuperseded:
		return "superseded"
	case TransitionPurged:
		return "purged"
	default:
		return "unknown"
	}
}

// LifecycleEvent is one observed delivery transition. Every field is safe to export
// to a metrics sink or an event bus: ExecutionID is the opaque queue/execution ID (a
// unique identifier for one unit of work, never a recipient address or the raw
// logical key), Kind and Purpose are the bounded envelope enums, Transition is the
// bounded lifecycle enum, Attempt is the delivery attempt count, and Count is the
// purged-batch size. No destination, secret, resolution input, or logical key ever
// reaches this seam.
type LifecycleEvent struct {
	ExecutionID string
	Kind        string
	Purpose     string
	Transition  Transition
	Attempt     int
	Count       int
}

// Observer receives one secret-free LifecycleEvent per delivery transition so a host
// can drive metrics, health, or a domain event rail without the transport owning any
// of those dependencies. It is OPTIONAL and observation-only: it is never on the path
// that records delivery state, so a nil Observer, a returned error, or a panic can
// never lose, retry, duplicate, or fail accepted delivery work. Because a bus may
// drop or redeliver, a downstream consumer must be idempotent — LifecycleEvent
// carries a stable ExecutionID+Transition identity precisely so it can de-duplicate.
type Observer interface {
	Observe(ctx context.Context, ev LifecycleEvent) error
}

// SafeObserve is the containment boundary the transport calls at every transition. It
// makes a nil Observer a zero-cost no-op, recovers any panic the Observer raises, and
// swallows any error it returns — so observation is strictly best-effort and can
// never propagate into the caller that is recording delivery state. It returns
// nothing: the transport must not branch on the result of observation.
func SafeObserve(ctx context.Context, obs Observer, ev LifecycleEvent) {
	if obs == nil {
		return
	}
	defer func() { _ = recover() }()
	_ = obs.Observe(ctx, ev)
}
