package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var _ securityevent.SecurityEventRepository = (*securityEventStore)(nil)

// securityEventStore fills securityevent.SecurityEventRepository over the
// security_events collection: append-only, no claims, and the widest filter
// matrix in the store (SCHEMA.md §7.3).
type securityEventStore struct {
	db *firestoredb.DB
}

func newSecurityEventStore(db *firestoredb.DB) *securityEventStore {
	return &securityEventStore{db: db}
}

// Create appends one event, minting the id when the caller sends none, and
// returns the STORED shape — Details normalized to a non-nil map, timestamps at
// the microsecond precision the document holds — so the returned record and a
// later List agree.
//
// One document, no read, no transaction: an audit append has nothing to
// arbitrate.
func (s *securityEventStore) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	if err := refuseAmbient(ctx); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	row, err := newSecurityEventDoc(evt)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	if err := putSecurityEvent(ctx, s.db, s.db.WriterFrom(ctx), row); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	return row.toDomain()
}

// List pages the rail under the filter's equality subset and its inclusive
// Since / exclusive Until range, ordered (created_at DESC, id DESC) by default.
func (s *securityEventStore) List(ctx context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[securityevent.SecurityEvent]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listSecurityEvents(s.db, filter), req)
}
