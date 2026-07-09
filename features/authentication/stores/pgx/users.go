package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// UserStore implements user.UserRepository over a PostgreSQL database. Uniqueness
// is on the normalized email (the users.email UNIQUE constraint), surfaced as
// errs.ErrAlreadyExists via the connector's MapError.
type UserStore struct {
	db *pgxdb.DB
}

var _ user.UserRepository = (*UserStore)(nil)

// NewUserStore returns a UserStore backed by db.
func NewUserStore(db *pgxdb.DB) *UserStore {
	return &UserStore{db: db}
}

const userColumns = "id, email, display_name, email_verified, created_at, updated_at"

// userRow is the store-local, db-tagged projection of a users row.
type userRow struct {
	ID            string    `db:"id"`
	Email         string    `db:"email"`
	DisplayName   string    `db:"display_name"`
	EmailVerified bool      `db:"email_verified"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

func (r userRow) toDomain() user.User {
	return user.User{
		ID:            r.ID,
		Email:         r.Email,
		DisplayName:   r.DisplayName,
		EmailVerified: r.EmailVerified,
		CreatedAt:     r.CreatedAt.UTC(),
		UpdatedAt:     r.UpdatedAt.UTC(),
	}
}

// Create persists a new user; a colliding normalized email → errs.ErrAlreadyExists.
func (s *UserStore) Create(ctx context.Context, u user.User) (user.User, error) {
	const q = `INSERT INTO users (` + userColumns + `)
		VALUES (@id, @email, @display_name, @email_verified, @created_at, @updated_at)`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"id":             u.ID,
		"email":          u.Email,
		"display_name":   u.DisplayName,
		"email_verified": u.EmailVerified,
		"created_at":     u.CreatedAt.UTC(),
		"updated_at":     u.UpdatedAt.UTC(),
	})
	if err != nil {
		return user.User{}, err
	}
	return u, nil
}

// Get returns the user with the given id, or errs.ErrNotFound.
func (s *UserStore) Get(ctx context.Context, id string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = @id`
	row, err := queryOne[userRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
}

// GetByEmail returns the user with the given normalized email, or errs.ErrNotFound.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = @email`
	row, err := queryOne[userRow](ctx, s.db, q, pgx.NamedArgs{"email": email})
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
}

// Update persists changes to an existing user; missing id → errs.ErrNotFound. It
// leaves id and created_at unchanged.
func (s *UserStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	const q = `UPDATE users
		SET email = @email, display_name = @display_name, email_verified = @email_verified, updated_at = @updated_at
		WHERE id = @id`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"email":          u.Email,
		"display_name":   u.DisplayName,
		"email_verified": u.EmailVerified,
		"updated_at":     u.UpdatedAt.UTC(),
		"id":             id,
	})
	if err != nil {
		return user.User{}, err
	}
	if n == 0 {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}
