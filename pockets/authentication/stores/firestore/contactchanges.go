package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ contactchange.Repository = (*contactChangeStore)(nil)

// contactChangeStore fills contactchange.Repository over the contact_changes
// collection, keyed by (user_id, kind) so Create IS the replacement
// (SCHEMA.md §3.12). It owns no claim; every reference goes through
// contactchanges_doc.go.
type contactChangeStore struct {
	db *firestoredb.DB
}

func newContactChangeStore(db *firestoredb.DB) *contactChangeStore {
	return &contactChangeStore{db: db}
}

// Create replaces the user's pending change for the kind and returns the stored
// row with its assigned id.
//
// One document, no read, no transaction: the document id IS the (user, kind)
// uniqueness rule, so a single Set both writes the new state and displaces the
// old one atomically. The SQL adapters need a delete-before-insert inside a
// transaction to say the same thing; here the write says it by itself, and
// refuseAmbient has already established that no host transaction is in play.
func (s *contactChangeStore) Create(ctx context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return contactchange.PendingChange{}, err
	}
	row := newContactChangeDoc(p)
	if err := putContactChange(ctx, s.db, s.db.WriterFrom(ctx), row); err != nil {
		return contactchange.PendingChange{}, err
	}
	p.ID = row.ID
	return p, nil
}

// Consume is single-use: the row is deleted and returned. The deletion COMMITS
// even when the row was expired, and sdk.ErrExpired is reported afterwards
// (N-D2); a missing or already-consumed pair is sdk.ErrNotFound.
//
// That ordering is the port's contract, not an optimization: the row is deleted
// REGARDLESS of expiry, so a second Consume of any pair is sdk.ErrNotFound. It
// is also why the expired outcome cannot be the callback's error — returning it
// from the callback would roll the deletion back and leave an expired pending
// value consumable forever.
func (s *contactChangeStore) Consume(ctx context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return contactchange.PendingChange{}, err
	}
	var out changeConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.consume(ctx, userID, kind, &out)
	})
	if err != nil {
		return contactchange.PendingChange{}, err
	}
	switch {
	case !out.found:
		return contactchange.PendingChange{}, sdk.ErrNotFound
	case out.expired:
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return out.change, nil
}

// changeConsumption is ONE attempt's outcome of the single-use consume: what was
// read, and what the caller must report AFTER the transaction commits.
type changeConsumption struct {
	change  contactchange.PendingChange
	found   bool
	expired bool
}

// consume is one attempt of the read-and-delete: it RESETS the outcome, reads
// the row, records what it found, and queues the deletion.
//
// The reset is the load-bearing line, for the reason consumeState states: a
// Firestore transaction callback may run more than once, and an attempt that
// observed an expired row and then lost its commit race must not leave "expired"
// behind for the attempt that finds nothing. An absent row is NOT an error here
// — the callback returns nil, commits nothing, and Consume reports
// sdk.ErrNotFound from the outcome.
func (s *contactChangeStore) consume(ctx context.Context, userID string, kind identifier.Kind, out *changeConsumption) error {
	*out = changeConsumption{}

	row, found, err := readContactChange(ctx, s.db, s.db.ReaderFrom(ctx), userID, kind)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	p := row.toDomain()
	out.change, out.found, out.expired = p, true, p.Expired(time.Now())
	return dropContactChange(ctx, s.db, s.db.WriterFrom(ctx), userID, kind)
}
