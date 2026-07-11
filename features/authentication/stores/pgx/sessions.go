package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// SessionStore implements session.SessionRepository over a PostgreSQL database.
// Sessions are opaque: the token column holds whatever value the auth service
// supplies as the primary key — the service hashes the cookie token before every
// call (design §7.3), so the store persists and looks up by that opaque value
// and does no hashing itself. Get enforces expired-at-read: a row past its
// expires_at surfaces sdk.ErrExpired rather than a dead session.
type SessionStore struct {
	db *pgxdb.DB
}

var _ session.SessionRepository = (*SessionStore)(nil)

// NewSessionStore returns a SessionStore backed by db.
func NewSessionStore(db *pgxdb.DB) *SessionStore {
	return &SessionStore{db: db}
}

const sessionColumns = "token, user_id, created_at, expires_at"

// sessionRow is the store-local, db-tagged projection of a sessions row.
type sessionRow struct {
	Token     string    `db:"token"`
	UserID    string    `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

func (r sessionRow) toDomain() session.Session {
	return session.Session{
		Token:     r.Token,
		UserID:    r.UserID,
		CreatedAt: r.CreatedAt.UTC(),
		ExpiresAt: r.ExpiresAt.UTC(),
	}
}

// Create persists a new session.
func (s *SessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	const q = `INSERT INTO sessions (` + sessionColumns + `)
		VALUES (@token, @user_id, @created_at, @expires_at)`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"token":      sess.Token,
		"user_id":    sess.UserID,
		"created_at": sess.CreatedAt.UTC(),
		"expires_at": sess.ExpiresAt.UTC(),
	})
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}

// Get returns the live session for token: unknown → sdk.ErrNotFound,
// present-but-expired → sdk.ErrExpired (checked against the read clock).
func (s *SessionStore) Get(ctx context.Context, token string) (session.Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions WHERE token = @token`
	row, err := pgxdb.QueryOne[sessionRow](ctx, s.db, q, pgx.NamedArgs{"token": token})
	if err != nil {
		return session.Session{}, err
	}
	sess := row.toDomain()
	if sess.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return sess, nil
}

// Delete removes the session for token; unknown → sdk.ErrNotFound.
func (s *SessionStore) Delete(ctx context.Context, token string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM sessions WHERE token = @token", pgx.NamedArgs{"token": token})
	if err != nil {
		return err
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
	_, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE user_id = @user_id", pgx.NamedArgs{"user_id": userID})
	return err
}
