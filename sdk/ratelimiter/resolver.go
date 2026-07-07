package ratelimiter

import "context"

// =============================================================================
// Limit Resolver Interface
// =============================================================================

// LimitResolver determines the rate limit for a given subject.
// This is the extension point for future tier/subscription logic.
//
// The default implementation uses hardcoded limits + API key overrides.
// Swap in a DB-backed resolver later for subscription tiers.
type LimitResolver interface {
	// Resolve returns the Limit for the given subject.
	// Implementations should return a sensible default if resolution fails.
	Resolve(ctx context.Context, req ResolveRequest) Limit
}

// ResolveRequest contains information about the request subject.
type ResolveRequest struct {
	// SubjectType is the type of authenticated subject.
	// Values: "user", "service_account", "anonymous"
	SubjectType string

	// SubjectID is the ID of the authenticated subject (user ID or service account ID).
	// Empty for anonymous requests.
	SubjectID string

	// APIKey contains API key info if authenticated via API key.
	// Nil for JWT or anonymous authentication.
	APIKey *APIKeyInfo

	// ClientIP is the client's IP address (for anonymous fallback keying).
	ClientIP string
}

// APIKeyInfo contains rate-limit-relevant API key data.
type APIKeyInfo struct {
	// APIKeyID is the unique identifier of the API key.
	APIKeyID string

	// ServiceAccountID is the service account that owns this key.
	ServiceAccountID string

	// RateLimitPerMinute is the explicit rate limit override from the API key.
	// If non-nil, this takes priority over all other limit sources.
	RateLimitPerMinute *int
}

// =============================================================================
// Default Limit Resolver
// =============================================================================

// DefaultLimitResolver uses hardcoded defaults with API key overrides.
//
// Resolution order:
//  1. API key explicit override (rate_limit_per_minute from schema)
//  2. Default based on subject type (user, service_account, anonymous)
//
// To add subscription tiers later, create a new resolver that wraps this
// one and adds tier lookups before falling back to defaults.
type DefaultLimitResolver struct {
	// UserLimit is the default limit for authenticated users.
	UserLimit Limit

	// ServiceAccountLimit is the default limit for service accounts (via API key).
	ServiceAccountLimit Limit

	// AnonymousLimit is the default limit for unauthenticated requests.
	AnonymousLimit Limit
}

// NewDefaultResolver creates a resolver with sensible defaults.
//
// Default limits:
//   - Users: 100 req/min + 10 burst
//   - Service Accounts: 500 req/min + 50 burst
//   - Anonymous: 60 req/min + 5 burst
func NewDefaultResolver() *DefaultLimitResolver {
	return &DefaultLimitResolver{
		UserLimit:           PerMinute(100).WithBurst(10),
		ServiceAccountLimit: PerMinute(500).WithBurst(50),
		AnonymousLimit:      PerMinute(60).WithBurst(5),
	}
}

// Resolve returns the appropriate rate limit for the request.
func (r *DefaultLimitResolver) Resolve(_ context.Context, req ResolveRequest) Limit {
	// 1. API key explicit override takes priority.
	if req.APIKey != nil && req.APIKey.RateLimitPerMinute != nil {
		return PerMinute(*req.APIKey.RateLimitPerMinute)
	}

	// 2. Use default based on subject type.
	switch req.SubjectType {
	case "user":
		return r.UserLimit
	case "service_account":
		return r.ServiceAccountLimit
	default:
		return r.AnonymousLimit
	}
}
