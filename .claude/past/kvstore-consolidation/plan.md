# kvstore-consolidation — goredis multi-port module + pgx connector rename

Status: RATIFIED in-discussion (jrazmi, 2026-07-06) — both rulings verbatim:
(1) "/integrations/kvstores/goredis … then cacher, ratelimiter, bus can be
files under that rather than separate packages … the ports are now defined
in sdk so we don't need separate adapter packages like the old gopernicus
model"; (2) "rename postgres to pgx to match the third party provider rather
than generic postgres. if we wanted to support sqlx later on we'd call it
datastores/sqlx".

## Rulings this plan encodes

- **R-KV1 (multi-port integration modules).** An integration module may
  implement several sdk facility ports when ONE client library serves them —
  the module unit is the library, not the port. Category naming: capability
  category by default (`oauth/`, `scheduling/`, `cryptids/`); tech-family
  category (`kvstores/`) when the library is genuinely multi-port.
- **R-KV2 (connector naming).** Connector modules are named for the
  third-party library they wrap (`pgx`, `goredis`, `robfig-cron`), never the
  generic protocol; a future sqlx connector is `datastores/sqlx`.
- **R-KV3 (stores are named for the package they're built on — CORRECTED by
  jrazmi 2026-07-06).** A feature's logic/ports are driver-independent; its
  store MODULE is a concrete implementation written against one package's
  custom API (pgx's batching/CopyFrom/pgconn errors; sqlx's; turso's) — and
  that API is why the package was chosen. Store modules are therefore named
  for that package: `features/*/stores/pgx` (was `stores/postgres`),
  `stores/turso` (already correct). A future sqlx-based store would be a NEW
  module `stores/sqlx`, not a reuse of stores/pgx. The earlier "dialect"
  framing in this plan's first draft was wrong and is superseded; docs that
  say "per-dialect" shift to "per-store-implementation" where touched.
- Facility adapters on pg/turso (NOTIFY bus, durable limiter) are RATIFIED
  DEFERRED — no consumer yet; placement ruling only (they'd be files in the
  datastores connectors or kvstores siblings, decided when built).
- Boundary note: a redis session store for auth would implement a
  feature-owned port → `features/auth/stores/redis`, never kvstores/goredis
  (facility ports only).

## Non-goals

- No pg/turso facility adapters (above). No sqlx connector (name reserved).
- `datastores/turso` keeps its name — Turso IS the provider name (wraps
  libsql-client-go); flagged as an open question below, default keep.
- No behavior changes to the moved Bus/Broadcaster code.

## task-1: integrations/kvstores/goredis (move + Cacher + Limiter)

Move module `integrations/events/redis-streams` → `integrations/kvstores/goredis`
(module path `gopernicus/integrations/kvstores/goredis`, package `goredis`;
existing Bus/Broadcaster files carried over with package clause + doc
updates; delete the old directory). ADD two files implementing sdk ports
against the same caller-supplied `*redis.Client`:
- `cacher.go` — `goredis.Cacher` implementing `cacher.Storer` (salvage
  old `infrastructure/cache/rediscache/store.go`; byte values,
  Get/GetMany/Set/Delete/DeletePattern/Close semantics per the sdk port docs;
  no OTel — tracing port integration is deferred).
- `limiter.go` — `goredis.Limiter` implementing `ratelimiter.Limiter`
  (salvage old `infrastructure/ratelimiter/goredislimiter/limiter.go` sliding
  window + its compliance tests).
Conformance: env-gated on `REDIS_TEST_ADDR` (loud skip) running ALL THREE sdk
suites — eventstest (existing), cachertest, ratelimitertest — plus the
existing hermetic unit tests. README rewritten: one library/three ports,
delivery guarantees per path (unchanged), R-KV1 rationale paragraph.
go.work + Makefile MODULES updated (path swap; count stays 21).
Verify: module build/test/vet + gofmt clean; full `make check`; optional live
docker leg for all three suites.

## task-1b: goredis connection + instrumentation (gap flagged by jrazmi)

The facilities take a caller-supplied `*redis.Client` — correct — but the
module owned no way to BUILD one (the old goredisdb's construction half
vanished with the indirection). Add, matching the datastores connectors'
shape and salvaging old `infrastructure/database/kvstore/goredisdb`:

- `client.go` — `Config` struct (addr/password/db/pool sizes/dial-read-write
  timeouts, `env:` tags per repo convention) + `Open(ctx, cfg, opts...)
  (*redis.Client, error)`: constructs the go-redis client with sane defaults
  and a fail-fast PING (documented, like pgx's construction-time check).
  Returns the raw `*redis.Client` — no wrapper; facilities and callers use it
  directly; bring-your-own-client stays fully supported.
- `hooks.go` — instrumentation via go-redis's own Hook API (named for the
  package's API per R-KV2 spirit): `LoggingHook(log *slog.Logger)` (errors +
  slow commands) and `TracingHook(tracing.Tracer)` spanning each command
  against the sdk port (stdlib — otel stays the deferred integration);
  `Open` options `WithLogging`/`WithTracing` install them.
- Tests: hermetic (config defaults, option wiring, hook behavior against a
  recorded conn if feasible); Open's live path joins the env-gated
  conformance leg.

Sequenced AFTER task-2 completes (go.work/Makefile stability during the
directory moves); no new dependencies.

## task-2: datastores/postgres → datastores/pgx AND stores/postgres → stores/pgx

Two coupled renames, one task (the same ~20 files are touched by both):

(a) Connector: rename dir + module path
(`gopernicus/integrations/datastores/pgx`) + package (`postgres` → `pgx`).
Internal collision: only the connector's own files import
`github.com/jackc/pgx/v5` — alias jackc inside them; consumers never
co-import jackc (verified).

(b) Store modules (per corrected R-KV3): `features/{auth,cms,jobs}/stores/postgres`
→ `features/{auth,cms,jobs}/stores/pgx` — dir moves, module paths, package
clauses (`postgres` → `pgx`), connector import path + qualifier updates
inside. A store package named `pgx` importing the connector package `pgx` is
legal (local import shadows own package name); hosts wiring both alias at the
composition root exactly as the turso pair already does. No in-repo host
imports the postgres stores (verified — all examples are turso/in-memory).

**HARD CONSTRAINT: MigrationSource `Name` strings must NOT change** — the
migration ledger is keyed (source, version); renaming a source string would
orphan applied rows on every existing database. Names are identifiers, not
paths; verify each store's registered source name is byte-identical before
and after.

go.work + Makefile updated (4 path swaps total; count stays 21).
Verify: full `make check`; grep gates: `grep -rn 'datastores/postgres\|stores/postgres'`
returns nothing in Go files, go.mod/go.work files, or live docs (plan-history
mentions exempt); `make test-stores` optional-if-creds.

## task-3: docs + rulings (Fable, main session)

ARCHITECTURE.md (module tree rows, integrations bullet + taxonomy examples,
R-KV1/R-KV2/R-KV3 sentences where the connector rules live), README.md module
list, RELEASING.md enumeration, events-feature-design status amendment
(redis-streams name → kvstores/goredis), NOTES.md ruling entry, execution-log.

## task-4: final verifier gate

Fresh full `make check` (21 modules); grep gates for both old paths
(`events/redis-streams`, `datastores/postgres`) across Go + docs; go.work ↔
Makefile agreement; race-run goredis package hermetically.

## Sequencing

task-1 → task-2 (both edit go.work/Makefile — no parallel) → task-3 → task-4.

## Open question (non-blocking, default chosen)

- `datastores/turso` vs `datastores/libsql` under R-KV2: turso wraps
  libsql-client-go, but "Turso" is the provider name and the module also
  carries the vendor's live service assumptions. DEFAULT: keep `turso`.
  jrazmi may overrule cheaply pre-tag.

## Execution status (2026-07-06)

- task-1 DONE: kvstores/goredis live; Cacher (GET/MGET/SET/SCAN, key-prefix
  option) + Limiter (atomic Lua sliding window, EVALSHA+reload); all three
  conformance suites env-gated; live docker leg race-clean; old module gone,
  grep-clean; error prefixes redisstreams:→goredis:; package doc in doc.go.
- task-2 DONE: 4 directory/module renames; connector package pgx (jackc
  aliased `jackpgx` internally); store connector alias postgresdb→pgxdb;
  migration sources proven byte-identical ("auth"/"cms"/"jobs" — matching
  turso siblings, ledger-portable); Makefile MODULES+STORE_MODULES+
  test-stores updated; make check green; grep gates clean; test-stores
  skipped (no creds).
- task-1b DONE: client.go (Config env-tagged REDIS_*, Open fail-fast PING,
  WithLogging/WithTracing) + hooks.go (LoggingHook slog w/ slow threshold,
  TracingHook vs sdk/tracing, redis.Nil treated as miss not error); live
  docker leg through Open passed; open flag: no StatusCheck parity with pgx
  (surface kept surgical).
- task-3 docs: mechanical sweep DONE by implementer (README/RELEASING/
  feature+store+connector+example READMEs/events-design amendment);
  judgment pass DONE by main session (ARCHITECTURE tree/taxonomy/litmus/
  R-KV1 rule/naming conventions; features/README charter vocabulary —
  "dialect" retained only where it means SQL dialect; NOTES.md entry).
- task-4 final gate: PASSED 2026-07-06 — fresh make check (21 modules),
  all grep gates zero (code + build files + live docs), go.work↔Makefile
  agree, STORE_MODULES = six stores/{pgx,turso}, goredis race-clean with
  loud hermetic skips, old directories confirmed absent. MILESTONE CLOSED.
