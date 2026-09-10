package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/authgrant"
)

var _ authgrant.Repository = (*authGrantStore)(nil)

// authGrantStore fills authgrant.Repository over the authentication_grants
// collection, selected by the derived consume key (SCHEMA.md §4.2). It owns no
// claim — the SQL index it mirrors is not unique. Bodies land in N3b, and the
// bulk revocation helpers it exposes are what N2b's SetStatus reuses.
type authGrantStore struct {
	db *firestoredb.DB
}

func newAuthGrantStore(db *firestoredb.DB) *authGrantStore {
	return &authGrantStore{db: db}
}

// Create persists a step-up grant, minting the id when the caller sends none.
func (s *authGrantStore) Create(ctx context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	return authgrant.Grant{}, errNotImplemented
}

// Consume spends the oldest unspent grant matching (session, purpose, context)
// in one transaction. An expired grant's consumption COMMITS, then
// sdk.ErrExpired is returned; no match → sdk.ErrNotFound.
func (s *authGrantStore) Consume(ctx context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	return authgrant.Grant{}, errNotImplemented
}

// DeleteBySession removes every grant bound to the session. Bulk and idempotent.
func (s *authGrantStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}
