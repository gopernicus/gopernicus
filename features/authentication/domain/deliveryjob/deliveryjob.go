// Package deliveryjob is the durable, enumeration-safe outbound outbox domain
// (design §6.1.1). Every auth message that must not resolve an account or call a
// provider on the request path — passwordless start, contact-change codes,
// sensitive-op codes — is enqueued here as an OPAQUE job and delivered later by
// the phase-4 worker. Public unauthenticated starts therefore perform only
// normalization, rate limiting, and enqueue: known and unknown identifiers share
// one bounded request path, so provider latency can never become an enumeration
// signal.
//
// The job carries the MINIMUM PII and its whole envelope — destination, rendered
// secret/message, and the account-resolution input — lives ONLY inside the
// encrypted Payload blob (sealed through the required DeliveryEncrypter). There
// is deliberately no plaintext destination, message, or identifier column: the
// envelope value type and its seal/open live in internal/logic/delivery, and a
// store persists Payload as opaque ciphertext. Raw secrets never appear in
// LastError or any log.
//
// This package holds only the entity, its lifecycle vocabulary, and the derived
// predicates. The atomic enqueue/claim/complete/purge operations that make the
// outbox at-least-once and single-claimant are the Repository (repository.go);
// the worker that consumes them is phase-4 orchestration (internal, not here).
package deliveryjob

import "time"

// Job lifecycle states. A job is enqueued StatePending and becomes claimable when
// it is due; the worker moves it to exactly one terminal state. StatePending is
// the only non-terminal state — an in-flight claim is expressed by the lease
// fields (LeaseID/LeasedUntil), not a separate state, so a crashed worker's lease
// simply expires and the still-Pending job is reclaimable (at-least-once).
const (
	// StatePending is enqueued-and-not-yet-completed; claimable when Due.
	StatePending = "pending"
	// StateSucceeded is a terminal successful delivery.
	StateSucceeded = "succeeded"
	// StateFailed is a terminal delivery failure (retry budget exhausted or a
	// permanent error). The worker deletes the job's challenge on this transition.
	StateFailed = "failed"
	// StateCanceled is a terminal cancellation: a standalone Cancel or the prior
	// job superseded by a user-requested resend (Replace).
	StateCanceled = "canceled"
)

// Job is one queued outbound delivery. It is addressed only by its opaque,
// PII-free IdempotencyKey (a keyed digest from the IdentifierKeyer, §6.1.1): the
// same key deduplicates a double-submitted enqueue and groups the pending job a
// resend replaces. Payload is the encrypted envelope — the sole home of the
// destination, rendered message, and account-resolution input. AttemptCount is
// the number of delivery attempts (incremented on each Claim). AvailableAt is the
// due time a retry pushes forward with backoff. LeaseID/LeasedUntil name the
// current claimant and bound its lease; a due job is one whose lease is absent or
// expired. LastError is a redacted failure reason (never a raw secret). TerminalAt
// is set once the job reaches a terminal state (the purge cursor).
type Job struct {
	ID             string
	Kind           string
	Purpose        string
	IdempotencyKey string
	Payload        []byte
	State          string
	AttemptCount   int
	AvailableAt    time.Time
	LeaseID        string
	LeasedUntil    time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

// Terminal reports whether the job has reached a terminal state and will never be
// claimed again.
func (j Job) Terminal() bool {
	return j.State == StateSucceeded || j.State == StateFailed || j.State == StateCanceled
}

// Leased reports whether the job is held by a live, unexpired lease at now.
func (j Job) Leased(now time.Time) bool {
	return j.LeaseID != "" && now.Before(j.LeasedUntil)
}

// Due reports whether the job is claimable at now: pending, past its available
// time, and not held by a live lease.
func (j Job) Due(now time.Time) bool {
	return j.State == StatePending && !j.AvailableAt.After(now) && !j.Leased(now)
}
