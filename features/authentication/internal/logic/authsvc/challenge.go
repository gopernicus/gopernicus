package authsvc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// Challenge secret formats. A purpose is either a short numeric CODE (OTP /
// sensitive-op, HMAC-protected, retry-with-lockout) or a high-entropy URL TOKEN
// (magic link, SHA-256-digested, single-use). The format decides which secret
// the issue path mints and which consume path (ConsumeChallenge vs RedeemToken)
// is the allowed caller path — the "allowed caller path" half of the purpose
// metadata (design §3.2).
type challengeFormat int

const (
	// formatCode is a six-digit numeric code (OTP / sensitive-op).
	formatCode challengeFormat = iota
	// formatToken is a 256-bit URL-safe token (magic link / reset link).
	formatToken
)

// Challenge secret sizes.
const (
	// codeMax bounds the uniform six-digit code space [000000, 999999].
	codeMax = 1_000_000
	// tokenBytes is the 256-bit token width; base64url-encoded to a URL-safe
	// secret. Entropy, not a pepper, protects tokens (design §3.3).
	tokenBytes = 32
)

// challengeSpec is a purpose's metadata: its secret format, time-to-live, and
// wrong-attempt budget (design §3.2). The registry below is the whole set of
// legal purposes — an unknown purpose is rejected, so a caller can never drive
// the rail with a user-controlled purpose string.
type challengeSpec struct {
	format      challengeFormat
	ttl         time.Duration
	maxAttempts int
}

// challengeSpecs is the closed purpose registry (design §3.2 TTL table). Codes
// carry the MaxAttempts lockout budget; tokens carry none (the 256-bit space is
// the defense). The register-verify and password-reset purposes are added when
// their flows migrate onto the rail (AV3-3.2 / AV3-3.3).
var challengeSpecs = map[string]challengeSpec{
	challenge.PurposeVerifyRegistration: {format: formatCode, ttl: 15 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposePasswordReset:      {format: formatToken, ttl: passwordResetTTL},
	challenge.PurposeLoginMagicLink:     {format: formatToken, ttl: 15 * time.Minute},
	challenge.PurposeLoginOTP:           {format: formatCode, ttl: 5 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposeChangeEmail:        {format: formatCode, ttl: 15 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposeChangePhone:        {format: formatCode, ttl: 15 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposeRemovePassword:     {format: formatCode, ttl: 15 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposeUnlinkOAuth:        {format: formatCode, ttl: 15 * time.Minute, maxAttempts: challenge.MaxAttempts},
	challenge.PurposeStepUp:             {format: formatCode, ttl: 5 * time.Minute, maxAttempts: challenge.MaxAttempts},
}

// Stable challenge-rail errors (design §5.8). The transport maps the wrapped sdk
// kind to the machine codes challenge_expired (410), challenge_invalid (400), and
// too_many_attempts (403). Every non-success disposition collapses to one of
// these three so a response never distinguishes "no such challenge" from "wrong
// code" (enumeration protection) — a secret is never named in the error.
var (
	// ErrChallengeExpired is returned when the challenge existed but was past its
	// TTL; the row is already deleted. Maps to 410.
	ErrChallengeExpired = fmt.Errorf("challenge expired: %w", sdk.ErrExpired)
	// ErrChallengeInvalid is the single generic failure for a missing, wrong,
	// malformed, or wrong-context challenge; on the code path a wrong attempt was
	// counted, on the token path the row (if any) is already consumed. Maps to 400.
	ErrChallengeInvalid = fmt.Errorf("challenge invalid: %w", sdk.ErrInvalidInput)
	// ErrTooManyAttempts is returned when a wrong code spent the last attempt: the
	// row was deleted and the flow is locked out. Maps to 403.
	ErrTooManyAttempts = fmt.Errorf("too many attempts: %w", sdk.ErrForbidden)
	// ErrUnknownChallengePurpose is returned when a purpose is not in the closed
	// registry. It is a wiring bug (purposes are package constants), never a
	// user-facing outcome, so it wraps sdk.ErrInvalidInput and is caught in tests.
	ErrUnknownChallengePurpose = fmt.Errorf("unknown challenge purpose: %w", sdk.ErrInvalidInput)
)

// challengeProtector protects short codes with a keyed HMAC and digests
// high-entropy tokens (design §3.3). auth.ChallengeProtector (the bundled
// HMACChallengeProtector) satisfies it structurally; it is declared here rather
// than imported from the auth package so the internal service carries no import
// cycle with its own host-facing package. Accept interfaces, return structs.
type challengeProtector interface {
	ActiveKeyID() string
	DigestCode(keyID, userID, purpose, code string) (string, error)
	CandidateCodeDigests(userID, purpose, code string) ([]challenge.DigestCandidate, error)
	DigestToken(token string) string
}

// challengeOptions collects the per-call context binding (design §3.2). On issue
// it is the binding the challenge is minted for; on consume it is the CURRENT
// binding to validate against — a code compares a digest inside the atomic
// ConsumeCode, a token compares the returned blob after the atomic delete. The
// zero value means "no binding".
type challengeOptions struct {
	context    any
	hasContext bool
}

// ChallengeOption configures IssueChallenge / ConsumeChallenge / RedeemToken.
type ChallengeOption func(*challengeOptions)

// WithStoredContext binds context v into an issued challenge (design §3.2). For a
// code it stores the context DIGEST that ConsumeChallenge later compares by
// equality; for a token it stores the identity-binding blob the redeemer
// validates against the current identifier. It is never a payload channel — only
// a redemption-time binding validator.
func WithStoredContext(v any) ChallengeOption {
	return func(o *challengeOptions) { o.context = v; o.hasContext = true }
}

// WithExpectedContext supplies the CURRENT binding to validate a redemption
// against (design §3.2). A code passes it as the expected digest to the atomic
// ConsumeCode; a token compares it to the consumed row's stored blob. A mismatch
// is ErrChallengeInvalid and the secret is spent either way — a valid secret
// never survives a wrong-context replay.
func WithExpectedContext(v any) ChallengeOption {
	return func(o *challengeOptions) { o.context = v; o.hasContext = true }
}

// IssueChallenge mints a fresh secret for (userID, purpose), protects it, and
// atomically replaces any prior (user, purpose) challenge, returning the
// PLAINTEXT secret for the caller to deliver — the only moment it exists. The
// secret is never persisted, logged, or placed in an event: the row stores only
// its digest. A code is HMAC-protected under the active key; a token is
// SHA-256-digested. An unknown purpose is ErrUnknownChallengePurpose.
func (s *Service) IssueChallenge(ctx context.Context, userID, purpose string, opts ...ChallengeOption) (string, error) {
	spec, ok := challengeSpecs[purpose]
	if !ok {
		return "", ErrUnknownChallengePurpose
	}
	if s.challenges == nil || s.protector == nil {
		return "", fmt.Errorf("challenge subsystem not wired: %w", sdk.ErrForbidden)
	}
	o := applyChallengeOptions(opts)
	now := s.now()

	var (
		secret, digest, keyID string
		contextBytes          []byte
		err                   error
	)
	switch spec.format {
	case formatCode:
		secret, err = generateCode()
		if err != nil {
			return "", fmt.Errorf("generate code: %w", err)
		}
		keyID = s.protector.ActiveKeyID()
		digest, err = s.protector.DigestCode(keyID, userID, purpose, secret)
		if err != nil {
			return "", fmt.Errorf("digest code: %w", err)
		}
		if o.hasContext {
			contextBytes, err = contextDigest(o.context)
			if err != nil {
				return "", err
			}
		}
	default: // formatToken
		secret, err = generateToken()
		if err != nil {
			return "", fmt.Errorf("generate token: %w", err)
		}
		digest = s.protector.DigestToken(secret)
		if o.hasContext {
			contextBytes, err = contextBlob(o.context)
			if err != nil {
				return "", err
			}
		}
	}

	ch := challenge.Challenge{
		UserID:         userID,
		Purpose:        purpose,
		SecretDigest:   digest,
		ProtectorKeyID: keyID,
		Context:        contextBytes,
		ExpiresAt:      now.Add(spec.ttl),
		CreatedAt:      now,
	}
	if _, err := s.challenges.Replace(ctx, ch); err != nil {
		return "", err
	}
	return secret, nil
}

// ConsumeChallenge redeems a CODE challenge for (userID, purpose): it builds a
// candidate digest per accepted protector key (so a code issued under a rotated-
// away key stays verifiable) and the expected context digest, then the repository
// atomically compares, counts a wrong attempt, locks out, or consumes — exactly
// one concurrent correct submission wins. The ConsumeOutcome maps to a stable
// error; a redeemed code returns the Consumed row for the caller's business
// action. A lockout records a challenge_lockout security event.
func (s *Service) ConsumeChallenge(ctx context.Context, userID, purpose, presented string, opts ...ChallengeOption) (challenge.Consumed, error) {
	spec, ok := challengeSpecs[purpose]
	if !ok {
		return challenge.Consumed{}, ErrUnknownChallengePurpose
	}
	if spec.format != formatCode {
		return challenge.Consumed{}, ErrUnknownChallengePurpose
	}
	if s.challenges == nil || s.protector == nil {
		return challenge.Consumed{}, fmt.Errorf("challenge subsystem not wired: %w", sdk.ErrForbidden)
	}
	if presented == "" {
		return challenge.Consumed{}, ErrChallengeInvalid
	}
	candidates, err := s.protector.CandidateCodeDigests(userID, purpose, presented)
	if err != nil {
		// A malformed (e.g. empty) code never matches; do not leak the reason.
		return challenge.Consumed{}, ErrChallengeInvalid
	}
	o := applyChallengeOptions(opts)
	expected := ""
	if o.hasContext {
		digestBytes, derr := contextDigest(o.context)
		if derr != nil {
			return challenge.Consumed{}, derr
		}
		expected = string(digestBytes)
	}

	consumed, outcome, err := s.challenges.ConsumeCode(ctx, userID, purpose, candidates, expected, spec.maxAttempts, s.now())
	if err != nil {
		return challenge.Consumed{}, err
	}
	switch outcome {
	case challenge.OutcomeRedeemed:
		return consumed, nil
	case challenge.OutcomeExpired:
		return challenge.Consumed{}, ErrChallengeExpired
	case challenge.OutcomeLockedOut:
		s.recordSecurityEvent(ctx, securityEventInput{
			UserID:  userID,
			Type:    securityevent.TypeChallengeLockout,
			Status:  securityevent.StatusBlocked,
			Details: map[string]any{"purpose": purpose},
		})
		return challenge.Consumed{}, ErrTooManyAttempts
	default: // OutcomeNotFound, OutcomeRejected, OutcomeContextMismatch, unknown
		return challenge.Consumed{}, ErrChallengeInvalid
	}
}

// RedeemToken redeems a TOKEN challenge by (purpose, digest): the repository
// atomically deletes-and-returns the row, the user is resolved FROM that row, and
// the stored identity binding is validated against the caller-supplied current
// binding (WithExpectedContext) BEFORE any business action. A magic link whose
// bound identifier changed since issue therefore fails as ErrChallengeInvalid
// with the secret already spent (anti-probing). Missing/malformed → the generic
// ErrChallengeInvalid; expired → ErrChallengeExpired.
func (s *Service) RedeemToken(ctx context.Context, purpose, presented string, opts ...ChallengeOption) (challenge.Consumed, error) {
	spec, ok := challengeSpecs[purpose]
	if !ok {
		return challenge.Consumed{}, ErrUnknownChallengePurpose
	}
	if spec.format != formatToken {
		return challenge.Consumed{}, ErrUnknownChallengePurpose
	}
	if s.challenges == nil || s.protector == nil {
		return challenge.Consumed{}, fmt.Errorf("challenge subsystem not wired: %w", sdk.ErrForbidden)
	}
	if presented == "" {
		return challenge.Consumed{}, ErrChallengeInvalid
	}
	digest := s.protector.DigestToken(presented)
	consumed, err := s.challenges.ConsumeToken(ctx, purpose, digest, s.now())
	if err != nil {
		switch {
		case errors.Is(err, sdk.ErrExpired):
			return challenge.Consumed{}, ErrChallengeExpired
		case errors.Is(err, sdk.ErrNotFound):
			return challenge.Consumed{}, ErrChallengeInvalid
		default:
			return challenge.Consumed{}, err
		}
	}
	o := applyChallengeOptions(opts)
	if o.hasContext {
		expected, derr := contextBlob(o.context)
		if derr != nil {
			return challenge.Consumed{}, derr
		}
		// The row is already consumed; a stale binding is a generic invalid.
		if string(consumed.Context) != string(expected) {
			return challenge.Consumed{}, ErrChallengeInvalid
		}
	}
	return consumed, nil
}

// applyChallengeOptions folds the option funcs into a challengeOptions.
func applyChallengeOptions(opts []ChallengeOption) challengeOptions {
	var o challengeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// contextDigest encodes a code challenge's binding as a stable SHA-256 hex digest
// (design §3.2): the row stores the digest, and ConsumeCode compares the expected
// digest by equality — the raw binding never reaches the store.
func contextDigest(v any) ([]byte, error) {
	blob, err := contextBlob(v)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	dst := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(dst, sum[:])
	return dst, nil
}

// contextBlob encodes a binding as canonical JSON bytes: a token stores the blob
// (returned in Consumed for the redeemer to validate) and a code digests it.
func contextBlob(v any) ([]byte, error) {
	blob, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode challenge context: %w", sdk.ErrInvalidInput)
	}
	return blob, nil
}

// generateCode returns a cryptographically secure, uniformly distributed
// six-digit numeric code. It uses crypto/rand directly — NEVER Config.IDs (the
// D9/D10 opaque-secret rule): a secret's entropy must never follow an entity-ID
// wiring choice.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(codeMax))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// generateToken returns a cryptographically secure 256-bit URL-safe token
// (base64url, no padding) from crypto/rand — never Config.IDs.
func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
