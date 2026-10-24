# integrations/authentication/firebase

The Firebase Authentication preset for `sdk/capabilities/authentication`
(imported as `sdkauth`). It isolates the Firebase Admin Go SDK
(`firebase.google.com/go/v4`) and the Google client companions its API forces.
Package name: `firebase`.

```go
client, err := firebase.NewClient(ctx, firebase.ClientConfig{ProjectID: projectID}) // ADC
if err != nil {
	return err
}
cfg := firebase.Config{ProjectID: projectID, Mode: mode}
idTokens, err := firebase.New(ctx, client, cfg)             // Authorization: Bearer <ID token>
cookies, err := firebase.NewSessionCookie(ctx, client, cfg) // the server-minted session cookie
```

Both values are `sdkauth.Provider` and `sdkauth.RevocationChecker`. The host
wires them in `cmd`; nothing in a pocket names Firebase.

## Surface

| Member | Behavior |
|---|---|
| `NewClient(ctx, ClientConfig) (*auth.Client, error)` | Admin SDK auth client. `CredentialsJSON` empty uses Application Default Credentials (which honor `GOOGLE_APPLICATION_CREDENTIALS`); set, it must be a service-account key. No request at construction. Hosts holding a `*firebase.App` pass `app.Auth(ctx)` instead. |
| `New(ctx, client, Config) (*Provider, error)` | ID tokens; `Issuer()` is `https://securetoken.google.com/<project>`. |
| `NewSessionCookie(ctx, client, Config) (*Provider, error)` | Session cookies; `Issuer()` is `https://session.firebase.google.com/<project>`. Their keys are x509 certificates at `https://www.googleapis.com/identitytoolkit/v3/relyingparty/publicKeys`, not a JWKS, which is why this module uses the Admin SDK. |
| `Provider.Authenticate(ctx, sdkauth.Credential)` | 16 KiB cap, then the unverified `iss` must equal `Issuer()` (`ErrIssuerMismatch`), then the Admin SDK verifies, then `Identity` is built. |
| `Provider.CheckRevocation(ctx, issuer, subject)` | One `GetUser`: `Disabled`, and `ValidAfter` from `TokensValidAfterMillis`. Accepts either issuer of the project. An unknown or empty subject is `ErrUnknownSubject` (matches `sdk.ErrNotFound`). |
| `Provider.MintSessionCookie(ctx, idToken, ttl)` | Firebase `createSessionCookie`; `ttl` from 5 minutes to 14 days (`ErrInvalidSessionTTL`). A refused ID token is `ErrInvalidCredential`. |
| `Profile(projectID) sdkauth.Profile` | For hosts verifying ID tokens with a generic OIDC verifier (`cmd` composes it with `integrations/cryptids/go-oidc`, issuer `IDTokenIssuer(p)`, audience `p`). Header-only: no revocation facts and no session cookies. It produces the same `Identity` as `New`. |
| `IDTokenIssuer(p)`, `SessionCookieIssuer(p)` | The two issuer strings. |
| `DefaultDenySignIn()` | `["anonymous"]`. |
| `ErrEmulatorInProduction`, `ErrInvalidConfig` | Construction refusals; both match `sdk.ErrInvalidInput`. |

`Config` has `ProjectID` (required, pins `iss` and `aud`), `Mode`
(`environment.Mode`, required), `DenySignIn` and `Logger`.

## Identity

`Identity` is built from the verified `*auth.Token` struct fields: `Issuer`,
`Subject`, `AuthTime` (else `IssuedAt`), `Expires`, `Firebase.SignInProvider`,
`Firebase.Tenant`. Profile claims come from `Token.Claims`: `email`,
`email_verified`, `phone_number`, `name`. MFA is `firebase.sign_in_second_factor`
being present, read from `Claims["firebase"]` because the Go SDK's
`FirebaseInfo` has no field for it (v4.22.0). `Methods` stays empty: Firebase
issues no `amr`. `Attributes["firebase.sign_in_provider"]` carries the sign-in
provider (`password`, `google.com`, `phone`, …).

- **Phone is trusted.** Firebase sets `phone_number` only after SMS possession,
  so a present phone is `PhoneVerified`.
- **Anonymous users are denied** (`ErrSignInMethodDenied`): `DenySignIn` nil
  means `DefaultDenySignIn()`. A non-nil empty slice denies nothing; consumers
  of this release cannot provision an identifier-less user anyway. An
  anonymous-to-email upgrade keeps the uid, so the first accepted token is the
  upgraded one.
- **Tenants:** `Identity.Tenant` is `firebase.tenant`. Consumers pin tenants;
  this module accepts any tenant of the project.

### What `Token.Claims` carries

Verified against the Auth emulator (firebase-tools 15.34.0) and the SDK
source: the Admin SDK decodes the whole payload into `Claims` and deletes only
`iss`, `aud`, `exp`, `iat`, `sub` and `uid`. Everything else stays, including
`auth_time`, `user_id`, `email`, `email_verified`, `name`, `phone_number`,
`firebase` (a map with `sign_in_provider`, `identities`, and
`sign_in_second_factor`/`tenant` when present) and any custom claims. The
module still prefers the struct fields where they exist. An email/password
emulator token's `Claims` keys were
`[auth_time email email_verified firebase name user_id]`.

## Revocation

`CheckRevocation` returns facts, not a verdict. The consumer compares
`ValidAfter` with each token's `AuthenticatedAt`, so a cached answer never
locks out a user who signed in again. Firebase stores revocation with
one-second granularity and treats a token issued in the revocation second as
valid. `ValidAfter` is the user's `validSince`: the last `RevokeRefreshTokens`,
else account creation, so it is rarely zero.

Without a revocation check, an ID token stays valid until it expires (one
hour) and a session cookie until its own expiry, even after the user is
disabled or revoked. The Admin SDK's own `…AndCheckRevoked` calls are not used:
the consumer caches facts instead of paying a `GetUser` per request.

## Errors

| Situation | Error |
|---|---|
| Bad, expired, revoked, foreign or tampered credential | `sdkauth.ErrInvalidCredential` (`ErrIssuerMismatch`, `ErrSignInMethodDenied` match it) |
| Google unreachable, 5xx, certificate fetch failure | `*sdkauth.UnavailableError` kind `network` / `upstream` |
| Deadline or cancellation | kind `deadline` |
| Google refused THIS process's credential: a rotated, deleted or wrong key, or a missing IAM permission (token endpoint 4xx, API 401/403) | kind `rejected_credential` — an operator problem, not a Google outage |

Every outage matches `sdk.ErrUnavailable` and never `ErrInvalidCredential`. An
unexplained `GetUser` failure is reported as an outage, never as "valid".
Error strings never contain the credential. The Admin SDK retries 503s and
network errors itself (up to four times with backoff); bound requests with a
context deadline.

## Credentials and permissions

- **ADC first.** On Cloud Run, GKE or any GCP workload, attach a service account
  and set no key. Off GCP, point `GOOGLE_APPLICATION_CREDENTIALS` at a key file
  or pass `ClientConfig.CredentialsJSON` from a secret store. `.env.example`
  holds placeholders only.
- **Secrets.** `ClientConfig.CredentialsJSON` is an `sdk.Secret`: it renders
  as `[redacted]` through `fmt`, `slog` and `encoding/json`, and key bytes
  convert with `sdk.Secret(string(b))`.
- **Least privilege.** Use a dedicated service account, never the default
  `firebase-adminsdk-*` account (it holds broad admin roles). Token
  verification needs no permission (public keys). `CheckRevocation` needs
  user read (`firebaseauth.users.get`); `MintSessionCookie` needs session-cookie
  creation (`firebaseauth.users.createSession`). **Unverified:** both
  permission names come from the Firebase IAM docs and have not been exercised
  against a real project (the emulator ignores credentials).
- **Never logged or kept.** Raw tokens, session cookies and the `GetUser`
  record (email, phone, provider UIDs, custom claims) are never logged or
  cached; only `Disabled` and `TokensValidAfterMillis` are read.

## Emulator mode

When `FIREBASE_AUTH_EMULATOR_HOST` is set the Admin SDK talks to the Auth
emulator and accepts **unsigned** tokens (`alg: none`) without any signature
check. `New` and `NewSessionCookie` therefore **refuse** construction in
`environment.ModeProduction` while the variable is set
(`ErrEmulatorInProduction`), and log a warning in development. The SDK reads
the variable when the client is built and this module reads it at provider
construction; keep it out of production environments entirely. In emulator
mode the SDK also checks revocation on every verification.

## Testing

```sh
go test ./...                                   # hermetic
make dev-firebase                               # from the repo root
FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 go test -tags=emulator ./...
make dev-firebase-down
FIREBASE_PROJECT_ID=… GOOGLE_APPLICATION_CREDENTIALS=… FIREBASE_LIVE_ID_TOKEN=… go test -tags=live ./...
```

- **Hermetic:** the real Admin SDK in emulator mode against an in-process fake
  of the Identity Toolkit endpoints. `authenticationtest.RunProvider` and
  `RunRevocation` over both providers, identity mapping (struct fields over
  the claims map), `Profile`/`NewOIDC` parity on a recorded emulator payload,
  cheap rejections with no SDK call, anonymous denial, construction and the
  production refusal, error classification, session-cookie minting.
- **Emulator** (`-tags=emulator`, skips loudly without
  `FIREBASE_AUTH_EMULATOR_HOST`; `FIREBASE_PROJECT_ID` defaults to
  `demo-gopernicus`): users and tokens come from the emulator's REST API.
  Email and phone sign-in → `Identity`; mint and verify a session cookie;
  revocation facts; a token minted before `RevokeRefreshTokens` reads revoked
  while a new sign-in does not; disabled users; anonymous users denied (header
  and cookie); production construction refused; both conformance suites. It
  proves plumbing only: the emulator's tokens are unsigned, so verification
  against Google's real keys is not exercised. The signed-JWT path is proven
  generically by `integrations/cryptids/go-oidc`.
- **Live** (`-tags=live`, skips loudly without its variables): one real ID
  token in production mode, a minted and verified session cookie, revocation
  facts; `FIREBASE_LIVE_REVOKE=1` also revokes the user's refresh tokens and
  proves the token reads revoked. Not run for v0.1.0.
