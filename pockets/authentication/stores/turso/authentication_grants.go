package turso

import (
	"context"
	"encoding/json"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// AuthGrantStore checks the user/session revocation anchors and persists or spends
// recent-authentication grants in the same serialized write transaction.
type AuthGrantStore struct {
	db *tursodb.DB
}

var _ authgrant.Repository = (*AuthGrantStore)(nil)

// NewAuthGrantStore returns an AuthGrantStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewAuthGrantStore(db *tursodb.DB) *AuthGrantStore {
	if db == nil {
		panic("authentication turso: NewAuthGrantStore received a nil database")
	}
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
func (s *AuthGrantStore) Create(ctx context.Context, g authgrant.Grant, expectedAuthRevision int64, now time.Time) (authgrant.Grant, error) {
	var out authgrant.Grant
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
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

func (s *AuthGrantStore) insertGrant(ctx context.Context, q tursodb.Querier, g authgrant.Grant) (authgrant.Grant, error) {
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
		if err := q.QueryRow(ctx, insert, args...).Scan(&g.ID); err != nil {
			return authgrant.Grant{}, tursodb.MapError(err)
		}
		return g, nil
	}
	const insert = `INSERT INTO authentication_grants
		(id, session_id, user_id, purpose, context_digest, methods, assurance, authenticated_at, expires_at, created_at, consumed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := q.Exec(ctx, insert, append([]any{g.ID}, args...)...); err != nil {
		return authgrant.Grant{}, tursodb.MapError(err)
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
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := s.lockGrantSession(ctx, tx, r.SessionID, r.UserID, now); err != nil {
			return err
		}
		var err error
		g, err = s.consumeGrant(ctx, tx, r, now)
		return tursodb.MapError(err)
	})
	if err != nil {
		return authgrant.Grant{}, err
	}
	if g.Expired(now) {
		return authgrant.Grant{}, sdk.ErrExpired
	}
	return g, nil
}

func (s *AuthGrantStore) consumeGrant(ctx context.Context, tx *tursodb.Tx, r authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	const q = `UPDATE authentication_grants SET consumed_at = ?
		WHERE id = (SELECT id FROM authentication_grants
			WHERE session_id = ? AND user_id = ? AND purpose = ? AND context_digest = ? AND consumed_at IS NULL
			AND (expires_at <= ? OR (authenticated_at >= ? AND authenticated_at <= ?
			AND CASE assurance WHEN 'aal1' THEN 1 WHEN 'aal2' THEN 2 WHEN 'aal3' THEN 3 ELSE 0 END >= ?))
			ORDER BY created_at, id LIMIT 1) AND consumed_at IS NULL
		RETURNING ` + authGrantReturning
	return scanGrant(tx.QueryRow(ctx, q, tursodb.FormatTime(now), r.SessionID, r.UserID, r.Purpose,
		r.ContextDigest, tursodb.FormatTime(now), tursodb.FormatTime(r.AuthenticatedAfter),
		tursodb.FormatTime(now), grantAssuranceRank(r.MinAssurance)))
}

// Lock the user before its session, matching credential/status revocation order.
// Proof admission and consumption cannot commit beside a completed revocation.
func (s *AuthGrantStore) lockGrantSession(ctx context.Context, q tursodb.Querier, sessionID, userID string, now time.Time) (int64, error) {
	var status string
	var revision int64
	if err := q.QueryRow(ctx, `SELECT status, auth_revision FROM users WHERE id = ?`, userID).Scan(&status, &revision); err != nil {
		return 0, tursodb.MapError(err)
	}
	if !user.NormalizeStatus(user.Status(status)).Active() {
		return 0, sdk.ErrUnauthorized
	}
	var owner string
	var expiry tursodb.Time
	if err := q.QueryRow(ctx, `SELECT user_id, expires_at FROM sessions WHERE id = ?`, sessionID).Scan(&owner, &expiry); err != nil {
		return 0, tursodb.MapError(err)
	}
	if owner != userID || !now.Before(expiry.Time) {
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
	if _, err := s.db.Exec(ctx, `DELETE FROM authentication_grants WHERE session_id = ?`, sessionID); err != nil {
		return tursodb.MapError(err)
	}
	return nil
}
