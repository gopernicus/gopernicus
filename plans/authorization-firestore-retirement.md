# Retire the authorization Firestore adapter

Status: IMPLEMENTED AND VERIFIED, unreleased. This is the executed copy of the
plan originally held at `.claude/plans/authorization-firestore-retirement.md`.

## Context

The owner has removed Firestore from the authorization redesign. Authorization
will support memory, PostgreSQL and SQLite/Turso authorities, with an optional
Redis tuple mirror. This retirement is independent of the shared Firestore
connector and the authentication pocket's Firestore adapter.

## Goal

Remove the authorization Firestore module and its active build, CI and
documentation references while keeping the remaining workspace green.

## Scope and preconditions

- Branch: `main`. The current uncommitted tuple-value foundation is retained.
- Preserve owner changes in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md` and
  `plans/segovia-v2-audit-upgrade-handoff.md`.
- Delete only `pockets/authorization/stores/firestore/`; it has no owner edits.
- Do not contact Firestore, remove hosted data, publish module retractions, or
  change the shared connector/authentication implementation.
- Historical audit, release and plan records describe historical capabilities;
  retain them. Current supported-store documentation must describe the new set.

## Tasks

### R1 — Remove the adapter and active consumers

- **depends_on:** []
- **files:** `pockets/authorization/stores/firestore/**`, `go.work`,
  `.github/workflows/live-stores.yml`, `.github/scripts/authorization_cache_*`,
  maintained READMEs and workshop documentation.
- **description:** Remove the module, its workspace entry and its CI fixture,
  emulator/live legs. Update current support tables and test-isolation examples.
  Keep remaining Firestore tests and connector code intact.
- **verify:** Scan maintained source/config for imports or active paths to the
  retired module; verify the remaining workspace contains 42 modules.

### R2 — Update root wiring, review fixes and retirement record

- **depends_on:** [R1]
- **files:** `Makefile`, `ARCHITECTURE.md`, `pockets/authorization/README.md`,
  `AUDIT.md`, `RELEASING.md`, `.claude/plans/authorization-unified-tuples.md`.
- **description:** Update module/test lists, number the canonical tuple guard
  G26, keep architecture doctrine free of progress reports, and remove
  Firestore from the tuple redesign. Record the removal and pending unified
  authorization API transition explicitly.
- **verify:** `git diff --check`; `make guard`; inspect the exact removal diff.

### R3 — Verify the remaining workspace

- **depends_on:** [R2]
- **files:** execution record in this plan.
- **description:** Run `make check` with a writable Go build cache and local test
  fixture permissions as needed. Run authorization core race tests. No remote
  datastore migration/conformance run is required for this removal.
- **verify:** `GOCACHE=/tmp/gopernicus-authorization-hardening/cache make check`;
  from `pockets/authorization`, `GOCACHE=/tmp/gopernicus-authorization-hardening/cache
  go test -race ./...`; protected owner-file hash verification.

## Reversibility and release

The source deletion is recoverable from Git. Existing published module versions
remain available at their old tags; they do not implement the forthcoming tuple
contracts. Any release of the remaining modules is a separate coordinated step.
No application Firestore migration or data deletion is performed here.

## Execution record

### Changes

- Removed all 57 tracked files in `pockets/authorization/stores/firestore/`.
- Removed the module from `go.work` and `Makefile`; the workspace now has 42
  modules. Removed its emulator/live jobs, index manifests and artifact entries
  from `.github/workflows/live-stores.yml`.
- Removed Firestore modes and imports from the authorization cache fixture and
  Python helper, and removed their orphaned tests. Retained SQL modes.
- Updated current support documentation in the root and authorization READMEs,
  `ARCHITECTURE.md` and workshop pocket pages. Updated only documentation/comments
  in the shared Firestore connector and its test helper.
- Recorded the removal and canonical tuple foundation in AUDIT-040/AUDIT-041 and
  the unreleased section of `RELEASING.md`. Corrected P1's tuple-leaf guard to G26
  and widened G19 to forbid roles importing tuplecache. Recorded the existing P1
  validation diagnostic change; no new persistence or role-check change here.
- Replanned unified tuples at `.claude/plans/authorization-unified-tuples.md`.
  The next integrated phase is unimplemented; retirement does not claim its
  migrations, protocol, APIs or benchmarks are complete.

### Verification

All commands below passed on 2026-09-15:

```sh
env -u POSTGRES_TEST_DSN -u POSTGRES_NON_C_TEST_DSN \
  -u TURSO_DATABASE_URL -u TURSO_AUTH_TOKEN \
  -u FIRESTORE_EMULATOR_HOST -u FIRESTORE_LIVE_PROJECT_ID \
  -u FIRESTORE_LIVE_CREDENTIALS_B64 -u FIRESTORE_PROJECT_ID \
  GOCACHE=/tmp/gopernicus-authorization-hardening/cache make check
```

This completed build, test and vet across all 42 modules, generated-artifact
checks, tagged-store compilation and architecture guards. Local socket/loopback
permission was granted for test fixtures; remote datastore variables were unset.

From `pockets/authorization`:

```sh
GOCACHE=/tmp/gopernicus-authorization-hardening/cache go test -race ./...
```

The Python script suite passed all 36 tests. Its initial local socket restriction
was resolved before the passing run. The generated Go cache fixture was not
built: it already references retired pre-TupleCache APIs (`WithCacheReads`,
`WithCacher`, `decisions.CacheSource`). Its modernization is separate work;
passing Python helper tests do not establish that fixture's end-to-end behavior.

Additional checks: `git diff --check` passed; `go work edit -json` reports 42
modules; maintained-source/config scans found no active retired-module paths;
SHA-256 checks confirmed all three protected owner files unchanged. Historical
audit, release and executed-plan mentions remain intentionally.

No remote SQL/Turso/Firestore suite, Firestore emulator, production migration,
new benchmark, tag or publication was run. The remaining Firestore connector and
authentication adapter were built/tested locally by `make check`; their hosted
behavior was not exercised. No generated artifact drift was introduced.
