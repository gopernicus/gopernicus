// Package verification is the domain for the two time-bound secrets auth issues:
// a short-lived Code (email verification) and a longer-lived Token (password
// reset). Both are opaque random values (sdk/id) tied to a user and an expiry;
// they are separate entities with separate ports because they have different
// lifetimes and different store-level access needs.
package verification

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/id"
)

// Code is a short-lived email-verification secret tied to a user.
type Code struct {
	Code      string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewCode mints an email-verification code for userID that expires ttl after
// now. The code is an opaque random value (sdk/id).
func NewCode(userID string, ttl time.Duration, now time.Time) Code {
	now = now.UTC()
	return Code{
		Code:      id.New(),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

// Expired reports whether the code is at or past its expiry at now.
func (c Code) Expired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}

// Token is a longer-lived password-reset secret tied to a user.
type Token struct {
	Token     string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewToken mints a password-reset token for userID that expires ttl after now.
// The token is an opaque random value (sdk/id).
func NewToken(userID string, ttl time.Duration, now time.Time) Token {
	now = now.UTC()
	return Token{
		Token:     id.New(),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

// Expired reports whether the token is at or past its expiry at now.
func (t Token) Expired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}
