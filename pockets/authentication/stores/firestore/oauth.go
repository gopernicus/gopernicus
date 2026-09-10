package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
)

var (
	_ oauthaccount.OAuthAccountRepository = (*oauthAccountStore)(nil)
	_ oauthstate.StateRepository          = (*oauthStateStore)(nil)
)

// oauthAccountStore fills oauthaccount.OAuthAccountRepository over the
// oauth_accounts collection, whose document id IS the (provider,
// provider_user_id) primary key — so provider uniqueness needs no claim
// (SCHEMA.md §5.8). Bodies land in N3b.
type oauthAccountStore struct {
	db *firestoredb.DB
}

func newOAuthAccountStore(db *firestoredb.DB) *oauthAccountStore {
	return &oauthAccountStore{db: db}
}

// Create links a provider identity to a local user; a duplicate provider
// identity is sdk.ErrAlreadyExists at the server.
func (s *oauthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return oauthaccount.OAuthAccount{}, errNotImplemented
}

// GetByProvider reads the link by its (provider, provider_user_id) id.
func (s *oauthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return oauthaccount.OAuthAccount{}, errNotImplemented
}

// ListByUser returns the user's links, ordered (linked_at DESC,
// provider_user_id DESC).
func (s *oauthAccountStore) ListByUser(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

// Delete unlinks (userID, provider); no match → sdk.ErrNotFound.
func (s *oauthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// oauthStateStore fills oauthstate.StateRepository over the oauth_states
// collection: single-use flow secrets keyed by token, consumed by a
// transactional get-and-delete. Bodies land in N3b.
type oauthStateStore struct {
	db *firestoredb.DB
}

func newOAuthStateStore(db *firestoredb.DB) *oauthStateStore {
	return &oauthStateStore{db: db}
}

// Create persists a one-time state.
func (s *oauthStateStore) Create(ctx context.Context, st oauthstate.State) (oauthstate.State, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthstate.State{}, err
	}
	return oauthstate.State{}, errNotImplemented
}

// Consume deletes and returns the state. The deletion COMMITS even when the row
// was expired; sdk.ErrExpired is reported afterwards (N-D2).
func (s *oauthStateStore) Consume(ctx context.Context, token string) (oauthstate.State, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthstate.State{}, err
	}
	return oauthstate.State{}, errNotImplemented
}
