# Authorization verification and benchmarks

## What the suite proves

SQL remains authoritative. TupleCache is an optional, bounded-staleness mirror
of raw facts; it does not cache permission decisions. The suite exercises both
paths and recovery between them.

| Area | Retained coverage |
| --- | --- |
| Durable relationship decisions | Deterministic concurrent rewrites that must never combine incompatible graph states; Check, Explain, Batch and Filter through relationship and decision services; memory, PostgreSQL and SQLite snapshots |
| Snapshot lifecycle | Validation before reads, unknown/empty requests, cancellation, callback/completion failures, ambient commit/rollback ownership, no nested snapshots |
| Redis | Real Redis processes; receipt conflicts, expiry, loss/restart, malformed data, paused-server deadlines, encoded byte boundaries, atomic rejection and publication |
| Runtime and recovery | Capacity fallback after revocation, ignored callback read errors, explicit rebuild, failed acknowledgement, conflicting publishers, shared delivery gate, freshness expiry and poll diagnostics |
| SQL delivery sources | Real triggers, exact captured event IDs, reset detection, current-fact validation, ID-only full snapshots despite obsolete malformed event payloads |
| SQL → Redis | Actual SQLite and PostgreSQL sources with actual Redis; cold/warm batches and filters, bounded revocation lag, delivery, lost mirror, failed acknowledgement, over-capacity backlog and explicit rebuild |
| Existing contracts | Model validation, guarded writes, audit, adapter conformance, upgrades, lookup behavior and architecture guards remain covered by the repository suites |

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

These commands use real local fixtures, exclude setup from timings, verify
results, and report allocations. PostgreSQL requires the disposable DSN above.
Use longer runs and repeated samples for decisions about capacity or deployment.

```sh
(cd pockets/authorization && go test -run '^$' -bench '^BenchmarkTupleCachePublicationOverlap$' -benchmem -benchtime=1s -count=5 ./logic/tuplecache)
(cd pockets/authorization/stores/goredis && go test -run '^$' -bench '^BenchmarkTupleCache$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/turso && go test -run '^$' -bench 'Benchmark(ThroughBatchSQLite|TupleSourceBacklog|GuardedWriterContentionSQLite)$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/pgx && go test -run '^$' -bench 'Benchmark(DecisionsPostgres|TupleSourceBacklog|GuardedWriterContentionPostgres)$' -benchmem -benchtime=1s -count=5 ./...)
(cd pockets/authorization/stores/goredis && go test -tags=integration -run '^$' -bench '^BenchmarkSQLRedisDecisions$' -benchmem -benchtime=1s -count=5 ./...)
```

| Benchmark | Workload |
| --- | --- |
| TupleCachePublicationOverlap | Reference memory backend; deterministic idle renewal or unrelated publication during a read; hit/fallback/publication counts |
| TupleCache | Real Redis; 100–100,000 documents for one principal; accepted/rejected reverse-set reads, hot-set deltas and complete builds |
| ThroughBatchSQLite | Direct/Through checks and 366-resource batch/filter; sequential reader versus batched SQL reader, including SQL query counts |
| DecisionsPostgres | Direct/Through checks and 366-resource batch/filter against PostgreSQL |
| TupleSourceBacklog | 0, 2,000 or 20,000 pending trigger events but one current tuple; delta decoding versus full snapshot with event IDs |
| GuardedWriterContention… | Actual alternating grant/revoke with a guard; one versus eight concurrent workers targeting different tenant IDs |
| SQLRedisDecisions | Same 128-document userset/Through graph; SQL-only, cold cache and warm cache; single check, batch and filter; hits/fallbacks per operation |

The overlap benchmark is a deterministic behavior probe, not a production
contention model. Writer benchmarks retain global SQL serialization; distinct
tenant IDs do not remove that lock. `cold` never polls the cache, so it measures
durable fallback overhead. `warm` uses a one-hour freshness window to keep expiry
out of the read measurement; that is a fixture setting, not a recommended policy.

## Local measurements — 2026-09-15

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

## Verification record and remaining boundaries

The implementation run passed `make check` across all 43 modules, the core and
local SQLite/Redis race suites, PostgreSQL race suites in default and named
schemas (514 passed and one non-C-locale skip each), and the tagged combined
SQLite/PostgreSQL → Redis race suite. Disposable servers were stopped afterward.

Remote Turso, Firestore emulator/live behavior and PostgreSQL's non-C locale
proof were not run. Tagged external-service sources compiled and passed vet.
The relation-exclusivity schema, OR-only policy language, per-kind consistency
of ordinary mixed RBAC/ReBAC batches, single independently maintained mirror per
SQL source, shared receipt and serialized guarded SQL writers remain deliberate
boundaries. None of these benchmarks removes those constraints.
