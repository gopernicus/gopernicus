package totp

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
)

// Repository is the second-factor aggregate: one TOTP credential and its
// recovery codes per user. Implemented by pocket store adapters
// (pockets/authentication/stores/pgx, .../turso) or any host-provided
// implementation, backed by the same transaction-capable adapter as Users,
// Sessions, AuthenticationGrants and Passkeys; storetest.RunTOTP is the
// executable contract.
//
// Sentinel contract:
//   - Begin upserts the PENDING row (a re-Begin replaces it, resetting its
//     failures); a confirmed row → ErrAlreadyEnrolled.
//   - Get of a user with no row → sdk.ErrNotFound.
//   - Confirm locks the owning user row FIRST, then refuses in this order: an
//     unknown user → sdk.ErrNotFound; an inactive one → session.ErrUserNotActive;
//     no pending row → sdk.ErrNotFound; a confirmed one → ErrAlreadyEnrolled; an
//     auth_revision other than in.ExpectedAuthRevision → sdk.ErrConflict; a
//     bootstrap-tier input (in.Bootstrap) for an owner holding an AAL2-capable
//     (user-verified) passkey → ErrBootstrapClosed. It then, atomically, sets
//     ConfirmedAt and LastUsedStep, clears the pending fields and the failure
//     count, replaces the recovery codes with in.Codes, and increments
//     auth_revision. Two codes of one user with the same digest violate the
//     (user_id, code_digest) uniqueness → sdk.ErrAlreadyExists. A failure leaves
//     no confirmed row and no codes.
//   - RecordUse is a compare-and-swap on a confirmed row: it sets LastUsedStep to
//     step and LastUsedAt to at, and clears FailedAttempts and LockedUntil, only
//     while the stored LastUsedStep equals expectedStep and step is greater; a
//     miss, an equal or lower step, or no confirmed row → sdk.ErrConflict. Of two
//     concurrent calls at one expected step exactly one succeeds.
//   - RecordFailure is ONE atomic increment of FailedAttempts that reads the row
//     it writes: the failure reaching lockAt sets LockedUntil = now + lockFor and
//     resets the count to zero in the same statement, returning Failure{Attempts:
//     lockAt, LockedUntil}; any other returns the count reached and a nil
//     LockedUntil. Concurrent failures reaching the threshold lock exactly once.
//     No row → sdk.ErrNotFound.
//   - ReplaceRecoveryCodes replaces every recovery code of a user holding a
//     CONFIRMED credential (none → sdk.ErrNotFound). With expectedGeneratedAt it
//     is a compare-and-swap on the current set's GeneratedAt (absent codes compare
//     as nil): a mismatch → sdk.ErrConflict with nothing written.
//   - ConsumeRecoveryCode deletes the one code of userID matching a candidate
//     (KeyID and Digest both) and returns how many remain; no match, or a code of
//     another user → sdk.ErrNotFound. Concurrent consumes of one code: exactly one
//     succeeds.
//   - CountRecoveryCodes returns the count and the set's GeneratedAt (nil, zero
//     when none).
//   - Remove locks the owning user row FIRST: an unknown user → sdk.ErrNotFound;
//     an auth_revision other than expectedRevision → sdk.ErrConflict; no row →
//     Removed{} with nothing written. Otherwise it deletes the credential and every
//     recovery code, deletes every session and authentication grant of the user,
//     and increments auth_revision, in one transaction.
//   - RecoveryCodeKeyIDs returns the distinct key ids recovery codes are stored
//     under, across every user.
//
// Erasure (user.EraseRepository) deletes both the credential and the codes when
// user.EraseInput.TOTP says TOTP is wired.
type Repository interface {
	Begin(ctx context.Context, c Credential) error
	Get(ctx context.Context, userID string) (Credential, error)
	Confirm(ctx context.Context, in Confirmation) error
	RecordUse(ctx context.Context, userID string, expectedStep, step int64, at time.Time) error
	RecordFailure(ctx context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (Failure, error)
	ReplaceRecoveryCodes(ctx context.Context, userID string, codes []RecoveryCode, expectedGeneratedAt *time.Time) error
	ConsumeRecoveryCode(ctx context.Context, userID string, candidates []challenge.DigestCandidate, now time.Time) (remaining int, err error)
	CountRecoveryCodes(ctx context.Context, userID string) (int, *time.Time, error)
	Remove(ctx context.Context, userID string, expectedRevision int64) (Removed, error)
	RecoveryCodeKeyIDs(ctx context.Context) ([]string, error)
}

// SchemaProber is the optional boot check a Repository implements when its
// schema is migrated separately (the SQL stores: migration 0026). The pocket
// calls ProbeSchema once at construction, and ONLY when TOTP is wired: a host
// that never wires TOTP never needs the tables. A non-nil error refuses
// construction, so the store's error should name the missing migration.
type SchemaProber interface {
	ProbeSchema(ctx context.Context) error
}
