package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// UserStore implements user.UserRepository over a libSQL database. Identity (the
// addresses) lives in user_identifiers; a lost authentication claim on the
// primary identifier rolls a CreateWithPrimaryIdentifier back as
// sdk.ErrAlreadyExists via the connector's MapError.
type UserStore struct {
	db *tursodb.DB
}

var _ user.UserRepository = (*UserStore)(nil)

// NewUserStore returns a UserStore backed by db.
func NewUserStore(db *tursodb.DB) *UserStore {
	return &UserStore{db: db}
}

const userColumns = "id, display_name, auth_revision, created_at, updated_at"

// userRow is the store-local, db-tagged projection of a users row ScanStruct scans
// into; toDomain maps it to the persistence-free domain entity.
type userRow struct {
	ID           string       `db:"id"`
	DisplayName  string       `db:"display_name"`
	AuthRevision int64        `db:"auth_revision"`
	CreatedAt    tursodb.Time `db:"created_at"`
	UpdatedAt    tursodb.Time `db:"updated_at"`
}

func (r userRow) toDomain() user.User {
	return user.User{
		ID:           r.ID,
		DisplayName:  r.DisplayName,
		AuthRevision: r.AuthRevision,
		CreatedAt:    r.CreatedAt.Time,
		UpdatedAt:    r.UpdatedAt.Time,
	}
}

// CreateWithPrimaryIdentifier inserts the user and its first identifier in one
// transaction (design §2.2): the identifier's partial unique authentication claim
// rolls the whole aggregate back as sdk.ErrAlreadyExists. It targets the
// users.auth_revision column and the user_identifiers table. Empty IDs use the
// DB-generated convention (RETURNING id).
func (s *UserStore) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if u.ID == "" {
			const q = `INSERT INTO users (display_name, auth_revision, created_at, updated_at)
				VALUES (?, ?, ?, ?) RETURNING id`
			if err := tx.QueryRow(ctx, q,
				u.DisplayName, u.AuthRevision,
				tursodb.FormatTime(u.CreatedAt), tursodb.FormatTime(u.UpdatedAt),
			).Scan(&u.ID); err != nil {
				return tursodb.MapError(err)
			}
		} else {
			const q = `INSERT INTO users (id, display_name, auth_revision, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?)`
			if _, err := tx.Exec(ctx, q,
				u.ID, u.DisplayName, u.AuthRevision,
				tursodb.FormatTime(u.CreatedAt), tursodb.FormatTime(u.UpdatedAt),
			); err != nil {
				return tursodb.MapError(err)
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
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = ?`
	row, err := tursodb.QueryOne[userRow](ctx, s.db, q, id)
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
}

// Update persists changes to an existing user; missing id → sdk.ErrNotFound. It
// leaves id, created_at, and auth_revision unchanged (the revision-CAS paths own
// auth_revision).
func (s *UserStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	const q = `UPDATE users SET display_name=?, updated_at=? WHERE id=?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q,
		u.DisplayName, tursodb.FormatTime(u.UpdatedAt), id,
	)
	if err != nil {
		return user.User{}, err
	}
	if n == 0 {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}
