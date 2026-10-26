package credential

import "context"

// SnapshotScope names the optional credential tables a scoped Snapshot reads.
// Passkeys false means the host did not wire passkeys and may not have applied
// migration 0025: the store must not read passkey_credentials, and the
// MethodSet it returns carries no Passkeys.
type SnapshotScope struct {
	Passkeys bool
}

// MutationRepository is the revision-serialized credential/identifier mutation
// port (design §5.6). It is the store seam that closes concurrent self-removal:
// Snapshot reads the typed MethodSet together with the user's auth_revision, the
// service evaluates policy against the proposed set, and Apply performs the typed
// mutation atomically only when the expected revision still matches. Passwords,
// OAuth links, and identifiers stay in their typed tables; a common
// transaction-capable adapter coordinates them without a uniform storage model.
type MutationRepository interface {
	// Snapshot returns the user's current MethodSet, including the AuthRevision it
	// was read at and every passkey the user holds. An unknown user returns
	// sdk.ErrNotFound.
	Snapshot(ctx context.Context, userID string) (MethodSet, error)
	// Apply performs mutation atomically, incrementing auth_revision exactly once
	// on success, and revoking sessions, grants and password-reset challenges.
	// A stale expectedAuthRevision returns sdk.ErrConflict; a caller acting on
	// authentication proof must obtain fresh proof rather than rebasing it.
	// An unknown user returns sdk.ErrNotFound. Identifier mutations must validate
	// active ownership and replacement eligibility within the same transaction
	// (see RetireIdentifier.Validate and ChangeIdentifierUses.Validate).
	// RemovePasskey of an id the user does not own returns sdk.ErrNotFound;
	// RemovePasskeys deletes all of the user's passkeys, zero included.
	// A conflict or any failure leaves the user's credential state unchanged — the
	// mutation is never partially applied.
	Apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation Mutation) error
}

// ScopedSnapshotter is the OPTIONAL capability a MutationRepository implements
// when a table Snapshot reads is migrated only for a host that wires its
// feature (the SQL stores: passkey_credentials, migration 0025). SnapshotScoped
// is Snapshot reading only the tables scope names. The service calls it in
// place of Snapshot when passkeys are not wired; a repository without it is
// read through Snapshot and the service drops the Passkeys it returns, so a
// host implementation keeps working unchanged.
type ScopedSnapshotter interface {
	SnapshotScoped(ctx context.Context, userID string, scope SnapshotScope) (MethodSet, error)
}
