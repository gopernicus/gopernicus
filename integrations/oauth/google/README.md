# integrations/oauth/google

An `sdk/oauth.Provider` implementation for **Google** OAuth 2.0 + OpenID
Connect. It wraps exactly one third-party library —
`github.com/coreos/go-oidc/v3` — used solely for cryptographic ID token
verification (OIDC discovery, JWKS fetch/cache, RS256 signature validation).
The authorization-code, token-exchange, refresh, and userinfo flows are
hand-rolled on `net/http`; there is **no `golang.org/x/oauth2` dependency**. It
imports `sdk/oauth` for the port vocabulary and no feature or other integration.

## Construction fetches the network (fail-fast)

```go
provider, err := google.New(ctx, clientID, clientSecret, scopes, httpClient)
if err != nil {
    return err // Google unreachable at boot — fail fast
}
```

`New` performs **OIDC discovery at construction time**: it fetches Google's
`.well-known/openid-configuration` to resolve the issuer, JWKS URL, and
supported signing algorithms, then builds the ID token verifier. This is a
deliberate fail-fast network call — if Google is unreachable, `New` returns an
error rather than deferring the failure to the first `ValidateIDToken`. Pass a
context with a timeout to bound it. The library caches the JWKS keys and handles
Google's key rotation for the life of the provider.

## Configuration surface

| Parameter | Meaning |
|---|---|
| `ctx` | Context for the construction-time discovery call (bound it with a timeout). |
| `clientID` | Google OAuth client ID; also the expected `aud` for ID token validation. |
| `clientSecret` | Google OAuth client secret, sent on token exchange/refresh. |
| `scopes` | Requested scopes; empty defaults to `["openid", "email", "profile"]`. |
| `client` | `*http.Client` for discovery and the token/userinfo flows; nil defaults to a 30s-timeout client. |

The authorization URL is built for the PKCE (S256) authorization-code flow with
`access_type=offline` and `prompt=consent` (to obtain a refresh token) and an
optional `nonce` echoed back in the ID token. Response bodies from the token and
userinfo endpoints are capped at 1 MB.

## Provider behavior

- `Name()` → `"google"`; `SupportsOIDC()` → `true`;
  `TrustEmailVerification()` → `true`.
- `ExchangeCode` / `RefreshToken` — `application/x-www-form-urlencoded` POST to
  Google's token endpoint.
- `GetUserInfo` — bearer-token GET to Google's userinfo endpoint.
- `ValidateIDToken` — verifies the RSA signature against Google's JWKS and the
  standard claims (`iss`, `aud`, `exp`); checks `nonce` when provided and
  requires a non-empty `email`.

## Testing

The test suite is fully hermetic — it stands up an `httptest` server backed by
go-oidc's `oidctest` helper for OIDC discovery, JWKS, and ID token signing, with
caller-supplied fakes for the token and userinfo endpoints. No live Google
account or network is required.
