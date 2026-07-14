// Package work is the keyed-work submission protocol: submit a unit of work
// once by a PII-free logical key, optionally replace/supersede it, and read the
// deterministic latest lifecycle status back by that key.
//
// Vocabulary + contract only; NO default implementation — the oauth precedent.
// A keyed queue cannot operate without durable storage and a claim/lease
// executor, so no honest process-local default exists; the implementation of
// record is features/jobs, and any other backend supplies its own. Capabilities
// MAY ship a stdlib default, an integration implementation, or a feature
// implementation of record; this one has the last kind only.
//
// Status is the FULL frozen seven-value lifecycle vocabulary, adopted verbatim
// from the persisted job status so every bridge transports it without a mapping
// layer. Two semantics are carried in doc + predicate rather than in the strings
// themselves: StatusFailed is NON-terminal — a failed unit is retryable and
// rescheduled — while StatusDeadLetter is the permanent terminal failure.
// Terminal reports whether a status is an end state; Known reports membership in
// the canonical seven and is the totality helper a status projection asserts.
//
// The status read is lifecycle-only: it exposes where a keyed unit sits in its
// lifecycle and NEVER its payload, destination, attempt count, or any secret.
// An unknown logical key resolves to the sdk not-found error class
// (errors.Is(err, sdk.ErrNotFound)); it is never a distinct in-band status.
//
// Payload is opaque bytes the queue never interprets. It is []byte and
// deliberately NOT json.RawMessage: some producers submit ciphertext, and the
// protocol must not imply the payload is JSON.
//
// Executor-side behavior — claim, lease, checkpoint, fencing, scheduling, retry
// policy, dead-letter hooks, purge/retention — is out of this protocol; it lives
// in sdk/foundation/workers and features/jobs.
package work

import "context"

// Status is the keyed-work lifecycle vocabulary: the full frozen seven-value
// set, matching the persisted job status strings byte-for-byte.
type Status string

const (
	// StatusPending is admitted and awaiting execution.
	StatusPending Status = "pending"
	// StatusRunning is claimed and executing.
	StatusRunning Status = "running"
	// StatusCompleted is a successful terminal end state.
	StatusCompleted Status = "completed"
	// StatusFailed is retryable and NON-terminal: the unit is rescheduled.
	StatusFailed Status = "failed"
	// StatusDeadLetter is the permanent terminal failure.
	StatusDeadLetter Status = "dead_letter"
	// StatusCanceled is a terminal end state: the unit was canceled.
	StatusCanceled Status = "canceled"
	// StatusSuperseded is a terminal end state: a Replace admitted a fresh
	// generation and retired this one.
	StatusSuperseded Status = "superseded"
)

// Terminal reports whether s is an end state — completed, dead_letter, canceled,
// or superseded. StatusFailed is deliberately absent: a failed unit is retryable.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusDeadLetter, StatusCanceled, StatusSuperseded:
		return true
	default:
		return false
	}
}

// Known reports whether s is one of the canonical seven statuses. A status
// projection asserts Known to prove totality — every value it emits is in the
// frozen vocabulary.
func (s Status) Known() bool {
	switch s {
	case StatusPending, StatusRunning, StatusCompleted, StatusFailed,
		StatusDeadLetter, StatusCanceled, StatusSuperseded:
		return true
	default:
		return false
	}
}

// Enqueuer is the producer half: idempotent keyed admission. EnqueueOnce admits
// a unit under logicalKey and returns its executionID; while a unit is active
// for that key, a second EnqueueOnce with the same key returns the SAME
// executionID rather than admitting a duplicate. payload is opaque bytes the
// queue never interprets ([]byte, deliberately NOT json.RawMessage — some
// payloads are ciphertext, and the protocol must not imply JSON).
type Enqueuer interface {
	EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (executionID string, err error)
}

// Replacer is the optional atomic replace/supersede capability. It is segregated
// from Enqueuer because an implementation may honestly support keyed admission
// without replacement. Replace supersedes every active generation for logicalKey
// and admits a fresh unit, returning the new executionID.
type Replacer interface {
	Replace(ctx context.Context, kind, logicalKey string, payload []byte) (executionID string, err error)
}

// StatusReader is the status half: the deterministic latest-by-key lifecycle
// projection. An unknown key resolves to the sdk not-found error class. The read
// is lifecycle-only: never payload, destination, attempt count, or secret.
type StatusReader interface {
	LatestStatusByKey(ctx context.Context, logicalKey string) (Status, error)
}
