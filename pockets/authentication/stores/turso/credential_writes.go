package turso

import (
	"context"
	"database/sql"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// All credential writers and session admission serialize through this user row.
func lockCredentialUser(ctx context.Context, tx *tursodb.Tx, userID string) (int64, error) {
	var revision int64
	var status string
	err := tx.QueryRow(ctx, `SELECT auth_revision, status FROM users WHERE id = ?`, userID).Scan(&revision, &status)
	if err != nil {
		return 0, tursodb.MapError(err)
	}
	if !user.NormalizeStatus(user.Status(status)).Active() {
		return 0, session.ErrUserNotActive
	}
	return revision, nil
}

func revokeCredentialState(ctx context.Context, tx *tursodb.Tx, userID string) error {
	for _, table := range []string{"sessions", "authentication_grants"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id = ?", userID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM challenges WHERE user_id = ? AND purpose = 'password_reset'`, userID)
	return err
}

func bumpCredentialRevision(ctx context.Context, tx *tursodb.Tx, userID string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE users SET auth_revision = auth_revision + 1, updated_at = ? WHERE id = ?`, tursodb.FormatTime(now), userID)
	return err
}

func changePassword(ctx context.Context, tx *tursodb.Tx, userID string, change user.PasswordChange, conditional bool) (int64, error) {
	revision, err := lockCredentialUser(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if conditional {
		if revision != change.ExpectedAuthRevision {
			return 0, sdk.ErrConflict
		}
		var current string
		err := tx.QueryRow(ctx, `SELECT hash FROM user_passwords WHERE user_id = ?`, userID).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return 0, tursodb.MapError(err)
		}
		if current != change.ExpectedHash {
			return 0, sdk.ErrConflict
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_passwords (user_id, hash) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET hash = excluded.hash`, userID, change.NewHash)
	if err != nil {
		return 0, err
	}
	if err := bumpCredentialRevision(ctx, tx, userID, change.Now); err != nil {
		return 0, err
	}
	if err := revokeCredentialState(ctx, tx, userID); err != nil {
		return 0, err
	}
	return revision + 1, nil
}
