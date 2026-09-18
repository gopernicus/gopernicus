// Package oauth2 owns authorization-server grants and their atomic persistence
// contracts. Provider sign-in remains the separate authentication OAuth client.
package oauth2

import (
	"context"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

var (
	ErrInvalidGrant = errors.New("oauth2: invalid grant")
	ErrRefreshReuse = errors.New("oauth2: refresh credential reused")
)

// Client is a validated, bounded public-client metadata snapshot. It contains no
// client secret. Trust must be checked again even when this snapshot is cached.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	ExpiresAt    time.Time
}

// Code stores a hash of a short-lived authorization code and the immutable
// consent binding. It has no dependency on the approving browser session.
type Code struct {
	Hash          string
	UserID        string
	AuthRevision  int64
	Delegation    session.Delegation
	RedirectURI   string
	CodeChallenge string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// Refresh binds a presented hash to its public client and original resource.
// Hash and NewHash cover the complete namespaced refresh credential. An empty
// NewHash permits spent-token detection/revocation only; it never rotates a
// current token. Services use this when a current-token read found no match.
type Refresh struct {
	Hash     string
	NewHash  string
	ClientID string
	Resource string
	Now      time.Time
}

// Repository is one transactional authority shared with Users and Sessions.
// Implementations never receive raw codes, refresh credentials or client secrets.
// Not-found code/refresh and stale bindings return ErrInvalidGrant. Infrastructure
// errors remain distinguishable. Every write is all-or-nothing.
type Repository interface {
	PutClient(context.Context, Client) error
	GetClient(context.Context, string) (Client, error)
	// CreateCode locks the user, requires the same current revision and a live
	// owned first-party approving session, then stores the code. This closes a
	// global-revoke race at approval. It does NOT retain a liveness dependency
	// on that browser session after approval.
	CreateCode(ctx context.Context, code Code, approvingSessionID string) error
	GetCode(context.Context, string) (Code, error)
	// RedeemCode compares the entire expected code against the stored row,
	// including its unexpired time, and locks the owning user before consuming
	// it. The active user's revision must match the approval revision. It checks
	// that proposed is delegated, has the same user/binding/revision, a unique
	// nonempty refresh hash, and an expiry after now; consumes the code and
	// creates proposed atomically. No other caller may redeem the same code.
	RedeemCode(ctx context.Context, expected Code, proposed session.Session, now time.Time) (session.Session, error)
	// RotateRefresh finds a current or retained spent hash, then locks user and
	// session in that order and rechecks active user, live delegated session,
	// client/resource and immutable binding. For a current hash it retains the
	// old hash through session expiry and atomically installs NewHash. It never
	// extends expiry or uses first-party grace. A spent hash with the correct
	// binding commits revocation of ONLY that session, THEN returns
	// ErrRefreshReuse. Wrong-client/resource attempts never mutate anything.
	RotateRefresh(context.Context, Refresh) (session.Session, error)
	// RevokeRefresh accepts current or spent hash with matching public client;
	// it deletes only the associated delegated session. Unknown/wrong-client
	// hashes are idempotent successes. Store failures must be returned.
	RevokeRefresh(ctx context.Context, hash, clientID string) error
	// RevokeGrant is similarly idempotent and checks all supplied ownership
	// coordinates plus the delegated profile in the mutation itself.
	RevokeGrant(ctx context.Context, id, userID, clientID string) error
	// Prune removes expired codes, metadata and refresh history only after its
	// connection horizon. It never removes history of a usable connection.
	Prune(ctx context.Context, now time.Time) error
}
