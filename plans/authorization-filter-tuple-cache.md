# Authorization: route candidate filters through TupleCache

Status: COMPLETE — 2026-09-15.

## Preconditions and scope

- Starting branch `main`, HEAD `43112566`. Preserve the owner's edits in
  `plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`, and
  untracked `plans/segovia-v2-audit-upgrade-handoff.md`.
- Multi-module Go workspace, no root go.mod. Formatter:
  `/Users/jrazmi/go/bin/goimports`. No named project agents found.
- Fix `Decisions.FilterAuthorized` bypassing TupleCache for relationship-owned
  permissions and shorten Redis keys to use readable namespaces. The owner
  confirmed only one dev server runs and there is no production cache.
  Implementation was initially local; the owner subsequently requested the
  [release](authorization-filter-tuple-cache-release.md). No host deployment.

## Design and acceptance

1. Keep the existing relationship candidate path when TupleCache is absent.
   With TupleCache configured, route candidate checks through the composite's
   `CheckBatch` orchestration, preserving validation, budgets, order, duplicates,
   whole-operation snapshot fallback and fail-closed results.
2. Add a regression using real local SQLite and the reference cache backend:
   warm relationship filters must increment cache hits and issue zero permission
   SQL queries. Cover Through/userset evaluation, cold and unavailable fallback,
   revocation after publication, empty/invalid/oversized inputs and errors.
3. Update the stale filter comments and clarify the public cached-filter contract.
4. Use `tuplecache:{<readable namespace>}`, accepting ASCII letters, digits and
   `:._-/`. Validate at construction without I/O; preserve namespaces verbatim.
   Colons are safe within a hash tag; braces are rejected. Temporary build hashes
   keep the same tag. The dev server's next restart/poll rebuilds the new hash;
   leave old keys untouched. Document the key shape, accepted characters and
   the multiple-process upgrade case without requiring production work here.

## Tasks

- [x] Reproduce the routing bug with a failing regression.
- [x] Fix routing and document the behavior.
- [x] Shorten the Redis key, validate namespaces and verify on isolated Redis.
- [x] Format, run module build/test/vet and relevant race tests; review the diff.
- [x] Record results, changed files, and remaining deployment verification.

## Verification

Use a writable task GOCACHE at `/tmp/gopernicus-filter-tuple-cache/cache`.
Run `go build ./...`, `go test ./...`, and `go vet ./...` in authorization core,
Turso and go-redis modules; run relevant race tests. Local SQLite fixtures test
real SQL behavior and isolated Redis processes verify keys and publications.
Segovia and remote datastore measurements remain host checks.

## Changed files

- `plans/authorization-filter-tuple-cache.md`
- `pockets/authorization/README.md`
- `pockets/authorization/logic/decisions/composite.go`
- `pockets/authorization/logic/decisions/tuple_cache_test.go`
- `pockets/authorization/stores/turso/batch_queries_test.go`
- `pockets/authorization/stores/goredis/tuple_cache.go`
- `pockets/authorization/stores/goredis/tuple_cache_test.go`
- `pockets/authorization/stores/goredis/README.md`

## Results

- The regression failed before implementation: filtering bypassed even the
  runtime's cold durable snapshot, leaving both Hits and Fallbacks unchanged.
- The local SQLite workload (128 dashboards plus duplicate/missing candidates)
  now reports 4 permission SQL queries / 1 fallback cold, 0 queries / 1 hit warm,
  0 queries / 1 hit after published revocation, and 7 queries / 1 fallback after
  closing the runtime. The revoked durable case explores remaining branches;
  its initial expected count of 4 was corrected to 7.
- Core regressions cover direct cached filtering, input order and duplicates,
  invalid/empty/oversized inputs, final receipt validation failure after a
  provisional grant, revocation in the durable retry, failed snapshot error
  propagation, and ambient reader bypass.
- Real isolated Redis tests verify readable colon/slash namespaces, separation
  of similar namespaces, full publication and temporary-key cleanup, and that
  the prior base64 key is untouched. Namespace construction rejects invalid
  characters without I/O. Opaque tuple reference encoding remains unchanged.
- Passed formatter (`goimports -l` empty), `git diff --check`, core
  `go test -race ./...`, Turso
  `go test -race -run '^TestThroughBatchSQLiteTupleCache$' -count=1 -v ./...`,
  and Redis `go test -race -count=1 ./...`.
- The initial sandboxed workspace check and Redis test could not bind local
  sockets. Retried with local socket access; isolated Redis race tests pass.
  The complete workspace `make check` passed (exit 0): `go build ./...`,
  `go test ./...`, `go vet ./...` across all modules, generated-artifact checks,
  integration/live-tag compile checks, scaffold checks and layering guards.
  Exact root command:
  `env -u POSTGRES_TEST_DSN -u POSTGRES_NON_C_TEST_DSN -u TURSO_DATABASE_URL
  -u TURSO_AUTH_TOKEN -u FIRESTORE_EMULATOR_HOST
  GOCACHE=/tmp/gopernicus-filter-tuple-cache/cache make check`.
- Final review also replaced the obsolete README paragraph saying candidate
  filters bypassed the cache and describing the removed generation cache.
- No unresolved verification failures. Live remote datastore tests were
  credential-gated/skipped; integration/live-tag checks compiled them only.
  Existing owner changes remain intact. Work stays uncommitted on `main`.
- Logs: `/tmp/gopernicus-filter-tuple-cache/{make-check,core-race,turso-race,redis-race}.log`.

## Host adoption

Segovia has one dev server and no production cache. After adopting the changed
authorization core and Redis adapter, restart that server and let its normal
relay poll rebuild `tuplecache:{<AUTH_CACHE_NAMESPACE>}`. No SQL migration or
manual cache conversion is required. Re-run the host's 128-dashboard filter
benchmark and verify returned IDs, SQL counts and TupleCache hit deltas.
Segovia itself, remote Turso, PostgreSQL and Firestore were not exercised here.
