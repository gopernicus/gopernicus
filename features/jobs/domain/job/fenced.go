package job

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// Compile-time seam: FencedQueueRepository is a strict superset of the kernel's
// lease-fenced store — its Claim/Complete/Fail share workers.FencedStore's exact
// signatures — so a FencedQueueRepository is directly usable as the store a
// workers.FencedRunner drives, with no shim (the QueueRepository/JobStore
// precedent). Reclaimed/superseded → sdk.ErrConflict is the fence the runner reads.
var _ workers.FencedStore[Job] = (FencedQueueRepository)(nil)

// FencedQueueRepository is the FROZEN (AV3D-0.3) extension port that hardens the
// durable queue into an at-least-once, lease-fenced, logical-key outbox — the
// generic-jobs delivery surface authentication runs its encrypted delivery work on
// in jobs mode (constitution rule 6) instead of a bespoke feature-local queue. It
// is domain
// -rich; the consumer-facing keyed-work protocol a consuming feature depends on
// without importing this package is sdk/capabilities/work (Enqueuer, Replacer,
// StatusReader), which the jobs Service implements.
//
// This interface is a SPECIFICATION frozen at phase 0. No store implements it yet:
// the memory/pgx/turso implementations and the executable conformance suite land
// in phase 1 (AV3D-1.1..1.5), and the storetest RunFencedQueue skeleton is gated
// to skip until then. The current QueueRepository is untouched and keeps driving
// the existing cron/schedule queue; the two reconcile at phase 5 migration.
//
// The concurrency and error contract (the port doc comments are the spec; the
// storetest suite is their executable form):
//
//   - Every operation is ONE atomic step. Exactly one worker claims a given due
//     job, and a late caller whose lease was reclaimed or superseded cannot
//     clobber the current execution.
//   - JobID is the UNIQUE EXECUTION ID; LogicalKey is the OPTIONAL PII-free key
//     that groups execution generations. Distinct execution IDs always retain
//     history — a superseded generation stays queryable, tombstoned terminal.
//   - Claim stamps a fresh per-claim LeaseID. Checkpoint/Complete/Fail/Reschedule
//     require the caller to present the matching LeaseID: a reclaimed or superseded
//     lease returns sdk.ErrConflict; an unknown id returns sdk.ErrNotFound.
//     Terminal completions are idempotent — Complete of an already-completed job,
//     or Fail of an already-failed job, from the last lease holder is nil.
//   - Backoff and permanent failure never busy-loop: Reschedule sets a future
//     AvailableAt (retry-at) and clears the lease; Fail dead-letters permanently.
type FencedQueueRepository interface {
	// EnqueueOnce inserts in as a new StatusPending execution UNLESS a non-terminal
	// job already holds in.LogicalKey, in which case that active job is returned
	// unchanged and no second execution is created (idempotent admission). A
	// duplicate explicit in.ID yields sdk.ErrAlreadyExists; an empty in.ID is
	// generated. An empty in.LogicalKey disables the once semantics (plain insert).
	EnqueueOnce(ctx context.Context, in Enqueue) (Job, error)

	// Replace atomically supersedes: it marks every non-terminal generation holding
	// in.LogicalKey StatusSuperseded (terminal, stamping TerminalAt and fencing any
	// live lease so a running worker's later Checkpoint/Complete/Fail fails with
	// sdk.ErrConflict) and inserts in as one fresh StatusPending execution, which it
	// returns. This is the user-requested resend: the prior active generation is
	// superseded, not left to also deliver.
	//
	// IRREDUCIBLE RACE (stated honestly, not hidden): Replace fences the superseded
	// worker's queue transitions, but it cannot retract a provider call that worker
	// already made. A provider send is an external side effect the queue never
	// observes; if the superseded worker had already handed a message to the provider
	// before Replace superseded it, that message may still be delivered even though
	// the worker's subsequent Checkpoint/Complete/Fail is rejected. Delivery is
	// therefore at-least-once across a replacement, not exactly-once — the recipient
	// may receive both the superseded and the replacement message. The freshly issued
	// challenge invalidates/replaces the older proof at the auth layer where the flow
	// supports replacement (single-use redemption is preserved); what the queue
	// guarantees is only that no superseded worker can record state against, or
	// resurrect, the retired generation.
	Replace(ctx context.Context, in Enqueue) (Job, error)

	// Claim atomically selects and leases the oldest due job (StatusPending with
	// ScheduledFor at or before now, or a running job whose LeasedUntil has passed),
	// increments Retries, stamps a fresh LeaseID plus LeasedUntil = now + leaseFor,
	// and returns it; no due job yields workers.ErrNoWork. A given job is claimed by
	// at most one concurrent caller. leaseID is the caller-supplied fresh token.
	Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (Job, error)

	// Checkpoint atomically replaces the payload of the running job id while the
	// caller still holds the current lease (leaseID equality), preserving JobID,
	// Kind, LogicalKey, Retries, ScheduledFor, and StatusRunning. The updated bytes
	// round-trip exactly (opaque ciphertext, byte-for-byte, including arbitrary
	// non-UTF8/encrypted content). It is the durable checkpoint an opaque delivery
	// records BEFORE its side effect, so every retry resends the identical rendered
	// secret. A reclaimed or superseded lease yields sdk.ErrConflict; a non-running
	// or unknown job yields sdk.ErrConflict / sdk.ErrNotFound respectively.
	//
	// CONSUMER-ORDERING GUARANTEE: this method is the seam for the checkpoint-before
	// -side-effect pattern, but the ordering is the caller's to honor. A processor
	// MUST checkpoint the rendered payload and perform its external side effect (the
	// provider send) ONLY when Checkpoint returns nil. On any error — sdk.ErrConflict
	// from a stale/superseded lease in particular — the caller must NOT proceed to
	// the side effect: the current execution belongs to another worker, so sending
	// here would emit under a retired generation. The store guarantees the fenced
	// error; the consumer guarantees it does not send past it (proven structurally by
	// the storetest CheckpointBeforeSideEffect case).
	Checkpoint(ctx context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error

	// Complete marks the leaseID-held job StatusCompleted (terminal, stamping
	// CompletedAt and TerminalAt). A reclaimed/superseded lease yields
	// sdk.ErrConflict; an already-completed job from the same holder is idempotent
	// nil; an unknown id yields sdk.ErrNotFound.
	Complete(ctx context.Context, id, leaseID string, now time.Time) error

	// Reschedule reschedules the leaseID-held job to availableAt (retry-at, no busy
	// loop), moving it back to StatusPending, clearing the lease, and recording
	// reason. A reclaimed/superseded lease or an already-terminal job yields
	// sdk.ErrConflict; an unknown id yields sdk.ErrNotFound.
	Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error

	// Fail permanently dead-letters the leaseID-held job (StatusDeadLetter, terminal,
	// stamping TerminalAt) with reason. A reclaimed/superseded lease yields
	// sdk.ErrConflict; an already-dead-lettered job from the same holder is
	// idempotent nil; an unknown id yields sdk.ErrNotFound. The resulting terminal
	// transition is what a per-kind dead-letter hook (jobs.DeadLetterFunc) fires
	// after — never before — the transition is recorded (AV3D-1.4).
	Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error

	// Cancel terminally cancels a non-terminal job by id (StatusCanceled, stamping
	// TerminalAt), independent of any lease. An already-canceled job is idempotent
	// nil; an already-completed/dead-lettered/superseded job yields sdk.ErrConflict;
	// an unknown id yields sdk.ErrNotFound.
	Cancel(ctx context.Context, id string, now time.Time) error

	// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or
	// before before and returns the number removed — bounded batching for retention
	// cleanup. It never touches a non-terminal job.
	PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error)

	// GetLatestByKey returns the most-recently-created execution holding logicalKey
	// — the deterministic latest-by-key status projection a session-gated caller
	// polls. Because Replace/Cancel leave terminal tombstones under the same key,
	// the latest generation (greatest CreatedAt, JobID DESC tiebreak) is the live
	// one. No such key yields sdk.ErrNotFound. It never leases, mutates, or resolves
	// anything, so a status read can never affect delivery.
	GetLatestByKey(ctx context.Context, logicalKey string) (Job, error)

	// Get returns the job with the given unique execution id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
}
