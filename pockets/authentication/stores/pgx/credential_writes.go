package pgx

import (
	"bytes"
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// All credential writers and session admission serialize through this user row.
func lockCredentialUser(ctx context.Context, tx *pgxdb.Tx, q qualified, userID string) (int64, error) {
	var revision int64
	var status string
	err := tx.QueryRow(ctx, `SELECT auth_revision, status FROM `+q.table(usersTable)+` WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": userID}).Scan(&revision, &status)
	if err != nil {
		return 0, pgxdb.MapError(err)
	}
	if !user.NormalizeStatus(user.Status(status)).Active() {
		return 0, session.ErrUserNotActive
	}
	return revision, nil
}

func revokeCredentialState(ctx context.Context, tx *pgxdb.Tx, q qualified, userID string) error {
	for _, table := range []string{q.table(sessionsTable), q.table(authGrantsTable)} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id = @id", pgx.NamedArgs{"id": userID}); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM `+q.table(challengesTable)+` WHERE user_id = @id AND purpose = 'password_reset'`, pgx.NamedArgs{"id": userID})
	return err
}

func bumpCredentialRevision(ctx context.Context, tx *pgxdb.Tx, q qualified, userID string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE `+q.table(usersTable)+` SET auth_revision = auth_revision + 1, updated_at = @now WHERE id = @id`, pgx.NamedArgs{"id": userID, "now": now.UTC()})
	return err
}

func changePassword(ctx context.Context, tx *pgxdb.Tx, q qualified, userID string, change user.PasswordChange, conditional bool) (int64, error) {
	revision, err := lockCredentialUser(ctx, tx, q, userID)
	if err != nil {
		return 0, err
	}
	if conditional {
		if revision != change.ExpectedAuthRevision {
			return 0, sdk.ErrConflict
		}
		var current string
		err := tx.QueryRow(ctx, `SELECT hash FROM `+q.table(passwordsTable)+` WHERE user_id = @id`, pgx.NamedArgs{"id": userID}).Scan(&current)
		if err != nil && err != pgx.ErrNoRows {
			return 0, pgxdb.MapError(err)
		}
		if current != change.ExpectedHash {
			return 0, sdk.ErrConflict
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO `+q.table(passwordsTable)+` (user_id, hash) VALUES (@id, @hash) ON CONFLICT(user_id) DO UPDATE SET hash = excluded.hash`, pgx.NamedArgs{"id": userID, "hash": change.NewHash})
	if err != nil {
		return 0, err
	}
	if err := bumpCredentialRevision(ctx, tx, q, userID, change.Now); err != nil {
		return 0, err
	}
	if err := revokeCredentialState(ctx, tx, q, userID); err != nil {
		return 0, err
	}
	return revision + 1, nil
}

// passkeyOwned is the guard's projection of one of the owner's passkeys.
type passkeyOwned struct {
	UserVerified bool   `db:"user_verified"`
	UserHandle   []byte `db:"user_handle"`
}

// createPasskey stores p under the owner's user lock after the guard, in the
// passkey.Repository order: unknown or inactive owner, moved revision, closed
// bootstrap, taken credential id, the cap, a differing handle. It returns the
// stored id.
func createPasskey(ctx context.Context, tx *pgxdb.Tx, q qualified, p passkey.Passkey, transports string, in passkey.CreateInput) (string, error) {
	revision, err := lockCredentialUser(ctx, tx, q, p.UserID)
	if err != nil {
		return "", err
	}
	if revision != in.ExpectedAuthRevision {
		return "", sdk.ErrConflict
	}
	table := q.table(passkeyCredentialsTable)
	existing, err := pgxdb.Collect[passkeyOwned](ctx, tx, `SELECT user_verified, user_handle FROM `+table+`
		WHERE user_id = @user_id ORDER BY created_at, id`, pgx.NamedArgs{"user_id": p.UserID})
	if err != nil {
		return "", err
	}
	if in.Bootstrap {
		for _, e := range existing {
			if (passkey.Passkey{UserVerified: e.UserVerified}).AAL2Capable() {
				return "", passkey.ErrBootstrapClosed
			}
		}
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE credential_id = @credential_id)`,
		pgx.NamedArgs{"credential_id": nonNilBytes(p.CredentialID)}).Scan(&taken); err != nil {
		return "", pgxdb.MapError(err)
	}
	if taken {
		return "", sdk.ErrAlreadyExists
	}
	if len(existing) >= in.MaxPerUser {
		return "", passkey.ErrPasskeyLimit
	}
	if len(existing) > 0 && !bytes.Equal(existing[0].UserHandle, p.UserHandle) {
		return "", sdk.ErrConflict
	}
	args := pgx.NamedArgs{
		"user_id":         p.UserID,
		"name":            p.Name,
		"credential_id":   nonNilBytes(p.CredentialID),
		"public_key":      nonNilBytes(p.PublicKey),
		"aaguid":          nonNilBytes(p.AAGUID),
		"user_handle":     nonNilBytes(p.UserHandle),
		"sign_count":      int64(p.SignCount),
		"transports":      transports,
		"user_verified":   p.UserVerified,
		"backup_eligible": p.BackupEligible,
		"backup_state":    p.BackupState,
		"created_at":      p.CreatedAt,
	}
	// Empty ID → the schema default generates the key, read back with RETURNING.
	if p.ID == "" {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO `+table+` (user_id, name, credential_id, public_key, aaguid, user_handle, sign_count, transports, user_verified, backup_eligible, backup_state, created_at)
			VALUES (@user_id, @name, @credential_id, @public_key, @aaguid, @user_handle, @sign_count, @transports, @user_verified, @backup_eligible, @backup_state, @created_at)
			RETURNING id`, args).Scan(&id)
		return id, pgxdb.MapError(err)
	}
	args["id"] = p.ID
	_, err = tx.Exec(ctx, `INSERT INTO `+table+` (id, user_id, name, credential_id, public_key, aaguid, user_handle, sign_count, transports, user_verified, backup_eligible, backup_state, created_at)
		VALUES (@id, @user_id, @name, @credential_id, @public_key, @aaguid, @user_handle, @sign_count, @transports, @user_verified, @backup_eligible, @backup_state, @created_at)`, args)
	return p.ID, err
}
