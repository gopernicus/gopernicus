# Authorization verification and benchmarks

## What the suite proves

SQL remains authoritative. TupleCache is an optional, bounded-staleness mirror
of raw facts; it does not cache permission decisions. The suite exercises both
paths and recovery between them.

| Area | Retained coverage |
| --- | --- |
| Coherent decisions | Deterministic rewrites must never combine incompatible states across exact global/scoped predicates or graph traversal; Check, Explain, Batch, Filter and All/Any use one tuple snapshot |
| Composable route guards | Model-free exact roles, mixed relationship/permission leaves, whole-tree mount validation, lazy per-request targets, shared budgets, concurrent requests, resolver errors and input reuse across whole-operation fallback |
| Canonical authority | Owner and member coexist; both facades see the same fact; exact full-key deduplication, bulk reads, scope deletion, cross-facade guardian enforcement and one audit delta |
| Snapshot lifecycle | Validation before reads, unknown/empty requests, cancellation, callback/completion failures, escaped-reader refusal, ambient commit/rollback ownership, unsuitable PostgreSQL READ COMMITTED rejection |
| Redis protocol 2 | Real Redis processes; seven-component scope-aware identities, global usersets, protocol/binding fences, receipt conflicts, expiry, loss/restart, malformed data, paused-server deadlines, byte boundaries and atomic publication |
| Runtime and recovery | Capacity fallback after revocation, ignored callback read errors, explicit rebuild, failed acknowledgement, conflicting publishers, shared delivery gate, freshness expiry and poll diagnostics |
| SQL delivery sources | Real triggers, exact captured event IDs, reset detection, current-fact validation, ID-only full snapshots despite obsolete malformed event payloads |
| SQL → Redis | Actual SQLite and PostgreSQL sources with actual Redis; cold/warm batches and filters, bounded revocation lag, delivery, lost mirror, failed acknowledgement, over-capacity backlog and explicit rebuild |
| Existing contracts | Model validation, guarded writes, audit, adapter conformance, fresh schema installation, lookup behavior and architecture guards remain covered by the repository suites |

The cross-store suite lives in `stores/goredis/end_to_end_test.go` under the
`integration` tag. Its SQL dependencies are test-only imports, although Go still
includes them in the Redis adapter's module dependency graph. Ordinary adapter
runtime code does not import SQL drivers.

## Run verification

Run from the repository root unless a subshell changes directories. Go is pinned
by the workspace. Redis tests start private Unix-socket servers and require
`redis-server` on PATH or at `/opt/homebrew/bin/redis-server`. They explicitly
skip if it is unavailable; a green test command with that skip is not Redis
verification. SQLite tests create disposable local files.

The owned-fixture runner creates and verifies its own PostgreSQL 17 cluster,
SQLite files and Redis instances. It snapshots source, discards inherited
datastore settings, requires named behavioral proofs and rejects skipped SQL or
Redis checks. It includes both PostgreSQL schema variants and the CMS HTTP suite:

```sh
python3 -B .github/scripts/authorization_cache_verify.py --mode all \
  --postgres-bin /path/to/postgresql17/bin --report /tmp/authorization-verification.json
```

Its separate `--mode benchmark` requires all 109 current workload cases and five
completed samples per case. The JSON report records commands, output, source
digest and cleanup. Remote Turso remains a separate explicitly gated run.

PostgreSQL commands require `POSTGRES_TEST_DSN` pointing to a **disposable test
database**. The existing adapter conformance suite resets its tables. The new
SQL-to-Redis cases instead create and clean up unique schemas. Do not use an
application database for the adapter suite.

```sh
make check
(cd pockets/authorization && go test -race -count=1 ./...)
(cd pockets/authorization/stores/turso && go test -race -count=1 ./...)
(cd pockets/authorization/stores/goredis && go test -race -count=1 ./...)

# With POSTGRES_TEST_DSN exported:
(cd pockets/authorization/stores/pgx && go test -race -count=1 ./...)
(cd pockets/authorization/stores/pgx && POSTGRES_TEST_SCHEMA=auth_bench go test -race -count=1 ./...)
(cd pockets/authorization/stores/goredis && go test -tags=integration -race -count=1 ./...)
```

Without `POSTGRES_TEST_DSN`, the last command still exercises SQLite → Redis and
explicitly skips PostgreSQL. `make check` builds, tests and vets every workspace
module, compiles/vets tagged integration sources, and checks architecture and
generated-artifact drift. It does not run these tagged cross-store tests.

## Run benchmarks

The SQL/Redis benchmarks use real local fixtures, exclude setup from timings,
verify results, and report allocations. Core benchmarks use in-memory readers;
the denied-candidate cases include constructing an evaluator each iteration. PostgreSQL requires the disposable DSN above.
Use longer runs and repeated samples for decisions about capacity or deployment.

```sh
(cd pockets/authorization && go test -run '^$' -bench 'Benchmark(TupleCachePublicationOverlap|CanonicalRoleReads)$' -benchmem -benchtime=1s -count=5 ./logic/tuplecache)
(cd pockets/authorization && go test -run '^$' -bench 'Benchmark(RoleBatchReads|FilterAuthorizedDeniedCandidates|CheckBatchDeniedCandidates|CheckBatchThrough)$' -benchmem -benchtime=1s -count=5 ./logic/decisions)
(cd pockets/authorization && go test -run '^$' -bench '^BenchmarkComposableGuard$' -benchmem -benchtime=1s -count=5 ./inbound/http)
(cd pockets/authorization/stores/goredis && go test -run '^$' -bench '^BenchmarkTupleCache$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/turso && go test -run '^$' -bench 'Benchmark(ThroughBatchSQLite|TupleSourceBacklog|GuardedWriterContentionSQLite)$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/pgx && go test -run '^$' -bench 'Benchmark(DecisionsPostgres|TupleSourceBacklog|GuardedWriterContentionPostgres)$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/goredis && go test -tags=integration -run '^$' -bench '^BenchmarkSQLRedis(Decisions|RoleReads)$' -benchmem -benchtime=1s -count=5 ./...)
```

| Benchmark | Workload |
| --- | --- |
| CanonicalRoleReads | Reference memory backend; one coherent bulk exact read of 1, 16 or 128 mixed global/scoped facts; durable, cold and warm |
| RoleBatchReads | Unified decisions evaluator; 1, 20 or 128 permission requests with explicitly declared scoped/global alternatives |
| ComposableGuard | Mounted `Adapter.Require` requests: global role, two scoped roles, and mixed role/relationship/permission; includes memory snapshots and HTTP recorder costs |
| TupleCachePublicationOverlap | Reference memory backend; deterministic idle renewal or unrelated publication during a read; hit/fallback/publication counts |
| TupleCache | Real Redis; 100–100,000 documents for one principal; accepted/rejected reverse-set reads, hot-set deltas and complete builds |
| ThroughBatchSQLite | Direct/Through checks and 366-resource batch/filter; sequential reader versus batched SQL reader, including SQL query counts |
| DecisionsPostgres | Direct/Through checks and 366-resource batch/filter against PostgreSQL |
| TupleSourceBacklog | 0, 2,000 or 20,000 pending trigger events but one current tuple; delta decoding versus full snapshot with event IDs |
| GuardedWriterContention… | Actual alternating grant/revoke with a guard; one versus eight concurrent workers targeting different tenant IDs |
| SQLRedisDecisions | Same 128-document userset/Through graph; SQL-only, cold cache and warm cache; single check, batch and filter; hits/fallbacks per operation |
| SQLRedisRoleReads | Model-free All of 1, 16 or 128 exact global/scoped leaves, SQL-only/cold/warm; one coherent snapshot and checked allow result |

The overlap benchmark is a deterministic behavior probe, not a production
contention model. Writer benchmarks retain global SQL serialization; distinct
tenant IDs do not remove that lock. `cold` never polls the cache, so it measures
durable fallback overhead. `warm` uses a one-hour freshness window to keep expiry
out of the read measurement; that is a fixture setting, not a recommended policy.

## Current measurements — 2026-09-16

These samples use the final single-middleware API and fresh canonical SQL schema,
including PostgreSQL SMALLINT scope kinds. All 109 workloads completed five
one-second samples with `-count=5 -benchtime=1s -benchmem` (545 samples). The
runner rejected missing cases and incomplete samples; fixture cleanup passed.
Raw samples, commands and the tested source digest are retained in
[the benchmark record](../../plans/authorization-one-middleware-benchmarks.json).

Apple M4 Pro, darwin/arm64, Go 1.26.1, default GOMAXPROCS 14, Redis 8.4.0 on
private Unix sockets, PostgreSQL 17.4 (`en_US.UTF-8`) over loopback TCP, and
local SQLite through modernc.org/sqlite v1.52.0. Benchmark families ran
sequentially after repository verification completed. These are local repeated
measurements, not an isolated production capacity study or tail-latency estimate.
No controlled before/after comparison with earlier recordings is implied.

Warm cache fixtures use a one-hour freshness window to exclude expiry from
measurement. That is a fixture setting, not a recommended host policy. Cold
fixtures deliberately remain unready and measure durable fallback. Setup and
model compilation are outside timing; every workload verifies its result.

### Same graph, three read paths

Each batch/filter covers 128 resource IDs. Values are median microseconds per
operation, with equivalent grants checked in SQL-only, cold and warm modes.

| Source | Operation | SQL-only µs | Cold µs | Warm µs |
| --- | --- | ---: | ---: | ---: |
| sqlite | check | 383.5 | 382.0 | 117.9 |
| sqlite | batch128 | 1,514.9 | 1,497.9 | 945.9 |
| sqlite | filter128 | 1,519.6 | 1,530.6 | 947.4 |
| postgres | check | 173.2 | 174.6 | 118.7 |
| postgres | batch128 | 935.8 | 946.1 | 948.0 |
| postgres | filter128 | 954.4 | 955.8 | 953.5 |

### Model-free exact role composition

Each operation evaluates `All(...)` over alternating global and scoped exact
role leaves in one coherent snapshot. Values are median microseconds.

| Source | Exact role leaves | SQL-only µs | Cold µs | Warm µs |
| --- | ---: | ---: | ---: | ---: |
| sqlite | 1 | 21.8 | 22.0 | 55.3 |
| sqlite | 16 | 211.4 | 215.2 | 368.9 |
| sqlite | 128 | 1,644.2 | 1,660.2 | 2,751.4 |
| postgres | 1 | 109.0 | 105.5 | 55.8 |
| postgres | 16 | 606.4 | 598.4 | 372.8 |
| postgres | 128 | 4,284.7 | 4,270.8 | 2,750.5 |

### Mounted HTTP policies

`BenchmarkComposableGuard` measures the single `Adapter.Require` request path,
including its memory snapshot and HTTP response recorder. Policy construction
and model compilation occur before timing.

| Case | Median µs | Min–max µs | Median B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `ComposableGuard/global` | 0.818 | 0.815–0.898 | 3,320 | 28 |
| `ComposableGuard/two_exact_roles` | 1.417 | 1.410–1.423 | 4,616 | 34 |
| `ComposableGuard/mixed` | 2.396 | 2.368–2.518 | 7,864 | 44 |

### Interpretation and remaining costs

Caching remains workload-dependent: compare the SQL-only and warm columns for
the host's graph shape and network distance. A mirror-wide receipt means an
unrelated publication can force a complete read operation back to durable SQL.

At raised limits, the 100,000-fact Redis reverse-set fixture took a median
99.4 ms and allocated 84.1 MB in Go per read.
A one-fact delta took 448.4 ms because publication rewrites the large set;
a full rebuild took 282.3 ms and allocated 327.2 MB. Go figures exclude Redis
server memory. Exact role probes can encounter the same set-size limits when a
role has many assignees. Keep configured bounds deliberate.

Writer benchmarks exercise actual guarded writes with one or eight workers.
PostgreSQL still serializes authorization writes per schema; different tenant
IDs do not remove that lock. SQLite also serializes writers. These fixtures
measure the current design rather than promise linear write scaling.

Full source rebuilds read current facts plus pending event IDs. Backlog timings
exclude Redis publication and acknowledgement. Graph batching can reduce work
for large requests while adding overhead to small ones; retained optimized
readers should be evaluated by workload before any further consolidation.

### Complete sample matrix

All times below are microseconds per operation. Ranges cover all five samples;
allocation columns are medians. Redis accepted-set cases raise limits to 16 MiB;
rejected cases deliberately use smaller limits. Backlog workloads with 1,000 or
10,000 transient tuples contain 2,000 or 20,000 events.

<details>
<summary>Core decisions, cache and HTTP</summary>

| Case | Median µs | Min–max µs | Median B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `ComposableGuard/global` | 0.818 | 0.815–0.898 | 3,320 | 28 |
| `ComposableGuard/two_exact_roles` | 1.417 | 1.410–1.423 | 4,616 | 34 |
| `ComposableGuard/mixed` | 2.396 | 2.368–2.518 | 7,864 | 44 |
| `RoleBatchReads/requests_1` | 1.040 | 1.034–1.176 | 3,560 | 20 |
| `RoleBatchReads/requests_20` | 42.853 | 42.202–43.028 | 67,913 | 403 |
| `RoleBatchReads/requests_128` | 206.490 | 204.946–222.425 | 403,057 | 2,261 |
| `CheckBatchThrough/container-direct` | 10,623.345 | 10,301.722–10,831.570 | 26,243,200 | 11,650 |
| `CheckBatchThrough/container-through` | 10,641.970 | 10,469.897–11,042.021 | 26,243,090 | 11,650 |
| `FilterAuthorizedDeniedCandidates/n=50` | 393.020 | 390.252–396.701 | 575,061 | 2,248 |
| `FilterAuthorizedDeniedCandidates/n=300` | 2,133.405 | 2,109.460–2,307.476 | 3,205,770 | 10,647 |
| `CheckBatchDeniedCandidates/n=50` | 411.876 | 398.678–427.644 | 569,924 | 2,246 |
| `CheckBatchDeniedCandidates/n=300` | 2,133.388 | 2,123.241–2,224.205 | 3,176,353 | 10,645 |
| `TupleCachePublicationOverlap/warm` | 1.433 | 1.420–1.531 | 3,312 | 24 |
| `TupleCachePublicationOverlap/idle_poll_during_read` | 2.173 | 2.150–2.300 | 4,904 | 32 |
| `TupleCachePublicationOverlap/unrelated_publication_during_read` | 3.127 | 3.114–3.140 | 7,128 | 49 |
| `TupleCachePublicationOverlap/capacity_fallback` | 1.191 | 1.170–1.259 | 3,384 | 27 |
| `CanonicalRoleReads/roles=1/durable` | 0.231 | 0.230–0.234 | 1,201 | 6 |
| `CanonicalRoleReads/roles=1/cold` | 0.239 | 0.234–0.255 | 1,201 | 6 |
| `CanonicalRoleReads/roles=1/warm` | 0.855 | 0.851–0.937 | 2,225 | 16 |
| `CanonicalRoleReads/roles=16/durable` | 1.516 | 1.502–1.529 | 4,328 | 8 |
| `CanonicalRoleReads/roles=16/cold` | 1.522 | 1.501–1.610 | 4,328 | 8 |
| `CanonicalRoleReads/roles=16/warm` | 10.081 | 10.073–10.242 | 27,136 | 75 |
| `CanonicalRoleReads/roles=128/durable` | 11.176 | 11.076–12.052 | 33,112 | 8 |
| `CanonicalRoleReads/roles=128/cold` | 11.165 | 11.021–11.791 | 33,112 | 8 |
| `CanonicalRoleReads/roles=128/warm` | 84.515 | 83.526–85.007 | 239,920 | 426 |

</details>

<details>
<summary>PostgreSQL graph, backlog and writers</summary>

| Case | Median µs | Min–max µs | Median B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `DecisionsPostgres/direct` | 135.074 | 132.338–148.650 | 14,666 | 113 |
| `DecisionsPostgres/through` | 175.194 | 172.756–195.705 | 21,919 | 177 |
| `DecisionsPostgres/batch` | 1,729.589 | 1,713.442–1,772.454 | 935,783 | 10,874 |
| `DecisionsPostgres/filter` | 1,721.434 | 1,684.464–1,959.445 | 974,663 | 10,876 |
| `GuardedWriterContentionPostgres/tenants=1` | 205.675 | 190.365–213.336 | 14,080 | 190 |
| `GuardedWriterContentionPostgres/tenants=8` | 176.279 | 172.282–178.889 | 14,074 | 190 |
| `TupleSourceBacklog/transient-tuples=0/full=false` | 126.264 | 124.799–140.601 | 2,745 | 53 |
| `TupleSourceBacklog/transient-tuples=0/full=true` | 121.640 | 110.864–127.248 | 3,585 | 64 |
| `TupleSourceBacklog/transient-tuples=1000/full=false` | 3,231.469 | 3,226.292–3,293.449 | 1,924,051 | 50,060 |
| `TupleSourceBacklog/transient-tuples=1000/full=true` | 452.265 | 446.827–485.870 | 299,821 | 6,071 |
| `TupleSourceBacklog/transient-tuples=10000/full=false` | 30,243.606 | 30,031.599–31,262.702 | 20,700,337 | 500,069 |
| `TupleSourceBacklog/transient-tuples=10000/full=true` | 3,410.510 | 3,374.164–3,446.238 | 4,329,399 | 60,080 |

</details>

<details>
<summary>SQLite graph, backlog and writers</summary>

| Case | Median µs | Min–max µs | Median B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `ThroughBatchSQLite/sequential/direct` | 338.553 | 337.315–341.152 | 39,153 | 669 |
| `ThroughBatchSQLite/sequential/through` | 798.826 | 794.251–813.725 | 87,334 | 1,498 |
| `ThroughBatchSQLite/sequential/batch` | 46,169.846 | 45,675.347–47,595.348 | 6,103,923 | 88,067 |
| `ThroughBatchSQLite/sequential/filter` | 45,865.408 | 45,742.993–46,333.783 | 6,142,214 | 88,068 |
| `ThroughBatchSQLite/batched/direct` | 1,021.365 | 1,014.219–1,044.632 | 8,308 | 107 |
| `ThroughBatchSQLite/batched/through` | 2,141.714 | 2,138.384–2,143.991 | 20,079 | 291 |
| `ThroughBatchSQLite/batched/batch` | 13,729.727 | 13,662.496–14,346.148 | 1,981,873 | 20,503 |
| `ThroughBatchSQLite/batched/filter` | 13,711.397 | 13,580.395–13,763.666 | 2,020,802 | 20,505 |
| `TupleSourceBacklog/transient-tuples=0/full=false` | 15.773 | 15.624–16.348 | 3,520 | 97 |
| `TupleSourceBacklog/transient-tuples=0/full=true` | 25.320 | 25.033–26.182 | 4,920 | 137 |
| `TupleSourceBacklog/transient-tuples=1000/full=false` | 2,115.547 | 2,103.456–2,123.045 | 1,938,671 | 53,759 |
| `TupleSourceBacklog/transient-tuples=1000/full=true` | 287.021 | 281.227–295.000 | 243,308 | 5,798 |
| `TupleSourceBacklog/transient-tuples=10000/full=false` | 20,845.048 | 20,631.811–21,639.917 | 20,714,969 | 539,768 |
| `TupleSourceBacklog/transient-tuples=10000/full=true` | 2,723.646 | 2,692.087–2,734.011 | 3,755,524 | 59,807 |
| `GuardedWriterContentionSQLite/tenants=1` | 149.066 | 128.791–149.632 | 6,812 | 135 |
| `GuardedWriterContentionSQLite/tenants=8` | 383.396 | 297.767–521.807 | 6,783 | 134 |

</details>

<details>
<summary>Redis and SQL-to-Redis</summary>

| Case | Median µs | Min–max µs | Median B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `SQLRedisDecisions/sqlite/sql/check` | 383.529 | 379.131–389.370 | 11,429 | 157 |
| `SQLRedisDecisions/sqlite/sql/batch128` | 1,514.884 | 1,484.082–1,526.725 | 349,381 | 4,196 |
| `SQLRedisDecisions/sqlite/sql/filter128` | 1,519.647 | 1,503.186–1,520.538 | 362,569 | 4,198 |
| `SQLRedisDecisions/sqlite/cold/check` | 381.997 | 380.669–391.640 | 11,419 | 157 |
| `SQLRedisDecisions/sqlite/cold/batch128` | 1,497.866 | 1,490.309–1,538.379 | 349,371 | 4,196 |
| `SQLRedisDecisions/sqlite/cold/filter128` | 1,530.647 | 1,488.889–1,540.135 | 362,565 | 4,198 |
| `SQLRedisDecisions/sqlite/warm/check` | 117.884 | 117.698–123.490 | 14,905 | 295 |
| `SQLRedisDecisions/sqlite/warm/batch128` | 945.933 | 933.171–964.712 | 688,905 | 7,896 |
| `SQLRedisDecisions/sqlite/warm/filter128` | 947.356 | 926.650–997.228 | 702,100 | 7,898 |
| `SQLRedisDecisions/postgres/sql/check` | 173.217 | 172.036–179.215 | 21,661 | 177 |
| `SQLRedisDecisions/postgres/sql/batch128` | 935.847 | 930.030–948.193 | 350,364 | 3,954 |
| `SQLRedisDecisions/postgres/sql/filter128` | 954.392 | 943.265–1,037.637 | 363,554 | 3,956 |
| `SQLRedisDecisions/postgres/cold/check` | 174.560 | 172.506–180.973 | 21,661 | 177 |
| `SQLRedisDecisions/postgres/cold/batch128` | 946.089 | 925.283–995.926 | 350,350 | 3,954 |
| `SQLRedisDecisions/postgres/cold/filter128` | 955.779 | 905.854–982.689 | 363,556 | 3,956 |
| `SQLRedisDecisions/postgres/warm/check` | 118.731 | 116.907–118.998 | 14,903 | 295 |
| `SQLRedisDecisions/postgres/warm/batch128` | 948.040 | 935.561–1,037.298 | 688,862 | 7,896 |
| `SQLRedisDecisions/postgres/warm/filter128` | 953.515 | 939.110–1,049.851 | 702,070 | 7,898 |
| `SQLRedisRoleReads/sqlite/roles=1/sql` | 21.811 | 21.451–21.893 | 6,144 | 104 |
| `SQLRedisRoleReads/sqlite/roles=1/cold` | 21.957 | 21.673–22.073 | 6,144 | 104 |
| `SQLRedisRoleReads/sqlite/roles=1/warm` | 55.317 | 54.876–56.144 | 6,871 | 114 |
| `SQLRedisRoleReads/sqlite/roles=16/sql` | 211.391 | 211.126–214.745 | 45,521 | 703 |
| `SQLRedisRoleReads/sqlite/roles=16/cold` | 215.247 | 211.244–216.385 | 45,521 | 703 |
| `SQLRedisRoleReads/sqlite/roles=16/warm` | 368.912 | 367.095–371.948 | 54,558 | 999 |
| `SQLRedisRoleReads/sqlite/roles=128/sql` | 1,644.152 | 1,641.307–1,649.031 | 350,524 | 5,133 |
| `SQLRedisRoleReads/sqlite/roles=128/cold` | 1,660.224 | 1,651.781–1,671.111 | 350,525 | 5,133 |
| `SQLRedisRoleReads/sqlite/roles=128/warm` | 2,751.404 | 2,716.712–2,768.380 | 436,063 | 7,509 |
| `SQLRedisRoleReads/postgres/roles=1/sql` | 108.951 | 106.507–116.782 | 7,232 | 112 |
| `SQLRedisRoleReads/postgres/roles=1/cold` | 105.507 | 104.741–107.479 | 7,227 | 112 |
| `SQLRedisRoleReads/postgres/roles=1/warm` | 55.750 | 55.005–60.737 | 6,870 | 114 |
| `SQLRedisRoleReads/postgres/roles=16/sql` | 606.380 | 591.252–670.787 | 72,996 | 1,131 |
| `SQLRedisRoleReads/postgres/roles=16/cold` | 598.375 | 588.895–608.036 | 72,972 | 1,131 |
| `SQLRedisRoleReads/postgres/roles=16/warm` | 372.771 | 372.015–413.511 | 54,559 | 999 |
| `SQLRedisRoleReads/postgres/roles=128/sql` | 4,284.727 | 4,181.714–4,537.705 | 574,981 | 8,700 |
| `SQLRedisRoleReads/postgres/roles=128/cold` | 4,270.825 | 4,260.652–4,284.623 | 574,808 | 8,699 |
| `SQLRedisRoleReads/postgres/roles=128/warm` | 2,750.525 | 2,697.718–3,005.065 | 436,055 | 7,509 |
| `TupleCache/tuples=100/read_allowed` | 117.654 | 116.600–118.624 | 81,712 | 1,944 |
| `TupleCache/tuples=100/read_rejected` | 16.635 | 16.502–18.016 | 672 | 24 |
| `TupleCache/tuples=100/delta_rejected` | 30.343 | 30.009–32.825 | 2,331 | 54 |
| `TupleCache/tuples=100/delta_allowed` | 414.122 | 409.050–418.225 | 2,348 | 54 |
| `TupleCache/tuples=100/rebuild` | 308.913 | 306.809–328.302 | 300,594 | 2,819 |
| `TupleCache/tuples=1000/read_allowed` | 1,015.099 | 1,009.131–1,020.248 | 863,869 | 19,050 |
| `TupleCache/tuples=1000/read_rejected` | 16.574 | 16.328–18.186 | 672 | 24 |
| `TupleCache/tuples=1000/delta_rejected` | 30.220 | 29.974–33.358 | 2,331 | 54 |
| `TupleCache/tuples=1000/delta_allowed` | 3,861.539 | 3,827.479–3,907.915 | 2,353 | 54 |
| `TupleCache/tuples=1000/rebuild` | 2,724.102 | 2,723.180–2,809.896 | 3,383,990 | 27,213 |
| `TupleCache/tuples=10000/read_allowed` | 9,763.644 | 9,727.729–9,842.742 | 8,493,758 | 190,087 |
| `TupleCache/tuples=10000/read_rejected` | 16.826 | 16.616–18.470 | 672 | 24 |
| `TupleCache/tuples=10000/delta_rejected` | 30.229 | 30.018–33.378 | 2,330 | 54 |
| `TupleCache/tuples=10000/delta_allowed` | 39,142.296 | 38,794.794–39,396.754 | 2,536 | 54 |
| `TupleCache/tuples=10000/rebuild` | 25,236.740 | 24,909.707–27,306.136 | 33,706,728 | 270,857 |
| `TupleCache/tuples=100000/read_allowed` | 99,373.872 | 98,936.670–99,730.361 | 84,114,116 | 1,900,323 |
| `TupleCache/tuples=100000/read_rejected` | 16.757 | 16.639–18.593 | 672 | 24 |
| `TupleCache/tuples=100000/delta_rejected` | 30.239 | 30.004–33.556 | 2,328 | 54 |
| `TupleCache/tuples=100000/delta_allowed` | 448,444.972 | 438,057.708–451,781.708 | 4,277 | 61 |
| `TupleCache/tuples=100000/rebuild` | 282,278.875 | 277,035.417–302,499.792 | 327,242,234 | 2,706,236 |

</details>

## Historical measurements — previous protocol, 2026-09-15

These measurements preceded canonical tuple unification and used the former
relationship-only cache protocol. They are retained as historical evidence;
they do not measure the current implementation.

Apple M4 Pro, darwin/arm64, Go 1.26.1, Redis 8.4.0, PostgreSQL 17.4 over a private
Unix socket, and local SQLite through modernc.org/sqlite v1.52.0. These are short,
single-machine measurements with no network latency or production background
load. They are evidence about these fixtures, not latency or throughput promises.

### Same graph: SQL-only, cold and warm

Median elapsed microseconds per operation from three 100 ms runs. A batch/filter
operation contains 128 resource IDs. Warm cases reported one hit and zero
fallbacks per operation; cold cases reported one fallback and zero hits.

| Source | Operation | SQL-only µs | Cold µs | Warm µs |
| --- | --- | ---: | ---: | ---: |
| SQLite | Check | 312.6 | 323.9 | 112.5 |
| SQLite | Batch 128 | 1,363.1 | 1,352.3 | 761.0 |
| SQLite | Filter 128 | 1,355.9 | 1,391.0 | 856.2 |
| PostgreSQL | Check | 115.1 | 129.8 | 111.3 |
| PostgreSQL | Batch 128 | 651.2 | 667.3 | 752.8 |
| PostgreSQL | Filter 128 | 613.1 | 651.5 | 780.6 |

The warm mirror helped this SQLite fixture. Local PostgreSQL batching was faster
than Redis batching here; Redis adds round trips, JSON decoding and allocations.
For PostgreSQL batch 128, SQL allocated about 346 KB/3,935 allocations versus
531 KB/6,468 for warm Redis. SQLite batch used about 356 KB/4,204 allocations
versus 531 KB/6,468 for warm Redis. Warm SQLite filter samples ranged from 766 to
861 µs, illustrating the variability of short runs.

Keep TupleCache opt-in. Measure database load, network distance, graph shape,
write rate and fallback rate before deciding that it improves a host's workload.

### High-cardinality Redis sets

One 100 ms run per case. Accepted operations explicitly raise limits to 16 MiB;
rejected operations use smaller limits to exercise early rejection. Allocation
figures measure Go allocations, excluding Redis server memory and Lua work.

| 100,000-document hot set | Time/op | Go bytes/op |
| --- | ---: | ---: |
| Accepted read | 51.2 ms | 49.2 MB |
| Rejected read (default 1 MiB cap) | 17.1 µs | 648 B |
| Accepted one-tuple delta | 202 ms | 6.3 KB |
| Rejected delta (configured cap) | 31.6 µs | 2.1 KB |
| Complete build (raised limits) | 238 ms | 184.8 MB |

Rejection cost excludes the subsequent durable authorization operation. This is
bounded failure overhead, not a faster answer to the authorization question.
Default limits are 1 MiB per raw read and 4 MiB per mutation budget. Complete
builds limit individual fields and upload chunks, while total rebuild memory
still scales with graph size. Raising a limit reintroduces the corresponding
decoding/Lua/memory cost.

### Backlog recovery

One three-iteration smoke run per case: 20,000 obsolete insert/delete events and
one current tuple. These timings measure source snapshot acquisition only,
excluding Redis publication and SQL acknowledgement.

| Source | Delta payload decode | Full facts + event IDs | Delta / full Go bytes |
| --- | ---: | ---: | ---: |
| PostgreSQL | 36.4 ms | 4.02 ms | 19.6 / 4.34 MB |
| SQLite | 21.0 ms | 2.87 ms | 20.4 / 3.76 MB |

`Rebuild(ctx)` benefits when history is much larger than current authority. It
still captures every pending event ID for exact acknowledgement and loads every
current tuple; it is not a constant-memory or streaming rebuild.

### Publication coupling

Three 100 ms reference-backend runs showed one hit per read during an idle
freshness renewal and one durable fallback per read when an unrelated publication
was deliberately interleaved. This follows from the shared receipt protecting
the whole mirror. Per-tenant validation would require a different protocol.

The adapter decision and guarded-writer benchmarks were also smoke-run with
`-benchtime=3x`; those short samples verify the harness, not a contention curve.
Use the repeated one-second commands above to characterize a target host.

## Historical verification record

The previous implementation run passed `make check` across 43 modules, core and
local SQLite/Redis race suites, PostgreSQL race suites in default and named
schemas, and the tagged SQLite/PostgreSQL → Redis race suite. Its per-kind
consistency and one-relation-per-subject restrictions have since been removed.
Those results do not certify this revision.

## Current verification boundaries

The canonical suite uses memory, disposable local SQLite, PostgreSQL and Redis.
Remote Turso is not covered by those local runs. Authorization Firestore has
been removed. Ordinary PostgreSQL READ COMMITTED transactions remain suitable
for raw single reads and writes, while compound authorization reads require a
verified REPEATABLE READ or SERIALIZABLE ambient transaction.

One optional mirror serves all raw facts. Cached reads consciously accept the
configured freshness window; guarded writes use authoritative serialized views.
The receipt still covers the whole mirror, and SQL guarded writes remain
serialized. These are architectural tradeoffs, not performance promises.

## Historical HTTP benchmark record

The initial three-shape HTTP measurements preceded middleware consolidation and
are retained in `plans/authorization-composable-guards-verification.json`.
Current mounted HTTP results are included in the 109-case matrix above.
