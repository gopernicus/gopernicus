package deliveryjob

import (
	"context"
	"time"
)

// Repository persists delivery jobs as a durable, at-least-once outbox with atomic
// enqueue, claim, completion, and purge operations (design §6.1.1). Implemented by
// external host/store adapters (features/authentication/stores/turso, .../pgx) or
// any host-provided implementation (see the storetest reference); the port is
// public because those adapters live outside the feature module. The port doc
// comments are the spec; the storetest conformance suite is their executable form.
//
// The claim/complete operations are the whole concurrency surface: each is ONE
// atomic step, so exactly one worker claims a given due job and a late completer
// whose lease was reclaimed cannot clobber the new claimant. State transitions are
// idempotent — a repeated Succeed/Fail from the lease holder, or a completion of a
// job already in that terminal state, is a no-op success — because at-least-once
// delivery means a job may be reported more than once.
//
//   - Enqueue is idempotent by IdempotencyKey: if a non-terminal job already holds
//     the key it is returned unchanged (a double-submitted start makes no second
//     job); otherwise the job is inserted StatePending with its ID assigned.
//   - Replace supersedes: it atomically cancels every non-terminal job holding the
//     new job's IdempotencyKey (StateCanceled, terminal) and inserts the new
//     StatePending job. This is the user-requested resend — the earlier pending
//     job is canceled, not left to also deliver.
//   - Claim atomically selects the oldest due job (StatePending, AvailableAt at or
//     before now, lease absent or expired), increments AttemptCount, stamps
//     LeaseID/LeasedUntil, and returns it; no due job → sdk.ErrNotFound. A given
//     job is claimed by at most one concurrent caller.
//   - Succeed/Fail move a job the caller still leases to the matching terminal
//     state; Retry reschedules a leased job with backoff and clears the lease. All
//     three require LeaseID equality — a reclaimed job returns sdk.ErrConflict —
//     except an already-in-that-terminal-state completion, which is idempotent nil.
//   - Cancel terminally cancels a non-terminal job by ID (idempotent if already
//     canceled; sdk.ErrConflict if already succeeded/failed).
//   - PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or
//     before before, returning the count removed (bounded batching).
//   - GetLatestByIdempotencyKey returns the most-recently-created job holding
//     idempotencyKey — the read-only status projection a session-gated caller polls
//     with the Receipt it was handed (design §6.1.1). It is deliberately NOT part of
//     the concurrency surface: it never leases, mutates, or resolves an account, so
//     a status read can never affect delivery. Because a resend (Replace) or the
//     worker's initialize-time Replace leaves canceled tombstones under the same key,
//     the LATEST row (greatest CreatedAt) is the live one; no such key →
//     sdk.ErrNotFound. Authorization is possession of the key plus the caller's live
//     session — the key is a PII-free digest the owner received, so it names no
//     account and reveals only that owner's own delivery state.
type Repository interface {
	// Enqueue inserts job unless a non-terminal job already holds its
	// IdempotencyKey, in which case that existing job is returned unchanged.
	Enqueue(ctx context.Context, job Job) (Job, error)
	// Replace atomically cancels every non-terminal job holding job.IdempotencyKey
	// and inserts job as a fresh StatePending row, returning it.
	Replace(ctx context.Context, job Job) (Job, error)
	// Claim atomically leases and returns the oldest due job, incrementing its
	// AttemptCount and setting LeaseID=leaseID / LeasedUntil=now+leaseFor. No due
	// job → sdk.ErrNotFound.
	Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (Job, error)
	// Succeed marks the leaseID-held job StateSucceeded. Reclaimed lease →
	// sdk.ErrConflict; already succeeded → nil; unknown → sdk.ErrNotFound.
	Succeed(ctx context.Context, id, leaseID string, now time.Time) error
	// Retry reschedules the leaseID-held job to availableAt with backoff, clears
	// the lease, and records lastErr. Reclaimed lease or terminal job →
	// sdk.ErrConflict; unknown → sdk.ErrNotFound.
	Retry(ctx context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error
	// Fail marks the leaseID-held job StateFailed with lastErr. Reclaimed lease →
	// sdk.ErrConflict; already failed → nil; unknown → sdk.ErrNotFound.
	Fail(ctx context.Context, id, leaseID, lastErr string, now time.Time) error
	// Cancel terminally cancels a non-terminal job by ID. Already canceled → nil;
	// already succeeded/failed → sdk.ErrConflict; unknown → sdk.ErrNotFound.
	Cancel(ctx context.Context, id string, now time.Time) error
	// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or
	// before before and returns the number removed.
	PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error)
	// GetLatestByIdempotencyKey returns the most-recently-created job holding
	// idempotencyKey (the read-only status projection). No such key →
	// sdk.ErrNotFound. It never leases or mutates.
	GetLatestByIdempotencyKey(ctx context.Context, idempotencyKey string) (Job, error)
}

// Durability is the OPTIONAL metadata a Repository declares so a production host
// can fail closed on an in-process-only outbox (design §8). It mirrors the
// email/notify transport capability posture, inverted for the "where metadata can
// identify it" rule: a Repository that does not implement DurabilityReporter
// declares nothing and production TOLERATES it (a durable store — pgx, turso —
// need not implement this and is not asked to prove a negative). Only a Repository
// that positively identifies itself as non-durable is rejected in production.
type Durability struct {
	// InProcessOnly marks a Repository whose jobs do not survive a process restart
	// (a memory reference, a test double). Production rejects it: an outbox that
	// loses jobs on restart silently drops verification, reset, and magic-link
	// delivery — the outbox exists precisely to be durable across the request path
	// and provider latency, so a non-durable one defeats its purpose.
	InProcessOnly bool
}

// DurabilityReporter is the OPTIONAL interface a Repository implements to declare
// its Durability. A durable store need not implement it; an in-process reference
// declares InProcessOnly so a production host fails closed (design §8).
type DurabilityReporter interface {
	Durability() Durability
}
