// Package challenge is the atomic secret domain that replaces v1's plaintext-at-
// rest verification codes/tokens (design §3). One uniform contract carries every
// purpose: a short OTP-style code protected by a keyed HMAC (design §3.3) or a
// high-entropy URL token digested with SHA-256. The domain is deliberately its
// own table, NOT a widening of the frozen verification Code/Token ports — its
// redemption is atomic and keyed by (user, purpose) or (purpose, digest), never
// by the secret's plaintext value, which makes composite single-use redemption
// structural rather than disciplinary.
//
// The repository (repository.go) is the whole security surface: expiry, digest
// comparison, attempt counting, lockout, and consumption all happen inside one
// atomic operation so exactly one correct concurrent request can win. This
// package holds only the entity, the value types the port returns, and the
// vocabulary constants; the HMAC protection itself lives in the feature's public
// ChallengeProtector (a candidate digest per accepted key ID flows in through
// DigestCandidate).
package challenge

import "time"

// MaxAttempts is the default wrong-code budget a code challenge is consumed
// under: the fifth wrong attempt deletes the row and locks the flow out (design
// §3.2). It is passed to ConsumeCode as maxAttempts so a host can tighten it; the
// store never hard-codes a limit.
const MaxAttempts = 5

// Challenge purposes label a row so one uniform contract can serve every flow
// without forking the schema (design §3.2). A purpose is data, not a contract
// fork.
const (
	// PurposeVerifyRegistration confirms a newly registered email with a code
	// delivered to that address; consumption verifies the primary email identifier
	// (design §3.2 / §5.9). It carries the code lockout budget.
	PurposeVerifyRegistration = "verify_registration"
	// PurposePasswordReset is a forgot-password reset link (token) delivered to an
	// active verified recovery identifier; redemption runs the atomic
	// passwordreset composition (design §3.2 / §5.9). It uses no code lockout — the
	// 256-bit token space is the defense.
	PurposePasswordReset = "password_reset"
	// PurposeLoginMagicLink is a passwordless magic-link (token) login.
	PurposeLoginMagicLink = "login_magic_link"
	// PurposeLoginOTP is a passwordless one-time-code login.
	PurposeLoginOTP = "login_otp"
	// PurposeChangeEmail gates an email add/change with a code to the new address.
	PurposeChangeEmail = "change_email"
	// PurposeChangePhone gates a phone add/change with a code to the new number.
	PurposeChangePhone = "change_phone"
	// PurposeRemovePassword gates a code-confirmed password removal.
	PurposeRemovePassword = "remove_password"
	// PurposeUnlinkOAuth gates a provider-bound code-confirmed OAuth unlink.
	PurposeUnlinkOAuth = "unlink_oauth"
	// PurposeStepUp is a recent-authentication step-up code delivered to an existing
	// verified identifier (design §5.0). Its stored context binds the step-up to the
	// requested operation (grant purpose + context), so a code earned for one
	// mutation cannot complete another. It carries the code lockout budget.
	PurposeStepUp = "step_up"
)

// ConsumeOutcome is the disposition ConsumeCode decided inside its one atomic
// operation. It is the authoritative signal — the code path never smuggles its
// result through the error return, which is reserved for infrastructure failures
// — so a caller maps outcome to a stable error/event without string-parsing. The
// zero value is OutcomeNotFound so a zero-value return (e.g. after an infra
// error) reads fail-closed: no redemption.
type ConsumeOutcome int

const (
	// OutcomeNotFound means no live (user, purpose) row existed to consume.
	OutcomeNotFound ConsumeOutcome = iota
	// OutcomeExpired means the row was past its expiry; it was deleted.
	OutcomeExpired
	// OutcomeRejected means the code was wrong and an attempt was counted; the
	// row survives with an incremented attempt_count.
	OutcomeRejected
	// OutcomeLockedOut means the wrong code reached maxAttempts; the row was
	// deleted and the flow is locked out.
	OutcomeLockedOut
	// OutcomeContextMismatch means the code was correct but the bound context did
	// not match; the row was consumed anyway (anti-probing: a valid secret never
	// survives a wrong-context replay).
	OutcomeContextMismatch
	// OutcomeRedeemed means the code was correct and the context matched; the row
	// was consumed and the returned Consumed is authoritative.
	OutcomeRedeemed
)

// String renders the outcome for test and log output.
func (o ConsumeOutcome) String() string {
	switch o {
	case OutcomeNotFound:
		return "not_found"
	case OutcomeExpired:
		return "expired"
	case OutcomeRejected:
		return "rejected"
	case OutcomeLockedOut:
		return "locked_out"
	case OutcomeContextMismatch:
		return "context_mismatch"
	case OutcomeRedeemed:
		return "redeemed"
	default:
		return "unknown"
	}
}

// DigestCandidate pairs a challenge-protector key ID with the code digest
// computed under that key. During key rotation the protector returns one
// candidate per accepted key ID, and ConsumeCode selects the candidate whose
// KeyID matches the row's ProtectorKeyID before comparing digests (design §3.3),
// so an unexpired challenge issued under an old key stays verifiable. The
// feature's public authentication.DigestCandidate is an alias of this type so the
// protector and the repository speak one type.
type DigestCandidate struct {
	KeyID  string
	Digest string
}

// Challenge is one issued secret bound to a user and a purpose. SecretDigest is
// the stored digest (HMAC-SHA-256 for codes, SHA-256 for tokens) — the plaintext
// secret is never persisted. ProtectorKeyID names the pepper key a code digest
// was produced under (empty for tokens, which use no pepper). Context is an
// opaque binding blob (JSON): a code flow stores the context digest ConsumeCode
// compares by equality, a token flow stores the identifier binding the redeemer
// validates from Consumed. AttemptCount is the wrong-code counter; Version is the
// row's schema version.
type Challenge struct {
	ID             string
	UserID         string
	Purpose        string
	SecretDigest   string
	ProtectorKeyID string
	Context        []byte
	AttemptCount   int
	ExpiresAt      time.Time
	CreatedAt      time.Time
	Version        int
}

// Expired reports whether the challenge is at or past its expiry at now.
func (c Challenge) Expired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}

// Consumed is the row an atomic consume returned after deleting it. A code redeem
// yields it with OutcomeRedeemed; a token redeem returns it directly. The
// redeemer reads UserID/Context to validate the current identity binding before
// performing any business action (the token flow cannot know the user until the
// atomically deleted row is returned).
type Consumed struct {
	ID             string
	UserID         string
	Purpose        string
	Context        []byte
	ProtectorKeyID string
	ConsumedAt     time.Time
}
