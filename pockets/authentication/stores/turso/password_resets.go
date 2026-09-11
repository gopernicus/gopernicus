package turso

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// PasswordResetStore implements passwordreset.Repository over a libSQL database
// (design §5.9). Redeem performs the whole reset composition in ONE write
// transaction: a guarded DELETE ... RETURNING consumes the live (purpose, digest)
// challenge, the typed user_passwords row is upserted, and every session,
// recent-authentication grant, and outstanding password/reset challenge for the
// resolved user is deleted. libSQL/SQLite serializes writes, so two simultaneous
// resets of one token resolve to exactly one committing DELETE and the loser sees
// no row; any statement failure aborts the transaction, so there is never a
// changed-password/live-old-session partial state.
type PasswordResetStore struct {
	db *tursodb.DB
}

var _ passwordreset.Repository = (*PasswordResetStore)(nil)

// NewPasswordResetStore returns a PasswordResetStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewPasswordResetStore(db *tursodb.DB) *PasswordResetStore {
	if db == nil {
		panic("authentication turso: NewPasswordResetStore received a nil database")
	}
	return &PasswordResetStore{db: db}
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
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {

		if err := tx.QueryRow(ctx, `SELECT user_id FROM challenges WHERE purpose = ? AND secret_digest = ? AND expires_at > ?`, in.Purpose, in.TokenDigest, tursodb.FormatTime(in.Now)).Scan(&userID); err != nil {
			return tursodb.MapError(err)
		}
		revision, err := lockCredentialUser(ctx, tx, userID)
		if errors.Is(err, session.ErrUserNotActive) {
			return sdk.ErrNotFound
		}
		if err != nil {
			return err
		}
		var proofContext sql.NullString
		// 1. Consume the LIVE password_reset challenge, resolving the user from it.
		// The expires_at guard excludes expired rows, so unknown/expired/used all
		// return no row → sdk.ErrNotFound (the single generic failure).
		selErr := tx.QueryRow(ctx,
			`DELETE FROM challenges
				WHERE user_id = ? AND purpose = ? AND secret_digest = ? AND expires_at > ?
				RETURNING context`,
			userID, in.Purpose, in.TokenDigest, tursodb.FormatTime(in.Now)).
			Scan(&proofContext)
		if selErr != nil {
			if errors.Is(selErr, sql.ErrNoRows) {
				return sdk.ErrNotFound
			}
			return tursodb.MapError(selErr)
		}

		binding, err := passwordreset.ParseBinding(bytesFrom(proofContext))
		if err != nil {
			return err
		}
		row, err := tursodb.QueryOne[identifierRow](ctx, tx,
			`SELECT `+identifierColumns+` FROM user_identifiers WHERE id = ?`, binding.IdentifierID)
		if err != nil {
			return tursodb.MapError(err)
		}
		if !binding.Matches(userID, revision, row.toDomain()) {
			return sdk.ErrNotFound
		}

		if err := bumpCredentialRevision(ctx, tx, userID, in.Now); err != nil {
			return err
		}
		// 2. Set the typed password row.
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_passwords (user_id, hash) VALUES (?, ?)
				ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash`,
			userID, in.NewPasswordHash); err != nil {
			return err
		}
		// 3. Revoke every session.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return err
		}
		// 4a. Revoke every outstanding recent-authentication grant.
		if _, err := tx.Exec(ctx, `DELETE FROM authentication_grants WHERE user_id = ?`, userID); err != nil {
			return err
		}
		// 4b. Purge the user's outstanding password/reset challenges. libSQL has no
		// array parameter, so the purpose set is expanded into an IN placeholder list.
		if len(in.PurgeChallengePurposes) > 0 {
			args := make([]any, 0, len(in.PurgeChallengePurposes)+1)
			args = append(args, userID)
			placeholders := make([]string, len(in.PurgeChallengePurposes))
			for i, p := range in.PurgeChallengePurposes {
				placeholders[i] = "?"
				args = append(args, p)
			}
			q := `DELETE FROM challenges WHERE user_id = ? AND purpose IN (` + strings.Join(placeholders, ", ") + `)`
			if _, err := tx.Exec(ctx, q, args...); err != nil {
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
