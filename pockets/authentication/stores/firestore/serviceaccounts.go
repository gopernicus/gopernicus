package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var _ serviceaccount.ServiceAccountRepository = (*serviceAccountStore)(nil)

// serviceAccountStore fills serviceaccount.ServiceAccountRepository over the
// service_accounts collection, whose document id is the KeyHash of the account
// id (SCHEMA.md §3.6). There is no claim: `name` carries no unique index in
// either SQL dialect, so this store does not invent one.
type serviceAccountStore struct {
	db *firestoredb.DB
}

func newServiceAccountStore(db *firestoredb.DB) *serviceAccountStore {
	return &serviceAccountStore{db: db}
}

// Create persists a machine identity, minting the id when the caller sends none.
//
// It writes ONE document and reads nothing, so it needs no transaction: a
// single-document write is already atomic, and the only uniqueness rule it can
// break — the primary key — is the document id, whose Create precondition the
// SERVER evaluates.
func (s *serviceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	row := newServiceAccountDoc(sa)
	if err := putServiceAccount(ctx, s.db, s.db.WriterFrom(ctx), row); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// Get returns the service account, or sdk.ErrNotFound.
func (s *serviceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	row, err := readServiceAccount(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// List pages the directory, ordered (created_at DESC, id DESC) by default.
func (s *serviceAccountStore) List(ctx context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[serviceaccount.ServiceAccount]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listServiceAccounts(s.db), req)
}

// Update persists changes; unknown id → sdk.ErrNotFound. It leaves id and
// created_at alone, exactly as the SQL adapters' UPDATE does, and returns the
// caller's value — the same shape turso returns.
//
// It is a FIELD update on one document with no read: the vendor's Update carries
// an implicit exists precondition, which IS the SQL adapters' affected-rows
// check, so a missing account fails without a round trip to prove it.
func (s *serviceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	if err := updateServiceAccountProfile(ctx, s.db, s.db.WriterFrom(ctx), id, sa); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}

// Delete removes the service account; unknown id → sdk.ErrNotFound.
//
// Unlike Update, this one IS a transaction, because a Firestore delete of an
// absent document SUCCEEDS: the row is read and deleted together, so "unknown
// id" is the read's sdk.ErrNotFound and two concurrent deletes cannot both
// report success.
func (s *serviceAccountStore) Delete(ctx context.Context, id string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		row, err := readServiceAccount(ctx, s.db, s.db.ReaderFrom(ctx), id)
		if err != nil {
			return err
		}
		return dropServiceAccount(ctx, s.db, s.db.WriterFrom(ctx), row)
	})
}
