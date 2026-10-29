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
| `WithAdministration` | `AdministrationConfig`: machine gate, user administration, list strategy, the `UserErasureRoute` opt-in and the `BeforeErase` hook |
| `WithMaintenance` | `MaintenanceConfig`: the retention policy (`maintenance.Config`) and `MaintenanceRunnerAcknowledged` |

`WithIDs` and `WithLogger` select optional host dependencies. With no `WithIDs`,
users, service accounts and invitations get NanoIDs (identities: the user id is
the JWT `sub`), and every other generated record id (identifiers, API keys,
security events, passkeys, recovery codes, challenges, contact changes,
authentication grants) gets a UUIDv7. One `WithIDs(generator)` overrides every
class. Secrets and keys (session ids, refresh tokens, OAuth state, API-key
secrets, invitation secrets) never follow it. Nil options return
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

## JSON session inventory over the service

The pocket's session-management routes are HTML: `GET /auth/sessions`, `POST
/auth/sessions/{id}/revoke` and `POST /auth/sessions/revoke-all`, mounted with the OAuth2
configuration. A JSON arm in the pocket is deferred to its own plan, because it has to fit the
adapter's dual-transport rule and a single wiring point. Until then, a host with a JSON client
builds three routes of its own over the same service methods:

| Method | Does |
|---|---|
| `ListUserSessions(ctx, userID, req)` | one owner's live sessions, newest first, as a `list.Page[session.Session]` |
| `RevokeUserSession(ctx, userID, sessionID)` | deletes one of that owner's sessions; an unknown id or another user's id is `sdk.ErrNotFound` |
| `RevokeAllUserSessions(ctx, userID)` | ends every session the owner holds, pending approvals included |

All three return `sdk.ErrForbidden` when `Repositories.SessionManagement` is nil, so mount the
routes only when your repositories fill it (the bundled pgx and Turso `Repositories` do). The
methods trust their caller to authorize `userID`. The routes derive it from the live session,
never from the request.

The gates to reuse:

- **First-party and live.** `RequirePrincipal(FirstParty(), Live())`. `FirstParty()` refuses API
  keys, external tokens and delegated OAuth access tokens, so a connected app cannot list or end
  the user's sessions. `Live()` re-checks the session on every request and is what makes
  `CurrentSessionID` report the caller's own session.
- **Browser-safe mutations.** `RequireBrowserSafeMutation()` after the authenticator on both
  writes: an allowlisted Origin plus the `X-CSRF-Token` double-submit for cookie callers.
  Bearer callers pass.
- **No caching.** `web.NoStore()` on all three.

The handlers, as host inbound code:

```go
// SessionInventory is the slice of *authentication.Service these routes use.
type SessionInventory interface {
	CurrentUser(context.Context) (string, bool)
	CurrentSessionID(context.Context) (string, bool)
	ListUserSessions(context.Context, string, list.Request) (list.Page[session.Session], error)
	RevokeUserSession(context.Context, string, string) error
	RevokeAllUserSessions(context.Context, string) error
}

// CookieClearer ends the browser's session cookies (*authenticationhttp.Adapter).
type CookieClearer interface {
	ClearSessionCookies(http.ResponseWriter)
}

// Router is the registration method of *web.WebHandler.
type Router interface {
	Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware)
}

type sessionView struct {
	ID         string    `json:"id"`
	FirstParty bool      `json:"first_party"`
	ClientID   string    `json:"client_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

type sessionHandlers struct {
	svc     SessionInventory
	cookies CookieClearer
}

// Mount registers the routes behind authenticate (FirstParty, Live) and, on
// the writes, browserSafe.
func Mount(r Router, svc SessionInventory, cookies CookieClearer, authenticate, browserSafe web.Middleware) {
	h := sessionHandlers{svc: svc, cookies: cookies}
	r.Handle("GET", "/api/me/sessions", h.list, web.NoStore(), authenticate)
	r.Handle("DELETE", "/api/me/sessions/{id}", h.revoke, web.NoStore(), authenticate, browserSafe)
	r.Handle("POST", "/api/me/sessions/revoke-all", h.revokeAll, web.NoStore(), authenticate, browserSafe)
}

func (h sessionHandlers) caller(w http.ResponseWriter, r *http.Request) (userID, sessionID string, ok bool) {
	userID, uok := h.svc.CurrentUser(r.Context())
	sessionID, sok := h.svc.CurrentSessionID(r.Context())
	if !uok || !sok {
		web.RespondJSONDomainError(w, sdk.ErrUnauthorized)
		return "", "", false
	}
	return userID, sessionID, true
}

func (h sessionHandlers) list(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := h.caller(w, r)
	if !ok {
		return
	}
	req, err := list.ParseQuery(r.URL.Query(), list.QueryOptions{})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	req.Order = session.DefaultOrder
	page, err := h.svc.ListUserSessions(r.Context(), userID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, list.MapPage(page, func(s session.Session) sessionView {
		return sessionView{ID: s.ID, FirstParty: s.FirstParty(), ClientID: s.Delegation.ClientID,
			CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, Current: s.ID == current}
	}))
}

func (h sessionHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := h.caller(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.svc.RevokeUserSession(r.Context(), userID, id); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if id == current {
		h.cookies.ClearSessionCookies(w)
	}
	web.RespondNoContent(w)
}

func (h sessionHandlers) revokeAll(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.svc.RevokeAllUserSessions(r.Context(), userID); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.cookies.ClearSessionCookies(w)
	web.RespondNoContent(w)
}
```

`cmd` wires it with the pocket's adapter and service:

```go
sessionsinbound.Mount(router, auth.Authentication, auth.HTTP,
	auth.HTTP.RequirePrincipal(authenticationhttp.FirstParty(), authenticationhttp.Live()),
	auth.HTTP.RequireBrowserSafeMutation())
```

What the recipe guarantees, and what to keep when you adapt it:

- **Identical not-found.** Another user's session id and an id that does not exist both answer
  the same 404 body, because the store constrains the delete to the owner and answers
  `sdk.ErrNotFound` for both. Do not look the session up first to give a better error. (The
  HTML arm answers a blanket 400, which is not the model here.)
- **Revoking the current session ends the browser's sign-in.** Revoke-current and revoke-all
  clear the session cookies on the response. A bearer client drops its own tokens on the 204.
- **No secrets in the projection.** A `session.Session` carries refresh-token hashes; the view
  copies the id, the timestamps, the delegation's client id and `FirstParty()` only.
  `FirstParty()` separates the user's own sign-ins from connected apps (delegated sessions,
  where revoking one disconnects the app). Filter on it if your page lists only devices. The
  session row has no IP address or user agent. If you join those in from your own records,
  show them only on these owner-only routes.
- **Your paths.** The recipe mounts under the host's API prefix, outside `/auth/*`, so a later
  pocket arm cannot collide with it. Add the three routes to your endpoint matrix as `self`.

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
  the claim; an unknown `aal` value is rejected. Since v0.37.0 a delegated
  token may carry `aal` too, signed only under
  [`OAuth2Config.DelegatedAssurance`](#added-by-s4d-part-4-delegated-assurance-and-asserted-authentication-time);
  without it a delegated token reads `unknown`. API keys stay `unknown` (S4d).
- **The `methods` claim** (since v0.28.0). Every native mint also signs
  `methods`, the session row's method kinds (`password`, `passkey`, `totp`,
  `recovery_code`, …), beside `aal`, and `Credential.Methods` reads it. An
  unknown kind is rejected. A delegated token carries `methods` under the same
  rule as `aal` (since v0.37.0). An external token's source is `idp`.
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
  passwordless or OAuth login without `WithPasskeys` or `WithTOTP` (with either it
  is native `all`, since v0.38.0:
  [Added by S4c-2](#added-by-s4c-2-native-all-and-the-enrollment-grace)), or a `Methods` entry naming a
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
  /auth/invitations/accept {token}` (or `POST /auth/invitations/decline {token}`); serve the page with
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
  provider's session; on an `iat`-only provider it bounds token age only (since v0.37.0,
  `RequireAuthenticationTime` refuses such tokens instead; see S4d part 4 below). In production a
  provider with no `RevocationChecker` and no bound WARNs `external_authentication_age=unbounded`.
- **Boot lines.** `auth: session lifetime access_token_ttl=… refresh_ttl=… idle_timeout=off|<d>
  idle_source=config|default activity=refresh_rotation idle_scope=first_party
  live_floor=enforced|relaxed`; per provider, `auth: external provider lifetime provider=…
  max_authentication_age=off|<d> revocation_checker=wired|none fail_open=<bool>`. The
  production `RevocationFailOpen` line is now `posture_waiver=RevocationFailOpen`: re-key log
  alerts on the old text.

The pocket README's "Session lifetime (SOC 2 S4d part 2)" section is the reference, with the
per-provider authentication-age table and the residuals.

## Added by v0.34.0 (invitation create actor and decline by token)

Authentication v0.34.0 is additive: no store, migration or views change.

- **Create attribution.** `PreparedCreate.WithActor(principal)` attributes a create the
  way `PreparedManagement.WithActor` attributes cancel and resend: audit only, never
  authority, and never visible through `Input()`. On the `invitation_created` row a user
  actor is the `UserID`; a non-user actor sets `Actor` with `UserID = ""` and
  `Details["invited_by"]`; a zero actor (and headless `Create`) keeps today's row. A
  direct add's `invitation_granted` rows gain `Actor` and keep `UserID` = the granted
  subject. The bundled create route attaches the signed-in user. A host bootstrap goes
  `PrepareCreate` → `WithActor(sdk.Principal{Type: "system", ID: "bootstrap"})` →
  `CreatePrepared`. A query keyed `user_id = invited_by` no longer sees a non-user
  actor's created row. Audit consumers that diff detail shapes should expect this:
  rows from the bundled create route now carry `Actor={user,id}`, created rows gain
  `details.invited_by`, and direct-add grant rows gain `Actor`; `user_id` is unchanged.
- **Decline by token.** `Service.DeclineByToken(ctx, token)` and the public `POST
  /auth/invitations/decline` `{token}` let a landing page holding only `#token=` offer
  Decline. Same Origin-only gate and `invitation_decline` budget as `POST
  /auth/invitations/{id}/decline`, which stays. The route mounts only when the wired
  `InvitationService` also has `DeclineByToken`, so host fakes keep compiling.

  | Request | Response |
  |---|---|
  | pending | 200 `{"status":"declined"}` and an `invitation_declined` row |
  | unknown or wrong token | 404, the same body as an unknown id |
  | unclaimed, past expiry | 410 (as accept) |
  | accepted, or being accepted | 409 |
  | declined or cancelled, before expiry | 409; a second click reads as done |
  | declined or cancelled, past expiry | 410 (the stores expire every unclaimed row) |
  | missing token or malformed body | 400 |
  | cross-origin | 403 `origin_rejected` |

  The by-id decline keeps declining an expired pending row in this minor; the token
  route surfaces the expiry.
- **Host upgrade.** A host whose route matrix test enumerates routes adds `POST
  /auth/invitations/decline` as public.

## Added by S4d part 3 (key management and password policy)

Authentication v0.35.0 with sdk v0.23.0, golang-jwt v0.4.0, bcrypt v0.3.0, the new
`integrations/passwords/hibp` v0.1.0 and stores pgx v0.18.0 / Turso v0.17.0 (SOC 2 slice S4d
part 3). The core stays on sdk v0.21.0; a host that wires the ring forms lifts sdk to v0.23.0
through golang-jwt or its own `cryptids` call. Everything is host opt-in except bcrypt's default
cost, and a host setting only the single key variables is unchanged.

- **Key rings.** `NewKeys` reads a ring form beside four single variables: `AUTH_JWT_KEYS` /
  `AUTH_JWT_ACTIVE_KEY`, `AUTH_CHALLENGE_PEPPERS` / `AUTH_CHALLENGE_ACTIVE_PEPPER`,
  `AUTH_DELIVERY_ENCRYPTER_KEYS` / `…_ACTIVE_KEY` and `AUTH_TOKEN_ENCRYPTER_KEYS` /
  `…_ACTIVE_KEY` (`id:hex,id:hex` and the active id). The JWT and the two encrypters join a
  single variable set beside the ring under the reserved id `legacy`, so the ring keeps reading
  what the single key wrote; the pepper ring names its own ids (both forms refuse). Build the
  adapters from `Keys`:

  ```go
  var signer cryptids.JWTSigner
  var err error
  if len(keys.JWTRing.Keys) > 0 {
      signer, err = golangjwt.NewRing(keys.JWTRing, golangjwt.WithLegacyKeyID(keys.JWTLegacyKeyID))
  } else {
      signer, err = golangjwt.New(string(keys.JWTSecret))
  }
  // the same branch for each encrypter; an empty legacy id is no option
  delivery, err := cryptids.NewAESGCMRing(keys.DeliveryEncrypterRing,
      cryptids.WithLegacyKeyID(keys.DeliveryLegacyKeyID))
  // the pepper ring converts directly
  protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing(keys.ChallengeRing))
  ```

  A ring signer stamps `kid` and verifies under any ring key; a token without `kid` verifies
  only under the legacy id. While `legacy` is the active id the adapters write the single-key
  shape, which makes a rolling three-phase rotation safe (*Operations runbooks*, "Rotate a
  key", one runbook per key).
- **The `auth: keys` boot line.** One INFO line per `New`: each role's kind (`ring`, `single`,
  `opaque`, `none`), a ring's active id, ids and legacy state, the identifier keyer, and the
  first-party token issuer and audience. Never key material. Production logs
  `posture_warning=legacy_key_read_enabled key=<role>` while a legacy id is set. Rings report
  through the named optional interface `authlogic.KeyRingReporter`.
- **First-party `iss` / `aud`.** `SessionsConfig.Issuer` (`AUTH_TOKEN_ISSUER`) and `Audience`
  (`AUTH_TOKEN_AUDIENCE`): set, first-party tokens carry and require them (one refresh per
  client at enablement); a collision with the OAuth2 issuer or resources refuses
  (`ErrTokenAudienceCollides`).
- **Passwords.** bcrypt v0.3.0 defaults to cost 12 (`WithCost(10)` hatch) and reports it
  (`authlogic.CostReporter`); production WARNs `password_hash_cost_low` below 12 and
  `password_breach_check_none` when password flows run with no
  `CompromisedPasswordChecker`. The checkers are `integrations/passwords/hibp` (the keyless
  Pwned Passwords range API, fail-closed: 503 on an outage unless
  `CompromisedPasswordFailOpen`) and `protection.NewBlocklistChecker` over a host-supplied
  list; the pocket ships no list, and no example wires a checker. Login miss paths verify
  against a dummy hash, so an unknown identifier costs what a wrong password does.
- **Rehash on login (live).** A hasher implementing `authlogic.Rehasher` (bcrypt v0.3.0
  `NeedsRehash`) gets a lower-cost hash re-hashed after a successful login or step-up,
  through the optional compare-and-swap `user.PasswordHashUpgrader` (`UpgradeHash`; pgx
  v0.18.0, Turso v0.17.0, and the auth-cms memory store). No revision bump, no session revoked,
  no security event; a failure is one `password_rehash_failed` WARN and never fails the login.
  `storetest.RunPasswordHashUpgrade` is the conformance suite (outside `Run`). The timing
  residual left by older, cheaper hashes closes as users log in.
- **No lockout.** The per-account control is `AuthenticationLimits.Login.PerSubject` (5 a
  minute by default; an hourly ceiling on a shared limiter is one option).

The pocket README's "Key rings and the `auth: keys` boot line (SOC 2 S4d part 3)" and "Host
password policy" sections are the reference.

## Added by S4d part 4 (delegated assurance and asserted authentication time)

Authentication v0.37.0 with sdk v0.24.0, firebase v0.3.0 and stores pgx v0.19.0 / Turso v0.18.0
(SOC 2 slice S4d part 4; views/goth is unchanged). Both controls are host opt-in. The one
unconditional change is on the verifier side, and it only admits: v0.37.0 accepts `aal` and
`methods` on a delegated token, a shape nobody mints until a host sets the flag.

- **Delegated assurance.** `OAuth2Config{DelegatedAssurance: true}` makes an MCP grant carry
  the approving browser session's authentication. Approval copies the session's `aal`, methods
  and authentication time into the authorization code, in the same transaction that already
  locks the session. Redemption persists them on the delegated session. Every token minted from
  that session (redeem, refresh, exchange) signs `aal` and `methods` as a first-party token
  does, and `POST /auth/oauth2/introspect` reports them as `aal` and `methods`, only when
  recorded. A grant approved at AAL2 then passes `RequireMFA()` on a route that admits its
  audience (`Audience`, `DelegatedAudience`); `MFAConfig.Methods` judges the approval's factor.
  **An MCP grant never reaches admin:** the bundled admin and machine groups name no audience,
  so admission refuses a delegated token before any MFA check.
- **Store and migration.** `Repositories.OAuth2` must implement the optional
  `oauth2.CodeAuthenticationRepository`, and the database must carry migration
  `0030_oauth_code_authentication.sql` (three nullable columns on `oauth_authorization_codes`;
  additive, NULL on every existing row). With the flag set, `New` refuses a store without the
  interface (`ErrDelegatedAssuranceRequires`) or without the columns
  (`ErrDelegatedAssuranceSchemaMissing`, naming 0030); any other probe failure is
  `sdk.ErrUnavailable`. A host without the flag is never probed. `--posture` hosts copy 0030
  into their ledger when the pgx pin reaches v0.19.0, even with the flag off (guard P9).
  `storetest.RunOAuth2CodeAuthentication` is the conformance runner (outside `Run`; it skips a
  store without the interface). The auth-cms memory store implements it.
- **Fixed at approval (R-4).** A delegated grant's level is the approving session's at consent.
  Refresh never re-reads it and exchange copies it, never raises it, so an AAL2 grant stays AAL2
  for as long as the grant lives, `OAuth2Config.Server.SessionTTL` (thirty days by default). No
  age bound applies yet (follow-up: `DelegatedAssuranceMaxAge`). A host that needs a shorter
  window shortens `SessionTTL`. Grants approved before the flag carry no level
  (`AssuranceUnknown`) until the user re-consents.
- **Rollout and rollback.** Verifiers first, then 0030, then the flag, then re-consent; turning
  it back off stops new tokens carrying a level, so wait one `AccessTTL` before a downgrade
  (*Operations runbooks*, "Enable delegated assurance"). Boot logs
  `auth: oauth2 delegated_assurance=on|off`.
- **Asserted authentication time.** `sdkauth.Identity.AuthTimeAsserted` (sdk v0.24.0) says
  whether `AuthenticatedAt` came from `auth_time` (or the provider's own sign-in instant) or
  only from the `iat` fallback. The sdk's `OIDC` provider sets it when `auth_time` is present
  and valid. Firebase v0.3.0 always sets it. A host-written `Provider` leaves it false unless it
  sets it, and an unkeyed `Identity{...}` literal stops compiling.
- **`ExternalProvider.RequireAuthenticationTime`** (opt-in, default off, R-5) turns a
  `MaxAuthenticationAge` bound into a sign-in bound: an identity whose time is only the `iat`
  fallback is denied before the subject resolves, with one
  `external_subject_denied reason=authenticated_at_unasserted provider=<name>` row per token
  (the same once-per-token dedupe as the age reasons). Set without a positive bound it is
  `sdk.ErrInvalidInput` at construction. The provider lifetime boot line gains
  `authentication_time=asserted_required|any`. Firebase hosts may set it at once. A
  Google-ID-token or other `iat`-only issuer must request `auth_time` (`max_age`) first, or
  every token is denied. production-baseline reads it from `IDP_REQUIRE_AUTHENTICATION_TIME`.
- **`FreshAuth` and `iat` (R-6).** `FreshAuth(within)` is unchanged: it still reads
  `AuthenticatedAt` including the `iat` fallback, so on a provider that omits `auth_time` a
  freshly issued token passes it however long ago the person signed in. Set
  `RequireAuthenticationTime` (with a bound) on that provider to refuse such tokens before any
  route option runs. A `FreshAuth` mode that honours the flag is a follow-up.

The pocket README's "OAuth2 delegated assurance (v0.37.0)" and "External authentication age"
sections are the reference.

## Added by S4c-2 (native all and the enrollment grace)

Authentication v0.38.0 with sdk v0.24.0 (SOC 2 slice S4c-2; no store, views/goth or sdk tag,
no migration). `MFAConfig{Required: MFARequiredAll}` beside `WithPasskeys` or `WithTOTP` is
**native `all`**: every first-party session must reach AAL2, and a user without a second factor
gets a bounded window to enrol. It is opt-in; no host wired today changes on the wire except
the #282 fix below.

```go
auth.WithMFA(auth.MFAConfig{
	Required:        auth.MFARequiredAll,
	EnrollmentStart: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), // a literal, never env
	EnrollmentGrace: 14 * 24 * time.Hour,                           // zero = 7 days; at most 30
})
```

- **#282.** The OAuth2 authorize page and the HTML sessions page no longer admit external
  tokens, so a host with `WithOAuth2` and `WithExternal` boots without
  `WithBrowserLoginPath`. A browser holding only a provider cookie gets the login redirect with
  `recover=1` (a 303 either way; a provider outage there was a 503, is a 303) and the cookie is
  no longer read there.
- **The window.** A user's grace closes at `max(EnrollmentStart, user created at) +
  EnrollmentGrace`. Every mode refuses a zero `EnrollmentStart`
  (`ErrMFAEnrollmentStartRequired`), a negative grace or one above `MaxMFAEnrollmentGrace`
  (`ErrMFAEnrollmentGraceInvalid`), and a `Methods` that admits neither `passkey` nor `totp`
  (`ErrMFAUnsatisfiable`). Keep `EnrollmentStart` a literal: moving it later reopens the window
  for every unenrolled user, moving it earlier closes it.
- **The claims.** A first-party access token minted below AAL2 carries `mfa_enroll_by` (unix
  seconds) when its holder holds no AAL2 factor, or `mfa_enrolled: true` when they do
  (`Credential.MFAEnrollBy`, `Credential.MFAEnrolled`). Every login, refresh and step-up
  re-evaluates them; an AAL2 token carries neither, and a delegated token never does. A factor
  read that fails refuses the mint with `sdk.ErrUnavailable` (503), never a silent pass; on a
  magic-link sign-in that happens after the session row is written, so the person requests a
  new link. A v0.37.0 verifier ignores both claims.
- **The floor** runs in every `RequirePrincipal` without `AllowMFAEnrollment()`, on
  first-party access tokens below AAL2 only (API keys, delegated and external tokens are
  untouched):

  | Token | JSON | `Browser()` with the cookie |
  |---|---|---|
  | `mfa_enroll_by` ahead | passes | passes |
  | `mfa_enroll_by` passed | 403 `{"code":"mfa_enrollment_required","enroll_by":…}` | 303 to the TOTP enrollment page (else the passkeys page) with `return_to` |
  | `mfa_enrolled` | 401 + the RFC 9470 challenge | 303 to the step-up page |
  | neither (minted before the flip) | plain 401: refresh and retry | 303 to the login page with `recover=1` |

  An enrolled user's password sign-in therefore gets the step-up answer, not a refresh loop.
  Known limitations (follow-ups): the TOTP enrollment page does not yet honour `return_to`, so
  after enrolling the person steps up and navigates back themselves; reached with no setup in
  progress, as the floor's redirect does, it answers 410 with "That setup expired" above its
  set-up button; and until the session refreshes or steps up, a just-enrolled person is still
  sent back to it.
- **The enrollment surface** carries `AllowMFAEnrollment()`: `/auth/me`, `/auth/csrf`,
  `/auth/methods`, credential management (step-up, TOTP and passkey enrolment), the browser
  account pages, the OAuth link start and `/auth/sessions`. `/auth/me` and `/auth/methods` add
  `"mfa_enrollment": {"required": true, "enroll_by": "<RFC 3339>"}` inside the window.
  `/auth/refresh` and `/auth/logout` are not gated.
- **An SPA** whose shell sits under `RequirePrincipal()` adds `AllowMFAEnrollment()` to it or
  serves it unauthenticated, and routes 403 `mfa_enrollment_required` to its enrollment UI,
  401 with the RFC 9470 challenge to step-up, and a plain 401 to `/auth/refresh`.
- **No grace for durable credentials.** The admin gate (`/auth/admin/users*`, with
  `UserAdminCheck`), the machine lifecycle routes and the OAuth2 authorize page require AAL2
  from every session, so nothing minted during the grace (an API key, a grant) outlives it.
  `RequireMFA()` never accepts the grace.
- **Posture and events.** Native password, passwordless and OAuth login leave the production
  single-factor refusal (*Production posture*, "Added by S4c-2"). The boot line reads
  `mfa_required=all gates=first_party_routes[,/auth/admin/users*],machine_lifecycle[,oauth2_authorize][,external_tokens]
  … enrollment_start=<RFC 3339> enrollment_grace=<duration>`. A denial records
  `mfa_required_denied gate=enrollment` with `reason=enrollment` (window closed) or
  `reason=assurance` (holder must step up), once per session per process.
- **Scope.** The floor covers `RequirePrincipal` routes only; host middleware that verifies
  tokens itself is outside it. External-only `all` (an IdP-only host) is unchanged.

*Operations runbooks*, "Turn on MFA for everyone", has the rollout, rollback and break-glass.
The pocket README's "MFAConfig.Required all (native)" section is the reference.

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
- **`BeforeErase`.** `AdministrationConfig.BeforeErase` (`func(ctx, actor sdk.Principal, userID
  string) error`) runs after `EraseUser`'s own checks and before the store transaction, under the
  request context bounded by `EraseTimeout`; it is where the host revokes the person's
  authorization role assignments in every scope. A non-nil error refuses the erase with nothing
  erased (`authlogic.ErrEraseRefusedByHook` wrapping the hook's error, mapped by its sdk kind; an
  unkinded error is a 500). The two transactions are never atomic: the hook runs first because
  its work is reversible, and it runs on every call, so re-running the erase completes a
  half-finished one and sweeps orphan grants. A 409 after the roles vanished means the hook ran
  and the store refused (a held claim, logged as `auth: erase refused after BeforeErase ran`):
  settle the claim, re-run. The hook is idempotent, attempts integrity-protected relations first,
  re-reads the first page until it is empty, stamps the administrator as audit actor with reason
  `user_erase`, returns the pocket error unwrapped and never runs inside a `Transact` on the
  authorization store. Without `Repositories.UserErasure` it is `ErrBeforeEraseWithoutErasure`.
  The pocket README's "Erasure" section has every partial state.
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

There is no per-user re-open of an enrollment grace and no account-page banner with its deadline (native `all`, S4c-2); no age bound on a delegated grant's `aal` (it is fixed at approval; S4d part 4), no API keys under MFA gates and no re-sealing of TOTP secrets on ring rotation (S4d); no SHA-256 or 8-digit TOTP, no push or SMS second factor, and TOTP is never a sign-in method; no username-first passkey sign-in; and no attestation or AAGUID allow-list. MFA that an external identity provider asserted is enforced (see [Assurance and MFA](#assurance-and-mfa)).

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
