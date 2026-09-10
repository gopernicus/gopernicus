package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
)

var _ user.UserRepository = (*userStore)(nil)

// userStore fills user.UserRepository over the users collection, whose document
// id is the KeyHash of the user id (SCHEMA.md §3.1). It also owns the DIRECTORY
// PROJECTION on that document (primary_email, email_verified). Every method
// refuses an ambient transaction first (R1); the bodies land in N2a/N2c.
type userStore struct {
	db *firestoredb.DB
}

func newUserStore(db *firestoredb.DB) *userStore {
	return &userStore{db: db}
}

// CreateWithPrimaryIdentifier commits the user, its first identifier, that
// identifier's claims, and the directory projection in ONE transaction: a lost
// authentication claim rolls the whole aggregate back as sdk.ErrAlreadyExists.
func (s *userStore) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	return user.User{}, identifier.Identifier{}, errNotImplemented
}

// Get returns the user with the given id, or sdk.ErrNotFound.
func (s *userStore) Get(ctx context.Context, id string) (user.User, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, err
	}
	return user.User{}, errNotImplemented
}

// Update persists profile changes, leaving id, created_at, auth_revision, and
// the lifecycle columns to the paths that own them.
func (s *userStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, err
	}
	return user.User{}, errNotImplemented
}
