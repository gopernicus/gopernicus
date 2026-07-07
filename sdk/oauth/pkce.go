package oauth

import (
	"crypto/sha256"
	"encoding/base64"
)

// GenerateCodeChallenge creates the S256 code challenge from a code verifier.
// The challenge is sent in the authorization request; the verifier is sent in
// the token exchange request. The output is base64url without padding, per
// RFC 7636.
func GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
