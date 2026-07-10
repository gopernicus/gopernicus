// Package google implements the sdk/capabilities/oauth.Provider port for Google's OAuth 2.0
// + OpenID Connect endpoints. It wraps exactly one third-party library —
// github.com/coreos/go-oidc/v3 — used solely for cryptographic ID token
// verification (OIDC discovery, JWKS fetch/cache, RS256 signature validation).
// The authorization-code, token-exchange, refresh, and userinfo flows are
// hand-rolled on net/http; there is no x/oauth2 dependency. It imports sdk/capabilities/oauth
// for the port vocabulary and no feature or other integration.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

// maxResponseBody caps HTTP response bodies read from Google's token and
// userinfo endpoints, guarding against OOM from a malicious or broken endpoint.
const maxResponseBody = 1 << 20 // 1 MB

// Google's production issuer and endpoints. Discovery runs against issuerURL;
// the token/userinfo/authorization endpoints are used directly (Google's
// discovery document advertises the same values).
const (
	issuerURL      = "https://accounts.google.com"
	authURL        = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL       = "https://oauth2.googleapis.com/token"
	userInfoURL    = "https://openidconnect.googleapis.com/v1/userinfo"
	jwksURL        = "https://www.googleapis.com/oauth2/v3/certs"
	defaultTimeout = 30 * time.Second
)

// Compile-time assertion that Provider satisfies the port.
var _ oauth.Provider = (*Provider)(nil)

// endpoints groups the OAuth/OIDC URLs the provider talks to. Production uses
// Google's real values (see googleEndpoints); tests point them at httptest
// fakes so the whole flow stays hermetic.
type endpoints struct {
	issuer      string
	authURL     string
	tokenURL    string
	userInfoURL string
	jwksURL     string
}

func googleEndpoints() endpoints {
	return endpoints{
		issuer:      issuerURL,
		authURL:     authURL,
		tokenURL:    tokenURL,
		userInfoURL: userInfoURL,
		jwksURL:     jwksURL,
	}
}

// Provider implements oauth.Provider for Google with cryptographic ID token
// verification via JWKS.
type Provider struct {
	config    oauth.ProviderConfig
	endpoints endpoints
	client    *http.Client
	verifier  *oidc.IDTokenVerifier
}

// New creates a Google OAuth provider with the given credentials.
//
// New performs OIDC discovery at construction time: it fetches Google's
// .well-known/openid-configuration over the network to resolve the issuer, JWKS
// URL, and supported signing algorithms. This is a deliberate fail-fast call —
// if Google is unreachable, New returns an error rather than deferring the
// failure to the first ID token validation. Pass a context with a timeout to
// bound it.
//
// If scopes is empty, it defaults to ["openid", "email", "profile"]. If client
// is nil, a client with a 30s timeout is used; the same client is used for both
// discovery and the token/userinfo flows.
func New(ctx context.Context, clientID, clientSecret string, scopes []string, client *http.Client) (*Provider, error) {
	return newProvider(ctx, clientID, clientSecret, scopes, client, googleEndpoints())
}

// newProvider is the endpoint-injectable constructor New delegates to. Tests
// supply httptest endpoints; New supplies Google's real ones.
func newProvider(ctx context.Context, clientID, clientSecret string, scopes []string, client *http.Client, eps endpoints) (*Provider, error) {
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	// OIDC discovery: fetches the issuer, JWKS URL, and supported algorithms.
	// go-oidc caches the JWKS keys and handles key rotation. This is the
	// fail-fast network call documented on New.
	oidcCtx := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(oidcCtx, eps.issuer)
	if err != nil {
		return nil, fmt.Errorf("oauth google: OIDC discovery: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})

	return &Provider{
		config: oauth.ProviderConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       scopes,
			AuthURL:      eps.authURL,
			TokenURL:     eps.tokenURL,
			UserInfoURL:  eps.userInfoURL,
			JWKSURL:      eps.jwksURL,
		},
		endpoints: eps,
		client:    client,
		verifier:  verifier,
	}, nil
}

func (p *Provider) Name() string                 { return "google" }
func (p *Provider) SupportsOIDC() bool           { return true }
func (p *Provider) TrustEmailVerification() bool { return true }

// GetAuthorizationURL builds the Google consent-screen URL for the
// authorization-code flow with PKCE (S256). access_type=offline and
// prompt=consent request a refresh token; nonce, when non-empty, is echoed back
// in the ID token for replay protection.
func (p *Provider) GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string {
	params := url.Values{
		"client_id":             {p.config.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(p.config.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {oauth.GenerateCodeChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}
	if nonce != "" {
		params.Set("nonce", nonce)
	}
	return p.config.AuthURL + "?" + params.Encode()
}

func (p *Provider) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*oauth.TokenResponse, error) {
	data := url.Values{
		"code":          {code},
		"client_id":     {p.config.ClientID},
		"client_secret": {p.config.ClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {codeVerifier},
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := p.postForm(ctx, p.config.TokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: exchange code: %w", err)
	}

	return &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		IDToken:      raw.IDToken,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
	}, nil
}

func (p *Provider) GetUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error) {
	var raw struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := p.getJSON(ctx, p.config.UserInfoURL, accessToken, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: get user info: %w", err)
	}

	return &oauth.UserInfo{
		ProviderUserID: raw.Sub,
		Email:          raw.Email,
		EmailVerified:  raw.EmailVerified,
		Name:           raw.Name,
		Picture:        raw.Picture,
	}, nil
}

// ValidateIDToken verifies a Google OIDC ID token cryptographically.
//
// The token's RSA signature is verified against Google's public keys (fetched
// and cached from JWKS by go-oidc), and the standard claims are validated: iss
// must be the discovered issuer, aud must match the configured ClientID, and
// exp must not be expired. The nonce claim is checked when a non-empty nonce is
// provided; the email claim must be present.
func (p *Provider) ValidateIDToken(ctx context.Context, idToken, nonce string) (*oauth.IDTokenClaims, error) {
	token, err := p.verifier.Verify(ctx, idToken)
	if err != nil {
		return nil, fmt.Errorf("oauth google: verify ID token: %w", err)
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Nonce         string `json:"nonce"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oauth google: extract claims: %w", err)
	}

	if nonce != "" && claims.Nonce != nonce {
		return nil, fmt.Errorf("oauth google: nonce mismatch")
	}
	if claims.Email == "" {
		return nil, fmt.Errorf("oauth google: missing email in ID token")
	}

	return &oauth.IDTokenClaims{
		Subject:       token.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
		Nonce:         claims.Nonce,
	}, nil
}

func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error) {
	data := url.Values{
		"client_id":     {p.config.ClientID},
		"client_secret": {p.config.ClientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}

	var raw struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}
	if err := p.postForm(ctx, p.config.TokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: refresh token: %w", err)
	}

	return &oauth.TokenResponse{
		AccessToken: raw.AccessToken,
		ExpiresIn:   raw.ExpiresIn,
		TokenType:   raw.TokenType,
		Scopes:      raw.Scope,
	}, nil
}

func (p *Provider) postForm(ctx context.Context, endpoint string, data url.Values, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return json.Unmarshal(body, result)
}

func (p *Provider) getJSON(ctx context.Context, endpoint, bearerToken string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return json.Unmarshal(body, result)
}
