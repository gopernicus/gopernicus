package pgx

import (
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// OAuthAccountStore implements oauthaccount.OAuthAccountRepository over a
// PostgreSQL database. Uniqueness is on the (provider, provider_user_id) primary
// key — a provider identity belongs to at most one local user — surfaced as
// sdk.ErrAlreadyExists via MapError. There is NO upsert: a colliding Create is an
// error, never a silent overwrite (design §3 — upsert is outside the port and
// dialect-divergent). The token columns are persisted verbatim (ciphertext when an
// encrypter is wired, else empty).
type OAuthAccountStore struct {
	db *pgxdb.DB
	qualified
}

var _ oauthaccount.OAuthAccountRepository = (*OAuthAccountStore)(nil)

// NewOAuthAccountStore returns an OAuthAccountStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewOAuthAccountStore(db *pgxdb.DB, opts ...Option) *OAuthAccountStore {
	if db == nil {
		panic("authentication pgx: NewOAuthAccountStore received a nil database")
	}
	return &OAuthAccountStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

const oauthAccountColumns = "provider, provider_user_id, user_id, provider_email, provider_email_verified, account_verified, linked_at, access_token, refresh_token, token_expires_at, token_type, scope"

// oauthAccountRow is the store-local, db-tagged projection of an oauth_accounts
// row. token_expires_at is nullable (a pointer, zero-time when NULL).
type oauthAccountRow struct {
	Provider              string     `db:"provider"`
	ProviderUserID        string     `db:"provider_user_id"`
	UserID                string     `db:"user_id"`
	ProviderEmail         string     `db:"provider_email"`
	ProviderEmailVerified bool       `db:"provider_email_verified"`
	AccountVerified       bool       `db:"account_verified"`
	LinkedAt              time.Time  `db:"linked_at"`
	AccessToken           string     `db:"access_token"`
	RefreshToken          string     `db:"refresh_token"`
	TokenExpiresAt        *time.Time `db:"token_expires_at"`
	TokenType             string     `db:"token_type"`
	Scope                 string     `db:"scope"`
}

func (r oauthAccountRow) toDomain() oauthaccount.OAuthAccount {
	return oauthaccount.OAuthAccount{
		Provider:              r.Provider,
		ProviderUserID:        r.ProviderUserID,
		UserID:                r.UserID,
		ProviderEmail:         r.ProviderEmail,
		ProviderEmailVerified: r.ProviderEmailVerified,
		AccountVerified:       r.AccountVerified,
		LinkedAt:              r.LinkedAt.UTC(),
		AccessToken:           r.AccessToken,
		RefreshToken:          r.RefreshToken,
		TokenExpiresAt:        pgxdb.FromNullTime(r.TokenExpiresAt),
		TokenType:             r.TokenType,
		Scope:                 r.Scope,
	}
}

// Create persists a new link; a colliding (provider, provider_user_id) →
// sdk.ErrAlreadyExists (plain INSERT, no ON CONFLICT).
func (s *OAuthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	var created oauthaccount.OAuthAccount
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, s.qualified, a.UserID); err != nil {
			return err
		}
		var err error
		created, err = insertOAuthAccount(ctx, tx, s.qualified, a)
		return err
	})
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return created, nil
}

func insertOAuthAccount(ctx context.Context, tx *pgxdb.Tx, qualifiedTables qualified, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	q := `INSERT INTO ` + qualifiedTables.table(oauthAccountsTable) + ` (` + oauthAccountColumns + `)
		VALUES (@provider, @provider_user_id, @user_id, @provider_email, @provider_email_verified,
			@account_verified, @linked_at, @access_token, @refresh_token, @token_expires_at, @token_type, @scope)`
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"provider":                a.Provider,
		"provider_user_id":        a.ProviderUserID,
		"user_id":                 a.UserID,
		"provider_email":          a.ProviderEmail,
		"provider_email_verified": a.ProviderEmailVerified,
		"account_verified":        a.AccountVerified,
		"linked_at":               a.LinkedAt.UTC(),
		"access_token":            a.AccessToken,
		"refresh_token":           a.RefreshToken,
		"token_expires_at":        pgxdb.NullTime(a.TokenExpiresAt),
		"token_type":              a.TokenType,
		"scope":                   a.Scope,
	})
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}

// GetByProvider returns the link for a provider identity, or sdk.ErrNotFound.
func (s *OAuthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	q := `SELECT ` + oauthAccountColumns + ` FROM ` + s.table(oauthAccountsTable) + ` WHERE provider = @provider AND provider_user_id = @provider_user_id`
	row, err := pgxdb.QueryOne[oauthAccountRow](ctx, s.db, q, pgx.NamedArgs{"provider": provider, "provider_user_id": providerUserID})
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return row.toDomain(), nil
}

// ListByUser returns every link owned by userID (empty slice, nil error when none).
func (s *OAuthAccountStore) ListByUser(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	q := `SELECT ` + oauthAccountColumns + ` FROM ` + s.table(oauthAccountsTable) + ` WHERE user_id = @user_id ORDER BY linked_at DESC, provider_user_id DESC`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"user_id": userID})
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[oauthAccountRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out := make([]oauthaccount.OAuthAccount, 0, len(list))
	for _, r := range list {
		out = append(out, r.toDomain())
	}
	return out, nil
}

// Delete removes userID's link to provider; no such link → sdk.ErrNotFound.
func (s *OAuthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, s.qualified, userID); err != nil {
			return err
		}
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(oauthAccountsTable)+` WHERE user_id = @id AND provider = @provider`, pgx.NamedArgs{"id": userID, "provider": provider})
		if err != nil {
			return err
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		if err := bumpCredentialRevision(ctx, tx, s.qualified, userID, time.Now().UTC()); err != nil {
			return err
		}
		return revokeCredentialState(ctx, tx, s.qualified, userID)
	})
}

// Link makes adoption and provider attachment one conditional credential commit.
func (s *OAuthAccountStore) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	var revision int64
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		current, err := lockCredentialUser(ctx, tx, s.qualified, a.UserID)
		if err != nil {
			return err
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}
		if adoptIdentifierID != "" {
			n, err := pgxdb.ExecAffecting(ctx, tx, `UPDATE `+s.table(identifiersTable)+` SET verified_at = @now, updated_at = @now WHERE id = @identifier_id AND user_id = @id AND kind = 'email' AND replaced_at IS NULL AND (login_enabled = TRUE OR recovery_enabled = TRUE)`, pgx.NamedArgs{"id": a.UserID, "identifier_id": adoptIdentifierID, "now": now.UTC()})
			if err != nil {
				return err
			}
			if n != 1 {
				return sdk.ErrConflict
			}
			if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(passwordsTable)+` WHERE user_id = @id`, pgx.NamedArgs{"id": a.UserID}); err != nil {
				return err
			}
			if err := revokeCredentialState(ctx, tx, s.qualified, a.UserID); err != nil {
				return err
			}
		}
		if _, err := insertOAuthAccount(ctx, tx, s.qualified, a); err != nil {
			return err
		}
		if err := bumpCredentialRevision(ctx, tx, s.qualified, a.UserID, now); err != nil {
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
