# Capability map — gopernicus-original → the new structure

Status: executed 2026-07-02 (phase 4, W1 + W4).
**Execution note (2026-07-02, jobs-v1 close):** the Jobs & events
section's jobs rows are BUILT — `sdk/workers` (pool + Runner[T]), the
scheduler/queue feature (`features/jobs` + `stores/{turso,postgres}` +
in-core memstore), and the cron port + `integrations/scheduling/
robfig-cron` (ratified call #6's shape, with `CronSchedule` as a type
alias) — all live-verified incl. the §8 proof protocol. The events rows
(bus, outbox, SSE) remain deferred per jrazmi's 2026-07-02 scope ruling;
`sdk/workers` already satisfies the outbox poller's stated requirements.
**Execution note (2026-07-02, auth-v1 close):** the Authentication &
identity section's v1 rows are BUILT — Authenticator core (password +
sessions), the five entity-repository ports, `PasswordHasher` port +
`integrations/cryptids/bcrypt`, the login/session HTTP surface, and the
identity-in-context convention (feature-internal, per the design doc's W3
revision) — as `features/auth` + `stores/{turso,postgres}` +
`examples/auth-cms`, live-verified. The Datastores section's postgres-
connector row is BUILT (`integrations/datastores/postgres`, portability
P1). v2 rows (OAuth, API keys, JWT, invitations, ReBAC, tenancy) remain
deferred as classified.
**Execution note (2026-07-07, telemetry-closeout):** the Telemetry
section's rows are BUILT — `sdk/tracing` port + `Noop`,
`integrations/tracing/otel` (stdout/OTLP exporters), the trace-aware
`sdk/logging` handler, and the request-scoped span middleware
(`sdk/web.Tracing`, place outer of `web.Logger`), all real-drive-verified
on `examples/cms` (remote playground Turso; span/log excerpts in
`.claude/plans/telemetry-closeout/plan.md`). Metrics stay N/A (no original
capability). The "Bridge transit middleware" residue rows (max-body-size /
client-info / trust-proxy / idempotency-key, and the generic HTTP
rate-limit middleware under Rate limiting) remain backlog — now
trigger-gated in the NOTES.md 2026-07-07 demand-gated deferral ledger, not
scheduled.
**Execution note (2026-07-07, auth-v2 close):** the Authentication &
identity section's v2 rows are BUILT into `features/auth` +
`stores/{turso,pgx}` (zero new modules): the v2 entity-ports row — API
keys, OAuth accounts, service accounts, security events (the synchronous
audit table, §5.1) — **except principals, which is NOT SALVAGED per
ratified AV5** (actor references are `(subject_type, subject_id)` string
pairs; `auth.Principal` is a value type; registry demand-gated); the
`JWTSigner` row (port consumed as `sdk/cryptids.JWTSigner`;
`integrations/cryptids/golang-jwt` host-wired, stateless short-TTL user
tokens per AV6); the `TokenEncrypter` row (AES-GCM shipped as
`sdk/cryptids.AESGCM`); both invitations rows — **decoupled from ReBAC
and events per ratified AV4** (grant-on-accept `Granter` port,
deny-by-absence routes; this row's original "depends on: authorization,
events" is dissolved — the A9 proof host grants through a toy membership
map with no ReBAC in its module graph); the OAuth provider ports/flow
(feature flow over the already-built `sdk/oauth` + `integrations/oauth/*`;
mobile flow + code-gated unlink OUT per AV7); and the open-redirect
allowlist matcher (feature-internal, per its row). The security-events
**durable emission rail (outbox) is DEFERRED per ratified AV10** — trigger:
the first real durable consumer (webhooks/alerting); governed by the
auth-v2 design doc §5.2. The Authorization/ReBAC rows (engine + storage)
remain deferred to the `authorization-v1` milestone (2026-07-06 ruling:
supported, never required); tenancy remains trigger-gated as classified.
Live-verified end to end (A9 protocol + per-dialect store runs; NOTES.md
2026-07-07 auth-v2 close entry is the record).
Source: exhaustive walk of `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original`
(`github.com/gopernicus/gopernicus`, the old single-module framework), top-level by
top-level: `bridge/`, `core/`, `infrastructure/`, `sdk/`, `telemetry/`, `workshop/`.

Target-home vocabulary: **sdk** facility (`sdk/<concern>`, stdlib-only, per
`sdk/README.md`'s admission policy) · **integration**
(`integrations/<category>/<tech>`, one third-party lib, per constitution rule 2) ·
**feature** (`features/<name>`, datastore-free core + `stores/<dialect>`, per
`features/README.md`) · **workshop v2** (generation-time only, scoped in
`05-workshop-v2-brief.md`, not built this milestone) · **drop** (a documented
non-decision to build).

Rows marked **YOUR CALL** are genuinely contested; each carries jrazmi's default
recommendation, not an executor decision — flag before building on them.

## Authentication & identity (the auth feature's territory — see `auth-feature-design.md`)

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Authenticator core (login/register/token refresh/session validation/password mgmt/email verification/OAuth orchestration) | `core/auth/authentication/{authenticator,repositories,hooks,...}.go` | **feature** (`features/auth` core, v1 minus OAuth) | Domain logic with its own entities/storage a host mounts; already declares its own ports (`PasswordHasher`, `JWTSigner`, 5 repository ports) satisfied structurally by infra — the exact pattern the phase file calls "the good pattern to generalize." v1 scope (password + sessions only) detailed in `auth-feature-design.md` §1 | sdk/web, sdk/errs, sdk/id, sdk/email, sdk/ratelimiter | L |
| Auth entity repositories & ports: users, user-passwords, sessions, verification codes, verification tokens | `core/repositories/auth/{users,userpasswords,sessions,verificationcodes,verificationtokens}/` + generated `*pgx` adapters | **feature** (`features/auth` — `user`, `session`, `verification` public packages; ports only, generated pgx adapters do NOT carry over — see `stores/<dialect>` row below) | These 5 are the original `Authenticator.Repositories`' own **required** set (`NewRepositories`'s 5 params) — not an arbitrary v1 cut, the original's own scope line. Ports belong with authenticator (their consumer), never their SQL implementor (fixes the flaw this whole repo exists to fix) | none (ports are pure Go) | M |
| Auth entity repositories & ports: API keys, OAuth accounts, service accounts, security events, principals | `core/repositories/auth/{apikeys,oauthaccounts,serviceaccounts,securityevents,principals}/` + generated `*pgx` adapters | **feature** (`features/auth` v2 subdomain — explicitly deferred) | These were already **optional** `With*` extensions on the original `Authenticator`, not part of its required Repositories — confirms they're a clean v2 boundary, not a new cut. `auth-feature-design.md` §1 names where each lands | auth v1 core | M |
| `PasswordHasher` port + bcrypt adapter | `core/auth/authentication/authenticator.go` (port), `infrastructure/cryptids/bcrypt` (adapter, `golang.org/x/crypto/bcrypt`) | port: **feature** (`features/auth`, declared by its consumer) — adapter: **integration** (`integrations/cryptids/bcrypt`) | Rule 1 (needs 3rd-party lib) for the adapter; rule 3 (ports live with consumer) for the port — only auth needs password hashing today, fails sdk's plurality test, so it stays feature-owned, not sdk-owned, mirroring the original's own design | golang.org/x/crypto/bcrypt (adapter only) | S |
| `JWTSigner` port + JWT adapter | `core/auth/authentication/authenticator.go` (port), `infrastructure/cryptids/golangjwt` (adapter, `github.com/golang-jwt/jwt/v5`) | port: **feature** (`features/auth` v2, deferred — see design doc §1) — adapter: **integration** (`integrations/cryptids/golangjwt`, future) | v1 uses opaque server-side session tokens (`sdk/id`), not signed JWTs — JWT/bearer-token mode is a v2 concern paired with API keys/service accounts (machine clients), not browser session identity | github.com/golang-jwt/jwt/v5 (adapter, v2) | S |
| `TokenEncrypter` (OAuth provider-token storage) | `core/auth/authentication/authenticator.go` (`WithProviderTokens`) | **feature** (`features/auth` v2, paired with OAuth) | Only meaningful once OAuth account linking exists (v2) | infrastructure/cryptids/aesgcm equivalent | S |
| Authorization (ReBAC permission engine: Check/CheckBatch/FilterAuthorized/LookupResources, schema DSL) | `core/auth/authorization/{authorizer,model,builder,schema_validator,...}.go` | **feature — YOUR CALL** | Seed-flagged contested row. Full relationship engine (subject/resource/through-relation/cycle-detection, recursive-CTE descendant lookup) is real domain logic a host would mount, so **feature** not sdk is right in kind — the open question is *scope*, not *home*. **Recommended default: defer past v1 entirely.** v1 auth ships no authorization decisions beyond "is there a valid session" (`RequireUser`); a simple role/ownership check, if genuinely needed by cms admin gating, can live as an ad-hoc auth-v1.5 addition. Build the full ReBAC engine only when a real multi-tenant/fine-grained permission need appears (mirrors D6's "genuinely foreseen" bar for ratelimiter) — building it speculatively repeats the original's own generator-maturity mismatch (ReBAC is its most mature domain, worth salvaging design-wise, but salvaging ≠ building now) | none (stdlib-expressible engine itself) | L |
| ReBAC storage (relationships incl. recursive-CTE group expansion, relationship metadata, groups, ReBAC-flavored invitations) | `core/repositories/rebac/{groups,invitations,rebacrelationshipmetadata,rebacrelationships}/` + generated adapters | **feature — YOUR CALL**, same as above | Ports would live in the future authorization feature per rule 3; SQL (recursive CTEs) is genuinely dialect-specific, confirming the `stores/<dialect>` split holds even for ReBAC. Not built until authorization row above is greenlit | authorization feature core | L |
| Generic resource invitations (create/token/event/accept, ReBAC tuple creation) | `core/auth/invitations/{inviter,satisfiers}.go` | **feature** (`features/auth` v2, explicitly named in the phase brief's v1-exclusion list) | Domain logic with its own storage; needs authorization (ReBAC tuples) and events, so it's gated behind both those v2 items | authorization feature, events, infrastructure/cryptids-equivalent (token hashing) | M |
| Invitation HTTP flow + email-template subscribers | `bridge/auth/invitations/{bridge,http,model,subscribers}.go` | **feature** (`features/auth/internal/http`, v2, alongside the Inviter above) | Thin adapter over the Inviter; folds into the same v2 milestone | features/auth v2 core, sdk/email | M |
| Login/register/session/password/verification/OAuth HTTP bridge (cookie handling, origin-allowlist, error-code mapping, email-template wiring) | `bridge/auth/authentication/{bridge,cookie,errors,http,model,oauth,oauth_model,subscribers}.go` + `templates/` | **feature** (`features/auth/internal/http`) | Thin HTTP-driving adapter over the Authenticator core — exactly the shape `features/cms/internal/http` already proves. v1 ships the password/session/verification/reset subset only (JSON API, no server-rendered login page — see design doc §2) | features/auth core, sdk/web | L |
| SSE event gateway (per-connection fan-out hub, tenant/resource-scoped streams, connect-time authz) | `bridge/events/ssebridge/{bridge,hub}.go` | **feature** (`features/events`, future — see Jobs/Events section) | Owns real infra logic (fan-out hub) but is domain-shaped (needs an event bus + authorization at connect time) — a host-mounted feature, not sdk. `sdk/web` already carries the *generic* SSE primitives (`sse.go`/`stream.go`); this is the domain-specific consumer of them | events feature core, sdk/web, auth (for connect-time RequireUser) | M |
| Open-redirect origin allowlist matcher | `bridge/transit/allowlist/allowlist.go` | **feature** (`features/auth/internal`, until a 2nd consumer exists) | Stdlib-only, narrow, but only one real consumer today (auth's OAuth/invitation redirect flows) — fails sdk's plurality test *for now*. Per C2's corollary, promote to `sdk/web` the day a second feature needs identical redirect-safety logic, not before | none | S |
| Generated CRUD HTTP admin bridges over auth/ReBAC/tenancy entities (List/Get/Create/Update/Archive/Delete/Restore handlers, pagination, OpenAPI spec, `bridge.yml`-driven) | `bridge/repositories/{authreposbridge,rebacreposbridge,tenancyreposbridge}/**` (15 subpackages, generated.go pattern) | **workshop v2** | Generation-time only (rule 4) — this is exactly the `bridge.yml` + entity-CRUD generation pattern `05-workshop-v2-brief.md` scopes. `features/cms` proves the **registry-driven-routes** alternative needs no generation for CRUD HTTP at all; whether auth's admin surface follows cms's pattern or still wants generated bridges is an open question for whoever builds workshop v2 (already listed in the brief's §4 open questions) | workshop v2 codegen engine | L |
| Identity-in-context convention (context key + accessor for "who is the authenticated caller") | not a distinct original capability — synthesized from `core/auth/authentication` + `bridge/transit/httpmid`'s context helpers | **feature-internal in v1** (`features/auth`, unexported) — **not sdk yet**; revised by the design doc's W3 adversarial pass | Tracing the actual call graph: cms only ever calls `auth.Service.CurrentUser(ctx)` (an exported method), never the context key directly — so there is exactly one real consumer package (auth itself) today, failing sdk's plurality test the same way `PasswordHasher` fails it. Promote to `sdk` (mirroring `sdk/logging`'s `contextKey` pattern) only when a second feature needs the key/accessor directly, without going through `auth.Service`'s API — see `auth-feature-design.md` §3 | none (stdlib `context`) | S |

## Tenancy

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Tenancy (tenant entity + FK-based scoping elsewhere; no dedicated business-logic package in the original) | `core/repositories/tenancy/tenants/` (single entity, `GetBySlug`/`GetIDBySlug`), `bridge/transit/httpmid/tenant.go` (`ExtractTenantID`/`InjectDefaultTenant`), ReBAC schema treats tenant as a resource type | **feature — YOUR CALL: auth subdomain, not a standalone feature, deferred past v1** | Seed-flagged contested row. The original itself never gave tenancy its own business-logic layer — it's one entity repo plus scoping conventions expressed through ReBAC and middleware. Building a standalone `features/tenancy` would out-engineer what the original actually needed. **Recommended default:** fold into `features/auth` as a v2+ subdomain (a `Tenant` entity + `TenantRepository` port, `ExtractTenantID` becoming an auth-owned middleware) once a real multi-tenant host exists — not before, and not as its own feature module | features/auth v2, authorization (if tenant-scoped permissions are needed) | S |

## Jobs & events

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Generic goroutine worker pool + generic job runner (`Runner[T Job]`, retry, hooks) | `sdk/workers/{pool,runner,model,middleware,tracer}.go` | **sdk** (new `sdk/workers`, backlog) | Stdlib-only (`sync/atomic`, `log/slog`), framework-generic, no backend specifics leak through — passes admission policy cleanly, same shape as the original's own sdk placement | none | M |
| Cron-driven job firer (`Scheduler[S Schedule]`, `ListDue`/`ClaimDue` compare-and-set, deterministic refire) | `core/jobs/scheduler/scheduler.go` | **feature** (`features/jobs`, future) | Domain logic with its own storage (`ClaimDue` needs a real datastore for CAS semantics) that a host mounts — not sdk. The cron-*expression-parsing* sub-concern (`github.com/robfig/cron/v3`) is the one third-party dependency this feature would want; per rule 2, that's either a stdlib-only naive parser shipped as `features/jobs`'s own tiny default, or the feature declares a `CronParser` port satisfied by a small `integrations/scheduling/robfig-cron` module — **YOUR CALL: recommend the port + integration split**, keeping `features/jobs`'s own go.mod stdlib+sdk only, consistent with every other feature | sdk/workers (execution), a `CronParser` port (integration-backed) | M |
| Job queue + job schedule entities (`JobQueue` satisfying `sdk/workers.Job`, `jobschedules` backing the scheduler) | `core/repositories/jobs/{jobqueue,jobschedules}/` | **feature** (`features/jobs` core + `stores/<dialect>`) | Same domain as the scheduler row — one feature, ports+entities in core, SQL in stores | features/jobs core | M |
| Event bus (`Bus.Emit/Subscribe/Close`, `TypedHandler[T]`, in-process + Redis Streams backends) | `infrastructure/events/{events,memorybus,goredisbus,poller,wake,registry}.go` | port+memory default: **sdk — YOUR CALL**; Redis backend: **integration** | Contested by kind, not by home: an in-process pub/sub port with a stdlib default is structurally identical to `cacher`/`email`/`ratelimiter` (rule 2's "stdlib-only default ships inside sdk"), and `features/README.md` §6 already names "an event bus port" as a **candidate** `Mount` field. **Recommended default: do NOT build this for auth v1** — v1's email delivery is a direct `Config.Mailer` call (mirrors cms's `Config.Mailer`), no pub/sub needed (see design doc §3). Build the real `sdk/events` port + `Mount` field only when a second real multi-feature event consumer appears (e.g. the SSE gateway feature) | integrations/events/redis (Redis Streams adapter, `github.com/redis/go-redis/v9`) | M |
| Transactional outbox (durable at-least-once delivery, DB-backed poller) | `core/repositories/events/eventoutbox/`, `infrastructure/events/poller/poller.go` | **feature** (`features/events`, future, paired with the SSE gateway row above) | Needs its own persisted storage + a background worker (built on `sdk/workers` above) — domain-shaped, a host mounts it | sdk/workers, event bus port | M |

## Telemetry

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Structured logging (`slog` setup, trace-aware handler) | `sdk/logger/{logger,tracing}.go` | **sdk** (already ported as `sdk/logging` — done, no new work) | Already exists in the new repo; original capability fully covered | none | — |
| Tracing (OTEL span provider, start/inject/extract helpers) | `telemetry/{telemetry,span,context,types}.go`, `infrastructure/tracing/{otlptrace,stdouttrace}` | port + noop default: **sdk — YOUR CALL**; OTLP/stdout exporters: **integration** | Seed-flagged contested row. D9's rationale ("OTEL story returns deliberately via the phase-4 map, not a vestigial field") asks this phase for a real answer, not just a park. **Recommended default:** mirror the cacher/email/filestorage shape exactly — a narrow `sdk/tracing` `Tracer`/`Span` port + a stdlib-only `Noop` default (satisfies the D9 removal's promise that "OTEL story returns deliberately"), with `integrations/tracing/otel` wrapping `go.opentelemetry.io/otel/*` for the real exporter (OTLP or stdout). **Not built this phase** — this is the ratified target, execution is a future milestone | go.opentelemetry.io/otel/* (integration only) | S (port), M (integration) |
| Metrics | seed inventory names it; **no metrics code exists in the original** — `telemetry/` is tracing-only, confirmed by exhaustive read | **N/A — nothing to classify** | Flag for the record: the seed inventory's "OTEL logging/tracing/metrics" wording overstates the original; there is no metrics capability to port. If metrics are wanted, that's new scope, not a migration | — | — |
| Request-scoped OTEL span middleware | `bridge/transit/httpmid/telemetry.go` | **sdk** (`sdk/web` middleware, parameterized by the `sdk/tracing` port above) — bundled with the tracing decision, not built this phase | Generic middleware once the tracing port exists; no reason to special-case it per-feature | sdk/tracing port | S |

## Rate limiting

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Rate limit port + memory backend | `infrastructure/ratelimiter/{ratelimiter,memorylimiter}.go` | **sdk** (already ported as `sdk/ratelimiter` + `Memory` default — done, D6/phase 2 W5) | Already exists; auth v1 is its first real consumer (login-attempt limiting, see design doc §3) | none | — |
| Redis-backed limiter (Lua-scripted atomic sliding window) | `infrastructure/ratelimiter/goredislimiter/` | **integration** (`integrations/ratelimiters/redis`, backlog) | Rule 1: third-party lib implementing the `sdk/ratelimiter.Limiter` port | github.com/redis/go-redis/v9 | S |
| SQLite-backed limiter | `infrastructure/ratelimiter/sqlitelimiter/` | **drop** | `sdk/ratelimiter.Memory` already covers the single-process case SQLite persistence was approximating; no current host needs cross-restart rate-limit durability without also needing Redis. Revisit only if a concrete need appears | — | — |
| Generic rate-limit HTTP middleware (functional-options `RateLimit`) | `bridge/transit/httpmid/rate_limit.go` | **sdk** (`sdk/web` middleware helper over `sdk/ratelimiter`, backlog) | Stdlib-expressible once `sdk/ratelimiter` exists (it does); genuinely reusable across any route, not auth-specific — the original itself used it as generic middleware, not an auth-only mechanism | sdk/ratelimiter | S |

## Caches, storage, email, crypto (facility ports)

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Cache port + in-process/no-op backends | `infrastructure/cache/{cache,memorycache,noopcache}.go` | **sdk** (already ported as `sdk/cacher` + `Memory` default — done) | Already exists | none | — |
| Redis cache backend | `infrastructure/cache/rediscache/` | **integration** (`integrations/caches/redis`, backlog) | Rule 1 | github.com/redis/go-redis/v9 | S |
| File storage port + local disk backend | (new repo's `sdk/filestorage` + `Disk` — already ported) | **sdk** (done) | Already exists | none | — |
| GCS storage backend | `infrastructure/storage/gcs/` | **integration** (`integrations/filestores/gcs`, backlog) | Rule 1, explicitly named in the phase file's classification-rule example | cloud.google.com/go/storage, google.golang.org/api | M |
| S3 (+ S3-compatible: DO Spaces, MinIO) storage backend | `infrastructure/storage/s3/` | **integration** (`integrations/filestores/s3`, backlog) | Rule 1, explicitly named in the phase file's classification-rule example | github.com/aws/aws-sdk-go-v2* | M |
| Email port + SMTP/console backends | (new repo's `sdk/email` + `SMTP`/`Console` — already ported) | **sdk** (done) | Already exists | none | — |
| SendGrid email backend | `infrastructure/communications/emailer/sendgridemailer/` | **integration** (`integrations/email/sendgrid`, backlog) | Rule 1, explicitly named in the phase file's classification-rule example | github.com/sendgrid/sendgrid-go | S |
| bcrypt password hashing | `infrastructure/cryptids/bcrypt/` | **integration** (see Authentication section — `integrations/cryptids/bcrypt`) | Rule 1; listed here for completeness, decision recorded once | golang.org/x/crypto/bcrypt | S |
| AES-GCM encryption (`Encrypter`, for OAuth provider-token storage) | `infrastructure/cryptids/aesgcm/` | **sdk** (stdlib-only — `crypto/aes`/`crypto/cipher`/`crypto/rand` — candidate default, v2 alongside `TokenEncrypter`) | Genuinely stdlib-only per the original's own implementation; a legitimate sdk default the day `TokenEncrypter` (v2) is built | none | S |
| JWT signing (HMAC-SHA256) | `infrastructure/cryptids/golangjwt/` | **integration** (see Authentication section — `integrations/cryptids/golangjwt`, v2) | Rule 1; decision recorded once | github.com/golang-jwt/jwt/v5 | S |
| SHA-256 fast hashing + ID generation helpers | `infrastructure/cryptids/{sha256hasher,id}.go` | **sdk** (`sdk/id` already covers ID generation via `crypto/rand`; SHA-256 fast-hash helper is a thin stdlib wrapper, low-priority backlog if a feature needs non-bcrypt token hashing, e.g. verification codes) | Stdlib-only (`crypto/sha256`), narrow, directly reusable by auth's verification-code hashing | none | S |
| OAuth provider ports + Google/GitHub adapters | `infrastructure/oauth/{oauth,pkce,googleoauth,githuboauth}.go` | port: **feature** (`features/auth` v2, declared by its consumer, same reasoning as `PasswordHasher`) — adapters: **integration** (`integrations/oauth/google`, `integrations/oauth/github`) | Rule 1 for adapters (`github.com/coreos/go-oidc/v3` for Google's OIDC verification; GitHub is plain OAuth2); rule 3 for the port — only auth needs OAuth today | github.com/coreos/go-oidc/v3 (Google only) | M |

## Datastores

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Turso/libSQL driver + query plumbing | (new repo's `integrations/datastores/turso` — already built) | **integration** (done) | Already exists, proven by `features/cms/stores/turso` | github.com/tursodatabase/libsql-client-go, modernc.org/sqlite | — |
| Postgres/pgx driver + connection pool + error-code mapping | `infrastructure/database/postgres/pgxdb/` | **integration** (`integrations/datastores/postgres`, backlog) | Rule 1, explicitly named in the phase file's classification-rule example. First real consumer would be `features/auth/stores/postgres` or `features/cms/stores/postgres`, per D2/D4's scaffold-and-own model | github.com/jackc/pgx/v5 | M |
| Pure-Go SQLite driver + wrapper (`modernc.org/sqlite`, CGO-free) | `infrastructure/database/sqlite/moderncdb/` | **drop** | Superseded in practice: `integrations/datastores/turso`'s own go.mod already vendors `modernc.org/sqlite` for local/embedded-replica mode, so a Turso-free pure-local-SQLite integration duplicates capability the ecosystem already has a path to. Revisit only if a host genuinely needs SQLite with zero Turso involvement | — | — |
| Redis connection wrapper (used by cache/ratelimiter/events backends) | `infrastructure/database/kvstore/goredisdb/` | **drop — folds into each Redis integration** | Not a standalone capability: each `integrations/{caches,ratelimiters,events}/redis` module vendors `github.com/redis/go-redis/v9` directly (rule 2 — one dependency, its own module — doesn't require a shared wrapper module) | — | — |
| Generic CRUD engine: `Spec[T,F,C,U]` + `Store` + `Dialect` (postgres/sqlite) + `Querier` adapters | `infrastructure/database/crud/{spec,store,dialect,dialect_postgres,dialect_sqlite,pgxq,sqliteq}.go` | **workshop v2** | Rule 4 — explicitly the "engine worth salvaging for codegen" per the phase file's own framing. Generic (works for any entity given a `Spec`, no per-entity code needed at the `Store` level) but its only current consumer in the original is a hand-written "golden reference" (`core/repositories/auth/users/usersstore/store.go`) demonstrating what a *future* generator would emit — it is not yet load-bearing production code even in the original. `05-workshop-v2-brief.md` §2 already names it as carrying over by reference | workshop v2 codegen | L |

## sdk-shaped utilities (stdlib-expressible, framework-generic)

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Errors (`ErrNotFound`, `ErrAlreadyExists`, ...) | `sdk/errs/errs.go` | **sdk** (already ported as `sdk/errs` — done) | Already exists | none | — |
| HTTP framework (`WebHandler`, middleware, request/response helpers, SSE) | `sdk/web/*.go` | **sdk** (already ported as `sdk/web` — done, minus OpenAPI, see below) | Already exists | sdk/errs | — |
| `.env` + struct-tag env loading | `sdk/environment/*.go` | **sdk** (already ported as `sdk/config` — done) | Already exists, same concern under a different name | none | — |
| Async goroutine pool (bounded concurrency, drop-on-full, panic recovery) | `sdk/async/pool.go` | **sdk** (new `sdk/async`, backlog) | Stdlib-only (`sync`, `sync/atomic`), framework-generic, no per-feature specifics | none | S |
| Conversion helpers (case conversion, flexible date parsing, pointer/slice generics, URL slugging) | `sdk/conversion/{cases,datetime,json,ptr,slices,urls}.go` | **sdk — split by sub-concern, YOUR CALL on scope** | Not one capability: `ToURLSlug` overlaps the new repo's existing `sdk/slug` (already ported — likely redundant, don't re-add); `Ptr`/`Deref`/`Overlap` generics are genuinely tiny stdlib utility worth a small `sdk/conversion` or folding into call sites as needed; `ToPascalCase`/`ParseFlexibleDate` are speculative ("might need it" — fails admission criterion 1) until a real consumer appears. **Recommended default: do not port wholesale; add narrow pieces only when a feature actually needs them** | none | S |
| Filter/order/pagination encoding (cursor codec, `Order`, `Pagination`) | `sdk/fop/{pagination,cursor,order}.go` | **sdk — largely already covered, YOUR CALL on the gap** | The new repo's `sdk/repository` already owns `Page`/`ListRequest`/a cursor codec — the same vocabulary. **Recommended default:** treat `sdk/fop` as prior art already folded into `sdk/repository`; no new module needed. The one original piece not yet mirrored is the *authorization-aware* overfetch loop (`bridge/transit/fop.PostfilterLoop`), which depends on an authorization capability that doesn't exist yet (see Authorization row) — park it with that decision, not sdk | sdk/repository (existing) | S |
| Validation (hand-rolled, not a 3rd-party wrapper: `Required`, `Email`, `UUID`, `PasswordStrength`, ...) | `sdk/validation/{validate,errors}.go` | **sdk** (new `sdk/validation`, backlog) | Stdlib-only (`net/mail`, `net/url`, `regexp`, `unicode`), framework-generic, genuinely needed by every feature's input handling (cms and auth both need it today — passes plurality) | none | M |
| Background job-worker framework | `sdk/workers/*.go` | **sdk** (see Jobs & events section — same row, listed once) | — | none | — |
| Generic type-safe JSON HTTP client | `infrastructure/httpc/*.go` | **sdk** (new, backlog) | Stdlib-only (`net/http`), genuinely reusable across future integrations (OAuth code exchange, webhooks) — plurality is foreseen the moment `integrations/oauth/*` gets built | none | S |
| OpenAPI 3.1 generation from route specs (runtime reflection over `RouteSpec`, not generation-time) | `sdk/web/{openapi,spec}.go` | **sdk — YOUR CALL** (contested row named in the seed inventory: "OpenAPI (sdk/web vs workshop)") | This is a **runtime** reflection-based builder (`BuildOpenAPISpec` walks `RouteSpec` values a route table already has), not a code generator — it produces a JSON document at request time, same shape as any other `sdk/web` response helper. **Recommended default: `sdk/web`, not workshop v2** — the generation-time question (TS *client* generation *from* an OpenAPI doc) is a separate, genuinely workshop-v2 concern (see next row) | none | M |
| TypeScript client generation (from route/schema info) | `workshop/clients/typescript/*.gen.ts`, `workshop/codegen/generators/tsclient.go` | **workshop v2** | Rule 4 — generation-time only, explicitly named in the seed inventory and `05-workshop-v2-brief.md` §4's open questions | workshop v2 codegen, OpenAPI builder above (as its input) | M |

## Bridge transit middleware (the remaining pieces not covered above)

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Request-id/logger/panic-recovery middleware | `bridge/transit/httpmid/{logger,panics}.go` | **sdk** (already ported into `sdk/web` per its README description — done) | Already exists | none | — |
| Max-body-size limiter, client-info extraction, trust-proxy IP resolution, idempotency-key dedupe | `bridge/transit/httpmid/{body_limit,client_info,trust_proxies,unique_to_id}.go` | **sdk** (`sdk/web` middleware additions, backlog) | Stdlib-only, generic across any route in any feature — same reasoning as the existing logger/panics middleware already in `sdk/web` | none | S |
| Authenticate/Authorize context middleware (JWT/session → context, permission checks) | `bridge/transit/httpmid/{authenticate,authorize}.go` | **feature** (`features/auth` — this *is* `RequireUser`, see design doc §3) | Feature-owned by definition — it's literally identity/session logic | features/auth core | (folded into auth row above) |

## Workshop / codegen tooling

| capability | where in original | target home | rationale | depends on | size |
|---|---|---|---|---|---|
| Codegen engine (`queries.sql` `-- @` annotation parsing, `bridge.yml` parsing, repository/pgx-store/spec-store/bridge/authschema/test-fixture/TS-client generators, reflected-schema consumption) | `workshop/codegen/{generators,database,schema}/**` | **workshop v2** | Rule 4, explicitly named in the seed inventory and the phase-5 brief's carry-over list | postgres/sqlite drivers, `go/ast`/`go/parser`/`go/format`, `gopkg.in/yaml.v3`, `github.com/pganalyze/pg_query_go/v6` | L |
| SQL-injection lint (`sqlguard` — AST scan for unsanctioned dynamic SQL concatenation) | `workshop/codegen/sqlguard/sqlguard.go` (also surfaced via `doctor`) | **workshop v2** (folds into the `doctor` row below) | Generation/tooling-time only, no runtime component | go/ast, go/parser | S |
| CLI framework + command dispatch (`doctor`, `db migrate/reflect/status/create`, `boot`/`generate`, `init`, `new adapter/case/deploy`, `version`) | `workshop/gopernicus/{main,cli/cli,commands/**}.go` | **workshop v2** | Rule 4, explicitly named in the seed inventory (`doctor`, `init`/`new` scaffolding, migrations CLI) | none (hand-rolled dispatcher, no cobra/urfave) | M |
| Bootstrap migration SQL content (auth/rebac/tenants/event-outbox/job-queue/job-schedules tables) | `workshop/migrations/primary/{0001..0006}_*.sql` | **feature** (content migrates into each future feature's `stores/<dialect>` canonical migrations — auth's `0001_users.sql` etc., jobs', events' — NOT into workshop v2 itself) | This is migration *content*, not tooling — it belongs with the feature that owns the tables, per D4's scaffold-and-own model. The CLI verbs that run migrations (`db migrate/status/create`) are the workshop v2 capability; the SQL itself travels with each feature as it's built | the respective future feature's `stores/<dialect>` | S |
| Integration-test harness (testcontainers-backed Postgres/Redis, in-process SQLite, pre-wired auth fixtures, HTTP test client, fixture seeding) | `workshop/testing/{testpgx,testredis,testsqlite,testenv,testauth,testhttp,testserver,pgxfixtures,fixtures}/**` | **workshop v2 — YOUR CALL** | Not pure generation, but it's tooling that only makes sense once the entities/features it fixtures for exist — genuinely follows the "codegen follows design" rule even though it's not code*generation* per se. **Recommended default:** treat as workshop v2 scope (a testing-harness generator/library), built alongside whichever feature needs testcontainers-backed integration tests first (likely `features/auth/stores/postgres`) | testcontainers-go, stretchr/testify | M |
| TS client hand-written bootstrap (`client.ts`/`index.ts`, created-once, never overwritten) | `workshop/clients/typescript/{client,index}.ts` | **workshop v2** (folds into TS client generation row above) | Same capability, listed once | — | — |
| Documentation site (Docusaurus) | `workshop/documentation/**` | **drop** | Not framework logic — a docs site for the *old* framework. The new repo's docs live in `ARCHITECTURE.md` + per-module `README.md`s + this plans directory; a Docusaurus site is out of scope until/unless the new repo reaches a public-release maturity that warrants one (ties to D8's deferred module-path rename) | — | — |
| Local dev Docker Compose (Postgres/Redis/Jaeger) | `workshop/dev/local-data-compose.yml` | **drop — regenerate when needed** | A dev-environment convenience file, not a capability; trivial to recreate once `integrations/datastores/postgres` and a Redis integration actually exist. Nothing to migrate today | — | — |

## Summary

- **Total capability rows:** 62 (every seed-inventory item appears at least once;
  several seed items — auth entities, telemetry, jobs, migrations tooling — expand
  into multiple rows because the original's own file layout splits them).
- **By target home:** feature 19 · sdk 20 (11 already-done, 9 backlog) ·
  integration 12 (1 already-done, 11 backlog) · workshop v2 8 · drop 6 · N/A 1
  (metrics — no original capability exists) · a few rows are two-part (port+adapter)
  and counted once under their primary home with the split noted in rationale.
- **YOUR CALL rows (9) — ALL RATIFIED to their recommended defaults by jrazmi,
  2026-07-02:**
  1. **Authorization/ReBAC scope** — feature, but defer building past v1; salvage
     the design, don't build speculatively.
  2. **ReBAC storage** — same call, tied to #1.
  3. **Tenancy placement** — fold into `features/auth` as a v2+ subdomain, not a
     standalone feature (the original never gave it one either).
  4. **Event bus port/home** — sdk shape (mirrors cacher/email), but don't build
     for auth v1 (direct `Config.Mailer` call suffices); build when a second real
     multi-feature consumer (the SSE gateway) needs it.
  5. **Telemetry/OTEL scope** — sdk port + Noop default, `integrations/tracing/otel`
     for the real exporter; ratified target, not built this phase.
  6. **Cron-parsing dependency in the jobs feature** — port + `integrations/
     scheduling/robfig-cron`, keeping `features/jobs`'s own go.mod stdlib+sdk only.
  7. **`sdk/conversion` scope** — port narrow pieces only when a feature needs
     them; don't bulk-port speculative helpers.
  8. **`sdk/fop` gap (authorization-aware overfetch)** — park with the
     authorization decision (#1), not a standalone sdk item.
  9. **Integration-test harness workshop-v2-ness** — treat as workshop v2 scope,
     built alongside the first feature that needs testcontainers-backed tests.

  (Nine calls listed; the phase file named four as "expected" — telemetry,
  tenancy, ReBAC, OpenAPI — the other five surfaced during the walk and are
  flagged with the same rigor.)

  **OpenAPI resolved, not contested**: recommended `sdk/web` (runtime
  reflection-based builder), with TS-client-*generation* staying workshop v2 —
  listed as YOUR CALL in its row per the seed's framing, but the walk found a
  clean, non-arbitrary answer (runtime vs. generation-time is the actual dividing
  line, and OpenAPI-from-routes is runtime).

## W4 — recommended build order for the next milestones

1. **`features/auth` v1** (password + sessions + `RequireUser` middleware, per
   `auth-feature-design.md`) — first, because it is both the feature-contract's
   acid test (cross-feature identity, per constitution rule 6/C2) **and** the
   original's most mature, most-depended-on domain (everything else in the
   Authentication & identity section above assumes it exists).
2. **Its integrations**: `integrations/cryptids/bcrypt` (blocks v1 shipping at
   all — no adapter, no working `PasswordHasher`), then `integrations/datastores/
   postgres` (blocks any non-Turso host from mounting auth in production).
3. **Cross-feature proof**: the auth+cms two-feature host (design doc §4) —
   validated immediately after #1/#2 land, before anything else, because it's
   the cheapest possible check that constitution rule 6 holds under a *second*
   real feature, not just the illustrative C2 example.
4. **`features/jobs`** — next, because `features/events`' outbox poller (below)
   is built on `sdk/workers`, which jobs also needs; sequencing jobs first avoids
   building the worker-pool primitive twice. Needs the cron-parsing YOUR CALL
   (#6 above) resolved first.
5. **`features/events`** (bus port/Mount decision + outbox + SSE gateway) —
   after jobs, because it reuses jobs' worker infrastructure and because nothing
   today has a *second* real multi-feature event consumer yet (the YOUR CALL
   default above is explicit: don't build the bus speculatively; build it when
   this milestone gives it a genuine second consumer).
6. **Telemetry decision execution** (`sdk/tracing` port + `integrations/tracing/
   otel`) — after the domain features exist, because tracing is valuable in
   proportion to how much request-flow there is to observe; building it before
   auth/jobs/events exist would trace almost nothing.
7. **Remaining integrations** (redis cache/ratelimiter/events, GCS/S3, SendGrid,
   OAuth google/github, JWT signer) — as each becomes a real blocker for a real
   host, not speculatively; each is a small, independent module per constitution
   rule 2, so there's no sequencing constraint forcing them earlier.
8. **Workshop v2** — last, per the standing rule restated in the phase file:
   codegen follows design. It has the most salvage value (crud Spec engine,
   `queries.sql` annotations, the bootstrap/generated split) but generating into
   an unproven structure repeats the original's own mistake; auth's build (step
   1) is the structure's second full proof after cms, and jobs/events its third
   and fourth — workshop v2 should generate into a structure that has survived
   at least that much real use.

## Execution log

### 2026-07-02 — phase 4 W1 + W4 executed

Exhaustive walk of `gopernicus-original`'s six top-level directories performed via
three parallel research passes (bridge/; core/+infrastructure/; sdk/+telemetry/+
workshop/), each verifying tree completeness against the actual filesystem before
reporting. 62 capability rows produced, covering every seed-inventory item (several
expand into multiple rows per the original's own file layout) plus items discovered
beyond the seed list (the generic CRUD engine's actual maturity — a hand-written
"golden reference," not yet generator-driven even in the original; the original's
own required-vs-optional Repositories split, which directly informed auth v1's
scope boundary in `auth-feature-design.md`; the httpc generic HTTP client; the
`sdk/conversion`/`sdk/fop` overlaps with the new repo's existing `sdk/slug`/
`sdk/repository`). Nine YOUR CALL rows recorded with recommended defaults (four
matched the phase file's "expected contested" list — telemetry, tenancy, ReBAC,
OpenAPI — OpenAPI resolved cleanly rather than staying open; five more surfaced
during the walk). W4 build order appended above. No code touched.
