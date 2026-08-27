package passwordreset

import "context"

// Repository performs the atomic password-reset composition (design §5.9).
// Implemented by feature store adapters (features/authentication/stores/turso,
// .../pgx) or any host-provided implementation (see the storetest reference); the
// port is public because those adapters live outside the feature module. The port
// doc comments are the spec; the storetest conformance suite is their executable
// form.
//
// Redeem is ONE atomic operation over a common transaction-capable adapter that,
// on a live password_reset challenge, performs all of the following or none of
// them:
//
//  1. deletes/returns the live (Purpose, TokenDigest) challenge — resolving the
//     user FROM that row;
//  2. sets the typed user_passwords row to NewPasswordHash;
//  3. deletes every session belonging to that user;
//  4. deletes that user's outstanding recent-authentication grants and the
//     PurgeChallengePurposes challenges; and
//  5. returns the user ID for the caller's notification/audit.
//
// A challenge that is unknown, already consumed, or expired is NOT live and
// returns sdk.ErrNotFound with no changes applied — the single generic failure
// (design §5.8 enumeration/anti-probing). Because the composition is atomic, two
// simultaneous resets presenting one token yield exactly one success and one
// sdk.ErrNotFound, and an injected failure at any statement rolls the whole
// operation back — there is never a changed-password/live-old-session partial
// state.
type Repository interface {
	// Redeem atomically consumes the live reset challenge and applies the full
	// reset composition, returning the reset user's ID. A non-live challenge
	// (unknown, consumed, or expired) → sdk.ErrNotFound, no changes.
	Redeem(ctx context.Context, in RedeemInput) (RedeemResult, error)
}
