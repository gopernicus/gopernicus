---
title: Authentication
description: Identity, sessions, credentials, OAuth, recovery, delivery, and optional HTML.
---

# Authentication

`pockets/authentication` is a datastore-free identity pocket for human and machine principals. It owns users, multiple identifiers, credentials, sessions, challenges, recovery, OAuth linking, API keys, invitations, security events, and optional user administration.

It also implements `sdk.IdentityResolver` and exposes middleware other host routes and pockets can use.

The root constructor returns `Components` with named `Authentication`,
`Invitations`, `Delivery` and `HTTP` fields. Real use cases live in public
`logic/authentication`, `logic/invitations` and `logic/delivery`; middleware,
cookies, views and route registration live in public `inbound/http`. Hosts can
construct these components directly or use the root's validated assembly.
Private credential-proof helpers remain private. Invitations is nil when disabled.

## Identity is larger than email

A user can have multiple email or phone identifiers. Each verified identifier carries independent uses:

- login;
- recovery;
- notification;
- primary within its kind.

Email normalization is trim + lowercase; phone normalization is strict E.164. Authentication claims are exclusive, while notification-only addresses may be shared. Multi-table identity changes use repository transactions and revision checks rather than service-level best effort.

## Surface

The claimed namespace is `/auth/*`. The JSON surface includes:

- registration, verification, login, refresh, logout, and current-session hydration;
- forgot/reset/change/set/remove password flows;
- step-up and credential/identifier management;
- passwordless code and magic-link flows when enabled;
- passkey registration, usernameless sign-in, step-up and management when `WithPasskeys` wires a relying party;
- authenticator-app (TOTP) enrollment, step-up and management, and single-use recovery codes, when `WithTOTP` wires an encrypter;
- OAuth login/link/unlink when providers are wired;
- service account and API key lifecycle routes when both repositories are wired and `MachineRoutesGate` is set (API-key authentication itself needs only the repositories);
- invitations when a granter and host authorization check are wired;
- user administration when the host explicitly supplies `UserAdminCheck`.

Optional subsystems are deny-by-absence: routes are not registered when their enabling collaborator is missing.

`BrowserConfig.Views == nil` keeps the pocket JSON-only. Supplying the `pockets/authentication/views/goth` adapter adds HTML pages and form handling without changing JSON contracts.

## Inbound access policy and data integrity

Every principal permission check runs at inbound, before the application service.
`authentication/inbound/http` owns `InviteCheck`, `UserAdminCheck` and their
request/action types. Root `WithInvitations` and `WithAdministration` wire these
callbacks into HTTP; direct HTTP construction uses `WithInviteCheck` and
`WithUserAdminCheck`. Logic stores no host access callback. Custom transports
perform their own admission checks.

For an invitation needing normalized recipient context, inbound calls
`PrepareCreate`, authorizes the inspected `PreparedCreate.Input()` and
`ResolvedSubjectID()`, then calls `CreatePrepared` with that same opaque value.
The service normalizes and resolves once; inspection cannot rewrite execution.
Plain `Create` and `ListByResource` remain policy-free service operations.

Invitation cancel/resend follows the same boundary: `PrepareManagement(ctx, id)`
returns the immutable target and issuer for inbound to check. After admission,
call `Cancel(ctx, prepared)` or `Resend(ctx, prepared, redirect)`. Execution is
bound to the originating service and observed token version; stale state is a
conflict. The bundled handlers deny another issuer before any write or delivery.
User-admin handlers call their policy before looking up the target account;
actor fields in service calls provide audit attribution only.

Services still enforce data validation, token/session consistency and recipient
identity proof. These establish valid authentication evidence or data, rather
than deciding what the caller may do. Quotas and last-active-admin rules require
an atomic operation owned by the relevant domain; a preliminary access check
cannot preserve them under concurrent writes. The authorization store's
`IntegrityPolicy` counts grants, not active user accounts.

Admission does not hold a permission snapshot through application execution.
Later revocation cannot retract admitted work. An invitation admitted at issuance
remains an expiring capability; acceptance does not recheck the inviter's current
permission. Its recipient proof, lifecycle and grant integrity still apply.

## Authentication proof

Credential changes atomically advance the user's authentication revision and revoke
sessions. Login and grant admission check that revision inside the store, so stale
password proof cannot create a fresh session after reset. Explicit recent-auth grants
enforce the requested age and assurance and require a live session.

Passwordless, recovery and credential-management proofs for an existing account
retain their issuing credential revision. A later credential change invalidates them; identifier confirmation also
requires the live session that started it. Password reset checks the original
verified recovery identifier inside the reset transaction.

Invitations require verified address ownership. Acceptance claims the invitation
before invoking the host granter; the host deduplicates its stable operation ID.
Retries resume the claim after ambiguous failures, while cancellation/resend cannot
replace an acceptance already in progress.

Browser login and refresh enforce Origin policy for JSON and forms. Native callers
remain supported. Logout accepts refresh proof or a verified, unexpired access token;
it reports revocation errors while clearing browser cookies.

The public service exposes passwordless, credential, identifier and step-up use cases
for custom host transports. The host supplies authenticated user/session IDs and
application authorization. `AuthenticationLimits` provides independent subject and
IP budgets for password login, reset starts and sensitive proofs.

Authentication SQL stores own their focused atomic operations; they do not generally
join host ambient transactions.

## Minimal development wiring

The required security choices are explicit. A password-and-email development host needs a password hasher, mail sender, token signer, deployment mode, challenge protector, delivery encrypter, and delivery mode in addition to complete core repositories. An OAuth-only host can disable password flows and delivery and omit the hasher, password repository and mailer. Production requires secure session cookies.

```go
authSvc, err := authentication.New(
    repos, signer, environment.ModeDevelopment, delivery.ModeInProcess,
    authentication.WithPassword(authentication.PasswordConfig{
        Hasher: bcrypt.New(),
    }),
    authentication.WithIdentity(authentication.IdentityConfig{
        ChallengeProtector: protector,
    }),
    authentication.WithDelivery(authentication.DeliveryConfig{
        Mailer: email.NewConsole(log),
        MailFrom: "auth@example.com",
        DeliveryEncrypter: deliveryKey,
    }),
    authentication.WithLogger(log),
)
if err != nil {
    return err
}

go func() {
    if err := authSvc.Delivery.Run(ctx); err != nil {
        log.ErrorContext(ctx, "authentication delivery stopped", "error", err)
    }
}()

if err := authSvc.HTTP.Register(pockets.Mount{
    Router: router,
    Logger: log,
    Events: bus,
}); err != nil {
    return err
}
```

This is a development outline, not a production recipe. See `examples/auth-cms/cmd/server` for complete construction and lifecycle handling.

Import `environment` from `sdk/pkg/environment` and `delivery` from
`pockets/authentication/logic/delivery`. Direct HTTP construction also requires
an explicit runtime mode; production requires secure cookies and a shared limiter.

## Constructor options

`New(repos, signer, runtimeMode, deliveryMode, opts ...Option)` keeps repositories,
the token signer and both required mode selections explicit. Optional policy is
supplied as a named group:

| Option | Group |
|---|---|
| `WithPassword` | `PasswordConfig`: hasher, password validation and login policy |
| `WithSessions` | `SessionsConfig`: access-token and refresh horizons |
| `WithIdentity` | `IdentityConfig`: proof protection, normalization, identifier keys and credential policy |
| `WithAbuseProtection` | `AbuseProtectionConfig`: limiter and abuse budgets |
| `WithDelivery` | `DeliveryConfig`: sender, encrypted dispatch and runtime settings |
| `WithMessages` | `MessagesConfig`: templates, branding, subjects and enrichment |
| `WithOAuth` | `OAuthConfig`: providers, token storage, callback and email trust policy |
| `WithPasswordless` | `PasswordlessConfig`: enabled kinds and provisioning policy |
| `WithLinks` | `LinksConfig`: public destinations and redirect allowlist |
| `WithBrowser` | `BrowserConfig`: cookies, origins, views and browser route policy |
| `WithInvitations` | `InvitationsConfig`: granter and host access checks |
| `WithPasskeys` | `PasskeyConfig`: the WebAuthn relying party, user verification, the per-user cap and the passkey pages (see [Passkeys](#passkeys)) |
| `WithAdministration` | `AdministrationConfig`: machine gate, user administration, list strategy and the `UserErasureRoute` opt-in |
| `WithMaintenance` | `MaintenanceConfig`: the retention policy (`maintenance.Config`) and `MaintenanceRunnerAcknowledged` |

`WithIDs` and `WithLogger` select optional host dependencies. Nil options return
an error; they are not omission markers. Each group replaces its complete value,
including zero fields. For example, assemble one `DeliveryConfig` with all needed
senders and encryption settings rather than applying two partial groups that
overwrite one another. Options snapshot retained configuration; borrowed services
and callbacks keep their existing ownership. No option can reconfigure a running
component. Conditional required wiring and production validation remain enforced.

Policy records retain relevant environment tags. Hosts parse their chosen records
and pass them through these options; there is no exported root `Config` bag.
Direct logic and HTTP constructors similarly take required collaborators first
and their own typed options afterward.

## Delivery modes

Authentication sends no provider message on the request path. Registration, recovery, passwordless, and similar producers submit opaque encrypted delivery commands.

| Mode | Behavior | Use |
|---|---|---|
| `in_process` | finite queue and worker pool; lost on crash; per-process de-duplication only | single-instance development and simple deployments |
| `jobs` | durable, cross-instance keyed/fenced work through the jobs pocket | production and multi-instance deployments |

The host owns either runtime. In jobs mode, authentication exposes delivery callbacks the host registers with `queue.NewFencedRuntime` from `pockets/jobs/logic/queue`; the pocket does not import jobs.

## Middleware and revocation

- `authenticationhttp.Adapter.RequirePrincipal(opts ...PrincipalOption)` is the one authenticator; `Accept`/`Transports` narrow which credential kinds and transports it admits, and named helpers like `RequireAccessToken()` and `RequireAPIKey()` are one-line pre-compositions of it.
- Liveness is per route: `LiveOnMutation()` (the default since v0.19.0) checks the session anchor on `POST`/`PUT`/`PATCH`/`DELETE` and lets safe reads pass on signature and expiry; `Live()` checks it on every request; `Stateless()` never does. A session-store read error is a 503 with `Retry-After`, never a false 401.
- browser-sensitive mutations apply an allowlisted Origin check and double-submit CSRF protection; since v0.19.1 every cookie mutation needs the `X-CSRF-Token` header whatever its content type, except the few form-lane routes whose handler checks a body token;
- API bearer callers do not need a browser CSRF cookie.
- the double-submit cookie follows the session cookie (since v0.27.0): a `Secure` session cookie (every production host) gets `__Host-auth_csrf`, unchanged; a development host with a non-`Secure` session cookie (plain `http://localhost`) gets `auth_csrf`, the same cookie without `Secure` and the prefix, because WebKit drops a `Secure` cookie over http. Boot logs `csrf_cookie=auth_csrf secure=false`. Production refuses it twice, independently (see [Production posture](../guides/production-posture.md)). SPAs keep reading `csrf_token` from the `GET /auth/csrf` body, never the cookie.

The access JWT is short-lived. The server-side session is the refresh and revocation anchor. Refresh tokens rotate within a fixed horizon rather than extending it indefinitely.

## External identity providers

Since v0.19 the pocket also admits tokens from an external identity provider
(Firebase Auth, Auth0, Clerk, Cognito, Okta, Keycloak) as ordinary principals.
The provider owns credentials: passwords, sign-in UI, reset, email
verification, MFA enrollment and its own sessions. The pocket owns identity:
the local user and its status, the `(provider name, subject) → user` mirror
(`external_subjects`), API keys, invitations and security events. Downstream
pockets still see only `sdk.Principal`.

- **Port and verifier.** [`sdk/capabilities/authentication`](../sdk/authentication.md)
  defines `Provider` and `Identity`; [go-oidc](../integrations/go-oidc.md) verifies
  any OIDC issuer's JWTs; [Firebase](../integrations/firebase.md) and
  [Auth0](../integrations/auth0.md) are presets.
- **Wiring.** An external-only host uses `authlogic.NewExternalAuthenticator`
  with `authenticationhttp.NewAuthenticator`, and needs no signer, sessions or
  mail. A hybrid host passes `auth.WithExternal(cfg)` to `New`.
- **Routing.** A token goes to exactly one provider, chosen by its `iss`. An
  unknown subject is provisioned only when the provider sets
  `ProvisionOnFirstVerification`, and a collision with an existing login email is
  denied and audited.
- **Revocation.** Mutations re-check local status and, where the provider can
  answer (Firebase), cached revocation facts compared with each token's
  `auth_time`. A provider outage is a 503 after a grace window, never a 401.
- **Server-rendered pages** use the provider's own session where one exists:
  Firebase session cookies, through a host exchange handler, and Clerk's
  `__session`. Auth0, Cognito, Okta and Keycloak are header-only.
- **Assurance.** `RequireMFA()`, `FreshAuth(d)`, `VerifiedEmail()`, `Tenant()`
  and `Check()` gate routes on provider facts. Provider roles are never read as
  permissions. `RequireMFA()` reads the assurance the provider asserted (see
  [Assurance and MFA](#assurance-and-mfa)).
- **Losses, by design.** Step-up, `/auth/sessions` and credential management
  need a native session, so provider users are denied there. Per-request
  sign-in events and the provider's own MFA events (enrollment, challenges)
  stay in the provider's log. The pocket records the assurance each token
  asserted and its own MFA denials.
- **Host CSRF.** An external-only host built on `NewAuthenticator` has no
  `/auth/csrf`; it protects its own cookie mutations with
  `http.CrossOriginProtection`. This applies to every host, native or
  external-only. The pocket's double-submit gate covers only the bundled
  `/auth/*` routes, so a host whose clients use the session cookie (such as a
  same-origin SPA) must wrap its own router in `http.CrossOriginProtection`.
  CORS alone is not CSRF protection. Known limitation: requests with neither
  `Origin` nor `Sec-Fetch-Site` pass, and `SameSite=Lax` is the backstop.

The pocket README's "External identity providers" section has the per-vendor
recipes and claim tables, the server-session support matrix, the revocation lag
table, the identity edge cases and the hash-export notes for leaving a
provider. The runnable proofs are `examples/external-identity` and
`examples/auth-cms` with `AUTH_EXTERNAL_DEMO=1`.

## Assurance and MFA

Since v0.25.0 (with sdk v0.19.0) a host on an identity provider can require
MFA for its administrators or for every user. The pocket enforces it from what
the provider asserted. Since v0.26.0 a user-verified [passkey](#passkeys) is
a native AAL2 source, and native access tokens carry their assurance. Since
v0.28.0 an [authenticator app or a recovery code](#totp-and-recovery-codes) is
the fallback native AAL2 source, and `MFAConfig.Methods` restricts which
sources the gates admit.

- **The stamp.** `Credential.Assurance` (`session.AssuranceLevel`) is what a
  credential proves. An external token is AAL2 when the provider set
  `Identity.MFA` (the sdk's [strict amr rule](../sdk/authentication.md#mfa)),
  and AAL1 otherwise. Unknown is never higher, and nothing produces AAL3.
  Every gate compares levels with `session.AssuranceLevel.AtLeast(min)`.
- **The `aal` claim** (since v0.26.0). Every native mint signs `aal` (`aal1` or
  `aal2`) into the access token from the session row, and
  `Credential.Assurance` reads it. A session is AAL2 only after a user-verified
  passkey sign-in or step-up, or after a TOTP or recovery-code step-up (since
  v0.28.0); password, code, magic-link and OAuth sessions are AAL1. A token minted before the upgrade carries no `aal` and reads `unknown`
  until it refreshes; a legacy session row without an assurance level omits
  the claim; an unknown `aal` value is rejected; a delegated token
  carrying `aal` is refused. API keys stay `unknown` (S4d).
- **The `methods` claim** (since v0.28.0). Every native mint also signs
  `methods`, the session row's method kinds (`password`, `passkey`, `totp`,
  `recovery_code`, …), beside `aal`, and `Credential.Methods` reads it. An
  unknown kind is rejected, and a delegated token carrying `methods` is
  refused, as for `aal`. An external token's source is `idp`.
- **`MFAConfig.Methods`** (since v0.28.0) restricts the AAL2 sources every MFA
  gate admits: `MFAMethodPasskey`, `MFAMethodTOTP` (recovery codes count under
  it) and `MFAMethodIdP`. Empty admits any source. A first-party AAL2 token
  minted before the claim existed has no source and is denied under a
  non-empty `Methods` until it refreshes, so leave `Methods` empty through the
  rollout. A host that refuses a possession code composed onto a passwordless
  or OAuth primary sets `Methods: [passkey, idp]`. A denial records
  `mfa_required_denied` with `reason` (`assurance` or `method`) and `method`.
- **Three gates require AAL2.** Each answers with the RFC 9470 challenge
  (`WWW-Authenticate: Bearer error="insufficient_user_authentication"`) and
  records one `mfa_required_denied` row. The row is budgeted per client IP and
  carries bounded Details, never an amr value, a claim or the token.

  | Gate | Scope |
  |---|---|
  | `RequireMFA()` on `RequirePrincipal` | access and external tokens on that route (since v0.26.0 it no longer panics beside access tokens; `FreshAuth`, `VerifiedEmail`, `Tenant` and `Check` still do). An API key passes. Beside `Browser()`, an AAL1 cookie session is redirected to the "Confirm it's you" chooser (`/auth/step-up/mfa`) when `TOTPConfig.Views` is wired, else to the passkey step-up page when `PasskeyConfig.Views` is |
  | `ExternalProvider.RequireMFA` | every route. It is checked before the subject resolves, so a denied token provisions nothing |
  | `WithMFA(MFAConfig{Required: MFARequiredAdmins})` | `/auth/admin/users*`, for callers the `UserAdminCheck` admits, and beside passkeys the machine lifecycle routes after `MachineRoutesGate`. A non-admin keeps the check's 403, and an API key passes |

- **`MFAConfig{Required}`** (`WithMFA`) is a wiring literal with no env tag.
  The zero value (`MFARequiredDefault`, since v0.26.0) resolves once at
  construction: to `admins` when `WithPasskeys` wires a relying party or
  `WithTOTP` an encrypter (since v0.28.0), and
  `AdministrationConfig.UserAdminCheck` is set, else to none.
  `MFARequiredNone` is now the explicit `"none"`, the opt-out and the sole-admin
  break-glass. `admins` gates the admin routes above, and `all` sets
  `RequireMFA` on a copy of every `WithExternal` provider. Some wiring cannot
  satisfy the requirement, and every mode refuses it with `ErrMFAUnsatisfiable`:
  any requirement without `WithExternal`, `WithPasskeys` or `WithTOTP`, `admins`
  without `AdministrationConfig.UserAdminCheck`, `all` beside native password,
  passwordless, OAuth, passkey or TOTP wiring (native `all` is the follow-on
  `s4c2-native-all`; use `admins` meanwhile), or a `Methods` entry naming a
  source that is not wired (`passkey` without `WithPasskeys`, `totp` without
  `WithTOTP`, `idp` without `WithExternal`). An unknown value is
  `sdk.ErrInvalidInput`. `New` logs one boot line, `auth: mfa posture
  mfa_required=<none|admins|all> source=<config|default>
  gates=<none|/auth/admin/users*[,machine_lifecycle]|external_tokens>
  sources=<passkey,totp,idp|none> methods=<any|…>`.
- **`admins` does not gate** role routes, cms admin routes or host admin pages.
  Add `RequireMFA()` to those yourself. Without passkeys it does not gate the
  machine lifecycle routes either (with passkeys or TOTP it does). API keys
  pass every MFA gate (S4d).
- **A host's own `CredentialService`** stamps `Assurance` itself: AAL2 when
  `Identity.MFA` is set, else AAL1. Without the stamp, every MFA gate denies its
  external tokens.
- **Re-authentication.** On `insufficient_user_authentication`, send the user
  back to the IdP with a forced second factor (the IdP's own step-up
  parameter, or `prompt=login`). A browser route behind
  `RequireMFA()` needs a login path that does this. Otherwise a live IdP
  session answers AAL1 again and the browser loops.

The pocket README's "Assurance model" section has the full gate table and the
self-check: after signing in with MFA, the newest `external_login` row's
Details must read `"assurance":"aal2"`. It also has the passkey caveat and the
per-IdP table of what each vendor emits. Auth0 and Firebase are known. Google,
Okta and Entra ID are **unknown**, so run the self-check. The production
refusals are in [Production posture](../guides/production-posture.md), under
"Added by S4a", "Added by S4b" and "Added by S4c".

## Passkeys

Since v0.26.0 (with sdk v0.20.0 and the `integrations/cryptids/go-webauthn`
adapter v0.1.0) native users can enroll a passkey and sign in or step up with
it. A **user-verified** passkey (the authenticator checked a PIN or biometric)
is the only native AAL2 factor; a presence-only passkey signs in at AAL1.
Sign-in is **usernameless only**: `residentKey` is `required`, assertions are
discoverable, and no credential id leaves the server before authentication, so
U2F-only security keys cannot enroll. Attestation is `none`; the AAGUID is
recorded when the client reports one, unverified.

- **Turn it on.** Apply migration `0025_passkey_credentials` (stores pgx
  v0.12.0, Turso v0.11.0). Since authentication v0.29.0 (stores pgx v0.14.0,
  Turso v0.13.0) it is required **only when passkeys are wired**: `New` probes
  a `Repositories.Passkeys` that implements `passkey.SchemaProber` and refuses
  with `ErrPasskeySchemaMissing`, naming 0025; an unwired host is never probed
  and never reads or erases the table. (From stores pgx v0.12.0 / Turso v0.11.0
  until then, the store probe refused every mode, passkeys wired or not.) Build the relying party with
  `gowebauthn.New(webauthn.Config{RPID, RPName, Origins})`; **the RP ID is
  permanent**, and changing it orphans every registered passkey. Wire
  `WithPasskeys(PasskeyConfig{RelyingParty: rp, Views: …})`. It needs
  `Repositories.Passkeys`, `CredentialMutations`, `AuthenticationGrants` and
  `Challenges` (`ErrPasskeyReposRequired`). `UserVerification` defaults to
  `preferred` (`discouraged` is refused) and `MaxPerUser` to 10 (at most 25).
- **Views and the script.** `PasskeyConfig.Views` (a `PasskeyViews`; views/goth
  v0.7.0's `authgoth.Views` satisfies it) mounts `GET /auth/account/passkeys`,
  its rename/remove form lane and `GET /auth/step-up/passkey`; nil keeps the
  routes JSON-only. With views/goth, mount `authgoth.PasskeyScriptHandler()` at
  `authgoth.DefaultPasskeyScriptPath` (`/assets/auth/passkey.js`); unmounted,
  the passkey controls stay hidden. The tag carries no nonce, so it loads under
  `HTMLPolicy`'s `script-src 'self'` and is blocked by a nonce-only CSP.
- **Ceremonies.** Each is a single-use, 5-minute handle on the challenge rail,
  bound to the caller's session where it has one. The first passkey bootstraps
  at AAL1 from a fresh primary login or a password/code step-up grant in the
  last five minutes; once a user holds a user-verified passkey, adding or
  removing one needs AAL2. `{"user_verification":"required"}` on the
  registration options raises that one ceremony to required (a biometric or
  PIN prompt) and leaves the user's presence-only passkeys out of
  `excludeCredentials`, so the same device can add a verified one. A passkey
  **step-up always requires user verification** (since v0.27.1): a
  presence-only response is refused (`401 passkey_rejected`), while sign-in
  keeps the host setting. Removing a passkey revokes every session of its user.
- **Wire contract.** Every `…/options` call takes `{}` and answers
  `{ceremony, public_key}` (`public_key` is what
  `PublicKeyCredential.parse*OptionsFromJSON` takes); every finish takes
  `{ceremony, credential}`, `credential` being `PublicKeyCredential.toJSON()`.
  Sign-in answers exactly as `POST /auth/login`; step-up adds `assurance`.
  Refusals: `410 challenge_expired`, `401 passkey_rejected`, `409
  passkey_exists`/`passkey_limit`, `403` when the registration gate needs a
  step-up. No response carries a public key, ceremony state or credential id of
  another user. SPAs read `GET /auth/methods` (which lists `passkeys`) on an
  admin 401 to choose their step-up.
- **Operators.** An AAL2 admin, or an admin API key the host's
  `UserAdminCheck` admits, resets a user's passkeys with
  `POST /auth/admin/users/{id}/passkeys/reset`. Admins enroll two passkeys;
  the sole-admin break-glass is `MFAConfig{Required: MFARequiredNone}` and a
  redeploy. Registration, removal and reset send a `passkey_change_notice` to
  the user's verified channels, the detective control for the AAL1 bootstrap.
- **Events.** `passkey_registered`, `passkey_removed`, `passkeys_reset`,
  `passkey_login`, `passkey_failed` (budgeted per IP) and
  `passkey_sign_count_regressed` (budgeted; a regressed counter is refused and
  the stored count kept). No row carries a public key, credential id, user
  handle, passkey name, origin or raw response.

The pocket README's "Passkeys (SOC 2 S4b)" section is the reference: the
ceremony table, the routes, the custom-view markup contract for hosts that
override a page, and the operator recipes. `examples/auth-cms` wires it end to
end.

## TOTP and recovery codes

Since v0.28.0 (with views/goth v0.8.0 and the `integrations/encoding/rsc-qr`
adapter v0.1.0; pin authentication v0.29.0 and views/goth v0.8.1) an
authenticator app is the native fallback second factor beside passkeys, with
ten single-use recovery codes beside it. TOTP is RFC 6238 in the pocket's
standard library code: SHA-1, six digits, 30 seconds, ±1 step. A TOTP or
recovery-code **step-up of a live session** re-mints it at AAL2 (the session
row, the `aal` and `methods` claims, `RequireMFA()` and the `admins` gates).
TOTP is a second factor only: it never signs anyone in. It is **opt-in**: a host
that does not call `WithTOTP` gets no TOTP route, no TOTP schema probe and no
erasure of TOTP tables.

- **Turn it on.** Apply migration `0026_totp` (stores pgx v0.13.0, Turso
  v0.12.0; required only when TOTP is wired, `ErrTOTPSchemaMissing` otherwise).
  Add `KeyTOTPRing` to `NewKeys`: `AUTH_TOTP_KEYS` is a ring `id:hex,id:hex`
  (32 bytes per key) and `AUTH_TOTP_ACTIVE_KEY` names the sealing id; production
  refuses it unset (*[Keys and rotation](../guides/keys-and-rotation.md)*). Wire
  `WithTOTP(TOTPConfig{Encrypter: cryptids.NewAESGCMRing(keys.TOTPRing), Issuer:
  "Your product", Views: …})` with `Repositories.TOTP`, `CredentialMutations`,
  `AuthenticationGrants` and a `ChallengeProtector` (`ErrTOTPReposRequired`).
  Each secret is sealed with its user id, so a row copied onto another user
  never opens. Keep NTP on: skew beyond ±30 s fails every code.
- **The gates.** One predicate covers every second factor: a user who holds an
  AAL2 factor (a user-verified passkey or a confirmed authenticator app) needs
  AAL2 within five minutes to add or remove a passkey, enroll or remove TOTP, or
  regenerate recovery codes; anyone else bootstraps at AAL1 from a fresh login
  or a password/code step-up grant. The fifth refused code in a row locks the
  second factor for five minutes (`429 too_many_attempts` with `Retry-After`),
  and no code's step is accepted twice. **One code is a recent AAL2 login for
  every gate for five minutes**, removal and regeneration included. Removing
  TOTP revokes every session of the user; an admin resets it with `POST
  /auth/admin/users/{id}/totp/reset`.
- **Wire contract** (shared by SPAs and the goth forms). `POST
  /auth/totp/enrollment` → `{secret, otpauth_uri, issuer, account, algorithm,
  digits, period, expires_at}` (the only response that carries the secret);
  `POST /auth/totp/enrollment/confirm {code}` → 201 `{enrolled_at,
  recovery_codes, generated_at}`, shown once; `POST /auth/step-up/totp` and
  `/auth/step-up/recovery-code {code}` answer as `POST /auth/login` plus
  `assurance`; `GET`/`DELETE /auth/totp`; `GET`/`POST /auth/recovery-codes`
  (regeneration takes `expected_generated_at`, else `409
  recovery_codes_stale`). `GET /auth/methods` gains `assurance`, `totp` and
  `recovery_codes`, so an SPA picks its step-up after an MFA 401 and renders
  its own QR from `otpauth_uri`.
- **Views and the chooser.** `TOTPConfig.Views` (views/goth's `authgoth.Views`
  satisfies `TOTPViews`) mounts the manage page, the enroll page (the key in
  groups of four, the `otpauth://` link and, with
  `authgoth.WithQR(rscqr.Modules)`, a server-side SVG QR code; no script) and
  the "Confirm it's you" chooser at `GET /auth/step-up/mfa`, which offers,
  filtered by `MFAConfig.Methods`, a passkey, the authenticator code, a recovery
  code, or the setup links. With TOTP views wired the chooser is where a
  `Browser()` + `RequireMFA()` denial of an AAL1 cookie lands.
- **Recovery codes and the pepper.** Codes are digested under the challenge
  pepper with the key id stored. **Regenerate recovery codes before dropping a
  pepper key**; `New` WARNs `auth: recovery codes sealed under retired pepper
  keys key_ids=[…]` while any remain.
- **Erasure.** `user.EraseInput.TOTP` and `.Passkeys` are set only when the
  factor is wired; a host `EraseRepository` must then delete and count those
  rows (`storetest.RunTOTP`, `storetest.RunPasskeys`). A host that unwires a
  factor stops erasing its tables: purge them first.
- **Events.** `totp_enrolled`, `totp_removed`, `totp_reset`, `totp_step_up`,
  `totp_failed` and `totp_locked` (both budgeted per IP),
  `recovery_codes_generated` and `recovery_code_used`; the `mfa_change_notice`
  mails the user's verified channels on every change and every recovery-code
  use. No row carries a secret, code, digest, key URI or ciphertext.

The pocket README's "TOTP and recovery codes (SOC 2 S4c)" section is the
reference: the gate table, the routes, the custom-view markup and the operator
recipes. `examples/auth-cms` wires it end to end.

## Invitations and the audit route (S7)

Authentication v0.30.0 (SOC 2 slice S7) changes how invitation links travel and how
invitations find their invitee, and adds an opt-in HTTP read of the security-event
rail.

- **The token is in the URL fragment (BREAKING for landing pages).** The mailed link
  is `<destination>#token=<token>`, so the token never reaches a server log, a proxy
  or a `Referer`. A landing page that reads `?token=` stops accepting the day the host
  repins. `LinksConfig.InvitationTokenInQuery` (`AUTH_INVITATION_TOKEN_IN_QUERY`)
  restores `?token=` for **one release** while the page moves (production logs one
  `posture_waiver=InvitationTokenInQuery` WARN); the next minor removes the field. A
  requested `redirect` containing `#` is 400 `invitation_redirect_fragment` before
  anything persists. A template that builds its own link from `Secret` is unaffected.
- **`LinksConfig.InvitationURL`** (`AUTH_INVITATION_URL`) is the default destination
  when no allowlisted `redirect` is requested: an absolute http(s) URL with no
  fragment or userinfo, HTTPS in production, and **required in production beside a
  Granter** (`ErrInvitationURLRequired`). It replaces the old relative `/` fallback,
  which broke in mail clients; development WARNs and keeps `/`.
- **The landing page.** Invitation, magic-link and OAuth pending-link URLs all carry
  `#token=`, so tell them apart **by path** and give the invitation landing its own
  route. Read `location.hash` and `history.replaceState` it away before any network
  call or third-party script; across a sign-in detour hold the token in
  `sessionStorage` (never in `return_to` or a query); then `POST
  /auth/invitations/accept {token}` (or `…/{id}/decline`); serve the page with
  `Referrer-Policy: no-referrer` and `Cache-Control: no-store`. The pocket ships no
  invitation page.
- **Identifier resolution.** An invitation is addressed to a `(kind, identifier)`.
  Every verification of that identifier on an account resolves the invitations
  addressed to it: registration verify, passwordless provisioning and adoption,
  OAuth and external provisioning, a confirmed identifier add or change, and
  enabling login or recovery on a verified identifier. `AutoAccept` invitations grant
  (`resolved_by: verification`); the others stay pending for the token. **Only
  verified identifiers holding the sign-in/recovery claim** (login- or
  recovery-enabled, unique across users) resolve, sync, list under `mine` and match
  at accept; a notification-only identifier never does. A second sign-in email
  matches, not only the primary. Identifiers of other kinds are never verified, so
  an open-kind invitation accepts by token alone, and only for a caller holding a
  claim-holding verified identifier.
- **On demand.** `POST /auth/invitations/sync` → `{"resolved": n}` runs the same
  resolution over the caller's claim-holding verified identifiers (pending rows
  only, at most eight identifiers, `AuthenticationLimits.InvitationSync` 10 a minute
  per user by default; idempotent). `GET
  /auth/invitations/mine?identifier_kind=email|phone` (default `email`; any other
  value 400) lists by the primary claim-holding identifier of that kind. SPAs call
  sync after an identifier change and read `mine` per kind.
- **Go callers.** `invitations.Service.ResolveInvitations` gains a `kind` parameter,
  and `invitations.AccessConfig.VerifiedIdentifiers` replaces `CallerIdentifiers`;
  both are wired by `New`, so only a host that calls or fakes them changes code.
- **Error codes and provenance.** A caller whose verified identifiers do not match
  gets 403 `invitation_identity_mismatch` (was `permission_denied`; a caller with no
  verified email no longer gets 404 first). Every `invitation_granted` row carries
  `operation_id` and `resolved_by` (`accept`, `verification`, `sync`, `direct_add`);
  a direct add adds `direct_add` and `invited_by`. Racing paths may record duplicate
  rows for one grant: dedupe on `operation_id`.
- **`GET /auth/admin/security-events`.** `AdministrationConfig.SecurityEventsRoute`
  (false by default) mounts the rail under the admin chain with the new action
  `UserAdminReadAudit` (`read-audit`; the `user_id` filter is the target) and, under
  `admins`, the AAL2 gate. It needs `Repositories.SecurityEvents` and a
  `UserAdminCheck` (`ErrSecurityEventsRouteRequires`). **A `UserAdminCheck` that does
  not switch on `Action` admits the new action:** switch on it with an allow-list
  before opting in. Filters `user_id`, `event_type`, `event_status`, `since`
  (inclusive), `until` (exclusive); cursor paging only (`offset`, `q`, `order` and a
  repeated key are 400); `no-store`. Each page read records one `audit_read` row
  (`rail: security_events`, the filter keys present, never their values).

The pocket README's "The invitation link and the landing page" and "Reading the rail
over HTTP" sections are the reference. Account merge (two existing accounts for one
invitee) is not offered yet.

## Added by S4d (session and machine governance)

Authentication v0.31.0 (SOC 2 slice S4d part 1), with stores pgx v0.15.0 / Turso v0.14.0
and views/goth v0.9.0. Everything is a host opt-in or today's behaviour except one break:
API keys at the MFA gates.

- **Revoke every session.** `Service.RevokeAllSessions(ctx, actor)` bumps every active
  user's `auth_revision` and deletes their sessions, step-up grants and `password_reset`
  challenges, in keyset batches of 500 users (one store transaction each), then sweeps
  the rows of users who are not active. It is not globally atomic: a failure returns the
  partial counts and a re-run is safe. It records `all_sessions_revoked_global`
  `started`, then `success` (or `failure`) with the counts. It needs
  `Repositories.GlobalSessions`, which the bundled stores fill. Delegated OAuth2
  connections are sessions, so they end too; API keys and external IdP sessions do not.
- **The opt-in route.** `AdministrationConfig.GlobalSessionRevocationRoute` mounts `POST
  /auth/admin/sessions/revoke-all`: a first-party access token or an external token,
  `Live()` (an API key or a delegated token is a 401), the browser-safe gate,
  `UserAdminCheck` with the new action `UserAdminRevokeAllSessions`
  (`revoke-all-sessions`), the admins MFA gate, then one call a minute per actor (429).
  The body is strict `{}`; the 200 is `{"users","sessions","grants","batches","revoked_at"}`
  and clears the session cookies (the caller's own session ends with the rest). The run
  is detached from the request, with a 10-minute limit. JSON only; distinct from the
  self-service `POST /auth/sessions/revoke-all`. **A `UserAdminCheck` that does not switch
  on `Action` admits the new action.**
- **Registration mode.** `IdentityConfig.Registration` (`AUTH_REGISTRATION`): `open` (the
  zero value), `invitation_only` or `closed`, applied on every account-creating path:
  `POST /auth/register`, passwordless provision-on-redeem (decided when the link is
  issued), OAuth first sign-in and external first verification. An unknown value refuses
  boot in every mode; `invitation_only` needs a Granter; `closed` beside
  `ProvisionOnFirstVerification` is a construction error. Refusals are 403
  `registration_invitation_required` / `registration_closed`; under `closed` the register
  routes answer 404. The goth login hides "Create an account" under `closed` only
  (`LoginPage.RegistrationClosed`).
- **API-key lifetime.** `AdministrationConfig.APIKeyMaxTTL` (`AUTH_API_KEY_MAX_TTL`):
  zero is 365 days in production and unlimited in development; 90 days is recommended.
  Under a bound, an omitted `expires_at` is filled with now plus the bound (echoed in the
  201; `api_key_minted` carries `expires_at_defaulted`); beyond it is 400
  `api_key_ttl_exceeded` (`max_ttl_seconds`, `max_expires_at`); a past one is 400
  `api_key_expiry_past`. `APIKeyUnlimitedTTLAcknowledged` keeps production unlimited
  with a WARN. Existing keys are untouched.
- **Key listing filters.** `GET /auth/service-accounts/{id}/keys?status=active|expired|revoked&never_expires=true|false`,
  filtered in the store (`apikey.FilteredLister`; without it a filtered call is 400
  `filter_unsupported`). A repeated key or an unknown value is 400;
  `status=expired&never_expires=true` is 400 `filter_contradiction`. Every key row now
  carries `status`.
- **Disable a service account.** `AdministrationConfig.ServiceAccountStatus`
  (`AUTH_SERVICE_ACCOUNT_STATUS`) needs migration 0027 (`service_account_status`) only
  when it is set. `POST /auth/service-accounts/{id}/disable` marks the account disabled
  AND revokes every unrevoked key in one transaction; `…/enable` restores minting only,
  and the old keys stay revoked. Both answer `{"service_account","changed","keys_revoked"}`.
  A disabled account mints nothing (409 `service_account_disabled`) and its keys are
  refused. `serviceAccountResponse.status` is always present.
- **API keys at MFA gates (BREAKING).** An API key cannot step up, so `RequireMFA()`, the
  admins gate and the machine gate refuse it: 403 `mfa_credential_not_eligible` with no
  `WWW-Authenticate` challenge, recorded as `mfa_required_denied reason=api_key`.
  `MFAConfig.APIKeysExempt` restores the pass (production logs
  `posture_waiver=APIKeysExempt`); the revoke-all route refuses keys regardless.
- **Boot lines.** `auth: registration registration=<mode> source=…`, `auth: api key
  lifetime api_key_max_ttl=<d>|unlimited source=…`, and `api_keys=denied|exempt` on the
  MFA posture line.

The pocket README's "Session and machine governance (SOC 2 S4d)" section is the reference,
including the revocation table and a CLI recipe for when the admin path is the thing that
is compromised.

## Added by S4d part 2 (session lifetime)

Authentication v0.32.0 (SOC 2 slice S4d part 2), with stores pgx v0.16.0 / Turso v0.15.0
(both require core v0.32.0 and sdk v0.20.0; views/goth is unchanged). Each control is a host
opt-in except two breaks: an incoherent `AccessTokenTTL`/`RefreshTTL` pair, and the liveness
floor under an overridden sensitive group. With the defaults (idle off, access 15 m, refresh
7 d, no authentication-age bound) nothing visible changes.

- **Idle timeout.** `SessionsConfig.IdleTimeout` (`AUTH_IDLE_TIMEOUT`; zero is off) ends a
  first-party session nobody has refreshed for that long. **Inactivity is no refresh rotation
  within `IdleTimeout`**: it is judged at `POST /auth/refresh` only, and an idle refresh deletes
  the row and answers the same generic 401 as any other refresh denial. It needs
  `Repositories.Sessions` to implement `session.ActivityRepository` (the bundled stores do from
  pgx v0.16.0 / Turso v0.15.0; otherwise `ErrIdleTimeoutRequires`) and migration 0029
  (`sessions.last_rotated_at`) — only when it is set: wired and unmigrated refuses boot naming
  `0029_session_activity.sql`. A value at or below `AccessTokenTTL` refuses; under twice it
  WARNs `idle_granularity`; at or above `RefreshTTL` it WARNs `idle_never_fires`. Delegated
  OAuth2 connections keep `OAuth2Config.SessionTTL`.
- **The grandfather rule.** A session is judged on its `last_rotated_at` stamp; a session never
  refreshed is judged on `created_at`; a session rotated while idle was off (rotations, no
  stamp) is not judged until its first refresh after the switch stamps it. Turning idle on
  across a live fleet logs nobody out at once. Re-enabling after a disabled window re-judges
  the old stamps; the NULL reset in *Operations runbooks* avoids that.
- **`session_idle_expired`.** Recorded once per deleted row (status `blocked`; `session_id`,
  `last_activity`, `idle_seconds`, `idle_timeout_seconds`, `surface: "refresh"`). A replayed
  consumed refresh slot is still `refresh_reuse`, never idle.
- **Access-token ceiling (BREAKING for an incoherent pair).** `AccessTokenTTL > RefreshTTL`
  refuses boot in every mode (`ErrAccessTokenTTLExceedsRefreshTTL`). In production an
  `AccessTokenTTL` above 1 h logs `posture_warning=access_token_ttl_long`, a WARN only.
- **The liveness floor (BREAKING for a lowered override).** `SessionHydration` (`/auth/me`),
  `SessionSecurityReads`, `CredentialManagement`, `MachineLifecycle` and `UserAdministration`
  keep a `Live()` floor under a `BundledRouteAuth` override. The default postures already are
  `Live()` and pay nothing; an override lowered to `Stateless()` or `LiveOnMutation()` now does
  one session read per call, and for an external token under `RevocationFailOpen` fails closed
  (503) on those groups during a provider outage.
  `BrowserConfig.BundledRouteAuthLivenessFloorRelaxed` restores the `Stateless()` floor
  (production logs `posture_waiver=BundledRouteAuthLivenessFloorRelaxed`).
- **External authentication age.** `ExternalProvider.MaxAuthenticationAge` (zero is unbounded)
  denies a token whose holder authenticated longer ago (`auth_time`, else `iat`; a 60 s
  leeway), on every request and before the subject resolves:
  `external_subject_denied reason=authentication_age_exceeded`, or
  `reason=authenticated_at_missing` when the token carries neither claim. The event is
  recorded once per token. On a provider that asserts `auth_time` (Firebase) it caps the
  provider's session; on an `iat`-only provider it bounds token age only. In production a
  provider with no `RevocationChecker` and no bound WARNs `external_authentication_age=unbounded`.
- **Boot lines.** `auth: session lifetime access_token_ttl=… refresh_ttl=… idle_timeout=off|<d>
  idle_source=config|default activity=refresh_rotation idle_scope=first_party
  live_floor=enforced|relaxed`; per provider, `auth: external provider lifetime provider=…
  max_authentication_age=off|<d> revocation_checker=wired|none fail_open=<bool>`. The
  production `RevocationFailOpen` line is now `posture_waiver=RevocationFailOpen`: re-key log
  alerts on the old text.

The pocket README's "Session lifetime (SOC 2 S4d part 2)" section is the reference, with the
per-provider authentication-age table and the residuals.

## Production posture

Production has no implicit mode and fails closed on incomplete security wiring. Among other requirements:

- signing/encryption/protection keys must be stable and shared across instances;
- delivery transports must report production-capable posture;
- passwordless/reset public links require configured HTTPS destinations, and invitation links require `LinksConfig.InvitationURL` beside a Granter (S7);
- rate limiting should use a durable/shared implementation in multi-instance deployments;
- in-process delivery should not be mistaken for durability;
- SQL/query logs that include arguments must remain off.

## Retention and erasure

`Components.Maintenance` (`logic/maintenance.Service`) is the retention service over the
OPTIONAL `Repositories.Maintenance` (`maintenance.Repository`), `Challenges` and
`SecurityEvents`. It is never nil and starts nothing: the host calls `Run(ctx)` in process (a jittered first pass,
then one an hour) or `Pass(ctx)` from its own ticker or a jobs schedule, and reads `LastReport()`
for its health surface. One pass purges expired sessions, OAuth states, grants,
contact changes and challenges, terminal and expired-pending invitations (never `accepting`), the
OAuth2 rows (bounded `PruneOAuth2`), and once a day the `security_events` rail past
`SecurityEventRetention` (400 days; below 366 refused). The table of rows, defaults and the
database floor is in *Production posture*, "Retention".

```go
authSvc, err := authentication.New(repos, signer, mode, delivery.ModeJobs,
    authentication.WithMaintenance(authentication.MaintenanceConfig{
        Config: maintenance.Config{
            ArchiveSink:     archive,            // a filestorage.Storer satisfies it
            ArchiveLocation: "gcs:audit-archive", // the label the registration line logs
        },
        MaintenanceRunnerAcknowledged: true, // the host runs Components.Maintenance
    }),
    // … the other groups
)
```

- **The rail is archived, then purged, per chunk.** With an `ArchiveSink` each chunk is written
  as one NDJSON object, read back and verified, recorded as a `security_events_archived` row, and
  only then purged; any failure purges nothing that pass (`maintenance.ErrArchiveFailed`). Without
  a sink the rail is purged unarchived (`archive_sink=none`, a valid policy). Every purge call
  appends one `security_events_purged` row, a zero count included. **The 365-day database floor
  is an invariant**, and `Repositories.Maintenance` must be backed by the database that holds the
  rail.
- **The acknowledgement.** `MaintenanceRunnerAcknowledged` asserts the host runs the service;
  production logs one `maintenance_runner=unacknowledged` WARN while it is false, and beside a nil
  `Repositories.Maintenance` it is `ErrMaintenanceAcknowledgedWithoutRepository` in every mode. It
  is a literal with no env tag; `guard --posture` prints its `ack` line.
- **Erasure.** The OPTIONAL `Repositories.UserErasure` (`user.EraseRepository`, which requires
  `Repositories.ActiveSessions`) backs `Service.EraseUser(ctx, actor, id)`: one store transaction
  marks the user erased, blanks and retires its identifiers, deletes its credentials, sessions,
  grants, challenges and act-as-user service accounts with their keys, deletes or blanks the
  invitations addressed to it, and appends `user_erased`. It refuses while an invitation claim is
  `accepting` (`user.ErrEraseBlockedByClaim`, 409) and is idempotent (`changed:false`). Reads
  project `user.StatusErased` from `users.erased_at`; the stored status stays `deactivated`. The
  `users` row and every pseudonymous id survive, and the rail is not rewritten. Erase is not a ban.
- **The route is an explicit opt-in.** `POST /auth/admin/users/{id}/erase` mounts under the admin
  chain only when `Repositories.UserErasure` is wired **and** `AdministrationConfig.UserErasureRoute`
  is true; the host check receives `UserAdminErase`, and the route answers 200
  `{user, changed, counts}`, 409 on an accepting claim, 400 on self (`ErrEraseSelf`). Without the
  port or the check it is `ErrUserErasureRouteRequires`; the port without `ActiveSessions` is
  `ErrUserErasureRequiresActiveSessions`. `UserErasureRoute` is not an acknowledgement.
- **Service accounts.** `Service.DeleteServiceAccount(ctx, actor, id)` deletes the account and its
  API keys in one transaction through the store's optional `serviceaccount.CascadeDeleter` (every
  bundled SQL store), recording `service_account_deleted`; no route ships it.

The SQL stores require `0023_retention.sql` (the `expires_at` indexes, then `users.erased_at`)
and `0024_security_events_retention.sql` (the floor and, on pgx, the `SECURITY DEFINER` purge
function the runtime role needs `EXECUTE` on) and refuse a database without them in every mode.
Apply them in their own runner invocation; *Production database posture* has the grants and the
index pre-build recipe, and *Operations runbooks* the schedules and "Erase a person".

## Persistence and views

The pgx and Turso store modules implement the pocket repositories and export the authentication migration set. The host owns its final migration ledger and upgrades.

Migration `0020_external_subjects.sql` (stores pgx v0.9.0 and Turso v0.8.0) is
required for every SQL host, including hosts that wire no external provider,
because both store constructors probe the table at boot. Migrations
`0025_passkey_credentials.sql` and `0026_totp.sql` are **conditional**: since
authentication v0.29.0 with stores pgx v0.14.0 and Turso v0.13.0, each is
required only when its factor is wired (`WithPasskeys`, `WithTOTP`), and the
pocket probes it through the store's optional `SchemaProber`. A host that wires
neither, such as an IdP-only or OAuth-only host, boots without both tables and
may apply them ahead of time. (Stores pgx v0.12.0–v0.13.0 and Turso
v0.11.0–v0.12.0 refused a database without `passkey_credentials` in every
mode.)

The pocket core imports neither store nor UI. `pockets/authentication/views/goth` maps the technology-neutral `Views` port onto `ui/goth`; hosts can embed that default and override pages, or implement the port with another renderer.

For an OAuth-only or passwordless-only host, `PasswordFlowsDisabled: true` keeps
the sign-in page available while removing password endpoints. Authentication
`v0.13.2` exposes that setting as `LoginPage.PasswordFlowsDisabled`; Goth views
`v0.5.1` hides the password form and registration/recovery links while preserving
the configured sign-in alternatives and notices. Custom login renderers should
honor the same field. Its zero value keeps the existing password form.

## Not shipped

There is no native `MFAConfig.Required` `all` and no enrollment grace yet (the follow-on `s4c2-native-all`); no `aal` or `methods` on delegated tokens, no API keys under MFA gates and no re-sealing of TOTP secrets on ring rotation (S4d); no SHA-256 or 8-digit TOTP, no push or SMS second factor, and TOTP is never a sign-in method; no username-first passkey sign-in; and no attestation or AAGUID allow-list. MFA that an external identity provider asserted is enforced (see [Assurance and MFA](#assurance-and-mfa)).

## OAuth client transports and trust

Browser OAuth uses a separate per-flow HttpOnly cookie to bind completion to the
initiating browser. Native clients use JSON start/completion and retain the
returned `flow_secret` independently from public `state`; their endpoints set no
cookies. Native routes require exact `OAuthNativeRedirectURIs` host configuration
and compatible provider registration. Fixed loopback IP/ports, private schemes
and claimed HTTPS callbacks are supported; device authorization is a separate,
unimplemented grant.

Service callers use explicit `OAuthStartRequest` and `OAuthCallbackRequest`.
Wrong proof cannot consume a valid flow. New email-based registration/adoption
requires verified evidence and the host's `TrustOAuthEmail` callback; nil denies
those paths. Existing linked-ID login requires neither email nor that policy.
When an email is already claimed, mailed pending-link proof is still required;
native clients complete it at `/auth/oauth/native/verify-link` for JSON tokens.

## MCP OAuth authorization server

The optional `WithOAuth2(OAuth2Config{...})` feature, released in v0.13.0, makes
this host an authorization server for its MCP resource. `WithOAuth` remains the
separate Google/GitHub human-login configuration. One shared OAuth endpoint family
serves all explicitly trusted clients; there are no Claude-specific endpoints.

Each consent creates a new delegated session. Web session W and MCP connections
A/B are independently revocable at `/auth/sessions`. Logging out of W leaves A/B
active. Revoking A ends its MCP token, refresh token and exchanged API tokens.
The explicit revoke-all action ends every session and invalidates pending codes.

The client requests the configured `resource`; the server signs its audience.
MCP receives an MCP-only token and authenticates to `/auth/oauth2/token` for a
restricted exchange to an API-only token. Both share the same delegated session.
The API still enforces the person's current permissions. MCP uses authenticated
`/auth/oauth2/introspect` on each operation; never share its issuer's HMAC signing
key, cache positive liveness or forward the incoming token unchanged to the API.

Authentication v0.13.1 adds `DelegatedAudience(apiResource)` for shared API routes:
pass it to `RequirePrincipal` to admit delegated API tokens while preserving the
existing web-session and API-key policy. `Audience(apiResource)` remains the
strict gate that rejects audience-less web tokens and keys. `Accept`, `Transports`
and `FirstParty` still constrain both options; delegated tokens stay header-only
and always require live session checks. Combining the audience options requires
both to match, independent of order. Existing store/view releases remain compatible;
this patch requires no migration or host credential-dispatch middleware.

The feature mounts RFC 8414 discovery and `/auth/oauth2/{authorize,token,revoke,introspect}`.
Authorization is code + PKCE S256 with browser consent and CSRF protection.
Public clients use allowlisted Client ID Metadata Documents, with SSRF-safe HTTPS
retrieval. Exchange/introspection require the configured confidential MCP client.
Token endpoints accept forms; no scopes or dynamic registration are provided.
Default access/session lifetimes are five minutes/thirty days, with strict refresh
rotation: replay revokes only that connection. First-party refresh grace is unchanged.

Use the root-mounted browser surface and supply both browser and OAuth views.
Production requires explicit HTTPS issuer/resources, client trust and confidential
credentials. SQL adapters require the new append-only `0019_oauth2_sessions.sql`
in the host's migration ledger before boot. Upgrade the core, adapter and views
together and drain old writers before enabling the feature. PostgreSQL, Turso and
the example memory store support it.
The host still owns MCP protected-resource metadata, tool authorization and the
actual Claude connector acceptance test. See the pocket README and
`plans/authentication-mcp-oauth.md` for the full API and rollout contract.
