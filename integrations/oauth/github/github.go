// Package github implements the sdk/oauth.Provider port for GitHub's OAuth 2.0
// endpoints. It is deliberately an integration despite requiring no third-party
// library: the isolated external dependency is GitHub's live API contract — the
// shape of its token, user, and email endpoints — which churns on GitHub's
// release schedule, not sdk's. sdk defaults must be vendor-neutral, so a GitHub
// connector is never an sdk default even though it is stdlib-implementable. It
// imports sdk/oauth for the port vocabulary and no feature or other integration.
//
// GitHub does not support OpenID Connect for user login (no ID tokens), so
// SupportsOIDC reports false and ValidateIDToken always returns an error;
// callers use GetUserInfo instead. GitHub Apps issue expiring user access tokens
// (8h) with refresh tokens (6mo), so RefreshToken is GitHub-Apps-aware.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/oauth"
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
	config    oauth.ProviderConfig
	endpoints endpoints
	client    *http.Client
}

// New creates a GitHub OAuth provider with the given credentials.
//
// Unlike an OIDC provider, New performs no network I/O at construction — GitHub
// has no discovery document to fetch, so there is nothing to fail fast on. If
// scopes is empty, it defaults to ["user:email"] (required to read the primary
// verified email). If client is nil, a client with a 30s timeout is used.
func New(clientID, clientSecret string, scopes []string, client *http.Client) *Provider {
	return newProvider(clientID, clientSecret, scopes, client, githubEndpoints())
}

// newProvider is the endpoint-injectable constructor New delegates to. Tests
// supply httptest endpoints; New supplies GitHub's real ones.
func newProvider(clientID, clientSecret string, scopes []string, client *http.Client, eps endpoints) *Provider {
	if len(scopes) == 0 {
		scopes = []string{"user:email"}
	}
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Provider{
		config: oauth.ProviderConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       scopes,
			AuthURL:      eps.authURL,
			TokenURL:     eps.tokenURL,
			UserInfoURL:  eps.userInfoURL,
		},
		endpoints: eps,
		client:    client,
	}
}

func (p *Provider) Name() string                 { return "github" }
func (p *Provider) SupportsOIDC() bool           { return false }
func (p *Provider) TrustEmailVerification() bool { return false }

// GetAuthorizationURL builds the GitHub authorization URL for the
// authorization-code flow with PKCE (S256). GitHub does not implement OIDC, so
// nonce is unused (there is no ID token to echo it back in).
func (p *Provider) GetAuthorizationURL(state, codeVerifier, _, redirectURI string) string {
	params := url.Values{
		"client_id":             {p.config.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(p.config.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {oauth.GenerateCodeChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
	return p.config.AuthURL + "?" + params.Encode()
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
	if err := p.postForm(ctx, p.config.TokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth github: exchange code: %w", err)
	}
	if raw.Error != "" {
		return nil, fmt.Errorf("oauth github: %s: %s", raw.Error, raw.ErrorDesc)
	}

	return &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
	}, nil
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
	if err := p.getJSON(ctx, p.config.UserInfoURL, accessToken, &profile); err != nil {
		return nil, fmt.Errorf("oauth github: get user profile: %w", err)
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, p.endpoints.emailsURL, accessToken, &emails); err != nil {
		return nil, fmt.Errorf("oauth github: get user emails: %w", err)
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
	if primaryEmail == "" {
		return nil, fmt.Errorf("oauth github: no email found for user")
	}

	return &oauth.UserInfo{
		ProviderUserID: strconv.Itoa(profile.ID),
		Email:          primaryEmail,
		EmailVerified:  emailVerified,
		Name:           profile.Name,
		Picture:        profile.AvatarURL,
	}, nil
}

// ValidateIDToken is not supported — GitHub does not support OIDC for user
// login. Callers must use GetUserInfo instead.
func (p *Provider) ValidateIDToken(_ context.Context, _, _ string) (*oauth.IDTokenClaims, error) {
	return nil, fmt.Errorf("oauth github: OIDC not supported")
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
	if err := p.postForm(ctx, p.config.TokenURL, data, &raw); err != nil {
		return nil, fmt.Errorf("oauth github: refresh token: %w", err)
	}
	if raw.Error != "" {
		return nil, fmt.Errorf("oauth github: %s: %s", raw.Error, raw.ErrorDesc)
	}

	return &oauth.TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
		TokenType:    raw.TokenType,
		Scopes:       raw.Scope,
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
