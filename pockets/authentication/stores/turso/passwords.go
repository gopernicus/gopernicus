package turso

import (
	"context"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// PasswordStore implements user.PasswordRepository over a libSQL database. It
// keeps credential material in its own table (user_passwords) so a store can
// guard it independently of the users table. Set is an upsert keyed by user_id.
type PasswordStore struct {
	db *tursodb.DB
}

var _ user.PasswordRepository = (*PasswordStore)(nil)

// NewPasswordStore returns a PasswordStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewPasswordStore(db *tursodb.DB) *PasswordStore {
	if db == nil {
		panic("authentication turso: NewPasswordStore received a nil database")
	}
	return &PasswordStore{db: db}
}

// Set upserts the password hash for userID: it creates the row when absent and
// replaces the hash when present, so a password change never collides.
func (s *PasswordStore) Set(ctx context.Context, userID, hash string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		_, err := changePassword(ctx, tx, userID, user.PasswordChange{NewHash: hash, Now: time.Now().UTC()}, false)
		return err
	})
}

func (s *PasswordStore) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	var revision int64
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var err error
		revision, err = changePassword(ctx, tx, userID, change, true)
		return err
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// Get returns the stored password hash for userID, or sdk.ErrNotFound.
func (s *PasswordStore) Get(ctx context.Context, userID string) (string, error) {
	const q = `SELECT hash FROM user_passwords WHERE user_id = ?`
	var hash string
	if err := s.db.QueryRow(ctx, q, userID).Scan(&hash); err != nil {
		return "", tursodb.MapError(err)
	}
	return hash, nil
}
