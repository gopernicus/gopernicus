package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
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

// serviceAccountRow is the store-local, db-tagged projection of a service_accounts
// row that pgx.RowToStructByName scans into; toDomain maps it to the persistence-
// free domain entity.
type serviceAccountRow struct {
	ID          string    `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	CreatedBy   string    `db:"created_by"`
	ActAsUser   bool      `db:"act_as_user"`
	OwnerUserID string    `db:"owner_user_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (r serviceAccountRow) toDomain() serviceaccount.ServiceAccount {
	return serviceaccount.ServiceAccount{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		CreatedBy:   r.CreatedBy,
		ActAsUser:   r.ActAsUser,
		OwnerUserID: r.OwnerUserID,
		CreatedAt:   r.CreatedAt.UTC(),
		UpdatedAt:   r.UpdatedAt.UTC(),
	}
}

// Create persists a new service account.
func (s *ServiceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	args := pgx.NamedArgs{
		"name":          sa.Name,
		"description":   sa.Description,
		"created_by":    sa.CreatedBy,
		"act_as_user":   sa.ActAsUser,
		"owner_user_id": sa.OwnerUserID,
		"created_at":    sa.CreatedAt.UTC(),
		"updated_at":    sa.UpdatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if sa.ID == "" {
		const q = `INSERT INTO service_accounts (name, description, created_by, act_as_user, owner_user_id, created_at, updated_at)
			VALUES (@name, @description, @created_by, @act_as_user, @owner_user_id, @created_at, @updated_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&sa.ID); err != nil {
			return serviceaccount.ServiceAccount{}, pgxdb.MapError(err)
		}
		return sa, nil
	}
	const q = `INSERT INTO service_accounts (` + serviceAccountColumns + `)
		VALUES (@id, @name, @description, @created_by, @act_as_user, @owner_user_id, @created_at, @updated_at)`
	args["id"] = sa.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}

// Get returns the account for id, or sdk.ErrNotFound.
func (s *ServiceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	const q = `SELECT ` + serviceAccountColumns + ` FROM service_accounts WHERE id = @id`
	row, err := queryOne[serviceAccountRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor-paginated page ordered created_at DESC, id DESC.
func (s *ServiceAccountStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	q := pgxdb.ListQuery[serviceAccountRow]{
		BaseSQL:      `SELECT ` + serviceAccountColumns + ` FROM service_accounts`,
		OrderFields:  serviceaccount.OrderFields,
		DefaultOrder: serviceaccount.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r serviceAccountRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r serviceAccountRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[serviceaccount.ServiceAccount]{}, err
	}
	return crud.MapPage(page, serviceAccountRow.toDomain), nil
}

// Update replaces the account for id; unknown → sdk.ErrNotFound. It leaves id and
// created_at unchanged.
func (s *ServiceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	const q = `UPDATE service_accounts
		SET name = @name, description = @description, created_by = @created_by,
			act_as_user = @act_as_user, owner_user_id = @owner_user_id, updated_at = @updated_at
		WHERE id = @id`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"name":          sa.Name,
		"description":   sa.Description,
		"created_by":    sa.CreatedBy,
		"act_as_user":   sa.ActAsUser,
		"owner_user_id": sa.OwnerUserID,
		"updated_at":    sa.UpdatedAt.UTC(),
		"id":            id,
	})
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
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM service_accounts WHERE id = @id", pgx.NamedArgs{"id": id})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
