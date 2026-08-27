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
// Status is the account lifecycle posture (CHAU-1.1); see status.go. It is
// StatusActive for every user the domain constructs and, after the bundled
// status migration, for every pre-existing row. A store reader may resolve a
// legacy empty column with NormalizeStatus. StatusChangedAt is the last
// transition time; its zero value means the account has never transitioned.
type User struct {
	ID              string
	DisplayName     string
	AuthRevision    int64
	Status          Status
	StatusChangedAt time.Time // zero → never transitioned
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Active reports whether the user may obtain a new session. A zero-value Status
// read from a pre-migration row counts as active (NormalizeStatus), so this is
// safe to call on a user loaded by a store that has not yet been upgraded.
func (u User) Active() bool { return NormalizeStatus(u.Status).Active() }

// NewUser trims the display name, mints an ID from ids (empty under
// cryptids.Database — the store then assigns the key), and returns a new user.
// Identity (the addresses) is carried by the primary identifier the atomic
// CreateWithPrimaryIdentifier persists alongside the user (design §2.2).
func NewUser(ids cryptids.IDGenerator, displayName string, now time.Time) User {
	now = now.UTC()
	return User{
		ID:          ids.MustGenerate(),
		DisplayName: strings.TrimSpace(displayName),
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
