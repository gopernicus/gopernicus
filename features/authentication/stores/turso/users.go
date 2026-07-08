package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// UserStore implements user.UserRepository over a libSQL database. Uniqueness is
// on the normalized email (the users.email UNIQUE constraint), surfaced as
// errs.ErrAlreadyExists via the connector's MapError.
type UserStore struct {
	db *tursodb.DB
}

var _ user.UserRepository = (*UserStore)(nil)

// NewUserStore returns a UserStore backed by db.
func NewUserStore(db *tursodb.DB) *UserStore {
	return &UserStore{db: db}
}

const userColumns = "id, email, display_name, email_verified, created_at, updated_at"

// Create persists a new user; a colliding normalized email → errs.ErrAlreadyExists.
func (s *UserStore) Create(ctx context.Context, u user.User) (user.User, error) {
	const q = `INSERT INTO users (` + userColumns + `) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		u.ID, u.Email, u.DisplayName, tursodb.BoolToInt(u.EmailVerified),
		tursodb.FormatTime(u.CreatedAt), tursodb.FormatTime(u.UpdatedAt),
	)
	if err != nil {
		return user.User{}, err
	}
	return u, nil
}

// Get returns the user with the given id, or errs.ErrNotFound.
func (s *UserStore) Get(ctx context.Context, id string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = ?`
	return scanUser(s.db.QueryRow(ctx, q, id))
}

// GetByEmail returns the user with the given normalized email, or errs.ErrNotFound.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = ?`
	return scanUser(s.db.QueryRow(ctx, q, email))
}

// Update persists changes to an existing user; missing id → errs.ErrNotFound. It
// leaves id and created_at unchanged.
func (s *UserStore) Update(ctx context.Context, id string, u user.User) (user.User, error) {
	const q = `UPDATE users SET email=?, display_name=?, email_verified=?, updated_at=? WHERE id=?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q,
		u.Email, u.DisplayName, tursodb.BoolToInt(u.EmailVerified), tursodb.FormatTime(u.UpdatedAt), id,
	)
	if err != nil {
		return user.User{}, err
	}
	if n == 0 {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}

// scanUser scans one users row into a User, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanUser(sc scanner) (user.User, error) {
	var (
		u                    user.User
		verified             int64
		createdAt, updatedAt string
	)
	if err := sc.Scan(&u.ID, &u.Email, &u.DisplayName, &verified, &createdAt, &updatedAt); err != nil {
		return user.User{}, tursodb.MapError(err)
	}
	u.EmailVerified = verified != 0
	var err error
	if u.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return user.User{}, err
	}
	if u.UpdatedAt, err = tursodb.ParseTime(updatedAt); err != nil {
		return user.User{}, err
	}
	return u, nil
}
