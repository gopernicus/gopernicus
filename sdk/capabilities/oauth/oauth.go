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
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// ErrDevelopmentOnlyProvider is returned by CheckProvider in
// environment.ModeProduction for a provider that declares itself
// development-only. The returned error wraps it with the provider's name.
var ErrDevelopmentOnlyProvider = errors.New("oauth: production rejects a development-only provider")

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

// CapabilityReporter is the optional interface a Provider implements to declare
// its production posture. Consumers detect it with a type assertion, so any
// provider, bundled or host-owned, can opt in.
type CapabilityReporter interface {
	Capabilities() Capabilities
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

// Capabilities is the posture metadata a Provider declares.
type Capabilities struct {
	// DevelopmentOnly marks a provider that must never run in production, such
	// as a fake that signs in whoever asks. CheckProvider refuses it there.
	DevelopmentOnly bool
}

// ProviderPosture is what CheckProvider returns: what the provider declared,
// separate from whether that is acceptable for a mode. A development caller
// uses it to phrase its own warning; this package takes no logger.
type ProviderPosture struct {
	// Declared reports whether the provider implements CapabilityReporter.
	Declared bool
	// Capabilities is what the provider declared; the zero value when Declared
	// is false.
	Capabilities Capabilities
}

// CheckProvider validates p against mode and returns the provider's declared
// posture either way. In environment.ModeProduction a provider that declares
// DevelopmentOnly is rejected with an error wrapping ErrDevelopmentOnlyProvider;
// in environment.ModeDevelopment it is accepted and the posture tells the
// caller to warn.
//
// A provider that declares nothing is ACCEPTED in every mode. This is the
// deliberate opposite of notify.CheckTransport, which refuses an undeclared
// transport: providers predate the hook, so refusing them would break every
// production host, while a development-only provider is the one shape known
// to be unsafe. A nil provider declares nothing.
//
// An invalid or empty mode is rejected with the environment package's own
// validation error rather than defaulting to a posture the host did not choose.
func CheckProvider(mode environment.Mode, p Provider) (ProviderPosture, error) {
	posture := inspectProvider(p)
	if err := environment.ValidateMode(mode); err != nil {
		return posture, err
	}
	if mode.IsProduction() && posture.Capabilities.DevelopmentOnly {
		return posture, fmt.Errorf("%w: %q", ErrDevelopmentOnlyProvider, p.Name())
	}
	return posture, nil
}

// inspectProvider reports what p declares, treating a nil or typed-nil
// provider as declaring nothing.
func inspectProvider(p Provider) ProviderPosture {
	if p == nil {
		return ProviderPosture{}
	}
	v := reflect.ValueOf(p)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return ProviderPosture{}
		}
	}
	r, ok := p.(CapabilityReporter)
	if !ok {
		return ProviderPosture{}
	}
	return ProviderPosture{Declared: true, Capabilities: r.Capabilities()}
}
