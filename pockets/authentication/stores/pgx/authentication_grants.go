package pgx

import (
	"context"
	"encoding/json"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// AuthGrantStore checks the user/session revocation anchors and persists or spends
// recent-authentication grants in the same transaction. Locks follow the user,
// session, grant order used by credential revocation.
type AuthGrantStore struct {
	db *pgxdb.DB
	qualified
}

var _ authgrant.Repository = (*AuthGrantStore)(nil)

// NewAuthGrantStore returns an AuthGrantStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewAuthGrantStore(db *pgxdb.DB, opts ...Option) *AuthGrantStore {
	if db == nil {
		panic("authentication pgx: NewAuthGrantStore received a nil database")
	}
	return &AuthGrantStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

const authGrantReturning = "id, session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at"

// encodeMethods marshals the honest method descriptors to the JSON text stored in
// the methods column; an empty set stores the empty string (the column DEFAULT).
func encodeMethods(methods []session.AuthenticationMethod) (string, error) {
	if len(methods) == 0 {
		return "", nil
	}
	b, err := json.Marshal(methods)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeMethods unmarshals the methods column back to descriptors; the empty
// string reads back as nil.
func decodeMethods(s string) ([]session.AuthenticationMethod, error) {
	if s == "" {
		return nil, nil
	}
	var out []session.AuthenticationMethod
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanGrant scans a full authentication_grants row from a Scanner.
func scanGrant(row pgxdb.Scanner) (authgrant.Grant, error) {
	var (
		g          authgrant.Grant
		methods    string
		assurance  string
		consumedAt *time.Time
	)
	err := row.Scan(
		&g.ID, &g.SessionID, &g.UserID, &g.Purpose, &g.ContextDigest,
		&methods, &assurance, &g.AuthenticatedAt, &g.ExpiresAt, &g.CreatedAt, &consumedAt,
	)
	if err != nil {
		return authgrant.Grant{}, err
	}
	decoded, err := decodeMethods(methods)
	if err != nil {
		return authgrant.Grant{}, err
	}
	g.Methods = decoded
	g.Assurance = session.AssuranceLevel(assurance)
	g.AuthenticatedAt = g.AuthenticatedAt.UTC()
	g.ExpiresAt = g.ExpiresAt.UTC()
	g.CreatedAt = g.CreatedAt.UTC()
	g.ConsumedAt = pgxdb.FromNullTime(consumedAt)
	return g, nil
}

// Create persists a new grant, assigning its ID when empty.
func (s *AuthGrantStore) Create(ctx context.Context, g authgrant.Grant, expectedAuthRevision int64, now time.Time) (authgrant.Grant, error) {
	var out authgrant.Grant
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		revision, err := s.lockGrantSession(ctx, tx, g.SessionID, g.UserID, now)
		if err != nil {
			return err
		}
		if revision != expectedAuthRevision {
			return sdk.ErrConflict
		}
		out, err = s.insertGrant(ctx, tx, g)
		return err
	})
	return out, err
}

func (s *AuthGrantStore) insertGrant(ctx context.Context, q pgxdb.Querier, g authgrant.Grant) (authgrant.Grant, error) {
	methods, err := encodeMethods(g.Methods)
	if err != nil {
		return authgrant.Grant{}, err
	}
	args := pgx.NamedArgs{
		"session_id":       g.SessionID,
		"user_id":          g.UserID,
		"purpose":          g.Purpose,
		"context_digest":   g.ContextDigest,
		"methods":          methods,
		"assurance":        string(g.Assurance),
		"authenticated_at": g.AuthenticatedAt.UTC(),
		"expires_at":       g.ExpiresAt.UTC(),
		"created_at":       g.CreatedAt.UTC(),
		"consumed_at":      pgxdb.NullTime(g.ConsumedAt),
	}
	if g.ID == "" {
		insert := `INSERT INTO ` + s.table(authGrantsTable) + `
			(session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
			VALUES (@session_id, @user_id, @purpose, @context_digest, @methods, @assurance, @authenticated_at, @expires_at, @created_at, @consumed_at)
			RETURNING id`
		if err := q.QueryRow(ctx, insert, args).Scan(&g.ID); err != nil {
			return authgrant.Grant{}, pgxdb.MapError(err)
		}
		return g, nil
	}
	args["id"] = g.ID
	insert := `INSERT INTO ` + s.table(authGrantsTable) + `
		(id, session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
		VALUES (@id, @session_id, @user_id, @purpose, @context_digest, @methods, @assurance, @authenticated_at, @expires_at, @created_at, @consumed_at)`
	if _, err := q.Exec(ctx, insert, args); err != nil {
		return authgrant.Grant{}, pgxdb.MapError(err)
	}
	return g, nil
}

// Consume atomically spends the (sessionID, purpose, contextDigest) grant: live →
// the Grant; expired → sdk.ErrExpired (consumed); no unconsumed match → sdk.ErrNotFound.
func (s *AuthGrantStore) Consume(ctx context.Context, r authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	if err := r.Validate(); err != nil {
		return authgrant.Grant{}, err
	}
	var g authgrant.Grant
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := s.lockGrantSession(ctx, tx, r.SessionID, r.UserID, now); err != nil {
			return err
		}
		var err error
		g, err = s.consumeGrant(ctx, tx, r, now)
		return pgxdb.MapError(err)
	})
	if err != nil {
		return authgrant.Grant{}, err
	}
	if g.Expired(now) {
		return authgrant.Grant{}, sdk.ErrExpired
	}
	return g, nil
}

func (s *AuthGrantStore) consumeGrant(ctx context.Context, tx *pgxdb.Tx, r authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	q := `UPDATE ` + s.table(authGrantsTable) + ` SET consumed_at = @now
		WHERE id = (SELECT id FROM ` + s.table(authGrantsTable) + `
			WHERE session_id = @session_id AND user_id = @user_id AND purpose = @purpose
			AND context_digest = @context_digest AND consumed_at IS NULL
			AND (expires_at <= @now OR (authenticated_at >= @oldest AND authenticated_at <= @now
			AND CASE assurance WHEN 'aal1' THEN 1 WHEN 'aal2' THEN 2 WHEN 'aal3' THEN 3 ELSE 0 END >= @assurance))
			ORDER BY created_at, id LIMIT 1 FOR UPDATE)
		RETURNING ` + authGrantReturning
	return scanGrant(tx.QueryRow(ctx, q, pgx.NamedArgs{
		"now": now.UTC(), "session_id": r.SessionID, "user_id": r.UserID,
		"purpose": r.Purpose, "context_digest": r.ContextDigest,
		"oldest": r.AuthenticatedAfter.UTC(), "assurance": grantAssuranceRank(r.MinAssurance),
	}))
}

// Lock the user before its session, matching credential/status revocation order.
// Proof admission and consumption cannot commit beside a completed revocation.
func (s *AuthGrantStore) lockGrantSession(ctx context.Context, q pgxdb.Querier, sessionID, userID string, now time.Time) (int64, error) {
	var status string
	var revision int64
	if err := q.QueryRow(ctx, `SELECT status, auth_revision FROM `+s.table(usersTable)+` WHERE id = $1 FOR UPDATE`, userID).Scan(&status, &revision); err != nil {
		return 0, pgxdb.MapError(err)
	}
	if !user.NormalizeStatus(user.Status(status)).Active() {
		return 0, sdk.ErrUnauthorized
	}
	var owner string
	var expiry time.Time
	if err := q.QueryRow(ctx, `SELECT user_id, expires_at FROM `+s.table(sessionsTable)+` WHERE id = $1 FOR UPDATE`, sessionID).Scan(&owner, &expiry); err != nil {
		return 0, pgxdb.MapError(err)
	}
	if owner != userID || !now.Before(expiry) {
		return 0, sdk.ErrUnauthorized
	}
	return revision, nil
}

func grantAssuranceRank(a session.AssuranceLevel) int {
	switch a {
	case session.AssuranceAAL1:
		return 1
	case session.AssuranceAAL2:
		return 2
	case session.AssuranceAAL3:
		return 3
	default:
		return 0
	}
}

// DeleteBySession removes every grant for sessionID; bulk and idempotent.
func (s *AuthGrantStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE session_id = @session_id`,
		pgx.NamedArgs{"session_id": sessionID}); err != nil {
		return pgxdb.MapError(err)
	}
	return nil
}
