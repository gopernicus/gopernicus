// gopernicus:bootstrap kind=pgxstore/store.go template=5fef194f164b
// This file is created once by gopernicus and will NOT be overwritten.
// Add custom store methods here. Store is defined in generated.go.
//
// Example:
//
//	func (s *Store) MyCustomQuery(ctx context.Context, id string) (jobschedules.JobSchedule, error) {
//		rows, err := s.db.Query(ctx, `SELECT ... FROM ... WHERE id = @id`, pgx.NamedArgs{"id": id})
//		if err != nil {
//			return jobschedules.JobSchedule{}, err
//		}
//		defer rows.Close()
//		return pgx.CollectOneRow(rows, pgx.RowToStructByName[jobschedules.JobSchedule])
//	}

package jobschedulespgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/core/repositories/jobs/jobschedules"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
)

// ─── scheduler claim path (hand-written; see core/jobs/scheduler) ───────────

// ListDue returns enabled schedules whose next_run_at has passed, by the
// DATABASE clock (now() in SQL — instance clock skew never decides
// due-ness). Plain read: claiming happens per row via ClaimDue's
// compare-and-set, so no locks are held across cron computation.
func (s *Store) ListDue(ctx context.Context, limit int) ([]jobschedules.JobSchedule, error) {
	query := `
		SELECT * FROM job_schedules
		WHERE enabled = TRUE AND next_run_at <= now()
		ORDER BY next_run_at
		LIMIT @limit`

	rows, err := s.db.Query(ctx, query, pgx.NamedArgs{"limit": limit})
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	records, err := pgx.CollectRows(rows, pgx.RowToStructByName[jobschedules.JobSchedule])
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	return records, nil
}

// ClaimDue advances a schedule's slot with a compare-and-set on
// next_run_at: exactly one instance wins the (schedule, slot) pair, with
// no FOR UPDATE and no transaction. A loser simply observes claimed=false.
// The winner fires the slot it claimed (prevNextRunAt) — the deterministic
// job id derived from it makes any crash-window refire idempotent.
func (s *Store) ClaimDue(ctx context.Context, scheduleID string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	query := `
		UPDATE job_schedules
		SET next_run_at = @next, last_run_at = @now, updated_at = @now
		WHERE schedule_id = @schedule_id
		  AND next_run_at = @prev
		  AND enabled = TRUE`

	tag, err := s.db.Exec(ctx, query, pgx.NamedArgs{
		"schedule_id": scheduleID,
		"prev":        prevNextRunAt,
		"next":        newNextRunAt,
		"now":         now,
	})
	if err != nil {
		return false, pgxdb.HandlePgError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetLastJob records the audit pointer to the most recently fired job.
func (s *Store) SetLastJob(ctx context.Context, scheduleID, jobID string, now time.Time) error {
	query := `
		UPDATE job_schedules
		SET last_job_id = @job_id, updated_at = @now
		WHERE schedule_id = @schedule_id`

	if _, err := s.db.Exec(ctx, query, pgx.NamedArgs{
		"schedule_id": scheduleID,
		"job_id":      jobID,
		"now":         now,
	}); err != nil {
		return pgxdb.HandlePgError(err)
	}
	return nil
}
