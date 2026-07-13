// Package user is the identity domain: the User aggregate plus the two
// credential-hygiene-separated repository ports. UserRepository owns profile
// reads/writes; PasswordRepository owns credential material (the password hash)
// so a store can guard the password table independently of the users table.
// Other domains reference a user by ID only.
package user

import (
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// User is the identity aggregate — the stable human subject (design §2.1). The
// addresses by which the subject is found or contacted live in the identifier
// domain (user_identifiers); User keeps only the stable subject and profile
// fields plus AuthRevision.
//
// AuthRevision is the optimistic serialization anchor (design §2.1/§5.6,
// users.auth_revision): the identifier ApplyVerifiedChange and the credential
// mutation rail both compare-and-swap on it so a stale, safe-looking method set
// can never win a concurrent mutation. It starts at 0 and increments once per
// applied mutation.
type User struct {
	ID           string
	DisplayName  string
	AuthRevision int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewUser trims the display name, mints an ID from ids (empty under
// cryptids.Database — the store then assigns the key), and returns a new user.
// Identity (the addresses) is carried by the primary identifier the atomic
// CreateWithPrimaryIdentifier persists alongside the user (design §2.2).
func NewUser(ids cryptids.IDGenerator, displayName string, now time.Time) User {
	now = now.UTC()
	return User{
		ID:          ids.MustGenerate(),
		DisplayName: strings.TrimSpace(displayName),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
