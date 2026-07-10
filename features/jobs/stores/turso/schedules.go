package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// scheduleColumns is the job_schedules projection, in scheduleRow's field order.
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

// scheduleRow is the store-local, db-tagged projection of a job_schedules row
// ScanStruct scans into; toDomain maps it to the domain entity. The nullable
// cron_expr / every_secs / last_job_id columns scan into sql.Null* and last_run_at
// into turso.NullTime; enabled is the 0/1 flag read via turso.Bool.
type scheduleRow struct {
	ID        string           `db:"schedule_id"`
	Name      string           `db:"name"`
	Kind      string           `db:"kind"`
	CronExpr  sql.NullString   `db:"cron_expr"`
	EverySecs sql.NullInt64    `db:"every_secs"`
	Payload   []byte           `db:"payload"`
	Enabled   tursodb.Bool     `db:"enabled"`
	NextRunAt tursodb.Time     `db:"next_run_at"`
	LastRunAt tursodb.NullTime `db:"last_run_at"`
	LastJobID sql.NullString   `db:"last_job_id"`
	CreatedAt tursodb.Time     `db:"created_at"`
	UpdatedAt tursodb.Time     `db:"updated_at"`
}

func (r scheduleRow) toDomain() schedule.Schedule {
	return schedule.Schedule{
		ID:        r.ID,
		Name:      r.Name,
		Kind:      r.Kind,
		Spec:      schedule.Spec{Cron: r.CronExpr.String, Every: time.Duration(r.EverySecs.Int64) * time.Second},
		Payload:   json.RawMessage(r.Payload),
		Enabled:   bool(r.Enabled),
		NextRunAt: r.NextRunAt.Time,
		LastRunAt: r.LastRunAt.TimePtr(),
		LastJobID: r.LastJobID.String,
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
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
			row, err := queryOne[scheduleRow](ctx, tx, sel, in.Name)
			switch {
			case err == nil:
				existing := row.toDomain()
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
				if _, err := tx.Exec(ctx, upd, existing.Kind, cron, every, payloadValue(existing.Payload), tursodb.FormatTime(existing.NextRunAt), tursodb.FormatTime(existing.UpdatedAt), existing.ID); err != nil {
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
				if _, err := tx.Exec(ctx, ins, sch.ID, sch.Name, sch.Kind, cron, every, payloadValue(sch.Payload), tursodb.FormatTime(sch.NextRunAt), tursodb.FormatTime(sch.CreatedAt), tursodb.FormatTime(sch.UpdatedAt)); err != nil {
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
	rows, err := s.db.Query(ctx, q, tursodb.FormatTime(now.UTC()), lim)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var due []schedule.Schedule
	for rows.Next() {
		row, err := tursodb.ScanStruct[scheduleRow](rows)
		if err != nil {
			return nil, err
		}
		due = append(due, row.toDomain())
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
		n, err := tursodb.ExecAffecting(ctx, s.db, q, tursodb.FormatTime(newNextRunAt.UTC()), tursodb.FormatTime(now.UTC()), tursodb.FormatTime(now.UTC()), id, tursodb.FormatTime(prevNextRunAt.UTC()))
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
	return s.execAffecting(ctx, q, jobID, tursodb.FormatTime(now.UTC()), id)
}

// Get returns the schedule with the given id, or errs.ErrNotFound.
func (s *Schedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	const q = `SELECT ` + scheduleColumns + ` FROM job_schedules WHERE schedule_id = ?`
	row, err := queryOne[scheduleRow](ctx, s.db, q, id)
	if err != nil {
		return schedule.Schedule{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor- or offset-paginated page of schedules, in the resolved
// order (default created_at DESC, schedule_id DESC).
func (s *Schedules) List(ctx context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	lq := tursodb.ListQuery[scheduleRow]{
		BaseSQL:      `SELECT ` + scheduleColumns + ` FROM job_schedules`,
		OrderFields:  schedule.OrderFields,
		DefaultOrder: schedule.DefaultOrder,
		PK:           "schedule_id",
		OrderValueOf: func(r scheduleRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r scheduleRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, lq, req)
	if err != nil {
		return crud.Page[schedule.Schedule]{}, err
	}
	return crud.MapPage(page, scheduleRow.toDomain), nil
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// errs.ErrNotFound.
func (s *Schedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	const q = `UPDATE job_schedules SET enabled = ?, updated_at = ? WHERE schedule_id = ?`
	return s.execAffecting(ctx, q, tursodb.BoolToInt(enabled), tursodb.FormatTime(now.UTC()), id)
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
		n, err := tursodb.ExecAffecting(ctx, s.db, query, args...)
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
