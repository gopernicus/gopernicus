// Package google implements the sdk/capabilities/oauth.Provider port for Google's OAuth 2.0
// + OpenID Connect endpoints. It wraps exactly one third-party library —
// github.com/coreos/go-oidc/v3 — used solely for cryptographic ID token
// verification (OIDC discovery, JWKS fetch/cache, RS256 signature validation).
// The authorization-code, token-exchange, refresh, and userinfo flows are
// hand-rolled on net/http; there is no x/oauth2 dependency. It imports sdk/capabilities/oauth
// for the port vocabulary and no pocket or other integration.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
	config    Config
	endpoints endpoints
	client    *http.Client
	verifier  *oidc.IDTokenVerifier
}

// Config belongs to the host. Scopes and the HTTP client configuration are
// copied at construction; the underlying transport remains host-owned.
type Config struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
	HTTPClient   *http.Client
	// AccessType may be empty, "online", or "offline". Prompt is an optional
	// space-separated combination of "consent" and "select_account", or "none".
	AccessType string
	Prompt     string
}

// New performs OIDC discovery using the supplied context. Bound that context
// with a deadline. The copied client is used for discovery, JWKS and API calls.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	return newProvider(ctx, cfg, googleEndpoints())
}

func newProvider(ctx context.Context, cfg Config, eps endpoints) (*Provider, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("oauth google: client ID is required")
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	cfg.Scopes = slices.Clone(cfg.Scopes)
	if !slices.Contains(cfg.Scopes, "openid") {
		return nil, fmt.Errorf("oauth google: openid scope is required")
	}
	if cfg.AccessType != "" && cfg.AccessType != "online" && cfg.AccessType != "offline" {
		return nil, fmt.Errorf("oauth google: invalid access type")
	}
	for _, prompt := range strings.Fields(cfg.Prompt) {
		if prompt != "consent" && prompt != "select_account" && prompt != "none" || prompt == "none" && cfg.Prompt != "none" {
			return nil, fmt.Errorf("oauth google: invalid prompt")
		}
	}
	client := http.Client{Timeout: defaultTimeout}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	// Never replay credentials or bearer tokens at a redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = boundedTransport{base: transport}
	cfg.HTTPClient = &client
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, &client), eps.issuer)
	if err != nil {
		return nil, &oauth.Error{Provider: "google", Operation: "OIDC discovery", Cause: errors.Join(err, ctx.Err())}
	}
	return &Provider{config: cfg, endpoints: eps, client: &client,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})}, nil
}

func (p *Provider) Name() string { return "google" }

// GetAuthorizationURL builds an authorization-code URL with PKCE S256.
func (p *Provider) GetAuthorizationURL(r oauth.AuthorizationRequest) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	params := url.Values{
		"client_id":             {p.config.ClientID},
		"redirect_uri":          {r.RedirectURI},
		"scope":                 {strings.Join(p.config.Scopes, " ")},
		"state":                 {r.State},
		"code_challenge":        {oauth.GenerateCodeChallenge(r.CodeVerifier)},
		"code_challenge_method": {"S256"},
	}
	params.Set("response_type", "code")
	if r.Nonce != "" {
		params.Set("nonce", r.Nonce)
	}
	if p.config.AccessType != "" {
		params.Set("access_type", p.config.AccessType)
	}
	if p.config.Prompt != "" {
		params.Set("prompt", p.config.Prompt)
	}
	return p.endpoints.authURL + "?" + params.Encode(), nil
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
	if err := p.postForm(ctx, p.endpoints.tokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: exchange code: %w", err)
	}

	token := &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		IDToken:      raw.IDToken,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(token.TokenType, "bearer") {
		return nil, fmt.Errorf("oauth google: unsupported token type")
	}
	return token, nil
}

func (p *Provider) GetUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error) {
	var raw struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := p.getJSON(ctx, p.endpoints.userInfoURL, accessToken, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: get user info: %w", err)
	}

	info := &oauth.UserInfo{
		ProviderUserID:     raw.Sub,
		Email:              raw.Email,
		EmailVerified:      raw.EmailVerified,
		EmailAuthoritative: authoritativeEmail(raw.Email, raw.EmailVerified, raw.HostedDomain),
		Name:               raw.Name,
		Picture:            raw.Picture,
	}
	if err := info.Validate(); err != nil {
		return nil, err
	}
	return info, nil
}

// ValidateIDToken verifies a Google OIDC ID token cryptographically.
//
// The token's RSA signature is verified against Google's public keys (fetched
// and cached from JWKS by go-oidc), and the standard claims are validated: iss
// must be the discovered issuer, aud must match the configured ClientID, and
// exp must not be expired. The nonce claim is checked when a non-empty nonce is
// provided; the subject claim must be present. Email remains optional.
func (p *Provider) ValidateIDToken(ctx context.Context, idToken, nonce string) (*oauth.IDTokenClaims, error) {
	if err := ctx.Err(); err != nil {
		return nil, &oauth.Error{Provider: "google", Operation: "verify ID token", Cause: err}
	}
	token, err := p.verifier.Verify(ctx, idToken)
	if err != nil {
		return nil, &oauth.Error{Provider: "google", Operation: "verify ID token", Cause: errors.Join(err, ctx.Err())}
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Nonce         string `json:"nonce"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, &oauth.Error{Provider: "google", Operation: "extract claims", Cause: err}
	}

	if nonce != "" && claims.Nonce != nonce {
		return nil, fmt.Errorf("oauth google: nonce mismatch")
	}
	if strings.TrimSpace(token.Subject) == "" {
		return nil, fmt.Errorf("oauth google: missing subject in ID token")
	}

	return &oauth.IDTokenClaims{
		Subject:            token.Subject,
		Email:              claims.Email,
		EmailVerified:      claims.EmailVerified,
		EmailAuthoritative: authoritativeEmail(claims.Email, claims.EmailVerified, claims.HostedDomain),
		Name:               claims.Name,
		Picture:            claims.Picture,
		Nonce:              claims.Nonce,
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
	if err := p.postForm(ctx, p.endpoints.tokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth google: refresh token: %w", err)
	}

	token := &oauth.TokenResponse{
		AccessToken: raw.AccessToken,
		ExpiresIn:   raw.ExpiresIn,
		TokenType:   raw.TokenType,
		Scopes:      raw.Scope,
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(token.TokenType, "bearer") {
		return nil, fmt.Errorf("oauth google: unsupported token type")
	}
	return token, nil
}

func (p *Provider) postForm(ctx context.Context, endpoint string, data url.Values, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	return p.doJSON(req, result)
}

func (p *Provider) getJSON(ctx context.Context, endpoint, bearerToken string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Accept", "application/json")

	return p.doJSON(req, result)
}

// Google is authoritative for Gmail and verified Workspace identities. A raw
// email_verified claim alone does not establish continuing mailbox ownership.
func authoritativeEmail(email string, verified bool, hostedDomain string) bool {
	local, domain, ok := strings.Cut(email, "@")
	return verified && ok && local != "" && domain != "" && !strings.Contains(domain, "@") &&
		(strings.EqualFold(domain, "gmail.com") || strings.TrimSpace(hostedDomain) != "")
}

func (p *Provider) doJSON(req *http.Request, result any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return &oauth.Error{Provider: "google", Operation: "HTTP request", Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return &oauth.Error{Provider: "google", Operation: "read response", StatusCode: resp.StatusCode, Cause: err}
	}
	if len(body) > maxResponseBody {
		return &oauth.Error{Provider: "google", Operation: "response too large", StatusCode: resp.StatusCode}
	}
	var failure struct {
		Code string `json:"error"`
	}
	_ = json.Unmarshal(body, &failure)
	if resp.StatusCode != http.StatusOK || failure.Code != "" {
		return &oauth.Error{Provider: "google", Operation: "provider response", StatusCode: resp.StatusCode, Code: failure.Code}
	}
	if err := json.Unmarshal(body, result); err != nil {
		return &oauth.Error{Provider: "google", Operation: "decode response", StatusCode: resp.StatusCode, Cause: err}
	}
	return nil
}

// Bound discovery and JWKS bodies as well as direct API responses without
// replacing go-oidc's verification/key-cache implementation.
type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: maxResponseBody}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining < 0 {
		return 0, fmt.Errorf("oauth google: response too large")
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return 0, fmt.Errorf("oauth google: response too large")
	}
	return n, err
}
