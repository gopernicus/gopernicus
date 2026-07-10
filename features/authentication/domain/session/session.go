// Package session is the server-side session domain: an opaque, cookie-delivered
// token that identifies an authenticated user until it expires. Sessions carry
// no claims; all state is server-side (no JWT in v1).
package session

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// secrets mints session tokens with the default nanoid shape. Deliberately NOT
// the app's entity-ID strategy (amended D9): a token is a credential, and its
// entropy must never follow a wiring choice like cryptids.Database.
var secrets = cryptids.IDGenerator{}

// Session is an opaque server-side session. Token is the stored value (the
// SHA-256 hash of the cookie value): the auth service hashes the minted cookie
// before persisting and returns the plaintext cookie separately at mint (design
// §7.3), so a leaked session row cannot be replayed as a cookie.
type Session struct {
	Token     string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewSession mints a session for userID that expires ttl after now. The token
// is an opaque random value (sdk/foundation/cryptids), never derived from the user's identity;
// the auth service replaces it with the token's hash before persisting (design
// §7.3), keeping the plaintext for the cookie only.
func NewSession(userID string, ttl time.Duration, now time.Time) Session {
	now = now.UTC()
	return Session{
		Token:     secrets.MustGenerate(),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

// Expired reports whether the session is at or past its expiry at now.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
