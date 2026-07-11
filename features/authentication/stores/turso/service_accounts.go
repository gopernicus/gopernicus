package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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

// serviceAccountRow is the store-local, db-tagged projection of a service_accounts
// row ScanStruct scans into; toDomain maps it to the persistence-free domain
// entity.
type serviceAccountRow struct {
	ID          string       `db:"id"`
	Name        string       `db:"name"`
	Description string       `db:"description"`
	CreatedBy   string       `db:"created_by"`
	ActAsUser   tursodb.Bool `db:"act_as_user"`
	OwnerUserID string       `db:"owner_user_id"`
	CreatedAt   tursodb.Time `db:"created_at"`
	UpdatedAt   tursodb.Time `db:"updated_at"`
}

func (r serviceAccountRow) toDomain() serviceaccount.ServiceAccount {
	return serviceaccount.ServiceAccount{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		CreatedBy:   r.CreatedBy,
		ActAsUser:   bool(r.ActAsUser),
		OwnerUserID: r.OwnerUserID,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

// Create persists a new service account.
func (s *ServiceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if sa.ID == "" {
		const q = `INSERT INTO service_accounts (name, description, created_by, act_as_user, owner_user_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			sa.Name, sa.Description, sa.CreatedBy, tursodb.BoolToInt(sa.ActAsUser),
			sa.OwnerUserID, tursodb.FormatTime(sa.CreatedAt), tursodb.FormatTime(sa.UpdatedAt),
		).Scan(&sa.ID); err != nil {
			return serviceaccount.ServiceAccount{}, tursodb.MapError(err)
		}
		return sa, nil
	}
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

// Get returns the account for id, or sdk.ErrNotFound.
func (s *ServiceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	const q = `SELECT ` + serviceAccountColumns + ` FROM service_accounts WHERE id = ?`
	row, err := tursodb.QueryOne[serviceAccountRow](ctx, s.db, q, id)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor-paginated page ordered created_at DESC, id DESC.
func (s *ServiceAccountStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	q := tursodb.ListQuery[serviceAccountRow]{
		BaseSQL:      `SELECT ` + serviceAccountColumns + ` FROM service_accounts`,
		OrderFields:  serviceaccount.OrderFields,
		DefaultOrder: serviceaccount.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r serviceAccountRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r serviceAccountRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[serviceaccount.ServiceAccount]{}, err
	}
	return crud.MapPage(page, serviceAccountRow.toDomain), nil
}

// Update replaces the account for id; unknown → sdk.ErrNotFound. It leaves id and
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
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	return sa, nil
}

// Delete removes the account for id; unknown → sdk.ErrNotFound.
func (s *ServiceAccountStore) Delete(ctx context.Context, id string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM service_accounts WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
