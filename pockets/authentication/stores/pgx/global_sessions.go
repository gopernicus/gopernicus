package pgx

import (
	"context"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// globalRevocationTimeouts bound every batch transaction on its own, so a
// pool-wide statement timeout never decides whether the incident button works
// and a batch queued behind a long lock gives up instead of stalling logins.
const globalRevocationTimeouts = `SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '60s'`

var _ session.GlobalRevoker = (*OAuth2Store)(nil)

// RevokeAllBatch is session.GlobalRevoker: one transaction that pages the next
// limit active users after afterUserID in byte order (COLLATE "C"), locks them
// FOR UPDATE in that order, bumps their auth_revision, then deletes their
// sessions, step-up grants and password_reset challenges — the per-user order
// RevokeAllForUser takes. The page is read without a lock so Done reflects the
// page size; a user deactivated between the read and the lock is skipped (the
// sweep reaches its rows).
func (s *OAuth2Store) RevokeAllBatch(ctx context.Context, afterUserID string, limit int, now time.Time) (session.GlobalRevocationBatch, error) {
	if limit <= 0 {
		return session.GlobalRevocationBatch{}, fmt.Errorf("authentication pgx store: revocation batch limit must be positive: %w", sdk.ErrInvalidInput)
	}
	batch := session.GlobalRevocationBatch{LastUserID: afterUserID}
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, globalRevocationTimeouts); err != nil {
			return err
		}
		page, err := collectIDs(ctx, tx, `SELECT id FROM `+s.table(usersTable)+`
			WHERE status = 'active' AND erased_at IS NULL AND id COLLATE "C" > @after
			ORDER BY id COLLATE "C" LIMIT @limit`, pgx.NamedArgs{"after": afterUserID, "limit": limit})
		if err != nil {
			return err
		}
		batch.Done = len(page) < limit
		if len(page) == 0 {
			return nil
		}
		batch.LastUserID = page[len(page)-1]
		bumped, err := collectIDs(ctx, tx, `WITH locked AS (
				SELECT id FROM `+s.table(usersTable)+`
				WHERE id = ANY(@ids) AND status = 'active' AND erased_at IS NULL
				ORDER BY id COLLATE "C" FOR UPDATE)
			UPDATE `+s.table(usersTable)+` u SET auth_revision = u.auth_revision + 1, updated_at = @now
			FROM locked WHERE u.id = locked.id RETURNING u.id`, pgx.NamedArgs{"ids": page, "now": now.UTC()})
		if err != nil {
			return err
		}
		batch.Users = len(bumped)
		args := pgx.NamedArgs{"ids": bumped}
		tag, err := tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+` WHERE user_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		batch.Sessions = int(tag.RowsAffected())
		if tag, err = tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE user_id = ANY(@ids)`, args); err != nil {
			return err
		}
		batch.Grants = int(tag.RowsAffected())
		_, err = tx.Exec(ctx, `DELETE FROM `+s.table(challengesTable)+` WHERE user_id = ANY(@ids) AND purpose = 'password_reset'`, args)
		return err
	})
	if err != nil {
		return session.GlobalRevocationBatch{}, err
	}
	return batch, nil
}

// SweepOrphans is session.GlobalRevoker's final pass: one transaction, under
// the batch timeouts, deleting every step-up grant and session whose user is
// absent or not active.
func (s *OAuth2Store) SweepOrphans(ctx context.Context, _ time.Time) (sessions, grants int, err error) {
	inactive := ` x WHERE NOT EXISTS (SELECT 1 FROM ` + s.table(usersTable) + ` u
		WHERE u.id = x.user_id AND u.status = 'active' AND u.erased_at IS NULL)`
	err = s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, globalRevocationTimeouts); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+inactive)
		if err != nil {
			return err
		}
		grants = int(tag.RowsAffected())
		if tag, err = tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+inactive); err != nil {
			return err
		}
		sessions = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return sessions, grants, nil
}

// collectIDs runs a query returning one text column and collects it.
func collectIDs(ctx context.Context, tx *pgxdb.Tx, q string, args pgx.NamedArgs) ([]string, error) {
	rows, err := tx.Query(ctx, q, args)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	return ids, nil
}
