// Package githuboauth implements [oauth.Provider] for GitHub OAuth 2.0.
//
// GitHub does NOT support OpenID Connect (no ID tokens in the login flow).
// GitHub Apps issue expiring user access tokens (8h) with refresh tokens (6mo).
package githuboauth

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

	"github.com/gopernicus/gopernicus/infrastructure/oauth"
)

// maxResponseBody is the maximum size of HTTP response bodies from GitHub's
// APIs. Prevents OOM from malicious or broken endpoints.
const maxResponseBody = 1 << 20 // 1 MB

// GitHubProvider implements OAuth 2.0 for GitHub.
type GitHubProvider struct {
	config oauth.ProviderConfig
	client *http.Client
}

// NewGitHubProvider creates a GitHub OAuth provider with the given credentials.
// If scopes is nil, defaults to ["user:email"].
// If client is nil, a default client with a 30s timeout is used.
func NewGitHubProvider(clientID, clientSecret string, scopes []string, client *http.Client) *GitHubProvider {
	if len(scopes) == 0 {
		scopes = []string{"user:email"}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHubProvider{
		config: oauth.ProviderConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       scopes,
			AuthURL:      "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			UserInfoURL:  "https://api.github.com/user",
		},
		client: client,
	}
}

func (p *GitHubProvider) Name() string                 { return "github" }
func (p *GitHubProvider) SupportsOIDC() bool            { return false }
func (p *GitHubProvider) TrustEmailVerification() bool  { return false }

func (p *GitHubProvider) GetAuthorizationURL(state, codeVerifier, _, redirectURI string) string {
	params := url.Values{
		"client_id":              {p.config.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(p.config.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {oauth.GenerateCodeChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
	return p.config.AuthURL + "?" + params.Encode()
}

func (p *GitHubProvider) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*oauth.TokenResponse, error) {
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
// GitHub requires two API calls: /user for the profile and /user/emails for
// the primary verified email address.
func (p *GitHubProvider) GetUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error) {
	// Fetch user profile.
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

	// Fetch emails to find primary verified email.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, "https://api.github.com/user/emails", accessToken, &emails); err != nil {
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
		// Fallback to profile email — treat as unverified since
		// the /user/emails endpoint didn't confirm it.
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

// ValidateIDToken is not supported — GitHub does not support OIDC for user login.
func (p *GitHubProvider) ValidateIDToken(_ context.Context, _, _ string) (*oauth.IDTokenClaims, error) {
	return nil, fmt.Errorf("oauth github: OIDC not supported")
}

// RefreshToken exchanges a refresh token for a new access token and refresh token.
// GitHub Apps issue expiring user access tokens (8h) with refresh tokens (6mo).
func (p *GitHubProvider) RefreshToken(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error) {
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

// ---------------------------------------------------------------------------
// Internal HTTP helpers
// ---------------------------------------------------------------------------

func (p *GitHubProvider) postForm(ctx context.Context, endpoint string, data url.Values, result any) error {
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

func (p *GitHubProvider) getJSON(ctx context.Context, endpoint, bearerToken string, result any) error {
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
