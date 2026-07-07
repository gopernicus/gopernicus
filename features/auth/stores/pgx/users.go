package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/user"
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

// Create persists a new user; a colliding normalized email → errs.ErrAlreadyExists.
func (s *UserStore) Create(ctx context.Context, u user.User) (user.User, error) {
	const q = `INSERT INTO users (` + userColumns + `) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.db.Exec(ctx, q,
		u.ID, u.Email, u.DisplayName, u.EmailVerified,
		u.CreatedAt.UTC(), u.UpdatedAt.UTC(),
	)
	if err != nil {
		return user.User{}, err
	}
	return u, nil
}

// Get returns the user with the given id, or errs.ErrNotFound.
func (s *UserStore) Get(ctx context.Context, id string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	return scanUser(s.db.QueryRow(ctx, q, id))
}

// GetByEmail returns the user with the given normalized email, or errs.ErrNotFound.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = $1`
	return scanUser(s.db.QueryRow(ctx, q, email))
}

// Update persists changes to an existing user; missing id → errs.ErrNotFound. It
// leaves id and created_at unchanged.
func (s *UserStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	const q = `UPDATE users SET email=$1, display_name=$2, email_verified=$3, updated_at=$4 WHERE id=$5`
	res, err := s.db.Exec(ctx, q,
		u.Email, u.DisplayName, u.EmailVerified, u.UpdatedAt.UTC(), id,
	)
	if err != nil {
		return user.User{}, err
	}
	if res.RowsAffected() == 0 {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}

// scanUser scans one users row into a User, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanUser(sc scanner) (user.User, error) {
	var (
		u                    user.User
		createdAt, updatedAt time.Time
	)
	if err := sc.Scan(&u.ID, &u.Email, &u.DisplayName, &u.EmailVerified, &createdAt, &updatedAt); err != nil {
		return user.User{}, pgxdb.MapError(err)
	}
	u.CreatedAt = createdAt.UTC()
	u.UpdatedAt = updatedAt.UTC()
	return u, nil
}
