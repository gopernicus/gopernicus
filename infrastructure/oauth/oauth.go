// Package oauth defines the Provider interface for OAuth 2.0 / OIDC providers.
//
// The interface is consumed by [core/auth/authentication.Authenticator].
// Implementations live in subdirectories:
//   - googleoauth: Google OAuth 2.0 + OIDC (with cryptographic ID token verification)
//   - githuboauth: GitHub OAuth 2.0
package oauth

import "context"

// Provider is the interface that OAuth providers implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "google", "github").
	Name() string

	// SupportsOIDC returns true if the provider supports OpenID Connect
	// (ID token validation). Providers that don't support OIDC must use
	// [GetUserInfo] instead of [ValidateIDToken].
	SupportsOIDC() bool

	// TrustEmailVerification returns true if the provider's email verification
	// claims should be trusted for setting AccountVerified on OAuth accounts.
	// Typically true for major providers (Google, GitHub) with reliable email
	// verification. Set to false for providers where email verification claims
	// may not be trustworthy.
	TrustEmailVerification() bool

	// GetAuthorizationURL builds the URL to redirect the user to for authorization.
	GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string

	// ExchangeCode exchanges an authorization code for tokens.
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*TokenResponse, error)

	// GetUserInfo fetches the user's profile from the provider's API.
	GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error)

	// ValidateIDToken validates an OIDC ID token and extracts claims.
	// Returns an error for providers that don't support OIDC.
	ValidateIDToken(ctx context.Context, idToken, nonce string) (*IDTokenClaims, error)

	// RefreshToken exchanges a refresh token for new tokens.
	// Returns an error for providers that don't support token refresh.
	RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error)
}

// TokenResponse is returned by [Provider.ExchangeCode] and [Provider.RefreshToken].
type TokenResponse struct {
	AccessToken  string
	RefreshToken string // empty if not provided
	ExpiresIn    int    // seconds
	IDToken      string // empty if not an OIDC provider
	TokenType    string
	Scopes       string
}

// UserInfo is the user's profile as returned by the provider's API.
type UserInfo struct {
	ProviderUserID string
	Email          string
	EmailVerified  bool
	Name           string // may be empty
	Picture        string // may be empty
}

// IDTokenClaims are extracted from a validated OIDC ID token.
type IDTokenClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string // may be empty
	Picture       string // may be empty
	Nonce         string
}

// ProviderConfig holds the credentials and endpoints for an OAuth provider.
type ProviderConfig struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	JWKSURL      string // for OIDC ID token validation
}
