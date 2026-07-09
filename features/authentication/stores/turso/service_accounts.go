package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ServiceAccountStore implements serviceaccount.ServiceAccountRepository over a
// libSQL database. List is keyset-paginated in the pinned created_at DESC, id DESC
// order (the id tiebreak keeps pages stable across a shared created_at).
type ServiceAccountStore struct {
	db *tursodb.DB
}

var _ serviceaccount.ServiceAccountRepository = (*ServiceAccountStore)(nil)

// NewServiceAccountStore returns a ServiceAccountStore backed by db.
func NewServiceAccountStore(db *tursodb.DB) *ServiceAccountStore {
	return &ServiceAccountStore{db: db}
}

const serviceAccountColumns = "id, name, description, created_by, act_as_user, owner_user_id, created_at, updated_at"

// Create persists a new service account.
func (s *ServiceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	const q = `INSERT INTO service_accounts (` + serviceAccountColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		sa.ID, sa.Name, sa.Description, sa.CreatedBy, tursodb.BoolToInt(sa.ActAsUser),
		sa.OwnerUserID, tursodb.FormatTime(sa.CreatedAt), tursodb.FormatTime(sa.UpdatedAt),
	)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}

// Get returns the account for id, or errs.ErrNotFound.
func (s *ServiceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	const q = `SELECT ` + serviceAccountColumns + ` FROM service_accounts WHERE id = ?`
	return scanServiceAccount(s.db.QueryRow(ctx, q, id))
}

// List returns a cursor-paginated page ordered created_at DESC, id DESC.
func (s *ServiceAccountStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	q := tursodb.ListQuery[serviceaccount.ServiceAccount]{
		BaseSQL:      `SELECT ` + serviceAccountColumns + ` FROM service_accounts`,
		OrderFields:  serviceaccount.OrderFields,
		DefaultOrder: serviceaccount.DefaultOrder,
		PK:           "id",
		Scan:         scanServiceAccount,
		OrderValueOf: func(sa serviceaccount.ServiceAccount, _ string) any { return sa.CreatedAt },
		PKOf:         func(sa serviceaccount.ServiceAccount) string { return sa.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

// Update replaces the account for id; unknown → errs.ErrNotFound. It leaves id and
// created_at unchanged.
func (s *ServiceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	const q = `UPDATE service_accounts SET name=?, description=?, created_by=?, act_as_user=?, owner_user_id=?, updated_at=? WHERE id=?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q,
		sa.Name, sa.Description, sa.CreatedBy, tursodb.BoolToInt(sa.ActAsUser), sa.OwnerUserID, tursodb.FormatTime(sa.UpdatedAt), id,
	)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	if n == 0 {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	return sa, nil
}

// Delete removes the account for id; unknown → errs.ErrNotFound.
func (s *ServiceAccountStore) Delete(ctx context.Context, id string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM service_accounts WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanServiceAccount scans one service_accounts row, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanServiceAccount(sc scanner) (serviceaccount.ServiceAccount, error) {
	var (
		sa                   serviceaccount.ServiceAccount
		actAsUser            int64
		createdAt, updatedAt string
	)
	if err := sc.Scan(&sa.ID, &sa.Name, &sa.Description, &sa.CreatedBy, &actAsUser, &sa.OwnerUserID, &createdAt, &updatedAt); err != nil {
		return serviceaccount.ServiceAccount{}, tursodb.MapError(err)
	}
	sa.ActAsUser = actAsUser != 0
	var err error
	if sa.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	if sa.UpdatedAt, err = tursodb.ParseTime(updatedAt); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}
