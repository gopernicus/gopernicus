package oauthaccount

import "context"

// OAuthAccountRepository persists user↔provider links. Implemented by feature
// store adapters (features/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create whose (Provider, ProviderUserID) collides with an existing link →
//     errs.ErrAlreadyExists (a provider identity belongs to at most one local
//     user; no upsert).
//   - GetByProvider for an unknown (provider, providerUserID) → errs.ErrNotFound.
//   - Delete for a (userID, provider) with no link → errs.ErrNotFound.
type OAuthAccountRepository interface {
	// Create persists a new link; colliding (Provider, ProviderUserID) →
	// errs.ErrAlreadyExists.
	Create(ctx context.Context, a OAuthAccount) (OAuthAccount, error)
	// GetByProvider returns the link for a provider identity, or
	// errs.ErrNotFound.
	GetByProvider(ctx context.Context, provider, providerUserID string) (OAuthAccount, error)
	// ListByUser returns every link owned by userID (empty slice, nil error when
	// none).
	ListByUser(ctx context.Context, userID string) ([]OAuthAccount, error)
	// Delete removes userID's link to provider; no such link → errs.ErrNotFound.
	Delete(ctx context.Context, userID, provider string) error
}
