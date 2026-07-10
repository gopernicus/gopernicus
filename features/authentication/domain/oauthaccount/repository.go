package oauthaccount

import "context"

// OAuthAccountRepository persists user↔provider links. Implemented by feature
// store adapters (features/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create whose (Provider, ProviderUserID) collides with an existing link →
//     sdk.ErrAlreadyExists (a provider identity belongs to at most one local
//     user; no upsert).
//   - GetByProvider for an unknown (provider, providerUserID) → sdk.ErrNotFound.
//   - Delete for a (userID, provider) with no link → sdk.ErrNotFound.
type OAuthAccountRepository interface {
	// Create persists a new link; colliding (Provider, ProviderUserID) →
	// sdk.ErrAlreadyExists.
	Create(ctx context.Context, a OAuthAccount) (OAuthAccount, error)
	// GetByProvider returns the link for a provider identity, or
	// sdk.ErrNotFound.
	GetByProvider(ctx context.Context, provider, providerUserID string) (OAuthAccount, error)
	// ListByUser returns every link owned by userID (empty slice, nil error when
	// none).
	ListByUser(ctx context.Context, userID string) ([]OAuthAccount, error)
	// Delete removes userID's link to provider; no such link → sdk.ErrNotFound.
	Delete(ctx context.Context, userID, provider string) error
}
