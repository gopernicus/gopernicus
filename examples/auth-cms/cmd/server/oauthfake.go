package main

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/oauth"
)

// fakeOAuthProvider is a host-local, self-contained sdk/oauth.Provider — NO
// external vendor and no network. It exists so the A9 OAuth leg runs end to end
// with zero infra: it derives a STABLE provider identity from the authorization
// `code`, so re-running the flow with the same code lands on the login path (no
// duplicate account), and a fresh code is a fresh identity (register path). It
// speaks no OIDC, so the auth service reads the identity through GetUserInfo.
//
// This is deliberately not an integrations/oauth/* connector: it isolates no
// real vendor API. A real host swaps in integrations/oauth/google or /github via
// the same auth.Config.Providers slot.
type fakeOAuthProvider struct{}

var _ oauth.Provider = fakeOAuthProvider{}

// Name is the provider key in the /auth/oauth/{provider}/* routes.
func (fakeOAuthProvider) Name() string { return "fake" }

// SupportsOIDC is false: the identity is read from GetUserInfo, not an ID token.
func (fakeOAuthProvider) SupportsOIDC() bool { return false }

// TrustEmailVerification is true, so a first-seen identity's verified-email claim
// marks the newly registered user verified — the OAuth-registered user can then
// pass the host's RequireVerifiedEmail login gate.
func (fakeOAuthProvider) TrustEmailVerification() bool { return true }

// GetAuthorizationURL builds a fake authorize URL that surfaces the state and the
// S256 PKCE challenge as query params (so the A9 transcript can note them in the
// 302 Location), plus a suggested `code` the operator can pass straight to the
// callback. Any code works — the identity is derived from it.
func (fakeOAuthProvider) GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string {
	q := url.Values{}
	q.Set("state", state)
	q.Set("code_challenge", oauth.GenerateCodeChallenge(codeVerifier))
	q.Set("code_challenge_method", "S256")
	q.Set("redirect_uri", redirectURI)
	q.Set("code", "oauth-user@fake.local")
	return "https://oauth.fake.local/authorize?" + q.Encode()
}

// ExchangeCode returns a token that carries the authorization code forward as its
// access token, so GetUserInfo can derive a stable identity from it. There is no
// real exchange — the verifier is accepted as-is.
func (fakeOAuthProvider) ExchangeCode(_ context.Context, code, _ /*codeVerifier*/, _ /*redirectURI*/ string) (*oauth.TokenResponse, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("fake oauth: empty authorization code")
	}
	return &oauth.TokenResponse{
		AccessToken: code,
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scopes:      "email",
	}, nil
}

// GetUserInfo derives a deterministic identity from the access token (the code):
// the same code always yields the same ProviderUserID + Email, so a repeat flow
// is a login, never a duplicate registration.
func (fakeOAuthProvider) GetUserInfo(_ context.Context, accessToken string) (*oauth.UserInfo, error) {
	email := strings.TrimSpace(accessToken)
	if email == "" {
		return nil, errors.New("fake oauth: empty access token")
	}
	if !strings.Contains(email, "@") {
		email += "@fake.local"
	}
	return &oauth.UserInfo{
		ProviderUserID: "fake|" + email,
		Email:          email,
		EmailVerified:  true,
	}, nil
}

// ValidateIDToken is unsupported (no OIDC).
func (fakeOAuthProvider) ValidateIDToken(context.Context, string, string) (*oauth.IDTokenClaims, error) {
	return nil, errors.New("fake oauth: OIDC not supported")
}

// RefreshToken is unsupported.
func (fakeOAuthProvider) RefreshToken(context.Context, string) (*oauth.TokenResponse, error) {
	return nil, errors.New("fake oauth: refresh not supported")
}
