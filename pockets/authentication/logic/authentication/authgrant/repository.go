package authgrant

import (
	"context"
	"time"
)

// Repository persists recent-authentication proofs. Admission and consumption
// participate in the same user/session concurrency boundary as credential and
// status revocation; a service-level read followed by an unchecked write does not
// satisfy this contract. Methods use their own atomic operation, not a host's
// ambient transaction.
type Repository interface {
	// Create checks the active owner, matching live session and expected user
	// AuthRevision atomically with inserting the grant. A stale revision returns
	// sdk.ErrConflict; an absent owner/session returns sdk.ErrNotFound; an inactive,
	// mismatched or expired session returns sdk.ErrUnauthorized. No rejected grant
	// is persisted. ID is assigned when empty. Method slices are caller-owned.
	Create(ctx context.Context, g Grant, expectedAuthRevision int64, now time.Time) (Grant, error)
	// Consume checks a live matching session and active user, then atomically spends
	// the oldest unspent matching grant that is expired or satisfies requirement.
	// An expired grant is spent and returns sdk.ErrExpired. A live suitable grant
	// is spent and returned. No suitable match returns sdk.ErrNotFound and leaves
	// unsuitable grants unspent. A second consume cannot spend the same grant.
	// Expiry/session checks use now; invalid requirements return sdk.ErrInvalidInput.
	Consume(ctx context.Context, requirement Requirement, now time.Time) (Grant, error)
	// DeleteBySession removes every grant for sessionID, including consumed rows.
	// This is an idempotent bulk operation; zero matches returns nil.
	DeleteBySession(ctx context.Context, sessionID string) error
}
