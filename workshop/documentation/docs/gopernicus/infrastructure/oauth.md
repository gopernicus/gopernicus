---
sidebar_position: 8
title: OAuth
---

# Infrastructure — OAuth

`github.com/gopernicus/gopernicus/infrastructure/oauth`

The `oauth` package defines the `Provider` interface (port) and shared types for OAuth 2.0 / OIDC flows. Concrete implementations live in subdirectories. The interface is consumed by `core/auth/authentication` — the infrastructure layer's job is to normalize provider differences behind a consistent contract.

## Provider interface

```go
type Provider interface {
    Name() string
    SupportsOIDC() bool
    TrustEmailVerification() bool

    GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string
    ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*TokenResponse, error)
    GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error)
    ValidateIDToken(ctx context.Context, idToken, nonce string) (*IDTokenClaims, error)
    RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error)
}
```

`SupportsOIDC` signals whether the provider issues ID tokens that can be cryptographically validated. When it returns `false`, `ValidateIDToken` will return an error and the authentication layer falls back to `GetUserInfo`.

### `TrustEmailVerification`

Not all OAuth providers reliably verify that a user controls the email address they claim. `TrustEmailVerification` lets each implementation declare whether its `email_verified` claim can be trusted for marking an account as verified in your system.

Google returns `true` — its verification process is considered reliable. GitHub returns `false` — while GitHub does expose email verification status, its policies don't meet the same bar, so Gopernicus treats those emails as unverified by default. When evaluating a new provider, consider whether its verification process carries the same trust guarantees before setting this to `true`.

## Shared types

```go
type TokenResponse struct {
    AccessToken  string
    RefreshToken string // empty if not provided
    ExpiresIn    int    // seconds
    IDToken      string // empty for non-OIDC providers
    TokenType    string
    Scopes       string
}

type UserInfo struct {
    ProviderUserID string
    Email          string
    EmailVerified  bool
    Name           string // may be empty
    Picture        string // may be empty
}

type IDTokenClaims struct {
    Subject       string
    Email         string
    EmailVerified bool
    Name          string // may be empty
    Picture       string // may be empty
    Nonce         string
}
```

## PKCE

All providers use PKCE (Proof Key for Code Exchange) with S256. `GenerateCodeChallenge` produces the code challenge from a verifier:

```go
challenge := oauth.GenerateCodeChallenge(verifier)
// challenge is sent in the authorization URL
// verifier is sent in the token exchange
```

The caller (typically `core/auth/authentication`) is responsible for generating and storing the verifier. The providers handle embedding the challenge in the authorization URL automatically.

## Implementations

| Package | Provider | OIDC | Trust email |
|---|---|---|---|
| `googleoauth` | Google | yes (JWKS verification) | yes |
| `githuboauth` | GitHub | no | no |

### Google (`googleoauth`)

```go
import "github.com/gopernicus/gopernicus/infrastructure/oauth/googleoauth"

provider, err := googleoauth.NewGoogleProvider(ctx, clientID, clientSecret, scopes, nil)
```

`NewGoogleProvider` performs **OIDC discovery at startup** — it fetches Google's `.well-known/openid-configuration` to resolve the JWKS URL and supported algorithms. This makes a network call during construction. If Google is unreachable, initialization fails fast.

ID tokens are verified cryptographically: the RS256 signature is checked against Google's public keys (fetched and cached from JWKS, with automatic key rotation). This prevents forged tokens from being accepted.

Pass `nil` for the HTTP client to use a default with a 30s timeout. Scopes default to `["openid", "email", "profile"]`. The authorization URL includes `access_type=offline` and `prompt=consent` to request a refresh token.

### GitHub (`githuboauth`)

```go
import "github.com/gopernicus/gopernicus/infrastructure/oauth/githuboauth"

provider := githuboauth.NewGitHubProvider(clientID, clientSecret, scopes, nil)
```

GitHub does not support OIDC — there are no ID tokens in the login flow. `GetUserInfo` makes two API calls: `/user` for the profile and `/user/emails` for the primary verified email address. The email endpoint is required because GitHub users can have a private profile email; the emails endpoint returns the primary address even when the profile hides it.

GitHub Apps issue **expiring user access tokens** (8 hours) with refresh tokens valid for 6 months. `RefreshToken` is supported. Scopes default to `["user:email"]`.

## Wiring example

```go
// Google
googleProvider, err := googleoauth.NewGoogleProvider(
    ctx,
    cfg.GoogleClientID,
    cfg.GoogleClientSecret,
    nil, // default scopes
    nil, // default http.Client
)
if err != nil {
    return fmt.Errorf("init google oauth: %w", err)
}

// GitHub
githubProvider := githuboauth.NewGitHubProvider(
    cfg.GitHubClientID,
    cfg.GitHubClientSecret,
    nil, // default scopes
    nil, // default http.Client
)

// Pass providers to core/auth/authentication
authenticator := authentication.New(/* ... */, googleProvider, githubProvider)
```

## See also

- [Core / Auth / Authentication](/docs/gopernicus/core/auth/authentication) — how providers are used in the login and account linking flows
