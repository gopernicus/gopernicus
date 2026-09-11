# Integration sweep

Status: COMPLETE — 2026-09-11; final workspace verification passed. Owner authorized
the remaining integration audit and concrete fixes after the completed public
pocket structure pass. All twelve findings below are implemented.
Central context: [framework-audit.md](framework-audit.md), task-3.

## Scope and approach

Review all 14 integration modules against their observable SDK/pocket contracts,
concentrating on uncovered behavior rather than repeating earlier work. Include
non-CMS pocket stores where a connector interaction warrants it. CMS feature and
store redesign remain deferred. Prefer straightforward control flow and explicit
host configuration. Keep vendor differences visible; interface satisfaction does
not imply identical ordering, consistency, retries or limits.

For each confirmed finding, record evidence, the intended fix, compatibility
impact and verification before editing source. Implement concrete correctness
fixes and justified simplifications within this authorized sweep. Do not invent
new abstractions, add missing optional features, upgrade dependencies or migrate
schemas without a demonstrated need. Record incomplete capabilities separately.
Breaking behavior/API changes belong in AUDIT-029 and release guidance.

## Review matrix and ownership

| Modules | Existing coverage / focus | Owner | Status |
|---|---|---|---|
| datastores/pgxdb | listing, transactions and limiter previously covered; connection/pool defaults, errors, retry, migration/resource ownership and store interactions | root | complete |
| datastores/turso, datastores/firestore | listing/transactions previously covered; configuration, cancellation, error mapping, ownership, retries and non-CMS store boundary claims | datastore implementer | complete |
| kvstores/goredis | cache, limiter and bus previously covered; shared-client construction, cancellation, ownership, defaults, diagnostics | root | complete |
| scheduling/robfig-cron | parse/schedule contract, time and configuration semantics | adapter implementer | complete |
| cryptids/{bcrypt,golang-jwt,google-uuid} | prior S4 implementation; verify remaining wrapper/configuration/contract gaps | adapter implementer | complete |
| email/sendgrid, filestorage/{gcs,s3}, oauth/{github,google}, tracing/otel | prior S9a–c implementation; focused review of ownership, failure and configuration gaps, retain explicit provider limitations | adapter implementer | complete |
| cross-layer architecture and guards | actual import/module directions, constructor ownership, optional capability claims, docs drift | architecture steward (read only) | complete |

Named project implementer/steward instructions apply. Root owns this plan, shared
documentation, AUDIT/release guidance and final cross-module verification. Agents
record owned findings/reports under /tmp and send evidence before source edits.

## Preconditions and verification

- Branch firestore-authentication, HEAD 6807ed062fa93d71e176ae85d0612e2342dc8694.
  Preserve the extensive pre-existing uncommitted audit work.
- Fresh baseline: /tmp/gopernicus-integration-sweep-baseline and
  /tmp/gopernicus-integration-sweep-baseline.json; branch status snapshot:
  /tmp/gopernicus-integration-sweep-git-status.txt.
- Formatter: /Users/jrazmi/go/bin/goimports. Go commands use
  GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
- Run affected module build/test/vet, meaningful regressions and race checks for
  lifecycle/concurrency changes. Use pinned vendor source/official documentation
  to verify driver semantics. Compile integration/live-tag code where relevant.
- Confirm local tooling and isolated service ownership before live verification.
  Use disposable local databases/Redis where available; never attach ambient
  credentials to external databases or cloud services. No publication,
  deployment, external consumer edits or production mutations are authorized.
- At completion, run actual-tree guards and the 42-module make check against a
  source snapshot if earlier dirty generated files prevent HEAD-based drift
  checks. Verify docs if changed. Record exact live evidence and unverified
  provider behavior, plus final file inventory and remaining decisions.

## Confirmed findings and decisions

### IS-01: PostgreSQL connection lifetime defaults (carried forward)

Confirmed in postgres.go: cfg.poolConfig overwrites parsed lifetimes with zero.
Pinned pgx v5.8.0 defaults MaxConnLifetime to one hour and MaxConnIdleTime to
30 minutes; pool connection retirement compares the configured lifetime at release.
Positive explicit Config fields should override parsed DSN values; zero should
retain DSN/driver defaults. Reject invalid counts/durations before narrowing int
to int32 or starting a health-check ticker; preserve valid explicit DSN behavior.
Verify parsed precedence, rejected bounds and actual connection reuse in a
disposable PostgreSQL cluster. Document driver environment fallback accurately.

### IS-02: Redis command deadlines

Pinned go-redis v9.18.0 replaces I/O context with context.Background unless
ContextTimeoutEnabled is true. Our Open leaves it false while promising a
deadline-bounded construction ping. Enable it on clients Open constructs and
verify against a local server that accepts connections but does not respond.
Normalize returned context failure when appropriate. Borrowed clients remain
caller-owned and unmodified; document the vendor option required for I/O deadline
support, rather than pretending wrappers can override their transport policy.

### IS-03: Turso credential construction and redaction

Confirmed Config.dsn concatenates AuthToken unescaped, and appends it after an
existing URL token, so reserved bytes/old query tokens change authentication.
Pinned libsql also accepts auth_token and jwt; RedactDSN masks only authToken
and silently ignores query parse failures. Build/validate parsed query values,
give an explicit AuthToken precedence, and redact every supported credential
alias with a fail-closed malformed-query result. Verify actual Authorization
header bytes via local HTTP and redaction regressions. Owner: datastore implementer.

### IS-04: Firestore terminal error identity

MapError drops errors.Is(context.DeadlineExceeded) by formatting it with %s.
attemptState.result always prefers the previous failed callback, even if the
vendor's next BeginTransaction or retry cancellation returns a different terminal
failure. Preserve the deadline cause and classify the actual terminal vendor
error; keep exact callback errors when that is what the vendor returned.
Verify callback identity, exhausted contention, subsequent begin failure and
cancellation with pinned-vendor/local transport regressions. No transaction
algorithm or schema change. Owner: datastore implementer.

### IS-05: Cron grammar enforcement

The jobs port and adapter explicitly promise UTC-only evaluation. The pinned
parser nevertheless accepts TZ=/CRON_TZ= and standalone prefixes panic; @every
zero/negative/fractional durations silently become different whole-second work.
Reject unsupported timezone prefixes before delegation and require positive
whole-second @every durations. Preserve standard fields/descriptors and UTC
evaluation. Fix stale docs claiming a host wrapper is necessary: both schedule
interfaces are aliases and the parser directly satisfies the port.
Owner: adapter implementer. Breaking invalid-input handling is recorded in AUDIT.

### IS-06: SQL migration file selection

Both SQL connectors recursively WalkDir but discard directory components, then
read the basename from the root. Nested migrations can therefore fail lookup or
collide with another file. Use flat ReadDir matching the documented merged host
stream, ignore directories, and preserve lexical order and underscore skips.
Export only the documented SQL files. Test nested/same-basename fixtures and
actual migration idempotency/checksum behavior. No SQL or ledger identity changes.
Owner: root (pgxdb), datastore implementer (turso).

### IS-07: Integration dependency guard coverage

All current integration modules comply, but G13 omits peer integrations, UI and
go.mod framework dependencies despite the architecture's SDK/self-only rule.
Extend the guard with each integration's self-module allowance and fixtures for
forbidden peer imports/requirements. Preserve existing vendor companion-module
allowance; no new dependency or production boundary is introduced.

### IS-08: S3 resolved signing region

Open currently succeeds with no region, although its returned store cannot sign
requests/URLs. Validate the resolved AWS configuration after vendor loading,
preserving environment/shared-config fallback and explicit Region precedence.
Verify missing/fallback/explicit-region behavior without contacting AWS.
Owner: adapter implementer.

### IS-09: Jobs Turso silently mutates borrowed connection policy

Both queue constructors issue PRAGMA busy_timeout=5000 on one arbitrary pooled
connection and ignore errors. Remove that hidden mutation: the host owns the
client/connection configuration and explicit store retry remains in force.
Authorization Turso's two constructors have the same hidden mutation; remove
those too. Verify queue/fenced and guarded mutations on local libsql.
Owner: datastore implementer (jobs), root (authorization).

### IS-10: Store construction can hang on unbounded startup probes

Non-CMS SQL constructors use context.Background for table/column queries after
Open has completed. Its ConnectTimeout does not bound these later operations.
Inventory found eight constructors in six source files with 47 executable callers,
all tests. Prepend ctx context.Context to authentication/authorization SQL
Repositories, authorization SQL RelationshipRepository, and events SQL New.
Pass it through every probe; remove unbounded background contexts. Update the
two store scaffold templates, generated conformance callers and docs. Preserve
early schema validation without a duplicate context-free API or fixed host
startup timeout. Verify cancellation on an exhausted real connection pool.
Owner: root. These signatures are breaking and recorded in AUDIT-029.

### IS-11: Events payload probe reuses stale PostgreSQL row metadata

Live verification of IS-01 exposed TestPayloadMigrationPreservesHistoricalBytes:
after applying migration 0002 on a reused connection, a SELECT payload LIMIT 0
probe consults pgx's cached field description from the pre-migration JSON column.
The old connection-churn bug concealed this. Query pg_attribute for the current
column OID instead; keep the result shape stable and bind the qualified relation
name. Pin the existing upgrade regression to one connection, retain exact byte
preservation checks, and rerun events live conformance. No schema change.

### IS-12: PostgreSQL diagnostic redaction misses accepted credential forms

The final redaction cross-check found the pgx twin also only masks URL userinfo.
Pinned pgx accepts password and sslpassword URL parameters and keyword DSNs;
net/url accepts keyword DSNs as relative paths, so the current fallback can echo
their password. Mask both query credentials, fail closed on malformed queries,
fragments and non-PostgreSQL URLs/keyword DSNs, and retain useful valid URL
targets. Verify exact redacted output and absence of every fake secret. No
connection parsing or authentication behavior changes.

## Verification and final handoff

All accepted findings are implemented; no implementation or approval blocker
remains. The architecture steward independently checked constructor forwarding,
the expanded dependency guard, ownership and documentation against the final
sources. No framework dependency, SQL, persisted format or SDK change was needed.
AUDIT-029 is the consumer migration record; RELEASING links it.

### Executed checks

Every Go command used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

- All 14 integration modules passed fresh `go build ./...`,
  `go test -count=1 ./...` and `go vet ./...`. Changed runtime adapters also passed
  `go test -race -count=1 ./...`: pgxdb, Turso, Firestore, Redis, cron and S3.
  Jobs Turso passed those checks as well. PostgreSQL redaction's final regression
  passed under the race detector after IS-12 was added.
- Both new 42-module `make check` runs passed. The final check includes IS-12 on
  `/tmp/gopernicus-integration-sweep-check-final`, with all live-service environment
  variables cleared. It ran module build/test/vet, integration/live tag checks,
  template regeneration, scaffold verification and layering guards.
  A source snapshot avoids unrelated prior dirty generated files failing a
  comparison against HEAD. Source manifest and log:
  `/tmp/gopernicus-integration-sweep-check-final-manifest.json` and
  `/tmp/gopernicus-integration-sweep-check-final.log`.
  Only audit/handoff Markdown changed after this snapshot; all checked code,
  configuration and templates match the final working sources.
- Actual-repository `make guard` passed, covering the Git-dependent checks too;
  final repetition log: `/tmp/gopernicus-integration-sweep-guard-final.log`.
  `go test ./internal/commands -run 'TestIntegrationBoundar|TestScaffoldPocket' -count=1`
  passed in workshop/gopernicus. The constructor migration's tagged compile
  checks passed. Logs: `/tmp/gopernicus-integration-sweep-scaffold.log` and
  `/tmp/gopernicus-integration-sweep-context-compile.log`.
- Documentation `pnpm typecheck` and `pnpm build` passed in
  workshop/documentation. Logs:
  `/tmp/gopernicus-integration-sweep-docs-{typecheck,build}.log`.
- `goimports -l` reported no changes in all 64 Go files changed in this phase;
  `git diff --check` passed. Module manifests, SDK, CMS implementation, SQL and
  generated templ files are unchanged from this phase's baseline.

### Actual datastore and protocol behavior

Disposable local PostgreSQL 17 databases and Redis were created on random
loopback ports and stopped afterward. No ambient service credentials were used.
Commands were `go test -race -count=1 ./...` in each listed module, with the
harness supplying only its own database/address:

| Backend / modules | Result and observed behavior |
|---|---|
| PostgreSQL connector | PASS; default pool reuses one backend connection, configuration rejects invalid bounds, migrations and transaction behavior execute against PostgreSQL. |
| PostgreSQL authentication, authorization and jobs stores | PASS; existing conformance/upgrade/transaction suites execute after the constructor migration and pool correction. |
| PostgreSQL events store | PASS after fixing IS-11; byte-preserving migration works on a reused connection, and a constructor deadline interrupts an exhausted-pool wait. |
| Redis connector | PASS; cache, limiter and bus suites execute against Redis. The separate nonresponding TCP fixture verifies a deadline on established I/O. |
| Local SQLite through pinned libsql, jobs | PASS under `-race`; shared queue/fenced/schedule conformance and host busy-timeout preservation. |
| Local SQLite through pinned libsql, authentication | PASS under `-race -tags=integration`, 271 tests/subtests, zero skips, host busy timeout 5s. |
| Local SQLite through pinned libsql, authorization | PASS under the same flags, 239 tests/subtests, zero skips, host busy timeout 50ms; both constructors preserve it. |
| Local SQLite through pinned libsql, events | PASS under the same flags, 14 tests/subtests, zero skips, host busy timeout 50ms. |

All migrated Turso constructor cancellation checks passed against the actual file
driver. SQLite files were separate and disposable; scratch Go overlays registered
the existing driver and added the extra checks without repository dependency
changes. Authentication's initial 50ms pressure run returned SQLITE_BUSY during
concurrent replacements at BEGIN IMMEDIATE. That adapter does not retry those
transaction starts; configuring a five-second host timeout passed the full suite.
This is a documented contention/configuration limit, not a universal five-second
guarantee. Configure each pooled connection and choose operation deadlines
deliberately. Remote Turso transport/contention is not established by this test.

The initial PostgreSQL events failure and the successful correction remain in
`/tmp/gopernicus-integration-sweep-live.log` and
`/tmp/gopernicus-integration-sweep-live-followup.log`. The first harness stopped
before Redis; the follow-up ran events and Redis successfully. SQLite reports/logs:
`/tmp/gopernicus-integration-sweep-turso-store-verification.md`,
`/tmp/gopernicus-integration-sweep-{authentication,authorization,events}-sqlite.log`,
`/tmp/gopernicus-integration-sweep-authentication-sqlite-initial-50ms.log`, and
`/tmp/gopernicus-integration-sweep-jobs-sqlite.log`.

Firestore's pinned RunTransaction implementation ran over an in-process gRPC
transport: contention followed by denied begin, canceled retry, successful retry,
exhausted contention and exact domain-error identity, including non-comparable
error values. Turso's local HTTP fixture checked exact Authorization bytes for
reserved characters and credential precedence. Before-source overlays reproduced
the original failures. These are vendor/protocol regressions, not Firestore cloud
conformance. Cron tests exercise actual Parse/Next results; S3 tests use isolated
synthetic credentials and inspect real presigned URL scope without AWS requests.

### Deliberate limits and next work

- CMS remains deferred. Authentication Firestore remains a documented partial
  implementation. No missing optional capability was added merely to match an
  interface implemented elsewhere.
- Live Firestore, its emulator/index behavior, remote Turso, GCS/S3 emulator or
  cloud conformance, real OAuth providers, SendGrid delivery and an OTLP collector
  were not exercised this pass. Relevant Firestore integration/live sources
  compile and vet; fake transports do not prove cloud behavior. Prior adapter
  evidence is retained, not relabeled as a fresh live run.
- PostgreSQL suites used their default database/schema configuration, with
  included schema/decoy cases; the entire second custom-schema matrix and
  separately gated non-C collation tests were not rerun.
- Cron retains UTC-only evaluation, whole-second intervals and the pinned
  five-year calendar search horizon. Borrowed Redis clients need their own I/O
  deadline option. SQL ambient transactions require participating repositories
  on the same database; Firestore transaction callbacks may retry.
- Bcrypt cost/password policy remains host-owned. JWT stays HMAC-only with its
  existing skew and host issuer/audience policy. UUID v7 is not distributed causal
  ordering. OAuth account adoption, provider delivery retries, cloud signing/CORS
  and collector operations remain explicit host/provider concerns.
- No dependency upgrade/security audit, external consumer migration, release or
  deployment was performed. The next practical step is consumer adoption using
  AUDIT.md, followed by the live checks for whichever providers that host uses.
  There is no remaining implementation decision blocking this sweep.

Detailed review reports remain at
`/tmp/gopernicus-integration-sweep-datastores.md` and
`/tmp/gopernicus-integration-sweep-adapters.md`. This plan preserves their findings,
verification limits and decisions for future context windows.

### Changed-file inventory

Compared with the fresh phase baseline: 8 added, 82 modified, 0 removed.
Most store test edits only pass the new constructor context. The exact hashed
inventory is `/tmp/gopernicus-integration-sweep-final-files.json`; pre-existing
audit changes outside these paths were preserved.

```text
AUDIT.md
Makefile
RELEASING.md
examples/auth-cms/internal/outbound/domains/documents/postgres_test.go
integrations/cryptids/bcrypt/README.md
integrations/cryptids/bcrypt/bcrypt.go
integrations/datastores/firestore/README.md
integrations/datastores/firestore/errors.go
integrations/datastores/firestore/errors_test.go
integrations/datastores/firestore/transact.go
integrations/datastores/firestore/transact_driver_test.go
integrations/datastores/firestore/transact_internal_test.go
integrations/datastores/pgxdb/README.md
integrations/datastores/pgxdb/migrate.go
integrations/datastores/pgxdb/migrate_export_test.go
integrations/datastores/pgxdb/migrate_test.go
integrations/datastores/pgxdb/pool_config_test.go
integrations/datastores/pgxdb/pool_live_test.go
integrations/datastores/pgxdb/postgres.go
integrations/datastores/pgxdb/redact.go
integrations/datastores/pgxdb/redact_test.go
integrations/datastores/turso/README.md
integrations/datastores/turso/config_test.go
integrations/datastores/turso/migrate.go
integrations/datastores/turso/migrate_export_test.go
integrations/datastores/turso/migrate_internal_test.go
integrations/datastores/turso/redact.go
integrations/datastores/turso/redact_test.go
integrations/datastores/turso/turso.go
integrations/filestorage/s3/README.md
integrations/filestorage/s3/contract_test.go
integrations/filestorage/s3/s3.go
integrations/kvstores/goredis/README.md
integrations/kvstores/goredis/client.go
integrations/kvstores/goredis/client_deadline_test.go
integrations/scheduling/robfig-cron/README.md
integrations/scheduling/robfig-cron/robfigcron.go
integrations/scheduling/robfig-cron/robfigcron_test.go
plans/framework-audit.md
plans/integration-sweep.md
pockets/README.md
pockets/authentication/README.md
pockets/authentication/stores/pgx/README.md
pockets/authentication/stores/pgx/conformance_test.go
pockets/authentication/stores/pgx/invitation_upgrade_test.go
pockets/authentication/stores/pgx/passwordless_lock_order_test.go
pockets/authentication/stores/pgx/postgres.go
pockets/authentication/stores/pgx/schema_decoy_test.go
pockets/authentication/stores/pgx/schema_probe_test.go
pockets/authentication/stores/turso/conformance_integration_test.go
pockets/authentication/stores/turso/invitation_upgrade_test.go
pockets/authentication/stores/turso/metadata_integration_test.go
pockets/authentication/stores/turso/schema_probe_test.go
pockets/authentication/stores/turso/turso.go
pockets/authorization/stores/pgx/README.md
pockets/authorization/stores/pgx/audit_live_test.go
pockets/authorization/stores/pgx/collation_test.go
pockets/authorization/stores/pgx/conformance_test.go
pockets/authorization/stores/pgx/mutations_live_test.go
pockets/authorization/stores/pgx/postgres.go
pockets/authorization/stores/pgx/tuple_upgrade_test.go
pockets/authorization/stores/pgx/upgrade_runbook_test.go
pockets/authorization/stores/turso/README.md
pockets/authorization/stores/turso/audit_live_test.go
pockets/authorization/stores/turso/conformance_test.go
pockets/authorization/stores/turso/mutations_live_test.go
pockets/authorization/stores/turso/tuple_upgrade_test.go
pockets/authorization/stores/turso/turso.go
pockets/events/stores/pgx/README.md
pockets/events/stores/pgx/appender_test.go
pockets/events/stores/pgx/conformance_test.go
pockets/events/stores/pgx/constructor_context_test.go
pockets/events/stores/pgx/decoy_test.go
pockets/events/stores/pgx/payload_migration_test.go
pockets/events/stores/pgx/postgres.go
pockets/events/stores/turso/appender_test.go
pockets/events/stores/turso/conformance_test.go
pockets/events/stores/turso/turso.go
pockets/events/stores/turso/validation_test.go
pockets/jobs/stores/turso/fenced.go
pockets/jobs/stores/turso/queue.go
workshop/documentation/docs/integrations/catalog.md
workshop/gopernicus/internal/commands/integration_boundaries_test.go
workshop/gopernicus/internal/commands/templates/pocket/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/conformance_test.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/postgres.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/conformance_test.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/turso.go.tmpl
```
