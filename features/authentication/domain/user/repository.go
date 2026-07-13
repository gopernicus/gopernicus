package user

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
)

// UserRepository persists user aggregates. Implemented by feature store adapters
// (features/authentication/stores/turso) or any host-provided implementation (see the
// storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - CreateWithPrimaryIdentifier commits the user and its first identifier
//     atomically, or neither: a lost authentication-claim race →
//     sdk.ErrAlreadyExists with no orphan user row.
//   - Get for an unknown id → sdk.ErrNotFound.
//   - Update for an unknown id → sdk.ErrNotFound.
type UserRepository interface {
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
	// Update persists changes to an existing user; missing id → sdk.ErrNotFound.
	Update(ctx context.Context, id string, u User) (User, error)
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
	// Set upserts the password hash for userID.
	Set(ctx context.Context, userID, hash string) error
	// Get returns the stored password hash for userID, or sdk.ErrNotFound.
	Get(ctx context.Context, userID string) (string, error)
}
