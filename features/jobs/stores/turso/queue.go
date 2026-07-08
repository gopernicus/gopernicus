package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/logic/job"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// DefaultLease is the stale-claim recovery window applied when no WithLease
// option is passed: a running job whose claimed_at is older than this is
// reclaimable by a later Claim (design §6.3, default 15m).
const DefaultLease = 15 * time.Minute

// jobColumns is the job_queue projection, in scanJob's order.
const jobColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, worker_name, failure_reason, scheduled_for, claimed_at, completed_at, created_at, updated_at"

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

// Queue implements job.QueueRepository over a libSQL database. Claim is a single
// UPDATE ... WHERE job_id=(SELECT ... LIMIT 1) ... RETURNING statement: SQLite's
// single-writer model serializes the whole statement so the subquery evaluates
// against committed state and double-claim is impossible. The real hazard is
// SQLITE_BUSY under concurrent writers, handled inside the adapter by retryBusy.
type Queue struct {
	db    *tursodb.DB
	lease time.Duration
}

// NewQueueStore returns a Queue backed by db, applying opts (WithLease). It sets
// busy_timeout on the connection best-effort; the bounded retry loop is the real
// contention defense.
func NewQueueStore(db *tursodb.DB, opts ...QueueOption) *Queue {
	q := &Queue{db: db, lease: DefaultLease}
	for _, opt := range opts {
		opt(q)
	}
	_, _ = db.Exec(context.Background(), "PRAGMA busy_timeout = 5000")
	return q
}

// Enqueue inserts one pending job. A caller-supplied ID that already exists
// yields errs.ErrAlreadyExists (the idempotency key); an empty ID is generated.
// The scheduled_for, priority, and max_attempts are stored verbatim — the store
// invents no defaults (that is the service's job).
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
		VALUES (?, ?, ?, 'pending', ?, 0, ?, NULL, NULL, ?, NULL, NULL, ?, ?)`
	err := retryBusy(ctx, func() error {
		_, e := q.db.Exec(ctx, insert,
			j.JobID, j.Kind, payloadValue(j.Payload), j.Priority, j.MaxAttempts,
			tursodb.FormatTime(j.ScheduledFor), tursodb.FormatTime(j.CreatedAt), tursodb.FormatTime(j.UpdatedAt))
		return e
	})
	if err != nil {
		return job.Job{}, err
	}
	return j, nil
}

// Claim atomically transitions exactly one due job to running for workerID and
// returns it, or returns workers.ErrNoWork when none is due. "Due" is a pending
// job with scheduled_for <= now, OR a running job whose lease has expired
// (claimed_at < now - lease). The status predicate is repeated in the outer
// WHERE (SQLite has no FOR UPDATE SKIP LOCKED); selection order is priority DESC,
// then created_at, with job_id as a deterministic final tie-break.
func (q *Queue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	nowTS := tursodb.FormatTime(now.UTC())
	staleTS := tursodb.FormatTime(now.UTC().Add(-q.lease))

	const claim = `UPDATE job_queue
		SET status = 'running', worker_name = ?, claimed_at = ?, updated_at = ?
		WHERE job_id = (
			SELECT job_id FROM job_queue
			WHERE (status = 'pending' AND scheduled_for <= ?)
			   OR (status = 'running' AND claimed_at < ?)
			ORDER BY priority DESC, created_at, job_id
			LIMIT 1
		)
		  AND (
			(status = 'pending' AND scheduled_for <= ?)
			OR (status = 'running' AND claimed_at < ?)
		  )
		RETURNING ` + jobColumns

	var claimed job.Job
	err := retryBusy(ctx, func() error {
		var e error
		claimed, e = scanJob(q.db.QueryRow(ctx, claim, workerID, nowTS, nowTS, nowTS, staleTS, nowTS, staleTS))
		return e
	})
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
	ts := tursodb.FormatTime(now.UTC())
	const q1 = `UPDATE job_queue SET status = 'completed', completed_at = ?, updated_at = ? WHERE job_id = ?`
	return q.execAffecting(ctx, q1, ts, ts, jobID)
}

// Fail increments retry_count and, in one statement, either reschedules the job
// to pending (clearing worker_name/claimed_at so it is immediately re-claimable)
// or dead-letters it once retry_count + 1 reaches maxAttempts. reason is recorded
// as the failure cause. A missing id yields errs.ErrNotFound.
func (q *Queue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	ts := tursodb.FormatTime(now.UTC())
	const fail = `UPDATE job_queue
		SET retry_count = retry_count + 1,
		    failure_reason = ?,
		    updated_at = ?,
		    status = CASE WHEN retry_count + 1 >= ? THEN 'dead_letter' ELSE 'pending' END,
		    worker_name = CASE WHEN retry_count + 1 >= ? THEN worker_name ELSE NULL END,
		    claimed_at = CASE WHEN retry_count + 1 >= ? THEN claimed_at ELSE NULL END
		WHERE job_id = ?`
	return q.execAffecting(ctx, fail, reason, ts, maxAttempts, maxAttempts, maxAttempts, jobID)
}

// Get returns the job with the given id, or errs.ErrNotFound.
func (q *Queue) Get(ctx context.Context, id string) (job.Job, error) {
	const get = `SELECT ` + jobColumns + ` FROM job_queue WHERE job_id = ?`
	return scanJob(q.db.QueryRow(ctx, get, id))
}

// List returns a cursor-paginated page of jobs matching the filter, ordered by
// (created_at, job_id) descending.
func (q *Queue) List(ctx context.Context, f job.ListFilter, req crud.ListRequest) (crud.Page[job.Job], error) {
	where := "WHERE 1 = 1"
	var args []any
	if f.Kind != "" {
		where += " AND kind = ?"
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, string(f.Status))
	}

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[job.Job]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		ts := tursodb.FormatTime(cv)
		where += " AND ((created_at < ?) OR (created_at = ? AND job_id < ?))"
		args = append(args, ts, ts, cur.PK)
	}

	limit := req.NormalizedLimit()
	query := `SELECT ` + jobColumns + ` FROM job_queue ` + where + ` ORDER BY created_at DESC, job_id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := q.db.Query(ctx, query, args...)
	if err != nil {
		return crud.Page[job.Job]{}, err
	}
	defer rows.Close()

	var items []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return crud.Page[job.Job]{}, err
		}
		items = append(items, j)
	}
	if err := rows.Err(); err != nil {
		return crud.Page[job.Job]{}, tursodb.MapError(err)
	}

	return crud.TrimPage(items, limit, func(j job.Job) (string, error) {
		return crud.EncodeCursor(orderField, j.CreatedAt, j.JobID)
	})
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to errs.ErrNotFound and retrying transient busy errors.
func (q *Queue) execAffecting(ctx context.Context, query string, args ...any) error {
	return retryBusy(ctx, func() error {
		res, err := q.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.ErrNotFound
		}
		return nil
	})
}

// scanJob scans one job_queue row, mapping sql.ErrNoRows to errs.ErrNotFound.
func scanJob(sc scanner) (job.Job, error) {
	var (
		j                                  job.Job
		status, payload                    string
		scheduledFor, createdAt, updatedAt string
		workerName, failureReason          sql.NullString
		claimedAt, completedAt             sql.NullString
	)
	err := sc.Scan(
		&j.JobID, &j.Kind, &payload, &status, &j.Priority, &j.Retries, &j.MaxAttempts,
		&workerName, &failureReason, &scheduledFor, &claimedAt, &completedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return job.Job{}, tursodb.MapError(err)
	}

	j.Payload = json.RawMessage(payload)
	j.JobStatus = job.Status(status)
	j.WorkerName = workerName.String
	j.FailureReason = failureReason.String

	if j.ScheduledFor, err = tursodb.ParseTime(scheduledFor); err != nil {
		return job.Job{}, err
	}
	if j.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return job.Job{}, err
	}
	if j.UpdatedAt, err = tursodb.ParseTime(updatedAt); err != nil {
		return job.Job{}, err
	}
	if claimedAt.Valid && claimedAt.String != "" {
		t, err := tursodb.ParseTime(claimedAt.String)
		if err != nil {
			return job.Job{}, err
		}
		j.ClaimedAt = &t
	}
	if completedAt.Valid && completedAt.String != "" {
		t, err := tursodb.ParseTime(completedAt.String)
		if err != nil {
			return job.Job{}, err
		}
		j.CompletedAt = &t
	}
	return j, nil
}
