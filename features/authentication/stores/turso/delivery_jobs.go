package turso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// DeliveryJobStore implements deliveryjob.Repository over a libSQL database
// (design §6.1.1). payload is the whole encrypted envelope stored as opaque BLOB —
// bound as []byte, never string()-converted — and there is no plaintext
// destination/message/identifier column. Enqueue is idempotent by the partial-unique
// idempotency_key. libSQL/SQLite has no FOR UPDATE SKIP LOCKED, so Claim leases the
// oldest due job through one atomic UPDATE ... WHERE ... RETURNING whose outer
// predicate re-checks the lease so exactly one worker wins under the connector's
// serialized writes; Succeed/Fail/Retry are lease-checked so a reclaimed job's late
// completer loses.
type DeliveryJobStore struct {
	db *tursodb.DB
}

var _ deliveryjob.Repository = (*DeliveryJobStore)(nil)

// NewDeliveryJobStore returns a DeliveryJobStore backed by db.
func NewDeliveryJobStore(db *tursodb.DB) *DeliveryJobStore {
	return &DeliveryJobStore{db: db}
}

const deliveryJobColumns = "id, kind, purpose, idempotency_key, payload, state, attempt_count, available_at, lease_id, leased_until, last_error, created_at, updated_at, terminal_at"

// scanJob scans a full delivery_jobs row from a Scanner, mapping the BLOB payload
// to []byte, the fixed-width TEXT timestamps, and the nullable leased_until/
// terminal_at columns to the domain's zero-time sentinels.
func scanJob(row tursodb.Scanner) (deliveryjob.Job, error) {
	var (
		job         deliveryjob.Job
		availableAt tursodb.Time
		createdAt   tursodb.Time
		updatedAt   tursodb.Time
		leasedUntil tursodb.NullTime
		terminalAt  tursodb.NullTime
	)
	err := row.Scan(
		&job.ID, &job.Kind, &job.Purpose, &job.IdempotencyKey, &job.Payload, &job.State,
		&job.AttemptCount, &availableAt, &job.LeaseID, &leasedUntil, &job.LastError,
		&createdAt, &updatedAt, &terminalAt,
	)
	if err != nil {
		return deliveryjob.Job{}, err
	}
	job.AvailableAt = availableAt.Time
	job.CreatedAt = createdAt.Time
	job.UpdatedAt = updatedAt.Time
	job.LeasedUntil = leasedUntil.Time
	job.TerminalAt = terminalAt.Time
	return job, nil
}

// insertPending inserts job as a fresh StatePending row and returns the stored row
// (with any DB-generated id). Callers pass a *Tx for atomic enqueue/replace.
func insertPending(ctx context.Context, q tursodb.Querier, job deliveryjob.Job) (deliveryjob.Job, error) {
	payload := job.Payload
	if payload == nil {
		payload = []byte{}
	}
	args := []any{
		job.Kind,
		job.Purpose,
		job.IdempotencyKey,
		payload,
		job.AttemptCount,
		tursodb.FormatTime(job.AvailableAt),
		job.LastError,
		tursodb.FormatTime(job.CreatedAt),
		tursodb.FormatTime(job.UpdatedAt),
	}
	cols := `kind, purpose, idempotency_key, payload, state, attempt_count, available_at, lease_id, leased_until, last_error, created_at, updated_at, terminal_at`
	vals := `?, ?, ?, ?, 'pending', ?, ?, '', NULL, ?, ?, ?, NULL`
	if job.ID != "" {
		args = append([]any{job.ID}, args...)
		cols = "id, " + cols
		vals = "?, " + vals
	}
	insert := `INSERT INTO delivery_jobs (` + cols + `) VALUES (` + vals + `) RETURNING ` + deliveryJobColumns
	stored, err := scanJob(q.QueryRow(ctx, insert, args...))
	if err != nil {
		return deliveryjob.Job{}, tursodb.MapError(err)
	}
	return stored, nil
}

// Enqueue inserts job unless a non-terminal job already holds its IdempotencyKey,
// in which case that existing job is returned unchanged.
func (s *DeliveryJobStore) Enqueue(ctx context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	var result deliveryjob.Job
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		existing, err := scanJob(tx.QueryRow(ctx,
			`SELECT `+deliveryJobColumns+` FROM delivery_jobs WHERE idempotency_key = ? AND state = 'pending' LIMIT 1`,
			job.IdempotencyKey))
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return tursodb.MapError(err)
		}
		result, err = insertPending(ctx, tx, job)
		return err
	})
	if err != nil {
		return deliveryjob.Job{}, err
	}
	return result, nil
}

// Replace atomically cancels every non-terminal job holding job.IdempotencyKey and
// inserts job as a fresh StatePending row, returning it.
func (s *DeliveryJobStore) Replace(ctx context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	var result deliveryjob.Job
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		now := tursodb.FormatTime(time.Now())
		if _, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = 'canceled', terminal_at = ?, lease_id = '', leased_until = NULL, updated_at = ?
				WHERE idempotency_key = ? AND state = 'pending'`,
			now, now, job.IdempotencyKey); err != nil {
			return err
		}
		var err error
		result, err = insertPending(ctx, tx, job)
		return err
	})
	if err != nil {
		return deliveryjob.Job{}, err
	}
	return result, nil
}

// Claim atomically leases and returns the oldest due job, incrementing its
// AttemptCount and stamping LeaseID/LeasedUntil. No due job → sdk.ErrNotFound.
func (s *DeliveryJobStore) Claim(ctx context.Context, now time.Time, leaseID string, leaseFor time.Duration) (deliveryjob.Job, error) {
	nowText := tursodb.FormatTime(now)
	const q = `UPDATE delivery_jobs
		SET attempt_count = attempt_count + 1, lease_id = ?, leased_until = ?, updated_at = ?
		WHERE id = (
			SELECT id FROM delivery_jobs
			WHERE state = 'pending' AND available_at <= ? AND (leased_until IS NULL OR leased_until <= ?)
			ORDER BY available_at, created_at, id
			LIMIT 1
		) AND state = 'pending' AND (leased_until IS NULL OR leased_until <= ?)
		RETURNING ` + deliveryJobColumns
	job, err := scanJob(s.db.QueryRow(ctx, q,
		leaseID, tursodb.FormatTime(now.Add(leaseFor)), nowText,
		nowText, nowText, nowText))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deliveryjob.Job{}, sdk.ErrNotFound
		}
		return deliveryjob.Job{}, tursodb.MapError(err)
	}
	return job, nil
}

// Succeed marks the leaseID-held job StateSucceeded.
func (s *DeliveryJobStore) Succeed(ctx context.Context, id, leaseID string, now time.Time) error {
	return s.complete(ctx, id, leaseID, deliveryjob.StateSucceeded, "", now)
}

// Fail marks the leaseID-held job StateFailed with lastErr.
func (s *DeliveryJobStore) Fail(ctx context.Context, id, leaseID, lastErr string, now time.Time) error {
	return s.complete(ctx, id, leaseID, deliveryjob.StateFailed, lastErr, now)
}

// complete moves a leaseID-held job to a terminal state within one transaction; an
// already-in-that-state completion is idempotent, a reclaimed lease or a different
// terminal state is a conflict.
func (s *DeliveryJobStore) complete(ctx context.Context, id, leaseID, state, lastErr string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var (
			curState string
			curLease string
		)
		if err := tx.QueryRow(ctx, `SELECT state, lease_id FROM delivery_jobs WHERE id = ?`, id).
			Scan(&curState, &curLease); err != nil {
			return tursodb.MapError(err)
		}
		if curState == state {
			return nil // idempotent at-least-once report
		}
		if curState != deliveryjob.StatePending || curLease != leaseID {
			return sdk.ErrConflict // a terminal job or a reclaimed lease
		}
		nowText := tursodb.FormatTime(now)
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = ?, last_error = ?, terminal_at = ?, lease_id = '', leased_until = NULL, updated_at = ?
				WHERE id = ?`,
			state, lastErr, nowText, nowText, id)
		return err
	})
}

// Retry reschedules the leaseID-held job to availableAt with backoff, clears the
// lease, and records lastErr. Reclaimed lease or terminal job → sdk.ErrConflict.
func (s *DeliveryJobStore) Retry(ctx context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var (
			curState string
			curLease string
		)
		if err := tx.QueryRow(ctx, `SELECT state, lease_id FROM delivery_jobs WHERE id = ?`, id).
			Scan(&curState, &curLease); err != nil {
			return tursodb.MapError(err)
		}
		if curState != deliveryjob.StatePending || curLease != leaseID {
			return sdk.ErrConflict
		}
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET available_at = ?, last_error = ?, lease_id = '', leased_until = NULL, updated_at = ?
				WHERE id = ?`,
			tursodb.FormatTime(availableAt), lastErr, tursodb.FormatTime(now), id)
		return err
	})
}

// Cancel terminally cancels a non-terminal job by ID. Already canceled → nil;
// already succeeded/failed → sdk.ErrConflict; unknown → sdk.ErrNotFound.
func (s *DeliveryJobStore) Cancel(ctx context.Context, id string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var curState string
		if err := tx.QueryRow(ctx, `SELECT state FROM delivery_jobs WHERE id = ?`, id).Scan(&curState); err != nil {
			return tursodb.MapError(err)
		}
		if curState == deliveryjob.StateCanceled {
			return nil // idempotent
		}
		if curState != deliveryjob.StatePending {
			return sdk.ErrConflict // cannot cancel a succeeded/failed job
		}
		nowText := tursodb.FormatTime(now)
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = 'canceled', terminal_at = ?, lease_id = '', leased_until = NULL, updated_at = ?
				WHERE id = ?`,
			nowText, nowText, id)
		return err
	})
}

// GetLatestByIdempotencyKey returns the most-recently-created job holding
// idempotencyKey (the read-only status projection). It never leases or mutates. No
// such key → sdk.ErrNotFound.
func (s *DeliveryJobStore) GetLatestByIdempotencyKey(ctx context.Context, idempotencyKey string) (deliveryjob.Job, error) {
	job, err := scanJob(s.db.QueryRow(ctx,
		`SELECT `+deliveryJobColumns+` FROM delivery_jobs WHERE idempotency_key = ? ORDER BY created_at DESC, id DESC LIMIT 1`,
		idempotencyKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deliveryjob.Job{}, sdk.ErrNotFound
		}
		return deliveryjob.Job{}, tursodb.MapError(err)
	}
	return job, nil
}

// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or before
// before and returns the number removed (bounded batching).
func (s *DeliveryJobStore) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	args := []any{tursodb.FormatTime(before)}
	q := `DELETE FROM delivery_jobs WHERE state <> 'pending' AND terminal_at <= ?`
	if limit > 0 {
		q = `DELETE FROM delivery_jobs WHERE id IN (
			SELECT id FROM delivery_jobs WHERE state <> 'pending' AND terminal_at <= ? ORDER BY terminal_at LIMIT ?)`
		args = append(args, limit)
	}
	n, err := tursodb.ExecAffecting(ctx, s.db, q, args...)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
