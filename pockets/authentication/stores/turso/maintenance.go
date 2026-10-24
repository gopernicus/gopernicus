package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
)

// The retention migrations and the floor table 0024 creates.
const (
	retentionMigration     = "0023_retention.sql"
	railRetentionFile      = "0024_security_events_retention.sql"
	retentionFloorTable    = "security_events_retention"
	railTriggersChangedKey = "rail_triggers_changed"
	invitationTerminals    = "'accepted', 'declined', 'cancelled'"
	purgeCutoffLayout      = "2006-01-02T15:04:05.000000Z"
)

// MaintenanceStore implements maintenance.Repository over libSQL. Every purge
// is one bounded statement; BEGIN IMMEDIATE serializes writers, so concurrent
// passes never delete a row twice.
//
// The rail purge cannot be plain DML: the 0022 triggers abort every DELETE on
// security_events, from every connection. Each call is ONE short transaction
// that captures the table's triggers from sqlite_schema, drops exactly those,
// deletes the batch, re-creates the captured text verbatim and only then
// appends the security_events_purged row, so a host AFTER INSERT trigger sees
// the evidence. Restoring what was captured — never the embedded 0022 — leaves
// a host that declined 0022 without triggers and a host-altered trigger
// unrepaired, and the restored text is byte-identical to what the boot probe
// compares. After COMMIT the trigger set is read again and a difference logs
// ERROR rail_triggers_changed. The floor is row 1 of security_events_retention;
// a cutoff younger than now - floor_days is maintenance.ErrBelowRetentionFloor.
type MaintenanceStore struct {
	db *tursodb.DB

	// fault, when set, runs inside the purge transaction at each named stage
	// — stageDropped after the DROP TRIGGERs, stageRecorded after the evidence
	// row, before COMMIT; an error rolls the transaction back. Tests inject
	// faults through it.
	fault func(ctx context.Context, tx *tursodb.Tx, stage string) error
}

// The purge transaction's fault-injection stages.
const (
	stageDropped  = "dropped"
	stageRecorded = "recorded"
)

var _ maintenance.Repository = (*MaintenanceStore)(nil)

// NewMaintenanceStore returns a MaintenanceStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewMaintenanceStore(db *tursodb.DB) *MaintenanceStore {
	if db == nil {
		panic("authentication turso: NewMaintenanceStore received a nil database")
	}
	return &MaintenanceStore{db: db}
}

// archivedEventRow is a rail row with the recorded_at evidence column 0021
// adds; libSQL keeps no seq.
type archivedEventRow struct {
	ID          string           `db:"id"`
	UserID      string           `db:"user_id"`
	ActorType   string           `db:"actor_type"`
	ActorID     string           `db:"actor_id"`
	EventType   string           `db:"event_type"`
	EventStatus string           `db:"event_status"`
	Details     detailsJSON      `db:"details"`
	IPAddress   string           `db:"ip_address"`
	UserAgent   string           `db:"user_agent"`
	RequestID   string           `db:"request_id"`
	TraceID     string           `db:"trace_id"`
	CreatedAt   tursodb.Time     `db:"created_at"`
	RecordedAt  tursodb.NullTime `db:"recorded_at"`
}

func (r archivedEventRow) toDomain() maintenance.ArchivedEvent {
	evt := securityEventRow{
		ID: r.ID, UserID: r.UserID, ActorType: r.ActorType, ActorID: r.ActorID,
		EventType: r.EventType, EventStatus: r.EventStatus, Details: r.Details,
		IPAddress: r.IPAddress, UserAgent: r.UserAgent, RequestID: r.RequestID,
		TraceID: r.TraceID, CreatedAt: r.CreatedAt,
	}.toDomain()
	return maintenance.ArchivedEvent{SecurityEvent: evt, RecordedAt: r.RecordedAt.TimePtr()}
}

// railTrigger is one trigger on security_events as sqlite_schema stores it.
type railTrigger struct {
	name string
	sql  string
}

func checkLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("authentication turso store: limit %d: %w", limit, sdk.ErrInvalidInput)
	}
	return nil
}

// purgeExpired deletes up to limit rows of table whose expires_at <= before,
// oldest first, keyed by key. table and key are this file's literals.
func (s *MaintenanceStore) purgeExpired(ctx context.Context, table, key string, before time.Time, limit int) (int, error) {
	if err := checkLimit(limit); err != nil {
		return 0, err
	}
	q := `DELETE FROM ` + table + ` WHERE ` + key + ` IN (SELECT ` + key + ` FROM ` + table + ` WHERE expires_at <= ? ORDER BY expires_at LIMIT ?)`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, tursodb.FormatTime(before), limit)
	return int(n), err
}

// PurgeExpiredSessions deletes up to limit sessions, both profiles, whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredSessions(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, "sessions", "id", before, limit)
}

// PurgeExpiredOAuthStates deletes up to limit OAuth states whose expires_at <=
// before.
func (s *MaintenanceStore) PurgeExpiredOAuthStates(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, "oauth_states", "token", before, limit)
}

// PurgeExpiredGrants deletes up to limit authentication grants whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredGrants(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, "authentication_grants", "id", before, limit)
}

// PurgeExpiredContactChanges deletes up to limit pending contact changes whose
// expires_at <= before.
func (s *MaintenanceStore) PurgeExpiredContactChanges(ctx context.Context, before time.Time, limit int) (int, error) {
	return s.purgeExpired(ctx, "contact_changes", "id", before, limit)
}

// PurgeInvitations deletes up to limit invitations that are terminal with
// updated_at <= before or pending with expires_at <= before. An accepting
// claim is never selected.
func (s *MaintenanceStore) PurgeInvitations(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := checkLimit(limit); err != nil {
		return 0, err
	}
	at := tursodb.FormatTime(before)
	n, err := tursodb.ExecAffecting(ctx, s.db, `DELETE FROM invitations WHERE id IN (SELECT id FROM invitations
		WHERE (status IN (`+invitationTerminals+`) AND updated_at <= ?) OR (status = 'pending' AND expires_at <= ?) LIMIT ?)`, at, at, limit)
	return int(n), err
}

// CountStaleAcceptingInvitations counts the accepting invitations whose
// updated_at <= before.
func (s *MaintenanceStore) CountStaleAcceptingInvitations(ctx context.Context, before time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE status = 'accepting' AND updated_at <= ?`,
		tursodb.FormatTime(before)).Scan(&n); err != nil {
		return 0, tursodb.MapError(err)
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
		{"oauth_authorization_codes", "code_hash"},
		{"oauth_clients", "id"},
		{"oauth_refresh_history", "refresh_token_hash"},
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
// to every row tied at the last created_at. Seq is nil: libSQL keeps none.
func (s *MaintenanceStore) ExportSecurityEvents(ctx context.Context, before time.Time, after maintenance.ExportCursor, limit int) ([]maintenance.ArchivedEvent, maintenance.ExportCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, after, err
	}
	window, args := `created_at <= ?`, []any{tursodb.FormatTime(before)}
	if !after.CreatedAt.IsZero() {
		at := tursodb.FormatTime(after.CreatedAt)
		window += ` AND (created_at > ? OR (created_at = ? AND id > ?))`
		args = append(args, at, at, after.ID)
	}
	q := `WITH head AS (SELECT created_at FROM security_events WHERE ` + window + ` ORDER BY created_at, id LIMIT ?)
		SELECT ` + securityEventColumns + `, recorded_at FROM security_events
		WHERE ` + window + ` AND created_at <= (SELECT max(created_at) FROM head)
		ORDER BY created_at, id`
	all := append(append(slices.Clone(args), limit), args...)
	rows, err := s.db.Query(ctx, q, all...)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []maintenance.ArchivedEvent
	for rows.Next() {
		r, err := tursodb.ScanStruct[archivedEventRow](rows)
		if err != nil {
			return nil, after, tursodb.MapError(err)
		}
		out = append(out, r.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, after, tursodb.MapError(err)
	}
	if len(out) == 0 {
		return nil, after, nil
	}
	last := out[len(out)-1]
	return out, maintenance.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

// PurgeSecurityEventsBefore deletes up to limit rail rows with created_at <=
// before, oldest first, in one capture-drop-delete-restore-evidence
// transaction (see the type doc), and appends ONE security_events_purged row,
// a zero count included.
func (s *MaintenanceStore) PurgeSecurityEventsBefore(ctx context.Context, before time.Time, limit int) (maintenance.PurgeResult, error) {
	if err := checkLimit(limit); err != nil {
		return maintenance.PurgeResult{}, err
	}
	limit = min(limit, maintenance.MaxBatchSize)
	cutoff := tursodb.FormatTime(before)
	var (
		captured []railTrigger
		purged   int64
	)
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		floorDays, err := readFloorDays(ctx, tx)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if before.After(now.Add(-time.Duration(floorDays) * 24 * time.Hour)) {
			return fmt.Errorf("authentication turso store: cutoff %s is below the retention floor of %d days: %w",
				before.UTC().Format(time.RFC3339Nano), floorDays, maintenance.ErrBelowRetentionFloor)
		}
		// (1) Capture, and close the rows before any DDL: an open statement
		// on a local connection fails DDL with SQLITE_LOCKED.
		if captured, err = railTriggers(ctx, tx); err != nil {
			return err
		}
		// (2) Drop exactly the captured triggers.
		for _, t := range captured {
			if _, err := tx.Exec(ctx, `DROP TRIGGER `+quoteName(t.name)); err != nil {
				return err
			}
		}
		if err := s.inject(ctx, tx, stageDropped); err != nil {
			return err
		}
		// (3) The bounded delete.
		res, err := tx.Exec(ctx, `DELETE FROM security_events WHERE id IN (SELECT id FROM security_events WHERE created_at <= ? ORDER BY created_at, id LIMIT ?)`, cutoff, limit)
		if err != nil {
			return err
		}
		if purged, err = res.RowsAffected(); err != nil {
			return tursodb.MapError(err)
		}
		// (4) Restore the captured text verbatim.
		for _, t := range captured {
			if _, err := tx.Exec(ctx, t.sql); err != nil {
				return err
			}
		}
		// (5) The evidence, after the restore, by plain INSERT.
		if _, err := insertSecurityEvent(ctx, tx, securityevent.SecurityEvent{
			Actor:       securityevent.Principal{Type: "system", ID: "maintenance"},
			EventType:   securityevent.TypeSecurityEventsPurged,
			EventStatus: securityevent.StatusSuccess,
			Details: map[string]any{
				"cutoff":     before.UTC().Format(purgeCutoffLayout),
				"count":      purged,
				"floor_days": floorDays,
				"max_rows":   limit,
			},
			CreatedAt: now,
		}); err != nil {
			return err
		}
		return s.inject(ctx, tx, stageRecorded)
	})
	if err != nil {
		return maintenance.PurgeResult{}, err
	}
	// (6) After COMMIT, the trigger set must be what was captured.
	if after, err := railTriggers(ctx, s.db); err != nil || !slices.Equal(after, captured) {
		slog.Default().ErrorContext(ctx, "authentication turso store: the security_events trigger set differs from the one the purge captured",
			"error_kind", railTriggersChangedKey, "captured", triggerNames(captured), "after", triggerNames(after), "read_error", err)
	}
	return maintenance.PurgeResult{Cutoff: before, Count: int(purged)}, nil
}

// inject runs the fault hook at stage, if one is set.
func (s *MaintenanceStore) inject(ctx context.Context, tx *tursodb.Tx, stage string) error {
	if s.fault == nil {
		return nil
	}
	return s.fault(ctx, tx, stage)
}

// readFloorDays reads row 1 of the floor table; a missing row (or table) is a
// refusal naming 0024, never a default.
func readFloorDays(ctx context.Context, q tursodb.Querier) (int, error) {
	var days int
	err := q.QueryRow(ctx, `SELECT floor_days FROM `+retentionFloorTable+` WHERE id = 1`).Scan(&days)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("authentication turso store: %s row 1 missing — apply migration %s from the %q migration source before boot: %w",
			retentionFloorTable, railRetentionFile, "authentication", sdk.ErrNotFound)
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return 0, fmt.Errorf("authentication turso store: %s table missing — apply migration %s from the %q migration source before boot: %w",
			retentionFloorTable, railRetentionFile, "authentication", sdk.ErrNotFound)
	case err != nil:
		return 0, tursodb.MapError(err)
	}
	return days, nil
}

// railTriggers reads the triggers on security_events, in name order, closing
// the rows before it returns.
func railTriggers(ctx context.Context, q tursodb.Querier) ([]railTrigger, error) {
	rows, err := q.Query(ctx, `SELECT name, sql FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = 'security_events' COLLATE NOCASE ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []railTrigger
	for rows.Next() {
		var t railTrigger
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, rows.Close()
}

// quoteName renders a catalog name as an SQL identifier.
func quoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func triggerNames(ts []railTrigger) []string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.name
	}
	return names
}
