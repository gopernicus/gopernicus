package identifier

import (
	"context"
	"time"
)

// ApplyVerifiedChangeInput is the identifier-owned description of a confirmed
// contact change the store applies atomically (design §2.2). It is deliberately
// NOT contactchange.PendingChange: the identifier repository must not import the
// contactchange domain (a sibling-domain import cycle), so authsvc translates a
// consumed contactchange.PendingChange into this value at the confirm step
// (phase-1 implementation cut). It carries the persistence-relevant subset of the
// pending change: the target user, the new normalized value and its kind, the
// requested uses, whether the new identifier becomes primary, and the prior
// identifier it replaces (empty for a pure add).
type ApplyVerifiedChangeInput struct {
	UserID              string
	Kind                Kind
	NormalizedValue     string
	LoginEnabled        bool
	RecoveryEnabled     bool
	NotificationEnabled bool
	MakePrimary         bool
	// ReplacesIdentifierID names the active identifier retired in the same atomic
	// operation (a change/replacement); empty for a pure add.
	ReplacesIdentifierID string
}

// IdentifierRepository reads and mutates a user's identifiers (design §2.2). Its
// atomic operations (CreateWithPrimaryIdentifier lives on UserRepository, and
// ApplyVerifiedChange here) must be backed by the same transaction-capable
// adapter that owns UserRepository so user + identifier + auth_revision commit
// together. Reference implementations use one mutex across both and reproduce the
// partial-index semantics of §2.1.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown id → sdk.ErrNotFound.
//   - GetLogin / GetRecovery for an active identifier claiming that
//     (kind, normalized value) for the applicable use → the row; no active claim
//     → sdk.ErrNotFound. Only active (not replaced) rows are returned, and a
//     notification-only value is not a login or recovery claim.
//   - ListByUser returns the user's ACTIVE identifiers only (replaced rows are
//     history, not normal reads), never an error for an unknown user (empty
//     slice).
//   - ApplyVerifiedChange at a stale expectedAuthRevision → sdk.ErrConflict; a
//     lost authentication-claim race on the new value → sdk.ErrAlreadyExists;
//     otherwise it retires the replaced identifier (and any displaced primary),
//     claims the new value with verifiedAt recorded, increments the user's
//     auth_revision exactly once, and returns the new identifier.
type IdentifierRepository interface {
	// Get returns the identifier with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Identifier, error)
	// GetLogin returns the single active login-enabled identifier claiming
	// (kind, normalizedValue), or sdk.ErrNotFound. Verification is a service
	// branch, not a store filter — the row is returned even when unverified so the
	// caller can enforce §2.3.
	GetLogin(ctx context.Context, kind, normalizedValue string) (Identifier, error)
	// GetRecovery returns the single active recovery-enabled identifier claiming
	// (kind, normalizedValue), or sdk.ErrNotFound.
	GetRecovery(ctx context.Context, kind, normalizedValue string) (Identifier, error)
	// ListByUser returns the user's active identifiers (replaced rows excluded).
	ListByUser(ctx context.Context, userID string) ([]Identifier, error)
	// ApplyVerifiedChange atomically applies a confirmed change under the expected
	// auth_revision, returning the newly claimed identifier. See the sentinel
	// contract above for the error mapping.
	ApplyVerifiedChange(ctx context.Context, input ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (Identifier, error)
}
