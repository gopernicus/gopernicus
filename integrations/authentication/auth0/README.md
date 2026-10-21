# integrations/authentication/auth0

The Auth0 preset for `sdk/capabilities/authentication` (imported as `sdkauth`).
It imports no library — `go.mod` requires only `sdk` — and, like
`integrations/oauth/github`, exists to isolate a vendor contract: Auth0's
issuer format and claim names. Package name: `auth0`.

Verification is generic. `cmd` composes the preset with
`integrations/cryptids/go-oidc` (an integration never imports another):

```go
issuer := auth0.Issuer(domain) // "https://example.us.auth0.com/"
verifier, err := gooidc.New(ctx, issuer, gooidc.WithAudiences(apiAudience))
if err != nil {
	return err
}
provider := sdkauth.NewOIDC(issuer, verifier, auth0.Profile(domain))
```

## Surface

| Member | Behavior |
|---|---|
| `Issuer(domain) string` | `https://<domain>/` — **trailing slash kept**, exactly as Auth0 issues `iss` and publishes discovery. Accepts a bare tenant or custom domain; tolerates a scheme and trailing slash; panics on an empty domain or one with a path. A token whose `iss` lacks the slash is a foreign issuer. |
| `Profile(domain) sdkauth.Profile` | Standard OIDC claims at their defaults (`sub`, `email`, `email_verified`, `name`, `phone_number`, `phone_number_verified`, `auth_time` else `iat`, `amr`); `org_id` → `Identity.Tenant`; `gty` is the sign-in method, with `client-credentials` denied; `azp` → `Identity.Attributes["azp"]`. A fresh value per call. |
| `DefaultDenySignIn()` | `["client-credentials"]`. |
| `ClaimOrganization`, `ClaimGrantType`, `GrantClientCredentials`, `AttributeAuthorizedParty` | The claim names above. |

## The pinned Auth0 contract

- **Access tokens are JWTs only when the SPA requests an API `audience`.**
  Without one, Auth0 issues an opaque access token that no verifier can check;
  configure the SPA SDK with `authorizationParams.audience` set to the API
  identifier and pass the same value to `gooidc.WithAudiences`. Access tokens
  then carry `aud` as an array (API identifier plus `/userinfo`); any-of
  matching handles it.
- **`org_id` is the tenant** when Auth0 Organizations are in use; consumers pin
  allowed organizations (`AllowedTenants` in the pocket).
- **MFA:** Auth0 adds `"mfa"` to `amr` after a second factor, which the default
  MFA methods recognize.
- **Machine tokens are refused.** A client-credentials access token
  (`gty: client-credentials`, `sub: <client>@clients`) has no user to map;
  machine credentials stay native API keys. A host that really wants them
  passes a `Profile` with a different `DenySignIn`.
- **Access tokens carry no email or name** unless an Auth0 Action adds them,
  usually under a namespaced claim (`https://example.test/email`). Map those
  in the host by copying the `Profile` and setting `Claims.Email` (a
  URL-namespaced path resolves as a literal key first).
- **Header-only.** Auth0 has three session layers (application, Auth0 SSO,
  identity provider) and no API that mints or verifies an application session
  cookie, so there is no cookie provider:
  https://auth0.com/docs/manage-users/sessions/session-layers
- **No revocation facts.** Access tokens stay valid until they expire; keep
  their lifetime short. The preset implements no `RevocationChecker`.
- Roles and permissions (`permissions`, custom role claims) are never mapped:
  provider claims are identity data, and authorization is host code.

## Testing

`go test ./...` is hermetic: `authenticationtest.RunProvider` over
`NewOIDC(Issuer, decoding verifier, Profile)`, documented access-token and
ID-token shapes, the machine-token denial, the trailing-slash pin, issuer
normalization and `Profile` freshness.

The live leg checks the **mapping** of one real token and skips loudly without
its variables:

```sh
AUTH0_DOMAIN=… AUTH0_LIVE_TOKEN=… go test -tags=live ./...
```

It decodes the token without verifying its signature, because this module may
not import go-oidc (G13). A real Auth0 token's signature is verified through
`integrations/cryptids/go-oidc`'s live leg
(`OIDC_LIVE_ISSUER=https://<domain>/ OIDC_LIVE_AUDIENCE=… OIDC_LIVE_TOKEN=…`)
or the proof host's `cmd` tests.
