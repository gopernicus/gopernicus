package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

// OAuth2Store owns atomic consent redemption, delegated refresh families and
// session inventory. Mutations acquire the owning user before the session, the
// same order used by credential changes and account-wide revocation.
type OAuth2Store struct {
	db *pgxdb.DB
	qualified
}

var (
	_ oauth2.Repository            = (*OAuth2Store)(nil)
	_ session.ManagementRepository = (*OAuth2Store)(nil)
)

// NewOAuth2Store borrows db and panics if it is nil.
func NewOAuth2Store(db *pgxdb.DB, opts ...Option) *OAuth2Store {
	if db == nil {
		panic("authentication pgx: NewOAuth2Store received a nil database")
	}
	return &OAuth2Store{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

func (s *OAuth2Store) PutClient(ctx context.Context, client oauth2.Client) error {
	redirects, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO `+s.table(oauthClientsTable)+` (id, name, redirect_uris, expires_at)
 VALUES (@id, @name, @redirects, @expires) ON CONFLICT (id) DO UPDATE
 SET name = excluded.name, redirect_uris = excluded.redirect_uris, expires_at = excluded.expires_at`,
		pgx.NamedArgs{"id": client.ID, "name": client.Name, "redirects": string(redirects), "expires": client.ExpiresAt.UTC()})
	return pgxdb.MapError(err)
}

func (s *OAuth2Store) GetClient(ctx context.Context, id string) (oauth2.Client, error) {
	var client oauth2.Client
	var redirects string
	err := s.db.QueryRow(ctx, `SELECT id, name, redirect_uris, expires_at FROM `+s.table(oauthClientsTable)+` WHERE id = @id`, pgx.NamedArgs{"id": id}).Scan(&client.ID, &client.Name, &redirects, &client.ExpiresAt)
	if err != nil {
		return oauth2.Client{}, pgxdb.MapError(err)
	}
	if err := json.Unmarshal([]byte(redirects), &client.RedirectURIs); err != nil {
		return oauth2.Client{}, err
	}
	client.ExpiresAt = client.ExpiresAt.UTC()
	return client, nil
}

const oauthCodeColumns = "code_hash, user_id, auth_revision, delegation, redirect_uri, code_challenge, created_at, expires_at"

type oauthCodeRow struct {
	Hash          string    `db:"code_hash"`
	UserID        string    `db:"user_id"`
	AuthRevision  int64     `db:"auth_revision"`
	Delegation    string    `db:"delegation"`
	RedirectURI   string    `db:"redirect_uri"`
	CodeChallenge string    `db:"code_challenge"`
	CreatedAt     time.Time `db:"created_at"`
	ExpiresAt     time.Time `db:"expires_at"`
}

func (r oauthCodeRow) toDomain() (oauth2.Code, error) {
	code := oauth2.Code{Hash: r.Hash, UserID: r.UserID, AuthRevision: r.AuthRevision, RedirectURI: r.RedirectURI, CodeChallenge: r.CodeChallenge, CreatedAt: r.CreatedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC()}
	if err := json.Unmarshal([]byte(r.Delegation), &code.Delegation); err != nil {
		return oauth2.Code{}, err
	}
	return code, nil
}

func (s *OAuth2Store) CreateCode(ctx context.Context, code oauth2.Code, approvingSessionID string) error {
	now := time.Now().UTC()
	if code.Hash == "" || code.UserID == "" || approvingSessionID == "" || !validOAuthBinding(code.Delegation) || code.AuthRevision != code.Delegation.AuthRevision || code.RedirectURI == "" || code.CodeChallenge == "" || !code.ExpiresAt.After(now) || !code.ExpiresAt.After(code.CreatedAt) {
		return oauth2.ErrInvalidGrant
	}
	binding, err := json.Marshal(code.Delegation)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, s.qualified, code.UserID)
		if err != nil {
			return oauthGrantError(err)
		}
		if revision != code.AuthRevision {
			return oauth2.ErrInvalidGrant
		}
		sess, err := s.lockSession(ctx, tx, approvingSessionID, code.UserID)
		if err != nil {
			return oauthGrantError(err)
		}
		now = time.Now().UTC()
		if !sess.FirstParty() || sess.Expired(now) || !code.ExpiresAt.After(now) {
			return oauth2.ErrInvalidGrant
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+s.table(oauthCodesTable)+` (`+oauthCodeColumns+`)
 VALUES (@hash,@user,@revision,@delegation,@redirect,@challenge,@created,@expires)`, pgx.NamedArgs{
			"hash": code.Hash, "user": code.UserID, "revision": code.AuthRevision, "delegation": string(binding), "redirect": code.RedirectURI, "challenge": code.CodeChallenge, "created": code.CreatedAt.UTC(), "expires": code.ExpiresAt.UTC()})
		return pgxdb.MapError(err)
	})
}

func (s *OAuth2Store) GetCode(ctx context.Context, hash string) (oauth2.Code, error) {
	row, err := pgxdb.QueryOne[oauthCodeRow](ctx, s.db, `SELECT `+oauthCodeColumns+` FROM `+s.table(oauthCodesTable)+` WHERE code_hash = @hash`, pgx.NamedArgs{"hash": hash})
	if err != nil {
		return oauth2.Code{}, oauthGrantError(err)
	}
	return row.toDomain()
}

func (s *OAuth2Store) RedeemCode(ctx context.Context, expected oauth2.Code, proposed session.Session, now time.Time) (session.Session, error) {
	if expected.Hash == "" || proposed.ID == "" || proposed.UserID != expected.UserID || proposed.Profile != session.ProfileDelegated || proposed.Delegation != expected.Delegation || proposed.Delegation.AuthRevision != expected.AuthRevision || !validOAuthBinding(proposed.Delegation) || proposed.RefreshTokenHash == "" || proposed.PreviousRefreshTokenHash != "" || proposed.PreviousUsed || proposed.RotationCount != 0 || proposed.Expired(now) {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, s.qualified, expected.UserID)
		if err != nil {
			return oauthGrantError(err)
		}
		if revision != expected.AuthRevision {
			return oauth2.ErrInvalidGrant
		}
		row, err := pgxdb.QueryOne[oauthCodeRow](ctx, tx, `SELECT `+oauthCodeColumns+` FROM `+s.table(oauthCodesTable)+` WHERE code_hash = @hash FOR UPDATE`, pgx.NamedArgs{"hash": expected.Hash})
		if err != nil {
			return oauthGrantError(err)
		}
		stored, err := row.toDomain()
		if err != nil {
			return err
		}
		if !sameOAuthCode(stored, expected) || !now.Before(stored.ExpiresAt) {
			return oauth2.ErrInvalidGrant
		}
		if err := s.requireUnusedRefreshHash(ctx, tx, proposed.RefreshTokenHash); err != nil {
			return err
		}
		args, err := sessionArgs(proposed)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, sessionInsert(s.table(sessionsTable)), args); err != nil {
			return pgxdb.MapError(err)
		}
		_, err = tx.Exec(ctx, `DELETE FROM `+s.table(oauthCodesTable)+` WHERE code_hash = @hash`, pgx.NamedArgs{"hash": stored.Hash})
		return pgxdb.MapError(err)
	})
	if err != nil {
		return session.Session{}, err
	}
	return proposed, nil
}

func validOAuthBinding(d session.Delegation) bool {
	return d.AuthRevision >= 0 && d.Issuer != "" && d.ClientID != "" && d.Resource != "" && d.ExchangeResource != "" && d.ExchangeClientID != "" && d.Resource != d.ExchangeResource
}

func sameOAuthCode(a, b oauth2.Code) bool {
	// PostgreSQL stores timestamptz with microsecond precision.
	return a.Hash == b.Hash && a.UserID == b.UserID && a.AuthRevision == b.AuthRevision && a.Delegation == b.Delegation && a.RedirectURI == b.RedirectURI && a.CodeChallenge == b.CodeChallenge && a.CreatedAt.Equal(b.CreatedAt.Truncate(time.Microsecond)) && a.ExpiresAt.Equal(b.ExpiresAt.Truncate(time.Microsecond))
}

func oauthGrantError(err error) error {
	if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, session.ErrUserNotActive) {
		return oauth2.ErrInvalidGrant
	}
	return err
}

func (s *OAuth2Store) lockSession(ctx context.Context, tx *pgxdb.Tx, id, userID string) (session.Session, error) {
	row, err := pgxdb.QueryOne[sessionRow](ctx, tx, `SELECT `+sessionColumns+` FROM `+s.table(sessionsTable)+` WHERE id = @id AND user_id = @user FOR UPDATE`, pgx.NamedArgs{"id": id, "user": userID})
	if err != nil {
		return session.Session{}, err
	}
	return row.toDomain()
}

// Discovery does not lock a session: acquiring a session before its user would
// invert credential/global-revocation lock order. Every binding is reread after
// the user lock is held.
func (s *OAuth2Store) refreshOwner(ctx context.Context, tx *pgxdb.Tx, hash string) (string, string, error) {
	var id, userID string
	err := tx.QueryRow(ctx, `SELECT id, user_id FROM `+s.table(sessionsTable)+` WHERE refresh_token_hash = @hash
 UNION SELECT s.id, s.user_id FROM `+s.table(sessionsTable)+` s JOIN `+s.table(oauthHistoryTable)+` h ON h.session_id = s.id WHERE h.refresh_token_hash = @hash`, pgx.NamedArgs{"hash": hash}).Scan(&id, &userID)
	return id, userID, pgxdb.MapError(err)
}

func (s *OAuth2Store) hasSpentRefresh(ctx context.Context, tx *pgxdb.Tx, hash, id string) (bool, error) {
	var spent bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+s.table(oauthHistoryTable)+` WHERE refresh_token_hash = @hash AND session_id = @id)`, pgx.NamedArgs{"hash": hash, "id": id}).Scan(&spent)
	return spent, pgxdb.MapError(err)
}

func (s *OAuth2Store) requireUnusedRefreshHash(ctx context.Context, tx *pgxdb.Tx, hash string) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+s.table(sessionsTable)+` WHERE refresh_token_hash = @hash OR previous_refresh_token_hash = @hash
 UNION ALL SELECT 1 FROM `+s.table(oauthHistoryTable)+` WHERE refresh_token_hash = @hash)`, pgx.NamedArgs{"hash": hash}).Scan(&exists)
	if err != nil {
		return pgxdb.MapError(err)
	}
	if exists {
		return sdk.ErrAlreadyExists
	}
	return nil
}

func (s *OAuth2Store) RotateRefresh(ctx context.Context, refresh oauth2.Refresh) (session.Session, error) {
	if refresh.Hash == "" || refresh.ClientID == "" || refresh.Resource == "" {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	var result session.Session
	reused := false
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		id, userID, err := s.refreshOwner(ctx, tx, refresh.Hash)
		if err != nil {
			return oauthGrantError(err)
		}
		if _, err := lockCredentialUser(ctx, tx, s.qualified, userID); err != nil {
			return oauthGrantError(err)
		}
		sess, err := s.lockSession(ctx, tx, id, userID)
		if err != nil {
			return oauthGrantError(err)
		}
		if sess.Profile != session.ProfileDelegated || !validOAuthBinding(sess.Delegation) || sess.Delegation.ClientID != refresh.ClientID || sess.Delegation.Resource != refresh.Resource || sess.Expired(refresh.Now) {
			return oauth2.ErrInvalidGrant
		}
		spent, err := s.hasSpentRefresh(ctx, tx, refresh.Hash, id)
		if err != nil {
			return err
		}
		if spent {
			if err := s.deleteDelegatedSession(ctx, tx, id, userID, refresh.ClientID); err != nil {
				return err
			}
			reused = true
			return nil // Commit the isolated revocation before reporting reuse.
		}
		if sess.RefreshTokenHash != refresh.Hash || refresh.NewHash == "" || refresh.NewHash == refresh.Hash {
			return oauth2.ErrInvalidGrant
		}
		if err := s.requireUnusedRefreshHash(ctx, tx, refresh.NewHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+s.table(oauthHistoryTable)+` (refresh_token_hash,session_id,expires_at) VALUES (@hash,@id,@expires)`, pgx.NamedArgs{"hash": refresh.Hash, "id": id, "expires": sess.ExpiresAt}); err != nil {
			return pgxdb.MapError(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE `+s.table(sessionsTable)+` SET refresh_token_hash = @new, rotation_count = rotation_count + 1 WHERE id = @id AND user_id = @user AND session_profile = @profile AND refresh_token_hash = @old`, pgx.NamedArgs{"new": refresh.NewHash, "id": id, "user": userID, "profile": string(session.ProfileDelegated), "old": refresh.Hash}); err != nil {
			return pgxdb.MapError(err)
		}
		sess.RefreshTokenHash = refresh.NewHash
		sess.RotationCount++
		result = sess
		return nil
	})
	if err != nil {
		return session.Session{}, err
	}
	if reused {
		return session.Session{}, oauth2.ErrRefreshReuse
	}
	return result, nil
}

func (s *OAuth2Store) lockOwner(ctx context.Context, tx *pgxdb.Tx, userID string) error {
	var id string
	return pgxdb.MapError(tx.QueryRow(ctx, `SELECT id FROM `+s.table(usersTable)+` WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": userID}).Scan(&id))
}

func (s *OAuth2Store) deleteOwnedSession(ctx context.Context, tx *pgxdb.Tx, id, userID string) error {
	result, err := tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+` WHERE id = @id AND user_id = @user`, pgx.NamedArgs{"id": id, "user": userID})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if result.RowsAffected() == 0 {
		return sdk.ErrNotFound
	}
	_, err = tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE session_id = @id AND user_id = @user`, pgx.NamedArgs{"id": id, "user": userID})
	return pgxdb.MapError(err)
}

func (s *OAuth2Store) RevokeRefresh(ctx context.Context, hash, clientID string) error {
	if hash == "" || clientID == "" {
		return nil
	}
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		id, userID, err := s.refreshOwner(ctx, tx, hash)
		if err != nil {
			return err
		}
		if err := s.lockOwner(ctx, tx, userID); err != nil {
			return err
		}
		sess, err := s.lockSession(ctx, tx, id, userID)
		if err != nil {
			return err
		}
		if sess.Profile != session.ProfileDelegated || sess.Delegation.ClientID != clientID {
			return nil
		}
		if sess.RefreshTokenHash != hash {
			spent, err := s.hasSpentRefresh(ctx, tx, hash, id)
			if err != nil {
				return err
			}
			if !spent {
				return nil
			}
		}
		return s.deleteDelegatedSession(ctx, tx, id, userID, clientID)
	})
	if errors.Is(err, sdk.ErrNotFound) {
		return nil
	}
	return err
}

func (s *OAuth2Store) RevokeGrant(ctx context.Context, id, userID, clientID string) error {
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := s.lockOwner(ctx, tx, userID); err != nil {
			return err
		}
		sess, err := s.lockSession(ctx, tx, id, userID)
		if err != nil {
			return err
		}
		if sess.Profile != session.ProfileDelegated || sess.Delegation.ClientID != clientID {
			return nil
		}
		// The locked row cannot change between this ownership check and deletion.
		return s.deleteDelegatedSession(ctx, tx, id, userID, clientID)
	})
	if errors.Is(err, sdk.ErrNotFound) {
		return nil
	}
	return err
}

func (s *OAuth2Store) Prune(ctx context.Context, now time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		for _, table := range []string{oauthClientsTable, oauthCodesTable, oauthHistoryTable} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(table)+` WHERE expires_at <= @now`, pgx.NamedArgs{"now": now.UTC()}); err != nil {
				return pgxdb.MapError(err)
			}
		}
		return nil
	})
}

func (s *OAuth2Store) ListByUser(ctx context.Context, userID string, req list.Request) (list.Page[session.Session], error) {
	page, err := pgxdb.List(ctx, s.db, pgxdb.ListQuery[sessionRow]{
		BaseSQL: `SELECT ` + sessionColumns + ` FROM ` + s.table(sessionsTable) + ` WHERE user_id = @user AND expires_at > @now`,
		Args:    pgx.NamedArgs{"user": userID, "now": time.Now().UTC()}, OrderFields: session.OrderFields, DefaultOrder: session.DefaultOrder, PK: "id",
		OrderValueOf: func(r sessionRow, _ string) any { return r.CreatedAt.UTC() }, PKOf: func(r sessionRow) string { return r.ID },
	}, req)
	if err != nil {
		return list.Page[session.Session]{}, err
	}
	return list.MapPageErr(page, sessionRow.toDomain)
}

func (s *OAuth2Store) DeleteForUser(ctx context.Context, id, userID string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := s.lockOwner(ctx, tx, userID); err != nil {
			return err
		}
		return s.deleteOwnedSession(ctx, tx, id, userID)
	})
}

func (s *OAuth2Store) RevokeAllForUser(ctx context.Context, userID string, now time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := s.lockOwner(ctx, tx, userID); err != nil {
			return err
		}
		if err := bumpCredentialRevision(ctx, tx, s.qualified, userID, now); err != nil {
			return err
		}
		return revokeCredentialState(ctx, tx, s.qualified, userID)
	})
}

func (s *OAuth2Store) deleteDelegatedSession(ctx context.Context, tx *pgxdb.Tx, id, userID, clientID string) error {
	result, err := tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+` WHERE id = @id AND user_id = @user AND session_profile = @profile AND (delegation::jsonb ->> 'ClientID') = @client`, pgx.NamedArgs{"id": id, "user": userID, "profile": string(session.ProfileDelegated), "client": clientID})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if result.RowsAffected() == 0 {
		return sdk.ErrNotFound
	}
	_, err = tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE session_id = @id AND user_id = @user`, pgx.NamedArgs{"id": id, "user": userID})
	return pgxdb.MapError(err)
}
