package pgx

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// PasswordStore implements user.PasswordRepository over a PostgreSQL database. It
// keeps credential material in its own table (user_passwords) so a store can
// guard it independently of the users table. Set is an upsert keyed by user_id.
type PasswordStore struct {
	db *pgxdb.DB
	qualified
}

var _ user.PasswordRepository = (*PasswordStore)(nil)

// NewPasswordStore returns a PasswordStore backed by db.
func NewPasswordStore(db *pgxdb.DB, opts ...Option) *PasswordStore {
	return &PasswordStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// Set upserts the password hash for userID: it creates the row when absent and
// replaces the hash when present, so a password change never collides.
func (s *PasswordStore) Set(ctx context.Context, userID, hash string) error {
	q := `INSERT INTO ` + s.table(passwordsTable) + ` (user_id, hash) VALUES (@user_id, @hash)
		ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{"user_id": userID, "hash": hash})
	return err
}

// Get returns the stored password hash for userID, or sdk.ErrNotFound.
func (s *PasswordStore) Get(ctx context.Context, userID string) (string, error) {
	q := `SELECT hash FROM ` + s.table(passwordsTable) + ` WHERE user_id = @user_id`
	var hash string
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{"user_id": userID}).Scan(&hash); err != nil {
		return "", pgxdb.MapError(err)
	}
	return hash, nil
}
