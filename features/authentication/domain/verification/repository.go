package verification

import "context"

// CodeRepository persists email-verification codes keyed by code value.
// Implemented by feature store adapters (features/authentication/stores/turso) or any
// host-provided implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown code → sdk.ErrNotFound.
//   - Get for a code past its ExpiresAt → sdk.ErrExpired.
//   - Delete for an unknown code → sdk.ErrNotFound.
type CodeRepository interface {
	// Create persists a new verification code.
	Create(ctx context.Context, c Code) (Code, error)
	// Get returns the live code: unknown → sdk.ErrNotFound, expired →
	// sdk.ErrExpired.
	Get(ctx context.Context, code string) (Code, error)
	// Delete removes the code; unknown → sdk.ErrNotFound.
	Delete(ctx context.Context, code string) error
}

// TokenRepository persists password-reset tokens keyed by token value.
// Implemented by feature store adapters (features/authentication/stores/turso) or any
// host-provided implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown token → sdk.ErrNotFound.
//   - Get for a token past its ExpiresAt → sdk.ErrExpired.
//   - Delete for an unknown token → sdk.ErrNotFound.
type TokenRepository interface {
	// Create persists a new reset token.
	Create(ctx context.Context, t Token) (Token, error)
	// Get returns the live token: unknown → sdk.ErrNotFound, expired →
	// sdk.ErrExpired.
	Get(ctx context.Context, token string) (Token, error)
	// Delete removes the token; unknown → sdk.ErrNotFound.
	Delete(ctx context.Context, token string) error
}
