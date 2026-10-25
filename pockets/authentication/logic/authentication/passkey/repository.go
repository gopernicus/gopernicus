package passkey

import (
	"context"
	"time"
)

// CreateInput is one passkey for Repository.Create and the guard Create
// evaluates under the owner's user lock, beside the cap and handle checks.
type CreateInput struct {
	// Passkey is the row to store; an empty ID is assigned.
	Passkey Passkey
	// MaxPerUser is the cap: an owner already holding this many is refused with
	// ErrPasskeyLimit.
	MaxPerUser int
	// ExpectedAuthRevision is the owner's auth_revision when the registration
	// ceremony began. A moved revision — a credential changed, a passkey was
	// removed or reset, the account was deactivated and back — refuses with
	// sdk.ErrConflict; the caller restarts the ceremony.
	ExpectedAuthRevision int64
	// Bootstrap marks a ceremony authorized at the AAL1 bootstrap tier (the
	// owner held no AAL2-capable passkey when it began). It is refused with
	// ErrBootstrapClosed if the owner now holds one (Passkey.AAL2Capable).
	Bootstrap bool
}

// Repository persists passkeys. Implemented by pocket store adapters
// (pockets/authentication/stores/pgx, .../turso) or any host-provided
// implementation; storetest.RunPasskeys is the executable contract.
//
// Sentinel contract:
//   - Create locks the owning user row (the credential-mutation lock) and, under
//     it, in this order refuses: an unknown user with sdk.ErrNotFound; an
//     inactive one with session.ErrUserNotActive; an auth_revision other than
//     in.ExpectedAuthRevision with sdk.ErrConflict; a bootstrap-tier input
//     (in.Bootstrap) for an owner holding an AAL2-capable passkey with
//     ErrBootstrapClosed; a credential id already stored for anyone with
//     sdk.ErrAlreadyExists; an owner already holding in.MaxPerUser passkeys with
//     ErrPasskeyLimit; and a UserHandle that differs from the handle the owner's
//     existing passkeys carry with sdk.ErrConflict. Concurrent Creates for one
//     owner serialize on that lock, so the cap, the handle and the bootstrap
//     guard hold under concurrency. Create neither advances auth_revision nor
//     revokes anything.
//   - GetByCredentialID for an unknown credential id → sdk.ErrNotFound.
//   - Rename of an id the user does not own → sdk.ErrNotFound.
//   - RecordUse is a compare-and-swap on the stored sign count: a row whose count
//     is no longer expected, or a missing row, → sdk.ErrConflict; of two
//     concurrent calls at one expected count exactly one succeeds.
type Repository interface {
	// Create persists in.Passkey under the guard, assigning an ID when it is
	// empty, and returns the row as stored.
	Create(ctx context.Context, in CreateInput) (Passkey, error)
	// GetByCredentialID returns the passkey whose raw credential id is
	// credentialID.
	GetByCredentialID(ctx context.Context, credentialID []byte) (Passkey, error)
	// ListByUser returns userID's passkeys oldest first (an empty slice, nil
	// error, when none).
	ListByUser(ctx context.Context, userID string) ([]Passkey, error)
	// Rename sets the name of userID's passkey id.
	Rename(ctx context.Context, id, userID, name string) error
	// RecordUse sets the sign count to count, the backup state to backupState
	// and LastUsedAt to at, only while the stored count still equals expected.
	RecordUse(ctx context.Context, id string, expected, count uint32, backupState bool, at time.Time) error
}
