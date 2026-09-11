package turso

import (
	"context"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
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
	return retryBusy(ctx, func() error {
		return q.db.InTx(ctx, func(tx *tursodb.Tx) error {
			const query = `SELECT status FROM job_queue WHERE job_id = ?`
			var status string
			if err := tx.QueryRow(ctx, query, id).Scan(&status); err != nil {
				return tursodb.MapError(err)
			}
			if status != string(job.StatusRunning) {
				return sdk.ErrConflict
			}
			const upd = `UPDATE job_queue
				SET status = 'pending', scheduled_for = ?, worker_name = NULL,
				    claimed_at = NULL, failure_reason = ?, updated_at = ? WHERE job_id = ?`
			_, err := tx.Exec(ctx, upd, tursodb.FormatTime(availableAt.UTC()), reason, tursodb.FormatTime(now.UTC()), id)
			return err
		})
	})
}

// Defer releases a live claim until availableAt and refunds only that claim's
// attempt. BEGIN IMMEDIATE keeps the ownership check and refund atomic.
func (q *FencedQueue) Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	if !availableAt.After(now) {
		return sdk.ErrInvalidInput
	}
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
				SET status = 'pending', scheduled_for = ?, retry_count = retry_count - 1,
				    lease_id = NULL, leased_until = NULL, worker_name = NULL,
				    claimed_at = NULL, failure_reason = ?, updated_at = ?
				WHERE job_id = ? AND retry_count > 0`
			result, err := tx.Exec(ctx, upd, tursodb.FormatTime(availableAt.UTC()), reason, tursodb.FormatTime(now.UTC()), id)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				return sdk.ErrConflict
			}
			return nil
		})
	})
}
