package pgx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// scheduleColumns is the job_schedules column list, in Ensure's INSERT order.
const scheduleColumns = "schedule_id, name, kind, tenant_id, cron_expr, every_secs, payload, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at"

// scheduleRowColumns is the struct-scan projection for the NamedArgs read paths:
// every column is name-aliased so pgx.RowToStructByName matches it against
// scheduleRow's db tags. Nullable tenant_id/cron_expr/every_secs/last_job_id are
// COALESCEd so they scan into plain scalars; only last_run_at stays nullable
// (*time.Time).
const scheduleRowColumns = "schedule_id, name, kind, COALESCE(tenant_id, '') AS tenant_id, COALESCE(cron_expr, '') AS cron_expr, COALESCE(every_secs, 0) AS every_secs, payload, enabled, next_run_at, last_run_at, COALESCE(last_job_id, '') AS last_job_id, created_at, updated_at"

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules persists schedule templates and their pending occurrences.
type Schedules struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

// table renders one of this store's table names under the configured schema —
// the single chokepoint every statement in the file qualifies through.
func (s *Schedules) table(name string) string { return s.schema.Table(name) }

// scheduleRow is the store-local, db-tagged projection of a job_schedules row that
// pgx.RowToStructByName scans into; toDomain maps it to the domain entity.
type scheduleRow struct {
	ID        string     `db:"schedule_id"`
	Name      string     `db:"name"`
	Kind      string     `db:"kind"`
	TenantID  string     `db:"tenant_id"`
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
		TenantID:  r.TenantID,
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

// NewScheduleStore returns a Schedules store backed by db, applying opts
// (WithSchema). WithLease is accepted and ignored: schedules hold no claim
// lease, so the option exists here only because Repositories passes one opts set
// to both stores.
// It panics if db is nil; the caller owns the database lifecycle.
func NewScheduleStore(db *pgxdb.DB, opts ...Option) *Schedules {
	if db == nil {
		panic("jobs pgx: NewScheduleStore received a nil database")
	}
	cfg := newConfig(opts)
	return &Schedules{db: db, schema: cfg.schema}
}

// Ensure upserts by Name in one transaction: it creates the schedule (enabled,
// next_run_at = next) or updates the existing one's kind, spec, and payload,
// advancing next_run_at to next only when the spec changed.
func (s *Schedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	now := time.Now().UTC()
	cron, every := specColumns(in.Spec)
	q := `INSERT INTO ` + s.table("job_schedules") + ` AS current (` + scheduleColumns + `)
 VALUES (@id, @name, @kind, @tenant_id, @cron, @every, @payload, TRUE, @next, NULL, NULL, @now, @now)
 ON CONFLICT (name) DO UPDATE SET kind = EXCLUDED.kind, tenant_id = EXCLUDED.tenant_id,
 cron_expr = EXCLUDED.cron_expr, every_secs = EXCLUDED.every_secs, payload = EXCLUDED.payload,
 next_run_at = CASE WHEN current.cron_expr IS DISTINCT FROM EXCLUDED.cron_expr
 OR current.every_secs IS DISTINCT FROM EXCLUDED.every_secs THEN EXCLUDED.next_run_at ELSE current.next_run_at END,
 updated_at = GREATEST(current.updated_at, EXCLUDED.updated_at)
 RETURNING ` + scheduleRowColumns
	row, err := pgxdb.QueryOne[scheduleRow](ctx, s.db, q, pgx.NamedArgs{"id": newID("sched"), "name": in.Name, "kind": in.Kind, "tenant_id": nullString(in.TenantID), "cron": cron, "every": every, "payload": payloadValue(in.Payload), "next": next.UTC(), "now": now})
	if err != nil {
		return schedule.Schedule{}, err
	}
	return row.toDomain(), nil
}

// ListDue returns up to limit enabled schedules whose next_run_at <= now, ordered
// by (next_run_at, schedule_id) so the batch is deterministic. A non-positive
// limit returns all due schedules. A non-empty kinds (#37) restricts the scan to
// those kinds inside the query, before the limit.
func (s *Schedules) ListDue(ctx context.Context, now time.Time, limit int, kinds []string) ([]schedule.Schedule, error) {
	where := `WHERE enabled = TRUE AND next_run_at <= @now AND NOT EXISTS (SELECT 1 FROM ` + s.table("job_schedule_occurrences") + ` pending WHERE pending.schedule_id = ` + s.table("job_schedules") + `.schedule_id)`
	args := pgx.NamedArgs{"now": now.UTC()}
	if len(kinds) > 0 {
		where += ` AND kind = ANY(@kinds)`
		args["kinds"] = kinds
	}
	base := `SELECT ` + scheduleRowColumns + ` FROM ` + s.table("job_schedules") + `
		` + where + `
		ORDER BY next_run_at, schedule_id `

	query := base + "LIMIT ALL"
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

// Get returns the schedule with the given id, or sdk.ErrNotFound.
func (s *Schedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	q := `SELECT ` + scheduleRowColumns + ` FROM ` + s.table("job_schedules") + ` WHERE schedule_id = @id`
	row, err := pgxdb.QueryOne[scheduleRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return schedule.Schedule{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor- or offset-paginated page of schedules, in the resolved
// order (default created_at DESC, schedule_id DESC).
func (s *Schedules) List(ctx context.Context, req list.Request) (list.Page[schedule.Schedule], error) {
	lq := pgxdb.ListQuery[scheduleRow]{
		BaseSQL:      `SELECT ` + scheduleRowColumns + ` FROM ` + s.table("job_schedules"),
		OrderFields:  schedule.OrderFields,
		DefaultOrder: schedule.DefaultOrder,
		PK:           "schedule_id",
		OrderValueOf: func(r scheduleRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r scheduleRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, lq, req)
	if err != nil {
		return list.Page[schedule.Schedule]{}, err
	}
	return list.MapPage(page, scheduleRow.toDomain), nil
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// sdk.ErrNotFound.
func (s *Schedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	q := `UPDATE ` + s.table("job_schedules") + ` SET enabled = @enabled, updated_at = GREATEST(updated_at, @updated_at) WHERE schedule_id = @id`
	return s.execAffecting(ctx, q, pgx.NamedArgs{"enabled": enabled, "updated_at": now.UTC(), "id": id})
}

// Delete removes a schedule; a missing id yields sdk.ErrNotFound.
func (s *Schedules) Delete(ctx context.Context, id string) error {
	q := `DELETE FROM ` + s.table("job_schedules") + ` WHERE schedule_id = @id`
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
