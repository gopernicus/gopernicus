package turso

import (
	"context"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/sdk"
)

// OAuthAccountStore implements oauthaccount.OAuthAccountRepository over a libSQL
// database. Uniqueness is on the (provider, provider_user_id) primary key — a
// provider identity belongs to at most one local user — surfaced as
// sdk.ErrAlreadyExists via MapError. There is NO upsert: a colliding Create is an
// error, never a silent overwrite (design §3 — upsert is outside the port and
// dialect-divergent). The token columns are persisted verbatim (ciphertext when an
// encrypter is wired, else empty).
type OAuthAccountStore struct {
	db *tursodb.DB
}

var _ oauthaccount.OAuthAccountRepository = (*OAuthAccountStore)(nil)

// NewOAuthAccountStore returns an OAuthAccountStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewOAuthAccountStore(db *tursodb.DB) *OAuthAccountStore {
	if db == nil {
		panic("authentication turso: NewOAuthAccountStore received a nil database")
	}
	return &OAuthAccountStore{db: db}
}

const oauthAccountColumns = "provider, provider_user_id, user_id, provider_email, provider_email_verified, account_verified, linked_at, access_token, refresh_token, token_expires_at, token_type, scope"

// oauthAccountRow is the store-local, db-tagged projection of an oauth_accounts
// row. token_expires_at is nullable (turso.NullTime, zero-time when NULL).
type oauthAccountRow struct {
	Provider              string           `db:"provider"`
	ProviderUserID        string           `db:"provider_user_id"`
	UserID                string           `db:"user_id"`
	ProviderEmail         string           `db:"provider_email"`
	ProviderEmailVerified tursodb.Bool     `db:"provider_email_verified"`
	AccountVerified       tursodb.Bool     `db:"account_verified"`
	LinkedAt              tursodb.Time     `db:"linked_at"`
	AccessToken           string           `db:"access_token"`
	RefreshToken          string           `db:"refresh_token"`
	TokenExpiresAt        tursodb.NullTime `db:"token_expires_at"`
	TokenType             string           `db:"token_type"`
	Scope                 string           `db:"scope"`
}

func (r oauthAccountRow) toDomain() oauthaccount.OAuthAccount {
	return oauthaccount.OAuthAccount{
		Provider:              r.Provider,
		ProviderUserID:        r.ProviderUserID,
		UserID:                r.UserID,
		ProviderEmail:         r.ProviderEmail,
		ProviderEmailVerified: bool(r.ProviderEmailVerified),
		AccountVerified:       bool(r.AccountVerified),
		LinkedAt:              r.LinkedAt.Time,
		AccessToken:           r.AccessToken,
		RefreshToken:          r.RefreshToken,
		TokenExpiresAt:        r.TokenExpiresAt.Time,
		TokenType:             r.TokenType,
		Scope:                 r.Scope,
	}
}

// Create persists a new link; a colliding (provider, provider_user_id) →
// sdk.ErrAlreadyExists (plain INSERT, no ON CONFLICT).
func (s *OAuthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	var created oauthaccount.OAuthAccount
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, a.UserID); err != nil {
			return err
		}
		var err error
		created, err = insertOAuthAccount(ctx, tx, a)
		return err
	})
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return created, nil
}

func insertOAuthAccount(ctx context.Context, tx *tursodb.Tx, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	const q = `INSERT INTO oauth_accounts (` + oauthAccountColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.Exec(ctx, q,
		a.Provider, a.ProviderUserID, a.UserID, a.ProviderEmail,
		tursodb.BoolToInt(a.ProviderEmailVerified), tursodb.BoolToInt(a.AccountVerified),
		tursodb.FormatTime(a.LinkedAt), a.AccessToken, a.RefreshToken,
		tursodb.FormatNullTime(a.TokenExpiresAt), a.TokenType, a.Scope,
	)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}

// GetByProvider returns the link for a provider identity, or sdk.ErrNotFound.
func (s *OAuthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	const q = `SELECT ` + oauthAccountColumns + ` FROM oauth_accounts WHERE provider = ? AND provider_user_id = ?`
	row, err := tursodb.QueryOne[oauthAccountRow](ctx, s.db, q, provider, providerUserID)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return row.toDomain(), nil
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
		row, err := tursodb.ScanStruct[oauthAccountRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// Delete removes userID's link to provider; no such link → sdk.ErrNotFound.
func (s *OAuthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, userID); err != nil {
			return err
		}
		n, err := tursodb.ExecAffecting(ctx, tx, `DELETE FROM oauth_accounts WHERE user_id = ? AND provider = ?`, userID, provider)
		if err != nil {
			return err
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		if err := bumpCredentialRevision(ctx, tx, userID, time.Now().UTC()); err != nil {
			return err
		}
		return revokeCredentialState(ctx, tx, userID)
	})
}

// Link makes adoption and provider attachment one conditional credential commit.
func (s *OAuthAccountStore) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	var revision int64
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		current, err := lockCredentialUser(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}
		if adoptIdentifierID != "" {
			n, err := tursodb.ExecAffecting(ctx, tx, `UPDATE user_identifiers SET verified_at = ?, updated_at = ? WHERE id = ? AND user_id = ? AND kind = 'email' AND replaced_at IS NULL AND (login_enabled = TRUE OR recovery_enabled = TRUE)`, tursodb.FormatTime(now), tursodb.FormatTime(now), adoptIdentifierID, a.UserID)
			if err != nil {
				return err
			}
			if n != 1 {
				return sdk.ErrConflict
			}
			if _, err := tx.Exec(ctx, `DELETE FROM user_passwords WHERE user_id = ?`, a.UserID); err != nil {
				return err
			}
			if err := revokeCredentialState(ctx, tx, a.UserID); err != nil {
				return err
			}
		}
		if _, err := insertOAuthAccount(ctx, tx, a); err != nil {
			return err
		}
		if err := bumpCredentialRevision(ctx, tx, a.UserID, now); err != nil {
			return err
		}
		revision = current + 1
		return nil
	})
	if err != nil {
		return oauthaccount.OAuthAccount{}, 0, err
	}
	return a, revision, nil
}
