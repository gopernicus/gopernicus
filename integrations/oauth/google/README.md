# integrations/oauth/google

Google OAuth 2.0 and OpenID Connect behind the stdlib SDK provider port.
`go-oidc` owns discovery, signature verification and cached JWKS rotation;
the adapter uses `net/http` for token exchange, refresh and userinfo.

```go
provider, err := google.New(ctx, google.Config{
    ClientID: clientID,
    ClientSecret: clientSecret,
    HTTPClient: httpClient,
})
if err != nil {
    return err
}
```

Construction fetches discovery. Give `ctx` a deadline. `ClientID` is required;
`ClientSecret` may be empty for a provider registration that permits public
clients. The host must register each callback URI and choose a compatible client
type; a native URI does not turn a web client registration into a native client.

Empty `Scopes` defaults to `openid email profile`; custom scopes must include
`openid`. Scopes and HTTP client configuration are copied. The underlying
transport remains shared; callers must not mutate it concurrently. A nil client
gets a 30-second timeout. Redirects are always refused, including discovery and
JWKS redirects. Direct API, discovery and JWKS response bodies are limited to
1 MiB and overflow is an error.

`AccessType` defaults to empty; set `"offline"` when refresh tokens are wanted.
`Prompt` defaults to empty; choose `"consent"`, `"select_account"`, their
combination, or `"none"`. The adapter no longer forces offline consent on every
login. Whether Google returns a refresh token also depends on its grant policy.

The core `oauth.Provider` takes `oauth.AuthorizationRequest` and returns
`(string, error)` from `GetAuthorizationURL`. It validates state, PKCE verifier
and redirect shape. Google also implements optional `oauth.IDTokenValidator`
and `oauth.TokenRefresher`. ID token validation checks signature, issuer,
audience, expiry, a supplied nonce and a nonempty subject. Email is optional.
Authentication requires an ID token for a flow initiated through this OIDC
capability; missing tokens cannot fall back to userinfo.

`EmailVerified` preserves Google's assertion. `EmailAuthoritative` separately
reports verified Gmail or verified Workspace (`hd`) evidence. A verified email
from another domain alone does not establish Google's continuing authority over
that mailbox. The host's `authentication.Config.TrustOAuthEmail` decides whether
the evidence permits new email-based registration/adoption; existing linked-ID
login remains independent of email. See [Google's verification guidance](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token).

Malformed success responses fail before use. `errors.As(err, *oauth.Error)`
exposes operation, HTTP status and protocol code; `errors.Is` retains cancellation
and other wrapped causes. Error strings omit upstream bodies and descriptions;
the unwrapped cause and remote code are untrusted diagnostics.

Tests use owned HTTP/OIDC servers and locally signed tokens. No real Google
login, provider credentials or device-authorization grant is exercised.
Migration: [AUDIT-015](../../../AUDIT.md#audit-015-bound-oauth-flows-and-truthful-tracing).
