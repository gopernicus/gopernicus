package pgx

import (
	"context"
	"errors"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
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
	qualified
}

var _ passwordreset.Repository = (*PasswordResetStore)(nil)

// NewPasswordResetStore returns a PasswordResetStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewPasswordResetStore(db *pgxdb.DB, opts ...Option) *PasswordResetStore {
	if db == nil {
		panic("authentication pgx: NewPasswordResetStore received a nil database")
	}
	return &PasswordResetStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// Redeem atomically consumes the live reset challenge and applies the full reset
// composition, including the current recovery/revision binding check and one
// revision increment. Unknown, expired, consumed or stale proof returns
// sdk.ErrNotFound with no changes applied.
func (s *PasswordResetStore) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	if in.TokenDigest == "" {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	var userID string
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {

		if err := tx.QueryRow(ctx, `SELECT user_id FROM `+s.table(challengesTable)+` WHERE purpose = @purpose AND secret_digest = @digest AND expires_at > @now`, pgx.NamedArgs{"purpose": in.Purpose, "digest": in.TokenDigest, "now": in.Now.UTC()}).Scan(&userID); err != nil {
			return pgxdb.MapError(err)
		}
		revision, err := lockCredentialUser(ctx, tx, s.qualified, userID)
		if errors.Is(err, session.ErrUserNotActive) {
			return sdk.ErrNotFound
		}
		if err != nil {
			return err
		}
		var proofContext *string
		// 1. Consume the LIVE password_reset challenge, resolving the user from it.
		// The expires_at guard excludes expired rows, so unknown/expired/used all
		// return no row → sdk.ErrNotFound (the single generic failure).
		selErr := tx.QueryRow(ctx,
			`DELETE FROM `+s.table(challengesTable)+`
				WHERE user_id = @user_id AND purpose = @purpose AND secret_digest = @digest AND expires_at > @now
				RETURNING context`,
			pgx.NamedArgs{"user_id": userID, "purpose": in.Purpose, "digest": in.TokenDigest, "now": in.Now.UTC()}).
			Scan(&proofContext)
		if selErr != nil {
			if selErr == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(selErr)
		}

		binding, err := passwordreset.ParseBinding(bytesFrom(proofContext))
		if err != nil {
			return err
		}
		row, err := pgxdb.QueryOne[identifierRow](ctx, tx,
			`SELECT `+identifierColumns+` FROM `+s.table(identifiersTable)+` WHERE id = @id`,
			pgx.NamedArgs{"id": binding.IdentifierID})
		if err != nil {
			return pgxdb.MapError(err)
		}
		if !binding.Matches(userID, revision, row.toDomain()) {
			return sdk.ErrNotFound
		}

		if err := bumpCredentialRevision(ctx, tx, s.qualified, userID, in.Now); err != nil {
			return err
		}
		// 2. Set the typed password row.
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+s.table(passwordsTable)+` (user_id, hash) VALUES (@user_id, @hash)
				ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash`,
			pgx.NamedArgs{"user_id": userID, "hash": in.NewPasswordHash}); err != nil {
			return err
		}
		// 3. Revoke every session.
		if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+` WHERE user_id = @user_id`,
			pgx.NamedArgs{"user_id": userID}); err != nil {
			return err
		}
		// 4a. Revoke every outstanding recent-authentication grant.
		if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE user_id = @user_id`,
			pgx.NamedArgs{"user_id": userID}); err != nil {
			return err
		}
		// 4b. Purge the user's outstanding password/reset challenges.
		if len(in.PurgeChallengePurposes) > 0 {
			if _, err := tx.Exec(ctx,
				`DELETE FROM `+s.table(challengesTable)+` WHERE user_id = @user_id AND purpose = ANY(@purposes)`,
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
