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
// path (SCHEMA.md §5.4).
type apiKeyStore struct {
	db *firestoredb.DB
}

func newAPIKeyStore(db *firestoredb.DB) *apiKeyStore {
	return &apiKeyStore{db: db}
}

// Create mints a key and claims its hash; a colliding hash is
// sdk.ErrAlreadyExists with nothing written.
//
// The row and its claim are two documents, so this IS a transaction — and it
// reads neither of them. Both are written with Create, whose precondition the
// server evaluates at commit, which is what makes a lost race an
// sdk.ErrAlreadyExists rather than a duplicate credential (ruling R3).
func (s *apiKeyStore) Create(ctx context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	if err := refuseAmbient(ctx); err != nil {
		return apikey.APIKey{}, err
	}

	var created apikey.APIKey
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		// Reset every attempt: the value an attempt that lost the commit race
		// computed is not what this transaction stored.
		created = apikey.APIKey{}

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		row := newAPIKeyDoc(k)
		if err := putAPIKey(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		if err := plan.commit(ctx, w); err != nil {
			return err
		}
		var err error
		created, err = row.toDomain()
		return err
	})
	if err != nil {
		return apikey.APIKey{}, err
	}
	return created, nil
}

// GetByHash returns ANY present record for the hash — revoked and expired
// included; those are SERVICE branches, never store filters. It resolves through
// the hash claim (two point reads, no query, no index).
func (s *apiKeyStore) GetByHash(ctx context.Context, keyHash string) (apikey.APIKey, error) {
	if err := refuseAmbient(ctx); err != nil {
		return apikey.APIKey{}, err
	}
	row, err := readAPIKeyByHashClaim(ctx, s.db, s.db.ReaderFrom(ctx), keyHash)
	if err != nil {
		return apikey.APIKey{}, err
	}
	return row.toDomain()
}

// ListByServiceAccount pages the parent-scoped keys and is the pocket's ONLY
// searchable list: req.Search is applied as a client-side PostFilter over
// apikey.SearchFields (ruling R4). A blank term keeps the cheap server-side
// path; a non-blank one page-fills the forward page, the reverse HasPrev probe,
// and the count alike, so the total reflects the search.
func (s *apiKeyStore) ListByServiceAccount(ctx context.Context, serviceAccountID string, req list.Request) (list.Page[apikey.APIKey], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[apikey.APIKey]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listAPIKeys(s.db, serviceAccountID, req.Search), req)
}

// Revoke stamps revoked_at; unknown id → sdk.ErrNotFound. The hash claim is
// RETAINED: the SQL unique index is unconditional, so a revoked key's hash must
// stay un-mintable (SCHEMA.md §5.4).
func (s *apiKeyStore) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return revokeAPIKey(ctx, s.db, s.db.WriterFrom(ctx), id, revokedAt)
}

// TouchLastUsed stamps last_used_at; unknown id → sdk.ErrNotFound. One field
// update on one document, on the authentication hot path.
func (s *apiKeyStore) TouchLastUsed(ctx context.Context, id string, usedAt time.Time) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return touchAPIKey(ctx, s.db, s.db.WriterFrom(ctx), id, usedAt)
}
