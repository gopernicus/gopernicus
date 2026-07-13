package cryptids

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

// minSecretBytes is the shortest HMAC secret NewHS256 accepts: 256 bits, the
// NIST minimum for HMAC-SHA256. A shorter key weakens the MAC below its design
// strength, so NewHS256 rejects it rather than sign with it.
const minSecretBytes = 32

// hs256Leeway is the clock-skew tolerance applied to time-based claims on
// Verify (see §1.2 of the auth-jwt plan). exp is accepted up to this long after
// it passes, and iat up to this long in the future, absorbing modest clock drift
// between the signing and verifying hosts. golang-jwt applies no leeway by
// default; 60s is the sdk default, documented here.
const hs256Leeway = 60 * time.Second

var (
	// ErrSecretTooShort is returned by NewHS256 when the secret is under
	// minSecretBytes.
	ErrSecretTooShort = fmt.Errorf("hs256: secret must be at least %d bytes", minSecretBytes)

	// ErrEmptyToken is returned by Verify for an empty token string.
	ErrEmptyToken = errors.New("hs256: token is empty")

	// ErrMalformedToken is returned by Verify when the token is not three
	// base64url segments separated by dots, or a segment fails to decode.
	ErrMalformedToken = errors.New("hs256: malformed token")

	// ErrUnexpectedSigningMethod is returned by Verify when the header alg is
	// absent or anything other than HS256. This is the alg-confusion guard.
	ErrUnexpectedSigningMethod = errors.New("hs256: unexpected signing method")

	// ErrSignatureInvalid is returned by Verify when the MAC does not match.
	ErrSignatureInvalid = errors.New("hs256: signature is invalid")

	// ErrTokenExpired is returned by Verify when exp has passed beyond the
	// leeway.
	ErrTokenExpired = errors.New("hs256: token is expired")

	// ErrTokenUsedBeforeIssued is returned by Verify when iat sits further in
	// the future than the leeway allows.
	ErrTokenUsedBeforeIssued = errors.New("hs256: token used before issued")
)

// Compile-time proof that HS256 satisfies the sdk-owned port.
var _ JWTSigner = (*HS256)(nil)

// hs256Header is the fixed JWT header this signer emits and requires. alg is
// hardcoded so Verify can reject any token presenting a different alg (or none)
// before a MAC is ever computed, closing the JWT algorithm-confusion hole.
type hs256Header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// HS256 is the in-package default JWTSigner, signing and verifying HMAC-SHA256
// JSON Web Tokens from a shared secret. Every dependency is from the standard
// library (crypto/hmac, crypto/sha256, encoding/base64, encoding/json), so it
// ships in the kernel — the zero-integration default that keeps golang-jwt out
// of the sdk. Asymmetric schemes (RS256/ES256) remain an integration.
//
// The secret is a shared symmetric key: every instance that verifies a token
// must hold the same secret used to sign it. A per-instance ephemeral key is a
// dev / single-instance host convenience only — behind a load balancer,
// instances with different keys cannot cross-verify, so multi-instance
// deployments MUST share the secret. Construct with NewHS256; the zero value is
// not usable.
type HS256 struct {
	secret []byte
}

// NewHS256 builds an HS256 signer from a shared secret. It returns
// ErrSecretTooShort when the secret is under 32 bytes.
func NewHS256(secret []byte) (*HS256, error) {
	if len(secret) < minSecretBytes {
		return nil, ErrSecretTooShort
	}
	// Copy so a later mutation of the caller's slice cannot swap the key.
	key := make([]byte, len(secret))
	copy(key, secret)
	return &HS256{secret: key}, nil
}

// Sign creates a signed token carrying claims plus registered exp and iat
// claims. expiresAt sets exp; iat is the current UTC time. Caller-supplied
// claims are copied over the registered defaults, so a caller may override exp
// or iat by including them.
func (s *HS256) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	all := map[string]any{
		"exp": expiresAt.Unix(),
		"iat": time.Now().UTC().Unix(),
	}
	maps.Copy(all, claims)

	headerSeg, err := encodeSegment(hs256Header{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", fmt.Errorf("hs256: encode header: %w", err)
	}
	claimsSeg, err := encodeSegment(all)
	if err != nil {
		return "", fmt.Errorf("hs256: encode claims: %w", err)
	}

	signingInput := headerSeg + "." + claimsSeg
	sig := base64.RawURLEncoding.EncodeToString(s.mac(signingInput))
	return signingInput + "." + sig, nil
}

// Verify parses and validates a token, returning its claims when the header alg
// is HS256, the MAC matches under constant-time comparison, and the time claims
// (exp, iat) check out within the leeway.
func (s *HS256) Verify(token string) (map[string]any, error) {
	if token == "" {
		return nil, ErrEmptyToken
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}
	headerSeg, claimsSeg, sigSeg := parts[0], parts[1], parts[2]

	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	var header hs256Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	// Hardcoded HS256: reject any other alg, and reject an absent alg — the
	// alg-confusion guard, applied before any MAC is computed.
	if header.Alg != "HS256" {
		return nil, fmt.Errorf("%w: %q", ErrUnexpectedSigningMethod, header.Alg)
	}

	providedSig, err := base64.RawURLEncoding.DecodeString(sigSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
	}
	expectedSig := s.mac(headerSeg + "." + claimsSeg)
	if !hmac.Equal(expectedSig, providedSig) {
		return nil, ErrSignatureInvalid
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(claimsSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}

	now := time.Now()
	if exp, ok := numericClaim(claims["exp"]); ok {
		if now.After(time.Unix(exp, 0).Add(hs256Leeway)) {
			return nil, ErrTokenExpired
		}
	}
	if iat, ok := numericClaim(claims["iat"]); ok {
		if time.Unix(iat, 0).After(now.Add(hs256Leeway)) {
			return nil, ErrTokenUsedBeforeIssued
		}
	}

	return claims, nil
}

// mac computes the HMAC-SHA256 of signingInput under the signer's secret.
func (s *HS256) mac(signingInput string) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(signingInput))
	return h.Sum(nil)
}

// encodeSegment marshals v to JSON and returns its raw base64url encoding.
func encodeSegment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// numericClaim reads a JSON-decoded numeric claim. json.Unmarshal into an
// any-valued map decodes every number as float64, which is what MapClaims does
// too; other types (or absent claims) report ok=false, leaving the claim
// unvalidated per the golang-jwt default.
func numericClaim(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int64(f), true
}
