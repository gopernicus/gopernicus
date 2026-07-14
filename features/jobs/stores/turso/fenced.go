package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// fencedColumns is the fenced_job_queue projection, in fencedRow's field order.
// The same list backs every SELECT and every INSERT ... RETURNING, so a returned
// job carries the stored timestamps a later Get reads back.
const fencedColumns = "job_id, kind, payload, status, priority, retry_count, max_attempts, logical_key, lease_id, leased_until, worker_name, failure_reason, scheduled_for, claimed_at, completed_at, terminal_at, created_at, updated_at"

// Compile-time seams: the fenced store fills the frozen job.FencedQueueRepository
// port (a strict superset of the kernel's workers.FencedStore).
var (
	_ job.FencedQueueRepository    = (*FencedQueue)(nil)
	_ workers.FencedStore[job.Job] = (*FencedQueue)(nil)
)

// FencedQueue implements job.FencedQueueRepository over a libSQL database — the
// hardened, lease-fenced, logical-key sibling of Queue backed by the
// fenced_job_queue table. Concurrency: keyed admission and the fenced transitions
// run inside a BEGIN IMMEDIATE transaction (InTx), which takes the write lock up
// front so SQLite's single-writer model serializes contending operations and each
// read-then-write is atomic; contention surfaces as bounded busy-retry waiting,
// never a failed operation. The uq_fenced_job_queue_active_key partial unique
// index is the backing invariant for at-most-one active generation per key.
type FencedQueue struct {
	db *tursodb.DB
}

// NewFencedQueueStore returns a FencedQueue backed by db. The claim lease is
// per-claim (the caller supplies leaseFor to Claim), so there is no store-level
// lease default to configure. It sets busy_timeout best-effort; the bounded retry
// loop is the real contention defense.
func NewFencedQueueStore(db *tursodb.DB) *FencedQueue {
	_, _ = db.Exec(context.Background(), "PRAGMA busy_timeout = 5000")
	return &FencedQueue{db: db}
}

// fencedRow is the store-local, db-tagged projection of a fenced_job_queue row
// ScanStruct scans into; toDomain maps it to the domain entity. Nullable text
// columns scan into sql.NullString, nullable timestamps into tursodb.NullTime, and
// the BLOB payload into []byte (byte-for-byte).
type fencedRow struct {
	JobID         string           `db:"job_id"`
	Kind          string           `db:"kind"`
	Payload       []byte           `db:"payload"`
	Status        string           `db:"status"`
	Priority      int              `db:"priority"`
	Retries       int              `db:"retry_count"`
	MaxAttempts   int              `db:"max_attempts"`
	LogicalKey    sql.NullString   `db:"logical_key"`
	LeaseID       sql.NullString   `db:"lease_id"`
	LeasedUntil   tursodb.NullTime `db:"leased_until"`
	WorkerName    sql.NullString   `db:"worker_name"`
	FailureReason sql.NullString   `db:"failure_reason"`
	ScheduledFor  tursodb.Time     `db:"scheduled_for"`
	ClaimedAt     tursodb.NullTime `db:"claimed_at"`
	CompletedAt   tursodb.NullTime `db:"completed_at"`
	TerminalAt    tursodb.NullTime `db:"terminal_at"`
	CreatedAt     tursodb.Time     `db:"created_at"`
	UpdatedAt     tursodb.Time     `db:"updated_at"`
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
		LogicalKey:    r.LogicalKey.String,
		LeaseID:       r.LeaseID.String,
		WorkerName:    r.WorkerName.String,
		FailureReason: r.FailureReason.String,
		ScheduledFor:  r.ScheduledFor.Time,
		ClaimedAt:     r.ClaimedAt.TimePtr(),
		CompletedAt:   r.CompletedAt.TimePtr(),
		TerminalAt:    r.TerminalAt.TimePtr(),
		CreatedAt:     r.CreatedAt.Time,
		UpdatedAt:     r.UpdatedAt.Time,
	}
	if r.LeasedUntil.Valid {
		j.LeasedUntil = r.LeasedUntil.Time
	}
	return j
}

// EnqueueOnce inserts in as a new pending execution unless a non-terminal job
// already holds in.LogicalKey, in which case that active job is returned unchanged
// (idempotent admission). A duplicate explicit in.ID yields sdk.ErrAlreadyExists;
// an empty in.ID is generated; an empty LogicalKey disables the once semantics.
func (q *FencedQueue) EnqueueOnce(ctx context.Context, in job.Enqueue) (job.Job, error) {
	var out job.Job
	err := retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
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
	})
	return out, err
}

// Replace atomically supersedes: it marks every non-terminal job holding
// in.LogicalKey StatusSuperseded (terminal, stamping terminal_at and clearing any
// live lease so a running holder is fenced) and inserts in as one fresh pending
// execution, which it returns.
func (q *FencedQueue) Replace(ctx context.Context, in job.Enqueue) (job.Job, error) {
	var out job.Job
	err := retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			if in.LogicalKey != "" {
				now := tursodb.FormatTime(time.Now().UTC())
				const supersede = `UPDATE fenced_job_queue
					SET status = 'superseded', terminal_at = ?, lease_id = NULL, leased_until = NULL, updated_at = ?
					WHERE logical_key = ?
					  AND status NOT IN ('completed','dead_letter','canceled','superseded')`
				if _, err := tx.Exec(ctx, supersede, now, now, in.LogicalKey); err != nil {
					return err
				}
			}
			j, err := insertFenced(ctx, tx, in)
			out = j
			return err
		})
	})
	return out, err
}

// Claim atomically leases the oldest due job under the caller-supplied fresh
// leaseID for leaseFor, incrementing retry_count and returning it; no due job
// yields workers.ErrNoWork. "Due" is a pending job with scheduled_for <= now, or a
// running job whose lease has expired (leased_until <= now). The status predicate
// is repeated in the outer WHERE (SQLite has no FOR UPDATE SKIP LOCKED).
func (q *FencedQueue) Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (job.Job, error) {
	nowTS := tursodb.FormatTime(now.UTC())
	untilTS := tursodb.FormatTime(now.UTC().Add(leaseFor))

	const claim = `UPDATE fenced_job_queue
		SET status = 'running', lease_id = ?, leased_until = ?, claimed_at = ?,
		    retry_count = retry_count + 1, updated_at = ?
		WHERE job_id = (
			SELECT job_id FROM fenced_job_queue
			WHERE (status = 'pending' AND scheduled_for <= ?)
			   OR (status = 'running' AND leased_until <= ?)
			ORDER BY priority DESC, created_at, job_id
			LIMIT 1
		)
		  AND (
			(status = 'pending' AND scheduled_for <= ?)
			OR (status = 'running' AND leased_until <= ?)
		  )
		RETURNING ` + fencedColumns

	var claimed job.Job
	err := retryBusy(ctx, func() error {
		row, e := tursodb.QueryOne[fencedRow](ctx, q.db, claim, leaseID, untilTS, nowTS, nowTS, nowTS, nowTS, nowTS, nowTS)
		if e != nil {
			return e
		}
		claimed = row.toDomain()
		return nil
	})
	if errors.Is(err, sdk.ErrNotFound) {
		return job.Job{}, workers.ErrNoWork
	}
	if err != nil {
		return job.Job{}, err
	}
	return claimed, nil
}

// Checkpoint atomically replaces the payload of the running job id while the
// caller still holds the current lease, byte-for-byte. A reclaimed or superseded
// lease yields sdk.ErrConflict; an unknown job yields sdk.ErrNotFound.
func (q *FencedQueue) Checkpoint(ctx context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error {
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			st, err := readState(ctx, tx, id)
			if err != nil {
				return err
			}
			if !st.heldBy(leaseID, now) {
				return sdk.ErrConflict
			}
			const upd = `UPDATE fenced_job_queue SET payload = ?, updated_at = ? WHERE job_id = ?`
			_, err = tx.Exec(ctx, upd, payloadBytes(payload), tursodb.FormatTime(now.UTC()), id)
			return err
		})
	})
}

// Complete marks the leaseID-held job StatusCompleted (terminal). A reclaimed or
// superseded lease yields sdk.ErrConflict; an already-completed job from the same
// holder is idempotent nil; an unknown id yields sdk.ErrNotFound.
func (q *FencedQueue) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			st, err := readState(ctx, tx, id)
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
			ts := tursodb.FormatTime(now.UTC())
			const upd = `UPDATE fenced_job_queue
				SET status = 'completed', completed_at = ?, terminal_at = ?, updated_at = ? WHERE job_id = ?`
			_, err = tx.Exec(ctx, upd, ts, ts, ts, id)
			return err
		})
	})
}

// Reschedule moves the leaseID-held job back to StatusPending at availableAt
// (retry-at), clearing the lease and recording reason. A reclaimed/superseded
// lease or an already-terminal job yields sdk.ErrConflict; an unknown id yields
// sdk.ErrNotFound.
func (q *FencedQueue) Reschedule(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			st, err := readState(ctx, tx, id)
			if err != nil {
				return err
			}
			if !st.heldBy(leaseID, now) {
				return sdk.ErrConflict
			}
			const upd = `UPDATE fenced_job_queue
				SET status = 'pending', scheduled_for = ?, lease_id = NULL, leased_until = NULL,
				    claimed_at = NULL, failure_reason = ?, updated_at = ? WHERE job_id = ?`
			_, err = tx.Exec(ctx, upd, tursodb.FormatTime(availableAt.UTC()), reason, tursodb.FormatTime(now.UTC()), id)
			return err
		})
	})
}

// Fail permanently dead-letters the leaseID-held job (StatusDeadLetter, terminal)
// with reason. A reclaimed or superseded lease yields sdk.ErrConflict; an
// already-dead-lettered job from the same holder is idempotent nil; an unknown id
// yields sdk.ErrNotFound.
func (q *FencedQueue) Fail(ctx context.Context, id, leaseID, reason string, now time.Time) error {
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			st, err := readState(ctx, tx, id)
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
			ts := tursodb.FormatTime(now.UTC())
			const upd = `UPDATE fenced_job_queue
				SET status = 'dead_letter', failure_reason = ?, terminal_at = ?, updated_at = ? WHERE job_id = ?`
			_, err = tx.Exec(ctx, upd, reason, ts, ts, id)
			return err
		})
	})
}

// Cancel terminally cancels a non-terminal job by id (StatusCanceled), independent
// of any lease. An already-canceled job is idempotent nil; an
// already-completed/dead-lettered/superseded job yields sdk.ErrConflict; an
// unknown id yields sdk.ErrNotFound.
func (q *FencedQueue) Cancel(ctx context.Context, id string, now time.Time) error {
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			st, err := readState(ctx, tx, id)
			if err != nil {
				return err
			}
			if st.status == string(job.StatusCanceled) {
				return nil // idempotent
			}
			if st.terminal() {
				return sdk.ErrConflict
			}
			ts := tursodb.FormatTime(now.UTC())
			const upd = `UPDATE fenced_job_queue
				SET status = 'canceled', terminal_at = ?, lease_id = NULL, leased_until = NULL, updated_at = ? WHERE job_id = ?`
			_, err = tx.Exec(ctx, upd, ts, ts, id)
			return err
		})
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
			  AND terminal_at IS NOT NULL AND terminal_at <= ?
			ORDER BY terminal_at, job_id
			LIMIT ?
		)`
	var removed int64
	err := retryBusy(ctx, func() error {
		n, e := tursodb.ExecAffecting(ctx, q.db, del, tursodb.FormatTime(before.UTC()), limit)
		if e != nil {
			return e
		}
		removed = n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(removed), nil
}

// GetLatestByKey returns the most-recently-created execution holding logicalKey
// (greatest created_at, job_id DESC tiebreak), or sdk.ErrNotFound.
func (q *FencedQueue) GetLatestByKey(ctx context.Context, logicalKey string) (job.Job, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue
		WHERE logical_key = ? ORDER BY created_at DESC, job_id DESC LIMIT 1`
	row, err := tursodb.QueryOne[fencedRow](ctx, q.db, query, logicalKey)
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// Get returns the job with the given unique execution id, or sdk.ErrNotFound.
func (q *FencedQueue) Get(ctx context.Context, id string) (job.Job, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue WHERE job_id = ?`
	row, err := tursodb.QueryOne[fencedRow](ctx, q.db, query, id)
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// insertFenced inserts one fresh pending execution from in and returns the stored
// row (INSERT ... RETURNING). A duplicate explicit id (or a concurrent active-key
// insert that lost the unique-index race) yields sdk.ErrAlreadyExists.
func insertFenced(ctx context.Context, tx *tursodb.Tx, in job.Enqueue) (job.Job, error) {
	id := in.ID
	if id == "" {
		id = newID("job")
	}
	now := tursodb.FormatTime(time.Now().UTC())
	const insert = `INSERT INTO fenced_job_queue (` + fencedColumns + `)
		VALUES (?, ?, ?, 'pending', ?, 0, ?, ?, NULL, NULL, NULL, NULL, ?, NULL, NULL, NULL, ?, ?)
		RETURNING ` + fencedColumns
	row, err := tursodb.QueryOne[fencedRow](ctx, tx, insert,
		id, in.Kind, payloadBytes(in.Payload), in.Priority, in.MaxAttempts,
		nullString(in.LogicalKey), tursodb.FormatTime(in.ScheduledFor.UTC()), now, now)
	if err != nil {
		return job.Job{}, err
	}
	return row.toDomain(), nil
}

// activeByKey returns the single non-terminal (pending|running) job holding
// logicalKey. The enclosing BEGIN IMMEDIATE transaction makes the read-then-write
// atomic.
func activeByKey(ctx context.Context, tx *tursodb.Tx, logicalKey string) (job.Job, bool, error) {
	const query = `SELECT ` + fencedColumns + ` FROM fenced_job_queue
		WHERE logical_key = ? AND status IN ('pending','running')
		ORDER BY created_at DESC, job_id DESC LIMIT 1`
	row, err := tursodb.QueryOne[fencedRow](ctx, tx, query, logicalKey)
	if errors.Is(err, sdk.ErrNotFound) {
		return job.Job{}, false, nil
	}
	if err != nil {
		return job.Job{}, false, err
	}
	return row.toDomain(), true, nil
}

// fencedState is the minimal row state the fenced transitions decide on.
type fencedState struct {
	status      string
	leaseID     string
	leasedUntil time.Time
	leasedValid bool
}

func (s fencedState) heldBy(leaseID string, now time.Time) bool {
	return s.status == string(job.StatusRunning) && s.leaseID == leaseID &&
		s.leasedValid && now.UTC().Before(s.leasedUntil)
}

func (s fencedState) terminal() bool {
	switch job.Status(s.status) {
	case job.StatusCompleted, job.StatusDeadLetter, job.StatusCanceled, job.StatusSuperseded:
		return true
	default:
		return false
	}
}

// readState reads the fenced job's decision state, mapping an absent row to
// sdk.ErrNotFound. The enclosing BEGIN IMMEDIATE transaction holds the write lock,
// so the subsequent write is atomic against it.
func readState(ctx context.Context, tx *tursodb.Tx, id string) (fencedState, error) {
	const query = `SELECT status, COALESCE(lease_id, ''), leased_until FROM fenced_job_queue WHERE job_id = ?`
	var (
		status   string
		leaseID  string
		leasedNS sql.NullString
	)
	err := tx.QueryRow(ctx, query, id).Scan(&status, &leaseID, &leasedNS)
	if err != nil {
		return fencedState{}, tursodb.MapError(err)
	}
	leased, err := tursodb.ParseNullTime(leasedNS)
	if err != nil {
		return fencedState{}, err
	}
	return fencedState{status: status, leaseID: leaseID, leasedUntil: leased, leasedValid: !leased.IsZero()}, nil
}

// payloadBytes returns a non-nil byte slice for the NOT NULL BLOB column: a nil
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
