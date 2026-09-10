package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
)

var _ user.PasswordRepository = (*passwordStore)(nil)

// passwordStore fills user.PasswordRepository over the user_passwords
// collection, whose document id IS the user id — so the port's upsert is a
// document Set (SCHEMA.md §3.2).
type passwordStore struct {
	db *firestoredb.DB
}

func newPasswordStore(db *firestoredb.DB) *passwordStore {
	return &passwordStore{db: db}
}

// Set stores or replaces the user's password hash.
//
// It writes ONE document and reads none, so it is not wrapped in a transaction:
// a single-document write in Firestore is already atomic, and a transaction
// around it would only add a BeginTransaction and a Commit round trip to the
// login and password-change paths. refuseAmbient above has already established
// that no host transaction is in play (ruling R1), so the Writer this resolves
// to is the client's.
func (s *passwordStore) Set(ctx context.Context, userID, hash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return putPassword(ctx, s.db, s.db.WriterFrom(ctx), userID, hash)
}

// Get returns the stored hash, or sdk.ErrNotFound.
func (s *passwordStore) Get(ctx context.Context, userID string) (string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return "", err
	}
	return readPassword(ctx, s.db, s.db.ReaderFrom(ctx), userID)
}
