package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
)

var _ user.UserRepository = (*userStore)(nil)

// userStore fills user.UserRepository over the users collection, whose document
// id is the KeyHash of the user id (SCHEMA.md §3.1). The document also carries
// the DIRECTORY PROJECTION (primary_email, email_verified), which is why every
// users write in this store goes through users_doc.go.
type userStore struct {
	db *firestoredb.DB
}

func newUserStore(db *firestoredb.DB) *userStore {
	return &userStore{db: db}
}

// CreateWithPrimaryIdentifier commits the user, its first identifier, that
// identifier's claims, and the directory projection in ONE transaction: a lost
// authentication claim rolls the whole aggregate back as sdk.ErrAlreadyExists,
// with no orphan user row.
//
// It performs NO READS, which is worth stating because it looks like an
// omission. Every uniqueness rule this operation can break is a document that
// does not yet exist — the users primary key, the identifier primary key, the
// (kind, value) authentication claim, the (user, kind) primary claim — and each
// is written with CREATE, whose precondition the SERVER evaluates at commit. A
// check-then-write would be both slower and weaker (ruling R3).
//
// Both ids may be empty under the greenfield cryptids.Database convention; they
// are minted INSIDE the callback so a retried attempt mints fresh ones rather
// than reusing ids a losing attempt claimed (N-D5).
func (s *userStore) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}

	var (
		createdUser  user.User
		createdIdent identifier.Identifier
	)
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		// Reset every attempt: a result recorded by an attempt that lost the
		// commit race is not the result of the transaction that committed.
		createdUser, createdIdent = user.User{}, identifier.Identifier{}

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()

		userRow := newUserDoc(u)
		identRow := newIdentifierDoc(ident, userRow.ID)

		projection, err := resolveEmailProjection(identRow)
		if err != nil {
			return err
		}
		if err := putUser(ctx, s.db, w, userRow, projection); err != nil {
			return err
		}
		if err := putIdentifier(ctx, s.db, w, plan, identRow); err != nil {
			return err
		}
		if err := plan.commit(ctx, w); err != nil {
			return err
		}

		if createdUser, err = userRow.user(); err != nil {
			return err
		}
		createdIdent, err = identRow.toDomain()
		return err
	})
	if err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	return createdUser, createdIdent, nil
}

// Get returns the user with the given id, or sdk.ErrNotFound.
func (s *userStore) Get(ctx context.Context, id string) (user.User, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, err
	}
	row, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return user.User{}, err
	}
	return row.user()
}

// Update persists profile changes, leaving id, created_at, auth_revision, the
// lifecycle columns, and the directory projection to the paths that own them. A
// missing id is sdk.ErrNotFound.
//
// It is a FIELD update on one document, never a whole-document Set built from u:
// user.User has no email fields, so a Set would silently blank the projection
// and the operator directory would start answering empty addresses (SCHEMA.md
// §6.2, writer row 15). Being one document with no read, it needs no transaction
// — the field update is atomic on its own.
func (s *userStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.User{}, err
	}
	if err := updateUserProfile(ctx, s.db, s.db.WriterFrom(ctx), id, u.DisplayName, u.UpdatedAt); err != nil {
		return user.User{}, err
	}
	return u, nil
}
