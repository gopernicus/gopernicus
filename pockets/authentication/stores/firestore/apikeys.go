package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var _ apikey.APIKeyRepository = (*apiKeyStore)(nil)

// apiKeyStore fills apikey.APIKeyRepository over the api_keys collection and the
// key-hash claim, which is both the uniqueness mechanism and GetByHash's access
// path (SCHEMA.md §5.4). Bodies land in N4a.
type apiKeyStore struct {
	db *firestoredb.DB
}

func newAPIKeyStore(db *firestoredb.DB) *apiKeyStore {
	return &apiKeyStore{db: db}
}

// Create mints a key and claims its hash; a colliding hash is
// sdk.ErrAlreadyExists with nothing written.
func (s *apiKeyStore) Create(ctx context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	if err := refuseAmbient(ctx); err != nil {
		return apikey.APIKey{}, err
	}
	return apikey.APIKey{}, errNotImplemented
}

// GetByHash returns ANY present record for the hash — revoked and expired
// included; those are SERVICE branches, never store filters.
func (s *apiKeyStore) GetByHash(ctx context.Context, keyHash string) (apikey.APIKey, error) {
	if err := refuseAmbient(ctx); err != nil {
		return apikey.APIKey{}, err
	}
	return apikey.APIKey{}, errNotImplemented
}

// ListByServiceAccount pages the parent-scoped keys and is the pocket's ONLY
// searchable list: req.Search is applied as a client-side PostFilter over
// apikey.SearchFields (ruling R4).
func (s *apiKeyStore) ListByServiceAccount(ctx context.Context, serviceAccountID string, req list.Request) (list.Page[apikey.APIKey], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[apikey.APIKey]{}, err
	}
	return list.Page[apikey.APIKey]{}, errNotImplemented
}

// Revoke stamps revoked_at; unknown id → sdk.ErrNotFound.
func (s *apiKeyStore) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// TouchLastUsed stamps last_used_at; unknown id → sdk.ErrNotFound.
func (s *apiKeyStore) TouchLastUsed(ctx context.Context, id string, usedAt time.Time) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}
