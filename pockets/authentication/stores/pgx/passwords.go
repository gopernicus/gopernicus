package pgx

import (
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/jackc/pgx/v5"
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
// It panics if db is nil; the caller owns the database lifecycle.
func NewPasswordStore(db *pgxdb.DB, opts ...Option) *PasswordStore {
	if db == nil {
		panic("authentication pgx: NewPasswordStore received a nil database")
	}
	return &PasswordStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// Set upserts the password hash for userID: it creates the row when absent and
// replaces the hash when present, so a password change never collides.
func (s *PasswordStore) Set(ctx context.Context, userID, hash string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		_, err := changePassword(ctx, tx, s.qualified, userID, user.PasswordChange{NewHash: hash, Now: time.Now().UTC()}, false)
		return err
	})
}

func (s *PasswordStore) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	var revision int64
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var err error
		revision, err = changePassword(ctx, tx, s.qualified, userID, change, true)
		return err
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
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
