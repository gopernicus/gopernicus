---
title: Production posture
description: What the framework refuses in production today, what is documented only, the trust boundary, and the revocation SLA a host inherits.
---

# Production posture

This page describes the framework **as it is on `main` at 2026-10-22**. Where a control is
planned rather than present, it says so; an item marked *planned* is not a control a host
can claim yet. The SOC 2 readiness work tracks the planned items by slice (S1, S2, …).

## Refused at construction in production

Only `pockets/authentication` has a production mode today. `authentication.New` takes
`RuntimeMode` positionally (`environment.Mode`, no default); with `production` these
checks fail construction. Every other pocket and connector is mode-blind (next section).

| # | Production refuses | Sentinel | Where |
|---|---|---|---|
| 1 | an unset or unknown runtime mode (all modes) | `ErrRuntimeModeRequired` | `pockets/authentication/security.go` `validateRuntimeMode` |
| 2 | an unset or unknown delivery mode (all modes) | `ErrDeliveryModeRequired` / `ErrDeliveryModeInvalid` | `constructor.go` |
| 3 | a missing hasher (password flows on), mailer (delivery on) or token signer (all modes) | `ErrHasherRequired`, `ErrMailerRequired`, `ErrTokenSignerRequired` | `constructor.go:79-85` |
| 4 | `jobs` delivery without `DeliveryJobsAcknowledged` | `ErrDeliveryJobsUnacknowledged` | `constructor.go:206-207` |
| 5 | `in_process` delivery without `DeliveryEphemeralAcknowledged` | `ErrDeliveryEphemeralUnacknowledged` | `constructor.go:213-214` |
| 6 | a development-only or capability-less mail/SMS transport (console senders) | transport error | `security.go` `validateDeliveryTransports` |
| 7 | an in-process rate limiter (`ratelimiter.Memory` or a limiter declaring `InProcessOnly`) | `ErrNonDurableRateLimiter` | `security.go:88` |
| 8 | no `IdentifierKeyer` | `ErrIdentifierKeyerRequired` | `constructor.go:264-265` |
| 9 | no `PasswordResetURL` when the reset rail is wired, or an `http` one | `ErrPasswordResetURLRequired` / `ErrPasswordResetURLInsecure` | `constructor.go:380-381`, `security.go:134` |
| 10 | an `http` `PublicAuthBaseURL` when passwordless is on | `ErrPublicAuthBaseURLInsecure` | `security.go:118` |
| 11 | an `http` `OAuthLinkBaseURL` when set | `ErrOAuthLinkURLInsecure` | `security.go:144` |
| 12 | a session cookie without `Secure` | invalid input | `configuration.go:51-52` |
| 13 | a delegated (MCP) issuer that is not absolute `https` | invalid input | `logic/authentication/constructor.go:66` |
| 14 | an `http` origin namespace in `OriginPolicy`; an `http` OAuth callback base (native redirect URIs allow `http` on loopback only) | invalid input | `inbound/http/adapter.go`, `internal/redirect/oauth_config.go:45` |
| 15 | challenges wired without a `ChallengeProtector`; delivery that can send without a `DeliveryEncrypter` (all modes) | `ErrChallengeProtectorRequired`, `ErrDeliveryEncrypterRequired` | `constructor.go:170,184` |
| 16 | contradictory wiring (a gate without its repositories, an HTML policy without views, half-wired pairs) (all modes) | typed errors | `constructor.go:103-160` |

## Documented only: nothing refuses it today

A host can pass every check above and still run with each of these. They are the
production checklist an operator signs off by hand until the planned slice lands.

| Knob | Today | Planned |
|---|---|---|
| Authentication audit trail (`Repositories.SecurityEvents`) | nil is silent in every mode: no audit rows, no error, no WARN | required in production unless acknowledged (S1) |
| Authorization change history (`WithAudit()` on the store + `Repositories.Audit`) | default off; no production mode in the authorization pocket | required in production unless acknowledged (S1) |
| Persistent stores | in-memory authentication and authorization stores construct in production | surfaced by `gopernicus guard --posture` (S1) |
| Auth keys provisioned | the reference host (`examples/auth-cms`) mints random per-boot keys for `AUTH_JWT_SECRET`, `AUTH_CHALLENGE_PEPPER`, `AUTH_DELIVERY_ENCRYPTER_KEY`, `AUTH_IDENTIFIER_KEY` when unset, in every mode, with a WARN; the pocket cannot tell | fail-closed loader (S1) |
| Database TLS | `pgxdb` forwards `sslmode` verbatim (libpq default `prefer`, no certificate check); `turso` accepts any URL scheme; the `init` scaffold ships `DB_SSLMODE=disable` | verified TLS required in production (S1) |
| Client IP behind a proxy | `web.TrustProxies` must be installed by the host; the scaffold and five of six examples do not, so audit rows and per-IP budgets see the proxy address | scaffold + guard rule (S1) |
| Public origin is `https` | `web.ServerConfig.Origin()` derives `http://` when `PUBLIC_BASE_URL` is unset and validates nothing | `ValidateProduction()` (S1) |
| HSTS and baseline headers | none on JSON or host routes; the auth HTML pages and OAuth2 endpoints set their own | `web.SecurityHeaders` (S1) |
| cms admin surface | a nil `cms.Config.AdminMiddleware` mounts the admin routes ungated | deny-by-absence (S1) |
| Mail transport TLS | SMTP STARTTLS is opportunistic; a relay without it gets cleartext | `RequireTLS` + production refusal (S1) |
| `.env` in production | `environment.LoadEnv` reads `.env` in every mode | mode-aware loader (S1) |
| Query logging | `DB_LOG_QUERIES=true` logs bind arguments verbatim (hashes, emails, IPs) in any mode | refused in production unless acknowledged (S1) |
| `LOG_LEVEL=DEBUG` | authorization decision records carry principal and resource ids | keep INFO in production; redaction handler (S6) |
| Tracing exporter | OTel defaults to the stdout exporter at 100 % sampling | explicit exporter required in production (S1) |

## Trust boundary

- **TLS terminates at the edge.** `web.Run` serves plain HTTP; the framework emits no
  `Strict-Transport-Security` header today (the edge sets it; after S1 the app sets it too).
  Cookies are `Secure` in production (row 12), and every URL the authentication pocket
  builds must be `https` (rows 9–11).
- **The database link is inside the boundary and must be TLS** (table above): set
  `sslmode=verify-full` and treat the DSN as a secret.
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
