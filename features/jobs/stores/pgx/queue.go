package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// DefaultLease is the stale-claim recovery window applied when no WithLease
// option is passed: a running job whose claimed_at is older than this is
// reclaimable by a later Claim (design §6.3, default 15m).
const DefaultLease = 15 * time.Minute

// jobColumns is the job_queue column list, in INSERT order.
const jobColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, worker_name, failure_reason, scheduled_for, claimed_at, completed_at, created_at, updated_at"

// jobSelect is the job_queue read projection, in scanJob's order. Nullable text
// columns are COALESCEd to ” so they scan into plain strings; the two nullable
// timestamps scan into *time.Time.
const jobSelect = "job_id, kind, payload, status, priority, retry_count, max_attempts, COALESCE(worker_name, ''), COALESCE(failure_reason, ''), scheduled_for, claimed_at, completed_at, created_at, updated_at"

// Compile-time seam: the Queue fills the exact job.QueueRepository port.
var _ job.QueueRepository = (*Queue)(nil)

// QueueOption configures a Queue.
type QueueOption func(*Queue)

// WithLease sets the stale-claim recovery window folded into Claim's due
// predicate: a running job whose claimed_at is older than d becomes claimable
// again (design §6.3). It is store configuration, never a Claim parameter, so
// the port signature stays identical to workers.JobStore. Non-positive values
// are ignored and the default is kept.
func WithLease(d time.Duration) QueueOption {
	return func(q *Queue) {
		if d > 0 {
			q.lease = d
		}
	}
}

// Queue implements job.QueueRepository over a PostgreSQL database. Claim is one
// UPDATE ... WHERE job_id=(SELECT ... FOR UPDATE SKIP LOCKED) ... RETURNING
// statement: SKIP LOCKED gives contention-free concurrent claiming (N workers
// each lock a different row), and the lease-expiry reclaim arm is folded into the
// due predicate.
type Queue struct {
	db    *pgxdb.DB
	lease time.Duration
}

// NewQueueStore returns a Queue backed by db, applying opts (WithLease).
func NewQueueStore(db *pgxdb.DB, opts ...QueueOption) *Queue {
	q := &Queue{db: db, lease: DefaultLease}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Enqueue inserts one pending job. A caller-supplied ID that already exists
// yields errs.ErrAlreadyExists (the idempotency key, via the primary-key unique
// violation); an empty ID is generated. The scheduled_for, priority, and
// max_attempts are stored verbatim — the store invents no defaults (that is the
// service's job).
func (q *Queue) Enqueue(ctx context.Context, in job.Enqueue) (job.Job, error) {
	id := in.ID
	if id == "" {
		id = newID("job")
	}
	now := time.Now().UTC()
	j := job.Job{
		JobID:        id,
		Kind:         in.Kind,
		Payload:      in.Payload,
		JobStatus:    job.StatusPending,
		Priority:     in.Priority,
		MaxAttempts:  in.MaxAttempts,
		ScheduledFor: in.ScheduledFor,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	const insert = `INSERT INTO job_queue (` + jobColumns + `)
		VALUES ($1, $2, $3, 'pending', $4, 0, $5, NULL, NULL, $6, NULL, NULL, $7, $8)`
	if _, err := q.db.Exec(ctx, insert,
		j.JobID, j.Kind, payloadValue(j.Payload), j.Priority, j.MaxAttempts,
		j.ScheduledFor.UTC(), j.CreatedAt, j.UpdatedAt); err != nil {
		return job.Job{}, err
	}
	return j, nil
}

// Claim atomically transitions exactly one due job to running for workerID and
// returns it, or returns workers.ErrNoWork when none is due. "Due" is a pending
// job with scheduled_for <= now, OR a running job whose lease has expired
// (claimed_at < now - lease). FOR UPDATE SKIP LOCKED locks the selected row so N
// concurrent claimers each take a different job with no contention; selection
// order is priority DESC, then created_at, with job_id as a deterministic final
// tie-break.
func (q *Queue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	nowUTC := now.UTC()
	stale := nowUTC.Add(-q.lease)

	const claim = `UPDATE job_queue
		SET status = 'running', worker_name = $1, claimed_at = $2, updated_at = $2
		WHERE job_id = (
			SELECT job_id FROM job_queue
			WHERE (status = 'pending' AND scheduled_for <= $2)
			   OR (status = 'running' AND claimed_at < $3)
			ORDER BY priority DESC, created_at, job_id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + jobSelect

	claimed, err := scanJob(q.db.QueryRow(ctx, claim, workerID, nowUTC, stale))
	if errors.Is(err, errs.ErrNotFound) {
		return job.Job{}, workers.ErrNoWork
	}
	if err != nil {
		return job.Job{}, err
	}
	return claimed, nil
}

// Complete marks the job done. A missing id yields errs.ErrNotFound.
func (q *Queue) Complete(ctx context.Context, jobID string, now time.Time) error {
	const q1 = `UPDATE job_queue SET status = 'completed', completed_at = $1, updated_at = $1 WHERE job_id = $2`
	return q.execAffecting(ctx, q1, now.UTC(), jobID)
}

// Fail increments retry_count and, in one statement, either reschedules the job
// to pending (clearing worker_name/claimed_at so it is immediately re-claimable)
// or dead-letters it once retry_count + 1 reaches maxAttempts. reason is recorded
// as the failure cause. A missing id yields errs.ErrNotFound.
func (q *Queue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	const fail = `UPDATE job_queue
		SET retry_count = retry_count + 1,
		    failure_reason = $1,
		    updated_at = $2,
		    status = CASE WHEN retry_count + 1 >= $3 THEN 'dead_letter' ELSE 'pending' END,
		    worker_name = CASE WHEN retry_count + 1 >= $3 THEN worker_name ELSE NULL END,
		    claimed_at = CASE WHEN retry_count + 1 >= $3 THEN claimed_at ELSE NULL END
		WHERE job_id = $4`
	return q.execAffecting(ctx, fail, reason, now.UTC(), maxAttempts, jobID)
}

// Get returns the job with the given id, or errs.ErrNotFound.
func (q *Queue) Get(ctx context.Context, id string) (job.Job, error) {
	const get = `SELECT ` + jobSelect + ` FROM job_queue WHERE job_id = $1`
	return scanJob(q.db.QueryRow(ctx, get, id))
}

// List returns a cursor-paginated page of jobs matching the filter, ordered by
// (created_at, job_id) descending.
func (q *Queue) List(ctx context.Context, f job.ListFilter, req crud.ListRequest) (crud.Page[job.Job], error) {
	where := "WHERE 1 = 1"
	var args []any
	if f.Kind != "" {
		args = append(args, f.Kind)
		where += fmt.Sprintf(" AND kind = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	return pgxdb.ListPage(ctx, q.db, jobSelect, "job_queue", where, args, orderField, "job_id", req,
		scanJob,
		func(j job.Job) (time.Time, string) { return j.CreatedAt, j.JobID },
	)
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to errs.ErrNotFound. Driver errors are already mapped by the connector.
func (q *Queue) execAffecting(ctx context.Context, query string, args ...any) error {
	n, err := pgxdb.ExecAffecting(ctx, q.db, query, args...)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanJob scans one job_queue row (jobSelect projection), mapping pgx.ErrNoRows
// to errs.ErrNotFound via the connector's MapError.
func scanJob(sc scanner) (job.Job, error) {
	var (
		j                      job.Job
		payload                []byte
		status                 string
		scheduledFor           time.Time
		createdAt, updatedAt   time.Time
		claimedAt, completedAt *time.Time
	)
	err := sc.Scan(
		&j.JobID, &j.Kind, &payload, &status, &j.Priority, &j.Retries, &j.MaxAttempts,
		&j.WorkerName, &j.FailureReason, &scheduledFor, &claimedAt, &completedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return job.Job{}, pgxdb.MapError(err)
	}

	j.Payload = json.RawMessage(payload)
	j.JobStatus = job.Status(status)
	j.ScheduledFor = scheduledFor.UTC()
	j.CreatedAt = createdAt.UTC()
	j.UpdatedAt = updatedAt.UTC()
	if claimedAt != nil {
		t := claimedAt.UTC()
		j.ClaimedAt = &t
	}
	if completedAt != nil {
		t := completedAt.UTC()
		j.CompletedAt = &t
	}
	return j, nil
}
