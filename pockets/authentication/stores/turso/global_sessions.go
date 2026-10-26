package turso

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// globalRevocationPage is the active-user predicate over the page bounds
// (after, last]: inside the batch's BEGIN IMMEDIATE no other writer runs, so
// the bounds select exactly the users the page read.
const globalRevocationPage = `SELECT id FROM users WHERE status = 'active' AND erased_at IS NULL AND id > ? AND id <= ?`

var _ session.GlobalRevoker = (*OAuth2Store)(nil)

// RevokeAllBatch is session.GlobalRevoker: one BEGIN IMMEDIATE transaction —
// this store's user-row lock, held over the whole database — that pages the
// next limit active users after afterUserID in byte order (SQLite's BINARY
// collation), bumps their auth_revision, then deletes their sessions, step-up
// grants and password_reset challenges, the order RevokeAllForUser takes.
func (s *OAuth2Store) RevokeAllBatch(ctx context.Context, afterUserID string, limit int, now time.Time) (session.GlobalRevocationBatch, error) {
	if limit <= 0 {
		return session.GlobalRevocationBatch{}, fmt.Errorf("authentication turso store: revocation batch limit must be positive: %w", sdk.ErrInvalidInput)
	}
	batch := session.GlobalRevocationBatch{LastUserID: afterUserID}
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var last string
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(id), '') FROM (
				SELECT id FROM users WHERE status = 'active' AND erased_at IS NULL AND id > ? ORDER BY id LIMIT ?)`,
			afterUserID, limit).Scan(&count, &last); err != nil {
			return tursodb.MapError(err)
		}
		batch.Done = count < limit
		if count == 0 {
			return nil
		}
		batch.Users, batch.LastUserID = count, last
		if _, err := tx.Exec(ctx, `UPDATE users SET auth_revision = auth_revision + 1, updated_at = ? WHERE id IN (`+globalRevocationPage+`)`,
			tursodb.FormatTime(now), afterUserID, last); err != nil {
			return err
		}
		res, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (`+globalRevocationPage+`)`, afterUserID, last)
		if err != nil {
			return err
		}
		if batch.Sessions, err = affected(res); err != nil {
			return err
		}
		if res, err = tx.Exec(ctx, `DELETE FROM authentication_grants WHERE user_id IN (`+globalRevocationPage+`)`, afterUserID, last); err != nil {
			return err
		}
		if batch.Grants, err = affected(res); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM challenges WHERE purpose = 'password_reset' AND user_id IN (`+globalRevocationPage+`)`, afterUserID, last)
		return err
	})
	if err != nil {
		return session.GlobalRevocationBatch{}, err
	}
	return batch, nil
}

// SweepOrphans is session.GlobalRevoker's final pass: one BEGIN IMMEDIATE
// transaction deleting every step-up grant and session whose user is absent or
// not active.
func (s *OAuth2Store) SweepOrphans(ctx context.Context, _ time.Time) (sessions, grants int, err error) {
	const inactive = ` AS x WHERE NOT EXISTS (SELECT 1 FROM users u
		WHERE u.id = x.user_id AND u.status = 'active' AND u.erased_at IS NULL)`
	err = s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		res, err := tx.Exec(ctx, `DELETE FROM authentication_grants`+inactive)
		if err != nil {
			return err
		}
		if grants, err = affected(res); err != nil {
			return err
		}
		if res, err = tx.Exec(ctx, `DELETE FROM sessions`+inactive); err != nil {
			return err
		}
		sessions, err = affected(res)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return sessions, grants, nil
}

// affected reads a statement's row count.
func affected(res sql.Result) (int, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, tursodb.MapError(err)
	}
	return int(n), nil
}
