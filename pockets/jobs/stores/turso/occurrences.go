package turso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
)

const occurrenceColumns = "job_id, schedule_id, kind, tenant_id, payload, slot, claimed_at, attempts"

type occurrenceRow struct {
	Attempts   int            `db:"attempts"`
	JobID      string         `db:"job_id"`
	ScheduleID string         `db:"schedule_id"`
	Kind       string         `db:"kind"`
	TenantID   sql.NullString `db:"tenant_id"`
	Payload    []byte         `db:"payload"`
	Slot       tursodb.Time   `db:"slot"`
	ClaimedAt  tursodb.Time   `db:"claimed_at"`
}

func (r occurrenceRow) toDomain() schedule.Occurrence {
	return schedule.Occurrence{Attempts: r.Attempts, JobID: r.JobID, ScheduleID: r.ScheduleID, Kind: r.Kind, TenantID: r.TenantID.String, Payload: r.Payload, Slot: r.Slot.Time, ClaimedAt: r.ClaimedAt.Time}
}
func (s *Schedules) ClaimDue(ctx context.Context, expected schedule.Schedule, next, now time.Time) (bool, error) {
	if !next.After(now) {
		return false, nil
	}
	var won bool
	err := retryBusy(ctx, func() error {
		won = false
		return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
			r, err := tursodb.QueryOne[scheduleRow](ctx, tx, `SELECT `+scheduleColumns+` FROM job_schedules WHERE schedule_id = ?`, expected.ID)
			if errors.Is(err, sdk.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			sch := r.toDomain()
			if !sch.Enabled || sch.NextRunAt.After(now) || !sch.NextRunAt.Equal(expected.NextRunAt) || sch.Kind != expected.Kind || sch.Spec != expected.Spec {
				return nil
			}
			n, err := tursodb.ExecAffecting(ctx, tx, `INSERT INTO job_schedule_occurrences (job_id, schedule_id, kind, tenant_id, payload, slot, claimed_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, schedule.OccurrenceID(sch.ID, sch.NextRunAt), sch.ID, sch.Kind, nullString(sch.TenantID), payloadValue(sch.Payload), tursodb.FormatTime(sch.NextRunAt), tursodb.FormatTime(now.UTC()))
			if err != nil || n == 0 {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE job_schedules SET next_run_at = ?, updated_at = MAX(updated_at, ?) WHERE schedule_id = ?`, tursodb.FormatTime(next.UTC()), tursodb.FormatTime(now.UTC()), sch.ID)
			won = err == nil
			return err
		})
	})
	return won && err == nil, err
}
func (s *Schedules) ListPending(ctx context.Context, limit int, kinds []string) ([]schedule.Occurrence, error) {
	kindClause, kindArgs := kindsIn(kinds)
	q := `SELECT ` + occurrenceColumns + ` FROM job_schedule_occurrences WHERE 1=1` + kindClause + ` ORDER BY attempts, slot, job_id LIMIT ?`
	if limit <= 0 {
		limit = -1
	}
	args := append(kindArgs, limit)
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schedule.Occurrence
	for rows.Next() {
		r, err := tursodb.ScanStruct[occurrenceRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.toDomain())
	}
	return out, tursodb.MapError(rows.Err())
}
func (s *Schedules) AckOccurrence(ctx context.Context, jobID string, now time.Time) error {
	return retryBusy(ctx, func() error {
		return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
			r, err := tursodb.QueryOne[occurrenceRow](ctx, tx, `DELETE FROM job_schedule_occurrences WHERE job_id = ? RETURNING `+occurrenceColumns, jobID)
			if errors.Is(err, sdk.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE job_schedules SET last_job_id = ?, last_run_at = ?, updated_at = MAX(updated_at, ?) WHERE schedule_id = ?`, jobID, tursodb.FormatTime(r.ClaimedAt.Time), tursodb.FormatTime(now.UTC()), r.ScheduleID)
			return err
		})
	})
}

func (s *Schedules) RecordAttempt(ctx context.Context, jobID string) (bool, error) {
	var found bool
	err := retryBusy(ctx, func() error {
		n, err := tursodb.ExecAffecting(ctx, s.db, `UPDATE job_schedule_occurrences SET attempts = attempts + 1 WHERE job_id = ?`, jobID)
		found = n == 1
		return err
	})
	return found && err == nil, err
}
