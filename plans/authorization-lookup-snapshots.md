# Authorization: consistent lookup reads under concurrent writes

Status: COMPLETE locally — 2026-09-15. Publication is tracked in the
[release plan](authorization-lookup-snapshots-release.md).

## Preconditions and scope

- Starting `main`, HEAD `3a50cab7`. Preserve owner edits in
  `plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`, and
  untracked `plans/segovia-v2-audit-upgrade-handoff.md`.
- Multi-module Go workspace; formatter `/Users/jrazmi/go/bin/goimports`.
  No applicable named project agents. No host edits or deployment.
- User reports 3–7% HTTP 409 responses during concurrent Segovia lookup reads.
  Inspect and fix the framework lookup semantics; do not introduce host retries.

## Findings

Before this change, `relationships.verifyLookup` returned sdk.ErrConflict when a reverse-enumerated
candidate is denied by forward verification. There is no global revision check.
The two phases currently issue separate durable reads, so relevant concurrent
revocation can trigger the error. Unrelated writes alone should not; test them
separately rather than treating the reported attribution as proven.
Turso and PostgreSQL connectors already expose `BeginRead` (deferred SQLite and
read-only repeatable-read PostgreSQL). Existing TupleCache snapshot readers can
be reused without requiring cache configuration or optional migrations.

## Design and acceptance

1. Add an optional model-scoped lookup snapshot capability. The relationship
   service uses it around the complete enumeration, verification batches and
   page lookahead. Preserve public signatures, model filtering, ordering, cursor
   semantics and per-attempt evaluation budgets. Lookups remain durable.
2. Implement the capability for Turso, PostgreSQL and reference memory readers.
   SQL owns one read transaction for ordinary calls, preserving schema and
   reader scope. Existing ambient/bound transactions retain their view and
   isolation; never commit or roll back a borrowed transaction. Readers close
   on callback return/error/panic and cannot be used after their lifetime.
3. Retry the complete lookup at most twice after a discovered grant fails
   verification, using fresh attempt-local state and a fresh owned snapshot.
   Do not retry arbitrary store conflicts/errors, cancellation or budget errors.
   Exhaustion returns `model.ErrEnumerationContended` wrapping sdk.ErrUnavailable
   (503), with no partial IDs or cursor. Hosts can distinguish it for Retry-After.
   Readers without snapshot capability retain guarded reads with bounded retries.
4. Prove deterministic revocation between discovery and verification, unrelated
   writes, same-snapshot verification across batches/lookahead, model scope,
   ambient transactions, cancellation/errors/lifetime and retry exhaustion.
   Exercise real local SQLite and isolated PostgreSQL where available; add a
   concurrent-reader/writer regression without relying on timing for correctness.
5. Document per-call snapshots (not snapshots across pages), custom-reader
   fallback and the distinct exhausted-contention error.

## Tasks

- [x] Reproduce the current conflict under controlled SQL interleaving.
- [x] Implement snapshot routing and bounded contention retries.
- [x] Add adapter snapshot implementations and regression coverage.
- [x] Format, verify core/adapters and real concurrency, review final changes.

## Verification

Task evidence: `/tmp/gopernicus-lookup-snapshots`.
Use the existing writable `/tmp/gopernicus-filter-tuple-cache/cache` GOCACHE.
Run build/test/vet in core and changed adapters, relevant race tests, then
`make check`. Use isolated local datastore fixtures; no inherited remote test
credentials. Record exact commands, changed files and unresolved failures here.

## Changed files

- `pockets/authorization/README.md`
- `pockets/authorization/logic/relationships/evaluation_regression_test.go`
- `pockets/authorization/logic/relationships/lookup.go`
- `pockets/authorization/logic/relationships/read_model.go`
- `plans/authorization-lookup-snapshots.md`
- `pockets/authorization/logic/model/lookup.go`
- `pockets/authorization/logic/relationships/lookup_operation.go`
- `pockets/authorization/logic/relationships/lookup_operation_test.go`
- `pockets/authorization/stores/memory/lookup_snapshot.go`
- `pockets/authorization/stores/memory/lookup_snapshot_test.go`
- `pockets/authorization/stores/pgx/lookup_snapshot.go`
- `pockets/authorization/stores/pgx/lookup_snapshot_test.go`
- `pockets/authorization/stores/storetest/lookup_snapshot.go`
- `pockets/authorization/stores/storetest/lookup_snapshot_concurrent.go`
- `pockets/authorization/stores/storetest/lookup_snapshot_lifecycle.go`
- `pockets/authorization/stores/storetest/lookup_snapshot_transaction.go`
- `pockets/authorization/stores/turso/lookup_snapshot.go`
- `pockets/authorization/stores/turso/lookup_snapshot_test.go`

## Results

- RED: `go test -run '^TestLookupSnapshots/space/page/revoke_membership$'
  -count=1 ./...` in the Turso adapter failed before implementation with
  `relationships changed during enumeration: conflict`; the test revoked the
  discovered user's group membership before verification. The same regression
  passes with the snapshot path.
- Deterministic SQLite, PostgreSQL and memory tests cover all/page lookups,
  direct usersets and Through reads, related revocation, unrelated relationships
  and unrelated roles, separate verification batches and page lookahead.
- Concurrent tests on each SQL backend completed 1,024 lookups with 16 readers,
  two writers and 128 relevant membership writes: zero errors, no mixed IDs or
  lookahead. These are framework tests, not a rerun of Segovia's HTTP benchmark.
- Core tests cover bounded full-call retries with and without snapshots,
  third-attempt success, exhausted contention classified as unavailable (not
  conflict), immediate store conflict/budget/cancellation/deadline errors, invalid
  snapshot readers and discarding results on snapshot completion failure.
- SQL ambient tests see uncommitted caller writes, leave outside readers isolated,
  and preserve the caller's commit/rollback choice. Shared lifecycle tests cover
  model scoping, cancellation, callback errors/panics and closed reader methods;
  SQLite uses a one-connection fixture to expose leaked transactions.
- `/Users/jrazmi/go/bin/goimports -w` on changed Go files: passed.
- `make check` with datastore credential/env gates unset and
  `GOCACHE=/tmp/gopernicus-filter-tuple-cache/cache`: passed. This includes
  `go build ./...`, `go test ./...`, `go vet ./...` per module, generation checks,
  tagged compilation and repository guards. Log: `make-check.log`.
- Core `go test -race ./...`: passed (`core-race.log`). The final added snapshot
  retry matrix also passed targeted relationship tests/race/vet.
- Turso `go test -race ./...`: passed (`sqlite-race.log`), local SQLite with WAL.
- PostgreSQL `go test -race -count=1 -timeout 10m ./...`: passed (`pg-race.log`).
  `POSTGRES_TEST_DSN=postgres://postgres@/postgres?host=/tmp/gopernicus-lookup-snapshots/pg/socket&port=55493&sslmode=disable`
  points only to this task's disposable PG17 server, initialized under `/tmp`.
  The server was stopped after verification; its data and logs remain for review.
- Unverified: Segovia `/home` HTTP benchmark; remote Turso/sqld; live/emulator
  Firestore (the unchanged adapter uses the tested nonsnapshot retry fallback).
  Hermetic datastore-gated tests still skip those environments explicitly.
- No migration, Redis key or host configuration changes. No new release tags or
  publishing in this task; a release must include core and both SQL adapters so
  consumers obtain snapshot support (upgrading only core gives bounded retries).

Evidence directory: `/tmp/gopernicus-lookup-snapshots` (`results.json`, logs).
No unresolved implementation failures.
Final `git diff --check` and goimports checks passed. The three owner files'
SHA-256 checksums still match the prior release's preservation record.
