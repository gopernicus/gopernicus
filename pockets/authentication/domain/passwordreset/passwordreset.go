// Package passwordreset is the atomic password-reset composition domain (design
// §5.9). A successful reset is NOT a bare password write: it must, in one
// same-adapter transaction, redeem the live password_reset challenge, set the
// typed password row, revoke every session, and revoke the user's outstanding
// recent-authentication grants and password/reset challenges — so no
// changed-password/live-old-session partial state can ever exist. This is the
// freeze-transfer rule applied correctly (design §3.1): reset has cross-aggregate
// consume semantics, so the generic challenge port is NOT widened with a
// callback; a narrow composition repository owns the whole transaction.
//
// The repository (repository.go) is the whole security surface: the composition
// is ONE atomic step, so two simultaneous resets presenting the same token
// produce exactly one commit and the reset never mints a session (the caller is
// not logged in — design §5.9).
package passwordreset

import "time"

// RedeemInput is everything the atomic reset needs. The caller (authsvc) has
// already validated the new password and hashed it, and digested the presented
// reset token through the challenge protector — the repository receives no
// plaintext secret. Purpose is the challenge purpose consumed
// (challenge.PurposePasswordReset); TokenDigest is the SHA-256 digest of the
// presented reset token; NewPasswordHash is the already-validated, already-hashed
// new password; PurgeChallengePurposes names the outstanding password/reset
// challenge purposes to delete for the user in the same transaction (belt-and-
// braces beyond the consumed reset row); Now is the caller's clock for the
// challenge expiry decision.
type RedeemInput struct {
	Purpose                string
	TokenDigest            string
	NewPasswordHash        string
	PurgeChallengePurposes []string
	Now                    time.Time
}

// RedeemResult reports the user the reset applied to, for the caller's
// out-of-band notification and audit (design §5.9). The reset itself never logs
// the caller in, so no credential is returned.
type RedeemResult struct {
	UserID string
}
