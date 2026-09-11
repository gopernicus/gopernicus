// Package queue owns durable job admission and worker runtimes. Its Job
// entity, the Enqueue input, and the QueueRepository outbound port a store
// adapter (pockets/jobs/stores/turso, the stdlib-only stores/memory package) or a host fills.
//
// The Job entity satisfies sdk/pkg/workers.Job (asserted at compile time
// below). QueueRepository is the pocket-specific queue port: its Claim takes the
// kinds the caller handles, so the runtime adapts it to the narrower kernel
// sdk/pkg/workers.JobStore[Job] by closing over its registered kinds.
package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Status is the lifecycle state of a queued job. It is a source-compatible ALIAS
// of the canonical keyed-work vocabulary (sdk/capabilities/work): the jobs pocket
// is the implementation of record for that protocol, so the lifecycle type has one
// source of truth rather than a duplicate definition guarded by a drift test. The
// persisted strings are byte-identical by construction.
type Status = work.Status

const (
	// StatusPending is a job waiting to be claimed.
	StatusPending = work.StatusPending
	// StatusRunning is a job claimed by a worker and in flight.
	StatusRunning = work.StatusRunning
	// StatusCompleted is a job that finished successfully.
	StatusCompleted = work.StatusCompleted
	// StatusFailed is a job that failed but has retries remaining; it is
	// rescheduled to pending.
	StatusFailed = work.StatusFailed
	// StatusDeadLetter is a job that exhausted its attempts and will not retry.
	StatusDeadLetter = work.StatusDeadLetter

	// StatusCanceled is a job terminated before completion by an explicit Cancel
	// (FencedQueueRepository.Cancel) — terminal, never claimed again. Extension
	// vocabulary (AV3D-0.3); the atomic transition lands with the fenced queue
	// implementation (AV3D-1.2). The current QueueRepository never produces it.
	StatusCanceled = work.StatusCanceled
	// StatusSuperseded is a job terminated because a newer generation replaced it
	// under the same LogicalKey (FencedQueueRepository.Replace) — terminal.
	// Extension vocabulary (AV3D-0.3); implemented at AV3D-1.2. Distinct from
	// StatusCanceled so the latest-by-key status projection can tell an
	// explicitly canceled attempt apart from one a resend replaced.
	StatusSuperseded = work.StatusSuperseded
)

// Compile-time seam: Job satisfies the generic fenced execution constraint.
// QueueRepository is NOT a kernel JobStore — its Claim carries a kinds filter —
// so the runtime wraps it with a kind-scoped adapter (see runtime.New). Claim's
// empty-queue signal is still workers.ErrNoWork (see the port doc).
var _ workers.FencedJob = Job{}

// Job is one durable unit of work.
//
// ID and RetryCount support generic SDK execution. Status exposes the stored
// lifecycle value as a string. Public backing fields let store adapters in
// sibling modules construct and populate the entity directly.
type Job struct {
	JobID string
	Kind  string
	// TenantID is the OPTIONAL host-defined boundary the job was enqueued under.
	// It is vocabulary only: the pocket attaches no semantics to it, never
	// filters or authorizes by it, and never derives it — a host sets it or does
	// not. Empty = no tenant, and stores map "" to NULL (the same empty-zero
	// convention as WorkerName/FailureReason). Its consumer is operator SQL.
	TenantID      string
	Payload       json.RawMessage
	JobStatus     Status
	Priority      int
	Retries       int
	MaxAttempts   int
	WorkerName    string
	FailureReason string
	ScheduledFor  time.Time
	ClaimedAt     *time.Time // nil until claimed; drives stale-claim recovery
	CompletedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time

	// --- fenced/keyed queue extension (AV3D-0.3; populated by the
	// FencedQueueRepository implementation from AV3D-1.x, zero on the current
	// QueueRepository so existing consumers are unaffected) ---

	// LogicalKey is the OPTIONAL PII-free idempotency/supersession key. It is
	// distinct from JobID (the unique execution ID): many execution generations
	// may share one LogicalKey over time. EnqueueOnce dedupes active work by it and
	// Replace supersedes the active generations holding it; the latest-by-key
	// status projection groups on it. Empty = the job takes part in no keyed
	// dedup/supersession.
	LogicalKey string
	// LeaseID names the CURRENT claim — a fresh per-claim token a fenced Claim
	// stamps. Checkpoint/Complete/Fail/Reschedule require the caller to present the
	// matching LeaseID, so a worker whose lease expired and was reclaimed (or whose
	// job was superseded) is fenced out with sdk.ErrConflict and cannot clobber the
	// current execution. It is distinct from WorkerName (the reusable worker
	// identity, not a per-claim fence): a worker that reclaims a job gets a NEW
	// LeaseID even under the same WorkerName.
	LeaseID string
	// LeasedUntil bounds the current lease. The job is claimable again once now is
	// at or after LeasedUntil even while LeaseID is still set (stale-claim
	// recovery), and a fence check at/after LeasedUntil fails with sdk.ErrConflict.
	LeasedUntil time.Time
	// TerminalAt is stamped when the job reaches a terminal state; it is the cursor
	// PurgeTerminal batches by. Nil while the job is non-terminal.
	TerminalAt *time.Time
}

// ID returns the job's identifier (sdk/pkg/workers.Job).
func (j Job) ID() string { return j.JobID }

// Status returns the stored lifecycle state as a string.
func (j Job) Status() string { return string(j.JobStatus) }

// RetryCount exposes the attempt count for workers.FencedJob.
func (j Job) RetryCount() int { return j.Retries }

// RetryLimit exposes the persisted ordinary-queue failure ceiling. Nonpositive
// values let the runtime use its configured default for older/direct inserts.
func (j Job) RetryLimit() int { return j.MaxAttempts }

// QueueDeferrer optionally supports temporary processing gates. Defer releases a
// running job to a future time without incrementing Retries. Prior failures and
// payload are preserved. The ordinary queue has no per-claim ownership token.
type QueueDeferrer interface {
	Defer(ctx context.Context, id string, availableAt time.Time, reason string, now time.Time) error
}

// Terminal reports whether the job has reached a terminal state and will never be
// claimed again: completed, dead-lettered, canceled, or superseded. Extension
// predicate (AV3D-0.3) — StatusCanceled/StatusSuperseded are only produced by the
// FencedQueueRepository, so on the current queue this is completed-or-dead-letter.
func (j Job) Terminal() bool { return j.JobStatus.Terminal() }

// Leased reports whether the job is held by a live, unexpired lease at now — the
// LeaseID/LeasedUntil fence the fenced queue uses (AV3D-0.3). A job that is not
// Leased at now is reclaimable, and a fence-checked operation from a stale lease
// fails with sdk.ErrConflict.
func (j Job) Leased(now time.Time) bool {
	return j.LeaseID != "" && now.Before(j.LeasedUntil)
}

// Enqueue is the input for inserting one job.
type Enqueue struct {
	// ID is optional; when set it is the UNIQUE EXECUTION key (a duplicate ID yields
	// sdk.ErrAlreadyExists). The scheduler's deterministic refire relies on it.
	ID   string
	Kind string
	// TenantID is the OPTIONAL host-defined boundary to stamp on the job (see
	// Job.TenantID). Empty = no tenant.
	TenantID string
	// Payload must be valid JSON for the ordinary queue. The public service
	// normalizes empty input to {}. The fenced queue instead accepts opaque bytes.
	Payload      json.RawMessage
	ScheduledFor time.Time // zero = now
	Priority     int
	MaxAttempts  int // zero = the Config default

	// LogicalKey is the OPTIONAL PII-free key EnqueueOnce dedupes and Replace
	// supersedes active work by (AV3D-0.3 extension), distinct from ID (the unique
	// execution key). Empty = no keyed dedup/supersession; the current
	// QueueRepository.Enqueue ignores it, so it is inert until the fenced queue
	// (AV3D-1.2) consumes it.
	LogicalKey string
}

// ListFilter narrows a QueueRepository.List query. Zero-value fields do not
// filter.
type ListFilter struct {
	Kind   string
	Status Status
}

// QueueRepository is the durable queue outbound port. A store adapter
// (pockets/jobs/stores/turso, the stdlib-only stores/memory package) or a host fills it; the
// pocket core stays dialect-blind. Complete/Fail share the kernel
// sdk/pkg/workers.JobStore[Job] signatures; Claim adds a trailing kinds
// filter, and the runtime adapts the port to the kernel by closing over the
// kinds it registered handlers for.
type QueueRepository interface {
	// Enqueue inserts one job; a duplicate ID yields sdk.ErrAlreadyExists. Callers
	// provide valid JSON payloads; the public service validates and supplies {} for
	// empty input before calling this repository.
	Enqueue(ctx context.Context, in Enqueue) (Job, error)
	// Claim atomically transitions exactly one due job to running for workerID
	// and returns it, or returns workers.ErrNoWork when none is due. "Due" means
	// pending with scheduled_for <= now, OR running with an expired lease
	// (stale-claim recovery). Two concurrent claimers never receive the same
	// job. Selection order is priority DESC, then created_at.
	//
	// kinds restricts the selection to jobs of those kinds — BOTH the pending
	// arm and the stale-reclaim arm — so a runtime never claims (and never
	// charges an attempt to) a job it has no handler for; a job of another kind
	// simply waits for the binary that owns it. A nil/empty kinds applies no
	// filter. Selection order is unchanged within the filtered set.
	Claim(ctx context.Context, workerID string, now time.Time, kinds []string) (Job, error)
	// Complete marks the job done. Repeated completion is an unchanged success;
	// another terminal state returns sdk.ErrConflict.
	Complete(ctx context.Context, jobID string, now time.Time) error
	// Fail increments retry_count and either reschedules the job to pending or
	// dead-letters it once the attempts are exhausted. reason is the cause.
	// An already-dead-lettered job is an unchanged success; another terminal
	// state returns sdk.ErrConflict. These state guards are not lease fencing.
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
	// Get returns the job with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
	// List returns a cursor-paginated page of jobs matching the filter.
	List(ctx context.Context, f ListFilter, req list.Request) (list.Page[Job], error)
}
