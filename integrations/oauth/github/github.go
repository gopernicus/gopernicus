// Package github implements the sdk/capabilities/oauth.Provider port for GitHub's OAuth 2.0
// endpoints. It is deliberately an integration despite requiring no third-party
// library: the isolated external dependency is GitHub's live API contract — the
// shape of its token, user, and email endpoints — which churns on GitHub's
// release schedule, not sdk's. sdk defaults must be vendor-neutral, so a GitHub
// connector is never an sdk default even though it is stdlib-implementable. It
// imports sdk/capabilities/oauth for the port vocabulary and no pocket or other integration.
//
// GitHub does not support OpenID Connect for user login (no ID tokens), so
// it implements no IDTokenValidator; callers use GetUserInfo. GitHub Apps issue expiring user access tokens
// (8h) with refresh tokens (6mo), so RefreshToken is GitHub-Apps-aware.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

// maxResponseBody caps HTTP response bodies read from GitHub's token, user, and
// email endpoints, guarding against OOM from a malicious or broken endpoint.
const maxResponseBody = 1 << 20 // 1 MB

const defaultTimeout = 30 * time.Second

// GitHub's production endpoints. GetUserInfo needs a second call to the emails
// endpoint (GitHub returns the primary verified email separately from /user).
const (
	authURL     = "https://github.com/login/oauth/authorize"
	tokenURL    = "https://github.com/login/oauth/access_token"
	userInfoURL = "https://api.github.com/user"
	emailsURL   = "https://api.github.com/user/emails"
)

// Compile-time assertion that Provider satisfies the port.
var _ oauth.Provider = (*Provider)(nil)

// endpoints groups the OAuth URLs the provider talks to. Production uses
// GitHub's real values (see githubEndpoints); tests point them at httptest fakes
// so the whole flow stays hermetic.
type endpoints struct {
	authURL     string
	tokenURL    string
	userInfoURL string
	emailsURL   string
}

func githubEndpoints() endpoints {
	return endpoints{
		authURL:     authURL,
		tokenURL:    tokenURL,
		userInfoURL: userInfoURL,
		emailsURL:   emailsURL,
	}
}

// Provider implements oauth.Provider for GitHub. GitHub does not support OIDC,
// so there is no ID token verifier.
type Provider struct {
	config    Config
	endpoints endpoints
	client    *http.Client
}

// Config belongs to the host. Scopes and the HTTP client configuration are
// copied at construction; the underlying transport remains host-owned.
type Config struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
	HTTPClient   *http.Client
}

// New validates local configuration; it performs no network I/O.
func New(cfg Config) (*Provider, error) {
	return newProvider(cfg, githubEndpoints())
}

func newProvider(cfg Config, eps endpoints) (*Provider, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("oauth github: client ID is required")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("oauth github: client secret is required")
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"user:email"}
	}
	cfg.Scopes = slices.Clone(cfg.Scopes)
	client := http.Client{Timeout: defaultTimeout}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	// Never replay credentials or bearer tokens at a redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTPClient = &client
	return &Provider{config: cfg, endpoints: eps, client: &client}, nil
}

func (p *Provider) Name() string { return "github" }

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
	return p.endpoints.authURL + "?" + params.Encode(), nil
}

func (p *Provider) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*oauth.TokenResponse, error) {
	data := url.Values{
		"client_id":     {p.config.ClientID},
		"client_secret": {p.config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := p.postForm(ctx, p.endpoints.tokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth github: exchange code: %w", err)
	}
	if raw.Error != "" {
		return nil, &oauth.Error{Provider: "github", Operation: "token response", StatusCode: http.StatusOK, Code: raw.Error}
	}

	token := &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(token.TokenType, "bearer") {
		return nil, fmt.Errorf("oauth github: unsupported token type")
	}
	return token, nil
}

// GetUserInfo fetches the user profile and primary verified email from GitHub.
// GitHub requires two API calls: /user for the profile and /user/emails for the
// primary verified email address (the profile's email may be null or private).
func (p *Provider) GetUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error) {
	var profile struct {
		ID        int    `json:"id"`
		Login     string `json:"login"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := p.getJSON(ctx, p.endpoints.userInfoURL, accessToken, &profile); err != nil {
		return nil, fmt.Errorf("oauth github: get user profile: %w", err)
	}

	if profile.ID <= 0 {
		return nil, fmt.Errorf("oauth github: missing user ID")
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, p.endpoints.emailsURL, accessToken, &emails); err != nil {
		var response *oauth.Error
		if !errors.As(err, &response) || response.StatusCode != http.StatusForbidden || response.Operation != "provider response" {
			return nil, fmt.Errorf("oauth github: get user emails: %w", err)
		}
		// Missing email permission does not invalidate the stable /user identity.
		// No verified email evidence is available on this path.
		emails = nil
	}

	var primaryEmail string
	var emailVerified bool
	for _, e := range emails {
		if e.Primary {
			primaryEmail = e.Email
			emailVerified = e.Verified
			break
		}
	}
	if primaryEmail == "" && profile.Email != "" {
		// Fallback to the profile email — treat as unverified since the
		// /user/emails endpoint did not confirm it.
		primaryEmail = profile.Email
		emailVerified = false
	}

	info := &oauth.UserInfo{
		ProviderUserID: strconv.Itoa(profile.ID),
		Email:          primaryEmail,
		EmailVerified:  emailVerified,
		Name:           profile.Name,
		Picture:        profile.AvatarURL,
	}
	if err := info.Validate(); err != nil {
		return nil, err
	}
	return info, nil
}

// RefreshToken exchanges a refresh token for a new access token and refresh
// token. GitHub Apps issue expiring user access tokens (8h) with refresh tokens
// (6mo); classic OAuth Apps issue non-expiring tokens and never reach here.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error) {
	data := url.Values{
		"client_id":     {p.config.ClientID},
		"client_secret": {p.config.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := p.postForm(ctx, p.endpoints.tokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth github: refresh token: %w", err)
	}
	if raw.Error != "" {
		return nil, &oauth.Error{Provider: "github", Operation: "token response", StatusCode: http.StatusOK, Code: raw.Error}
	}

	token := &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(token.TokenType, "bearer") {
		return nil, fmt.Errorf("oauth github: unsupported token type")
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

func (p *Provider) doJSON(req *http.Request, result any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return &oauth.Error{Provider: "github", Operation: "HTTP request", Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return &oauth.Error{Provider: "github", Operation: "read response", StatusCode: resp.StatusCode, Cause: err}
	}
	if len(body) > maxResponseBody {
		return &oauth.Error{Provider: "github", Operation: "response too large", StatusCode: resp.StatusCode}
	}
	var failure struct {
		Code string `json:"error"`
	}
	_ = json.Unmarshal(body, &failure)
	if resp.StatusCode != http.StatusOK || failure.Code != "" {
		return &oauth.Error{Provider: "github", Operation: "provider response", StatusCode: resp.StatusCode, Code: failure.Code}
	}
	if err := json.Unmarshal(body, result); err != nil {
		return &oauth.Error{Provider: "github", Operation: "decode response", StatusCode: resp.StatusCode, Cause: err}
	}
	return nil
}
