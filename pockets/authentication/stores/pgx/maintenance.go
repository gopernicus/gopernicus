package pgx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The retention objects migration 0024 creates. purgeFunction is SECURITY
// DEFINER, owned by the table's owner; floorFunction is the plain floor it
// reads. Their argument lists are written pg_catalog-qualified, as
// to_regprocedure and the posture probe bind them.
const (
	retentionMigration  = "0023_retention.sql"
	railRetentionFile   = "0024_security_events_retention.sql"
	purgeFunction       = "security_events_purge_before"
	purgeFunctionArgs   = "(pg_catalog.timestamptz, pg_catalog.int4)"
	floorFunction       = "security_events_retention_floor"
	floorFunctionArgs   = "()"
	belowFloorSQLState  = "22023"
	forbiddenSQLState   = "42501"
	undefinedFnSQLState = "42883"
	functionDeniedLead  = "permission denied for function"
	invitationTerminals = "'accepted', 'declined', 'cancelled'"
)

// MaintenanceStore implements maintenance.Repository over PostgreSQL. Every
// purge is ONE bounded statement whose victims are chosen by a FOR UPDATE SKIP
// LOCKED subselect, so concurrent passes never delete a row twice or wait on
// each other. The rail purge is the one exception to plain SQL: the runtime
// role holds no DELETE on security_events, so it calls the 0024 SECURITY
// DEFINER function, which refuses a cutoff younger than the database floor and
// appends the security_events_purged row in the same call.
type MaintenanceStore struct {
	db *pgxdb.DB
	qualified
}

var _ maintenance.Repository = (*MaintenanceStore)(nil)

// NewMaintenanceStore returns a MaintenanceStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewMaintenanceStore(db *pgxdb.DB, opts ...Option) *MaintenanceStore {
	if db == nil {
		panic("authentication pgx: NewMaintenanceStore received a nil database")
	}
	return &MaintenanceStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// archivedEventRow is a rail row with the evidence columns 0021 adds.
type archivedEventRow struct {
	ID          string         `db:"id"`
	UserID      string         `db:"user_id"`
	ActorType   string         `db:"actor_type"`
	ActorID     string         `db:"actor_id"`
	EventType   string         `db:"event_type"`
	EventStatus string         `db:"event_status"`
	Details     map[string]any `db:"details"`
	IPAddress   string         `db:"ip_address"`
	UserAgent   string         `db:"user_agent"`
	RequestID   string         `db:"request_id"`
	TraceID     string         `db:"trace_id"`
	CreatedAt   time.Time      `db:"created_at"`
	Seq         *int64         `db:"seq"`
	RecordedAt  *time.Time     `db:"recorded_at"`
}

func (r archivedEventRow) toDomain() maintenance.ArchivedEvent {
	evt := securityEventRow{
		ID: r.ID, UserID: r.UserID, ActorType: r.ActorType, ActorID: r.ActorID,
		EventType: r.EventType, EventStatus: r.EventStatus, Details: r.Details,
		IPAddress: r.IPAddress, UserAgent: r.UserAgent, RequestID: r.RequestID,
		TraceID: r.TraceID, CreatedAt: r.CreatedAt,
	}.toDomain()
	return maintenance.ArchivedEvent{SecurityEvent: evt, Seq: r.Seq, RecordedAt: pgxdb.FromNullTimePtr(r.RecordedAt)}
}

func checkLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("authentication pgx store: limit %d: %w", limit, sdk.ErrInvalidInput)
	}
	return nil
}

// purgeExpired deletes up to limit rows of table whose expires_at <= before,
// oldest first, keyed by key.
func (s *MaintenanceStore) purgeExpired(ctx context.Context, table, key string, before time.Time, limit int) (int, error) {
	if err := checkLimit(limit); err != nil {
		return 0, err
	}
	n, err := pgxdb.ExecAffecting(ctx, s.db, s.purgeExpiredSQL(table, key), pgx.NamedArgs{"before": before.UTC(), "limit": limit})
	return int(n), err
}

// purgeExpiredSQL is the bounded expiry purge of table, keyed by key, bound
// with @before and @limit; the 0023 expires_at index serves its subselect.
func (s *MaintenanceStore) purgeExpiredSQL(table, key string) string {
	t := s.table(table)
	return `DELETE FROM ` + t + ` WHERE ` + key + ` IN (SELECT ` + key + ` FROM ` + t + `
		WHERE expires_at <= @before ORDER BY expires_at LIMIT @limit FOR UPDATE SKIP LOCKED)`
}

// PurgeExpiredSessions deletes up to limit sessions, both profiles, whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredSessions(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, sessionsTable, "id", before, limit)
}

// PurgeExpiredOAuthStates deletes up to limit OAuth states whose expires_at <=
// before.
func (s *MaintenanceStore) PurgeExpiredOAuthStates(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, oauthStatesTable, "token", before, limit)
}

// PurgeExpiredGrants deletes up to limit authentication grants whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredGrants(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, authGrantsTable, "id", before, limit)
}

// PurgeExpiredContactChanges deletes up to limit pending contact changes whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredContactChanges(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, contactChangesTable, "id", before, limit)
}

// PurgeInvitations deletes up to limit invitations that are terminal with
// updated_at <= before or pending with expires_at <= before. An accepting
// claim is never selected.
func (s *MaintenanceStore) PurgeInvitations(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := checkLimit(limit); err != nil {
		return 0, err
	}
	t := s.table(invitationsTable)
	q := `DELETE FROM ` + t + ` WHERE id IN (SELECT id FROM ` + t + `
		WHERE (status IN (` + invitationTerminals + `) AND updated_at <= @before)
			OR (status = 'pending' AND expires_at <= @before)
		LIMIT @limit FOR UPDATE SKIP LOCKED)`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{"before": before.UTC(), "limit": limit})
	return int(n), err
}

// CountStaleAcceptingInvitations counts the accepting invitations whose
// updated_at <= before.
func (s *MaintenanceStore) CountStaleAcceptingInvitations(ctx context.Context, before time.Time) (int, error) {
	var n int
	q := `SELECT count(*) FROM ` + s.table(invitationsTable) + ` WHERE status = 'accepting' AND updated_at <= @before`
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{"before": before.UTC()}).Scan(&n); err != nil {
		return 0, pgxdb.MapError(err)
	}
	return n, nil
}

// PruneOAuth2 deletes up to limit expired rows from each of the three OAuth2
// tables and returns the sum.
func (s *MaintenanceStore) PruneOAuth2(ctx context.Context, now time.Time, limit int) (int, error) {
	if err := checkLimit(limit); err != nil {
		return 0, err
	}
	total := 0
	for _, t := range []struct{ table, key string }{
		{oauthCodesTable, "code_hash"},
		{oauthClientsTable, "id"},
		{oauthHistoryTable, "refresh_token_hash"},
	} {
		n, err := s.purgeExpired(ctx, t.table, t.key, now, limit)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ExportSecurityEvents returns up to limit rail rows with created_at <= before
// after the cursor, ascending (created_at, id), extended in the same statement
// to every row tied at the last created_at. A plain read: the runtime role
// holds no UPDATE on the rail, so no lock clause is taken here.
func (s *MaintenanceStore) ExportSecurityEvents(ctx context.Context, before time.Time, after maintenance.ExportCursor, limit int) ([]maintenance.ArchivedEvent, maintenance.ExportCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, after, err
	}
	t := s.table(securityEventsTable)
	args := pgx.NamedArgs{"before": before.UTC(), "limit": limit}
	window := `created_at <= @before`
	if !after.CreatedAt.IsZero() {
		window += ` AND (created_at, id) > (@after_at, @after_id)`
		args["after_at"], args["after_id"] = after.CreatedAt.UTC(), after.ID
	}
	q := `WITH head AS (
			SELECT created_at FROM ` + t + ` WHERE ` + window + ` ORDER BY created_at, id LIMIT @limit
		)
		SELECT ` + securityEventColumns + `, seq, recorded_at FROM ` + t + `
		WHERE ` + window + ` AND created_at <= (SELECT max(created_at) FROM head)
		ORDER BY created_at, id`
	rows, err := pgxdb.Collect[archivedEventRow](ctx, s.db, q, args)
	if err != nil {
		return nil, after, err
	}
	if len(rows) == 0 {
		return nil, after, nil
	}
	out := make([]maintenance.ArchivedEvent, len(rows))
	for i, r := range rows {
		out[i] = r.toDomain()
	}
	last := out[len(out)-1]
	return out, maintenance.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

// PurgeSecurityEventsBefore calls the 0024 definer function once: it deletes up
// to limit rail rows with created_at <= before, oldest first, and appends the
// security_events_purged row. The arguments are cast to the exact signature so
// no overload can make the call ambiguous, and the name is schema-qualified
// under WithSchema.
func (s *MaintenanceStore) PurgeSecurityEventsBefore(ctx context.Context, before time.Time, limit int) (maintenance.PurgeResult, error) {
	if err := checkLimit(limit); err != nil {
		return maintenance.PurgeResult{}, err
	}
	q := `SELECT cutoff, purged FROM ` + s.table(purgeFunction) + `($1::pg_catalog.timestamptz, $2::pg_catalog.int4)`
	var (
		cutoff time.Time
		purged int64
	)
	if err := s.db.QueryRow(ctx, q, before.UTC(), int32(min(limit, maintenance.MaxBatchSize))).Scan(&cutoff, &purged); err != nil {
		return maintenance.PurgeResult{}, s.purgeError(err)
	}
	return maintenance.PurgeResult{Cutoff: cutoff.UTC(), Count: int(purged)}, nil
}

// purgeError maps the definer call's failures: 22023 is the floor refusal;
// a 42501 naming the function is the missing EXECUTE grant, any other 42501 is
// the rail refusing the function's own delete; 42883 is a database without
// 0024.
func (s *MaintenanceStore) purgeError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == belowFloorSQLState:
			return fmt.Errorf("authentication pgx store: %s: %w", pgErr.Message, maintenance.ErrBelowRetentionFloor)
		case pgErr.Code == forbiddenSQLState && strings.Contains(pgErr.Message, functionDeniedLead):
			return fmt.Errorf("authentication pgx store: the runtime role cannot execute %s; fix: GRANT EXECUTE ON FUNCTION %s TO <runtime role>: %w",
				purgeFunction, s.table(purgeFunction)+purgeFunctionArgs, pgxdb.MapError(err))
		case pgErr.Code == forbiddenSQLState:
			return fmt.Errorf("authentication pgx store: security_events refused the delete of %s — it must be SECURITY DEFINER and owned by a member of the table's owner (ALTER FUNCTION %s SECURITY DEFINER; ALTER FUNCTION %s OWNER TO <table owner>): %w",
				purgeFunction, s.table(purgeFunction)+purgeFunctionArgs, s.table(purgeFunction)+purgeFunctionArgs, pgxdb.MapError(err))
		case pgErr.Code == undefinedFnSQLState && strings.Contains(pgErr.Message, purgeFunction):
			return fmt.Errorf("authentication pgx store: %s is missing — apply migration %s from the %q migration source: %w",
				purgeFunction, railRetentionFile, "authentication", sdk.ErrNotFound)
		}
	}
	return pgxdb.MapError(err)
}
