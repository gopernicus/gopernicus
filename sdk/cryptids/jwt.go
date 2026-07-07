package cryptids

import "time"

// JWTSigner defines signing and verifying stateless tokens carrying a claims
// map.
//
// This is a PORT ONLY. No default implementation ships in the kernel: signing
// requires a third-party library (golang-jwt), so the implementation lives in
// its own integrations/cryptids/golang-jwt module, which structurally satisfies
// this interface — sdk stays dependency-free.
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
