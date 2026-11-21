package pgx

import (
	"context"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// refreshSuccessorMigration is the file that adds the oauth_refresh_history
// successor columns; ProbeRefreshGraceSchema names it.
const refreshSuccessorMigration = "0031_oauth_refresh_successor.sql"

var _ oauth2.RefreshGraceRepository = (*OAuth2Store)(nil)

// refreshSuccessorColumnNames are the 0031 columns ProbeRefreshGraceSchema
// requires.
var refreshSuccessorColumnNames = []string{"rotated_at", "successor_ciphertext"}

// RotateRefreshWithSuccessor is RotateRefresh plus the 0031 record: the spent
// row keeps rotated_at = refresh.Now and the successor ciphertext as given. A
// retained spent hash commits nothing and returns oauth2.ErrRefreshSpent.
func (s *OAuth2Store) RotateRefreshWithSuccessor(ctx context.Context, refresh oauth2.Refresh, successor string) (session.Session, error) {
	if successor == "" {
		return session.Session{}, fmt.Errorf("authentication pgx store: empty refresh successor: %w", sdk.ErrInvalidInput)
	}
	return s.rotateRefresh(ctx, refresh, &successor)
}

// recordSuccessor writes the spent row with its 0031 columns, after clearing
// the session's older ciphertexts: only the newest rotation can be answered,
// so no older successor needs to stay at rest.
func (s *OAuth2Store) recordSuccessor(ctx context.Context, tx *pgxdb.Tx, refresh oauth2.Refresh, sessionID string, expires time.Time, successor string) error {
	if _, err := tx.Exec(ctx, `UPDATE `+s.table(oauthHistoryTable)+` SET successor_ciphertext = NULL WHERE session_id = @id AND successor_ciphertext IS NOT NULL`, pgx.NamedArgs{"id": sessionID}); err != nil {
		return pgxdb.MapError(err)
	}
	_, err := tx.Exec(ctx, `INSERT INTO `+s.table(oauthHistoryTable)+` (refresh_token_hash,session_id,expires_at,rotated_at,successor_ciphertext) VALUES (@hash,@id,@expires,@rotated,@successor)`,
		pgx.NamedArgs{"hash": refresh.Hash, "id": sessionID, "expires": expires, "rotated": refresh.Now.UTC(), "successor": successor})
	return pgxdb.MapError(err)
}

// GetRefreshSuccessor reads the spent row for hash with no lock and no write.
// sdk.ErrNotFound when there is none or it recorded no rotation or ciphertext.
func (s *OAuth2Store) GetRefreshSuccessor(ctx context.Context, hash string) (oauth2.RefreshSuccessor, error) {
	var (
		sessionID string
		rotated   *time.Time
		cipher    *string
	)
	err := s.db.QueryRow(ctx, `SELECT session_id, rotated_at, successor_ciphertext FROM `+s.table(oauthHistoryTable)+` WHERE refresh_token_hash = @hash`, pgx.NamedArgs{"hash": hash}).Scan(&sessionID, &rotated, &cipher)
	if err != nil {
		return oauth2.RefreshSuccessor{}, pgxdb.MapError(err)
	}
	if rotated == nil || cipher == nil || *cipher == "" {
		return oauth2.RefreshSuccessor{}, sdk.ErrNotFound
	}
	return oauth2.RefreshSuccessor{SessionID: sessionID, RotatedAt: rotated.UTC(), Ciphertext: *cipher}, nil
}

// ProbeRefreshGraceSchema refuses a database without migration 0031's columns
// with sdk.ErrNotFound naming the file. An infrastructure failure is returned
// via MapError, never as a missing column.
func (s *OAuth2Store) ProbeRefreshGraceSchema(ctx context.Context) error {
	for _, column := range refreshSuccessorColumnNames {
		var exists bool
		if err := s.db.QueryRow(ctx, probeSessionActivitySQL, s.table(oauthHistoryTable), column).Scan(&exists); err != nil {
			return pgxdb.MapError(err)
		}
		if !exists {
			return fmt.Errorf("authentication pgx store: %s.%s column missing — apply migration %s from the %q migration source before boot: %w",
				s.table(oauthHistoryTable), column, refreshSuccessorMigration, "authentication", sdk.ErrNotFound)
		}
	}
	return nil
}

// pruneSuccessors clears every successor ciphertext older than the maximum
// window, on a database that has migration 0031. Prune runs it on every host,
// wired or not, so it probes first and does nothing without the columns.
func (s *OAuth2Store) pruneSuccessors(ctx context.Context, tx *pgxdb.Tx, now time.Time) error {
	var exists bool
	if err := tx.QueryRow(ctx, probeSessionActivitySQL, s.table(oauthHistoryTable), "successor_ciphertext").Scan(&exists); err != nil {
		return pgxdb.MapError(err)
	}
	if !exists {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE `+s.table(oauthHistoryTable)+` SET successor_ciphertext = NULL WHERE successor_ciphertext IS NOT NULL AND rotated_at <= @before`,
		pgx.NamedArgs{"before": now.UTC().Add(-oauth2.MaxRefreshReuseGrace)})
	return pgxdb.MapError(err)
}
