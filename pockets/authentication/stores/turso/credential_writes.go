package turso

import (
	"bytes"
	"context"
	"database/sql"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
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

// createPasskey stores p under the owner's user lock after the guard, in the
// passkey.Repository order: unknown or inactive owner, moved revision, closed
// bootstrap, taken credential id, the cap, a differing handle. It returns the
// stored id.
func createPasskey(ctx context.Context, tx *tursodb.Tx, p passkey.Passkey, transports string, in passkey.CreateInput) (string, error) {
	revision, err := lockCredentialUser(ctx, tx, p.UserID)
	if err != nil {
		return "", err
	}
	if revision != in.ExpectedAuthRevision {
		return "", sdk.ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT user_verified, user_handle FROM passkey_credentials WHERE user_id = ? ORDER BY created_at, id`, p.UserID)
	if err != nil {
		return "", err
	}
	var (
		owned       int
		aal2Capable bool
		handle      []byte
	)
	for rows.Next() {
		var (
			verified tursodb.Bool
			h        []byte
		)
		if err := rows.Scan(&verified, &h); err != nil {
			rows.Close()
			return "", tursodb.MapError(err)
		}
		if owned == 0 {
			handle = h
		}
		owned++
		aal2Capable = aal2Capable || (passkey.Passkey{UserVerified: bool(verified)}).AAL2Capable()
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", tursodb.MapError(err)
	}
	rows.Close()
	if in.Bootstrap && aal2Capable {
		return "", passkey.ErrBootstrapClosed
	}
	var taken tursodb.Bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM passkey_credentials WHERE credential_id = ?)`, nonNilBytes(p.CredentialID)).Scan(&taken); err != nil {
		return "", tursodb.MapError(err)
	}
	if taken {
		return "", sdk.ErrAlreadyExists
	}
	if owned >= in.MaxPerUser {
		return "", passkey.ErrPasskeyLimit
	}
	if owned > 0 && !bytes.Equal(handle, p.UserHandle) {
		return "", sdk.ErrConflict
	}
	args := []any{
		p.UserID, p.Name, nonNilBytes(p.CredentialID), nonNilBytes(p.PublicKey), nonNilBytes(p.AAGUID), nonNilBytes(p.UserHandle),
		int64(p.SignCount), transports, tursodb.BoolToInt(p.UserVerified), tursodb.BoolToInt(p.BackupEligible), tursodb.BoolToInt(p.BackupState),
		tursodb.FormatTime(p.CreatedAt),
	}
	// Empty ID → the schema default generates the key, read back with RETURNING.
	if p.ID == "" {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO passkey_credentials (user_id, name, credential_id, public_key, aaguid, user_handle, sign_count, transports, user_verified, backup_eligible, backup_state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, args...).Scan(&id)
		return id, tursodb.MapError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO passkey_credentials (id, user_id, name, credential_id, public_key, aaguid, user_handle, sign_count, transports, user_verified, backup_eligible, backup_state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{p.ID}, args...)...)
	return p.ID, err
}
