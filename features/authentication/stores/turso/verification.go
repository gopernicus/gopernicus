package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// CodeStore implements verification.CodeRepository over a libSQL database. Codes
// are opaque, stored plainly by their value; Get enforces expired-at-read.
type CodeStore struct {
	db *tursodb.DB
}

var _ verification.CodeRepository = (*CodeStore)(nil)

// NewCodeStore returns a CodeStore backed by db.
func NewCodeStore(db *tursodb.DB) *CodeStore {
	return &CodeStore{db: db}
}

const codeColumns = "code, user_id, created_at, expires_at"

// codeRow is the store-local, db-tagged projection of a verification_codes row.
type codeRow struct {
	Code      string       `db:"code"`
	UserID    string       `db:"user_id"`
	CreatedAt tursodb.Time `db:"created_at"`
	ExpiresAt tursodb.Time `db:"expires_at"`
}

func (r codeRow) toDomain() verification.Code {
	return verification.Code{
		Code:      r.Code,
		UserID:    r.UserID,
		CreatedAt: r.CreatedAt.Time,
		ExpiresAt: r.ExpiresAt.Time,
	}
}

// Create persists a new verification code.
func (s *CodeStore) Create(ctx context.Context, c verification.Code) (verification.Code, error) {
	const q = `INSERT INTO verification_codes (` + codeColumns + `) VALUES (?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, c.Code, c.UserID, tursodb.FormatTime(c.CreatedAt), tursodb.FormatTime(c.ExpiresAt))
	if err != nil {
		return verification.Code{}, err
	}
	return c, nil
}

// Get returns the live code: unknown → sdk.ErrNotFound, expired → sdk.ErrExpired.
func (s *CodeStore) Get(ctx context.Context, code string) (verification.Code, error) {
	const q = `SELECT ` + codeColumns + ` FROM verification_codes WHERE code = ?`
	row, err := queryOne[codeRow](ctx, s.db, q, code)
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
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM verification_codes WHERE code = ?", code)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// TokenStore implements verification.TokenRepository over a libSQL database.
// Tokens are opaque, stored plainly by their value; Get enforces expired-at-read.
type TokenStore struct {
	db *tursodb.DB
}

var _ verification.TokenRepository = (*TokenStore)(nil)

// NewTokenStore returns a TokenStore backed by db.
func NewTokenStore(db *tursodb.DB) *TokenStore {
	return &TokenStore{db: db}
}

const tokenColumns = "token, user_id, created_at, expires_at"

// tokenRow is the store-local, db-tagged projection of a verification_tokens row.
type tokenRow struct {
	Token     string       `db:"token"`
	UserID    string       `db:"user_id"`
	CreatedAt tursodb.Time `db:"created_at"`
	ExpiresAt tursodb.Time `db:"expires_at"`
}

func (r tokenRow) toDomain() verification.Token {
	return verification.Token{
		Token:     r.Token,
		UserID:    r.UserID,
		CreatedAt: r.CreatedAt.Time,
		ExpiresAt: r.ExpiresAt.Time,
	}
}

// Create persists a new reset token.
func (s *TokenStore) Create(ctx context.Context, t verification.Token) (verification.Token, error) {
	const q = `INSERT INTO verification_tokens (` + tokenColumns + `) VALUES (?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, t.Token, t.UserID, tursodb.FormatTime(t.CreatedAt), tursodb.FormatTime(t.ExpiresAt))
	if err != nil {
		return verification.Token{}, err
	}
	return t, nil
}

// Get returns the live token: unknown → sdk.ErrNotFound, expired → sdk.ErrExpired.
func (s *TokenStore) Get(ctx context.Context, token string) (verification.Token, error) {
	const q = `SELECT ` + tokenColumns + ` FROM verification_tokens WHERE token = ?`
	row, err := queryOne[tokenRow](ctx, s.db, q, token)
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
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM verification_tokens WHERE token = ?", token)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
