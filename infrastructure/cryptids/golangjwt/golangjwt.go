// Package golangjwt provides a JWTSigner implementation using
// github.com/golang-jwt/jwt/v5 with HMAC-SHA256 signing.
//
// This is an adapter — it implements cryptids.JWTSigner. If golang-jwt
// is ever deprecated, write a new adapter and swap at the composition root.
// No other code changes needed.
//
// Example:
//
//	signer, err := golangjwt.NewSigner("my-secret-at-least-32-chars-long")
//
//	token, err := signer.Sign(map[string]any{
//	    "user_id": "u_abc123",
//	    "role":    "admin",
//	}, time.Now().Add(24 * time.Hour))
//
//	claims, err := signer.Verify(token)
package golangjwt

import (
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	gojwt "github.com/golang-jwt/jwt/v5"
)

var _ cryptids.JWTSigner = (*Signer)(nil)

// Signer implements cryptids.JWTSigner using golang-jwt with HMAC-SHA256.
type Signer struct {
	secret []byte
}

// Options holds configuration for the JWT signer.
// JWT_SECRET is required — no default is provided for security.
type Options struct {
	Secret string `env:"JWT_SECRET" required:"true"`
}

// New creates a JWT signer from Options (typically parsed from environment).
// Returns an error if the secret is shorter than 32 bytes (256 bits),
// the NIST minimum for HMAC-SHA256.
func New(opts Options) (*Signer, error) {
	if len(opts.Secret) < 32 {
		return nil, fmt.Errorf("golangjwt: secret must be at least 32 bytes (got %d)", len(opts.Secret))
	}
	return &Signer{secret: []byte(opts.Secret)}, nil
}

// NewSigner creates a JWT signer with the given secret directly.
// Returns an error if the secret is shorter than 32 bytes.
func NewSigner(secret string) (*Signer, error) {
	return New(Options{Secret: secret})
}

// Sign creates a signed JWT with the given claims and expiry.
// Standard claims (exp, iat) are added automatically.
func (s *Signer) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	mapClaims := gojwt.MapClaims{
		"exp": expiresAt.Unix(),
		"iat": time.Now().UTC().Unix(),
	}
	for k, v := range claims {
		mapClaims[k] = v
	}

	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, mapClaims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("golangjwt: failed to sign: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a JWT, returning the claims map if valid.
// Returns an error if the token is expired, malformed, or signed with a
// different secret.
func (s *Signer) Verify(tokenString string) (map[string]any, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("golangjwt: token cannot be empty")
	}

	token, err := gojwt.Parse(tokenString, func(token *gojwt.Token) (any, error) {
		if _, ok := token.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("golangjwt: %w", err)
	}

	claims, ok := token.Claims.(gojwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("golangjwt: invalid token claims")
	}

	result := make(map[string]any, len(claims))
	for k, v := range claims {
		result[k] = v
	}
	return result, nil
}
