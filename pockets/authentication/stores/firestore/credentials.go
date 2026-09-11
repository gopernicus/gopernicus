package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
)

var _ credential.MutationRepository = (*credentialMutationStore)(nil)

// credentialMutationStore fills credential.MutationRepository: the
// revision-serialized rail over users, user_passwords, oauth_accounts, and
// user_identifiers plus every claim and the directory projection those touch.
// Bodies land in N2b.
type credentialMutationStore struct {
	db *firestoredb.DB
}

func newCredentialMutationStore(db *firestoredb.DB) *credentialMutationStore {
	return &credentialMutationStore{db: db}
}

// Snapshot projects the user's typed MethodSet and the auth_revision it was read
// at, under ONE snapshot so the revision and the credentials cannot disagree.
func (s *credentialMutationStore) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	if err := refuseAmbient(ctx); err != nil {
		return credential.MethodSet{}, err
	}
	return credential.MethodSet{}, errNotImplemented
}

// Apply performs one revision-CAS typed mutation atomically, incrementing
// auth_revision exactly once. A stale revision is sdk.ErrConflict with nothing
// written; the mutation is never partially applied.
func (s *credentialMutationStore) Apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}
