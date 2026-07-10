// Package golangjwt is a stateless-token connector wrapping exactly one
// third-party library, github.com/golang-jwt/jwt/v5. Its Signer structurally
// satisfies the sdk-owned cryptids.JWTSigner port, signing and verifying
// HMAC-SHA JSON Web Tokens from a shared secret.
//
// It owns "how to sign a token with golang-jwt," never any feature's
// authentication policy. A different token scheme (asymmetric RS/ES, PASETO)
// would be a sibling connector, swapped at the composition root.
//
// Verification pins a token's signing method to the one this Signer was built
// with: an HS256 Signer rejects a token presented as HS384, HS512, any
// asymmetric algorithm, or alg=none. That closes the classic JWT
// algorithm-confusion hole, where an attacker rewrites the header alg to steer
// verification onto a weaker or key-mismatched path.
package golangjwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// minSecretBytes is the shortest HMAC secret Signer accepts: 256 bits, the NIST
// minimum for HMAC-SHA256. A shorter key weakens the MAC below its design
// strength, so New rejects it rather than sign with it.
const minSecretBytes = 32

var (
	// ErrSecretTooShort is returned by New when the secret is under
	// minSecretBytes.
	ErrSecretTooShort = fmt.Errorf("golangjwt: secret must be at least %d bytes", minSecretBytes)

	// ErrEmptyToken is returned by Verify for an empty token string.
	ErrEmptyToken = errors.New("golangjwt: token is empty")
)

// Compile-time proof that Signer satisfies the sdk-owned port.
var _ cryptids.JWTSigner = (*Signer)(nil)

// Signer signs and verifies JWTs with a fixed HMAC method and shared secret.
// Construct it with New; the zero value is not usable.
type Signer struct {
	secret []byte
	method *jwt.SigningMethodHMAC
}

// Option configures a Signer passed to New.
type Option func(*Signer)

// WithMethod pins the HMAC signing method (HS256, HS384, HS512); the default is
// HS256. Verify rejects any token whose alg header differs from this method, so
// the choice is a security boundary, not just a performance knob. A nil method
// is ignored, leaving the default in place.
func WithMethod(method *jwt.SigningMethodHMAC) Option {
	return func(s *Signer) {
		if method != nil {
			s.method = method
		}
	}
}

// New builds a Signer from a shared secret, defaulting to HS256. It returns
// ErrSecretTooShort when the secret is under 32 bytes.
func New(secret string, opts ...Option) (*Signer, error) {
	if len(secret) < minSecretBytes {
		return nil, ErrSecretTooShort
	}
	s := &Signer{secret: []byte(secret), method: jwt.SigningMethodHS256}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Sign creates a signed token carrying claims plus registered exp and iat
// claims. expiresAt sets exp; iat is the current UTC time. A caller may include
// nbf in claims to make the token not-yet-valid until a future time; Verify
// honors it.
func (s *Signer) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	mapClaims := jwt.MapClaims{
		"exp": expiresAt.Unix(),
		"iat": time.Now().UTC().Unix(),
	}
	for k, v := range claims {
		mapClaims[k] = v
	}
	token := jwt.NewWithClaims(s.method, mapClaims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("golangjwt: sign: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a token, returning its claims when the signature,
// signing method, and time claims (exp, nbf) all check out.
//
// The key function pins token.Method to this Signer's method before the secret
// is ever returned, so a token whose alg header was swapped — to a different
// HMAC variant, an asymmetric algorithm, or none — is rejected before any MAC
// is computed. WithValidMethods repeats the assertion at the parser boundary,
// and WithStrictDecoding rejects non-canonical base64url that would otherwise
// admit padding-bit signature malleability.
func (s *Signer) Verify(tokenString string) (map[string]any, error) {
	if tokenString == "" {
		return nil, ErrEmptyToken
	}

	keyFunc := func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != s.method.Alg() {
			return nil, fmt.Errorf("golangjwt: unexpected signing method %q, want %q", token.Method.Alg(), s.method.Alg())
		}
		return s.secret, nil
	}

	token, err := jwt.Parse(
		tokenString,
		keyFunc,
		jwt.WithValidMethods([]string{s.method.Alg()}),
		jwt.WithStrictDecoding(),
	)
	if err != nil {
		return nil, fmt.Errorf("golangjwt: verify: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("golangjwt: invalid token claims")
	}

	result := make(map[string]any, len(claims))
	for k, v := range claims {
		result[k] = v
	}
	return result, nil
}
