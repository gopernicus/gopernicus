// Package googleoauth implements [oauth.Provider] for Google OAuth 2.0 + OIDC.
//
// ID token signature verification uses [github.com/coreos/go-oidc/v3] to
// fetch Google's JWKS, cache public keys, and verify RS256 signatures
// cryptographically. This prevents forged ID tokens from being accepted.
package googleoauth

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
	"github.com/gopernicus/gopernicus/infrastructure/oauth"
)

// maxResponseBody is the maximum size of HTTP response bodies from Google's
// APIs. Prevents OOM from malicious or broken endpoints.
const maxResponseBody = 1 << 20 // 1 MB

// GoogleProvider implements OAuth 2.0 + OIDC for Google with cryptographic
// ID token verification via JWKS.
type GoogleProvider struct {
	config   oauth.ProviderConfig
	client   *http.Client
	verifier *oidc.IDTokenVerifier
}

// NewGoogleProvider creates a Google OAuth provider with the given credentials.
//
// ctx is used for OIDC discovery (fetching Google's .well-known/openid-configuration).
// This makes a network call at construction time — fail fast if Google is unreachable.
//
// If scopes is nil, defaults to ["openid", "email", "profile"].
// If client is nil, a default client with a 30s timeout is used.
func NewGoogleProvider(ctx context.Context, clientID, clientSecret string, scopes []string, client *http.Client) (*GoogleProvider, error) {
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	// OIDC discovery: fetches issuer, JWKS URL, and supported algorithms
	// from https://accounts.google.com/.well-known/openid-configuration.
	// The go-oidc library caches JWKS keys and handles key rotation.
	oidcCtx := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(oidcCtx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("oauth google: OIDC discovery: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: clientID,
	})

	return &GoogleProvider{
		config: oauth.ProviderConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       scopes,
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			UserInfoURL:  "https://openidconnect.googleapis.com/v1/userinfo",
			JWKSURL:      "https://www.googleapis.com/oauth2/v3/certs",
		},
		client:   client,
		verifier: verifier,
	}, nil
}

func (p *GoogleProvider) Name() string                 { return "google" }
func (p *GoogleProvider) SupportsOIDC() bool            { return true }
func (p *GoogleProvider) TrustEmailVerification() bool  { return true }

func (p *GoogleProvider) GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string {
	params := url.Values{
		"client_id":              {p.config.ClientID},
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

func (p *GoogleProvider) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*oauth.TokenResponse, error) {
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

func (p *GoogleProvider) GetUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error) {
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
// and cached from JWKS). The following claims are validated:
//   - iss: must be https://accounts.google.com
//   - aud: must match the configured ClientID
//   - exp: must not be expired
//   - sub: must be present
//
// The nonce claim is checked if a non-empty nonce is provided.
func (p *GoogleProvider) ValidateIDToken(ctx context.Context, idToken, nonce string) (*oauth.IDTokenClaims, error) {
	// Verify signature + standard claims (iss, aud, exp).
	// go-oidc fetches and caches Google's JWKS keys automatically.
	token, err := p.verifier.Verify(ctx, idToken)
	if err != nil {
		return nil, fmt.Errorf("oauth google: verify ID token: %w", err)
	}

	// Extract custom claims not covered by go-oidc's IDToken struct.
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

func (p *GoogleProvider) RefreshToken(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error) {
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

// ---------------------------------------------------------------------------
// Internal HTTP helpers
// ---------------------------------------------------------------------------

func (p *GoogleProvider) postForm(ctx context.Context, endpoint string, data url.Values, result any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(data.Encode()))
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

func (p *GoogleProvider) getJSON(ctx context.Context, endpoint, bearerToken string, result any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
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
