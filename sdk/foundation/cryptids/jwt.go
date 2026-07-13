package cryptids

import "time"

// JWTSigner defines signing and verifying stateless tokens carrying a claims
// map.
//
// The in-package default is HS256, a stdlib-only HMAC-SHA256 signer — the
// zero-integration default. Asymmetric schemes (RS256/ES256) need a third-party
// library and live in integrations/cryptids/golang-jwt, which structurally
// satisfies this interface, keeping sdk dependency-free.
//
//	type AuthenticationService struct {
//	    signer cryptids.JWTSigner
//	}
//
//	func (s *AuthenticationService) Login(userID string) (string, error) {
//	    return s.signer.Sign(map[string]any{"user_id": userID}, time.Now().Add(24*time.Hour))
//	}
type JWTSigner interface {
	// Sign creates a signed token with the given claims and expiry.
	Sign(claims map[string]any, expiresAt time.Time) (string, error)

	// Verify parses and validates a token, returning the claims if valid.
	Verify(token string) (map[string]any, error)
}
