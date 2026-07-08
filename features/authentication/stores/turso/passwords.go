package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// PasswordStore implements user.PasswordRepository over a libSQL database. It
// keeps credential material in its own table (user_passwords) so a store can
// guard it independently of the users table. Set is an upsert keyed by user_id.
type PasswordStore struct {
	db *tursodb.DB
}

var _ user.PasswordRepository = (*PasswordStore)(nil)

// NewPasswordStore returns a PasswordStore backed by db.
func NewPasswordStore(db *tursodb.DB) *PasswordStore {
	return &PasswordStore{db: db}
}

// Set upserts the password hash for userID: it creates the row when absent and
// replaces the hash when present, so a password change never collides.
func (s *PasswordStore) Set(ctx context.Context, userID, hash string) error {
	const q = `INSERT INTO user_passwords (user_id, hash) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash`
	_, err := s.db.Exec(ctx, q, userID, hash)
	return err
}

// Get returns the stored password hash for userID, or errs.ErrNotFound.
func (s *PasswordStore) Get(ctx context.Context, userID string) (string, error) {
	const q = `SELECT hash FROM user_passwords WHERE user_id = ?`
	var hash string
	if err := s.db.QueryRow(ctx, q, userID).Scan(&hash); err != nil {
		return "", tursodb.MapError(err)
	}
	return hash, nil
}
