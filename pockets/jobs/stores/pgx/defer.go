package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

var (
	_ workers.JobDeferrer    = (*Queue)(nil)
	_ workers.FencedDeferrer = (*FencedQueue)(nil)
)

// Defer releases a running job until availableAt without consuming a retry.
func (q *Queue) Defer(ctx context.Context, id string, availableAt time.Time, reason string, now time.Time) error {
	if !availableAt.After(now) {
		return sdk.ErrInvalidInput
	}
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		query := `SELECT status FROM ` + q.table("job_queue") + ` WHERE job_id = @id FOR UPDATE`
		var status string
		if err := tx.QueryRow(ctx, query, pgx.NamedArgs{"id": id}).Scan(&status); err != nil {
			return pgxdb.MapError(err)
		}
		if status != string(job.StatusRunning) {
			return sdk.ErrConflict
		}
		upd := `UPDATE ` + q.table("job_queue") + `
			SET status = 'pending', scheduled_for = @avail, worker_name = NULL,
			    claimed_at = NULL, failure_reason = @reason, updated_at = @now
			WHERE job_id = @id`
		_, err := tx.Exec(ctx, upd, pgx.NamedArgs{"avail": availableAt.UTC(), "reason": reason, "now": now.UTC(), "id": id})
		return err
	})
}

// Defer releases a live claim until availableAt and refunds only that claim's
// attempt. The row lock keeps the ownership check and refund atomic.
func (q *FencedQueue) Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	if !availableAt.After(now) {
		return sdk.ErrInvalidInput
	}
	return q.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		st, err := q.lockState(ctx, tx, id)
		if err != nil {
			return err
		}
		if !st.heldBy(leaseID, now) {
			return sdk.ErrConflict
		}
		upd := `UPDATE ` + q.table("fenced_job_queue") + `
			SET status = 'pending', scheduled_for = @avail, retry_count = retry_count - 1,
			    lease_id = NULL, leased_until = NULL, worker_name = NULL,
			    claimed_at = NULL, failure_reason = @reason, updated_at = @now
			WHERE job_id = @id AND retry_count > 0`
		result, err := tx.Exec(ctx, upd, pgx.NamedArgs{"avail": availableAt.UTC(), "reason": reason, "now": now.UTC(), "id": id})
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return sdk.ErrConflict
		}
		return nil
	})
}
