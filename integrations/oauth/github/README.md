# integrations/oauth/github

An `sdk/oauth.Provider` implementation for **GitHub** OAuth 2.0. This module has
**zero external requires** — it is built on `sdk/oauth` and the standard
library's `net/http` alone — yet it lives under `integrations/`, not in the sdk
kernel. Under the taxonomy amendment, an integration isolates exactly one
external dependency: a third-party library **or an external vendor's live API
contract**. Here the isolated dependency is GitHub's live API contract — the
shape of its authorization, token, user, and email endpoints — which churns on
GitHub's release schedule, not sdk's. sdk defaults must be vendor-neutral (they
make an app boot with no external account), so a GitHub connector is never an sdk
default even though it is stdlib-implementable; the R3 `stores/memory` refusal
stays distinct because it isolated nothing external.

## Construction does no network I/O

```go
provider := github.New(clientID, clientSecret, scopes, httpClient)
```

Unlike an OIDC provider, `New` performs no network call at construction — GitHub
has no discovery document to fetch, so there is nothing to fail fast on and `New`
returns no error. The provider is ready to use immediately.

## Configuration surface

| Parameter | Meaning |
|---|---|
| `clientID` | GitHub OAuth client ID. |
| `clientSecret` | GitHub OAuth client secret, sent on token exchange/refresh. |
| `scopes` | Requested scopes; empty defaults to `["user:email"]` (required to read the primary verified email). |
| `client` | `*http.Client` for the token/user flows; nil defaults to a 30s-timeout client. |

The authorization URL is built for the PKCE (S256) authorization-code flow.
Response bodies from the token, user, and email endpoints are capped at 1 MB.

## Provider behavior

- `Name()` → `"github"`; `SupportsOIDC()` → `false`;
  `TrustEmailVerification()` → `false`.
- `GetAuthorizationURL` — PKCE S256; `nonce` is ignored because GitHub issues no
  ID token to echo it back in.
- `ExchangeCode` / `RefreshToken` — `application/x-www-form-urlencoded` POST to
  GitHub's token endpoint with `Accept: application/json`. GitHub reports OAuth
  errors as an `error`/`error_description` object in a 200 body; both that and
  non-200 responses surface as errors.
- `GetUserInfo` — bearer-token GET to `/user`, then a second GET to
  `/user/emails` to find the primary verified email (the profile email may be
  null or private). If no primary email is present it falls back to the profile
  email, reported as unverified; if neither exists it errors.
- `ValidateIDToken` — GitHub does not support OpenID Connect for user login, so
  this always returns an `"OIDC not supported"` error. Use `GetUserInfo`.
- `RefreshToken` is GitHub-Apps-aware: GitHub Apps issue expiring user access
  tokens (8h) with refresh tokens (6mo); classic OAuth Apps issue non-expiring
  tokens and never reach here.

## Testing

The test suite is fully hermetic — it stands up an `httptest` server with
caller-supplied fakes for the authorization, token, user, and email endpoints.
No live GitHub account or network is required.
