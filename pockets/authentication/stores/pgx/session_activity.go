package pgx

import (
	"context"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// sessionActivityMigration is the file that adds sessions.last_rotated_at;
// ProbeSchema names it.
const sessionActivityMigration = "0029_session_activity.sql"

// sessionActivityColumns is the wired-only column list: sessionColumns plus
// the 0029 stamp. Only the ActivityRepository methods name it, so a host that
// leaves idle expiry unwired never reads a column it may not have migrated.
const sessionActivityColumns = sessionColumns + ", last_rotated_at"

// probeSessionActivitySQL resolves sessions exactly as the store's statements
// do — the qualified name under WithSchema, else the connection's search_path —
// so a same-named table in another schema can never satisfy it.
const probeSessionActivitySQL = `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_attribute
		WHERE attrelid = pg_catalog.to_regclass($1::pg_catalog.text) AND attname = $2 AND attnum > 0 AND NOT attisdropped
	)`

var _ session.ActivityRepository = (*SessionStore)(nil)

// sessionActivityRow is a sessions row plus its nullable activity stamp.
type sessionActivityRow struct {
	sessionRow
	LastRotatedAt *time.Time `db:"last_rotated_at"`
}

// SRE9: every ActivityRepository method runs on s.db, the pool, never on a
// transaction the context carries (s.db.QuerierFrom). A refresh reached
// inside a host Transact under forced RLS would otherwise CAS-miss on a
// connection that cannot see the row and burn the session as reuse.

// GetByRefreshHashWithActivity is GetByRefreshHash plus the row's
// last_rotated_at, read in the same SELECT. A NULL stamp is session.Activity{}.
func (s *SessionStore) GetByRefreshHashWithActivity(ctx context.Context, hash string) (session.Session, session.RefreshMatch, session.Activity, error) {
	if hash == "" {
		return session.Session{}, 0, session.Activity{}, sdk.ErrNotFound
	}
	q := `SELECT ` + sessionActivityColumns + ` FROM ` + s.table(sessionsTable) + `
		WHERE refresh_token_hash = @hash OR previous_refresh_token_hash = @hash`
	row, err := pgxdb.QueryOne[sessionActivityRow](ctx, s.db, q, pgx.NamedArgs{"hash": hash})
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	sess, err := row.toDomain()
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	match := session.RefreshMatchCurrent
	if sess.RefreshTokenHash != hash {
		match = session.RefreshMatchPrevious
	}
	var activity session.Activity
	if row.LastRotatedAt != nil {
		activity = session.Activity{LastRotatedAt: row.LastRotatedAt.UTC(), Stamped: true}
	}
	return sess, match, activity, nil
}

// RotateAt is Rotate's compare-and-swap plus last_rotated_at = now (the app
// clock, truncated to PostgreSQL's microsecond) in one UPDATE. Zero rows
// affected → ErrRotationConflict.
func (s *SessionStore) RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error {
	q := `UPDATE ` + s.table(sessionsTable) + `
		SET refresh_token_hash = @new_hash, previous_refresh_token_hash = @expected, previous_used = false, rotation_count = rotation_count + 1, last_rotated_at = @now
		WHERE id = @id AND refresh_token_hash = @expected AND session_profile IN ('', 'first_party')`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"new_hash": newHash,
		"expected": expectedCurrentHash,
		"now":      activityStamp(now),
		"id":       id,
	})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if n == 0 {
		return session.ErrRotationConflict
	}
	return nil
}

// DeleteIfIdle deletes the row only while its stamp (NULL matching NULL) and
// rotation_count are still the observed ones, reporting whether this call
// deleted it. A row that moved on or is gone is false, never an error.
func (s *SessionStore) DeleteIfIdle(ctx context.Context, id string, observed session.Activity, observedRotationCount int) (bool, error) {
	var stamp *time.Time
	if observed.Stamped {
		t := activityStamp(observed.LastRotatedAt)
		stamp = &t
	}
	q := `DELETE FROM ` + s.table(sessionsTable) + `
		WHERE id = @id AND last_rotated_at IS NOT DISTINCT FROM @observed::timestamptz AND rotation_count = @count`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"id":       id,
		"observed": stamp,
		"count":    observedRotationCount,
	})
	if err != nil {
		return false, pgxdb.MapError(err)
	}
	return n == 1, nil
}

// ProbeSchema refuses a database without migration 0029's column with
// sdk.ErrNotFound naming the file. An infrastructure failure is returned via
// MapError, never as a missing column.
func (s *SessionStore) ProbeSchema(ctx context.Context) error {
	const column = "last_rotated_at"
	var exists bool
	if err := s.db.QueryRow(ctx, probeSessionActivitySQL, s.table(sessionsTable), column).Scan(&exists); err != nil {
		return pgxdb.MapError(err)
	}
	if !exists {
		return fmt.Errorf("authentication pgx store: %s.%s column missing — apply migration %s from the %q migration source before boot: %w",
			s.table(sessionsTable), column, sessionActivityMigration, "authentication", sdk.ErrNotFound)
	}
	return nil
}

// activityStamp is a stamp as PostgreSQL stores it: UTC, truncated to the
// microsecond, so the write and the DeleteIfIdle comparison encode alike.
func activityStamp(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}
