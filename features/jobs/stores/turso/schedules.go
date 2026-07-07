package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/logic/schedule"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// scheduleColumns is the job_schedules projection, in scanSchedule's order.
const scheduleColumns = "schedule_id, name, kind, cron_expr, every_secs, payload, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at"

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules implements schedule.Repository over a libSQL database. ClaimDue is a
// pure value compare-and-set on next_run_at — no locking construct, byte-identical
// semantics to the postgres store — so N runtime instances fire each (schedule,
// slot) pair exactly once with no leader election.
type Schedules struct {
	db *tursodb.DB
}

// NewScheduleStore returns a Schedules store backed by db.
func NewScheduleStore(db *tursodb.DB) *Schedules {
	return &Schedules{db: db}
}

// Ensure upserts by Name in one transaction: it creates the schedule (enabled,
// next_run_at = next) or updates the existing one's kind, spec, and payload,
// advancing next_run_at to next only when the spec changed.
func (s *Schedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	now := time.Now().UTC()
	var out schedule.Schedule

	err := retryBusy(ctx, func() error {
		return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
			const sel = `SELECT ` + scheduleColumns + ` FROM job_schedules WHERE name = ?`
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
				const upd = `UPDATE job_schedules SET kind = ?, cron_expr = ?, every_secs = ?, payload = ?, next_run_at = ?, updated_at = ? WHERE schedule_id = ?`
				if _, err := tx.Exec(ctx, upd, existing.Kind, cron, every, payloadValue(existing.Payload), formatTS(existing.NextRunAt), formatTS(existing.UpdatedAt), existing.ID); err != nil {
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
				const ins = `INSERT INTO job_schedules (` + scheduleColumns + `) VALUES (?, ?, ?, ?, ?, ?, 1, ?, NULL, NULL, ?, ?)`
				if _, err := tx.Exec(ctx, ins, sch.ID, sch.Name, sch.Kind, cron, every, payloadValue(sch.Payload), formatTS(sch.NextRunAt), formatTS(sch.CreatedAt), formatTS(sch.UpdatedAt)); err != nil {
					return err
				}
				out = sch
				return nil
			default:
				return err
			}
		})
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
	const q = `SELECT ` + scheduleColumns + ` FROM job_schedules
		WHERE enabled = 1 AND next_run_at <= ?
		ORDER BY next_run_at, schedule_id LIMIT ?`
	lim := limit
	if lim <= 0 {
		lim = -1 // SQLite: no limit
	}
	rows, err := s.db.Query(ctx, q, formatTS(now.UTC()), lim)
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
	return due, tursodb.MapError(rows.Err())
}

// ClaimDue is the pure value compare-and-set on next_run_at: it advances
// next_run_at to newNextRunAt (and last_run_at to now) only when the row's
// current next_run_at still equals prevNextRunAt and the schedule is enabled,
// reporting true when this caller won the (schedule, slot) pair.
func (s *Schedules) ClaimDue(ctx context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	const q = `UPDATE job_schedules SET next_run_at = ?, last_run_at = ?, updated_at = ?
		WHERE schedule_id = ? AND next_run_at = ? AND enabled = 1`
	var won bool
	err := retryBusy(ctx, func() error {
		res, err := s.db.Exec(ctx, q, formatTS(newNextRunAt.UTC()), formatTS(now.UTC()), formatTS(now.UTC()), id, formatTS(prevNextRunAt.UTC()))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		won = n == 1
		return nil
	})
	return won, err
}

// SetLastJob records the id of the job fired for the most recent slot. A missing
// id yields errs.ErrNotFound.
func (s *Schedules) SetLastJob(ctx context.Context, id, jobID string, now time.Time) error {
	const q = `UPDATE job_schedules SET last_job_id = ?, updated_at = ? WHERE schedule_id = ?`
	return s.execAffecting(ctx, q, jobID, formatTS(now.UTC()), id)
}

// Get returns the schedule with the given id, or errs.ErrNotFound.
func (s *Schedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	const q = `SELECT ` + scheduleColumns + ` FROM job_schedules WHERE schedule_id = ?`
	return scanSchedule(s.db.QueryRow(ctx, q, id))
}

// List returns a cursor-paginated page of schedules, ordered by
// (created_at, schedule_id) descending.
func (s *Schedules) List(ctx context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	where := "WHERE 1 = 1"
	var args []any

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[schedule.Schedule]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		ts := formatTS(cv)
		where += " AND ((created_at < ?) OR (created_at = ? AND schedule_id < ?))"
		args = append(args, ts, ts, cur.PK)
	}

	limit := req.NormalizedLimit()
	query := `SELECT ` + scheduleColumns + ` FROM job_schedules ` + where + ` ORDER BY created_at DESC, schedule_id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return crud.Page[schedule.Schedule]{}, err
	}
	defer rows.Close()

	var items []schedule.Schedule
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return crud.Page[schedule.Schedule]{}, err
		}
		items = append(items, sch)
	}
	if err := rows.Err(); err != nil {
		return crud.Page[schedule.Schedule]{}, tursodb.MapError(err)
	}

	return crud.TrimPage(items, limit, func(sch schedule.Schedule) (string, error) {
		return crud.EncodeCursor(orderField, sch.CreatedAt, sch.ID)
	})
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// errs.ErrNotFound.
func (s *Schedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	const q = `UPDATE job_schedules SET enabled = ?, updated_at = ? WHERE schedule_id = ?`
	return s.execAffecting(ctx, q, boolToInt(enabled), formatTS(now.UTC()), id)
}

// Delete removes a schedule; a missing id yields errs.ErrNotFound.
func (s *Schedules) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM job_schedules WHERE schedule_id = ?`
	return s.execAffecting(ctx, q, id)
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to errs.ErrNotFound and retrying transient busy errors.
func (s *Schedules) execAffecting(ctx context.Context, query string, args ...any) error {
	return retryBusy(ctx, func() error {
		res, err := s.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.ErrNotFound
		}
		return nil
	})
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

// scanSchedule scans one job_schedules row, mapping sql.ErrNoRows to
// errs.ErrNotFound.
func scanSchedule(sc scanner) (schedule.Schedule, error) {
	var (
		sch                             schedule.Schedule
		cronExpr                        sql.NullString
		everySecs                       sql.NullInt64
		payload                         string
		enabled                         int
		nextRunAt, createdAt, updatedAt string
		lastRunAt, lastJobID            sql.NullString
	)
	err := sc.Scan(
		&sch.ID, &sch.Name, &sch.Kind, &cronExpr, &everySecs, &payload, &enabled,
		&nextRunAt, &lastRunAt, &lastJobID, &createdAt, &updatedAt,
	)
	if err != nil {
		return schedule.Schedule{}, tursodb.MapError(err)
	}

	sch.Spec = schedule.Spec{Cron: cronExpr.String, Every: time.Duration(everySecs.Int64) * time.Second}
	sch.Payload = json.RawMessage(payload)
	sch.Enabled = enabled != 0
	sch.LastJobID = lastJobID.String

	if sch.NextRunAt, err = parseTime(nextRunAt); err != nil {
		return schedule.Schedule{}, err
	}
	if sch.CreatedAt, err = parseTime(createdAt); err != nil {
		return schedule.Schedule{}, err
	}
	if sch.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return schedule.Schedule{}, err
	}
	if lastRunAt.Valid && lastRunAt.String != "" {
		t, err := parseTime(lastRunAt.String)
		if err != nil {
			return schedule.Schedule{}, err
		}
		sch.LastRunAt = &t
	}
	return sch, nil
}
