package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// SessionStore implements session.SessionRepository over a PostgreSQL database.
// Sessions are opaque: the token column holds whatever value the auth service
// supplies as the primary key — the service hashes the cookie token before every
// call (design §7.3), so the store persists and looks up by that opaque value
// and does no hashing itself. Get enforces expired-at-read: a row past its
// expires_at surfaces errs.ErrExpired rather than a dead session.
type SessionStore struct {
	db *pgxdb.DB
}

var _ session.SessionRepository = (*SessionStore)(nil)

// NewSessionStore returns a SessionStore backed by db.
func NewSessionStore(db *pgxdb.DB) *SessionStore {
	return &SessionStore{db: db}
}

const sessionColumns = "token, user_id, created_at, expires_at"

// Create persists a new session.
func (s *SessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	const q = `INSERT INTO sessions (` + sessionColumns + `) VALUES ($1, $2, $3, $4)`
	_, err := s.db.Exec(ctx, q, sess.Token, sess.UserID, sess.CreatedAt.UTC(), sess.ExpiresAt.UTC())
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}

// Get returns the live session for token: unknown → errs.ErrNotFound,
// present-but-expired → errs.ErrExpired (checked against the read clock).
func (s *SessionStore) Get(ctx context.Context, token string) (session.Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions WHERE token = $1`
	sess, err := scanSession(s.db.QueryRow(ctx, q, token))
	if err != nil {
		return session.Session{}, err
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, errs.ErrExpired
	}
	return sess, nil
}

// Delete removes the session for token; unknown → errs.ErrNotFound.
func (s *SessionStore) Delete(ctx context.Context, token string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM sessions WHERE token = $1", token)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// DeleteByUser removes every session for userID. It is bulk and idempotent: zero
// matching rows returns nil (never errs.ErrNotFound), so it doubles as the
// logout-everywhere primitive a password change uses.
func (s *SessionStore) DeleteByUser(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
	return err
}

// scanSession scans one sessions row, mapping pgx.ErrNoRows to errs.ErrNotFound.
func scanSession(sc scanner) (session.Session, error) {
	var (
		sess                 session.Session
		createdAt, expiresAt time.Time
	)
	if err := sc.Scan(&sess.Token, &sess.UserID, &createdAt, &expiresAt); err != nil {
		return session.Session{}, pgxdb.MapError(err)
	}
	sess.CreatedAt = createdAt.UTC()
	sess.ExpiresAt = expiresAt.UTC()
	return sess, nil
}
