package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
)

var _ user.PasswordRepository = (*passwordStore)(nil)

// passwordStore fills user.PasswordRepository over the user_passwords
// collection, whose document id IS the user id — so the port's upsert is a
// document Set (SCHEMA.md §3.2). Bodies land in N2a.
type passwordStore struct {
	db *firestoredb.DB
}

func newPasswordStore(db *firestoredb.DB) *passwordStore {
	return &passwordStore{db: db}
}

// Set stores or replaces the user's password hash.
func (s *passwordStore) Set(ctx context.Context, userID, hash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// Get returns the stored hash, or sdk.ErrNotFound.
func (s *passwordStore) Get(ctx context.Context, userID string) (string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return "", err
	}
	return "", errNotImplemented
}
