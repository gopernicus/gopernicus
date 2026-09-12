package firestore

import (
	"context"
	"errors"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// The contact_changes collection is owned here: putContactChange and
// dropContactChange are its only writers, and ownership_test.go refuses any
// other non-test file to name it.
//
// It owns NO claim, and that is a property of the layout rather than an omission
// (SCHEMA.md §3.12, §5.10). Its one unique index — (user_id, kind) — IS the
// document id, so "at most one pending change per user and kind" is enforced by
// the document itself: Create writes with Set and STRUCTURALLY displaces any
// prior pending change, where the SQL adapters need a delete-before-insert plus
// a unique index as the concurrent backstop.
//
// The surrogate id keeps living in a FIELD (PendingChange.ID is what
// challenge.context binds as the confirm-step validator), and no port reads a
// pending change by it — which is exactly the premise §5.10 records for deciding
// this collection needs no id claim.

// contactChangeRef is the row's document — the KeyHash of the (user_id, kind)
// pair a pending change is unique under, NOT of its surrogate id.
func contactChangeRef(db *firestoredb.DB, userID string, kind identifier.Kind) *gcfs.DocumentRef {
	return db.Doc(collectionContactChanges, contactChangeDocID(userID, string(kind)))
}

// newContactChangeDoc builds the document for a pending change being written,
// minting the id when the caller left it empty (the greenfield DB-generated
// convention this domain always uses — contactchange.New leaves it blank).
//
// It is called INSIDE the write path so a retried attempt mints a fresh id
// rather than reusing one a rolled-back attempt claimed (N-D5).
func newContactChangeDoc(p contactchange.PendingChange) contactChangeDoc {
	changeID := p.ID
	if changeID == "" {
		changeID = firestoredb.NewID()
	}
	return contactChangeDoc{
		ID:                   changeID,
		UserID:               p.UserID,
		Kind:                 string(p.Kind),
		NewValue:             p.NewValue,
		LoginEnabled:         p.LoginEnabled,
		RecoveryEnabled:      p.RecoveryEnabled,
		NotificationEnabled:  p.NotificationEnabled,
		MakePrimary:          p.MakePrimary,
		ReplacesIdentifierID: p.ReplacesIdentifierID,
		ExpiresAt:            firestoredb.TruncateTime(p.ExpiresAt),
		CreatedAt:            firestoredb.TruncateTime(p.CreatedAt),
	}
}

// putContactChange writes a pending change with Set. Set rather than Create
// because the document id IS the replacement key: the write displaces whatever
// row held (user_id, kind), which is the port's atomic replace in one write
// rather than two.
func putContactChange(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row contactChangeDoc) error {
	return w.Set(ctx, contactChangeRef(db, row.UserID, identifier.Kind(row.Kind)), row)
}

// dropContactChange deletes the pending change for (user, kind) — the write half
// of the single-use consume. There is no claim to release.
func dropContactChange(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID string, kind identifier.Kind) error {
	return w.Delete(ctx, contactChangeRef(db, userID, kind))
}

// readContactChange reads the pending change for (user, kind). An absent
// document is (zero, false, nil): the caller decides whether "no pending change"
// is sdk.ErrNotFound, and it makes that call AFTER the transaction commits.
func readContactChange(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string, kind identifier.Kind) (contactChangeDoc, bool, error) {
	snap, err := r.Get(ctx, contactChangeRef(db, userID, kind))
	if errors.Is(err, sdk.ErrNotFound) {
		return contactChangeDoc{}, false, nil
	}
	if err != nil {
		return contactChangeDoc{}, false, err
	}
	var row contactChangeDoc
	if err := snap.DataTo(&row); err != nil {
		return contactChangeDoc{}, false, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionContactChanges, err, sdk.ErrInvalidInput)
	}
	return row, true, nil
}

// toDomain projects the document onto the domain aggregate.
func (d contactChangeDoc) toDomain() contactchange.PendingChange {
	return contactchange.PendingChange{
		ID:                   d.ID,
		UserID:               d.UserID,
		Kind:                 identifier.Kind(d.Kind),
		NewValue:             d.NewValue,
		LoginEnabled:         d.LoginEnabled,
		RecoveryEnabled:      d.RecoveryEnabled,
		NotificationEnabled:  d.NotificationEnabled,
		MakePrimary:          d.MakePrimary,
		ReplacesIdentifierID: d.ReplacesIdentifierID,
		ExpiresAt:            d.ExpiresAt,
		CreatedAt:            d.CreatedAt,
	}
}
