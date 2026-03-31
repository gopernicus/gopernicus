package jobqueuepgx

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/core/repositories/jobs/jobqueue"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// Checkout atomically claims the next available PENDING job using
// FOR UPDATE SKIP LOCKED to prevent contention between workers.
func (s *Store) Checkout(ctx context.Context, workerID string, now time.Time) (jobqueue.JobQueue, error) {
	query := `
		UPDATE job_queue
		SET status = 'STAGED', worker_name = @worker_name, staged_at = @now, updated_at = @now
		WHERE job_id = (
			SELECT job_id FROM job_queue
			WHERE status = 'PENDING' AND scheduled_for <= @now
			ORDER BY priority DESC, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING *`

	args := pgx.NamedArgs{
		"worker_name": workerID,
		"now":         now,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return jobqueue.JobQueue{}, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	record, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[jobqueue.JobQueue])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return jobqueue.JobQueue{}, workers.ErrNoWork
		}
		return jobqueue.JobQueue{}, pgxdb.HandlePgError(err)
	}

	return record, nil
}

// Complete marks a job as successfully completed.
func (s *Store) Complete(ctx context.Context, jobID string, now time.Time) error {
	query := `
		UPDATE job_queue
		SET status = 'COMPLETED', completed_at = @now, updated_at = @now
		WHERE job_id = @job_id AND status = 'STAGED'`

	args := pgx.NamedArgs{
		"job_id": jobID,
		"now":    now,
	}

	result, err := s.db.Exec(ctx, query, args)
	if err != nil {
		return pgxdb.HandlePgError(err)
	}

	if result.RowsAffected() == 0 {
		return jobqueue.ErrJobQueueNotFound
	}

	return nil
}

// Fail marks a job as failed. If retry_count reaches maxAttempts, the job is
// dead-lettered. Otherwise it is rescheduled as PENDING with exponential backoff.
func (s *Store) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	query := `
		UPDATE job_queue
		SET
			retry_count = retry_count + 1,
			failure_reason = @reason,
			updated_at = @now,
			status = CASE
				WHEN retry_count + 1 >= @max_attempts THEN 'DEAD_LETTER'
				ELSE 'PENDING'
			END,
			scheduled_for = CASE
				WHEN retry_count + 1 >= @max_attempts THEN scheduled_for
				ELSE @now + (interval '1 second' * power(2, retry_count))
			END,
			worker_name = NULL,
			staged_at = NULL
		WHERE job_id = @job_id AND status = 'STAGED'`

	args := pgx.NamedArgs{
		"job_id":       jobID,
		"now":          now,
		"reason":       reason,
		"max_attempts": maxAttempts,
	}

	result, err := s.db.Exec(ctx, query, args)
	if err != nil {
		return pgxdb.HandlePgError(err)
	}

	if result.RowsAffected() == 0 {
		return jobqueue.ErrJobQueueNotFound
	}

	return nil
}

// compile-time check: Store implements workers.JobStore[jobqueue.JobQueue].
var _ workers.JobStore[jobqueue.JobQueue] = (*Store)(nil)
