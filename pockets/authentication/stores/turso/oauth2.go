package turso

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// OAuth2Store uses the same BEGIN IMMEDIATE user/session fence as credential
// changes. Approval, redemption, refresh and account-wide revocation serialize.
type OAuth2Store struct {
	db *tursodb.DB
}

var (
	_ oauth2.Repository            = (*OAuth2Store)(nil)
	_ session.ManagementRepository = (*OAuth2Store)(nil)
)

func NewOAuth2Store(db *tursodb.DB) *OAuth2Store {
	if db == nil {
		panic("authentication turso: NewOAuth2Store received a nil database")
	}
	return &OAuth2Store{db: db}
}

func (s *OAuth2Store) PutClient(ctx context.Context, client oauth2.Client) error {
	redirects, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, expires_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, redirect_uris = excluded.redirect_uris, expires_at = excluded.expires_at`,
		client.ID, client.Name, string(redirects), tursodb.FormatTime(client.ExpiresAt))
	return err
}

func (s *OAuth2Store) GetClient(ctx context.Context, id string) (oauth2.Client, error) {
	var out oauth2.Client
	var redirects string
	var expires tursodb.Time
	if err := s.db.QueryRow(ctx, `SELECT id, name, redirect_uris, expires_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&out.ID, &out.Name, &redirects, &expires); err != nil {
		return oauth2.Client{}, tursodb.MapError(err)
	}
	if err := json.Unmarshal([]byte(redirects), &out.RedirectURIs); err != nil {
		return oauth2.Client{}, err
	}
	out.ExpiresAt = expires.Time
	return out, nil
}

func (s *OAuth2Store) CreateCode(ctx context.Context, code oauth2.Code, approvingSessionID string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, code.UserID)
		if err != nil {
			return oauthGrantError(err)
		}
		if revision != code.AuthRevision || code.Delegation.AuthRevision != revision || code.Hash == "" ||
			!validOAuthDelegation(code.Delegation) || code.RedirectURI == "" || code.CodeChallenge == "" ||
			!time.Now().Before(code.ExpiresAt) {
			return oauth2.ErrInvalidGrant
		}
		approver, err := oauthSession(ctx, tx, approvingSessionID)
		if err != nil {
			return oauthGrantError(err)
		}
		if approver.UserID != code.UserID || !approver.FirstParty() || approver.Expired(time.Now()) {
			return oauth2.ErrInvalidGrant
		}
		delegation, err := json.Marshal(code.Delegation)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO oauth_authorization_codes
			(code_hash, user_id, auth_revision, delegation, redirect_uri, code_challenge, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, code.Hash, code.UserID, code.AuthRevision, string(delegation),
			code.RedirectURI, code.CodeChallenge, tursodb.FormatTime(code.CreatedAt), tursodb.FormatTime(code.ExpiresAt))
		return err
	})
}

func (s *OAuth2Store) GetCode(ctx context.Context, hash string) (oauth2.Code, error) {
	return getOAuthCode(ctx, s.db, hash)
}

func getOAuthCode(ctx context.Context, db tursodb.Querier, hash string) (oauth2.Code, error) {
	var out oauth2.Code
	var delegation string
	var created, expires tursodb.Time
	if err := db.QueryRow(ctx, `SELECT code_hash, user_id, auth_revision, delegation, redirect_uri, code_challenge, created_at, expires_at
		FROM oauth_authorization_codes WHERE code_hash = ?`, hash).
		Scan(&out.Hash, &out.UserID, &out.AuthRevision, &delegation, &out.RedirectURI, &out.CodeChallenge, &created, &expires); err != nil {
		return oauth2.Code{}, oauthGrantError(tursodb.MapError(err))
	}
	if err := json.Unmarshal([]byte(delegation), &out.Delegation); err != nil {
		return oauth2.Code{}, err
	}
	out.CreatedAt, out.ExpiresAt = created.Time, expires.Time
	return out, nil
}

func (s *OAuth2Store) RedeemCode(ctx context.Context, expected oauth2.Code, proposed session.Session, now time.Time) (session.Session, error) {
	var out session.Session
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, expected.UserID)
		if err != nil {
			return oauthGrantError(err)
		}
		stored, err := getOAuthCode(ctx, tx, expected.Hash)
		if err != nil {
			return err
		}
		if !sameOAuthCode(stored, expected) || !now.Before(stored.ExpiresAt) || revision != stored.AuthRevision ||
			proposed.Profile != session.ProfileDelegated || proposed.UserID != stored.UserID ||
			proposed.Delegation != stored.Delegation || proposed.Delegation.AuthRevision != revision ||
			proposed.ID == "" || proposed.RefreshTokenHash == "" || !validOAuthDelegation(proposed.Delegation) ||
			proposed.PreviousRefreshTokenHash != "" || proposed.PreviousUsed || proposed.RotationCount != 0 || proposed.Expired(now) {
			return oauth2.ErrInvalidGrant
		}
		if err := unusedOAuthRefresh(ctx, tx, proposed.RefreshTokenHash); err != nil {
			return err
		}
		out, err = insertSession(ctx, tx, proposed)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM oauth_authorization_codes WHERE code_hash = ?`, stored.Hash)
		return err
	})
	if err != nil {
		return session.Session{}, err
	}
	return out, nil
}

func (s *OAuth2Store) RotateRefresh(ctx context.Context, refresh oauth2.Refresh) (session.Session, error) {
	if refresh.Hash == "" {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	var out session.Session
	var reused bool
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		sess, err := findOAuthRefresh(ctx, tx, refresh.Hash)
		if err != nil {
			return oauthGrantError(err)
		}
		if _, err := lockCredentialUser(ctx, tx, sess.UserID); err != nil {
			return oauthGrantError(err)
		}
		// The user's revision fences pending approvals only. Existing grants
		// remain valid after unrelated revisions unless explicitly revoked.
		if sess.Profile != session.ProfileDelegated || !validOAuthDelegation(sess.Delegation) || sess.Expired(refresh.Now) ||
			sess.Delegation.ClientID != refresh.ClientID || sess.Delegation.Resource != refresh.Resource {
			return oauth2.ErrInvalidGrant
		}
		if sess.RefreshTokenHash != refresh.Hash {
			if err := deleteOAuthGrant(ctx, tx, sess.ID, sess.UserID, refresh.ClientID); err != nil {
				return err
			}
			reused = true
			return nil // Commit revocation before returning ErrRefreshReuse.
		}
		if refresh.NewHash == "" || refresh.Hash == refresh.NewHash {
			return oauth2.ErrInvalidGrant
		}
		if err := unusedOAuthRefresh(ctx, tx, refresh.NewHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO oauth_refresh_history (refresh_token_hash, session_id, expires_at) VALUES (?, ?, ?)`,
			refresh.Hash, sess.ID, tursodb.FormatTime(sess.ExpiresAt)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET refresh_token_hash = ?, previous_refresh_token_hash = NULL,
			previous_used = 0, rotation_count = rotation_count + 1 WHERE id = ? AND user_id = ? AND session_profile = 'delegated'`,
			refresh.NewHash, sess.ID, sess.UserID); err != nil {
			return err
		}
		sess.RefreshTokenHash = refresh.NewHash
		sess.PreviousRefreshTokenHash = ""
		sess.PreviousUsed = false
		sess.RotationCount++
		out = sess
		return nil
	})
	if err != nil {
		return session.Session{}, err
	}
	if reused {
		return session.Session{}, oauth2.ErrRefreshReuse
	}
	return out, nil
}

func (s *OAuth2Store) RevokeRefresh(ctx context.Context, hash, clientID string) error {
	if hash == "" || clientID == "" {
		return nil
	}
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		sess, err := findOAuthRefresh(ctx, tx, hash)
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if sess.Profile != session.ProfileDelegated || sess.Delegation.ClientID != clientID {
			return nil
		}
		return deleteOAuthGrant(ctx, tx, sess.ID, sess.UserID, clientID)
	})
}

func (s *OAuth2Store) RevokeGrant(ctx context.Context, id, userID, clientID string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		sess, err := oauthSession(ctx, tx, id)
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if sess.Profile != session.ProfileDelegated || sess.UserID != userID || sess.Delegation.ClientID != clientID {
			return nil
		}
		return deleteOAuthGrant(ctx, tx, sess.ID, sess.UserID, clientID)
	})
}

func (s *OAuth2Store) Prune(ctx context.Context, now time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		for _, table := range []string{"oauth_authorization_codes", "oauth_clients", "oauth_refresh_history"} {
			if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE expires_at <= ?", tursodb.FormatTime(now)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *OAuth2Store) ListByUser(ctx context.Context, userID string, req list.Request) (list.Page[session.Session], error) {
	q := tursodb.ListQuery[session.Session]{
		BaseSQL:      `SELECT ` + sessionColumns + ` FROM sessions WHERE user_id = ? AND expires_at > ?`,
		Args:         []any{userID, tursodb.FormatTime(time.Now())},
		OrderFields:  map[string]list.OrderField{"created_at": {Column: "created_at"}},
		DefaultOrder: list.Order{Field: "created_at", Direction: list.DESC},
		PK:           "id",
		Scan: func(scanner tursodb.Scanner) (session.Session, error) {
			var r sessionRow
			if err := scanner.Scan(&r.ID, &r.UserID, &r.RefreshTokenHash, &r.PreviousRefreshTokenHash, &r.PreviousUsed,
				&r.RotationCount, &r.AuthenticatedAt, &r.AuthenticationMethods, &r.AssuranceLevel, &r.CreatedAt,
				&r.ExpiresAt, &r.Profile, &r.Delegation); err != nil {
				return session.Session{}, err
			}
			return r.toDomain()
		},
		OrderValueOf: func(s session.Session, _ string) any { return s.CreatedAt },
		PKOf:         func(s session.Session) string { return s.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

func (s *OAuth2Store) DeleteForUser(ctx context.Context, id, userID string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		return deleteOAuthSession(ctx, tx, id, userID)
	})
}

func (s *OAuth2Store) RevokeAllForUser(ctx context.Context, userID string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = ?`, userID).Scan(&revision); err != nil {
			return tursodb.MapError(err)
		}
		if err := bumpCredentialRevision(ctx, tx, userID, now); err != nil {
			return err
		}
		return revokeCredentialState(ctx, tx, userID)
	})
}

func oauthSession(ctx context.Context, db tursodb.Querier, id string) (session.Session, error) {
	row, err := tursodb.QueryOne[sessionRow](ctx, db, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	if err != nil {
		return session.Session{}, err
	}
	return row.toDomain()
}

func findOAuthRefresh(ctx context.Context, tx *tursodb.Tx, hash string) (session.Session, error) {
	row, err := tursodb.QueryOne[sessionRow](ctx, tx, `SELECT `+sessionColumns+` FROM sessions
		WHERE refresh_token_hash = ? OR id = (SELECT session_id FROM oauth_refresh_history WHERE refresh_token_hash = ?)`, hash, hash)
	if err != nil {
		return session.Session{}, err
	}
	return row.toDomain()
}

func unusedOAuthRefresh(ctx context.Context, tx *tursodb.Tx, hash string) error {
	var exists tursodb.Bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE refresh_token_hash = ?
		UNION ALL SELECT 1 FROM oauth_refresh_history WHERE refresh_token_hash = ?)`, hash, hash).Scan(&exists)
	if err != nil {
		return tursodb.MapError(err)
	}
	if exists {
		return sdk.ErrAlreadyExists
	}
	return nil
}

func deleteOAuthSession(ctx context.Context, tx *tursodb.Tx, id, userID string) error {
	n, err := tursodb.ExecAffecting(ctx, tx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	_, err = tx.Exec(ctx, `DELETE FROM authentication_grants WHERE session_id = ?`, id)
	return err
}

func deleteOAuthGrant(ctx context.Context, tx *tursodb.Tx, id, userID, clientID string) error {
	n, err := tursodb.ExecAffecting(ctx, tx, `DELETE FROM sessions WHERE id = ? AND user_id = ?
		AND session_profile = 'delegated' AND json_extract(delegation, '$.ClientID') = ?`, id, userID, clientID)
	if err != nil || n == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM authentication_grants WHERE session_id = ?`, id)
	return err
}

func oauthGrantError(err error) error {
	if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, session.ErrUserNotActive) {
		return oauth2.ErrInvalidGrant
	}
	return err
}

func validOAuthDelegation(d session.Delegation) bool {
	return d.AuthRevision >= 0 && d.Issuer != "" && d.ClientID != "" && d.Resource != "" &&
		d.ExchangeResource != "" && d.ExchangeClientID != "" && d.Resource != d.ExchangeResource
}

func sameOAuthCode(a, b oauth2.Code) bool {
	return a.Hash == b.Hash && a.UserID == b.UserID && a.AuthRevision == b.AuthRevision && a.Delegation == b.Delegation &&
		a.RedirectURI == b.RedirectURI && a.CodeChallenge == b.CodeChallenge && a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}
