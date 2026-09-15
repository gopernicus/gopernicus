# Segovia: adopt and benchmark the released TupleCache

Implement the released gopernicus TupleCache and Through batching changes in
Segovia, then benchmark them on our representative dataset. Do the implementation
and measurements, not just a design proposal.

## Read first and preserve the baseline

Follow this repository's AGENTS.md and upstream-boundary rules. Inspect the active
branch, dirty changes, module/workspace setup, auth composition, existing migrations,
worker lifecycle, Redis client and benchmark tooling. Preserve unrelated changes.
Write an implementation/benchmark plan in the repo's established location.
Framework defects belong upstream in gopernicus; do not fork/cache-copy framework
code into Segovia or silently work around correctness issues.

Before changing dependencies, capture a repeatable baseline against a disposable
copy of the representative data. Record the exact module versions, dataset size,
backend/routing, configuration and test commands. Previously observed reference
numbers were 738 SQL queries for a 366-space lookup (9 enumeration +729 verification),
210 for a 50-space page, and per-resource checking for 128 dashboards. Reproduce
what is actually present; do not treat these historical numbers as a new measurement.

## Released dependencies

Pin these exact versions where used, with normal Go checksum verification:

- github.com/gopernicus/gopernicus/pockets/authorization v0.15.0
- github.com/gopernicus/gopernicus/pockets/authorization/stores/turso v0.9.0
- github.com/gopernicus/gopernicus/pockets/authorization/stores/goredis v0.1.0
- github.com/gopernicus/gopernicus/integrations/datastores/turso v0.6.0
- PostgreSQL hosts only: authorization/stores/pgx v0.10.0 with the existing
  integrations/datastores/pgxdb v0.8.1 connector.

Use the released module sources and README/API documentation. Verify the versions
also resolve with GOWORK=off and no local replacements before final acceptance.
Do not add PostgreSQL dependencies to a Turso-only host. The relevant upstream
files are authorization/README.md, stores/{turso,pgx,goredis}/README.md, AUDIT-037,
and plans/authorization-{tuple-cache,through-batching,tuple-cache-release}.md.

## Required behavior and wiring

The configured STORE (Turso or PostgreSQL) is the source of truth. Redis is a
complete reconstructible mirror of raw relationship sets. No DecisionCache,
expanded-membership cache, global invalidation or tenant-wide invalidation.

Replace WithCacher/ReadCache/CacheSource/WithCacheReads and obsolete generation
configuration with the released APIs. The SQL bundle uses its WithTupleCache()
option and exposes TupleSource. Construct stores/goredis.NewTupleCache with the
host-owned *redis.Client and one shared namespace per source delivery stream, then root
WithTupleCache(backend, tuplecache.Policy{MaxStaleness: ...}). Reuse the existing
host-selected freshness bound unless a change is explicitly agreed; every process
sharing the mirror must use the same bound. Keep requests authoritative until the
runtime's first successful Poll; constructors start no goroutines.

Apply host-owned base authorization migrations through 0007, then the separate
**authorization-cache** source through 0002, in the same database/schema. Preserve
published 0001 and the migration ledger's source/version identities. Stop/drain old
cache-enabled binaries before 0002; it replaces their schema. Use a fresh dedicated
Redis namespace. Do not run production migrations or deploy as part of this task.
On SQLite, ordinary INSERT/UPDATE/DELETE and framework reconciliation are captured;
raw INSERT OR REPLACE requires recursive_triggers=ON on every writer connection.
Use authoritative source routing, not an unverified lagging replica.

Supervise Components.TupleCache.Poll with the host's worker lifecycle, using
PollInterval() and WakeChannel(). Call Notify() only after the outermost tuple-writing
transaction commits; notifications are coalesced hints, while the transactional
outbox is the durable job queue. Keep periodic polling for external writers, lost
hints and retries. Successful delivery updates affected Redis sets and removes
processed events. Redis errors do not roll back an already committed SQL mutation.
Stop/join workers and drain requests before closing borrowed clients.

Route cache-eligible Check/CheckBatch/CheckExplain/FilterAuthorized calls through
the decision service. Direct relationship/role services and mutation guards remain
durable; role reads cause whole-operation durable fallback. SQL lookups/enumeration
stay SQL-backed and benefit from Through batching; do not claim Redis serves them.
Keep the existing evaluation/search limits and permission semantics intact.

## Benchmarks and correctness proof

Compare three configurations on the SAME fixtures and request distribution:
1. Current Segovia baseline.
2. Released framework with TupleCache disabled (isolates Through batching).
3. Released framework with TupleCache enabled: initial/rebuilding, warm, and busy.

Measure the 366-space lookup, paged/home path with 50 spaces, filtering 128 dashboards,
individual checks and batched checks. Cover users with different access breadth,
nested groups, mixed resource types, allow/deny results and hierarchy depth.
Run the actual app/routes as well as a repeatable low-level harness.

Report sample counts, warmup, duration, concurrency, write rate, throughput and
p50/p95/p99 latency; SQL query counts, Redis commands/bytes, allocations where
practical, cache hits/fallbacks, publications/rebuilds, outbox backlog and delivery
lag. Use AUTH_DB_LOG_QUERIES=true for separately captured query-count runs; run
latency measurements with verbose logging disabled. Separate cache-independent
SQL enumeration from checks and relay SQL from request SQL.

Use genuinely overlapping readers and sustained writers, not only sequential
write→Poll→check loops. Include dashboard/timeline/space creates, direct grants,
group-member changes/revocations and containment moves, both related and unrelated
to checked resources. Increase load in bounded steps; include the 1000-user scale
case if local capacity allows, otherwise state the actual ceiling. Check whether
unrelated writes preserve useful hits, whether concurrent publications cause
fallbacks, and whether relay throughput catches up. Report regressions honestly.

Prove transaction rollback publishes no change; grants and revocations take effect
after delivery; pending delivery obeys the chosen staleness bound; missing/wrong
models, cycles and limits never gain authority. Kill/restart the relay, interrupt
Redis, lose a wakeup, replay work, and rebuild an empty/older restored Redis mirror
after processed outbox rows are gone. Use disposable namespaces/fixtures for these
experiments. Verify permissions against authoritative reads after recovery.

Measure large sets and full rebuild time. The current backend stores one hash on
one Redis shard and decodes whole sets before charging the graph-state budget:
limits do not cap bytes/memory. Upstream's synthetic 10,000-grant sample fetched
~490 KB and allocated ~6.74 MB while correctly refusing at a 100-state limit. A full
build exceeding MaxStaleness remains unavailable. These are workload acceptance
questions, not reasons to hide a failing benchmark or relax correctness silently.

## Finish

Run the repo's Go formatter, build/test/vet and relevant race tests; inspect the
frontend lockfile/scripts before any frontend checks if UI code changes. Exercise
/home and the affected user flows in the running app. Leave a concise report with
exact pins, migrations/wiring, before/after tables, repeatable commands, durable
versus Redis work, remaining bottlenecks and unverified cases. Prepare a reviewable
commit/PR following repo conventions; do not deploy or publish externally. Keep
DecisionCache out of scope.
