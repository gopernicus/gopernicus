package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/logic/schedule"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// scheduleColumns is the job_schedules column list, in INSERT order.
const scheduleColumns = "schedule_id, name, kind, cron_expr, every_secs, payload, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at"

// scheduleSelect is the job_schedules read projection, in scanSchedule's order.
// Nullable cron_expr/every_secs/last_job_id are COALESCEd so they scan into plain
// scalars; only last_run_at stays nullable (*time.Time).
const scheduleSelect = "schedule_id, name, kind, COALESCE(cron_expr, ''), COALESCE(every_secs, 0), payload, enabled, next_run_at, last_run_at, COALESCE(last_job_id, ''), created_at, updated_at"

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules implements schedule.Repository over a PostgreSQL database. ClaimDue is
// a pure value compare-and-set on next_run_at — no locking construct,
// byte-identical semantics to the turso store — so N runtime instances fire each
// (schedule, slot) pair exactly once with no leader election.
type Schedules struct {
	db *pgxdb.DB
}

// NewScheduleStore returns a Schedules store backed by db.
func NewScheduleStore(db *pgxdb.DB) *Schedules {
	return &Schedules{db: db}
}

// Ensure upserts by Name in one transaction: it creates the schedule (enabled,
// next_run_at = next) or updates the existing one's kind, spec, and payload,
// advancing next_run_at to next only when the spec changed.
func (s *Schedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	now := time.Now().UTC()
	var out schedule.Schedule

	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		const sel = `SELECT ` + scheduleSelect + ` FROM job_schedules WHERE name = $1`
		existing, err := scanSchedule(tx.QueryRow(ctx, sel, in.Name))
		switch {
		case err == nil:
			specChanged := existing.Spec != in.Spec
			existing.Kind = in.Kind
			existing.Spec = in.Spec
			existing.Payload = in.Payload
			if specChanged {
				existing.NextRunAt = next
			}
			existing.UpdatedAt = now
			cron, every := specColumns(existing.Spec)
			const upd = `UPDATE job_schedules SET kind = $1, cron_expr = $2, every_secs = $3, payload = $4, next_run_at = $5, updated_at = $6 WHERE schedule_id = $7`
			if _, err := tx.Exec(ctx, upd, existing.Kind, cron, every, payloadValue(existing.Payload), existing.NextRunAt.UTC(), existing.UpdatedAt, existing.ID); err != nil {
				return err
			}
			out = existing
			return nil
		case errors.Is(err, errs.ErrNotFound):
			sch := schedule.Schedule{
				ID:        newID("sched"),
				Name:      in.Name,
				Kind:      in.Kind,
				Spec:      in.Spec,
				Payload:   in.Payload,
				Enabled:   true,
				NextRunAt: next,
				CreatedAt: now,
				UpdatedAt: now,
			}
			cron, every := specColumns(sch.Spec)
			const ins = `INSERT INTO job_schedules (` + scheduleColumns + `) VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7, NULL, NULL, $8, $9)`
			if _, err := tx.Exec(ctx, ins, sch.ID, sch.Name, sch.Kind, cron, every, payloadValue(sch.Payload), sch.NextRunAt.UTC(), sch.CreatedAt, sch.UpdatedAt); err != nil {
				return err
			}
			out = sch
			return nil
		default:
			return err
		}
	})
	if err != nil {
		return schedule.Schedule{}, err
	}
	return out, nil
}

// ListDue returns up to limit enabled schedules whose next_run_at <= now, ordered
// by (next_run_at, schedule_id) so the batch is deterministic. A non-positive
// limit returns all due schedules.
func (s *Schedules) ListDue(ctx context.Context, now time.Time, limit int) ([]schedule.Schedule, error) {
	query := `SELECT ` + scheduleSelect + ` FROM job_schedules
		WHERE enabled = TRUE AND next_run_at <= $1
		ORDER BY next_run_at, schedule_id`
	args := []any{now.UTC()}
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var due []schedule.Schedule
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, sch)
	}
	return due, pgxdb.MapError(rows.Err())
}

// ClaimDue is the pure value compare-and-set on next_run_at: it advances
// next_run_at to newNextRunAt (and last_run_at to now) only when the row's
// current next_run_at still equals prevNextRunAt and the schedule is enabled,
// reporting true when this caller won the (schedule, slot) pair.
func (s *Schedules) ClaimDue(ctx context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	const q = `UPDATE job_schedules SET next_run_at = $1, last_run_at = $2, updated_at = $2
		WHERE schedule_id = $3 AND next_run_at = $4 AND enabled = TRUE`
	tag, err := s.db.Exec(ctx, q, newNextRunAt.UTC(), now.UTC(), id, prevNextRunAt.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetLastJob records the id of the job fired for the most recent slot. A missing
// id yields errs.ErrNotFound.
func (s *Schedules) SetLastJob(ctx context.Context, id, jobID string, now time.Time) error {
	const q = `UPDATE job_schedules SET last_job_id = $1, updated_at = $2 WHERE schedule_id = $3`
	return s.execAffecting(ctx, q, jobID, now.UTC(), id)
}

// Get returns the schedule with the given id, or errs.ErrNotFound.
func (s *Schedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	const q = `SELECT ` + scheduleSelect + ` FROM job_schedules WHERE schedule_id = $1`
	return scanSchedule(s.db.QueryRow(ctx, q, id))
}

// List returns a cursor-paginated page of schedules, ordered by
// (created_at, schedule_id) descending.
func (s *Schedules) List(ctx context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	where := "WHERE 1 = 1"
	var args []any

	return pgxdb.ListPage(ctx, s.db, scheduleSelect, "job_schedules", where, args, orderField, "schedule_id", req,
		scanSchedule,
		func(sch schedule.Schedule) (time.Time, string) { return sch.CreatedAt, sch.ID },
	)
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// errs.ErrNotFound.
func (s *Schedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	const q = `UPDATE job_schedules SET enabled = $1, updated_at = $2 WHERE schedule_id = $3`
	return s.execAffecting(ctx, q, enabled, now.UTC(), id)
}

// Delete removes a schedule; a missing id yields errs.ErrNotFound.
func (s *Schedules) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM job_schedules WHERE schedule_id = $1`
	return s.execAffecting(ctx, q, id)
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to errs.ErrNotFound. Driver errors are already mapped by the connector.
func (s *Schedules) execAffecting(ctx context.Context, query string, args ...any) error {
	tag, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// specColumns maps a Spec to its (cron_expr, every_secs) storage values: exactly
// one is non-NULL. every_secs is a whole-second count.
func specColumns(spec schedule.Spec) (cron any, every any) {
	if spec.Cron != "" {
		return spec.Cron, nil
	}
	if spec.Every > 0 {
		return nil, int64(spec.Every / time.Second)
	}
	return nil, nil
}

// scanSchedule scans one job_schedules row (scheduleSelect projection), mapping
// pgx.ErrNoRows to errs.ErrNotFound via the connector's MapError.
func scanSchedule(sc scanner) (schedule.Schedule, error) {
	var (
		sch                  schedule.Schedule
		cronExpr             string
		everySecs            int64
		payload              []byte
		enabled              bool
		nextRunAt            time.Time
		createdAt, updatedAt time.Time
		lastRunAt            *time.Time
		lastJobID            string
	)
	err := sc.Scan(
		&sch.ID, &sch.Name, &sch.Kind, &cronExpr, &everySecs, &payload, &enabled,
		&nextRunAt, &lastRunAt, &lastJobID, &createdAt, &updatedAt,
	)
	if err != nil {
		return schedule.Schedule{}, pgxdb.MapError(err)
	}

	sch.Spec = schedule.Spec{Cron: cronExpr, Every: time.Duration(everySecs) * time.Second}
	sch.Payload = json.RawMessage(payload)
	sch.Enabled = enabled
	sch.LastJobID = lastJobID
	sch.NextRunAt = nextRunAt.UTC()
	sch.CreatedAt = createdAt.UTC()
	sch.UpdatedAt = updatedAt.UTC()
	if lastRunAt != nil {
		t := lastRunAt.UTC()
		sch.LastRunAt = &t
	}
	return sch, nil
}
