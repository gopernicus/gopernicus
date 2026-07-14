package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// fencedColumns is the fenced_job_queue projection, in fencedRow's field order.
// The same list backs every SELECT and every INSERT ... RETURNING, so a returned
// job carries the stored (dialect-precision) timestamps a later Get reads back —
// the returned generation and its Get are byte-identical.
const fencedColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, logical_key, lease_id, leased_until, worker_name, failure_reason, scheduled_for, claimed_at, completed_at, terminal_at, created_at, updated_at"

// Compile-time seams: the fenced store fills the frozen job.FencedQueueRepository
// port (a strict superset of the kernel's workers.FencedStore).
var (
	_ job.FencedQueueRepository    = (*FencedQueue)(nil)
	_ workers.FencedStore[job.Job] = (*FencedQueue)(nil)
)

// FencedQueue implements job.FencedQueueRepository over PostgreSQL — the hardened,
// lease-fenced, logical-key sibling of Queue backed by the fenced_job_queue table.
// Concurrency: keyed admission (EnqueueOnce/Replace) serializes per logical_key on
// a transaction-scoped advisory lock so its read-then-write is atomic even for a
// brand-new key (the uq_fenced_job_queue_active_key partial unique index is the
// backing invariant); the fenced transitions lock the target row FOR UPDATE and
// enforce the LeaseID fence, returning sdk.ErrConflict to a reclaimed or
// superseded holder. Claim is one UPDATE ... FOR UPDATE SKIP LOCKED ... RETURNING.
type FencedQueue struct {
	db *pgxdb.DB
}

// NewFencedQueueStore returns a FencedQueue backed by db. The claim lease is
// per-claim (the caller supplies leaseFor to Claim), so there is no store-level
// lease default to configure.
func NewFencedQueueStore(db *pgxdb.DB) *FencedQueue {
	return &FencedQueue{db: db}
}

// fencedRow is the store-local, db-tagged projection of a fenced_job_queue row
// pgx.RowToStructByName scans into; toDomain maps it to the domain entity. The
// nullable text/timestamp columns scan into pointers (nil == NULL).
type fencedRow struct {
	JobID         string     `db:"job_id"`
	Kind          string     `db:"kind"`
	Payload       []byte     `db:"payload"`
	Status        string     `db:"status"`
	Priority      int        `db:"priority"`
	Retries       int        `db:"retry_count"`
	MaxAttempts   int        `db:"max_attempts"`
	LogicalKey    *string    `db:"logical_key"`
	LeaseID       *string    `db:"lease_id"`
	LeasedUntil   *time.Time `db:"leased_until"`
	WorkerName    *string    `db:"worker_name"`
	FailureReason *string    `db:"failure_reason"`
	ScheduledFor  time.Time  `db:"scheduled_for"`
	ClaimedAt     *time.Time `db:"claimed_at"`
	CompletedAt   *time.Time `db:"completed_at"`
	TerminalAt    *time.Time `db:"terminal_at"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
}

func (r fencedRow) toDomain() job.Job {
	j := job.Job{
		JobID:         r.JobID,
		Kind:          r.Kind,
		Payload:       json.RawMessage(r.Payload),
		JobStatus:     job.Status(r.Status),
		Priority:      r.Priority,
		Retries:       r.Retries,
		MaxAttempts:   r.MaxAttempts,
		LogicalKey:    derefString(r.LogicalKey),
		LeaseID:       derefString(r.LeaseID),
		WorkerName:    derefString(r.WorkerName),
		FailureReason: derefString(r.FailureReason),
		ScheduledFor:  r.ScheduledFor.UTC(),
		ClaimedAt:     pgxdb.FromNullTimePtr(r.ClaimedAt),
		CompletedAt:   pgxdb.FromNullTimePtr(r.CompletedAt),
		TerminalAt:    pgxdb.FromNullTimePtr(r.TerminalAt),
		CreatedAt:     r.CreatedAt.UTC(),
		UpdatedAt:     r.UpdatedAt.UTC(),
	}
	if r.LeasedUntil != nil {
		j.LeasedUntil = r.LeasedUntil.UTC()
	}
	return j
}

// EnqueueOnce inserts in as a new pending execution unless a non-terminal job
// already holds in.LogicalKey, in which case that active job is returned unchanged
// (idempotent admission). A duplicate explicit in.ID yields sdk.ErrAlreadyExists;
// an empty in.ID is generated; an empty LogicalKey disables the once semantics.
func (q *FencedQueue) EnqueueOnce(ctx context.Context, in job.Enqueue) (job.Job, error) {
	var out job.Job
	err := q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := lockKey(ctx, tx, in.LogicalKey); err != nil {
			return err
		}
		if in.LogicalKey != "" {
			if active, ok, err := activeByKey(ctx, tx, in.LogicalKey); err != nil {
				return err
			} else if ok {
				out = active
				return nil
			}
		}
		j, err := insertFenced(ctx, tx, in)
		out = j
		return err
	})
	return out, err
}

// Replace atomically supersedes: it marks every non-terminal job holding
// in.LogicalKey StatusSuperseded (terminal, stamping terminal_at and clearing any
// live lease so a running holder is fenced) and inserts in as one fresh pending
// execution, which it returns.
func (q *FencedQueue) Replace(ctx context.Context, in job.Enqueue) (job.Job, error) {
	var out job.Job
	err := q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := lockKey(ctx, tx, in.LogicalKey); err != nil {
			return err
		}
		if in.LogicalKey != "" {
			now := time.Now().UTC()
			const supersede = `UPDATE fenced_job_queue
				SET status = 'superseded', terminal_at = @now, lease_id = NULL, leased_until = NULL, updated_at = @now
				WHERE logical_key = @key
				  AND status NOT IN ('completed','dead_letter','canceled','superseded')`
			if _, err := tx.Exec(ctx, supersede, pgx.NamedArgs{"now": now, "key": in.LogicalKey}); err != nil {
				return err
			}
		}
		j, err := insertFenced(ctx, tx, in)
		out = j
		return err
	})
	return out, err
}

// Claim atomically leases the oldest due job under the caller-supplied fresh
// leaseID for leaseFor, incrementing retry_count and returning it; no due job
// yields workers.ErrNoWork. "Due" is a pending job with scheduled_for <= now, or a
// running job whose lease has expired (leased_until <= now). FOR UPDATE SKIP
// LOCKED gives contention-free concurrent claiming.
func (q *FencedQueue) Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (job.Job, error) {
	nowUTC := now.UTC()
	until := nowUTC.Add(leaseFor)

	const claim = `UPDATE fenced_job_queue
		SET status = 'running', lease_id = @lease, leased_until = @until, claimed_at = @now,
		    retry_count = retry_count + 1, updated_at = @now
		WHERE job_id = (
			SELECT job_id FROM fenced_job_queue
			WHERE (status = 'pending' AND scheduled_for <= @now)
			   OR (status = 'running' AND leased_until <= @now)
			ORDER BY priority DESC, created_at, job_id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + fencedColumns

	row, err := pgxdb.QueryOne[fencedRow](ctx, q.db, claim, pgx.NamedArgs{"lease": leaseID, "until": until, "now": nowUTC})
	if errors.Is(err, sdk.ErrNotFound) {
		return job.Job{}, workers.ErrNoWork
	}
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// Checkpoint atomically replaces the payload of the running job id while the
// caller still holds the current lease, byte-for-byte. A reclaimed or superseded
// lease yields sdk.ErrConflict; an unknown job yields sdk.ErrNotFound.
func (q *FencedQueue) Checkpoint(ctx context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error {
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if !st.heldBy(leaseID, now) {
			return sdk.ErrConflict
		}
		const upd = `UPDATE fenced_job_queue SET payload = @payload, updated_at = @now WHERE job_id = @id`
		_, err = tx.Exec(ctx, upd, pgx.NamedArgs{"payload": payloadBytes(payload), "now": now.UTC(), "id": id})
		return err
	})
}

// Complete marks the leaseID-held job StatusCompleted (terminal). A reclaimed or
// superseded lease yields sdk.ErrConflict; an already-completed job from the same
// holder is idempotent nil; an unknown id yields sdk.ErrNotFound.
func (q *FencedQueue) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if st.status == string(job.StatusCompleted) {
			if st.leaseID == leaseID {
				return nil // idempotent from the last holder
			}
			return sdk.ErrConflict
		}
		if !st.heldBy(leaseID, now) {
			return sdk.ErrConflict
		}
		const upd = `UPDATE fenced_job_queue
			SET status = 'completed', completed_at = @now, terminal_at = @now, updated_at = @now
			WHERE job_id = @id`
		_, err = tx.Exec(ctx, upd, pgx.NamedArgs{"now": now.UTC(), "id": id})
		return err
	})
}

// Reschedule moves the leaseID-held job back to StatusPending at availableAt
// (retry-at), clearing the lease and recording reason. A reclaimed/superseded
// lease or an already-terminal job yields sdk.ErrConflict; an unknown id yields
// sdk.ErrNotFound.
func (q *FencedQueue) Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if !st.heldBy(leaseID, now) {
			return sdk.ErrConflict
		}
		const upd = `UPDATE fenced_job_queue
			SET status = 'pending', scheduled_for = @avail, lease_id = NULL, leased_until = NULL,
			    claimed_at = NULL, failure_reason = @reason, updated_at = @now
			WHERE job_id = @id`
		_, err = tx.Exec(ctx, upd, pgx.NamedArgs{"avail": availableAt.UTC(), "reason": reason, "now": now.UTC(), "id": id})
		return err
	})
}

// Fail permanently dead-letters the leaseID-held job (StatusDeadLetter, terminal)
// with reason. A reclaimed or superseded lease yields sdk.ErrConflict; an
// already-dead-lettered job from the same holder is idempotent nil; an unknown id
// yields sdk.ErrNotFound.
func (q *FencedQueue) Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error {
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if st.status == string(job.StatusDeadLetter) {
			if st.leaseID == leaseID {
				return nil // idempotent from the last holder
			}
			return sdk.ErrConflict
		}
		if !st.heldBy(leaseID, now) {
			return sdk.ErrConflict
		}
		const upd = `UPDATE fenced_job_queue
			SET status = 'dead_letter', failure_reason = @reason, terminal_at = @now, updated_at = @now
			WHERE job_id = @id`
		_, err = tx.Exec(ctx, upd, pgx.NamedArgs{"reason": reason, "now": now.UTC(), "id": id})
		return err
	})
}

// Cancel terminally cancels a non-terminal job by id (StatusCanceled), independent
// of any lease. An already-canceled job is idempotent nil; an
// already-completed/dead-lettered/superseded job yields sdk.ErrConflict; an
// unknown id yields sdk.ErrNotFound.
func (q *FencedQueue) Cancel(ctx context.Context, id string, now time.Time) error {
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if st.status == string(job.StatusCanceled) {
			return nil // idempotent
		}
		if st.terminal() {
			return sdk.ErrConflict
		}
		const upd = `UPDATE fenced_job_queue
			SET status = 'canceled', terminal_at = @now, lease_id = NULL, leased_until = NULL, updated_at = @now
			WHERE job_id = @id`
		_, err = tx.Exec(ctx, upd, pgx.NamedArgs{"now": now.UTC(), "id": id})
		return err
	})
}

// PurgeTerminal deletes up to limit terminal jobs whose terminal_at is at or
// before before and returns the number removed, never touching a non-terminal job.
// A negative limit is treated as unbounded.
func (q *FencedQueue) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit < 0 {
		limit = math.MaxInt32
	}
	const del = `DELETE FROM fenced_job_queue
		WHERE job_id IN (
			SELECT job_id FROM fenced_job_queue
			WHERE status IN ('completed','dead_letter','canceled','superseded')
			  AND terminal_at IS NOT NULL AND terminal_at <= @before
			ORDER BY terminal_at, job_id
			LIMIT @limit
		)`
	n, err := pgxdb.ExecAffecting(ctx, q.db, del, pgx.NamedArgs{"before": before.UTC(), "limit": limit})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// GetLatestByKey returns the most-recently-created execution holding logicalKey
// (greatest created_at, job_id DESC tiebreak), or sdk.ErrNotFound.
func (q *FencedQueue) GetLatestByKey(ctx context.Context, logicalKey string) (job.Job, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue
		WHERE logical_key = @key ORDER BY created_at DESC, job_id DESC LIMIT 1`
	row, err := pgxdb.QueryOne[fencedRow](ctx, q.db, query, pgx.NamedArgs{"key": logicalKey})
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// Get returns the job with the given unique execution id, or sdk.ErrNotFound.
func (q *FencedQueue) Get(ctx context.Context, id string) (job.Job, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue WHERE job_id = @id`
	row, err := pgxdb.QueryOne[fencedRow](ctx, q.db, query, pgx.NamedArgs{"id": id})
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// insertFenced inserts one fresh pending execution from in and returns the stored
// row (INSERT ... RETURNING), so the returned job's timestamps carry the column's
// dialect precision. A duplicate explicit id (or a concurrent active-key insert
// that lost the unique-index race) yields sdk.ErrAlreadyExists.
func insertFenced(ctx context.Context, tx *pgxdb.Tx, in job.Enqueue) (job.Job, error) {
	id := in.ID
	if id == "" {
		id = newID("job")
	}
	now := time.Now().UTC()
	const insert = `INSERT INTO fenced_job_queue (` + fencedColumns + `)
		VALUES (@job_id, @kind, @payload, 'pending', @priority, 0, @max_attempts, @logical_key,
		        NULL, NULL, NULL, NULL, @scheduled_for, NULL, NULL, NULL, @created_at, @updated_at)
		RETURNING ` + fencedColumns
	row, err := pgxdb.QueryOne[fencedRow](ctx, tx, insert, pgx.NamedArgs{
		"job_id":        id,
		"kind":          in.Kind,
		"payload":       payloadBytes(in.Payload),
		"priority":      in.Priority,
		"max_attempts":  in.MaxAttempts,
		"logical_key":   nullString(in.LogicalKey),
		"scheduled_for": in.ScheduledFor.UTC(),
		"created_at":    now,
		"updated_at":    now,
	})
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// activeByKey returns the single non-terminal (pending|running) job holding
// logicalKey, locking it FOR UPDATE so the caller's admission decision is atomic.
func activeByKey(ctx context.Context, tx *pgxdb.Tx, logicalKey string) (job.Job, bool, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue
		WHERE logical_key = @key AND status IN ('pending','running')
		ORDER BY created_at DESC, job_id DESC LIMIT 1 FOR UPDATE`
	row, err := pgxdb.QueryOne[fencedRow](ctx, tx, query, pgx.NamedArgs{"key": logicalKey})
	if errors.Is(err, sdk.ErrNotFound) {
		return job.Job{}, false, nil
	}
	if err != nil {
		return job.Job{}, false, err
	}
	return row.toDomain(), true, nil
}

// fencedState is the minimal row state the fenced transitions decide on: the
// status and current lease, read FOR UPDATE so the subsequent write is atomic.
type fencedState struct {
	status      string
	leaseID     string
	leasedUntil *time.Time
}

func (s fencedState) heldBy(leaseID string, now time.Time) bool {
	return s.status == string(job.StatusRunning) && s.leaseID == leaseID &&
		s.leasedUntil != nil && now.UTC().Before(*s.leasedUntil)
}

func (s fencedState) terminal() bool {
	switch job.Status(s.status) {
	case job.StatusCompleted, job.StatusDeadLetter, job.StatusCanceled, job.StatusSuperseded:
		return true
	default:
		return false
	}
}

// lockState reads and row-locks the fenced job's decision state, mapping an
// absent row to sdk.ErrNotFound.
func lockState(ctx context.Context, tx *pgxdb.Tx, id string) (fencedState, error) {
	const query = `SELECT status, COALESCE(lease_id, ''), leased_until FROM fenced_job_queue WHERE job_id = @id FOR UPDATE`
	var (
		st          fencedState
		leasedUntil *time.Time
	)
	err := tx.QueryRow(ctx, query, pgx.NamedArgs{"id": id}).Scan(&st.status, &st.leaseID, &leasedUntil)
	if err != nil {
		return fencedState{}, pgxdb.MapError(err)
	}
	st.leasedUntil = leasedUntil
	return st, nil
}

// lockKey takes a transaction-scoped advisory lock keyed on logicalKey, so keyed
// admission (EnqueueOnce/Replace) serializes its read-then-write per key even for
// a brand-new key with no row to lock. A keyless admission takes no lock.
func lockKey(ctx context.Context, tx *pgxdb.Tx, logicalKey string) error {
	if logicalKey == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(@key))`, pgx.NamedArgs{"key": logicalKey})
	return err
}

// payloadBytes returns a non-nil byte slice for the NOT NULL BYTEA column: a nil
// payload stores as an empty blob. Non-empty ciphertext (incl. non-UTF8) is stored
// verbatim and round-trips byte-for-byte.
func payloadBytes(p json.RawMessage) []byte {
	if p == nil {
		return []byte{}
	}
	return p
}

// nullString binds an empty string as SQL NULL (so keyless logical_key is NULL,
// distinct in the active-key unique index) and any other value verbatim.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// derefString reads a nullable text column back to the domain's "" absent value.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
