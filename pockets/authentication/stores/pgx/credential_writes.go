package pgx

import (
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
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
