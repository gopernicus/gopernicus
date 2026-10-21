---
title: Firebase Authentication preset
description: Verify Firebase ID tokens and session cookies, read revocation facts and mint session cookies.
---

# Firebase Authentication preset

`integrations/authentication/firebase` (package `firebase`) implements
`sdkauth.Provider` (`sdk/capabilities/authentication`) and `sdkauth.RevocationChecker`
over the Firebase Admin Go SDK (`firebase.google.com/go/v4`). One Admin SDK
client backs two providers: ID tokens, and server-minted session cookies, whose
x509 keys are not a JWKS.

```go
client, err := firebase.NewClient(ctx, firebase.ClientConfig{ProjectID: projectID}) // ADC
if err != nil {
	return err
}
cfg := firebase.Config{ProjectID: projectID, Mode: mode}
idTokens, err := firebase.New(ctx, client, cfg)             // iss https://securetoken.google.com/<p>
cookies, err := firebase.NewSessionCookie(ctx, client, cfg) // iss https://session.firebase.google.com/<p>
```

| Member | Behavior |
|---|---|
| `Authenticate` | 16 KiB cap and an exact `iss` check before the SDK; `Identity` built from the verified token's struct fields |
| `CheckRevocation` | one `GetUser`: `Disabled` and `ValidAfter` (`TokensValidAfterMillis`); unknown subject → `sdk.ErrNotFound` |
| `MintSessionCookie(ctx, idToken, ttl)` | 5 minutes to 14 days; the host's exchange handler checks `auth_time` freshness and the request origin first |
| `Profile(projectID)` | for header-only hosts verifying ID tokens through [go-oidc](go-oidc.md); same `Identity`, no revocation, no cookies |

- **Denied by default:** anonymous users (`DenySignIn` nil → `["anonymous"]`).
- **Phone trusted:** Firebase sets `phone_number` only after SMS possession.
- **MFA:** `firebase.sign_in_second_factor` present. Firebase issues no `amr`.
- **Errors:** a bad, expired or revoked credential is `ErrInvalidCredential`;
  outages are `*UnavailableError` (`network`, `upstream`, `deadline`), and a key
  Google rejects (rotated, deleted, missing permission) is `rejected_credential`.
- **Credentials:** Application Default Credentials first; off GCP, a dedicated
  least-privilege service account, never `firebase-adminsdk-*`. Raw tokens,
  cookies and `GetUser` records are never logged or cached.
- **Emulator:** with `FIREBASE_AUTH_EMULATOR_HOST` set the SDK accepts unsigned
  tokens, so production construction refuses it and development logs a warning.

## Testing

Hermetic tests run the real Admin SDK against an in-process fake. The emulator
leg uses `make dev-firebase` (`workshop/dev/firebase`: pinned firebase-tools,
Auth emulator only, project `demo-gopernicus`):

```sh
make dev-firebase
FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 go test -tags=emulator ./...
make dev-firebase-down
```

It proves plumbing (sign-in → `Identity`, session cookies, revocation facts,
anonymous denial, the production refusal, the conformance suites), not
signatures: emulator tokens are unsigned. The `-tags=live` leg against a real
project skips loudly without `FIREBASE_PROJECT_ID`,
`GOOGLE_APPLICATION_CREDENTIALS` and `FIREBASE_LIVE_ID_TOKEN`.
