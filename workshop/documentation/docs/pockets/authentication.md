---
title: Authentication
description: Identity, sessions, credentials, OAuth, recovery, delivery, and optional HTML.
---

# Authentication

`pockets/authentication` is a datastore-free identity pocket for human and machine principals. It owns users, multiple identifiers, credentials, sessions, challenges, recovery, OAuth linking, API keys, invitations, security events, and optional user administration.

It also implements `sdk/foundation/identity.Resolver` and exposes middleware other host routes and pockets can use.

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

`Config.Views == nil` keeps the pocket JSON-only. Supplying the `pockets/authentication/views/goth` adapter adds HTML pages and form handling without changing JSON contracts.

## Minimal development wiring

The required security choices are explicit. A development host needs a password hasher, mail sender, token signer, deployment mode, challenge protector, delivery encrypter, and delivery mode in addition to complete core repositories.

```go
cfg := authentication.Config{
    Hasher:      bcrypt.New(),
    Mailer:      email.NewConsole(log),
    MailFrom:    "auth@example.com",
    TokenSigner: signer,
    RuntimeMode: authentication.RuntimeModeDevelopment,

    ChallengeProtector: protector,
    DeliveryEncrypter:  deliveryKey,
    DeliveryMode:       authentication.DeliveryModeInProcess,
}

authSvc, err := authentication.NewService(repos, cfg)
if err != nil {
    return err
}

go func() {
    if err := authSvc.RunDelivery(ctx); err != nil {
        log.ErrorContext(ctx, "authentication delivery stopped", "error", err)
    }
}()

if err := authSvc.Register(pocket.Mount{
    Router: router,
    Logger: log,
    Events: bus,
}); err != nil {
    return err
}
```

This is a development outline, not a production recipe. See `examples/auth-cms/cmd/server` for complete construction and lifecycle handling.

## Delivery modes

Authentication sends no provider message on the request path. Registration, recovery, passwordless, and similar producers submit opaque encrypted delivery commands.

| Mode | Behavior | Use |
|---|---|---|
| `in_process` | finite queue and worker pool; lost on crash; per-process de-duplication only | single-instance development and simple deployments |
| `jobs` | durable, cross-instance keyed/fenced work through the jobs pocket | production and multi-instance deployments |

The host owns either runtime. In jobs mode, authentication exposes delivery callbacks the host registers with `jobs.NewFencedRuntime`; the pocket does not import jobs.

## Middleware and revocation

- `RequireUser` validates the access credential and stores a principal in context. Stateless routes can honor a revoked access JWT until its short TTL expires.
- `RequireLiveSession` also checks the session anchor and is used on sensitive routes for immediate revocation.
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
