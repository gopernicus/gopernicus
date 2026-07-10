package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// DefaultLease is the stale-claim recovery window applied when no WithLease
// option is passed: a running job whose claimed_at is older than this is
// reclaimable by a later Claim (design §6.3, default 15m).
const DefaultLease = 15 * time.Minute

// jobColumns is the job_queue column list, in Enqueue's INSERT order.
const jobColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, worker_name, failure_reason, scheduled_for, claimed_at, completed_at, created_at, updated_at"

// jobSelect is the job_queue read projection Claim's RETURNING scans positionally
// via scanJob. Nullable text columns are COALESCEd to ” so they scan into plain
// strings; the two nullable timestamps scan into *time.Time.
const jobSelect = "job_id, kind, payload, status, priority, retry_count, max_attempts, COALESCE(worker_name, ''), COALESCE(failure_reason, ''), scheduled_for, claimed_at, completed_at, created_at, updated_at"

// jobRowColumns is the struct-scan projection for the NamedArgs read paths (Get,
// List): every column is name-aliased so pgx.RowToStructByName matches it against
// jobRow's db tags, with nullable text COALESCEd to ”.
const jobRowColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, COALESCE(worker_name, '') AS worker_name, COALESCE(failure_reason, '') AS failure_reason, scheduled_for, claimed_at, completed_at, created_at, updated_at"

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

// jobRow is the store-local, db-tagged projection of a job_queue row that
// pgx.RowToStructByName scans into; toDomain maps it to the domain entity.
type jobRow struct {
	JobID         string     `db:"job_id"`
	Kind          string     `db:"kind"`
	Payload       []byte     `db:"payload"`
	Status        string     `db:"status"`
	Priority      int        `db:"priority"`
	Retries       int        `db:"retry_count"`
	MaxAttempts   int        `db:"max_attempts"`
	WorkerName    string     `db:"worker_name"`
	FailureReason string     `db:"failure_reason"`
	ScheduledFor  time.Time  `db:"scheduled_for"`
	ClaimedAt     *time.Time `db:"claimed_at"`
	CompletedAt   *time.Time `db:"completed_at"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
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
		WorkerName:    r.WorkerName,
		FailureReason: r.FailureReason,
		ScheduledFor:  r.ScheduledFor.UTC(),
		ClaimedAt:     pgxdb.FromNullTimePtr(r.ClaimedAt),
		CompletedAt:   pgxdb.FromNullTimePtr(r.CompletedAt),
		CreatedAt:     r.CreatedAt.UTC(),
		UpdatedAt:     r.UpdatedAt.UTC(),
	}
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
// yields sdk.ErrAlreadyExists (the idempotency key, via the primary-key unique
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
		VALUES (@job_id, @kind, @payload, 'pending', @priority, 0, @max_attempts, NULL, NULL, @scheduled_for, NULL, NULL, @created_at, @updated_at)`
	if _, err := q.db.Exec(ctx, insert, pgx.NamedArgs{
		"job_id":        j.JobID,
		"kind":          j.Kind,
		"payload":       payloadValue(j.Payload),
		"priority":      j.Priority,
		"max_attempts":  j.MaxAttempts,
		"scheduled_for": j.ScheduledFor.UTC(),
		"created_at":    j.CreatedAt,
		"updated_at":    j.UpdatedAt,
	}); err != nil {
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
//
// The claim statement and its positional scan are preserved VERBATIM from before
// the pgx idiom sweep (pgx-crud-v1 P5 directive): SKIP LOCKED correctness is
// load-bearing, so its args stay positional rather than risk any observable
// change from a NamedArgs rewrite.
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
	if errors.Is(err, sdk.ErrNotFound) {
		return job.Job{}, workers.ErrNoWork
	}
	if err != nil {
		return job.Job{}, err
	}
	return claimed, nil
}

// Complete marks the job done. A missing id yields sdk.ErrNotFound.
func (q *Queue) Complete(ctx context.Context, jobID string, now time.Time) error {
	const q1 = `UPDATE job_queue SET status = 'completed', completed_at = @now, updated_at = @now WHERE job_id = @job_id`
	return q.execAffecting(ctx, q1, pgx.NamedArgs{"now": now.UTC(), "job_id": jobID})
}

// Fail increments retry_count and, in one statement, either reschedules the job
// to pending (clearing worker_name/claimed_at so it is immediately re-claimable)
// or dead-letters it once retry_count + 1 reaches maxAttempts. reason is recorded
// as the failure cause. A missing id yields sdk.ErrNotFound.
func (q *Queue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	const fail = `UPDATE job_queue
		SET retry_count = retry_count + 1,
		    failure_reason = @reason,
		    updated_at = @now,
		    status = CASE WHEN retry_count + 1 >= @max THEN 'dead_letter' ELSE 'pending' END,
		    worker_name = CASE WHEN retry_count + 1 >= @max THEN worker_name ELSE NULL END,
		    claimed_at = CASE WHEN retry_count + 1 >= @max THEN claimed_at ELSE NULL END
		WHERE job_id = @job_id`
	return q.execAffecting(ctx, fail, pgx.NamedArgs{"reason": reason, "now": now.UTC(), "max": maxAttempts, "job_id": jobID})
}

// Get returns the job with the given id, or sdk.ErrNotFound.
func (q *Queue) Get(ctx context.Context, id string) (job.Job, error) {
	const get = `SELECT ` + jobRowColumns + ` FROM job_queue WHERE job_id = @job_id`
	row, err := queryOne[jobRow](ctx, q.db, get, pgx.NamedArgs{"job_id": id})
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor- or offset-paginated page of jobs matching the filter,
// in the resolved order (default created_at DESC, job_id DESC). The Kind/Status
// filter is shared by the page query and the WithCount total.
func (q *Queue) List(ctx context.Context, f job.ListFilter, req crud.ListRequest) (crud.Page[job.Job], error) {
	where, args := jobFilter(f)
	lq := pgxdb.ListQuery[jobRow]{
		BaseSQL:      `SELECT ` + jobRowColumns + ` FROM job_queue` + where,
		Args:         args,
		OrderFields:  job.OrderFields,
		DefaultOrder: job.DefaultOrder,
		PK:           "job_id",
		OrderValueOf: func(r jobRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r jobRow) string { return r.JobID },
	}
	page, err := pgxdb.List(ctx, q.db, lq, req)
	if err != nil {
		return crud.Page[job.Job]{}, err
	}
	return crud.MapPage(page, jobRow.toDomain), nil
}

// jobFilter composes the optional Kind and Status filters into a parameterized
// WHERE fragment and its NamedArgs — never string concatenation of values. The
// list helper's keyset builder appends its predicate with AND; the count wrap
// reuses the same fragment and args, so the total respects the filter.
func jobFilter(f job.ListFilter) (string, pgx.NamedArgs) {
	where := " WHERE 1 = 1"
	args := pgx.NamedArgs{}
	if f.Kind != "" {
		where += " AND kind = @kind"
		args["kind"] = f.Kind
	}
	if f.Status != "" {
		where += " AND status = @status"
		args["status"] = string(f.Status)
	}
	return where, args
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to sdk.ErrNotFound. Driver errors are already mapped by the connector.
func (q *Queue) execAffecting(ctx context.Context, query string, args pgx.NamedArgs) error {
	n, err := pgxdb.ExecAffecting(ctx, q.db, query, args)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// scanJob scans one job_queue row (jobSelect projection) positionally, mapping
// pgx.ErrNoRows to sdk.ErrNotFound via the connector's MapError. It backs Claim's
// preserved RETURNING statement.
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
