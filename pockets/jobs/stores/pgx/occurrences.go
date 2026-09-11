package pgx

import (
	"context"
	"errors"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

const occurrenceColumns = "job_id, schedule_id, kind, COALESCE(tenant_id, '') AS tenant_id, payload, slot, claimed_at, attempts"

type occurrenceRow struct {
	Attempts   int       `db:"attempts"`
	JobID      string    `db:"job_id"`
	ScheduleID string    `db:"schedule_id"`
	Kind       string    `db:"kind"`
	TenantID   string    `db:"tenant_id"`
	Payload    []byte    `db:"payload"`
	Slot       time.Time `db:"slot"`
	ClaimedAt  time.Time `db:"claimed_at"`
}

func (r occurrenceRow) toDomain() schedule.Occurrence {
	return schedule.Occurrence{Attempts: r.Attempts, JobID: r.JobID, ScheduleID: r.ScheduleID, Kind: r.Kind, TenantID: r.TenantID, Payload: r.Payload, Slot: r.Slot.UTC(), ClaimedAt: r.ClaimedAt.UTC()}
}

func (s *Schedules) ClaimDue(ctx context.Context, expected schedule.Schedule, next, now time.Time) (bool, error) {
	if !next.After(now) {
		return false, nil
	}
	var won bool
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		row, err := pgxdb.QueryOne[scheduleRow](ctx, tx, `SELECT `+scheduleRowColumns+` FROM `+s.table("job_schedules")+` WHERE schedule_id = @id FOR UPDATE`, pgx.NamedArgs{"id": expected.ID})
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		sch := row.toDomain()
		if !sch.Enabled || sch.NextRunAt.After(now) || !sch.NextRunAt.Equal(expected.NextRunAt) || sch.Kind != expected.Kind || sch.Spec != expected.Spec {
			return nil
		}
		id := schedule.OccurrenceID(sch.ID, sch.NextRunAt)
		n, err := pgxdb.ExecAffecting(ctx, tx, `INSERT INTO `+s.table("job_schedule_occurrences")+` (job_id, schedule_id, kind, tenant_id, payload, slot, claimed_at)
   VALUES (@job, @schedule, @kind, @tenant, @payload, @slot, @now) ON CONFLICT DO NOTHING`, pgx.NamedArgs{"job": id, "schedule": sch.ID, "kind": sch.Kind, "tenant": nullString(sch.TenantID), "payload": payloadValue(sch.Payload), "slot": sch.NextRunAt, "now": now.UTC()})
		if err != nil || n == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE `+s.table("job_schedules")+` SET next_run_at = @next, updated_at = GREATEST(updated_at, @now) WHERE schedule_id = @id`, pgx.NamedArgs{"next": next.UTC(), "now": now.UTC(), "id": sch.ID})
		won = err == nil
		return err
	})
	return won && err == nil, err
}

func (s *Schedules) ListPending(ctx context.Context, limit int, kinds []string) ([]schedule.Occurrence, error) {
	q := `SELECT ` + occurrenceColumns + ` FROM ` + s.table("job_schedule_occurrences")
	args := pgx.NamedArgs{}
	if len(kinds) > 0 {
		q += ` WHERE kind = ANY(@kinds)`
		args["kinds"] = kinds
	}
	q += ` ORDER BY attempts, slot, job_id`
	if limit > 0 {
		q += ` LIMIT @limit`
		args["limit"] = limit
	}
	rows, err := s.db.Query(ctx, q, args)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[occurrenceRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out := make([]schedule.Occurrence, len(items))
	for i, r := range items {
		out[i] = r.toDomain()
	}
	return out, nil
}

func (s *Schedules) AckOccurrence(ctx context.Context, jobID string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// Lock the schedule before deleting its occurrence, matching ClaimDue's lock
		// order. The row may have been deleted; admitted work still gets acknowledged.
		_, err := tx.Exec(ctx, `SELECT schedule_id FROM `+s.table("job_schedules")+` WHERE schedule_id = (SELECT schedule_id FROM `+s.table("job_schedule_occurrences")+` WHERE job_id = @job) FOR UPDATE`, pgx.NamedArgs{"job": jobID})
		if err != nil {
			return err
		}
		r, err := pgxdb.QueryOne[occurrenceRow](ctx, tx, `DELETE FROM `+s.table("job_schedule_occurrences")+` WHERE job_id = @job RETURNING `+occurrenceColumns, pgx.NamedArgs{"job": jobID})
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE `+s.table("job_schedules")+` SET last_job_id = @job, last_run_at = @claimed, updated_at = GREATEST(updated_at, @now) WHERE schedule_id = @id`, pgx.NamedArgs{"job": jobID, "claimed": r.ClaimedAt, "now": now.UTC(), "id": r.ScheduleID})
		return err
	})
}

func (s *Schedules) RecordAttempt(ctx context.Context, jobID string) (bool, error) {
	n, err := pgxdb.ExecAffecting(ctx, s.db, `UPDATE `+s.table("job_schedule_occurrences")+` SET attempts = attempts + 1 WHERE job_id = @id`, pgx.NamedArgs{"id": jobID})
	return n == 1, err
}
