package cryptids

import (
	"context"
	"time"
)

// JWTSigner signs and verifies expiring JSON Web Tokens. Hosts provide the
// implementation; integrations/cryptids/golang-jwt supports HMAC signing.
// Application-specific required claims and issuer/audience restrictions belong
// to the consumer's token profile.
type JWTSigner interface {
	// Sign copies custom claims, sets exp from expiresAt and iat from the current
	// time, and signs the result. The signer owns exp and iat even if claims
	// contains them. It does not mutate the caller's map.
	Sign(claims map[string]any, expiresAt time.Time) (string, error)

	// Verify requires a valid signature with the configured algorithm, strictly
	// decoded base64url, and a JSON object of claims. exp must be a NumericDate;
	// optional nbf and iat must also be NumericDates when present. It rejects
	// expired, not-yet-valid, and future-issued tokens with 60 seconds of clock
	// tolerance. Missing exp and explicit null time claims are invalid.
	// NumericDates must fall within calendar years 0001–9999.
	Verify(token string) (map[string]any, error)
}

// JWTVerifier verifies a bearer JWT issued by a party that is not this process:
// signature against the issuer's published keys, exp/nbf/iat with 60 seconds of
// clock tolerance, iss and aud pinned at construction, and an algorithm allow-list
// that never admits "none" or an HMAC method unless explicitly configured. Verify
// performs I/O (key discovery and refresh), hence ctx. It returns the verified
// claims; claim mapping and subject resolution belong to the consumer. It is
// distinct from oauth.IDTokenValidator, which is nonce-bound and lives in the login flow.
//
// An error wrapping sdk.ErrUnavailable means the issuer's keys could not be
// obtained (network, upstream failure, deadline) and says nothing about the
// token; every other error rejects the token.
type JWTVerifier interface {
	Verify(ctx context.Context, token string) (map[string]any, error)
}
