package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

// ServiceAccountStore implements serviceaccount.ServiceAccountRepository over a
// PostgreSQL database. List is keyset-paginated in the pinned created_at DESC,
// id DESC order (the id tiebreak keeps pages stable across a shared created_at).
type ServiceAccountStore struct {
	db *pgxdb.DB
	qualified
}

var (
	_ serviceaccount.ServiceAccountRepository = (*ServiceAccountStore)(nil)
	_ serviceaccount.CascadeDeleter           = (*ServiceAccountStore)(nil)
)

// NewServiceAccountStore returns a ServiceAccountStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewServiceAccountStore(db *pgxdb.DB, opts ...Option) *ServiceAccountStore {
	if db == nil {
		panic("authentication pgx: NewServiceAccountStore received a nil database")
	}
	return &ServiceAccountStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
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

// Create persists a new service account. An account with an owner is inserted
// under FOR SHARE on the owner's users row and refused with user.ErrErased when
// the owner is erased: the lock orders the insert against a concurrent Erase,
// which takes FOR UPDATE on the same row, so the insert either commits first
// (and the erase deletes an act-as account with it) or sees the marker.
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
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if sa.OwnerUserID != "" {
			var erasedAt *time.Time
			err := tx.QueryRow(ctx, `SELECT erased_at FROM `+s.table(usersTable)+` WHERE id = @id FOR SHARE`,
				pgx.NamedArgs{"id": sa.OwnerUserID}).Scan(&erasedAt)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return pgxdb.MapError(err)
			case erasedAt != nil:
				return fmt.Errorf("authentication pgx store: service account owner %q: %w", sa.OwnerUserID, user.ErrErased)
			}
		}
		// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
		// column so the schema default generates the key, read back with RETURNING.
		if sa.ID == "" {
			q := `INSERT INTO ` + s.table(serviceAccountsTable) + ` (name, description, created_by, act_as_user, owner_user_id, created_at, updated_at)
				VALUES (@name, @description, @created_by, @act_as_user, @owner_user_id, @created_at, @updated_at)
				RETURNING id`
			return pgxdb.MapError(tx.QueryRow(ctx, q, args).Scan(&sa.ID))
		}
		q := `INSERT INTO ` + s.table(serviceAccountsTable) + ` (` + serviceAccountColumns + `)
			VALUES (@id, @name, @description, @created_by, @act_as_user, @owner_user_id, @created_at, @updated_at)`
		args["id"] = sa.ID
		_, err := tx.Exec(ctx, q, args)
		return err
	})
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return sa, nil
}

// Get returns the account for id, or sdk.ErrNotFound.
func (s *ServiceAccountStore) Get(ctx context.Context, id string) (serviceaccount.ServiceAccount, error) {
	q := `SELECT ` + serviceAccountColumns + ` FROM ` + s.table(serviceAccountsTable) + ` WHERE id = @id`
	row, err := pgxdb.QueryOne[serviceAccountRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return row.toDomain(), nil
}

// List returns a cursor-paginated page ordered created_at DESC, id DESC.
func (s *ServiceAccountStore) List(ctx context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
	q := pgxdb.ListQuery[serviceAccountRow]{
		BaseSQL:      `SELECT ` + serviceAccountColumns + ` FROM ` + s.table(serviceAccountsTable),
		OrderFields:  serviceaccount.OrderFields,
		DefaultOrder: serviceaccount.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r serviceAccountRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r serviceAccountRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return list.Page[serviceaccount.ServiceAccount]{}, err
	}
	return list.MapPage(page, serviceAccountRow.toDomain), nil
}

// Update replaces the account for id; unknown → sdk.ErrNotFound. It leaves id and
// created_at unchanged.
func (s *ServiceAccountStore) Update(ctx context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	q := `UPDATE ` + s.table(serviceAccountsTable) + `
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

// Delete removes the account for id and every API key it holds, in one
// transaction; unknown → sdk.ErrNotFound. See DeleteWithKeys.
func (s *ServiceAccountStore) Delete(ctx context.Context, id string) error {
	_, err := s.DeleteWithKeys(ctx, id)
	return err
}

// DeleteWithKeys is the serviceaccount.CascadeDeleter capability: the account
// row is locked FOR UPDATE — APIKeyStore.Create takes FOR SHARE on it, so a
// concurrent mint either commits first and is deleted here or waits and finds
// the account gone — then its keys and the account are deleted and counted.
// Unknown id → sdk.ErrNotFound.
func (s *ServiceAccountStore) DeleteWithKeys(ctx context.Context, id string) (serviceaccount.Deletion, error) {
	var out serviceaccount.Deletion
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM `+s.table(serviceAccountsTable)+` WHERE id = @id FOR UPDATE`,
			pgx.NamedArgs{"id": id}).Scan(&locked); err != nil {
			return pgxdb.MapError(err)
		}
		if err := tx.QueryRow(ctx, `WITH gone AS (DELETE FROM `+s.table(apiKeysTable)+` WHERE service_account_id = @id RETURNING revoked_at)
			SELECT count(*), count(*) FILTER (WHERE revoked_at IS NULL) FROM gone`,
			pgx.NamedArgs{"id": id}).Scan(&out.KeyCount, &out.UnrevokedKeyCount); err != nil {
			return pgxdb.MapError(err)
		}
		_, err := tx.Exec(ctx, `DELETE FROM `+s.table(serviceAccountsTable)+` WHERE id = @id`, pgx.NamedArgs{"id": id})
		return err
	})
	if err != nil {
		return serviceaccount.Deletion{}, err
	}
	return out, nil
}
