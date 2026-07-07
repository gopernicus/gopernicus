package user

import "context"

// UserRepository persists user aggregates. Implemented by feature store adapters
// (features/auth/stores/turso) or any host-provided implementation (see the
// storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create whose normalized email collides with an existing user →
//     errs.ErrAlreadyExists. Uniqueness is on the normalized (lowercased) email.
//   - Get / GetByEmail for an unknown id / email → errs.ErrNotFound.
//   - Update for an unknown id → errs.ErrNotFound.
type UserRepository interface {
	// Create persists a new user; colliding email → errs.ErrAlreadyExists.
	Create(ctx context.Context, u User) (User, error)
	// Get returns the user with the given id, or errs.ErrNotFound.
	Get(ctx context.Context, id string) (User, error)
	// GetByEmail returns the user with the given normalized email, or
	// errs.ErrNotFound. Callers pass an already-normalized address.
	GetByEmail(ctx context.Context, email string) (User, error)
	// Update persists changes to an existing user; missing id → errs.ErrNotFound.
	Update(ctx context.Context, id string, u User) (User, error)
}

// PasswordRepository stores credential material (the password hash) keyed by
// user id. It is kept separate from UserRepository — credentials are
// queryable/rotatable independently of general user reads, and a store adapter
// can apply tighter access control to the password table without touching the
// users table.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for a user id with no stored password → errs.ErrNotFound.
//   - Set is an upsert: it creates the hash when absent and replaces it when
//     present, so a password change never collides.
type PasswordRepository interface {
	// Set upserts the password hash for userID.
	Set(ctx context.Context, userID, hash string) error
	// Get returns the stored password hash for userID, or errs.ErrNotFound.
	Get(ctx context.Context, userID string) (string, error)
}
