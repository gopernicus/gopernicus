# infrastructure/oauth -- OAuth Reference

Package `oauth` defines the Provider interface for OAuth 2.0 / OIDC providers and includes PKCE support.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/oauth`

## Provider Interface

The port consumed by the authentication core. Implementations live in subdirectories.

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

### Method Summary

| Method | Description |
|---|---|
| `Name()` | Provider identifier (e.g. "google", "github") |
| `SupportsOIDC()` | Whether the provider supports OpenID Connect ID tokens |
| `TrustEmailVerification()` | Whether to trust the provider's email verification claims |
| `GetAuthorizationURL` | Builds the redirect URL for user authorization |
| `ExchangeCode` | Exchanges authorization code for tokens (with PKCE verifier) |
| `GetUserInfo` | Fetches user profile via provider API (for non-OIDC providers) |
| `ValidateIDToken` | Validates an OIDC ID token and extracts claims |
| `RefreshToken` | Exchanges a refresh token for new tokens |

## Provider Implementations

### googleoauth

Google OAuth 2.0 + OIDC with cryptographic ID token verification (JWKS).

```go
import "github.com/gopernicus/gopernicus/infrastructure/oauth/googleoauth"
```

- `SupportsOIDC()` returns true
- `TrustEmailVerification()` returns true
- Uses JWKS for ID token validation

### githuboauth

GitHub OAuth 2.0.

```go
import "github.com/gopernicus/gopernicus/infrastructure/oauth/githuboauth"
```

- `SupportsOIDC()` returns false (uses `GetUserInfo` instead of `ValidateIDToken`)
- `TrustEmailVerification()` returns true

## Response Types

### TokenResponse

```go
type TokenResponse struct {
    AccessToken  string
    RefreshToken string // empty if not provided
    ExpiresIn    int    // seconds
    IDToken      string // empty if not an OIDC provider
    TokenType    string
    Scopes       string
}
```

### UserInfo

```go
type UserInfo struct {
    ProviderUserID string
    Email          string
    EmailVerified  bool
    Name           string // may be empty
    Picture        string // may be empty
}
```

### IDTokenClaims

```go
type IDTokenClaims struct {
    Subject       string
    Email         string
    EmailVerified bool
    Name          string // may be empty
    Picture       string // may be empty
    Nonce         string
}
```

## ProviderConfig

Credentials and endpoints for configuring a provider.

```go
type ProviderConfig struct {
    ClientID     string
    ClientSecret string
    Scopes       []string
    AuthURL      string
    TokenURL     string
    UserInfoURL  string
    JWKSURL      string // for OIDC ID token validation
}
```

## PKCE Support

PKCE (Proof Key for Code Exchange) prevents authorization code interception attacks.

### GenerateCodeChallenge

Creates the S256 code challenge from a code verifier. The challenge is sent in the authorization request; the verifier is sent in the token exchange.

```go
challenge := oauth.GenerateCodeChallenge(verifier)
```

The verifier should be a cryptographically random string (typically 43-128 characters). The challenge is the base64url-encoded SHA256 hash of the verifier.

## OAuth Flow

1. Generate a random `state`, `codeVerifier`, and `nonce` (for OIDC).
2. Build the authorization URL:
   ```go
   url := provider.GetAuthorizationURL(state, codeVerifier, nonce, redirectURI)
   ```
3. Redirect the user to the authorization URL.
4. On callback, validate `state`, then exchange the code:
   ```go
   tokens, err := provider.ExchangeCode(ctx, code, codeVerifier, redirectURI)
   ```
5. For OIDC providers, validate the ID token:
   ```go
   claims, err := provider.ValidateIDToken(ctx, tokens.IDToken, nonce)
   ```
6. For non-OIDC providers, fetch user info:
   ```go
   info, err := provider.GetUserInfo(ctx, tokens.AccessToken)
   ```

## Related

- [infrastructure/cryptography](../infrastructure/cryptography.md) -- Encrypter used to store OAuth tokens at rest
