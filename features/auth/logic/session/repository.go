package session

import "context"

// SessionRepository persists opaque server-side sessions keyed by token.
// Implemented by feature store adapters (features/auth/stores/turso) or any
// host-provided implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown token → errs.ErrNotFound.
//   - Get for a token whose ExpiresAt is at or before the read time →
//     errs.ErrExpired (expired-at-read: the store reports expiry rather than
//     returning a dead session; it MAY also delete the row).
//   - Delete for an unknown token → errs.ErrNotFound.
type SessionRepository interface {
	// Create persists a new session.
	Create(ctx context.Context, s Session) (Session, error)
	// Get returns the live session for token: unknown → errs.ErrNotFound,
	// present-but-expired → errs.ErrExpired.
	Get(ctx context.Context, token string) (Session, error)
	// Delete removes the session for token; unknown → errs.ErrNotFound.
	Delete(ctx context.Context, token string) error
}
