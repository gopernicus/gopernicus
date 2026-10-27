package turso

import (
	"context"
	"database/sql"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// sessionActivityMigration is the file that adds sessions.last_rotated_at;
// ProbeSchema names it.
const sessionActivityMigration = "0029_session_activity.sql"

// sessionActivityColumns is the wired-only column list: sessionColumns plus
// the 0029 stamp. Only the ActivityRepository methods name it, so a host that
// leaves idle expiry unwired never reads a column it may not have migrated.
const sessionActivityColumns = sessionColumns + ", last_rotated_at"

var _ session.ActivityRepository = (*SessionStore)(nil)

// sessionActivityRow is sessionRow plus the nullable activity stamp, stored
// in created_at's fixed-width TEXT encoding. tursodb.ScanStruct does not
// flatten an embedded struct, so the fields are repeated; the strict scan
// refuses any column sessionColumns gains that this row lacks, and a unit test
// pins the field set to sessionRow's.
type sessionActivityRow struct {
	ID                       string           `db:"id"`
	UserID                   string           `db:"user_id"`
	RefreshTokenHash         string           `db:"refresh_token_hash"`
	PreviousRefreshTokenHash sql.NullString   `db:"previous_refresh_token_hash"`
	PreviousUsed             tursodb.Bool     `db:"previous_used"`
	RotationCount            int              `db:"rotation_count"`
	AuthenticatedAt          tursodb.NullTime `db:"authenticated_at"`
	AuthenticationMethods    string           `db:"authentication_methods"`
	AssuranceLevel           string           `db:"assurance_level"`
	CreatedAt                tursodb.Time     `db:"created_at"`
	ExpiresAt                tursodb.Time     `db:"expires_at"`
	Profile                  string           `db:"session_profile"`
	Delegation               string           `db:"delegation"`
	LastRotatedAt            tursodb.NullTime `db:"last_rotated_at"`
}

// sessionRow drops the stamp, so the row maps through sessionRow.toDomain
// exactly as GetByRefreshHash's does.
func (r sessionActivityRow) sessionRow() sessionRow {
	return sessionRow{
		ID:                       r.ID,
		UserID:                   r.UserID,
		RefreshTokenHash:         r.RefreshTokenHash,
		PreviousRefreshTokenHash: r.PreviousRefreshTokenHash,
		PreviousUsed:             r.PreviousUsed,
		RotationCount:            r.RotationCount,
		AuthenticatedAt:          r.AuthenticatedAt,
		AuthenticationMethods:    r.AuthenticationMethods,
		AssuranceLevel:           r.AssuranceLevel,
		CreatedAt:                r.CreatedAt,
		ExpiresAt:                r.ExpiresAt,
		Profile:                  r.Profile,
		Delegation:               r.Delegation,
	}
}

// SRE9: every ActivityRepository method runs on s.db, the pool, never on a
// transaction the context carries (tursodb.QuerierFrom). A refresh reached
// inside a host Transact would otherwise CAS-miss on a connection that cannot
// see the row and burn the session as reuse.

// GetByRefreshHashWithActivity is GetByRefreshHash plus the row's
// last_rotated_at, read in the same SELECT. A NULL stamp is session.Activity{}.
func (s *SessionStore) GetByRefreshHashWithActivity(ctx context.Context, hash string) (session.Session, session.RefreshMatch, session.Activity, error) {
	if hash == "" {
		return session.Session{}, 0, session.Activity{}, sdk.ErrNotFound
	}
	const q = `SELECT ` + sessionActivityColumns + ` FROM sessions
		WHERE refresh_token_hash = ? OR previous_refresh_token_hash = ?`
	row, err := tursodb.QueryOne[sessionActivityRow](ctx, s.db, q, hash, hash)
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	sess, err := row.sessionRow().toDomain()
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	match := session.RefreshMatchCurrent
	if sess.RefreshTokenHash != hash {
		match = session.RefreshMatchPrevious
	}
	return sess, match, session.Activity{LastRotatedAt: row.LastRotatedAt.Time, Stamped: row.LastRotatedAt.Valid}, nil
}

// RotateAt is Rotate's compare-and-swap plus last_rotated_at = now (the app
// clock, in created_at's encoding) in one UPDATE. Zero rows affected →
// ErrRotationConflict.
func (s *SessionStore) RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error {
	const q = `UPDATE sessions
		SET refresh_token_hash = ?, previous_refresh_token_hash = ?, previous_used = 0, rotation_count = rotation_count + 1, last_rotated_at = ?
		WHERE id = ? AND refresh_token_hash = ? AND session_profile IN ('', 'first_party')`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, newHash, expectedCurrentHash, tursodb.FormatTime(now), id, expectedCurrentHash)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return session.ErrRotationConflict
	}
	return nil
}

// DeleteIfIdle deletes the row only while its stamp (IS: NULL matching NULL)
// and rotation_count are still the observed ones, reporting whether this call
// deleted it. The observed stamp is encoded exactly as RotateAt wrote it, so
// the match is text equality; a row that moved on or is gone is false, never
// an error.
func (s *SessionStore) DeleteIfIdle(ctx context.Context, id string, observed session.Activity, observedRotationCount int) (bool, error) {
	var stamp any
	if observed.Stamped {
		stamp = tursodb.FormatTime(observed.LastRotatedAt)
	}
	const q = `DELETE FROM sessions WHERE id = ? AND last_rotated_at IS ? AND rotation_count = ?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, id, stamp, observedRotationCount)
	if err != nil {
		return false, tursodb.MapError(err)
	}
	return n == 1, nil
}

// ProbeSchema refuses a database without migration 0029's column with
// sdk.ErrNotFound naming the file.
func (s *SessionStore) ProbeSchema(ctx context.Context) error {
	return probeColumn(ctx, s.db, "sessions", "last_rotated_at", sessionActivityMigration)
}
