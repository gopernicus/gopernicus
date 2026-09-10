package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
)

var _ contactchange.Repository = (*contactChangeStore)(nil)

// contactChangeStore fills contactchange.Repository over the contact_changes
// collection, keyed by (user_id, kind) so Create IS the replacement
// (SCHEMA.md §3.12). It owns no claim. Bodies land in N4c.
type contactChangeStore struct {
	db *firestoredb.DB
}

func newContactChangeStore(db *firestoredb.DB) *contactChangeStore {
	return &contactChangeStore{db: db}
}

// Create replaces the user's pending change for the kind.
func (s *contactChangeStore) Create(ctx context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return contactchange.PendingChange{}, err
	}
	return contactchange.PendingChange{}, errNotImplemented
}

// Consume is single-use: the row is deleted and returned. An expired row's
// deletion COMMITS, then sdk.ErrExpired is returned; absent → sdk.ErrNotFound.
func (s *contactChangeStore) Consume(ctx context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return contactchange.PendingChange{}, err
	}
	return contactchange.PendingChange{}, errNotImplemented
}
