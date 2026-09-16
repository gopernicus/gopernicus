# Authorization review hardening and performance suite

Status: COMPLETE — 2026-09-15. Relation exclusivity retained; deployment-scale
architecture extensions remain outside this hardening pass.

## Objective

Implement the fixes from `authorization-tuple-cache-deep-review.md`, and retain
reproducible correctness tests and benchmarks in the repository. Keep SQL
authoritative and preserve coherent cache publication and guarded mutations.

## Preconditions and scope

- Branch `main`, starting HEAD `debfddfd1e5d1d4c8812fcb85efc840210359872`.
- Preserve owner edits in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md`, untracked Segovia handoff, and the
  completed review document. No production datastore, deployment or publishing.
- Named implementer/backend/data-review/verifier roles retain their configured
  instructions using the inherited model (configured opus/sonnet unavailable).
- Formatter: `/Users/jrazmi/go/bin/goimports`. Task cache/logs:
  `/tmp/gopernicus-authorization-hardening/`.
- The optional question about removing relation exclusivity received no answer.
  The stated assumption was to preserve persisted semantics and document the
  constraint. No independent-relations migration was made.
- Redis Cluster, independently maintained regional mirrors, distributed
  fine-grained validation, and new policy-language operators are separate
  architectural extensions. Preserve existing safety guarantees; measure and
  document their existing constraints rather than weakening them.

## Tasks

### H1: Consistent durable permission operations — complete

- Reuse existing durable snapshot mechanisms for ordinary relationship Check,
  CheckExplain, CheckBatch and FilterAuthorized, independently of cache setup or
  optional migrations. Preserve ambient transaction ownership and model scoping.
- Ensure errors/cancellation/failed snapshot completion discard provisional
  results; avoid nested snapshots when already evaluating an operation view.
- Add deterministic impossible-grant interleavings and snapshot lifecycle tests
  for supported SQL/memory readers and all affected decision entry points.

### H2: Redis deadlines and physical resource limits — complete

- Validate borrowed clients honor context deadlines; do not mutate clients.
- Bound raw read payloads before fetching/decoding them, and bound affected
  publication payloads before expensive Lua transformations. Over-limit cache
  work must remain unavailable and fall back safely, never truncate authority.
- Add real paused-server, boundary, atomicity and large-set tests; retained
  benchmarks measure reads, allocations, rebuilds and mutations by set size.

### H3: Delivery recovery and observability — complete

- Make an explicit full rebuild available through the same publication gate and
  receipt protocol. Full source snapshots should avoid decoding obsolete pending
  tuple payloads while still acknowledging only captured event identities.
- Expose useful fallback/publication timing and failure evidence without adding
  background work. Cover backlog recovery, concurrent publishers, failed ack,
  expiry, cancellations and model changes in durable/cache parity tests.
- Preserve all-or-nothing SQL transaction visibility; do not split captured
  transactions into independently visible changes for throughput.

### H4: Model decision, documentation and benchmark matrix — complete

- Apply the user's answer about independent relations as a deliberate migration
  and conformance change if selected; otherwise retain and document exclusivity.
- Correct removed API names and stale tuple metadata comments. Explain durable
  consistency, staleness, limits, client configuration and recovery steps.
- Provide repeatable benchmark commands/results for SQL-only, cold/warm cache,
  direct/Through/batch/filter, high-cardinality sets, unrelated publications,
  rebuild/backlog and guarded-writer contention where supported.

### H5: Verification and independent review — complete

- Build/test/vet changed modules; focused and adapter race suites; full make check.
- Actual disposable SQLite, Redis and PostgreSQL (default and named schema).
  Compile external-service suites and report any unrun remote/emulator cases.
- Named read-only review of final diff, safety guarantees and evidence. Fix
  findings; record changed files, exact commands/results and unresolved limits.

## Acceptance

- No ordinary supported SQL relationship decision combines incompatible states.
- Redis deadline failure and oversized cache reads fall back with bounded work.
- Failed or oversized publication cannot expose partial authority or discard
  committed outbox work; explicit rebuild recovers from obsolete event backlog.
- Tests cover real behavior and interleavings, not only mocked call counts.
- Benchmarks are checked-in and runnable with documented fixture prerequisites;
  reported measurements separate synthetic evidence from production guarantees.

## Execution record

### Delivered behavior

- Ordinary relationship decisions use the existing optional `LookupSnapshotter`
  capability. Bundled memory/PostgreSQL/SQLite readers keep each operation
  coherent, discard provisional results on snapshot failure, and preserve
  ambient transaction ownership. Eight public entry points share a retained
  impossible-grant interleaving regression.
- Redis construction requires `ContextTimeoutEnabled` without mutating the
  borrowed client. Default encoded limits are 1 MiB per read and 4 MiB per delta
  budget; rejection precedes raw fetching/decoding or Lua transformation.
  Full builds limit individual fields/chunks. `ErrCapacity` also matches
  `ErrUnavailable`, retaining durable fallback.
- `Rebuild(ctx)` uses the same gate, freshness bound, receipt CAS and exact-ID
  acknowledgement as Poll. SQL full snapshots validate current facts and load
  only pending event IDs, avoiding obsolete tuple-payload decoding. Delta paths
  still validate payloads. Failed publications retain pending work.
- Stats add capacity/conflict counters, last successful poll, timings, captured
  counts and failure stage. A panic cannot record a successful poll and does not
  retain the local delivery gate.
- Retained correctness and benchmark suites include actual SQL → Redis delivery,
  revocation, cache loss, acknowledgement failure, bounded reads and backlog
  recovery. Makefile now vets this integration-tag source as part of `make check`.
- README fixes removed API names and synthetic tuple-ID comments, and documents
  transaction guarantees, exclusivity, deadlines, limits, recovery and staleness.
  `pockets/authorization/BENCHMARKS.md` records commands, evidence and boundaries.

### Changed-file inventory

Paths below are relative to `pockets/authorization/` unless noted.

- Documentation/workflow: `README.md`, `BENCHMARKS.md`, repository `Makefile`,
  and this plan. The preceding deep-review document remains the historical
  review; owner plan/handoff changes were preserved.
- Relationship implementation: `logic/relationships/check_operation.go`,
  `service.go`, `explain.go`, `lookup_operation.go`, `read_model.go`,
  `relationship.go`.
- Snapshot regressions: `logic/relationships/check_operation_test.go`,
  `stores/storetest/check_snapshot.go`, and
  `stores/{memory,pgx,turso}/lookup_snapshot_test.go`.
- Runtime: `logic/tuplecache/runtime.go`, `reader.go`, `tuplecache.go`,
  `runtime_test.go`, `recovery_test.go`, `runtime_benchmark_test.go`.
- Redis: `stores/goredis/tuple_cache.go`, `scripts.go`, `tuple_cache_test.go`,
  `limits_test.go`, `tuple_cache_bench_test.go`, `end_to_end_test.go`,
  `README.md`, `go.mod`, `go.sum`.
- PostgreSQL: `stores/pgx/tuple_source.go`, `tuple_source_test.go`,
  `tuple_source_benchmark_test.go`, `decisions_benchmark_test.go`.
- SQLite/Turso: `stores/turso/tuple_cache.go`, `tuple_cache_test.go`,
  `tuple_source_benchmark_test.go`, `writes_benchmark_test.go`,
  `batch_queries_test.go`.

### Verification commands and evidence

All Go edits used `/Users/jrazmi/go/bin/goimports`. Commands used
`GOCACHE=/tmp/gopernicus-authorization-hardening/cache`; module commands ran
inside the stated module.

| Command / scope | Result |
| --- | --- |
| `make check` at repository root | Passed build, test, vet for all 43 modules; tagged integration/live vet; architecture guards; generated-artifact drift checks. External service variables unset for this gate. |
| `go build ./...`, `go test ./...`, `go vet ./...` in affected modules | Passed through individual runs and the repository gate. |
| `go test -race -count=1 ./...` in authorization core | Passed all core packages. |
| `go test -race -count=1 ./...` in SQLite/Turso and Redis adapters | Passed with real disposable local SQLite/Redis. |
| PostgreSQL `go test -race -count=1 -json ./...` with disposable `POSTGRES_TEST_DSN`, default and named schema | Each passed 514 tests with one explicit non-C-locale skip. PostgreSQL 17.4. |
| Redis module `go test -tags=integration -race -count=1 ./...` with disposable PostgreSQL DSN | Passed combined actual SQLite/PostgreSQL → Redis cases and adapter suite (4.514 s). |
| After final poll panic fix: `go test -race -count=1 ./logic/tuplecache ./logic/decisions` and corresponding `go vet` | Passed. Whole-repository gate preceded this small reviewed change; relevant tests/vet were rerun. |
| Redis-only consumer with `GOWORK=off GOPROXY=off`: `go list -deps`, `go build`, binary `go version -m` | Passed; no SQL drivers in package dependencies or binary. SQL test modules do appear in the module graph. |
| Final `git diff --check` | Passed. |

Logs remain in `/tmp/gopernicus-authorization-hardening/`:
`make-check.log`, `core-all-race.log`, `pgx-source-race.json`,
`pgx-source-race-named.json`, `sql-redis-e2e.log`, and benchmark logs below.
The disposable PostgreSQL fixture was stopped and its data directory removed;
fixture logs remain under `/private/tmp/authorization-sql-review.qfdDH0/`.
SQLite/Redis test fixtures clean themselves up.

Initial sandbox-only socket-binding failures were resolved with automatically
approved local-test escalation. Dependency resolution was likewise approved.
The first integration fixture's 4 KiB cap could not hold its 128-document reverse
set; raising that fixture cap to 16 KiB retained the intended oversized-backlog
failure and made recovery testable. These are resolved setup failures, not
outstanding product failures.

### Benchmarks run

- `BenchmarkTupleCachePublicationOverlap`, `-benchtime=100ms -count=3 -benchmem`:
  `runtime-benchmarks.txt`; confirms idle renewal keeps hits and deliberately
  overlapping unrelated publications force whole-operation fallback.
- `BenchmarkTupleCache`, `-benchtime=100ms -benchmem`: `redis-bench.txt`;
  includes 100–100,000-tuple reads/deltas/full builds and capacity rejection.
- SQL decision, writer and backlog benchmarks, `-benchtime=3x -benchmem`:
  `pgx-benchmarks.txt`, `turso-benchmarks.txt`; smoke measurements, not a
  production contention curve.
- `BenchmarkSQLRedisDecisions`, `-tags=integration -benchtime=100ms -count=3
  -benchmem`: `sql-redis-benchmarks.txt`; SQLite/PostgreSQL × SQL/cold/warm ×
  check/batch/filter. Warm cache improves this SQLite fixture; local PostgreSQL
  batches outperform warm Redis and allocate less. Keep caching opt-in and
  measure representative host workloads.

### Independent review and remaining limits

Named backend review passed with no blockers after the poll panic fix. Named
data integration review passed the snapshot/recovery/e2e work; named verifier
completed the full repository gate. No deployment, publishing, application
datastore mutation, commit or generated-file edits were performed.

Remote Turso, Firestore emulator/live tests and PostgreSQL's non-C locale proof
remain unrun; external tagged sources compiled and passed vet. Relation
exclusivity, OR-only rules, ordinary mixed-kind batches' per-kind consistency,
the shared mirror receipt, one independently maintained mirror per SQL source,
one Redis shard and serialized guarded SQL writers remain unchanged. Physical
limits do not bound total graph/rebuild memory. Follow the benchmark guide's
longer repeated commands with deployment-shaped data before selecting limits
and freshness settings. No unresolved local test failures remain.
