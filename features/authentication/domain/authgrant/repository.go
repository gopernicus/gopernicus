package authgrant

import (
	"context"
	"time"
)

// Repository persists recent-authentication grants with an atomic single-use
// consume (design §5.0). Implemented by feature store adapters
// (features/authentication/stores/turso, .../pgx) or any host-provided
// implementation (see the storetest reference). The port doc comments are the
// spec; the storetest conformance suite is their executable form.
//
//   - Consume atomically resolves and spends the grant matching (sessionID,
//     purpose, contextDigest): expiry, single-use, session binding, and context
//     mismatch are all decided in that one operation. A live match is consumed
//     and returned; an expired match is consumed (single-use) and returns
//     sdk.ErrExpired; no unconsumed match (unknown, already consumed, or a
//     different context) → sdk.ErrNotFound. A grant earned for one context can
//     never be spent for another, and a second Consume of the same grant →
//     sdk.ErrNotFound.
//   - DeleteBySession removes every grant for a session (the revocation cascade:
//     revoking a session invalidates its grants). It is bulk and idempotent —
//     zero matching rows returns nil, never sdk.ErrNotFound.
type Repository interface {
	// Create persists a new grant, assigning its ID when empty.
	Create(ctx context.Context, g Grant) (Grant, error)
	// Consume atomically spends the (sessionID, purpose, contextDigest) grant:
	// live → the Grant; expired → sdk.ErrExpired (consumed); no unconsumed match →
	// sdk.ErrNotFound.
	Consume(ctx context.Context, sessionID, purpose, contextDigest string, now time.Time) (Grant, error)
	// DeleteBySession removes every grant for sessionID; bulk and idempotent.
	DeleteBySession(ctx context.Context, sessionID string) error
}
