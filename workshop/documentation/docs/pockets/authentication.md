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
- OAuth login/link/unlink when providers are wired;
- service accounts and API keys when both repositories are wired;
- invitations when a granter and host authorization check are wired;
- user administration when the host explicitly supplies `UserAdminCheck`.

Optional subsystems are deny-by-absence: routes are not registered when their enabling collaborator is missing.

`BrowserConfig.Views == nil` keeps the pocket JSON-only. Supplying the `pockets/authentication/views/goth` adapter adds HTML pages and form handling without changing JSON contracts.

## Authentication proof and host policy

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
join host ambient transactions. Firestore still has unimplemented challenge, recovery,
credential-management, invitation and machine-identity operations; a non-nil
repository slot does not imply full support.

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
| `WithAdministration` | `AdministrationConfig`: machine gate, user administration and list strategy |

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
- Without `Live()`, verification is stateless (signature + expiry only), so a revoked access JWT stays acceptable until its short TTL expires; `Live()` also checks the session anchor for immediate revocation on sensitive routes.
- browser-sensitive mutations apply an allowlisted Origin check and double-submit CSRF protection;
- API bearer callers do not need a browser CSRF cookie.

The access JWT is short-lived. The server-side session is the refresh and revocation anchor. Refresh tokens rotate within a fixed horizon rather than extending it indefinitely.

## Production posture

Production has no implicit mode and fails closed on incomplete security wiring. Among other requirements:

- signing/encryption/protection keys must be stable and shared across instances;
- delivery transports must report production-capable posture;
- passwordless/reset public links require configured HTTPS destinations;
- rate limiting should use a durable/shared implementation in multi-instance deployments;
- in-process delivery should not be mistaken for durability;
- SQL/query logs that include arguments must remain off.

## Persistence and views

Both pgx and Turso store modules implement the pocket repositories and export the authentication migration set. The host owns its final migration ledger and upgrades.

The pocket core imports neither store nor UI. `pockets/authentication/views/goth` maps the technology-neutral `Views` port onto `ui/goth`; hosts can embed that default and override pages, or implement the port with another renderer.

## Not shipped

Multi-factor authentication is not part of the current pocket. Method and assurance vocabulary reserve a future path, but there is no TOTP, passkey/WebAuthn, or recovery-code MFA surface today.

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
