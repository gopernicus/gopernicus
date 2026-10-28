package user

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
)

// InitialCredentials belongs to the initial user transaction. A host may omit all
// values for deliberate passwordless provisioning. OAuth and External are linked
// to the new user by the store; their supplied UserID is ignored. A store must
// persist External: the external engine re-reads it after Provision and fails
// closed when it is absent.
type InitialCredentials struct {
	PasswordHash string
	OAuth        *oauthaccount.OAuthAccount
	External     *externalsubject.ExternalSubject
}

// UserRepository persists user aggregates. Implemented by pocket store adapters
// (pockets/authentication/stores/turso) or any host-provided implementation (see the
// storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - CreateWithPrimaryIdentifier commits the user and its first identifier
//     atomically, or neither: a lost authentication-claim race →
//     sdk.ErrAlreadyExists with no orphan user row.
//   - Get for an unknown id → sdk.ErrNotFound.
//   - Update for an unknown id → sdk.ErrNotFound.
type UserRepository interface {
	// Provision commits the user, first identifier and initial credentials together.
	// Any identifier, user or provider-identity collision leaves all of them absent.
	Provision(ctx context.Context, u User, ident identifier.Identifier, credentials InitialCredentials) (User, identifier.Identifier, error)
	// CreateWithPrimaryIdentifier persists a new user together with its first
	// identifier in one atomic operation (design §2.2): both commit or neither
	// does. ident may arrive with an empty UserID — the store links it to the
	// newly created user in the same transaction — and an empty ID under the
	// greenfield DB-generated convention. A lost authentication-claim race (the
	// identifier's login/recovery value already claimed) → sdk.ErrAlreadyExists
	// and the user is NOT created. It returns the persisted user and identifier.
	CreateWithPrimaryIdentifier(ctx context.Context, u User, ident identifier.Identifier) (User, identifier.Identifier, error)
	// Get returns the user with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (User, error)
	// Update persists DisplayName and UpdatedAt only, preserving credential
	// revision and lifecycle state. A missing id returns sdk.ErrNotFound.
	Update(ctx context.Context, id string, u User) (User, error)
}

// PasswordChange is a conditional credential write. ExpectedHash is empty only
// when installing an initial password. The revision and hash must both match.
type PasswordChange struct {
	ExpectedAuthRevision int64
	ExpectedHash         string
	NewHash              string
	Now                  time.Time
}

// PasswordRepository stores credential material (the password hash) keyed by
// user id. It is kept separate from UserRepository — credentials are
// queryable/rotatable independently of general user reads, and a store adapter
// can apply tighter access control to the password table without touching the
// users table.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for a user id with no stored password → sdk.ErrNotFound.
//   - Set is an upsert: it creates the hash when absent and replaces it when
//     present, so a password change never collides.
type PasswordRepository interface {
	// Change atomically checks the active user's revision and current hash, writes
	// NewHash, increments auth_revision, and revokes every session, grant and
	// password-reset challenge. A stale expectation returns sdk.ErrConflict with
	// no side effects. The returned revision can fence the caller's new session.
	Change(ctx context.Context, userID string, change PasswordChange) (int64, error)
	// Set is a trusted host write. It also increments auth_revision and atomically
	// revokes sessions, grants and password-reset challenges; it never bypasses
	// the credential fence. Use Change for a mutation based on caller proof.
	Set(ctx context.Context, userID, hash string) error
	// Get returns the stored password hash for userID, or sdk.ErrNotFound.
	Get(ctx context.Context, userID string) (string, error)
}

// PasswordHashUpgrader is the optional PasswordRepository capability the
// service uses to re-hash a password at a higher work factor after a
// successful verification. UpgradeHash replaces the stored hash with newHash
// only while it still equals oldHash (compare-and-swap) and reports whether it
// did. Unlike Set and Change it is not a credential change: it must NOT bump
// auth_revision or revoke sessions, grants or challenges, since both hashes
// encode the same password. A store without it is never re-hashed.
type PasswordHashUpgrader interface {
	UpgradeHash(ctx context.Context, userID, oldHash, newHash string) (bool, error)
}
