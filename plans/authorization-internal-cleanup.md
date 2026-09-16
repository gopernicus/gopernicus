# Authorization internal cleanup

Status: COMPLETED — implemented and verified, 2026-09-16. Subsequent release
work is tracked in [the release plan](authorization-internal-cleanup-release.md).

## Goal and scope

Implement findings A1–A5 from authorization-internal-structure-audit.md.
Keep roles/relationships as tuple facades and retain model-scoped graph fast
paths, atomic writes, integrity, audit and persisted cursor formats.

Starting main HEAD: 6bad23cc8d9432a42f7125bf98ed695a817d2d6d. Existing owner
changes and the completed audit artifacts are preserved. No on-disk AGENTS.md
or named project agent definitions were found. No deployment, release, version
pin, schema change or downstream app edit is part of this work.

## Design decisions

- Remove four unreferenced private helpers and use tuples.Compare in Plan.
- Retire the obsolete decisions.Service validation trio, its production
  CreateRelationship alias and the never-returned relationship error. Keep
  GetSchema/SchemaDigest and live error identities. Check repository and available
  local adopter references; document this as an unreleased pre-v1 API removal.
- Delegate raw relationship exact reads/counts/listings to the existing core
  facade over canonical tuples. Retain model-filtered GetRelationTargets and
  other graph operations; retain atomic broad deletes and write adapters.
- Preserve raw SQL ambient joining, including READ COMMITTED, by binding only
  the private raw facade's tuple view to the existing transaction. Standalone
  raw lists/counts use canonical snapshot ownership. Decision/root facade
  snapshot requirements remain strict. Document this distinction explicitly.
- Keep the exported aggregate relationships.Storer and existing package locations;
  an interface/package redesign is unnecessary for this cleanup.
- Correct stale local comments and record accepted-input changes in AUDIT.md
  and the authorization upgrade notes. No historical release text is rewritten.

## Tasks

- [x] C1 Confirm API consumers; remove dead code/obsolete API and duplicate order.
- [x] C2 Consolidate equivalent raw relationship reads in memory, PG and Turso;
      remove mapping/query helpers orphaned by that change.
- [x] C3 Add shared parity regressions for selectors/search/cancellation and
      retain transaction/model boundaries; verify against local SQLite and an
      owned disposable PostgreSQL fixture when available.
- [x] C4 Update contracts, migration notes and final execution evidence.
- [x] C5 Goimports, affected module build/test/vet and core/adapters race tests,
      architecture guards and workspace checks appropriate to exported removals.

## Verification constraints

The repository has no root go.mod. Use goimports and module-aware Make targets.
Strip host datastore variables from hermetic checks; live tests only target an
owned disposable fixture. Preserve caller-owned transactions and no model-filter
bypass on graph reads. Do not regenerate unrelated artifacts unnecessarily.

## Execution record

- Removed the four confirmed private helpers, retired decision validation methods,
  production CreateRelationship alias and orphan error. Graph fixtures retain a
  test-only type alias. Repository and local Segovia/GPS Go sources have no
  production consumers of the removed APIs; arbitrary external consumers remain
  outside the compatibility claim.
- Plan now uses tuples.Compare. Raw memory/PG/Turso existence, count and listing
  methods delegate to relationships.Service over the same canonical authority.
  Removed orphan SQL projection structs/query builders and memory tuple-key helper.
  Raw SQL views bind their existing ambient transaction privately; no changes were
  made to canonical snapshot checks or graph filtering.
- Added shared RawParity conformance for exact concrete/userset/global identity,
  paged cursors and counts, empty results, invalid selectors, unsupported search,
  cancellation, and raw inspection on a graph model view. Core build/race/vet
  passes (1260 named tests/subtests; memory ambient family intentionally skips).
- PostgreSQL build, both public/named-schema full race suites, non-C collation
  checks and vet pass (537 named tests/subtests per schema, no skips). The fixture
  is owned by this task, labeled, loopback-only, has no host binds, and uses a
  disposable database. Existing application containers were inspected but not used.
- SQLite's full tagged integration race suite passed on an explicitly matched,
  disposable local-file URL (715 named tests/subtests, no skips). Actual PostgreSQL
  and SQLite to Redis integration races passed (52 named tests/subtests, no skips).
  Adapter build and tagged vet passed. The runner removed its PostgreSQL container
  and temporary SQLite fixture; tests cleaned their owned Redis processes.
- The complete sanitized 42-module make check passed: generated-file drift,
  build/test/vet, tagged compilation/vet and every architecture guard. All 1743
  recorded Go/build inputs stayed unchanged during the workspace gate. Seven
  preexisting owner/audit/cache files retain their saved hashes. No generated,
  dependency, module pin, migration or protocol changes occurred.
- Goimports and git diff --check passed. A final private-declaration reference
  scan found no new unreferenced private function candidates. This is not a
  staticcheck result; the installed binary's Go-version limitation remains as
  recorded in the original audit. No test failure remains unresolved.

## Final verification commands

All checks used GOCACHE=/tmp/gopernicus-go-build. Before checking, the runner
removed POSTGRES_TEST_DSN, POSTGRES_NON_C_TEST_DSN, POSTGRES_TEST_SCHEMA,
TURSO_DATABASE_URL, TURSO_AUTH_TOKEN, AUTHORIZATION_TURSO_DISPOSABLE_URL,
FIRESTORE_EMULATOR_HOST and FIRESTORE_PROJECT_ID. Live values below were supplied
only from task-owned fixtures, never inherited from host configuration.

| Module / gate | Passed commands and scope |
| --- | --- |
| authorization core | go build ./...; go test -race -json -count=1 ./...; go vet ./... |
| stores/pgx | go build ./...; go test -race -json -count=1 ./... in public and authorization_cleanup_named schemas; go vet ./... |
| stores/turso | go build ./...; go test -race -tags=integration -json -count=1 ./...; go vet -tags=integration ./... |
| stores/goredis | go build ./...; go test -race -tags=integration -json -count=1 ./... with both real SQL authorities; go vet -tags=integration ./... |
| workspace | make check, including ordinary go test ./... and all guards across 42 modules |
| formatting | goimports -l for changed/new Go files; git diff --check |

Core's sole named skip is TestTransactional: memory provides no ambient
transaction seam. The real SQL transaction suites passed. The hermetic workspace
gate intentionally skips unrelated database-dependent tests; those skips are not
live-store verification. Remote Turso, production-scale performance, browser
flows, remote CI and arbitrary external-consumer compatibility were not tested.
Existing real HTTP suites passed as part of core/workspace checks.

## Handoff

The change is a focused cleanup with documented exported API removals and stricter
raw read validation. Roles, relationships and the aggregate Storer remain public;
graph fast paths and write boundaries remain in place. There is no remaining
implementation step for this scope. The owner subsequently authorized release; coordinated versions and archive/consumer
verification are tracked in the release plan. No publication or application
migration was performed during the implementation phase.

The exact changed-file inventory and machine-readable verification results are
in [authorization-internal-cleanup-verification.json](authorization-internal-cleanup-verification.json).
Supplemental runner/logs live in /tmp/gopernicus-authorization-internal-cleanup;
this record and the JSON retain the outcome independently of temporary files.
