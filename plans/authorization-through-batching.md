# Authorization: batch Through reads without changing decisions

Status: COMPLETE — 2026-09-15. Owner authorized implementation after discussing
typed relationships, batch checks and host-wide search limits.

## Preconditions and scope

- Starting branch `main`, HEAD `4fb07615`. Preserve the owner's changes in
  `plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`, and untracked
  `plans/segovia-v2-audit-upgrade-handoff.md`.
- Go 1.26.1 workspace; no root go.mod. Formatter: `/Users/jrazmi/go/bin/goimports`.
- No applicable on-disk AGENTS.md or named project agents found. Work stays local;
  no release, publishing, host edits or production data mutations.
- Keep existing tuple/schema APIs, host-wide limits, root-relative budgets,
  canonical branch/target ordering, errors, lookup verification and paging.
- Per-type budgets, a new search-limit contract, SQL model-JSON optimization and
  a general SQL permission compiler are out of scope.

## Design and acceptance

1. Reuse the ordinary recursive Check evaluator. Suspend each batch request at
   needed reads using `iter.Pull`; collect compatible pending reads, execute set
   reads, then resume. Each request retains its own stack, steps and budget.
   Share successful facts only, never decisions or budget accounting. All reads
   remain sequential on the operation's existing model-scoped reader.
2. Use the existing optional `RelationSetReader` for Through targets and direct
   relations; retain sequential compatibility for readers without it. Keep the
   existing direct-only fast path. Mixed batches may group compatible reads
   without changing result order or duplicate handling.
3. Carry set reads through the SQL/Firestore snapshot and decision-cache views,
   preserving scope, snapshot lifetime, cache generation and validation. Cold
   cache misses must not restore per-resource database reads.
4. Prove Check/Batch/Filter/Lookup parity for root-relative limits, cycles,
   convergent paths, polymorphic targets, usersets, short circuits, cancellation
   and failures. Measure actual SQLite queries on 50/128/366-candidate workloads
   and >500-ID adapter chunking, including snapshot/cache operation.

## Tasks

- [x] Implement suspended batch evaluation and optional-reader routing.
- [x] Preserve batching through snapshot/cache readers.
- [x] Add parity, lifetime/error and SQL query-count regressions/benchmarks.
- [x] Update documentation, run formatter and verification, review final diff.

## Verification

Use a writable task-local GOCACHE under `/tmp/gopernicus-through-batching` if the
default cache is blocked. Run targeted tests during implementation, then
`go build ./...`, `go test ./...`, `go vet ./...` in authorization core and each
changed store module. Run race tests for the changed evaluator/cache path and
the repository `make check` gate. Local SQLite fixtures exercise actual SQL;
remote Turso/Postgres/Firestore and Segovia timings are not implied by those
results. Record exact commands, changed files and any unresolved failures below.

## Progress

- Confirmed N+1 fallback in `checkBatchSequential`; existing set methods are
  available on scoped stores but hidden by snapshot/cache CheckReader wrappers.
- Core `go test ./...` passes, including randomized bounded parity and existing
  regressions. Added scoped snapshot set-read isolation/lifetime assertions.
- Local SQLite fixture (real model-scoped SQL, same data and evaluator, hiding
  only the set capability for the before baseline): 366-space lookup 704 -> 7
  permission queries; page of 50 from 366 spaces 200 -> 11; filtering 128
  dashboards 376 -> 4. At 600 candidates adapter chunking stays bounded (9
  lookup / 7 filter queries). Group-member vs group-admin remains distinct.
- `CheckBatch` cache fixture: cold 4 permission queries, warm 0; revocation and
  generation polling remove the grants. This uses SQLite plus an in-memory
  cacher implementing the same port, not a live Redis server. FilterAuthorized
  and lookups intentionally bypass the cross-request cache; behavior preserved.
- Benchmark command (Turso module, task GOCACHE):
  `go test -run '^$' -bench '^BenchmarkThroughBatchSQLite$' -benchtime=3x -count=1`.
  Local 366-dashboard filter: sequential 41.17 ms / 1,066 queries / 6.54 MB;
  batched 10.32 ms / 4 queries / 1.71 MB. Short local sample, no network latency;
  these are not Segovia or remote Turso latency claims.
- Formatter and `git diff --check` pass. Authorization core and the complete
  Turso adapter suite pass `go test -race ./...`.
- The first sandboxed `make check` stopped at an SDK httptest loopback bind
  (`operation not permitted`). Retried with loopback access and datastore test
  credential variables unset; the complete workspace gate passed (42 modules,
  vet/build/test, generated-artifact drift, integration/live-tag compile checks,
  and layering guards).
- Final review added operation-wide cache-miss tracking: when a later pending
  check misses cache before an earlier request returns a budget error, the miss
  must still force the existing whole-operation snapshot retry. Its regression
  and the complete core race suite pass. The final workspace gate and targeted
  Turso race tests also pass after this change (exit 0).
- Live Postgres, remote Turso, Firestore and Redis/Segovia benchmarks are not
  exercised. Datastore test credential variables are absent; fixtures only use
  disposable local SQLite and in-memory cachers. No unresolved product failures.
- Final branch remains `main`; no commits, release or host deployment performed.
  The three pre-existing owner plan/handoff changes remain outside this diff.

## Changed files

- `plans/authorization-through-batching.md`
- `pockets/authorization/README.md`
- `pockets/authorization/logic/relationships/`: `service.go`, `set_reader.go`,
  `batch_reader.go`, new `batch_evaluate.go` and `batch_evaluate_test.go`,
  `batch_reader_test.go`, `evaluate_set_test.go`.
- `pockets/authorization/logic/decisions/`: `composite.go` (comment only),
  `read_cache_keys.go`, `read_cache_readers.go`, `read_cache_operation.go`, new
  `read_cache_sets.go` and `read_cache_sets_test.go`.
- `pockets/authorization/stores/{memory,pgx,turso,firestore}/read_snapshot.go`.
- `pockets/authorization/stores/firestore/relationships.go`.
- `pockets/authorization/stores/storetest/read_cache.go`.
- New `pockets/authorization/stores/turso/batch_queries_test.go`.

## Exact verification commands and artifacts

All Go commands use `GOCACHE=/tmp/gopernicus-through-batching/cache`.

- Authorization core: `go test ./...`, `go test -race ./...`.
- Turso store: `go test -run 'TestThroughBatchSQLite' -v ./...`,
  `go test -race ./...`, and final focused
  `go test -race -run 'TestThroughBatchSQLite|TestCachePublicBehavior' ./...`.
- Snapshot conformance after the optional-capability test adjustment:
  core `go test -race ./stores/memory`; Turso
  `go test -race -run '^TestCacheSnapshots$' ./...`.
- Root: `env -u POSTGRES_TEST_DSN -u POSTGRES_NON_C_TEST_DSN
  -u TURSO_DATABASE_URL -u TURSO_AUTH_TOKEN -u FIRESTORE_EMULATOR_HOST
  GOCACHE=/tmp/gopernicus-through-batching/cache make check`.
- Logs: `/tmp/gopernicus-through-batching/{make-check,core-race,turso-race,turso-final-race}.log`.
- Final adoption check: point Segovia at these framework changes, rerun its
  `AUTH_DB_LOG_QUERIES=true` 366-space lookup, page-of-50 and 128-dashboard
  filtering workloads, and compare returned IDs as well as queries and latency.
