package turso

import (
	"context"
	"database/sql"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// SessionStore implements session.SessionRepository over a libSQL database. A
// session row is the revocable anchor for an authenticated user, keyed by its
// app-minted id and carrying refresh-rotation state: the SHA-256 hash of the live
// refresh token, a single previous (grace) slot with a consumed flag, and a
// rotation counter. The service hashes every refresh token before it reaches this
// store, so the hashes persisted and matched on are opaque here (the store does no
// hashing). Get enforces expired-at-read; GetByRefreshHash returns the row verbatim
// (no expiry filter — expiry is a service branch); Rotate and ConsumeGrace are
// compare-and-swap UPDATEs that report a lost CAS as session.ErrRotationConflict.
type SessionStore struct {
	db *tursodb.DB
}

var _ session.SessionRepository = (*SessionStore)(nil)

// NewSessionStore returns a SessionStore backed by db.
func NewSessionStore(db *tursodb.DB) *SessionStore {
	return &SessionStore{db: db}
}

const sessionColumns = "id, user_id, refresh_token_hash, previous_refresh_token_hash, previous_used, rotation_count, authenticated_at, authentication_methods, assurance_level, created_at, expires_at"

// sessionRow is the store-local, db-tagged projection of a sessions row. The
// nullable previous slot scans into sql.NullString so a NULL (a fresh, never-rotated
// session) reads back as the empty string; previous_used scans the 0/1 INTEGER via
// turso.Bool. The auth-v3 §5.0 metadata columns back Session.Authentication:
// authenticated_at is nullable (NULL ↔ the zero "not recorded" sentinel) via
// turso.NullTime, authentication_methods is a JSON array of honest descriptors
// (empty string maps to no methods), assurance_level is the recorded AssuranceLevel
// string.
type sessionRow struct {
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
}

func (r sessionRow) toDomain() (session.Session, error) {
	methods, err := decodeMethods(r.AuthenticationMethods)
	if err != nil {
		return session.Session{}, err
	}
	return session.Session{
		ID:                       r.ID,
		UserID:                   r.UserID,
		RefreshTokenHash:         r.RefreshTokenHash,
		PreviousRefreshTokenHash: r.PreviousRefreshTokenHash.String,
		PreviousUsed:             bool(r.PreviousUsed),
		RotationCount:            r.RotationCount,
		CreatedAt:                r.CreatedAt.Time,
		ExpiresAt:                r.ExpiresAt.Time,
		Authentication: session.AuthenticationMetadata{
			AuthenticatedAt: r.AuthenticatedAt.Time,
			Methods:         methods,
			Assurance:       session.AssuranceLevel(r.AssuranceLevel),
		},
	}, nil
}

// nullHash maps an empty previous-slot hash to a SQL NULL so the partial index and
// the empty-previous guard hold (a fresh row must never store an empty string).
func nullHash(h string) any {
	if h == "" {
		return nil
	}
	return h
}

// Create persists a new session; a colliding refresh_token_hash → sdk.ErrAlreadyExists
// (the unique index, routed through MapError).
func (s *SessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	methods, err := encodeMethods(sess.Authentication.Methods)
	if err != nil {
		return session.Session{}, err
	}
	const q = `INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.Exec(ctx, q,
		sess.ID, sess.UserID, sess.RefreshTokenHash, nullHash(sess.PreviousRefreshTokenHash),
		tursodb.BoolToInt(sess.PreviousUsed), sess.RotationCount,
		tursodb.FormatNullTime(sess.Authentication.AuthenticatedAt), methods, string(sess.Authentication.Assurance),
		tursodb.FormatTime(sess.CreatedAt), tursodb.FormatTime(sess.ExpiresAt),
	)
	if err != nil {
		return session.Session{}, tursodb.MapError(err)
	}
	return sess, nil
}

// Get returns the live session for id: unknown → sdk.ErrNotFound,
// present-but-expired → sdk.ErrExpired (checked against the read clock).
func (s *SessionStore) Get(ctx context.Context, id string) (session.Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions WHERE id = ?`
	row, err := tursodb.QueryOne[sessionRow](ctx, s.db, q, id)
	if err != nil {
		return session.Session{}, err
	}
	sess, err := row.toDomain()
	if err != nil {
		return session.Session{}, err
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return sess, nil
}

// GetByRefreshHash returns the row whose current OR previous refresh-token hash
// equals hash, verbatim (no expiry filter), reporting which slot matched. An empty
// hash never matches; no match → sdk.ErrNotFound.
func (s *SessionStore) GetByRefreshHash(ctx context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if hash == "" {
		return session.Session{}, 0, sdk.ErrNotFound
	}
	const q = `SELECT ` + sessionColumns + ` FROM sessions
		WHERE refresh_token_hash = ? OR previous_refresh_token_hash = ?`
	row, err := tursodb.QueryOne[sessionRow](ctx, s.db, q, hash, hash)
	if err != nil {
		return session.Session{}, 0, err
	}
	sess, err := row.toDomain()
	if err != nil {
		return session.Session{}, 0, err
	}
	match := session.RefreshMatchCurrent
	if sess.RefreshTokenHash != hash {
		match = session.RefreshMatchPrevious
	}
	return sess, match, nil
}

// Rotate compare-and-swaps the live refresh token: it applies only when the row's
// current refresh_token_hash still equals expectedCurrentHash, moving that hash into
// the previous slot, clearing previous_used, and bumping rotation_count — WITHOUT
// touching expires_at (fixed horizon, D2). Zero rows affected → ErrRotationConflict.
func (s *SessionStore) Rotate(ctx context.Context, id, expectedCurrentHash, newHash string) error {
	const q = `UPDATE sessions
		SET refresh_token_hash = ?, previous_refresh_token_hash = ?, previous_used = 0, rotation_count = rotation_count + 1
		WHERE id = ? AND refresh_token_hash = ?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, newHash, expectedCurrentHash, id, expectedCurrentHash)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return session.ErrRotationConflict
	}
	return nil
}

// ConsumeGrace compare-and-swaps the previous slot's consumed flag: it flips
// previous_used to true only when previous_refresh_token_hash equals previousHash AND
// previous_used is still false. Zero rows affected → ErrRotationConflict.
func (s *SessionStore) ConsumeGrace(ctx context.Context, id, previousHash string) error {
	const q = `UPDATE sessions
		SET previous_used = 1
		WHERE id = ? AND previous_refresh_token_hash = ? AND previous_used = 0`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, id, previousHash)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return session.ErrRotationConflict
	}
	return nil
}

// Delete removes the session for id; unknown → sdk.ErrNotFound.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// DeleteByUser removes every session for userID. It is bulk and idempotent: zero
// matching rows returns nil (never sdk.ErrNotFound), so it doubles as the
// logout-everywhere primitive a password change uses.
func (s *SessionStore) DeleteByUser(ctx context.Context, userID string) error {
	if _, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return tursodb.MapError(err)
	}
	return nil
}
