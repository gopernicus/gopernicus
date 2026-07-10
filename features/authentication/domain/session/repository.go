package session

import "context"

// SessionRepository persists opaque server-side sessions keyed by their stored
// token value. Implemented by feature store adapters (features/authentication/stores/turso)
// or any host-provided implementation (see the storetest reference). The auth
// service hashes the cookie token before every Create/Get/Delete (design §7.3),
// so the token the store persists and looks up by is opaque to the store; the
// store does no hashing itself.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown token → sdk.ErrNotFound.
//   - Get for a token whose ExpiresAt is at or before the read time →
//     sdk.ErrExpired (expired-at-read: the store reports expiry rather than
//     returning a dead session; it MAY also delete the row).
//   - Delete for an unknown token → sdk.ErrNotFound.
//   - DeleteByUser is bulk and idempotent: it removes every session for the
//     user and returns nil even when none exist (never sdk.ErrNotFound).
type SessionRepository interface {
	// Create persists a new session.
	Create(ctx context.Context, s Session) (Session, error)
	// Get returns the live session for token: unknown → sdk.ErrNotFound,
	// present-but-expired → sdk.ErrExpired.
	Get(ctx context.Context, token string) (Session, error)
	// Delete removes the session for token; unknown → sdk.ErrNotFound.
	Delete(ctx context.Context, token string) error
	// DeleteByUser removes every session belonging to userID. It is bulk and
	// idempotent: zero matching rows returns nil, never sdk.ErrNotFound. It is
	// the logout-everywhere primitive (a password change revokes all sessions).
	DeleteByUser(ctx context.Context, userID string) error
}
