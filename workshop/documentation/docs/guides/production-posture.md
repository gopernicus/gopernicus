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
--posture` (P0–P9) finds in the host's `cmd/` tree.

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

**Added by S2** (audit integrity), through the pgx store constructors — authentication
`stores/pgx` v0.10.0, authorization `stores/pgx` v0.20.0, secrets `stores/pgx` v0.2.0 — each
of which runs the `pgxdb` append-only probe over its own audit tables. The contract, in one
sentence: in production a store refuses to boot when its runtime role is superuser, owns an
audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified triggers, or cannot
insert into it.

| Module | Production refuses | Sentinel | Acknowledgement |
|---|---|---|---|
| pgxdb (via the pgx stores) | a runtime role that is superuser, owns an audit table (its table, schema or trigger-function owner, by membership), or holds `UPDATE`/`DELETE`/`TRUNCATE`/`TRIGGER` on it without the verified immutability triggers (`DELETE` admitted on `iam_operations`) | `ErrAppendOnlyTableWritable` | `pgxdb.Config.AppendOnlyWritableAcknowledged` (the single-role host; one `posture_waiver` WARN per table) |
| pgxdb (via the pgx stores) | a runtime role that cannot insert into an audit table, or lacks `USAGE` on its sequence | `ErrAppendOnlyTableUnwritable` | none |
| turso (the turso stores) | nothing: a missing or altered immutability trigger pair is a production WARN (SQLite has no roles) | — | — |

Tamper evidence for the audit rails, by store family:

| Family | Status | What holds |
|---|---|---|
| pgx | DEFAULT | the immutability triggers ship as ordinary numbered store migrations, and the posture probe is enforced in production; the runner's `-posture-role` preflights it before deploy |
| turso | DEFAULT | the same triggers ship as migrations; the probe compares their text and only WARNs |

Persistent stores are still the host's choice: an in-memory authentication or authorization
store constructs in production, and `guard --posture` does not see which store a `Config`
names. The scaffold wires the chosen connector.

## Retention (S3)

Every row class the pockets write has an owner-set lifetime, each disposal is bounded, and the
pockets start none of it: the host runs `Components.Maintenance` (authentication),
`Mutations.ExpireOperations` (authorization), `PurgeTerminalJobs` (jobs) and the cms inquiry
eraser from its own schedule — *Operations runbooks*, "Retention". `<=` is the rule everywhere:
a row exactly at the cutoff is eligible.

| Rows | Eligible when | Default | Floor (database) | Who runs the statement |
|---|---|---|---|---|
| `sessions` (both profiles) | `expires_at <= now − SessionRetention` | 24h past expiry (`maintenance.Config`) | none | runtime role, `Components.Maintenance` over `Repositories.Maintenance` |
| `oauth_states` | `expires_at <= now − OAuthStateRetention` | 1h | none | runtime role |
| `authentication_grants` | `expires_at <= now − GrantRetention` | 24h | none | runtime role |
| `contact_changes` | `expires_at <= now − ContactChangeRetention` | 24h | none | runtime role |
| `invitations` | terminal (`accepted`/`declined`/`cancelled`) with `updated_at <=` the cutoff, or `pending` with `expires_at <=` it; **never `accepting`** | `InvitationRetention` 90 days | none | runtime role |
| `challenges` | `Challenges.PurgeExpired(now − 1h)` | fixed 1h | none | runtime role |
| OAuth2 codes, client metadata, refresh history | `expires_at <= now`, bounded per table (`PruneOAuth2`; the unbounded `OAuth2.Prune` is never called) | the stores' own horizons | none | runtime role |
| `security_events` | `created_at <= now − SecurityEventRetention`, archived then purged per chunk, once a day | 400 days; below `maintenance.MinSecurityEventRetention` (366 days) refused | **365 days, a framework invariant**: pgx `security_events_retention_floor()`; turso row 1 of `security_events_retention.floor_days` (SQLite has no roles: a footgun guard, not a control) | pgx: the runtime role calls `security_events_purge_before`, a migrator-owned `SECURITY DEFINER` function; turso: the store's capture-drop-restore transaction |
| `user_identifiers` (retired rows) | not purged: kept for live users, blanked on erasure | — | — | — |
| `iam_operations` | `applied`/`no_change`/`not_found` with `committed_at <=` the cutoff; fenced never | `authorization.WithOperationRetention` 180 days; below 7 days refused (`ErrOperationRetentionTooShort`) | none | runtime role (`DELETE` stays granted), `Mutations.ExpireOperations` over `Repositories.OperationExpiry` |
| `job_queue` (ordinary) | `completed`/`dead_letter` with `updated_at <= before` | host-chosen; never below the scheduler's longest retry window (a purged job frees its id for a late refire) | none | runtime role, `Queue.PurgeTerminalJobs` |
| `fenced_job_queue` | terminal, `Queue.PurgeTerminal` (unchanged) | host-chosen | none | runtime role |
| `event_outbox` | published, `outbox.PurgePublished(before)` — unbounded, so its own step | host-chosen | none | runtime role |
| cms `inquiries` | `created_at <= before` (`PurgeBefore`); `Delete(id)`; `DeleteByEmail(email)` (ASCII case-fold equality) | host-chosen | none | runtime role, through the `messaging.InquiryEraser` wired as `Repositories.InquiryMaintenance` |
| `oauth_refresh_history` | pruned by `PruneOAuth2` only past the store's reuse-detection horizon; never by erasure | — | — | — |
| `iam_audit`, `secrets_audit` | **host-owned**: no function ships; the definer-function template in *Production database posture* is the recipe | host | host | host migration + host cron |
| `ratelimit_windows` | the pgxdb README's cron, `DELETE … WHERE expires_at < now()` | host | — | host cron |

The pocket cannot see whether the host runs its maintenance, so the host says so:
`authentication.WithMaintenance(authentication.MaintenanceConfig{…, MaintenanceRunnerAcknowledged:
true})`, a literal in the composition root that `guard --posture` prints as an `ack` line (P5).
While it is false, production logs ONE WARN — `maintenance_runner=unacknowledged: retention is
not enforced; run Components.Maintenance.Run or the jobs schedule and set
MaintenanceConfig.MaintenanceRunnerAcknowledged` — a WARN today, a refusal at the roadmap's end. A
host enforcing retention through its own SQL cron leaves it false and accepts the WARN. Every
`New` logs `maintenance_runner=`, `archive_sink=<ArchiveLocation|none>` and
`security_event_retention=`; no sink is a valid policy (the rail is purged unarchived), not a
warning. In production back `ArchiveSink` with an object store — `integrations/filestorage/gcs`
or `integrations/filestorage/s3` — rather than `filestorage.NewDisk`: a disk sink is per instance
and as durable as that disk, and the archive holds IP and User-Agent (PII) that erasure does not
reach.

| Module | In production | Signal |
|---|---|---|
| authentication | `MaintenanceRunnerAcknowledged` false | the WARN above; the acknowledgement beside a nil `Repositories.Maintenance` is `ErrMaintenanceAcknowledgedWithoutRepository` in every mode |
| authentication | `AdministrationConfig.UserErasureRoute` without `Repositories.UserErasure` or `UserAdminCheck`; `UserErasure` without `ActiveSessions` (all modes) | `ErrUserErasureRouteRequires`, `ErrUserErasureRequiresActiveSessions` |
| authentication `stores/pgx` | a database without `users.erased_at` or the 0024 functions (all modes) | refused, naming `0023_retention.sql` or `0024_security_events_retention.sql` |
| authentication `stores/pgx` | an unsafe purge function: PUBLIC holds `EXECUTE`, `search_path` not pinned, or it resolves outside the rail's schema | `pgxdb.ErrDefinerFunctionUnsafe`, not waivable |
| authentication `stores/pgx` | an inoperative purge function: not `SECURITY DEFINER`, owner not a member of the table's owner, a superuser owner, or `CREATE` on the schema for the runtime role | one `definer_function=inoperative` WARN; the rail step then fails |
| authentication `stores/pgx` | a runtime role without `EXECUTE` on the purge function | one `maintenance_execute=missing` WARN naming the `GRANT`; the rail step reports `forbidden` every pass while every other step runs |
| authorization | `Repositories.Mutations` without `Repositories.OperationExpiry` | one WARN (`operation_expiry=false`) |
| cms | nil `Repositories.InquiryMaintenance` | one WARN (`inquiry_maintenance=absent`) |
| jobs | an ordinary `Repositories.Queue` that does not implement `queue.TerminalPurger` | one WARN in **every** mode — the queue service takes no runtime mode; every bundled store implements it |
| authorization, jobs and cms pgx stores | the admitted retention index missing (all modes) | one `index_missing=` WARN; the purge still runs, sorting per batch |

## Documented only: nothing refuses it today

| Knob | Today | Planned |
|---|---|---|
| `LOG_LEVEL=DEBUG` | authorization decision records carry principal and resource ids | keep INFO in production; redaction handler (S6) |
| Authentication failure events | the rail is required in production (above) but many failure paths still write no event | failures on every path (S2) |
| `Cache-Control: no-store` on `/auth` | the auth HTML pages set their own headers; JSON auth responses do not say `no-store` | S5 |

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
  example install it (guard P2). It makes audit rows and per-IP budgets see the client, not the
  proxy, only on routes that carry the authentication pocket's client-info stamp: the pocket's
  own routes always, a host route once the host installs
  `router.Use(authenticationComponents.HTTP.ClientInfoMiddleware())` after the `TrustProxies`
  `Use` (auth-cms and production-baseline do). Without it a host route's audit rows carry no
  IP or User-Agent and production logs one `mount ClientInfoMiddleware` WARN. Attribution is
  only as good as `TRUSTED_PROXY_COUNT` and an origin reachable solely through those proxies.
- **Redis is inside the authorization trust boundary when TupleCache is on.** Cached tuple
  sets are not signed; write access to that Redis is equivalent to write access to the
  authorization database. Run it dedicated, with TLS and AUTH, and treat it as an in-scope
  system. Without TupleCache, Redis holds only rate-limit windows, cache pages and events.
- **Audit tables are append-only in the database** on pgx (immutability triggers and the
  boot-time privilege probe, from the S2 store tags above) and by trigger only on turso. See
  *Production database posture* for the role split.

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
