// Package oauth is the facility port for OAuth 2.0 / OpenID Connect providers.
//
// It ships the Provider interface, the shared token/user/claims types, and the
// PKCE S256 helper only — no default implementation. A vendor connector cannot
// operate without a vendor account and its API surface churns on the vendor's
// schedule, not sdk's, so providers are never sdk defaults: concrete
// implementations live in integrations/oauth/* (for example
// integrations/oauth/google and integrations/oauth/github). This mirrors the
// tracing port shape, except oauth needs no Noop — a host that does not wire a
// provider simply has none.
//
// The interface is consumed by an authentication service, which drives the
// authorization-code flow with PKCE and, for OIDC providers, ID token
// validation.
package oauth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Provider drives an authorization-code flow. Host authentication policy belongs
// to the caller; provider evidence does not itself authorize account adoption.
type Provider interface {
	Name() string
	GetAuthorizationURL(AuthorizationRequest) (string, error)
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*TokenResponse, error)
	GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error)
}

// IDTokenValidator is an optional OIDC capability. A flow started with this
// capability must receive and validate an ID token; it must not downgrade to
// userinfo when the token is missing.
type IDTokenValidator interface {
	ValidateIDToken(ctx context.Context, idToken, nonce string) (*IDTokenClaims, error)
}

// TokenRefresher is optional. Whether refresh tokens are requested is host policy.
type TokenRefresher interface {
	RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error)
}

// AuthorizationRequest contains the values bound to one login transaction.
// CodeVerifier remains secret; only its S256 challenge appears in the URL.
type AuthorizationRequest struct {
	State        string
	CodeVerifier string
	Nonce        string
	RedirectURI  string
}

// Validate rejects incomplete requests before a provider URL is constructed.
func (r AuthorizationRequest) Validate() error {
	if strings.TrimSpace(r.State) == "" {
		return fmt.Errorf("oauth: state is required")
	}
	if len(r.CodeVerifier) < 43 || len(r.CodeVerifier) > 128 {
		return fmt.Errorf("oauth: PKCE verifier must contain 43 to 128 unreserved characters")
	}
	for _, c := range r.CodeVerifier {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return fmt.Errorf("oauth: invalid PKCE verifier")
		}
	}
	u, err := url.Parse(r.RedirectURI)
	if err != nil || u.Scheme == "" || u.Fragment != "" || strings.Contains(r.RedirectURI, "#") || u.User != nil ||
		((u.Scheme == "http" || u.Scheme == "https") && u.Hostname() == "") || (u.Host == "" && u.Path == "" && u.Opaque == "") {
		return fmt.Errorf("oauth: redirect URI must be absolute and contain no credentials or fragment")
	}
	return nil
}

// TokenResponse is returned by Provider.ExchangeCode and TokenRefresher.RefreshToken.
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
	// EmailAuthoritative reports whether the provider controls this mailbox
	// namespace. It is evidence for host policy, not permission to adopt an email.
	EmailAuthoritative bool
	Name               string // may be empty
	Picture            string // may be empty
}

// IDTokenClaims are extracted from a validated OIDC ID token.
type IDTokenClaims struct {
	Subject            string
	Email              string
	EmailVerified      bool
	EmailAuthoritative bool
	Name               string // may be empty
	Picture            string // may be empty
	Nonce              string
}

// Validate checks the protocol fields required before a token is used or stored.
func (t *TokenResponse) Validate() error {
	if t == nil || strings.TrimSpace(t.AccessToken) == "" || strings.TrimSpace(t.TokenType) == "" || t.ExpiresIn < 0 {
		return fmt.Errorf("oauth: invalid token response")
	}
	return nil
}

// Validate requires a stable opaque subject. Email is optional: an already
// linked identity can authenticate without exposing an email address.
func (u *UserInfo) Validate() error {
	if u == nil || strings.TrimSpace(u.ProviderUserID) == "" {
		return fmt.Errorf("oauth: missing provider user ID")
	}
	return nil
}

// Error describes a provider operation without reflecting remote bodies,
// descriptions, URLs or credentials in Error(). Code is the remote OAuth error
// code for programmatic inspection; treat it and the unwrapped cause as untrusted.
// StatusCode is zero when no HTTP response was received.
type Error struct {
	Provider   string
	Operation  string
	StatusCode int
	Code       string
	Cause      error
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("oauth %s: %s failed (HTTP %d)", e.Provider, e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("oauth %s: %s failed", e.Provider, e.Operation)
}

func (e *Error) Unwrap() error { return e.Cause }
