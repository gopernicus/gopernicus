package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// OAuthAccountStore implements oauthaccount.OAuthAccountRepository over a
// PostgreSQL database. Uniqueness is on the (provider, provider_user_id) primary
// key — a provider identity belongs to at most one local user — surfaced as
// errs.ErrAlreadyExists via MapError. There is NO upsert: a colliding Create is an
// error, never a silent overwrite (design §3 — upsert is outside the port and
// dialect-divergent). The token columns are persisted verbatim (ciphertext when an
// encrypter is wired, else empty).
type OAuthAccountStore struct {
	db *pgxdb.DB
}

var _ oauthaccount.OAuthAccountRepository = (*OAuthAccountStore)(nil)

// NewOAuthAccountStore returns an OAuthAccountStore backed by db.
func NewOAuthAccountStore(db *pgxdb.DB) *OAuthAccountStore {
	return &OAuthAccountStore{db: db}
}

const oauthAccountColumns = "provider, provider_user_id, user_id, provider_email, provider_email_verified, account_verified, linked_at, access_token, refresh_token, token_expires_at, token_type, scope"

// Create persists a new link; a colliding (provider, provider_user_id) →
// errs.ErrAlreadyExists (plain INSERT, no ON CONFLICT).
func (s *OAuthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	const q = `INSERT INTO oauth_accounts (` + oauthAccountColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := s.db.Exec(ctx, q,
		a.Provider, a.ProviderUserID, a.UserID, a.ProviderEmail,
		a.ProviderEmailVerified, a.AccountVerified,
		a.LinkedAt.UTC(), a.AccessToken, a.RefreshToken,
		pgxdb.NullTime(a.TokenExpiresAt), a.TokenType, a.Scope,
	)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}

// GetByProvider returns the link for a provider identity, or errs.ErrNotFound.
func (s *OAuthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	const q = `SELECT ` + oauthAccountColumns + ` FROM oauth_accounts WHERE provider = $1 AND provider_user_id = $2`
	return scanOAuthAccount(s.db.QueryRow(ctx, q, provider, providerUserID))
}

// ListByUser returns every link owned by userID (empty slice, nil error when none).
func (s *OAuthAccountStore) ListByUser(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	const q = `SELECT ` + oauthAccountColumns + ` FROM oauth_accounts WHERE user_id = $1 ORDER BY linked_at DESC, provider_user_id DESC`
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
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

// Delete removes userID's link to provider; no such link → errs.ErrNotFound.
func (s *OAuthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM oauth_accounts WHERE user_id = $1 AND provider = $2", userID, provider)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanOAuthAccount scans one oauth_accounts row, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanOAuthAccount(sc scanner) (oauthaccount.OAuthAccount, error) {
	var (
		a              oauthaccount.OAuthAccount
		linkedAt       time.Time
		tokenExpiresAt *time.Time
	)
	if err := sc.Scan(
		&a.Provider, &a.ProviderUserID, &a.UserID, &a.ProviderEmail,
		&a.ProviderEmailVerified, &a.AccountVerified, &linkedAt, &a.AccessToken, &a.RefreshToken,
		&tokenExpiresAt, &a.TokenType, &a.Scope,
	); err != nil {
		return oauthaccount.OAuthAccount{}, pgxdb.MapError(err)
	}
	a.LinkedAt = linkedAt.UTC()
	a.TokenExpiresAt = pgxdb.FromNullTime(tokenExpiresAt)
	return a, nil
}
