package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// DeliveryJobStore implements deliveryjob.Repository over a PostgreSQL database
// (design §6.1.1). payload is the whole encrypted envelope stored as opaque BYTEA
// — there is no plaintext destination/message/identifier column. Enqueue is
// idempotent by the partial-unique idempotency_key; Claim leases the oldest due
// job through one FOR UPDATE SKIP LOCKED subquery so exactly one worker wins;
// Succeed/Fail/Retry are lease-checked so a reclaimed job's late completer loses.
type DeliveryJobStore struct {
	db *pgxdb.DB
}

var _ deliveryjob.Repository = (*DeliveryJobStore)(nil)

// NewDeliveryJobStore returns a DeliveryJobStore backed by db.
func NewDeliveryJobStore(db *pgxdb.DB) *DeliveryJobStore {
	return &DeliveryJobStore{db: db}
}

const deliveryJobColumns = "id, kind, purpose, idempotency_key, payload, state, attempt_count, available_at, lease_id, leased_until, last_error, created_at, updated_at, terminal_at"

// scanJob scans a full delivery_jobs row from a Scanner, mapping the nullable
// leased_until/terminal_at columns to the domain's zero-time sentinels.
func scanJob(row pgxdb.Scanner) (deliveryjob.Job, error) {
	var (
		job         deliveryjob.Job
		leasedUntil *time.Time
		terminalAt  *time.Time
	)
	err := row.Scan(
		&job.ID, &job.Kind, &job.Purpose, &job.IdempotencyKey, &job.Payload, &job.State,
		&job.AttemptCount, &job.AvailableAt, &job.LeaseID, &leasedUntil, &job.LastError,
		&job.CreatedAt, &job.UpdatedAt, &terminalAt,
	)
	if err != nil {
		return deliveryjob.Job{}, err
	}
	job.AvailableAt = job.AvailableAt.UTC()
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	job.LeasedUntil = pgxdb.FromNullTime(leasedUntil)
	job.TerminalAt = pgxdb.FromNullTime(terminalAt)
	return job, nil
}

// insertPending inserts job as a fresh StatePending row and returns the stored
// row (with any DB-generated id). Callers pass a *Tx for atomic enqueue/replace.
func insertPending(ctx context.Context, q pgxdb.Querier, job deliveryjob.Job) (deliveryjob.Job, error) {
	payload := job.Payload
	if payload == nil {
		payload = []byte{}
	}
	args := pgx.NamedArgs{
		"kind":            job.Kind,
		"purpose":         job.Purpose,
		"idempotency_key": job.IdempotencyKey,
		"payload":         payload,
		"attempt_count":   job.AttemptCount,
		"available_at":    job.AvailableAt.UTC(),
		"last_error":      job.LastError,
		"created_at":      job.CreatedAt.UTC(),
		"updated_at":      job.UpdatedAt.UTC(),
	}
	cols := `kind, purpose, idempotency_key, payload, state, attempt_count, available_at, lease_id, leased_until, last_error, created_at, updated_at, terminal_at`
	vals := `@kind, @purpose, @idempotency_key, @payload, 'pending', @attempt_count, @available_at, '', NULL, @last_error, @created_at, @updated_at, NULL`
	if job.ID != "" {
		args["id"] = job.ID
		cols = "id, " + cols
		vals = "@id, " + vals
	}
	insert := `INSERT INTO delivery_jobs (` + cols + `) VALUES (` + vals + `) RETURNING ` + deliveryJobColumns
	stored, err := scanJob(q.QueryRow(ctx, insert, args))
	if err != nil {
		return deliveryjob.Job{}, pgxdb.MapError(err)
	}
	return stored, nil
}

// Enqueue inserts job unless a non-terminal job already holds its IdempotencyKey,
// in which case that existing job is returned unchanged.
func (s *DeliveryJobStore) Enqueue(ctx context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	var result deliveryjob.Job
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		existing, err := scanJob(tx.QueryRow(ctx,
			`SELECT `+deliveryJobColumns+` FROM delivery_jobs WHERE idempotency_key = @key AND state = 'pending' LIMIT 1`,
			pgx.NamedArgs{"key": job.IdempotencyKey}))
		if err == nil {
			result = existing
			return nil
		}
		if err != pgx.ErrNoRows {
			return pgxdb.MapError(err)
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
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = 'canceled', terminal_at = @now, lease_id = '', leased_until = NULL, updated_at = @now
				WHERE idempotency_key = @key AND state = 'pending'`,
			pgx.NamedArgs{"now": now, "key": job.IdempotencyKey}); err != nil {
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
	now = now.UTC()
	const q = `UPDATE delivery_jobs
		SET attempt_count = attempt_count + 1, lease_id = @lease_id, leased_until = @leased_until, updated_at = @now
		WHERE id = (
			SELECT id FROM delivery_jobs
			WHERE state = 'pending' AND available_at <= @now AND (leased_until IS NULL OR leased_until <= @now)
			ORDER BY available_at, created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + deliveryJobColumns
	job, err := scanJob(s.db.QueryRow(ctx, q, pgx.NamedArgs{
		"lease_id":     leaseID,
		"leased_until": now.Add(leaseFor),
		"now":          now,
	}))
	if err != nil {
		if err == pgx.ErrNoRows {
			return deliveryjob.Job{}, sdk.ErrNotFound
		}
		return deliveryjob.Job{}, pgxdb.MapError(err)
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
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var (
			curState string
			curLease string
		)
		if err := tx.QueryRow(ctx, `SELECT state, lease_id FROM delivery_jobs WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": id}).
			Scan(&curState, &curLease); err != nil {
			if err == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(err)
		}
		if curState == state {
			return nil // idempotent at-least-once report
		}
		if curState != deliveryjob.StatePending || curLease != leaseID {
			return sdk.ErrConflict // a terminal job or a reclaimed lease
		}
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = @state, last_error = @last_error, terminal_at = @now, lease_id = '', leased_until = NULL, updated_at = @now
				WHERE id = @id`,
			pgx.NamedArgs{"state": state, "last_error": lastErr, "now": now.UTC(), "id": id})
		return err
	})
}

// Retry reschedules the leaseID-held job to availableAt with backoff, clears the
// lease, and records lastErr. Reclaimed lease or terminal job → sdk.ErrConflict.
func (s *DeliveryJobStore) Retry(ctx context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var (
			curState string
			curLease string
		)
		if err := tx.QueryRow(ctx, `SELECT state, lease_id FROM delivery_jobs WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": id}).
			Scan(&curState, &curLease); err != nil {
			if err == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(err)
		}
		if curState != deliveryjob.StatePending || curLease != leaseID {
			return sdk.ErrConflict
		}
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET available_at = @available_at, last_error = @last_error, lease_id = '', leased_until = NULL, updated_at = @now
				WHERE id = @id`,
			pgx.NamedArgs{"available_at": availableAt.UTC(), "last_error": lastErr, "now": now.UTC(), "id": id})
		return err
	})
}

// Cancel terminally cancels a non-terminal job by ID. Already canceled → nil;
// already succeeded/failed → sdk.ErrConflict; unknown → sdk.ErrNotFound.
func (s *DeliveryJobStore) Cancel(ctx context.Context, id string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var curState string
		if err := tx.QueryRow(ctx, `SELECT state FROM delivery_jobs WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": id}).
			Scan(&curState); err != nil {
			if err == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(err)
		}
		if curState == deliveryjob.StateCanceled {
			return nil // idempotent
		}
		if curState != deliveryjob.StatePending {
			return sdk.ErrConflict // cannot cancel a succeeded/failed job
		}
		_, err := tx.Exec(ctx,
			`UPDATE delivery_jobs SET state = 'canceled', terminal_at = @now, lease_id = '', leased_until = NULL, updated_at = @now
				WHERE id = @id`,
			pgx.NamedArgs{"now": now.UTC(), "id": id})
		return err
	})
}

// GetLatestByIdempotencyKey returns the most-recently-created job holding
// idempotencyKey (the read-only status projection). It never leases or mutates. No
// such key → sdk.ErrNotFound.
func (s *DeliveryJobStore) GetLatestByIdempotencyKey(ctx context.Context, idempotencyKey string) (deliveryjob.Job, error) {
	job, err := scanJob(s.db.QueryRow(ctx,
		`SELECT `+deliveryJobColumns+` FROM delivery_jobs WHERE idempotency_key = @key ORDER BY created_at DESC, id DESC LIMIT 1`,
		pgx.NamedArgs{"key": idempotencyKey}))
	if err != nil {
		if err == pgx.ErrNoRows {
			return deliveryjob.Job{}, sdk.ErrNotFound
		}
		return deliveryjob.Job{}, pgxdb.MapError(err)
	}
	return job, nil
}

// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or before
// before and returns the number removed (bounded batching).
func (s *DeliveryJobStore) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	args := pgx.NamedArgs{"before": before.UTC()}
	q := `DELETE FROM delivery_jobs WHERE state <> 'pending' AND terminal_at <= @before`
	if limit > 0 {
		q = `DELETE FROM delivery_jobs WHERE id IN (
			SELECT id FROM delivery_jobs WHERE state <> 'pending' AND terminal_at <= @before ORDER BY terminal_at LIMIT @limit)`
		args["limit"] = limit
	}
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, args)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
