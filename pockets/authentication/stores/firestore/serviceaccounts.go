package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ serviceaccount.ServiceAccountRepository = (*serviceAccountStore)(nil)

// serviceAccountStore fills serviceaccount.ServiceAccountRepository over the
// service_accounts collection. Bodies land in N4a.
type serviceAccountStore struct {
	db *firestoredb.DB
}

func newServiceAccountStore(db *firestoredb.DB) *serviceAccountStore {
	return &serviceAccountStore{db: db}
}

// Create persists a machine identity, minting the id when the caller sends none.
func (s *serviceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return serviceaccount.ServiceAccount{}, errNotImplemented
}

// Get returns the service account, or sdk.ErrNotFound.
func (s *serviceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return serviceaccount.ServiceAccount{}, errNotImplemented
}

// List pages the directory, ordered (created_at DESC, id DESC) by default.
func (s *serviceAccountStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[serviceaccount.ServiceAccount]{}, err
	}
	return crud.Page[serviceaccount.ServiceAccount]{}, errNotImplemented
}

// Update persists changes; unknown id → sdk.ErrNotFound.
func (s *serviceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return serviceaccount.ServiceAccount{}, errNotImplemented
}

// Delete removes the service account; unknown id → sdk.ErrNotFound.
func (s *serviceAccountStore) Delete(ctx context.Context, id string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}
