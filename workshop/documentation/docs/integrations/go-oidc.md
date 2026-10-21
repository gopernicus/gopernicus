---
title: go-oidc verifier
description: Verify bearer JWTs from an external OpenID Connect issuer with a hardened key set.
---

# go-oidc verifier

`integrations/cryptids/go-oidc` (package `gooidc`) implements
[`cryptids.JWTVerifier`](../sdk/pkg.md#cryptids) over `github.com/coreos/go-oidc/v3`.
It verifies tokens minted by an identity provider that publishes a JWKS —
Firebase Auth ID tokens, Auth0, Clerk, Cognito, Okta, Keycloak — and is the
verifier `sdk/capabilities/authentication.NewOIDC` expects. It owns
verification only; claim mapping belongs to `NewOIDC` and its `Profile`.

```go
verifier, err := gooidc.New(ctx, issuer, gooidc.WithAudiences(audience))
if err != nil {
	return err
}
provider := sdkauth.NewOIDC(issuer, verifier, sdkauth.Profile{})
```

`New` runs discovery under `ctx` (bound it with a timeout) unless
`WithJWKSURL` pins the key set, in which case construction does no network
I/O. Keys are fetched on the first token. Share one `Verifier` per issuer.

## What it checks

| Step | Rule |
|---|---|
| size | at most 16 KiB, before any parsing |
| `iss` | equals the pinned issuer exactly |
| `alg` | in the allow-list (default `RS256`, `ES256`); `none` and HMAC can never be configured |
| `kid` | resolved from the cached key set; a kid-less token only against a one-key set |
| signature | go-oidc, plus the JWK's own `alg` when it pins one |
| `exp` / `nbf` / `iat` | `exp` required; 60 seconds of clock tolerance |
| `aud` | required; any of `WithAudiences` |

The first four steps never touch the network.

## Key set behavior

- At most one JWKS fetch per issuer per 30 seconds (the refetch floor),
  shared by concurrent requests, bounded by a 10-second timeout and a 1 MiB body.
- A failed fetch keeps the last good key set: known kids keep verifying.
- An unknown kid after a successful fetch is a rejection (`ErrUnknownKey`, 401
  upstream); an unknown kid while the issuer is unreachable is
  `ErrKeysUnavailable`, which matches `sdk.ErrUnavailable` (503 upstream).
- A known kid refreshes a key set older than 15 minutes in the background.
- Kids confirmed absent are cached negatively (bounded LRU); unknown-kid
  rejections are logged once per issuer per minute with a suppressed count.

## oidctest

`gooidc/oidctest` is a standard-library-only local issuer for tests and
development. `Start` serves discovery and a JWKS on a loopback server; `New`
plus `Handler` lets a host serve it under its own URL. `Mint` signs claims,
`MintAs` signs under a second issuer string so a host can stand in for a
provider session cookie, and `Rotate`, `Retire`, `SetJWKSAvailable`,
`MintWithUnpublishedKey` and `MintUnsigned` drive rotation, outage and
rejection cases.

## Live leg

`go test -tags=live ./...` with `OIDC_LIVE_ISSUER`, `OIDC_LIVE_AUDIENCE` and
`OIDC_LIVE_TOKEN` verifies one real provider token through discovery; without
them it skips loudly.
