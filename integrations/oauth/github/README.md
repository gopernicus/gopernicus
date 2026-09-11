# integrations/oauth/github

GitHub OAuth authorization-code login behind `sdk/capabilities/oauth.Provider`.
The adapter uses only SDK and Go standard library packages. It remains an
integration because its dependency is GitHub's vendor API contract.

```go
provider, err := github.New(github.Config{
    ClientID: clientID,
    ClientSecret: clientSecret,
    HTTPClient: httpClient,
})
if err != nil {
    return err
}
```

Construction validates required credentials without network I/O. Empty `Scopes`
defaults to `user:email`. Scopes and HTTP client configuration are copied; the
underlying transport remains host-owned. A nil client gets a 30-second timeout.
Redirects are refused so credentials cannot be replayed at another endpoint.
Bodies are limited to 1 MiB; overflow and malformed successes fail.

`GetAuthorizationURL(oauth.AuthorizationRequest)` returns `(string, error)` and
uses PKCE S256. GitHub has no user-login OIDC capability and does not implement
`oauth.IDTokenValidator`. It implements optional `oauth.TokenRefresher` for
registrations/grants that issue refresh tokens, such as expiring GitHub App user
tokens. The host chooses the application registration and requested scopes.

`GetUserInfo` requires a positive stable `/user` ID. It reads the primary email
from `/user/emails`; an absent email permission (403) leaves the identity usable
without verified email evidence. A profile-email fallback is unverified, and no
email at all is allowed for existing linked-ID login. Other email endpoint errors
remain errors. `EmailVerified` reports the endpoint's evidence;
`EmailAuthoritative` is false. New email-based registration/adoption requires an
explicit authentication host trust policy as well as verified evidence.

Both non-200 responses and OAuth errors in a 200 response fail. Inspect
`*oauth.Error` with `errors.As` for status and remote code. Its string omits
upstream bodies and descriptions; wrapped causes preserve cancellation.
Treat remote codes and unwrapped causes as untrusted diagnostics.

Tests use owned HTTP servers and fake transports, with no real GitHub account.
Migration: [AUDIT-015](../../../AUDIT.md#audit-015-bound-oauth-flows-and-truthful-tracing).
