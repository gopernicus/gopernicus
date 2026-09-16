# One authorization middleware API and refreshed benchmarks

Status: COMPLETED — implemented and verified; uncommitted and unreleased.

## Goal and owner decisions

Use one HTTP policy API: `Adapter.Require(Predicate)` with `All`, `Any`,
`HasRole`, `HasRelationship`, and `Can`, over explicit Global/Fixed/Path/Resource
targets. Remove the older middleware rather than retain compatibility. Segovia
and GPS360 will adapt separately. Preserve the canonical tuple model, snapshots,
fail-closed behavior, scoped identity, guarded mutations and optional caching.

## Scope

Preserve the existing uncommitted work. No release, commit, host application
database mutation, dependency change, graph-port relocation or schema change.
Only remove code made obsolete by the middleware consolidation. Keep the
transport-independent role/decision services and optional role administration.

## Tasks

### M1 — Consolidate HTTP policy evaluation

Remove RequirePermission/On/Fixed, RequireAnyPermission, GateSpec, Gates/NewGates,
Checker and the optional ExpressionEvaluator assertion. Adapter.Require directly
uses a mandatory narrow DecisionService containing ValidateExpression and
EvaluateResolved. HTTP no longer reads model declarations or resolved limits;
the evaluator owns validation, the coherent snapshot and shared budgets.
Remove FixedResource/PathResource resolver builders; retain ResourceResolver for
custom inputs and implement Path through the one Target API. Remove orphaned
model.Declarer if no consumers remain; retain live DeclaresPermission inspection.
Retarget meaningful old HTTP/root tests and delete redundant legacy fixtures.
Preserve mount validation, status/error bodies, principal-before-input handling,
ordered short-circuiting, cancellation, target memoization and whole-operation
cache fallback. Test the narrow custom decision contract and typed nil handling.

### M2 — Update framework consumers and documentation

Convert auth-cms callers and tests to Require(Can(...))/Require(Any(...)). Update
current authorization, authentication, example and workshop docs/comments, and
add an AUDIT entry with a concise old-to-new mapping and changed compound-snapshot
semantics. Historical executed plans and owner handoff files remain records.
Review the final surface with the project architecture/backend reviewers.

### M3 — Verify and refresh measurements

Add existing BenchmarkComposableGuard's three cases to the owned benchmark
runner and its exact inventory test (109 total). Run formatter, full sanitized
make check, documentation checks if affected, owned PostgreSQL/SQLite/Redis/CMS
integration with race, and runner safety tests. Then freeze source and run the
owned benchmark matrix sequentially: five one-second samples per case, including
allocations. Refresh BENCHMARKS.md from actual samples and retain durable evidence
with commands, hashes, suite outcomes, samples and explicit limits. Local results
are not production capacity claims; remote Turso remains unverified.

## Verification safety and ownership

Use the existing owned-fixture runner, which strips external datastore settings,
creates disposable PostgreSQL 17/SQLite/Redis resources and verifies cleanup.
For make check use a strict PATH/HOME/TMPDIR allowlist with GOENV=off,
GOPROXY=off, GOTOOLCHAIN=local and a writable temporary GOCACHE. No inherited
provider credentials or application database endpoints enter verification.

## Execution record

- Starting branch: main; prior authorization implementation is uncommitted.
- Baseline hashes: /tmp/gopernicus-authorization-one-middleware/baseline-hashes.json.
- M1 assigned to implementation; M2/M3 and final integration owned by root.
- M1/M2 implemented. The single Adapter.Require uses the two-method decision
  dependency. Removed old builders/wrappers and both unused Declarer contracts;
  role-route-only HTTP construction remains possible without Decisions.
- Converted auth-cms membership's hand-written OR to one coherent Any policy.
  A new host regression proves a failed admin lookup cannot fall through to a
  successful member lookup. Updated current docs and added AUDIT-044.
- Architecture and backend reviews found no blocking issue; substantive removed
  middleware test cases remain covered through the unified HTTP/core suites.
- Passed core build/test/vet and HTTP/decision race, full 42-module make check,
  documentation typecheck/build, 12 runner safety tests and the updated benchmark
  inventory test. 23 changed Go files are goimports-clean; diff check passes.
- Owned --mode all passed, with cleanup: pgxdb 301, core 1231, PostgreSQL 555 per
  schema, SQLite 542, Redis 52, CMS 528 named test passes. No SQL/Redis skip. Core
  memory TestTransactional intentionally skips; null skip entries are packages
  with no test files. Test source: ead72aa1e31822ee84d56a88f2fe41d2632dd88d79e6933051d88ceaf65a4d9c.
- Benchmarks passed: 109 cases, five one-second samples (545 valid samples),
  sequentially after correctness verification. Cleanup passed. Both owned runs
  used the same source snapshot. HTTP medians: global 0.818 µs, two roles 1.417 µs,
  mixed 2.396 µs, including memory snapshot/HTTP recorder costs.
- Refreshed BENCHMARKS.md with current comparison tables and complete sample
  ranges/allocations. Historical HTTP evidence remains linked. Raw samples and
  commands are retained in plans/authorization-one-middleware-benchmarks.json.
- Final evidence: plans/authorization-one-middleware-verification.json. No Go,
  SQL or module/workspace file changed after verification began. Subsequent
  document edits publish results. Remote Turso and production capacity are
  unverified; no release or external host adoption was performed.
- Temporary full logs: /tmp/gopernicus-authorization-one-middleware/.
