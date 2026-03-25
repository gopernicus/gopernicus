package cryptids

import "time"

// JWTSigner defines the interface for signing and verifying tokens.
// Implementations (golang-jwt, etc.) live in cryptids/golangjwt/, etc.
//
//	type AuthService struct {
//	    signer cryptids.JWTSigner
//	}
//
//	func (s *AuthService) Login(userID string) (string, error) {
//	    return s.signer.Sign(map[string]any{"user_id": userID}, time.Now().Add(24*time.Hour))
//	}
type JWTSigner interface {
	// Sign creates a signed token with the given claims and expiry.
	Sign(claims map[string]any, expiresAt time.Time) (string, error)

	// Verify parses and validates a token, returning the claims if valid.
	Verify(token string) (map[string]any, error)
}
