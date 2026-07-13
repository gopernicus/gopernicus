package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// AuthGrantStore implements authgrant.Repository over a libSQL database
// (design §5.0). libSQL/SQLite has no FOR UPDATE SKIP LOCKED, so Consume spends the
// matching grant through one atomic UPDATE ... WHERE ... AND consumed_at IS NULL
// RETURNING statement: the consumed_at IS NULL predicate plus the connector's
// serialized writes make exactly one concurrent consumer win, and a grant earned
// for one context can never be spent for another. DeleteBySession is the bulk,
// idempotent revocation cascade.
type AuthGrantStore struct {
	db *tursodb.DB
}

var _ authgrant.Repository = (*AuthGrantStore)(nil)

// NewAuthGrantStore returns an AuthGrantStore backed by db.
func NewAuthGrantStore(db *tursodb.DB) *AuthGrantStore {
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

// scanGrant scans a full authentication_grants row from a Scanner, decoding the
// JSON methods column, the fixed-width TEXT timestamps, and the nullable consumed_at.
func scanGrant(row tursodb.Scanner) (authgrant.Grant, error) {
	var (
		g               authgrant.Grant
		methods         string
		assurance       string
		authenticatedAt tursodb.Time
		expiresAt       tursodb.Time
		createdAt       tursodb.Time
		consumedAt      tursodb.NullTime
	)
	err := row.Scan(
		&g.ID, &g.SessionID, &g.UserID, &g.Purpose, &g.ContextDigest,
		&methods, &assurance, &authenticatedAt, &expiresAt, &createdAt, &consumedAt,
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
	g.AuthenticatedAt = authenticatedAt.Time
	g.ExpiresAt = expiresAt.Time
	g.CreatedAt = createdAt.Time
	g.ConsumedAt = consumedAt.Time
	return g, nil
}

// Create persists a new grant, assigning its ID when empty.
func (s *AuthGrantStore) Create(ctx context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	methods, err := encodeMethods(g.Methods)
	if err != nil {
		return authgrant.Grant{}, err
	}
	args := []any{
		g.SessionID,
		g.UserID,
		g.Purpose,
		g.ContextDigest,
		methods,
		string(g.Assurance),
		tursodb.FormatTime(g.AuthenticatedAt),
		tursodb.FormatTime(g.ExpiresAt),
		tursodb.FormatTime(g.CreatedAt),
		tursodb.FormatNullTime(g.ConsumedAt),
	}
	if g.ID == "" {
		const insert = `INSERT INTO authentication_grants
			(session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id`
		if err := s.db.QueryRow(ctx, insert, args...).Scan(&g.ID); err != nil {
			return authgrant.Grant{}, tursodb.MapError(err)
		}
		return g, nil
	}
	const insert = `INSERT INTO authentication_grants
		(id, session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(ctx, insert, append([]any{g.ID}, args...)...); err != nil {
		return authgrant.Grant{}, tursodb.MapError(err)
	}
	return g, nil
}

// Consume atomically spends the (sessionID, purpose, contextDigest) grant: live →
// the Grant; expired → sdk.ErrExpired (consumed); no unconsumed match → sdk.ErrNotFound.
func (s *AuthGrantStore) Consume(ctx context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	const q = `UPDATE authentication_grants SET consumed_at = ?
		WHERE id = (
			SELECT id FROM authentication_grants
			WHERE session_id = ? AND purpose = ? AND context_digest = ? AND consumed_at IS NULL
			ORDER BY created_at, id
			LIMIT 1
		) AND consumed_at IS NULL
		RETURNING ` + authGrantReturning
	g, err := scanGrant(s.db.QueryRow(ctx, q, tursodb.FormatTime(now), sessionID, purpose, contextDigest))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authgrant.Grant{}, sdk.ErrNotFound
		}
		return authgrant.Grant{}, tursodb.MapError(err)
	}
	if g.Expired(now) {
		return authgrant.Grant{}, sdk.ErrExpired
	}
	return g, nil
}

// DeleteBySession removes every grant for sessionID; bulk and idempotent.
func (s *AuthGrantStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM authentication_grants WHERE session_id = ?`, sessionID); err != nil {
		return tursodb.MapError(err)
	}
	return nil
}
