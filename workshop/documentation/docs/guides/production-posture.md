---
title: Production posture
description: What the framework refuses in production today, what is documented only, the trust boundary, and the revocation SLA a host inherits.
---

# Production posture

This page describes the framework **as it is on `main` at 2026-10-22, after SOC 2 slice S1**
(the production posture floor; release record in `RELEASING.md`, host handoff in
`plans/soc2-s1-production-posture-handoff.md`). Where a control is still planned, it says so;
an item marked *planned* is not a control a host can claim yet.

## Refused at construction in production

Every pocket and connector with an unsafe option takes the host's runtime mode:
`authentication.New` positionally (no default), `authorization.WithRuntimeMode`, and the
`RuntimeMode` field on `cms.Config`, `pgxdb.Config`, `tursodb.Config` and `otel.Config` (zero
is mode-blind). The mode comes from the process environment through
`environment.LoadEnvForMode`, never from `.env` (`ErrModeFromFile`). With `production` the
checks below fail construction, before any I/O, in one joined error naming every
acknowledgement. An acknowledgement is a boolean literal in the composition root with no env
tag, and each honored one is logged once as `posture_waiver=<Flag>`. What a refusal cannot see
— a mode never passed, a missing middleware, a key minted by hand — `gopernicus guard
--posture` (P0–P8) finds in the host's `cmd/` tree.

**`pockets/authentication`, before S1** (positional `RuntimeMode`):

| # | Production refuses | Sentinel | Where |
|---|---|---|---|
| 1 | an unset or unknown runtime mode (all modes) | `ErrRuntimeModeRequired` | `pockets/authentication/security.go` `validateRuntimeMode` |
| 2 | an unset or unknown delivery mode (all modes) | `ErrDeliveryModeRequired` / `ErrDeliveryModeInvalid` | `constructor.go` |
| 3 | a missing hasher (password flows on), mailer (delivery on) or token signer (all modes) | `ErrHasherRequired`, `ErrMailerRequired`, `ErrTokenSignerRequired` | `constructor.go` |
| 4 | `jobs` delivery without `DeliveryJobsAcknowledged` | `ErrDeliveryJobsUnacknowledged` | `constructor.go` |
| 5 | `in_process` delivery without `DeliveryEphemeralAcknowledged` | `ErrDeliveryEphemeralUnacknowledged` | `constructor.go` |
| 6 | a development-only or capability-less mail/SMS transport (console senders) | transport error | `security.go` `validateDeliveryTransports` |
| 7 | an in-process rate limiter (`ratelimiter.Memory` or a limiter declaring `InProcessOnly`) | `ErrNonDurableRateLimiter` | `security.go` |
| 8 | no `IdentifierKeyer` | `ErrIdentifierKeyerRequired` | `constructor.go` |
| 9 | no `PasswordResetURL` when the reset rail is wired, or an `http` one | `ErrPasswordResetURLRequired` / `ErrPasswordResetURLInsecure` | `constructor.go`, `security.go` |
| 10 | an `http` `PublicAuthBaseURL` when passwordless is on | `ErrPublicAuthBaseURLInsecure` | `security.go` |
| 11 | an `http` `OAuthLinkBaseURL` when set | `ErrOAuthLinkURLInsecure` | `security.go` |
| 12 | a session cookie without `Secure` | invalid input | `configuration.go` |
| 13 | a delegated (MCP) issuer that is not absolute `https` | invalid input | `logic/authentication/constructor.go` |
| 14 | an `http` origin namespace in `OriginPolicy`; an `http` OAuth callback base (native redirect URIs allow `http` on loopback only) | invalid input | `inbound/http/adapter.go`, `internal/redirect/oauth_config.go` |
| 15 | challenges wired without a `ChallengeProtector`; delivery that can send without a `DeliveryEncrypter` (all modes) | `ErrChallengeProtectorRequired`, `ErrDeliveryEncrypterRequired` | `constructor.go` |
| 16 | contradictory wiring (a gate without its repositories, an HTML policy without views, half-wired pairs) (all modes) | typed errors | `constructor.go` |

**Added by S1**, every module:

| Module | Production refuses | Sentinel | Acknowledgement |
|---|---|---|---|
| authentication | nil `Repositories.SecurityEvents` (no audit rail) | `ErrAuditRequired` | `WithAuditDisabledAcknowledged()`; beside a wired rail it is `ErrAuditAcknowledgedWithSecurityEvents` in every mode |
| authentication | native single-factor login: password flows on, or any `Passwordless` kind | `ErrSingleFactorUnacknowledged` | `IdentityConfig.SingleFactorAcknowledged` |
| authentication | a delivery transport declaring opportunistic STARTTLS, at both check sites | `ErrInsecureDeliveryTransport` (wraps `notify.ErrOpportunisticTLS`) | `DeliveryConfig.OpportunisticTLSAcknowledged` |
| authentication | an OAuth or external provider declaring `DevelopmentOnly` | `ErrDevelopmentOnlyProvider` | none |
| authentication | a needed key unset (`NewKeys`); a malformed key in every mode | `ErrKeyRequired` (joined, one per key) | none; `AUTH_TOKEN_ENCRYPTER_KEY` is optional — see *Keys and rotation* |
| authorization (`WithRuntimeMode`) | a tuple authority or `Mutations` not reporting `AuditRecording()` true, or nil `Repositories.Audit` | `ErrAuditRequired` | `WithAuditDisabledAcknowledged()`; beside a recording authority it is `ErrAuditAcknowledgedWithRecording` in every mode |
| cms | `AdminUngatedAcknowledged` (a nil `AdminMiddleware` never mounts the admin routes in any mode — AUDIT-053) | `ErrAdminUngatedRefused` | none; beside a gate it is `ErrAdminAcknowledgedWithMiddleware` in every mode |
| cms | a Mailer failing `notify.CheckTransport` | `ErrInsecureMailTransport` | none |
| pgxdb | a *plaintext* target (`sslmode=disable`, or the fallback `prefer`/`allow` keep) or an *unverified* one (bare `require`), on the primary or any fallback | `ErrTLSRequired` | `TLSUnverifiedAcknowledged` (bare `require`), `TLSDisabledAcknowledged` (all); a unix socket is *local* |
| pgxdb | `LogQueries`, or a `Tracer` that is or wraps `*LoggingQueryTracer` | `ErrQueryLoggingRefused` | none |
| turso | a hosted URL without TLS (`http://`, `ws://`, `libsql://…?tls=0`); `file:` is exempt | `ErrTLSRequired` | `TLSDisabledAcknowledged` |
| otel | an empty `Exporter` (the `stdout` env default is gone; `Open` still fills it outside production) | `ErrExporterRequired` | none — set `TRACING_EXPORTER` |
| otel | an OTLP exporter with `Insecure` to a non-loopback endpoint | `ErrInsecureExporter` | none |
| sdk `web` | `ServerConfig.ValidateProduction()`: `PUBLIC_BASE_URL` unset, not `https`, or not a bare origin (the host calls it) | `ErrPublicBaseURLRequired` / `ErrPublicBaseURLInsecure` / `ErrPublicBaseURLMalformed` | none |
| sdk `notify` | `RequireMandatoryTLS()` makes `CheckTransport` fail an opportunistic-STARTTLS transport; `SMTPConfig.RequireTLS` declares mandatory TLS and `Probe` proves it | `ErrOpportunisticTLS` | the authentication acknowledgement above |

Persistent stores are still the host's choice: an in-memory authentication or authorization
store constructs in production, and `guard --posture` does not see which store a `Config`
names. The scaffold wires the chosen connector.

## Documented only: nothing refuses it today

| Knob | Today | Planned |
|---|---|---|
| `LOG_LEVEL=DEBUG` | authorization decision records carry principal and resource ids | keep INFO in production; redaction handler (S6) |
| Authentication failure events | the rail is required in production (above) but many failure paths still write no event | failures on every path (S2) |
| `Cache-Control: no-store` on `/auth` | the auth HTML pages set their own headers; JSON auth responses do not say `no-store` | S5 |
| Database privileges | the role split and `REVOKE` in *Production database posture* are documented, not probed | privilege probe at boot (S2) |

## Trust boundary

- **TLS terminates at the edge.** `web.Run` serves plain HTTP. `web.SecurityHeaders`
  (installed by the scaffold and every example; guard P3) sets `Strict-Transport-Security:
  max-age=31536000; includeSubDomains` plus `nosniff`, `Referrer-Policy`, `X-Frame-Options:
  DENY` and a minimal `Permissions-Policy`; HSTS is sticky, so install it only on an origin
  that is fully `https`, and use `SecurityHeadersWithConfig` (`DisableFrameOptions`) where CSP
  `frame-ancestors` is the framing control. Cookies are `Secure` in production (row 12), every
  URL the authentication pocket builds must be `https` (rows 9–11), and `ValidateProduction`
  refuses a non-`https` `PUBLIC_BASE_URL`.
- **The database link is inside the boundary and must be TLS** (table above): production
  refuses a plaintext or unverified target unless acknowledged; set `sslmode=verify-full` and
  treat the DSN as a secret.
- **Client IP attribution** relies on `web.TrustProxies` running first; the scaffold and every
  example install it (guard P2), so audit rows and per-IP budgets see the client, not the
  proxy.
- **Redis is inside the authorization trust boundary when TupleCache is on.** Cached tuple
  sets are not signed; write access to that Redis is equivalent to write access to the
  authorization database. Run it dedicated, with TLS and AUTH, and treat it as an in-scope
  system. Without TupleCache, Redis holds only rate-limit windows, cache pages and events.
- **Audit tables are append-only at the Go port, not at the database.** See *Production
  database posture* for the role split and the `REVOKE` that makes them so.

## Revocation SLA

How long a revoked principal keeps access, by path. "Default tier" is `LivenessOnMutation`,
the zero value: safe methods skip the session read for first-party tokens.

| Event | Mutations | Reads (default tier) | Reads (`Live()` tier) |
|---|---|---|---|
| Deactivation (`SetUserStatus`): status, `auth_revision` bump, every session and grant deleted in one transaction | immediate | up to `AccessTokenTTL` (default 15 min) for an outstanding access JWT | immediate |
| Session revoked (`RevokeUserSession`, `RevokeAllUserSessions`) | immediate | up to `AccessTokenTTL` | immediate |
| Password change, reset, set or remove (revokes every session and grant) | immediate | up to `AccessTokenTTL` | immediate |
| Refresh-token reuse detected | the session is burned at once; unconditional WARN even with no audit rail | — | — |
| API key revoked | immediate (every API-key authentication reads the row) | immediate | immediate |
| External identity provider revokes or disables the user | mutations re-check per the provider's revocation checker; providers without one are not re-checked | up to the token's `exp` (the framework imposes no maximum accepted lifetime today) | per route |
| Local deactivation of an external-provider user | immediate on every request (`users.Get` is never cached) | immediate | immediate |

Nothing bundled revokes every session for every user at once; see *Keys and rotation*.
