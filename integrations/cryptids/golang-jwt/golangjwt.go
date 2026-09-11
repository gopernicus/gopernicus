// Package golangjwt is a stateless-token connector wrapping exactly one
// third-party library, github.com/golang-jwt/jwt/v5. Its Signer structurally
// satisfies the sdk-owned cryptids.JWTSigner port, signing and verifying
// HMAC-SHA JSON Web Tokens from a shared secret.
//
// It owns "how to sign a token with golang-jwt," never any pocket's
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
	"crypto"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// Limit NumericDates to calendar years 0001–9999. This avoids float-to-int and
// time.Time overflow in the library's conversion of extreme JSON numbers.
const (
	minNumericDate = -62135596800 // 0001-01-01T00:00:00Z, inclusive.
	maxNumericDate = 253402300800 // 10000-01-01T00:00:00Z, exclusive.
)

var (
	// ErrSecretTooShort is returned by New when the secret is shorter than
	// the selected method's hash output: 32, 48, or 64 bytes.
	ErrSecretTooShort = errors.New("golangjwt: secret is too short")

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

// Option configures construction of a Signer. Options apply in order.
type Option func(*config)

type config struct {
	method *jwt.SigningMethodHMAC
}

// WithMethod pins the HMAC signing method (HS256, HS384, HS512); the default is
// HS256. Verify rejects any token whose alg header differs from this method, so
// the choice is a security boundary, not just a performance knob. A nil method
// is ignored, leaving the current selection in place. The option snapshots the
// method so later caller changes cannot alter reused construction settings.
func WithMethod(method *jwt.SigningMethodHMAC) Option {
	if method == nil {
		return func(*config) {}
	}
	snapshot := *method
	return func(cfg *config) { cfg.method = &snapshot }
}

// New builds a Signer from a shared secret, defaulting to HS256. The string's
// bytes are used directly, without decoding. HS256, HS384, and HS512 require
// at least 32, 48, and 64 bytes respectively; shorter keys return ErrSecretTooShort.
// A nil option returns an error wrapping sdk.ErrInvalidInput.
func New(secret string, opts ...Option) (*Signer, error) {
	cfg := config{method: jwt.SigningMethodHS256}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("golangjwt: nil Option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.method == nil {
		return nil, errors.New("golangjwt: signing method is required")
	}
	var minSecretBytes int
	switch *cfg.method {
	case jwt.SigningMethodHMAC{Name: "HS256", Hash: crypto.SHA256}:
		minSecretBytes = 32
	case jwt.SigningMethodHMAC{Name: "HS384", Hash: crypto.SHA384}:
		minSecretBytes = 48
	case jwt.SigningMethodHMAC{Name: "HS512", Hash: crypto.SHA512}:
		minSecretBytes = 64
	default:
		return nil, fmt.Errorf("golangjwt: unsupported signing method %q", cfg.method.Alg())
	}
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("%w for %s: need at least %d bytes", ErrSecretTooShort, cfg.method.Alg(), minSecretBytes)
	}
	// Options can supply a caller-owned method; keep the validated configuration.
	method := *cfg.method
	return &Signer{secret: []byte(secret), method: &method}, nil
}

// Sign creates a signed token carrying claims plus registered exp and iat
// claims. expiresAt sets exp; iat is the current UTC time. These overwrite any
// caller-supplied exp or iat without changing the caller's map. A caller may
// include nbf in claims; Verify honors it.
func (s *Signer) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	if s == nil || s.method == nil || len(s.secret) == 0 {
		return "", errors.New("golangjwt: signer must be created with New")
	}
	mapClaims := make(jwt.MapClaims, len(claims)+2)
	for k, v := range claims {
		mapClaims[k] = v
	}
	mapClaims["exp"] = expiresAt.Unix()
	mapClaims["iat"] = time.Now().UTC().Unix()
	token := jwt.NewWithClaims(s.method, mapClaims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("golangjwt: sign: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a token, returning its claims when the signature,
// signing method, and time claims all check out. Claims must be a JSON object
// with a numeric exp. Optional nbf and iat must also be numeric; explicit null
// is invalid. Dates must be in calendar years 0001–9999. All three time checks
// allow 60 seconds of clock skew.
//
// The key function pins token.Method to this Signer's method before the secret
// is ever returned, so a token whose alg header was swapped — to a different
// HMAC variant, an asymmetric algorithm, or none — is rejected before any MAC
// is computed. WithValidMethods repeats the assertion at the parser boundary,
// and WithStrictDecoding rejects non-canonical base64url that would otherwise
// admit padding-bit signature malleability.
func (s *Signer) Verify(tokenString string) (map[string]any, error) {
	if s == nil || s.method == nil || len(s.secret) == 0 {
		return nil, errors.New("golangjwt: signer must be created with New")
	}
	if tokenString == "" {
		return nil, ErrEmptyToken
	}

	keyFunc := func(token *jwt.Token) (any, error) {
		method, ok := token.Method.(*jwt.SigningMethodHMAC)
		if !ok || *method != *s.method {
			return nil, fmt.Errorf("golangjwt: unexpected signing method %q, want %q", token.Method.Alg(), s.method.Alg())
		}
		return s.secret, nil
	}

	token, err := jwt.Parse(
		tokenString,
		keyFunc,
		jwt.WithValidMethods([]string{s.method.Alg()}),
		jwt.WithStrictDecoding(),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("golangjwt: verify: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("golangjwt: invalid token claims")
	}
	for _, name := range []string{"exp", "nbf", "iat"} {
		value, present := claims[name].(float64)
		if present && (value < minNumericDate || value >= maxNumericDate) {
			return nil, fmt.Errorf("golangjwt: verify: %w: %s outside calendar years 0001–9999", jwt.ErrTokenInvalidClaims, name)
		}
	}

	result := make(map[string]any, len(claims))
	for k, v := range claims {
		result[k] = v
	}
	return result, nil
}
