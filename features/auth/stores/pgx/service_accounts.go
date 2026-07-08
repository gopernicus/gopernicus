package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/serviceaccount"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ServiceAccountStore implements serviceaccount.ServiceAccountRepository over a
// PostgreSQL database. List is keyset-paginated in the pinned created_at DESC,
// id DESC order (the id tiebreak keeps pages stable across a shared created_at).
type ServiceAccountStore struct {
	db *pgxdb.DB
}

var _ serviceaccount.ServiceAccountRepository = (*ServiceAccountStore)(nil)

// NewServiceAccountStore returns a ServiceAccountStore backed by db.
func NewServiceAccountStore(db *pgxdb.DB) *ServiceAccountStore {
	return &ServiceAccountStore{db: db}
}

const serviceAccountColumns = "id, name, description, created_by, act_as_user, owner_user_id, created_at, updated_at"

// Create persists a new service account.
func (s *ServiceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	const q = `INSERT INTO service_accounts (` + serviceAccountColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := s.db.Exec(ctx, q,
		sa.ID, sa.Name, sa.Description, sa.CreatedBy, sa.ActAsUser,
		sa.OwnerUserID, sa.CreatedAt.UTC(), sa.UpdatedAt.UTC(),
	)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}

// Get returns the account for id, or errs.ErrNotFound.
func (s *ServiceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	const q = `SELECT ` + serviceAccountColumns + ` FROM service_accounts WHERE id = $1`
	return scanServiceAccount(s.db.QueryRow(ctx, q, id))
}

// List returns a cursor-paginated page ordered created_at DESC, id DESC.
func (s *ServiceAccountStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return listPage(ctx, s.db, serviceAccountColumns, "service_accounts", "WHERE 1 = 1", nil, "id", req,
		scanServiceAccount,
		func(sa serviceaccount.ServiceAccount) (time.Time, string) { return sa.CreatedAt, sa.ID },
	)
}

// Update replaces the account for id; unknown → errs.ErrNotFound. It leaves id and
// created_at unchanged.
func (s *ServiceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	const q = `UPDATE service_accounts SET name=$1, description=$2, created_by=$3, act_as_user=$4, owner_user_id=$5, updated_at=$6 WHERE id=$7`
	res, err := s.db.Exec(ctx, q,
		sa.Name, sa.Description, sa.CreatedBy, sa.ActAsUser, sa.OwnerUserID, sa.UpdatedAt.UTC(), id,
	)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	if res.RowsAffected() == 0 {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	return sa, nil
}

// Delete removes the account for id; unknown → errs.ErrNotFound.
func (s *ServiceAccountStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.Exec(ctx, "DELETE FROM service_accounts WHERE id = $1", id)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanServiceAccount scans one service_accounts row, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanServiceAccount(sc scanner) (serviceaccount.ServiceAccount, error) {
	var (
		sa                   serviceaccount.ServiceAccount
		createdAt, updatedAt time.Time
	)
	if err := sc.Scan(&sa.ID, &sa.Name, &sa.Description, &sa.CreatedBy, &sa.ActAsUser, &sa.OwnerUserID, &createdAt, &updatedAt); err != nil {
		return serviceaccount.ServiceAccount{}, pgxdb.MapError(err)
	}
	sa.CreatedAt = createdAt.UTC()
	sa.UpdatedAt = updatedAt.UTC()
	return sa, nil
}
