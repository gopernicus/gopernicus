package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// UserStore implements user.UserRepository over a PostgreSQL database. Identity
// (the addresses) lives in user_identifiers; a lost authentication claim on the
// primary identifier rolls a CreateWithPrimaryIdentifier back as
// sdk.ErrAlreadyExists via the connector's MapError.
type UserStore struct {
	db *pgxdb.DB
}

var _ user.UserRepository = (*UserStore)(nil)

// NewUserStore returns a UserStore backed by db.
func NewUserStore(db *pgxdb.DB) *UserStore {
	return &UserStore{db: db}
}

const userColumns = "id, display_name, auth_revision, created_at, updated_at"

// userRow is the store-local, db-tagged projection of a users row.
type userRow struct {
	ID           string    `db:"id"`
	DisplayName  string    `db:"display_name"`
	AuthRevision int64     `db:"auth_revision"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func (r userRow) toDomain() user.User {
	return user.User{
		ID:           r.ID,
		DisplayName:  r.DisplayName,
		AuthRevision: r.AuthRevision,
		CreatedAt:    r.CreatedAt.UTC(),
		UpdatedAt:    r.UpdatedAt.UTC(),
	}
}

// CreateWithPrimaryIdentifier inserts the user and its first identifier in one
// transaction (design §2.2): the identifier's partial unique authentication claim
// rolls the whole aggregate back as sdk.ErrAlreadyExists. It targets the
// users.auth_revision column and the user_identifiers table. Empty IDs use the
// DB-generated convention (RETURNING id).
func (s *UserStore) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		userArgs := pgx.NamedArgs{
			"display_name":  u.DisplayName,
			"auth_revision": u.AuthRevision,
			"created_at":    u.CreatedAt.UTC(),
			"updated_at":    u.UpdatedAt.UTC(),
		}
		if u.ID == "" {
			const q = `INSERT INTO users (display_name, auth_revision, created_at, updated_at)
				VALUES (@display_name, @auth_revision, @created_at, @updated_at)
				RETURNING id`
			if err := tx.QueryRow(ctx, q, userArgs).Scan(&u.ID); err != nil {
				return pgxdb.MapError(err)
			}
		} else {
			userArgs["id"] = u.ID
			const q = `INSERT INTO users (id, display_name, auth_revision, created_at, updated_at)
				VALUES (@id, @display_name, @auth_revision, @created_at, @updated_at)`
			if _, err := tx.Exec(ctx, q, userArgs); err != nil {
				return pgxdb.MapError(err)
			}
		}
		ident.UserID = u.ID
		created, err := insertIdentifier(ctx, tx, ident)
		if err != nil {
			return err
		}
		ident = created
		return nil
	})
	if err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	return u, ident, nil
}

// Get returns the user with the given id, or sdk.ErrNotFound.
func (s *UserStore) Get(ctx context.Context, id string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = @id`
	row, err := pgxdb.QueryOne[userRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
}

// Update persists changes to an existing user; missing id → sdk.ErrNotFound. It
// leaves id, created_at, and auth_revision unchanged (the revision-CAS paths own
// auth_revision).
func (s *UserStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	const q = `UPDATE users
		SET display_name = @display_name, updated_at = @updated_at
		WHERE id = @id`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"display_name": u.DisplayName,
		"updated_at":   u.UpdatedAt.UTC(),
		"id":           id,
	})
	if err != nil {
		return user.User{}, err
	}
	if n == 0 {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}
