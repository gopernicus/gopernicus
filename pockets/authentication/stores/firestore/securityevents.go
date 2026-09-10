package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ securityevent.SecurityEventRepository = (*securityEventStore)(nil)

// securityEventStore fills securityevent.SecurityEventRepository over the
// security_events collection: append-only, no claims, and the widest filter
// matrix in the store (SCHEMA.md §7). Bodies land in N4a.
type securityEventStore struct {
	db *firestoredb.DB
}

func newSecurityEventStore(db *firestoredb.DB) *securityEventStore {
	return &securityEventStore{db: db}
}

// Create appends one event, minting the id when the caller sends none.
func (s *securityEventStore) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	if err := refuseAmbient(ctx); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	return securityevent.SecurityEvent{}, errNotImplemented
}

// List pages the rail under the filter's equality subset and its inclusive
// Since / exclusive Until range, ordered (created_at DESC, id DESC) by default.
func (s *securityEventStore) List(ctx context.Context, filter securityevent.ListFilter, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[securityevent.SecurityEvent]{}, err
	}
	return crud.Page[securityevent.SecurityEvent]{}, errNotImplemented
}
