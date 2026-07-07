package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// SessionStore implements session.SessionRepository over a libSQL database.
// Sessions are opaque: the token is stored plainly as the primary key and the
// service looks a session up by that same raw token (no hashing), matching the
// session entity and the storetest reference. Get enforces expired-at-read: a
// row past its expires_at surfaces errs.ErrExpired rather than a dead session.
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
	_, err := s.db.Exec(ctx, q, sess.Token, sess.UserID, formatTS(sess.CreatedAt), formatTS(sess.ExpiresAt))
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
	res, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
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
	if sess.CreatedAt, err = parseTime(createdAt); err != nil {
		return session.Session{}, err
	}
	if sess.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
