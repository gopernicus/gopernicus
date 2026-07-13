package pgx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// AuthGrantStore implements authgrant.Repository over a PostgreSQL database
// (design §5.0). Consume atomically resolves and spends the grant matching
// (session, purpose, context_digest) among unconsumed rows within one
// FOR UPDATE SKIP LOCKED subquery, so exactly one concurrent consumer wins and a
// grant earned for one context can never be spent for another. DeleteBySession is
// the bulk, idempotent revocation cascade.
type AuthGrantStore struct {
	db *pgxdb.DB
}

var _ authgrant.Repository = (*AuthGrantStore)(nil)

// NewAuthGrantStore returns an AuthGrantStore backed by db.
func NewAuthGrantStore(db *pgxdb.DB) *AuthGrantStore {
	return &AuthGrantStore{db: db}
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
func (s *AuthGrantStore) Create(ctx context.Context, g authgrant.Grant) (authgrant.Grant, error) {
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
		const insert = `INSERT INTO authentication_grants
			(session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
			VALUES (@session_id, @user_id, @purpose, @context_digest, @methods, @assurance, @authenticated_at, @expires_at, @created_at, @consumed_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, insert, args).Scan(&g.ID); err != nil {
			return authgrant.Grant{}, pgxdb.MapError(err)
		}
		return g, nil
	}
	args["id"] = g.ID
	const insert = `INSERT INTO authentication_grants
		(id, session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
		VALUES (@id, @session_id, @user_id, @purpose, @context_digest, @methods, @assurance, @authenticated_at, @expires_at, @created_at, @consumed_at)`
	if _, err := s.db.Exec(ctx, insert, args); err != nil {
		return authgrant.Grant{}, pgxdb.MapError(err)
	}
	return g, nil
}

// Consume atomically spends the (sessionID, purpose, contextDigest) grant: live →
// the Grant; expired → sdk.ErrExpired (consumed); no unconsumed match → sdk.ErrNotFound.
func (s *AuthGrantStore) Consume(ctx context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	const q = `UPDATE authentication_grants SET consumed_at = @now
		WHERE id = (
			SELECT id FROM authentication_grants
			WHERE session_id = @session_id AND purpose = @purpose AND context_digest = @context_digest AND consumed_at IS NULL
			ORDER BY created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + authGrantReturning
	g, err := scanGrant(s.db.QueryRow(ctx, q, pgx.NamedArgs{
		"now":            now.UTC(),
		"session_id":     sessionID,
		"purpose":        purpose,
		"context_digest": contextDigest,
	}))
	if err != nil {
		if err == pgx.ErrNoRows {
			return authgrant.Grant{}, sdk.ErrNotFound
		}
		return authgrant.Grant{}, pgxdb.MapError(err)
	}
	if g.Expired(now) {
		return authgrant.Grant{}, sdk.ErrExpired
	}
	return g, nil
}

// DeleteBySession removes every grant for sessionID; bulk and idempotent.
func (s *AuthGrantStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM authentication_grants WHERE session_id = @session_id`,
		pgx.NamedArgs{"session_id": sessionID}); err != nil {
		return pgxdb.MapError(err)
	}
	return nil
}
