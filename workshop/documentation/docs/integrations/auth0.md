---
title: Auth0 preset
description: Map Auth0 tokens to an sdkauth Identity through the generic OIDC verifier.
---

# Auth0 preset

`integrations/authentication/auth0` (package `auth0`) imports no library. It
pins Auth0's issuer format and claim names as a `sdkauth.Profile`; `cmd`
composes it with the [go-oidc verifier](go-oidc.md):

```go
issuer := auth0.Issuer(domain) // "https://example.us.auth0.com/" — trailing slash kept
verifier, err := gooidc.New(ctx, issuer, gooidc.WithAudiences(apiAudience))
if err != nil {
	return err
}
provider := sdkauth.NewOIDC(issuer, verifier, auth0.Profile(domain))
```

| Claim | Mapping |
|---|---|
| `sub`, `email`, `email_verified`, `name`, `auth_time`/`iat`, `amr` | OIDC defaults; `"mfa"` in `amr` sets `Identity.MFA` |
| `org_id` | `Identity.Tenant` (Auth0 Organizations) |
| `gty` | sign-in method; `client-credentials` (machine tokens) is denied |
| `azp` | `Identity.Attributes["azp"]` |

- Access tokens are JWTs **only when the SPA requests an API `audience`**;
  otherwise they are opaque and cannot be verified.
- Access tokens carry email and name only when an Auth0 Action adds them
  (usually a namespaced claim the host maps by copying the `Profile`).
- **`auth_time` is not guaranteed.** Under `ExternalProvider.RequireAuthenticationTime`
  (authentication v0.37.0) a token without it is denied `authenticated_at_unasserted`;
  request it with `max_age` on the sign-in before setting the flag.
- **Header-only** and **no revocation facts**: Auth0 has no API for an
  application session cookie or for revoking access tokens.

`go test ./...` is hermetic. The `-tags=live` leg (`AUTH0_DOMAIN`,
`AUTH0_LIVE_TOKEN`) checks the mapping of one real token without verifying its
signature; signatures are verified through go-oidc's live leg.
