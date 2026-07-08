// Package job is the durable job-queue domain of the jobs feature: the Job
// entity, the Enqueue input, and the QueueRepository outbound port a store
// adapter (features/jobs/stores/turso, the in-core memstore) or a host fills.
//
// The Job entity satisfies sdk/workers.Job and QueueRepository is a strict
// superset of sdk/workers.JobStore[Job] — both asserted at compile time below,
// so the feature's runtime drives the store through the exact kernel contract
// with no adapter layer.
package job

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// Status is the lifecycle state of a queued job.
type Status string

const (
	// StatusPending is a job waiting to be claimed.
	StatusPending Status = "pending"
	// StatusRunning is a job claimed by a worker and in flight.
	StatusRunning Status = "running"
	// StatusCompleted is a job that finished successfully.
	StatusCompleted Status = "completed"
	// StatusFailed is a job that failed but has retries remaining; it is
	// rescheduled to pending.
	StatusFailed Status = "failed"
	// StatusDeadLetter is a job that exhausted its attempts and will not retry.
	StatusDeadLetter Status = "dead_letter"
)

// Compile-time seams: the Job entity satisfies the kernel's Job constraint, and
// QueueRepository structurally satisfies the kernel's JobStore[Job] — so a
// QueueRepository is directly usable as the store a workers.Runner drives, with
// no shim. Claim's empty-queue signal is workers.ErrNoWork (see the port doc).
var (
	_ workers.Job           = Job{}
	_ workers.JobStore[Job] = (QueueRepository)(nil)
)

// Job is one durable unit of work.
//
// The id, status, and retry-count are exposed as the ID/Status/RetryCount
// methods (the sdk/workers.Job contract, whose method names collide with the
// obvious field names); the backing fields are JobID, JobStatus, and Retries so
// store adapters in sibling modules can still construct and populate a Job
// directly.
type Job struct {
	JobID         string
	Kind          string
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
}

// ID returns the job's identifier (sdk/workers.Job).
func (j Job) ID() string { return j.JobID }

// Status returns the job's lifecycle state as a string (sdk/workers.Job).
func (j Job) Status() string { return string(j.JobStatus) }

// RetryCount returns how many times the job has been retried (sdk/workers.Job).
func (j Job) RetryCount() int { return j.Retries }

// Enqueue is the input for inserting one job.
type Enqueue struct {
	// ID is optional; when set it is the idempotency key (a duplicate ID yields
	// errs.ErrAlreadyExists). The scheduler's deterministic refire relies on it.
	ID           string
	Kind         string
	Payload      json.RawMessage
	ScheduledFor time.Time // zero = now
	Priority     int
	MaxAttempts  int // zero = the Config default
}

// ListFilter narrows a QueueRepository.List query. Zero-value fields do not
// filter.
type ListFilter struct {
	Kind   string
	Status Status
}

// QueueRepository is the durable queue outbound port. A store adapter
// (features/jobs/stores/turso, the in-core memstore) or a host fills it; the
// feature core stays dialect-blind. It is a strict superset of
// sdk/workers.JobStore[Job]: Claim/Complete/Fail share the kernel's exact
// signatures so a QueueRepository is the store a workers.Runner drives directly.
type QueueRepository interface {
	// Enqueue inserts one job; a duplicate ID yields errs.ErrAlreadyExists.
	Enqueue(ctx context.Context, in Enqueue) (Job, error)
	// Claim atomically transitions exactly one due job to running for workerID
	// and returns it, or returns workers.ErrNoWork when none is due. "Due" means
	// pending with scheduled_for <= now, OR running with an expired lease
	// (stale-claim recovery). Two concurrent claimers never receive the same
	// job. Selection order is priority DESC, then created_at.
	Claim(ctx context.Context, workerID string, now time.Time) (Job, error)
	// Complete marks the job done.
	Complete(ctx context.Context, jobID string, now time.Time) error
	// Fail increments retry_count and either reschedules the job to pending or
	// dead-letters it once the attempts are exhausted. reason is the cause.
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
	// Get returns the job with the given id, or errs.ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
	// List returns a cursor-paginated page of jobs matching the filter.
	List(ctx context.Context, f ListFilter, req crud.ListRequest) (crud.Page[Job], error)
}
