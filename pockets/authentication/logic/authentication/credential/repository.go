package credential

import "context"

// MutationRepository is the revision-serialized credential/identifier mutation
// port (design §5.6). It is the store seam that closes concurrent self-removal:
// Snapshot reads the typed MethodSet together with the user's auth_revision, the
// service evaluates policy against the proposed set, and Apply performs the typed
// mutation atomically only when the expected revision still matches. Passwords,
// OAuth links, and identifiers stay in their typed tables; a common
// transaction-capable adapter coordinates them without a uniform storage model.
type MutationRepository interface {
	// Snapshot returns the user's current MethodSet, including the AuthRevision it
	// was read at. An unknown user returns sdk.ErrNotFound.
	Snapshot(ctx context.Context, userID string) (MethodSet, error)
	// Apply performs mutation atomically, incrementing auth_revision exactly once
	// on success, and revoking sessions, grants and password-reset challenges.
	// A stale expectedAuthRevision returns sdk.ErrConflict; a caller acting on
	// authentication proof must obtain fresh proof rather than rebasing it.
	// An unknown user returns sdk.ErrNotFound.
	// A conflict or any failure leaves the user's credential state unchanged — the
	// mutation is never partially applied.
	Apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation Mutation) error
}
