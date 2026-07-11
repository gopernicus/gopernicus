package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// scheduleColumns is the job_schedules column list, in Ensure's INSERT order.
const scheduleColumns = "schedule_id, name, kind, cron_expr, every_secs, payload, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at"

// scheduleRowColumns is the struct-scan projection for the NamedArgs read paths:
// every column is name-aliased so pgx.RowToStructByName matches it against
// scheduleRow's db tags. Nullable cron_expr/every_secs/last_job_id are COALESCEd
// so they scan into plain scalars; only last_run_at stays nullable (*time.Time).
const scheduleRowColumns = "schedule_id, name, kind, COALESCE(cron_expr, '') AS cron_expr, COALESCE(every_secs, 0) AS every_secs, payload, enabled, next_run_at, last_run_at, COALESCE(last_job_id, '') AS last_job_id, created_at, updated_at"

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules implements schedule.Repository over a PostgreSQL database. ClaimDue is
// a pure value compare-and-set on next_run_at — no locking construct,
// byte-identical semantics to the turso store — so N runtime instances fire each
// (schedule, slot) pair exactly once with no leader election.
type Schedules struct {
	db *pgxdb.DB
}

// scheduleRow is the store-local, db-tagged projection of a job_schedules row that
// pgx.RowToStructByName scans into; toDomain maps it to the domain entity.
type scheduleRow struct {
	ID        string     `db:"schedule_id"`
	Name      string     `db:"name"`
	Kind      string     `db:"kind"`
	CronExpr  string     `db:"cron_expr"`
	EverySecs int64      `db:"every_secs"`
	Payload   []byte     `db:"payload"`
	Enabled   bool       `db:"enabled"`
	NextRunAt time.Time  `db:"next_run_at"`
	LastRunAt *time.Time `db:"last_run_at"`
	LastJobID string     `db:"last_job_id"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
}

func (r scheduleRow) toDomain() schedule.Schedule {
	return schedule.Schedule{
		ID:        r.ID,
		Name:      r.Name,
		Kind:      r.Kind,
		Spec:      schedule.Spec{Cron: r.CronExpr, Every: time.Duration(r.EverySecs) * time.Second},
		Payload:   json.RawMessage(r.Payload),
		Enabled:   r.Enabled,
		NextRunAt: r.NextRunAt.UTC(),
		LastRunAt: pgxdb.FromNullTimePtr(r.LastRunAt),
		LastJobID: r.LastJobID,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
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
		const sel = `SELECT ` + scheduleRowColumns + ` FROM job_schedules WHERE name = @name`
		row, err := pgxdb.QueryOne[scheduleRow](ctx, tx, sel, pgx.NamedArgs{"name": in.Name})
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
			const upd = `UPDATE job_schedules SET kind = @kind, cron_expr = @cron, every_secs = @every, payload = @payload, next_run_at = @next_run_at, updated_at = @updated_at WHERE schedule_id = @id`
			if _, err := tx.Exec(ctx, upd, pgx.NamedArgs{
				"kind":        existing.Kind,
				"cron":        cron,
				"every":       every,
				"payload":     payloadValue(existing.Payload),
				"next_run_at": existing.NextRunAt.UTC(),
				"updated_at":  existing.UpdatedAt,
				"id":          existing.ID,
			}); err != nil {
				return err
			}
			out = existing
			return nil
		case errors.Is(err, sdk.ErrNotFound):
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
			const ins = `INSERT INTO job_schedules (` + scheduleColumns + `) VALUES (@id, @name, @kind, @cron, @every, @payload, TRUE, @next_run_at, NULL, NULL, @created_at, @updated_at)`
			if _, err := tx.Exec(ctx, ins, pgx.NamedArgs{
				"id":          sch.ID,
				"name":        sch.Name,
				"kind":        sch.Kind,
				"cron":        cron,
				"every":       every,
				"payload":     payloadValue(sch.Payload),
				"next_run_at": sch.NextRunAt.UTC(),
				"created_at":  sch.CreatedAt,
				"updated_at":  sch.UpdatedAt,
			}); err != nil {
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
	const base = `SELECT ` + scheduleRowColumns + ` FROM job_schedules
		WHERE enabled = TRUE AND next_run_at <= @now
		ORDER BY next_run_at, schedule_id `

	query := base + "LIMIT ALL"
	args := pgx.NamedArgs{"now": now.UTC()}
	if limit > 0 {
		query = base + "LIMIT @limit"
		args["limit"] = limit
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[scheduleRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}

	due := make([]schedule.Schedule, len(items))
	for i, r := range items {
		due[i] = r.toDomain()
	}
	return due, nil
}

// ClaimDue is the pure value compare-and-set on next_run_at: it advances
// next_run_at to newNextRunAt (and last_run_at to now) only when the row's
// current next_run_at still equals prevNextRunAt and the schedule is enabled,
// reporting true when this caller won the (schedule, slot) pair.
//
// The CAS statement is preserved VERBATIM from before the pgx idiom sweep
// (pgx-crud-v1 P5 directive): the affected-rows bool contract is load-bearing, so
// its args stay positional rather than risk any observable change from a NamedArgs
// rewrite.
func (s *Schedules) ClaimDue(ctx context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	const q = `UPDATE job_schedules SET next_run_at = $1, last_run_at = $2, updated_at = $2
		WHERE schedule_id = $3 AND next_run_at = $4 AND enabled = TRUE`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, newNextRunAt.UTC(), now.UTC(), id, prevNextRunAt.UTC())
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SetLastJob records the id of the job fired for the most recent slot. A missing
// id yields sdk.ErrNotFound.
func (s *Schedules) SetLastJob(ctx context.Context, id, jobID string, now time.Time) error {
	const q = `UPDATE job_schedules SET last_job_id = @last_job_id, updated_at = @updated_at WHERE schedule_id = @id`
	return s.execAffecting(ctx, q, pgx.NamedArgs{"last_job_id": jobID, "updated_at": now.UTC(), "id": id})
}

// Get returns the schedule with the given id, or sdk.ErrNotFound.
func (s *Schedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	const q = `SELECT ` + scheduleRowColumns + ` FROM job_schedules WHERE schedule_id = @id`
	row, err := pgxdb.QueryOne[scheduleRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return schedule.Schedule{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor- or offset-paginated page of schedules, in the resolved
// order (default created_at DESC, schedule_id DESC).
func (s *Schedules) List(ctx context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	lq := pgxdb.ListQuery[scheduleRow]{
		BaseSQL:      `SELECT ` + scheduleRowColumns + ` FROM job_schedules`,
		OrderFields:  schedule.OrderFields,
		DefaultOrder: schedule.DefaultOrder,
		PK:           "schedule_id",
		OrderValueOf: func(r scheduleRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r scheduleRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, lq, req)
	if err != nil {
		return crud.Page[schedule.Schedule]{}, err
	}
	return crud.MapPage(page, scheduleRow.toDomain), nil
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// sdk.ErrNotFound.
func (s *Schedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	const q = `UPDATE job_schedules SET enabled = @enabled, updated_at = @updated_at WHERE schedule_id = @id`
	return s.execAffecting(ctx, q, pgx.NamedArgs{"enabled": enabled, "updated_at": now.UTC(), "id": id})
}

// Delete removes a schedule; a missing id yields sdk.ErrNotFound.
func (s *Schedules) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM job_schedules WHERE schedule_id = @id`
	return s.execAffecting(ctx, q, pgx.NamedArgs{"id": id})
}

// execAffecting runs a write that must touch exactly one row, mapping zero rows
// affected to sdk.ErrNotFound. Driver errors are already mapped by the connector.
func (s *Schedules) execAffecting(ctx context.Context, query string, args pgx.NamedArgs) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, query, args)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
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
