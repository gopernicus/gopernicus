package oauthaccount

import (
	"context"
	"time"
)

// OAuthAccountRepository persists user↔provider links. Implemented by pocket
// store adapters (pockets/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create whose (Provider, ProviderUserID) collides with an existing link →
//     sdk.ErrAlreadyExists (a provider identity belongs to at most one local
//     user; no upsert).
//   - GetByProvider for an unknown (provider, providerUserID) → sdk.ErrNotFound.
//   - Delete for a (userID, provider) with no link → sdk.ErrNotFound.
type OAuthAccountRepository interface {
	// Link conditionally attaches a provider identity to an active user and returns
	// the incremented auth_revision. For adoption, adoptIdentifierID must name the
	// same user's active login/recovery email captured as unverified at flow start.
	// The transaction verifies it, removes the old password and revokes sessions,
	// grants and reset challenges.
	// A revision mismatch returns sdk.ErrConflict without changing any state.
	Link(ctx context.Context, a OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (OAuthAccount, int64, error)
	// Create persists a new link for an existing active user; colliding
	// (Provider, ProviderUserID) returns sdk.ErrAlreadyExists.
	Create(ctx context.Context, a OAuthAccount) (OAuthAccount, error)
	// GetByProvider returns the link for a provider identity, or
	// sdk.ErrNotFound.
	GetByProvider(ctx context.Context, provider, providerUserID string) (OAuthAccount, error)
	// ListByUser returns every link owned by userID (empty slice, nil error when
	// none).
	ListByUser(ctx context.Context, userID string) ([]OAuthAccount, error)
	// Delete removes the active user's link to provider, advances auth_revision,
	// and revokes sessions, grants and reset challenges atomically. No link
	// returns sdk.ErrNotFound. Hosts should use credential mutations when the
	// remaining login methods must satisfy the host's removal policy.
	Delete(ctx context.Context, userID, provider string) error
}
