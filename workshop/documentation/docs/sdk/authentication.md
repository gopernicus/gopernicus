---
title: Authentication capability
description: The external identity provider port — Provider, Identity, Profile, NewOIDC and the conformance suite — and the WebAuthn relying-party port for passkeys.
---

# Authentication capability

`sdk/capabilities/authentication` (sdk v0.14.0) is the port for external
identity providers. It is imported as `sdkauth`, beside the authentication
pocket's own packages. A `Provider` turns a presented `Credential` into a
verified `Identity`: what the issuer says about who presented it, and how well.

| Type | Role |
|---|---|
| `Credential` | `Transport` (`header` or `cookie`) and `Raw`; `Raw` is never logged |
| `Identity` | `Issuer`, `Subject`, `Email`/`EmailVerified`, `Phone`/`PhoneVerified`, `DisplayName`, `Tenant`, `AuthenticatedAt` (`auth_time`, else `iat`), `ExpiresAt`, `Methods` (`amr`), `MFA` (see [MFA](#mfa)), `Attributes` (string-valued, at most `MaxAttributes` = 16) |
| `Provider` | `Issuer()` and `Authenticate(ctx, Credential) (Identity, error)` |
| `RevocationChecker` | optional: `CheckRevocation(ctx, issuer, subject)` returns `RevocationFacts` (`Disabled`, `ValidAfter`, `CheckedAt`) |
| `ClaimMapping`, `Profile` | the vendor-neutral claim mapping `NewOIDC` uses: dotted paths with OIDC defaults, `TrustPhone`, `DenySignIn` |
| `NewOIDC(issuer, verifier, profile)` | the one generic, stdlib-only provider over a `cryptids.JWTVerifier` |

**`Identity` carries no `sdk.Principal` and no local user id.** Mapping an
external subject to the host's principal belongs to the identity owner. In this
framework that is `pockets/authentication`, which consumes the port; it is not
the implementation of record. Implementations are `NewOIDC` here and the
integrations.

**Provider claims are identity data, never permissions.** `ClaimMapping` has no
field for roles, groups or permissions. `Attributes` holds the string-valued
`Extra` paths a host chooses to map in its own code.

## NewOIDC

```go
verifier, err := gooidc.New(ctx, issuer, gooidc.WithAudiences(apiAudience))
if err != nil {
	return err
}
provider := sdkauth.NewOIDC(issuer, verifier, sdkauth.Profile{
	Claims: sdkauth.ClaimMapping{Tenant: "org_id", Extra: []string{"azp"}},
})
```

`Authenticate` rejects oversize (16 KiB), malformed and foreign-issuer tokens
before it calls the verifier. It then re-checks `iss` on the verified claims,
maps them and applies `DenySignIn`. Defaults: `sub`, `email`, `email_verified`,
`phone_number`, `phone_number_verified`, `name`, `auth_time` (falling back to
`iat`) and `amr`. `MFAFlag`, `Tenant` and `SignInMethod` are optional paths.
`NewOIDC` panics on an empty issuer, a nil verifier, or `DenySignIn` without a
`SignInMethod` path, which would otherwise silently admit every method.

The JWT verifier is the plain port `cryptids.JWTVerifier` in `sdk/pkg/cryptids`.
[go-oidc](../integrations/go-oidc.md) is its integration. Vendor presets
([Firebase](../integrations/firebase.md), [Auth0](../integrations/auth0.md))
supply `Profile` values or whole providers, and no field or default in the
capability names a vendor.

## MFA

`Identity.MFA` means the same for every `Provider`: the provider asserted
authentication with at least two factor categories, or an explicit MFA claim or
flag. A one-factor sign-in leaves it false. Firebase's
`firebase.sign_in_second_factor` claim meets it.

`NewOIDC` sets `MFA` from the `MFAFlag` path or from `amr`. Since sdk v0.19.0
the default amr rule is strict, over the IANA amr registry sorted into factor
categories. `MFA` is set by `mfa`, or by values from at least two categories:

- knowledge: `pwd`, `pin`, `kba`
- possession: `otp`, `sms`, `tel`, `hwk`, `swk`, `sc`, `pop`
- inherence: `fpt`, `face`, `iris`, `retina`, `vbm`

One factor alone (`["otp"]`, `["hwk"]`) is not MFA. Any other value (`user`,
`geo`, `rba`, a vendor value) is no factor, and matching is case-sensitive.

`ClaimMapping.MFAMethods` is the per-provider hatch. When it is set, any listed
value alone sets `MFA` and no other value does.
`MFAMethods: []string{"mfa", "otp", "hwk", "sms", "swk", "pop"}` restores the
sdk v0.18.0 default exactly. List a value only when the issuer emits it after a
second factor. An empty list means the strict rule. The Auth0 preset keeps the
defaults (Auth0 emits `["mfa"]`), and Firebase sets the flag. An IdP whose
passkey or push sign-in emits a single possession value reads as one factor.
The [authentication pocket](../pockets/authentication.md#assurance-and-mfa)
turns `MFA` into the credential's assurance.

## Errors

| Error | Meaning |
|---|---|
| `ErrInvalidCredential` | wraps `sdk.ErrUnauthorized`: bad, expired, foreign or denied credential |
| `ErrIssuerMismatch`, `ErrSignInMethodDenied` | wrap `ErrInvalidCredential` |
| `ErrProviderUnavailable` | wraps `sdk.ErrUnavailable`; an `*UnavailableError` names its `Kind` (`network`, `upstream`, `deadline`, `rejected_credential`) for logs |

An outage is never reported as an invalid credential, so a consumer can answer
503 rather than a false 401.

## Revocation facts

A `RevocationChecker` returns facts, not a verdict. The consumer compares
`ValidAfter` with each token's `AuthenticatedAt`, so a cached answer never locks
out a subject who signed in again. An unknown subject is `Disabled` or
`sdk.ErrNotFound`, never a silent "valid".

## Conformance

`authenticationtest.RunProvider(t, newProvider, mint)` pins a valid token's
`Identity` (`Issuer` equals `Issuer()`), wrong issuer → `ErrIssuerMismatch`,
expired → `ErrInvalidCredential`, oversize rejected without calling the
verifier, `Methods` cloned, and no fabricated `Identity` on error.
`authenticationtest.RunRevocation` pins the checker contract. go-oidc runs
`RunProvider` against a real JWKS server, and the Firebase preset runs both
suites hermetically and against the Auth emulator.

See the [authentication pocket](../pockets/authentication.md) for how a host
wires providers, and the pocket README's "External identity providers" section
for per-vendor recipes.

## WebAuthn (passkeys)

`sdk/capabilities/authentication/webauthn` (sdk v0.20.0) is the relying-party
port for passkeys, so a consumer never imports a WebAuthn library. It sits
inside the authentication subsystem but imports nothing from its parent: root
`sdk` and `sdk/pkg/web` only. A host that wires passkeys does not link the
external-provider port, and the reverse. There is no default implementation;
the implementation of record is the
`integrations/cryptids/go-webauthn` adapter (`gowebauthn.New`), and
`webauthntest` ships a software authenticator and the conformance suite.

| Type | Role |
|---|---|
| `Config` | `RPID` (the domain credentials are scoped to: lowercase DNS labels, no scheme, port or path, not an IP), `RPName`, `Origins` (a `web.OriginPolicy`), `Timeout` (zero selects `MaxTimeout` = 5m; above it is refused) |
| `RelyingParty` | `Config()`, `BeginRegistration`/`FinishRegistration`, `BeginAssertion`/`ReadAssertion`/`FinishAssertion` |
| `Ceremony` | `Options` (the inner publicKey JSON the browser's `parse*OptionsFromJSON` takes), `State` (kept server-side, presented to Finish at most once), `ExpiresAt` |
| `Credential`, `Assertion` | the verified result: id, COSE public key, AAGUID, sign count, transports, and this ceremony's `UserVerified`, `BackupEligible`, `BackupState`; `Assertion.CounterRegressed` |
| `Requirement` | `required`, `preferred` (the zero value) or `discouraged`, the user-verification wire strings |

- **`Config.Validate`** runs before anything else in every implementation, so a
  bad configuration fails boot in every mode. Every origin host must equal
  `RPID` or sit under it (`ErrOriginOutsideRPID`, naming the host); an empty
  `Origins` and an empty `RPName` are refused. **The RP ID is permanent:**
  changing it orphans every registered credential.
- **Port rules.** Attestation is `none` and `residentKey` is `required`, so
  every assertion is discoverable: the options carry an empty
  `allowCredentials`, and no credential id leaves the server before the user is
  authenticated. The algorithms are ES256, EdDSA and RS256. The flags and the
  sign count come from this ceremony's authenticator data, never the stored
  record.
- **Errors.** `ErrCeremonyRejected` (wraps `sdk.ErrUnauthorized`) covers every
  failed response or state. A response that passes every check but lacks user
  verification under `required` is `ErrUserVerificationRequired` (also
  `sdk.ErrUnauthorized`). A regressed counter is not an error: the caller reads
  `CounterRegressed` and decides.
- **`sdk/pkg/web`** gains `OriginPolicy.Allows(origin)` (one serialized origin,
  as a browser writes it into `clientDataJSON`) and `Domains()` (every host the
  policy admits), which `Validate` uses.
- **Conformance.** `webauthntest.Run(t, factory)` pins registration and
  assertion with and without user verification under `preferred` and
  `required`, two credentials of one user, origin admission, every rejection
  class, extension outputs, counter regression and each `Validate` refusal,
  over a stdlib ES256 authenticator (`webauthntest.NewAuthenticator`). The
  go-webauthn adapter runs it.

The [authentication pocket](../pockets/authentication.md#passkeys) turns a
user-verified passkey into a native AAL2 session.
