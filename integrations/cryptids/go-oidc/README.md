# integrations/cryptids/go-oidc

Bearer-JWT verification for tokens from an external OpenID Connect issuer,
using `github.com/coreos/go-oidc/v3`. `Verifier` implements
`sdk/pkg/cryptids.JWTVerifier`. Hosts construct one per issuer in `cmd` and
hand it to `sdk/capabilities/authentication.NewOIDC`, which maps claims to an
`Identity`:

```go
verifier, err := gooidc.New(ctx, "https://issuer.example.com/",
	gooidc.WithAudiences("https://api.example.com"))
provider := sdkauth.NewOIDC("https://issuer.example.com/", verifier, sdkauth.Profile{})
```

The package name is `gooidc`. It owns verification only: claim mapping,
subject resolution and every authentication policy belong to `NewOIDC` and the
consumer. It is distinct from `integrations/oauth/google`, which validates
nonce-bound ID tokens inside a login flow.

## Surface

| Member | Behavior |
|---|---|
| `New(ctx, issuer string, opts ...Option) (*Verifier, error)` | Pins `issuer` (exact string). Runs OIDC discovery under `ctx` unless `WithJWKSURL` is set, in which case it does no network I/O. A discovered issuer must be `https`, or `http` on loopback (see Issuer transport). Keys are fetched on the first token. |
| `WithAudiences(aud ...string)` | Required. A token is accepted when `aud` (string or array) contains any of them. |
| `WithJWKSURL(url string)` | Skips discovery. `https`, or `http` on loopback only. |
| `WithInsecureIssuer()` | Development only. Lets `New` discover a plain-`http` issuer on a non-loopback host, with one WARN. |
| `WithAlgorithms(alg ...string)` | Replaces the allow-list (default `RS256`, `ES256`). RS/PS/ES 256–512 and `EdDSA` only; `none` and HMAC fail `New`. |
| `WithHTTPClient(*http.Client)` | Client for discovery and JWKS. The default times out after `FetchTimeout` and refuses redirects. |
| `WithLogger(*slog.Logger)` | Fetch failures and aggregated unknown-kid rejections. Nil uses `slog.Default()`. |
| `Verifier.Verify(ctx, token) (map[string]any, error)` | Returns verified claims; JSON numbers are `float64`. |
| `Verifier.Issuer() string` | The pinned issuer. |
| `ErrInvalidToken` | Every rejection; matches `sdk.ErrUnauthorized`. |
| `ErrUnknownKey` | The kid is not published after a fetch; matches `ErrInvalidToken`. |
| `ErrKeysUnavailable` | The issuer's keys could not be obtained; matches `sdk.ErrUnavailable` and says nothing about the token. |

## Issuer transport

Discovery fetches the issuer's metadata, which names the key set, so the
channel to the issuer is part of the trust chain. `New` therefore checks the
issuer's scheme only when it will discover (no `WithJWKSURL`):

| Issuer | Discovery | `WithJWKSURL` pinned |
|---|---|---|
| `https://…` | accepted | accepted |
| `http://` on loopback (`localhost`, `127.0.0.1`, `::1`) | accepted | accepted |
| `http://` on any other host | refused with `sdk.ErrInvalidInput` | accepted |
| `http://` on any other host + `WithInsecureIssuer()` | accepted, one WARN | accepted, no WARN |

With a pinned JWKS URL nothing is fetched from the issuer: the string is only
compared to `iss`, and the JWKS URL itself must be `https`, or `http` on
loopback. `WithInsecureIssuer()` waives the scheme rule for discovery only. A
`jwks_uri` discovered over plain `http` is admitted over `http` too, since it
is no more trustworthy than the channel that named it; a pinned JWKS URL is
never waived. Use the option for a container-hostname issuer in development
(`http://keycloak:8080`); the authentication pocket refuses `http` issuers in
production regardless.

## Verification order

Cheap rejections come first and never touch the network:

1. size: at most `MaxTokenBytes` (16 KiB);
2. shape: three strict base64url segments with JSON header and payload;
3. `iss` equals the pinned issuer exactly;
4. `alg` is in the allow-list, so `alg: none` (an auth emulator's unsigned
   token) and HMAC are refused here;
5. kid lookup in the cached key set, fetching only as described below;
6. signature through go-oidc (`IDTokenVerifier` over a one-key `StaticKeySet`,
   with the same allow-list), and the JWK's own `alg` when it pins one;
7. `exp` required, `nbf` and `iat` optional, all NumericDates in calendar
   years 0001–9999, each with `ClockTolerance` (60 s);
8. any-of audience.

A token without `kid` resolves only against a key set of exactly one key.

## Key set

One key set per `Verifier`; share one Verifier per issuer.

- **Refetch floor.** At most one JWKS fetch attempt per `RefetchFloor` (30 s),
  successful or not. Inside the floor an unknown kid is rejected without a
  fetch. A spray of 1,000 distinct kids costs one fetch per window.
- **Single flight.** Concurrent requests for an unknown kid share one fetch.
  A known kid never waits on the network.
- **Bounded fetch.** Each fetch runs under `FetchTimeout` (10 s) of its own,
  so one caller's cancellation does not fail the others, and reads at most
  `MaxJWKSBytes` (1 MiB). Non-200, oversize, malformed and keyless answers
  are failures. Usable keys: RSA of at least 2048 bits, EC P-256/384/521 and
  Ed25519, with `use` absent or `sig`.
- **Last good keys.** A failed fetch never replaces the key set. Known kids
  keep verifying through an outage; an unknown kid while the last attempt
  failed is `ErrKeysUnavailable` (an outage, 503 upstream), never a rejection.
- **Refresh.** After `KeySetMaxAge` (15 min) a known kid triggers a
  background refresh (floor permitting), so a withdrawn key stops verifying.
- **Negative kid cache.** A kid absent from a fetch the same request started
  is remembered for 5 minutes (LRU, 256 entries), so one repeated bogus kid
  cannot spend a fetch every window. The floor is the real defense.
- **Logging.** Unknown-kid rejections log at most once per issuer per minute,
  carrying the count suppressed in the previous window. Fetch failures log once
  per attempt (at most one per floor). Tokens and claims are never logged.

## oidctest

`oidctest` is a local issuer for tests and development, standard library only:

| Member | Behavior |
|---|---|
| `Start(opts...)` | Serves discovery and JWKS on a loopback `httptest` server; its URL is the issuer. `Close` stops it. |
| `New(issuer, opts...)` + `Handler()` | Same, served by the caller at `issuer` (`/.well-known/openid-configuration`, `/.well-known/jwks.json`). |
| `WithAlgorithm("RS256" \| "ES256")`, `WithAudience(aud)` | Signing algorithm (default RS256) and default `aud` (`oidctest`). |
| `Mint(claims)` | Signs with the current key, adding `iss`, `aud`, `iat`, `exp` (+10 min) only when absent. `Omit` as a value drops a claim. |
| `MintAs(issuer, claims)` | The same keys under a second issuer string, to stand in for a provider session cookie: wire a second Verifier with `WithJWKSURL(JWKSURL())`. |
| `MintWithUnpublishedKey(kid, claims)`, `MintUnsigned(claims)` | Tokens a verifier must reject: an unpublished key, and `alg: none`. |
| `Rotate()`, `Retire(kid)`, `SetJWKSAvailable(bool)`, `JWKSRequests()` | Rotation, key withdrawal, outage simulation and fetch counting. |

Keys are generated in memory on every construction; it is never a production
issuer.

## Testing

Run `go test ./...`. Tests drive real HTTP round trips against `oidctest`:
`authenticationtest.RunProvider` over `NewOIDC` (RS256 and ES256); the issuer
transport table above, including the refusal before any request; the cheap
rejection order with zero fetches (including `alg: none` and HMAC); the
emulator-shaped unsigned token; signature, key and JWK-alg mismatches; time
claims at and past the tolerance; any-of audience; rotation and withdrawal;
last-good keys through an outage and the Unavailable classification; cold
outages, oversize, malformed, keyless, weak-key, 5xx and redirect answers;
fetch and caller timeouts; single flight; the 1,000-kid spray with aggregated
logging; and the negative kid cache.

The live leg verifies one real provider token through discovery and is
skipped loudly unless all three variables are set:

```sh
OIDC_LIVE_ISSUER=… OIDC_LIVE_AUDIENCE=… OIDC_LIVE_TOKEN=… go test -tags=live -run TestLiveTenant ./...
```
