package turso

import (
	"context"
	"database/sql"

	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// OAuthAccountStore implements oauthaccount.OAuthAccountRepository over a libSQL
// database. Uniqueness is on the (provider, provider_user_id) primary key — a
// provider identity belongs to at most one local user — surfaced as
// errs.ErrAlreadyExists via MapError. There is NO upsert: a colliding Create is an
// error, never a silent overwrite (design §3 — upsert is outside the port and
// dialect-divergent). The token columns are persisted verbatim (ciphertext when an
// encrypter is wired, else empty).
type OAuthAccountStore struct {
	db *tursodb.DB
}

var _ oauthaccount.OAuthAccountRepository = (*OAuthAccountStore)(nil)

// NewOAuthAccountStore returns an OAuthAccountStore backed by db.
func NewOAuthAccountStore(db *tursodb.DB) *OAuthAccountStore {
	return &OAuthAccountStore{db: db}
}

const oauthAccountColumns = "provider, provider_user_id, user_id, provider_email, provider_email_verified, account_verified, linked_at, access_token, refresh_token, token_expires_at, token_type, scope"

// Create persists a new link; a colliding (provider, provider_user_id) →
// errs.ErrAlreadyExists (plain INSERT, no ON CONFLICT).
func (s *OAuthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	const q = `INSERT INTO oauth_accounts (` + oauthAccountColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		a.Provider, a.ProviderUserID, a.UserID, a.ProviderEmail,
		tursodb.BoolToInt(a.ProviderEmailVerified), tursodb.BoolToInt(a.AccountVerified),
		tursodb.FormatTime(a.LinkedAt), a.AccessToken, a.RefreshToken,
		tursodb.NullTime(a.TokenExpiresAt), a.TokenType, a.Scope,
	)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}

// GetByProvider returns the link for a provider identity, or errs.ErrNotFound.
func (s *OAuthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	const q = `SELECT ` + oauthAccountColumns + ` FROM oauth_accounts WHERE provider = ? AND provider_user_id = ?`
	return scanOAuthAccount(s.db.QueryRow(ctx, q, provider, providerUserID))
}

// ListByUser returns every link owned by userID (empty slice, nil error when none).
func (s *OAuthAccountStore) ListByUser(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	const q = `SELECT ` + oauthAccountColumns + ` FROM oauth_accounts WHERE user_id = ? ORDER BY linked_at DESC, provider_user_id DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []oauthaccount.OAuthAccount{}
	for rows.Next() {
		a, err := scanOAuthAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// Delete removes userID's link to provider; no such link → errs.ErrNotFound.
func (s *OAuthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	res, err := s.db.Exec(ctx, "DELETE FROM oauth_accounts WHERE user_id = ? AND provider = ?", userID, provider)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanOAuthAccount scans one oauth_accounts row, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanOAuthAccount(sc scanner) (oauthaccount.OAuthAccount, error) {
	var (
		a                           oauthaccount.OAuthAccount
		emailVerified, acctVerified int64
		linkedAt                    string
		tokenExpiresAt              sql.NullString
	)
	if err := sc.Scan(
		&a.Provider, &a.ProviderUserID, &a.UserID, &a.ProviderEmail,
		&emailVerified, &acctVerified, &linkedAt, &a.AccessToken, &a.RefreshToken,
		&tokenExpiresAt, &a.TokenType, &a.Scope,
	); err != nil {
		return oauthaccount.OAuthAccount{}, tursodb.MapError(err)
	}
	a.ProviderEmailVerified = emailVerified != 0
	a.AccountVerified = acctVerified != 0
	var err error
	if a.LinkedAt, err = tursodb.ParseTime(linkedAt); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	if a.TokenExpiresAt, err = tursodb.ParseNullTime(tokenExpiresAt); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}
