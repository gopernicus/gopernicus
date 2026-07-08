package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
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

// Create persists a new verification code.
func (s *CodeStore) Create(ctx context.Context, c verification.Code) (verification.Code, error) {
	const q = `INSERT INTO verification_codes (` + codeColumns + `) VALUES ($1, $2, $3, $4)`
	_, err := s.db.Exec(ctx, q, c.Code, c.UserID, c.CreatedAt.UTC(), c.ExpiresAt.UTC())
	if err != nil {
		return verification.Code{}, err
	}
	return c, nil
}

// Get returns the live code: unknown → errs.ErrNotFound, expired → errs.ErrExpired.
func (s *CodeStore) Get(ctx context.Context, code string) (verification.Code, error) {
	const q = `SELECT ` + codeColumns + ` FROM verification_codes WHERE code = $1`
	var (
		c                    verification.Code
		createdAt, expiresAt time.Time
	)
	if err := s.db.QueryRow(ctx, q, code).Scan(&c.Code, &c.UserID, &createdAt, &expiresAt); err != nil {
		return verification.Code{}, pgxdb.MapError(err)
	}
	c.CreatedAt = createdAt.UTC()
	c.ExpiresAt = expiresAt.UTC()
	if c.Expired(time.Now()) {
		return verification.Code{}, errs.ErrExpired
	}
	return c, nil
}

// Delete removes the code; unknown → errs.ErrNotFound.
func (s *CodeStore) Delete(ctx context.Context, code string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM verification_codes WHERE code = $1", code)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
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

// Create persists a new reset token.
func (s *TokenStore) Create(ctx context.Context, t verification.Token) (verification.Token, error) {
	const q = `INSERT INTO verification_tokens (` + tokenColumns + `) VALUES ($1, $2, $3, $4)`
	_, err := s.db.Exec(ctx, q, t.Token, t.UserID, t.CreatedAt.UTC(), t.ExpiresAt.UTC())
	if err != nil {
		return verification.Token{}, err
	}
	return t, nil
}

// Get returns the live token: unknown → errs.ErrNotFound, expired → errs.ErrExpired.
func (s *TokenStore) Get(ctx context.Context, token string) (verification.Token, error) {
	const q = `SELECT ` + tokenColumns + ` FROM verification_tokens WHERE token = $1`
	var (
		t                    verification.Token
		createdAt, expiresAt time.Time
	)
	if err := s.db.QueryRow(ctx, q, token).Scan(&t.Token, &t.UserID, &createdAt, &expiresAt); err != nil {
		return verification.Token{}, pgxdb.MapError(err)
	}
	t.CreatedAt = createdAt.UTC()
	t.ExpiresAt = expiresAt.UTC()
	if t.Expired(time.Now()) {
		return verification.Token{}, errs.ErrExpired
	}
	return t, nil
}

// Delete removes the token; unknown → errs.ErrNotFound.
func (s *TokenStore) Delete(ctx context.Context, token string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM verification_tokens WHERE token = $1", token)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}
