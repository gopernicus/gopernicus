---
title: Production posture
description: What the framework refuses in production today, what is documented only, the trust boundary, and the revocation SLA a host inherits.
---

# Production posture

This page describes the framework **as it is on `main` at 2026-10-24, after SOC 2 slices S1–S3,
S4a, S5 and S6** (the production posture floor, audit completeness and integrity, retention and
erasure, the MFA posture, transport and abuse hardening, secrets and logging; release records
in `RELEASING.md`, host handoffs in `plans/soc2-s1-production-posture-handoff.md`,
`plans/soc2-s2-audit-completeness-handoff.md` and `plans/soc2-s3-retention-erasure-handoff.md`;
S4a, S5 and S6 have no handoff file — their records carry the upgrade notes).
Where a control is still planned, it says so; an item marked *planned* is not a control a host
can claim yet.

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
| authentication | native single-factor login: password flows on, or any `Passwordless` kind (S4a widens the causes, *Added by S4a* below) | `ErrSingleFactorUnacknowledged` | `IdentityConfig.SingleFactorAcknowledged` |
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

**Added by S5** (authentication v0.24.0, pgxdb v0.13.0, go-oidc v0.2.0):

| Module | Production refuses | Sentinel | Acknowledgement |
|---|---|---|---|
| authentication | a session cookie with a `Domain` attribute | `ErrCookieDomainRefused` | `BrowserConfig.CookieDomainAcknowledged` (one `posture_waiver` WARN) |
| authentication | a cookie `Domain` beside an `OriginPolicy` namespace rule (all modes): a namespace keeps session cookies host-only | `ErrCookieDomainWithNamespaceRule` | none — remove one |
| pgxdb | `StatementTimeout` or `LockTimeout` set while the DSN or `PGOPTIONS` names the same parameter (all modes) | `sdk.ErrInvalidInput` at `Open` | none — set one |
| go-oidc | a non-loopback `http` issuer it would discover from, i.e. without `WithJWKSURL` (all modes) | `New` fails | `WithInsecureIssuer()` (one WARN; development only — the authentication pocket's production engine refuses `http` issuers) |

Two switches are **waivers** rather than acknowledgements: they refuse nothing, they turn a
fail-closed default open. `AbuseProtectionConfig.PublicRouteLimiterFailOpen` admits requests
when the rate limiter errors (production logs one `posture_waiver=PublicRouteLimiterFailOpen`
WARN at `New`); `PasswordConfig.CompromisedPasswordFailOpen` admits a password when the breach
checker errors (one WARN per admitted check). Both are literals in the composition root, and
`guard --posture` (workshop/gopernicus v0.8.0) prints a `posture_waiver` line for each one
written `true` (P5), where an acknowledgement prints `ack`.

**Added by S6** (sdk v0.18.0, pgxdb v0.14.0, turso v0.9.0):

| Module | Production refuses | Sentinel | Acknowledgement |
|---|---|---|---|
| turso | `LogQueries` (`DB_LOG_QUERIES`) or `LogQueryArgs` (`DB_LOG_QUERY_ARGS`), checked before the TLS test and joined with any TLS refusal; the error names field and variable, `Config.LogQueries (DB_LOG_QUERIES)` | `ErrQueryLoggingRefused` | none — unset it |
| pgxdb | `LogQueryArgs` (`DB_LOG_QUERY_ARGS`), even alone, beside S1's `LogQueries` and logging `Tracer`; the error names field and variable | `ErrQueryLoggingRefused` | none — unset it |
| pgxdb, turso | a revealed `DSN`/`Password` (pgxdb) or `URL`/`AuthToken` (turso) containing `[redacted]`, at `Open` before any dial (all modes) | `sdk.ErrRedactedSecret`, naming the field | none — compose from `Reveal()` |
| sdk `notify/email` | an `SMTPConfig.Password` containing `[redacted]`: `Probe` and `Send` fail (all modes; production hosts probe at boot) | `sdk.ErrRedactedSecret` | none |

`guard --posture` (workshop/gopernicus v0.9.0) gives the turso refusal its pre-deploy signal: an
uncommented `DB_LOG_QUERIES` or `DB_LOG_QUERY_ARGS` set true in `.env.example`, bare or
prefixed, is a P7 finding, as an uncommented `sslmode=disable` has been since S1.

**Added by S4a** (sdk v0.19.0, authentication v0.25.0). The S1 single-factor refusal now
covers every login source that admits a single-factor (AAL1) user, and it names the
source as a cause. There are four causes. `native_password` means password flows are on.
`passwordless` means any `Passwordless` kind is wired. `native_oauth` means any OAuth
`Providers` are wired; it is new, and an OAuth link is AAL1. `external_no_mfa` means a
`WithExternal` provider without `RequireMFA` while `MFAConfig.Required` is none; it is
also new. Native causes count under any `Required`, because native login cannot produce
AAL2 yet.

| Module | Production refuses | Sentinel | Acknowledgement |
|---|---|---|---|
| authentication (`New`) | any present cause, naming the causes | `ErrSingleFactorUnacknowledged` | `IdentityConfig.SingleFactorAcknowledged` (one `posture_waiver=SingleFactorAcknowledged` WARN with `causes=[…]`); `WithMFA` with `admins` or `all`, or `RequireMFA` on every provider, clears `external_no_mfa` instead |
| authentication (`NewExternalAuthenticator`, standalone) | a provider without `RequireMFA` (cause `external_no_mfa`); before S4a this constructor ran no posture check | `ErrSingleFactorUnacknowledged` (the same `errors.Is` target) | `ExternalConfig.SingleFactorAcknowledged` (one waiver WARN), or `RequireMFA` on every provider; root `New` refuses this field with `sdk.ErrInvalidInput` and reads the `IdentityConfig` one |
| authentication | an `MFAConfig.Required` the wiring cannot satisfy (all modes): any requirement without `WithExternal`, `admins` without `AdministrationConfig.UserAdminCheck`, `all` beside native password, passwordless or OAuth login | `ErrMFAUnsatisfiable`, naming the field | none — change the named field |

Development logs one WARN naming the acknowledgement and the causes for every shape that
production would refuse, and that WARN is the pre-deploy signal. One acknowledgement covers
every cause, and the `causes` attribute on the waiver keeps it auditable. `guard --posture` P5
prints `ack` for a `SingleFactorAcknowledged: true` literal on either config, with no
workshop change. Every `New` logs `auth: mfa posture mfa_required=<none|admins|all>
gates=<none|/auth/admin/users*|external_tokens>`. The gates themselves are in the
authentication page's *Assurance and MFA*.

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

## Transport and abuse (S5)

Each flip below names its escape hatch; `RELEASING.md`'s S5 section carries the full upgrade
notes table with module versions.

- **`no-store` on `/auth`.** Every response the authentication pocket's routes write — HTML
  pages, the OAuth2 authorization server, every JSON arm — carries `Cache-Control: no-store`.
  No knob.
- **Per-route IP budgets.** Per minute per client IP: `POST /auth/register` 10,
  `/auth/verify` 20, `/auth/password/reset` 20, `/auth/refresh` 30 (the IP arm; the per-session
  30 stays fixed), both verify-link routes 20, native OAuth start/complete/link-start 40 on one
  shared key, invitation decline 10. `/auth/password/forgot` and `/auth/token` keep their per-IP
  budgets in logic (`AuthenticationLimits`). Over budget is a 429 with `Retry-After`. Each is a
  `ratelimiter.Limit` on `AbuseProtectionConfig.PublicRouteLimits`; there is no off switch — a
  host behind its own edge throttle sets a large number.
- **Origin gate on the JSON arm.** The JSON handlers of register, verify, forgot, reset, token and
  verify-link answer 403 to a browser-borne request (one carrying `Origin` or `Sec-Fetch-Site`)
  that fails the origin policy; a client sending neither passes. An SPA calling them adds its
  origin to `BrowserConfig.AllowedOrigins` or `OriginPolicy`.
- **Limiters fail closed, in every mode.** A limiter error — a Redis or Postgres outage, the
  memory limiter at 10,000 keys — answers 503 with `Retry-After: 5` on every budgeted route, on
  refresh (a silent refresh fails, so SPA users are signed out) and on every host route behind
  `RateLimitByIP`. `PublicRouteLimiterFailOpen: true` admits instead, with a WARN per failure and,
  in production, the waiver above; the production answer is a highly available limiter.
- **Cookie `Domain`.** The two refusals in *Added by S5*: acknowledge a production `Domain`, and
  never pair one with a namespace rule.
- **`X-Real-IP`.** `web.TrustProxies(n)` reads `X-Forwarded-For` only, parses the chosen hop as
  an IP and falls back to the peer on anything else. A proxy that sets only `X-Real-IP` needs
  `web.TrustProxiesWithConfig(web.TrustProxiesConfig{TrustedProxyCount: n, TrustRealIPHeader:
  true})`, which reads it only when the count is at least 1 and `X-Forwarded-For` is absent
  (guard P2 accepts either form). Until then every client resolves to the proxy, so the budgets
  above key the whole site to one IP; the authentication pocket logs a WARN naming
  `TrustRealIPHeader` when a request carries `X-Real-IP`, no `X-Forwarded-For`, and resolves to
  the peer.
- **Statement and lock timeouts.** `pgxdb.Config.StatementTimeout` and `LockTimeout`
  (`DB_STATEMENT_TIMEOUT`, `DB_LOCK_TIMEOUT`) bound every statement and lock wait on the pool;
  zero, the default, leaves the server's or role's setting. Set them on the server process only:
  a migrator that parses the same environment zeroes both after parsing, as the scaffold's
  runner does from workshop/gopernicus v0.8.0, or index builds inherit the bound. Behind
  PgBouncer in transaction mode leave them zero and use `ALTER ROLE … SET`. Turso has no
  statement timeout.
- **https-only issuer discovery.** `gooidc.New` refuses a non-loopback `http` issuer when it
  discovers keys from it; `WithInsecureIssuer()` admits one in development, and a pinned
  `WithJWKSURL` fetches nothing from the issuer and needs no hatch.
- **Bounded JSON decode.** `web.DecodeJSON` reads at most 1 MiB (413 beyond it, whatever an
  outer `MaxBytesReader` allows) and answers decode failures with fixed sentences, never Go type
  names; `web.DecodeJSONLimit[T](r, n)` sets another bound. A panic on a JSON request answers a
  JSON 500, and a 503 without `Retry-After` gets `Retry-After: 5`.
- **Explicit-public page cache.** `cacher.Pages` stores a response only when its
  `Cache-Control` carries `public` or `s-maxage`; header-less or `max-age`-only HTML is no
  longer cached. `PageConfig.AllowImplicitPublic: true` restores it for routes that are public
  by construction (the cms pocket sets it on its own public mount and sends no directive).
- **cms uploads.** `Upload` stores only the sniffed types in `Config.AllowedMediaTypes`
  (default png, jpeg, gif, webp, pdf — no SVG; anything else is a 400 naming the list), caps
  bodies at `Config.MaxUploadBytes` (32 MiB, a 413), and media responses carry `nosniff` and a
  `Content-Disposition` — `inline` for images, `attachment` for everything else, so PDFs
  download.

## Secrets and logging (S6)

Each flip below names its escape hatch; `RELEASING.md`'s S6 section carries the full upgrade
notes table with module versions.

- **Credentials are `sdk.Secret`.** Every integration `Config` field that holds a credential is
  an `sdk.Secret`: pgxdb `DSN`/`Password`, turso `URL`/`AuthToken`, `email.SMTPConfig.Password`,
  goredis `URL`/`Password`, sendgrid `APIKey`, s3 `SecretAccessKey`, gcs `CredentialsJSON`,
  google/github `ClientSecret` and firebase `CredentialsJSON`. A Secret renders `[redacted]`
  through `fmt` (every verb), `slog`, `encoding/json` and `MarshalText`; `Reveal()` is the one
  read. `environment.ParseEnvTags` fills one like any string and an untyped literal still
  assigns; a `string` variable converts with `sdk.Secret(v)` (a compile error until it does, and
  firebase's former `[]byte` breaks every builder: `sdk.Secret(string(b))`). The adapters that
  keep a secret in an unexported field — google/github `Provider`, sendgrid `Sender`, gcs
  `Store`, golang-jwt `Signer` — implement `fmt.Formatter` and `slog.LogValuer`, so `%+v` or
  `slog.Any` of one prints a redacted summary; a host's own wrapper struct is its own to cover.
  Guard G37 keeps every new credential-named field under `integrations/` and `sdk/` an
  `sdk.Secret`.
- **A composed secret fails loudly.** A DSN or URL built from a Secret with `fmt` or `+` carries
  `[redacted]` instead of the credential. pgxdb and turso `Open`, and SMTP `Probe`/`Send`, refuse
  it with `sdk.ErrRedactedSecret` before any dial (table above): compose from `Reveal()` and wrap
  the result in `sdk.Secret(…)`. A key composed into a sendgrid, s3, gcs or OAuth call fails at
  its first use. `pgxdb.Config.Redacted()` and `turso.Config.Redacted()` remain the operator's
  view of the target.
- **Redaction is on by default.** `logging.New` composes `ContextHandler(RedactHandler(…))` in
  every mode and format. An attribute keyed `password`, `passwd`, `secret`, `token`,
  `access_token`, `refresh_token`, `api_key`, `apikey`, `x_api_key`, `authorization`, `cookie`,
  `set-cookie`, `private_key`, `client_secret`, `credentials_json`, `auth_token` or `dsn`
  (`logging.DefaultRedactKeys`) logs `[redacted]` whatever its kind, a group included. Keys match
  exactly, case-insensitively, with `-` and `_` equivalent (`X-API-Key` is `x_api_key`;
  `refresh_token_hash` is not `token`). `LOG_REDACT_KEYS` (comma-separated,
  `logging.Options.RedactKeys`) adds keys and never removes one. There is no off switch: a host
  that must log a value under one of these keys renames the attribute (auth-cms logs its demo key
  as `seeded_api_key`) or composes `logging.NewContextHandler` over its own handler. The handler
  sees keys only: a secret inside an `slog.Any` map or struct, or inside error text, passes
  through — including the request line's `error` attribute, where a pgx invalid-input error
  (SQLSTATE 22P02) repeats the rejected value.
- **The request line says who, from where.** `web.Logger` keeps `method`, `path`, `status`,
  `elapsed`, `remote_addr` (the raw peer), `error`, `aborted` and `hijacked`, never the query
  string, and adds, when known, `client_ip` (from `web.TrustProxies`), `user_agent` (truncated to
  256 bytes at a UTF-8 boundary), one `request_id`, and `principal_type`/`principal_id` — the last
  principal authenticated during the request, including one the surface then refused; observability
  only, never an authorization input. It must sit inside `TrustProxies` and `RequestID`
  (`TrustProxies`, then `RequestID`, then `Logger`), because it reads both from its own request
  context.
  A nested `web.RequestID` reuses the id already on the context, and `ContextHandler` adds no
  second `request_id` to a record that carries one. `client_ip` and `user_agent` are personal
  data: `LOG_REDACT_KEYS=client_ip,user_agent` drops them. Log retention is the host's log
  pipeline; the S3 rails cover datastore rows, not log output.
- **Query logs carry no values, and production refuses them.** `DB_LOG_QUERIES=true`
  (`LogQueries`) logs each query's `sql`, `arg_count` and `arg_types`, never a bind value, with
  the request's `request_id`; a failed pgx query logs its SQLSTATE and constraint (anything else
  its error type), never its error text. `DB_LOG_QUERY_ARGS=true` (`LogQueryArgs`) adds the
  values verbatim (and, on pgx, the error text) — development only, and inert without
  `DB_LOG_QUERIES`. Production refuses any query logging on both connectors (*Added by S6*);
  there is no acknowledgement — unset it.

## Documented only: nothing refuses it today

| Knob | Today | Planned |
|---|---|---|
| `LOG_LEVEL=DEBUG` | authorization decision records carry principal and resource ids | keep INFO in production; the S6 redaction handler matches credential keys, not ids |
| Authentication failure events | the rail is required in production (above) but many failure paths still write no event | failures on every path (S2) |

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
