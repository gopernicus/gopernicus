package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
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
		return session.Session{}, fmt.Errorf("authentication turso store: empty refresh successor: %w", sdk.ErrInvalidInput)
	}
	return s.rotateRefresh(ctx, refresh, &successor)
}

// recordSuccessor writes the spent row with its 0031 columns, after clearing
// the session's older ciphertexts: only the newest rotation can be answered,
// so no older successor needs to stay at rest.
func recordSuccessor(ctx context.Context, tx *tursodb.Tx, refresh oauth2.Refresh, sessionID string, expires time.Time, successor string) error {
	if _, err := tx.Exec(ctx, `UPDATE oauth_refresh_history SET successor_ciphertext = NULL WHERE session_id = ? AND successor_ciphertext IS NOT NULL`, sessionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO oauth_refresh_history (refresh_token_hash, session_id, expires_at, rotated_at, successor_ciphertext) VALUES (?, ?, ?, ?, ?)`,
		refresh.Hash, sessionID, tursodb.FormatTime(expires), tursodb.FormatTime(refresh.Now), successor)
	return err
}

// GetRefreshSuccessor reads the spent row for hash with no write. sdk.ErrNotFound
// when there is none or it recorded no rotation or ciphertext.
func (s *OAuth2Store) GetRefreshSuccessor(ctx context.Context, hash string) (oauth2.RefreshSuccessor, error) {
	var (
		sessionID string
		rotated   tursodb.NullTime
		cipher    sql.NullString
	)
	err := s.db.QueryRow(ctx, `SELECT session_id, rotated_at, successor_ciphertext FROM oauth_refresh_history WHERE refresh_token_hash = ?`, hash).Scan(&sessionID, &rotated, &cipher)
	if errors.Is(err, sql.ErrNoRows) {
		return oauth2.RefreshSuccessor{}, sdk.ErrNotFound
	}
	if err != nil {
		return oauth2.RefreshSuccessor{}, tursodb.MapError(err)
	}
	at := rotated.TimePtr()
	if at == nil || !cipher.Valid || cipher.String == "" {
		return oauth2.RefreshSuccessor{}, sdk.ErrNotFound
	}
	return oauth2.RefreshSuccessor{SessionID: sessionID, RotatedAt: at.UTC(), Ciphertext: cipher.String}, nil
}

// ProbeRefreshGraceSchema refuses a database without migration 0031's columns
// with sdk.ErrNotFound naming the file. An infrastructure failure is returned
// via MapError, never as a missing column.
func (s *OAuth2Store) ProbeRefreshGraceSchema(ctx context.Context) error {
	for _, column := range refreshSuccessorColumnNames {
		if err := probeColumn(ctx, s.db, "oauth_refresh_history", column, refreshSuccessorMigration); err != nil {
			return err
		}
	}
	return nil
}

// hasSuccessorColumns reports whether migration 0031 is applied. Prune runs on
// every host, wired or not, so it sweeps successors only then.
func (s *OAuth2Store) hasSuccessorColumns(ctx context.Context) (bool, error) {
	err := s.ProbeRefreshGraceSchema(ctx)
	if errors.Is(err, sdk.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// pruneSuccessors clears every successor ciphertext older than the maximum
// window.
func pruneSuccessors(ctx context.Context, tx *tursodb.Tx, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE oauth_refresh_history SET successor_ciphertext = NULL WHERE successor_ciphertext IS NOT NULL AND rotated_at <= ?`,
		tursodb.FormatTime(now.Add(-oauth2.MaxRefreshReuseGrace)))
	return err
}
