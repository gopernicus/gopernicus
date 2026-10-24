package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// ServiceAccountStore implements serviceaccount.ServiceAccountRepository over a
// libSQL database. List is keyset-paginated in the pinned created_at DESC, id DESC
// order (the id tiebreak keeps pages stable across a shared created_at).
type ServiceAccountStore struct {
	db *tursodb.DB
}

var (
	_ serviceaccount.ServiceAccountRepository = (*ServiceAccountStore)(nil)
	_ serviceaccount.CascadeDeleter           = (*ServiceAccountStore)(nil)
)

// NewServiceAccountStore returns a ServiceAccountStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewServiceAccountStore(db *tursodb.DB) *ServiceAccountStore {
	if db == nil {
		panic("authentication turso: NewServiceAccountStore received a nil database")
	}
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

// Create persists a new service account. An account with an owner is inserted
// in the same write transaction as a read of the owner's erasure marker and is
// refused with user.ErrErased when the owner is erased; BEGIN IMMEDIATE orders
// it against a concurrent Erase, as FOR SHARE does on the pgx sibling.
func (s *ServiceAccountStore) Create(ctx context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if sa.OwnerUserID != "" {
			var erasedAt tursodb.NullTime
			err := tx.QueryRow(ctx, `SELECT erased_at FROM users WHERE id = ?`, sa.OwnerUserID).Scan(&erasedAt)
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return tursodb.MapError(err)
			case erasedAt.Valid:
				return fmt.Errorf("authentication turso store: service account owner %q: %w", sa.OwnerUserID, user.ErrErased)
			}
		}
		// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
		// column so the schema default generates the key, read back with RETURNING.
		if sa.ID == "" {
			const q = `INSERT INTO service_accounts (name, description, created_by, act_as_user, owner_user_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`
			return tursodb.MapError(tx.QueryRow(ctx, q,
				sa.Name, sa.Description, sa.CreatedBy, tursodb.BoolToInt(sa.ActAsUser),
				sa.OwnerUserID, tursodb.FormatTime(sa.CreatedAt), tursodb.FormatTime(sa.UpdatedAt),
			).Scan(&sa.ID))
		}
		const q = `INSERT INTO service_accounts (` + serviceAccountColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		_, err := tx.Exec(ctx, q,
			sa.ID, sa.Name, sa.Description, sa.CreatedBy, tursodb.BoolToInt(sa.ActAsUser),
			sa.OwnerUserID, tursodb.FormatTime(sa.CreatedAt), tursodb.FormatTime(sa.UpdatedAt),
		)
		return err
	})
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
func (s *ServiceAccountStore) List(ctx context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
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
		return list.Page[serviceaccount.ServiceAccount]{}, err
	}
	return list.MapPage(page, serviceAccountRow.toDomain), nil
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

// Delete removes the account for id and every API key it holds, in one
// transaction; unknown → sdk.ErrNotFound. See DeleteWithKeys.
func (s *ServiceAccountStore) Delete(ctx context.Context, id string) error {
	_, err := s.DeleteWithKeys(ctx, id)
	return err
}

// DeleteWithKeys is the serviceaccount.CascadeDeleter capability: the account,
// its keys (counted) and the account row go in one BEGIN IMMEDIATE transaction,
// so a concurrent key mint either commits first and is deleted here or runs
// after. Unknown id → sdk.ErrNotFound.
func (s *ServiceAccountStore) DeleteWithKeys(ctx context.Context, id string) (serviceaccount.Deletion, error) {
	var out serviceaccount.Deletion
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var found string
		if err := tx.QueryRow(ctx, `SELECT id FROM service_accounts WHERE id = ?`, id).Scan(&found); err != nil {
			return tursodb.MapError(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*), count(CASE WHEN revoked_at IS NULL THEN 1 END) FROM api_keys WHERE service_account_id = ?`, id).
			Scan(&out.KeyCount, &out.UnrevokedKeyCount); err != nil {
			return tursodb.MapError(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM api_keys WHERE service_account_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM service_accounts WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return serviceaccount.Deletion{}, err
	}
	return out, nil
}
