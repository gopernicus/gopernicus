package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// CodeStore implements verification.CodeRepository over a PostgreSQL database.
// Codes are opaque, stored plainly by their value; Get enforces expired-at-read.
type CodeStore struct {
	db *pgxdb.DB
}

var _ verification.CodeRepository = (*CodeStore)(nil)

// NewCodeStore returns a CodeStore backed by db.
func NewCodeStore(db *pgxdb.DB) *CodeStore {
	return &CodeStore{db: db}
}

const codeColumns = "code, user_id, created_at, expires_at"

// codeRow is the store-local, db-tagged projection of a verification_codes row.
type codeRow struct {
	Code      string    `db:"code"`
	UserID    string    `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

func (r codeRow) toDomain() verification.Code {
	return verification.Code{
		Code:      r.Code,
		UserID:    r.UserID,
		CreatedAt: r.CreatedAt.UTC(),
		ExpiresAt: r.ExpiresAt.UTC(),
	}
}

// Create persists a new verification code.
func (s *CodeStore) Create(ctx context.Context, c verification.Code) (verification.Code, error) {
	const q = `INSERT INTO verification_codes (` + codeColumns + `)
		VALUES (@code, @user_id, @created_at, @expires_at)`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"code":       c.Code,
		"user_id":    c.UserID,
		"created_at": c.CreatedAt.UTC(),
		"expires_at": c.ExpiresAt.UTC(),
	})
	if err != nil {
		return verification.Code{}, err
	}
	return c, nil
}

// Get returns the live code: unknown → sdk.ErrNotFound, expired → sdk.ErrExpired.
func (s *CodeStore) Get(ctx context.Context, code string) (verification.Code, error) {
	const q = `SELECT ` + codeColumns + ` FROM verification_codes WHERE code = @code`
	row, err := pgxdb.QueryOne[codeRow](ctx, s.db, q, pgx.NamedArgs{"code": code})
	if err != nil {
		return verification.Code{}, err
	}
	c := row.toDomain()
	if c.Expired(time.Now()) {
		return verification.Code{}, sdk.ErrExpired
	}
	return c, nil
}

// Delete removes the code; unknown → sdk.ErrNotFound.
func (s *CodeStore) Delete(ctx context.Context, code string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM verification_codes WHERE code = @code", pgx.NamedArgs{"code": code})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// TokenStore implements verification.TokenRepository over a PostgreSQL database.
// Tokens are opaque, stored plainly by their value; Get enforces expired-at-read.
type TokenStore struct {
	db *pgxdb.DB
}

var _ verification.TokenRepository = (*TokenStore)(nil)

// NewTokenStore returns a TokenStore backed by db.
func NewTokenStore(db *pgxdb.DB) *TokenStore {
	return &TokenStore{db: db}
}

const tokenColumns = "token, user_id, created_at, expires_at"

// tokenRow is the store-local, db-tagged projection of a verification_tokens row.
type tokenRow struct {
	Token     string    `db:"token"`
	UserID    string    `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

func (r tokenRow) toDomain() verification.Token {
	return verification.Token{
		Token:     r.Token,
		UserID:    r.UserID,
		CreatedAt: r.CreatedAt.UTC(),
		ExpiresAt: r.ExpiresAt.UTC(),
	}
}

// Create persists a new reset token.
func (s *TokenStore) Create(ctx context.Context, t verification.Token) (verification.Token, error) {
	const q = `INSERT INTO verification_tokens (` + tokenColumns + `)
		VALUES (@token, @user_id, @created_at, @expires_at)`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"token":      t.Token,
		"user_id":    t.UserID,
		"created_at": t.CreatedAt.UTC(),
		"expires_at": t.ExpiresAt.UTC(),
	})
	if err != nil {
		return verification.Token{}, err
	}
	return t, nil
}

// Get returns the live token: unknown → sdk.ErrNotFound, expired → sdk.ErrExpired.
func (s *TokenStore) Get(ctx context.Context, token string) (verification.Token, error) {
	const q = `SELECT ` + tokenColumns + ` FROM verification_tokens WHERE token = @token`
	row, err := pgxdb.QueryOne[tokenRow](ctx, s.db, q, pgx.NamedArgs{"token": token})
	if err != nil {
		return verification.Token{}, err
	}
	t := row.toDomain()
	if t.Expired(time.Now()) {
		return verification.Token{}, sdk.ErrExpired
	}
	return t, nil
}

// Delete removes the token; unknown → sdk.ErrNotFound.
func (s *TokenStore) Delete(ctx context.Context, token string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM verification_tokens WHERE token = @token", pgx.NamedArgs{"token": token})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
