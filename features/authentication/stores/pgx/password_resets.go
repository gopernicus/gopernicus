package pgx

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// PasswordResetStore implements passwordreset.Repository over a PostgreSQL
// database (design §5.9). Redeem performs the whole reset composition in ONE
// transaction: a guarded DELETE ... RETURNING consumes the live (purpose,
// digest) challenge, the typed user_passwords row is upserted, and every session,
// recent-authentication grant, and outstanding password/reset challenge for the
// resolved user is deleted. Any statement failure rolls the transaction back, so
// there is never a changed-password/live-old-session partial state, and the
// (purpose, secret_digest) unique index makes two simultaneous resets of one token
// resolve to exactly one committing DELETE.
type PasswordResetStore struct {
	db *pgxdb.DB
}

var _ passwordreset.Repository = (*PasswordResetStore)(nil)

// NewPasswordResetStore returns a PasswordResetStore backed by db.
func NewPasswordResetStore(db *pgxdb.DB) *PasswordResetStore {
	return &PasswordResetStore{db: db}
}

// Redeem atomically consumes the live reset challenge and applies the full reset
// composition, returning the reset user's ID. A non-live challenge (unknown,
// consumed, or expired) → sdk.ErrNotFound with no changes applied.
func (s *PasswordResetStore) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	if in.TokenDigest == "" {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	var userID string
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// 1. Consume the LIVE password_reset challenge, resolving the user from it.
		// The expires_at guard excludes expired rows, so unknown/expired/used all
		// return no row → sdk.ErrNotFound (the single generic failure).
		selErr := tx.QueryRow(ctx,
			`DELETE FROM challenges
				WHERE purpose = @purpose AND secret_digest = @digest AND expires_at > @now
				RETURNING user_id`,
			pgx.NamedArgs{"purpose": in.Purpose, "digest": in.TokenDigest, "now": in.Now.UTC()}).
			Scan(&userID)
		if selErr != nil {
			if selErr == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(selErr)
		}
		// 2. Set the typed password row.
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_passwords (user_id, hash) VALUES (@user_id, @hash)
				ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash`,
			pgx.NamedArgs{"user_id": userID, "hash": in.NewPasswordHash}); err != nil {
			return err
		}
		// 3. Revoke every session.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = @user_id`,
			pgx.NamedArgs{"user_id": userID}); err != nil {
			return err
		}
		// 4a. Revoke every outstanding recent-authentication grant.
		if _, err := tx.Exec(ctx, `DELETE FROM authentication_grants WHERE user_id = @user_id`,
			pgx.NamedArgs{"user_id": userID}); err != nil {
			return err
		}
		// 4b. Purge the user's outstanding password/reset challenges.
		if len(in.PurgeChallengePurposes) > 0 {
			if _, err := tx.Exec(ctx,
				`DELETE FROM challenges WHERE user_id = @user_id AND purpose = ANY(@purposes)`,
				pgx.NamedArgs{"user_id": userID, "purposes": in.PurgeChallengePurposes}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return passwordreset.RedeemResult{}, err
	}
	return passwordreset.RedeemResult{UserID: userID}, nil
}
