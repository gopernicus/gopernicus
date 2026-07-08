package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// SessionStore implements session.SessionRepository over a libSQL database.
// Sessions are opaque: the token column holds whatever value the auth service
// supplies as the primary key — the service hashes the cookie token before every
// call (design §7.3), so the store persists and looks up by that opaque value
// and does no hashing itself. Get enforces expired-at-read: a row past its
// expires_at surfaces errs.ErrExpired rather than a dead session.
type SessionStore struct {
	db *tursodb.DB
}

var _ session.SessionRepository = (*SessionStore)(nil)

// NewSessionStore returns a SessionStore backed by db.
func NewSessionStore(db *tursodb.DB) *SessionStore {
	return &SessionStore{db: db}
}

const sessionColumns = "token, user_id, created_at, expires_at"

// Create persists a new session.
func (s *SessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	const q = `INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, sess.Token, sess.UserID, tursodb.FormatTime(sess.CreatedAt), tursodb.FormatTime(sess.ExpiresAt))
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}

// Get returns the live session for token: unknown → errs.ErrNotFound,
// present-but-expired → errs.ErrExpired (checked against the read clock).
func (s *SessionStore) Get(ctx context.Context, token string) (session.Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions WHERE token = ?`
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
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM sessions WHERE token = ?", token)
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
	_, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// scanSession scans one sessions row, mapping sql.ErrNoRows to errs.ErrNotFound.
func scanSession(sc scanner) (session.Session, error) {
	var (
		sess                 session.Session
		createdAt, expiresAt string
	)
	if err := sc.Scan(&sess.Token, &sess.UserID, &createdAt, &expiresAt); err != nil {
		return session.Session{}, tursodb.MapError(err)
	}
	var err error
	if sess.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return session.Session{}, err
	}
	if sess.ExpiresAt, err = tursodb.ParseTime(expiresAt); err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
