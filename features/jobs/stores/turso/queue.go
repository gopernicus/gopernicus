package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// DefaultLease is the stale-claim recovery window applied when no WithLease
// option is passed: a running job whose claimed_at is older than this is
// reclaimable by a later Claim (design §6.3, default 15m).
const DefaultLease = 15 * time.Minute

// jobColumns is the job_queue projection, in jobRow's field order.
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

// jobRow is the store-local, db-tagged projection of a job_queue row ScanStruct
// scans into; toDomain maps it to the domain entity. The nullable worker_name /
// failure_reason columns scan into sql.NullString (read back as "" when NULL) and
// the two nullable timestamps into turso.NullTime.
type jobRow struct {
	JobID         string           `db:"job_id"`
	Kind          string           `db:"kind"`
	Payload       []byte           `db:"payload"`
	Status        string           `db:"status"`
	Priority      int              `db:"priority"`
	Retries       int              `db:"retry_count"`
	MaxAttempts   int              `db:"max_attempts"`
	WorkerName    sql.NullString   `db:"worker_name"`
	FailureReason sql.NullString   `db:"failure_reason"`
	ScheduledFor  tursodb.Time     `db:"scheduled_for"`
	ClaimedAt     tursodb.NullTime `db:"claimed_at"`
	CompletedAt   tursodb.NullTime `db:"completed_at"`
	CreatedAt     tursodb.Time     `db:"created_at"`
	UpdatedAt     tursodb.Time     `db:"updated_at"`
}

func (r jobRow) toDomain() job.Job {
	return job.Job{
		JobID:         r.JobID,
		Kind:          r.Kind,
		Payload:       json.RawMessage(r.Payload),
		JobStatus:     job.Status(r.Status),
		Priority:      r.Priority,
		Retries:       r.Retries,
		MaxAttempts:   r.MaxAttempts,
		WorkerName:    r.WorkerName.String,
		FailureReason: r.FailureReason.String,
		ScheduledFor:  r.ScheduledFor.Time,
		ClaimedAt:     r.ClaimedAt.TimePtr(),
		CompletedAt:   r.CompletedAt.TimePtr(),
		CreatedAt:     r.CreatedAt.Time,
		UpdatedAt:     r.UpdatedAt.Time,
	}
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
		row, e := queryOne[jobRow](ctx, q.db, claim, workerID, nowTS, nowTS, nowTS, staleTS, nowTS, staleTS)
		if e != nil {
			return e
		}
		claimed = row.toDomain()
		return nil
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
	row, err := queryOne[jobRow](ctx, q.db, get, id)
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor- or offset-paginated page of jobs matching the filter,
// in the resolved order (default created_at DESC, job_id DESC). The Kind/Status
// filter is shared by the page query and the WithCount total.
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

	lq := tursodb.ListQuery[jobRow]{
		BaseSQL:      `SELECT ` + jobColumns + ` FROM job_queue ` + where,
		Args:         args,
		OrderFields:  job.OrderFields,
		DefaultOrder: job.DefaultOrder,
		PK:           "job_id",
		OrderValueOf: func(r jobRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r jobRow) string { return r.JobID },
	}
	page, err := tursodb.List(ctx, q.db, lq, req)
	if err != nil {
		return crud.Page[job.Job]{}, err
	}
	return crud.MapPage(page, jobRow.toDomain), nil
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to errs.ErrNotFound and retrying transient busy errors.
func (q *Queue) execAffecting(ctx context.Context, query string, args ...any) error {
	return retryBusy(ctx, func() error {
		n, err := tursodb.ExecAffecting(ctx, q.db, query, args...)
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.ErrNotFound
		}
		return nil
	})
}
