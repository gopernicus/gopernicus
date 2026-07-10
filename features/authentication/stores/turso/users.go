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

// userRow is the store-local, db-tagged projection of a users row ScanStruct scans
// into; toDomain maps it to the persistence-free domain entity.
type userRow struct {
	ID            string       `db:"id"`
	Email         string       `db:"email"`
	DisplayName   string       `db:"display_name"`
	EmailVerified tursodb.Bool `db:"email_verified"`
	CreatedAt     tursodb.Time `db:"created_at"`
	UpdatedAt     tursodb.Time `db:"updated_at"`
}

func (r userRow) toDomain() user.User {
	return user.User{
		ID:            r.ID,
		Email:         r.Email,
		DisplayName:   r.DisplayName,
		EmailVerified: bool(r.EmailVerified),
		CreatedAt:     r.CreatedAt.Time,
		UpdatedAt:     r.UpdatedAt.Time,
	}
}

// Create persists a new user; a colliding normalized email → errs.ErrAlreadyExists.
func (s *UserStore) Create(ctx context.Context, u user.User) (user.User, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if u.ID == "" {
		const q = `INSERT INTO users (email, display_name, email_verified, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			u.Email, u.DisplayName, tursodb.BoolToInt(u.EmailVerified),
			tursodb.FormatTime(u.CreatedAt), tursodb.FormatTime(u.UpdatedAt),
		).Scan(&u.ID); err != nil {
			return user.User{}, tursodb.MapError(err)
		}
		return u, nil
	}
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
	row, err := queryOne[userRow](ctx, s.db, q, id)
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
}

// GetByEmail returns the user with the given normalized email, or errs.ErrNotFound.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = ?`
	row, err := queryOne[userRow](ctx, s.db, q, email)
	if err != nil {
		return user.User{}, err
	}
	return row.toDomain(), nil
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
