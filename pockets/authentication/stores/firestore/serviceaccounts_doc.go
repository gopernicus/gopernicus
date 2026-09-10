package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
)

// The service_accounts collection's owner file: every reference to it, every
// decode of it, and every write of it lives here (ownership_test.go refuses any
// other non-test file to name it).
//
// It is the SIMPLEST collection in the store, and the audit is what makes that
// claim rather than an assumption: migration 0006 declares a single PRIMARY KEY
// and NO other unique index — in particular `name` is NOT unique (SCHEMA.md
// §5.9). So there is no claim document here, and adding one would make this
// store refuse a second "deployer" that both SQL siblings accept. The document
// id carries the primary key and nothing else has to.
//
// Every write is one document and reads nothing, except Delete, whose port
// contract ("unknown id → sdk.ErrNotFound") is the SQL adapters' affected-rows
// check and therefore needs a read in the same transaction as the deletion.

// The service_accounts field paths a field update addresses and the list orders
// on.
const (
	fieldServiceAccountName        = "name"
	fieldServiceAccountDescription = "description"
	fieldServiceAccountCreatedBy   = "created_by"
	fieldServiceAccountActAsUser   = "act_as_user"
	fieldServiceAccountOwnerUserID = "owner_user_id"
	fieldServiceAccountUpdatedAt   = "updated_at"
	fieldServiceAccountID          = "id"
)

// serviceAccountRef is the row's document — the KeyHash of its primary key. The
// id itself is kept in a field and is what the port returns and pages by
// (SCHEMA.md §4.2).
func serviceAccountRef(db *firestoredb.DB, id string) *gcfs.DocumentRef {
	return db.Doc(collectionServiceAccounts, serviceAccountDocID(id))
}

// serviceAccountsQuery is the unfiltered collection — the directory listing's
// base. It carries no order, limit, or cursor: the connector's List owns all
// three.
func serviceAccountsQuery(db *firestoredb.DB) gcfs.Query {
	return db.Collection(collectionServiceAccounts).Query
}

// newServiceAccountDoc builds the document for an account being CREATED, minting
// the id when the caller left it empty — the greenfield cryptids.Database
// convention, which the SQL adapters serve by omitting the column and reading
// the schema default back with RETURNING (N-D5).
func newServiceAccountDoc(sa serviceaccount.ServiceAccount) serviceAccountDoc {
	id := sa.ID
	if id == "" {
		id = firestoredb.NewID()
	}
	return serviceAccountDoc{
		ID:          id,
		Name:        sa.Name,
		Description: sa.Description,
		CreatedBy:   sa.CreatedBy,
		ActAsUser:   sa.ActAsUser,
		OwnerUserID: sa.OwnerUserID,
		CreatedAt:   firestoredb.TruncateTime(sa.CreatedAt),
		UpdatedAt:   firestoredb.TruncateTime(sa.UpdatedAt),
	}
}

// putServiceAccount writes a NEW account. The verb is Create, never Set: the
// document id IS the primary key, so a host that supplies a duplicate id loses
// at the server as sdk.ErrAlreadyExists rather than overwriting a live identity.
func putServiceAccount(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row serviceAccountDoc) error {
	return w.Create(ctx, serviceAccountRef(db, row.ID), row)
}

// updateServiceAccountProfile writes the mutable half of an account, leaving id
// and created_at alone — exactly the column list the SQL adapters' UPDATE names.
// A missing document fails sdk.ErrNotFound, which is the port's absent contract:
// the vendor's Update carries an implicit exists precondition, so no read is
// needed to honor it.
func updateServiceAccountProfile(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, id string, sa serviceaccount.ServiceAccount) error {
	return w.Update(ctx, serviceAccountRef(db, id), []gcfs.Update{
		{Path: fieldServiceAccountName, Value: sa.Name},
		{Path: fieldServiceAccountDescription, Value: sa.Description},
		{Path: fieldServiceAccountCreatedBy, Value: sa.CreatedBy},
		{Path: fieldServiceAccountActAsUser, Value: sa.ActAsUser},
		{Path: fieldServiceAccountOwnerUserID, Value: sa.OwnerUserID},
		{Path: fieldServiceAccountUpdatedAt, Value: firestoredb.TruncateTime(sa.UpdatedAt)},
	})
}

// dropServiceAccount enqueues the deletion of an already-read row. It takes the
// row rather than an id because a Firestore delete of an absent document
// SUCCEEDS, and the port's contract is the SQL adapters' affected-rows check:
// the caller reads first, in the same transaction, so "unknown id" is
// sdk.ErrNotFound and a concurrent delete cannot report two successes.
func dropServiceAccount(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row serviceAccountDoc) error {
	return w.Delete(ctx, serviceAccountRef(db, row.ID))
}

// readServiceAccount reads one row through r, so the same code serves a
// standalone Get and a transaction's read phase. An absent document is
// sdk.ErrNotFound.
func readServiceAccount(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, id string) (serviceAccountDoc, error) {
	snap, err := r.Get(ctx, serviceAccountRef(db, id))
	if err != nil {
		return serviceAccountDoc{}, err
	}
	return decodeServiceAccount(snap)
}

// decodeServiceAccount turns one snapshot into a service-account document.
func decodeServiceAccount(snap *gcfs.DocumentSnapshot) (serviceAccountDoc, error) {
	var row serviceAccountDoc
	if err := snap.DataTo(&row); err != nil {
		return serviceAccountDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionServiceAccounts, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// decodeServiceAccountRow is the listing's Decode: one snapshot straight to the
// domain entity, so a page costs exactly the documents it returns.
func decodeServiceAccountRow(snap *gcfs.DocumentSnapshot) (serviceaccount.ServiceAccount, error) {
	row, err := decodeServiceAccount(snap)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// listServiceAccounts is the ListQuery the directory pages with. The order
// allow-list and default come straight from the pocket (serviceaccount.
// OrderFields, serviceaccount.DefaultOrder) — the same values both SQL adapters
// pass — and PK is the row's own `id` field, so equal created_at values break
// the tie on the contractual column rather than on the hashed document name.
//
// No PostFilter is declared, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List rather than answered with an
// unfiltered page: serviceaccount declares no SearchFields, which is today's
// turso posture for a list with nothing searchable (ruling R4).
func listServiceAccounts(db *firestoredb.DB) firestoredb.ListQuery[serviceaccount.ServiceAccount] {
	return firestoredb.ListQuery[serviceaccount.ServiceAccount]{
		Query:        serviceAccountsQuery(db),
		OrderFields:  serviceaccount.OrderFields,
		DefaultOrder: serviceaccount.DefaultOrder,
		PK:           fieldServiceAccountID,
		Decode:       decodeServiceAccountRow,
		OrderValueOf: func(row serviceaccount.ServiceAccount, _ string) any { return row.CreatedAt },
		PKOf:         func(row serviceaccount.ServiceAccount) string { return row.ID },
	}
}

// toDomain projects the document onto the domain aggregate. Every field is
// non-nullable in SQL, so nothing here can fail.
func (d serviceAccountDoc) toDomain() serviceaccount.ServiceAccount {
	return serviceaccount.ServiceAccount{
		ID:          d.ID,
		Name:        d.Name,
		Description: d.Description,
		CreatedBy:   d.CreatedBy,
		ActAsUser:   d.ActAsUser,
		OwnerUserID: d.OwnerUserID,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}
