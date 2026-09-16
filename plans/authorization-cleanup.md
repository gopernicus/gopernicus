# Authorization cleanup implementation

Status: COMPLETED — implemented and verified; uncommitted and unreleased.

## Context and goal

Unified tuples and composable guards are implemented but uncommitted and
unreleased. Apply the accepted findings in `plans/authorization-cleanup-audit.md`:
make public raw pagination usable without private imports, close cursor parity
gaps, remove obsolete APIs, and make raw relationship services use the same
canonical authority as roles and decisions.

## Scope and decisions

- Preserve all existing worktree changes and owner plans. No commit, release,
  protocol, dependency, generated-file or SDK changes.
- Keep `internal/tuplekey` for the existing version-2 serialized listing keys.
  Canonical identity is the comparable Tuple value; public raw pagination uses
  `Query.After *Tuple` and `tuples.Compare`, ordered by scope kind, resource type,
  resource ID, relation, subject type, subject ID and subject relation in byte order.
- Remove `Repositories.Relationships`. Construct relationship services and trusted
  writers from required `Repositories.Tuples`, as roles and decisions already do.
  Extend raw Query filtering only as required to preserve relationship listings:
  resource-only facts and optional subject type/ID across concrete/userset facts.
  Exact Subject remains exact; inconsistent filters are invalid. Preserve atomic
  writes by translating directly to ApplyTuples, ReconcileTuples and DeleteScope.
- Retain optimized graph readers and flat expression projections. Moving graph
  ports and converging legacy HTTP middleware are separate behavior/design work.
- Retire LookupSnapshotter/ReadLookupSnapshot; preserve live colocated Lookup
  methods and retarget snapshot lifecycle tests to canonical tuple snapshots.

## Schema direction (owner correction)

The owner explicitly clarified that authorization is being built from scratch.
Replace the old relationship/role migration stream and upgrade-only machinery
with fresh canonical tuple/audit migrations and fresh optional cache migrations.
There is no requirement to preserve or convert a legacy authorization database.
Retain full tuple identity, audit integrity, cache capture/binding, schema probes,
byte ordering and fresh applied-schema tests. Remove legacy copy/preflight and
backup/downgrade scripts/tests. Update current docs; historical executed plans
remain records of the earlier decision. Verification may create owned temporary
databases; no application database is changed by this task.

## Tasks

### C1 — Typed pagination and cursor parity

Add public tuple comparison and typed continuation; update memory, SQL, cache,
decision lookup and tests. Add the narrowly required Query filters consistently
to validation, matching and SQL. Remove tuple-key encoding from raw order/identity
uses, including audit deduplication. Preserve serialized cursor bytes. Reject
wrong cursor fields, unequal order value/PK and obsolete key versions on every
tuple listing implementation; exercise forward and previous-page traversal.

### C2 — Canonical relationship facade

Implement raw relationship reads/list projections/writes over tuples.Storer;
remove independent root wiring and update constructors, callers and docs. Test
role/relationship/decision visibility in both directions, multi-label coexistence,
concrete versus userset behavior, filters, pagination and atomic reconciliation.
Confirm a tuple-only external adapter can provide the complete raw facade.

### C3 — Retire obsolete APIs and helpers

Remove GetPermissionsForRelation and its incomplete reverse index, unused
dedupeSortChecks/checkString/checkDirectRelation, the ignored checks argument,
cloneChange and ErrInvalidRoleAssignment. Preserve live flat graph optimizations.
Retire old snapshot ports and retarget cancellation, panic, closed-reader and
ambient transaction tests without losing concurrency/ownership coverage.

### C4 — Fresh SQL installation

Collapse PostgreSQL and SQLite/Turso authorization migrations to the canonical
schema and optional cache capture. Remove retired table creation/conversion and
rollback tooling. Update migration exports, test fixtures, schema assertions and
owned verification runner as needed. Verify fresh primary-only and primary+cache
installation, supported constraints/indexes, audit writes and capture transactions.

### C5 — Documentation and verification

Correct current contract comments and examples; keep historical migration,
downgrade and audit evidence intact. Use named implementation and architecture
agents for bounded parallel work and review. Record durable verification and an
executed copy under plans/ after completion.

## Verification

- Focused core/memory/cache tests and race tests; conformance covers typed raw
  continuation, malformed listing cursors and relationship facade projections.
- Public-consumer compile/run proof without private imports; exercised writes,
  exact checks and decision visibility through canonical root assembly.
- goimports, git diff --check, guards and full 42-module make check (build, test,
  vet, generated drift). Run with a strict environment allowlist and offline Go
  configuration so inherited datastore credentials cannot enable live tests.
- Owned integration runner for PostgreSQL (public and named schemas), SQLite and
  Redis, including race, snapshot lifecycle/ambient/concurrency and audit/cache
  suites. Only runner-owned temporary services; no external host endpoints.
- Preserve owner-file checksums. Report exact passes, skips, failures and any
  follow-up work; retain verification artifacts in the completed plan/manifest.

## Execution record

- Confirmed main at 1e1308733cd8c23995497134052b2ef16c1f5b30 with the prior
  unified-tuples/guards changes present. No user files reset or committed.
- Implementation started after the owner's explicit acceptance of audit fixes.
- Owner clarified the schema is from scratch; fresh-install simplification now
  replaces the previous migration-preservation assumption.

### Completed implementation

- C1: Public typed continuation, seven-field comparator, component filters and
  strict cursor validation are implemented consistently in memory/cache/SQL.
  Audit identity no longer depends on serialized cursor keys.
- C2: Root relationship injection is removed. Both facades share Tuples; common
  conformance exercises visibility, independent labels, exact/userset identity,
  list filtering/cursors/counts and invalid selectors. SQL ambient fixtures also
  exercise facade pending writes, rollback ownership and weak isolation refusal.
  Retained optimized raw graph ports are tested through a conformance-only bundle.
- C3: Removed the incorrect introspection API and index, dead helpers/sentinel
  and obsolete lookup snapshot protocol. Canonical lifecycle coverage remains.
- C4: Replaced both SQL histories with one base0001 tuples/audit migration and one
  optionalcache0002 migration, ordered for combined export. Removed old conversion,
  backup/downgrade scripts and legacy audit encodings. Fresh schema, applied
  constraints/indexes, cache-later and failed-DDL rollback tests replace upgrade tests.
- C5: Current documentation describes the new setup. Historical executed plans
  remain evidence of earlier decisions. Backend reviews found no blocking issues.

### Verification so far

- Core build/test/vet and full core race tests passed; focused lifecycle and
  immutable-model cases passed independently. Both adapter modules build/test/vet
  passed offline (PostgreSQL live cases deferred to the owned runner).
- Full 42-module make check passed with environment allowlist/offline Go and
  permission for test-owned local sockets. All guards passed.
- Twelve runner safety tests passed. Earlier sandbox-only socket/cache permission
  failures were resolved by the sanitized local verification invocation.
- Documentation pnpm typecheck and build passed; only the optional Docusaurus
  update-config write was unavailable.
- External GOWORK=off public consumer ran typed raw pagination and proved mixed
  checks see role/relationship writes and revocation. No private imports required.
- All changed Go files are goimports-clean; diff check and owner-file hashes pass.

### Final verification

- Owned local integration runner passed with race enabled: PostgreSQL 17.4 under
  non-C locale in public and named schemas, local SQLite, real Redis and CMS HTTP.
  Fresh base/cache installation, cache-later, rollback, applied schema, malformed
  schema refusal, cursor/facade conformance and lifecycle proofs all passed.
- Runner cleanup passed. No external datastore was used. Remote Turso was not run.
- Distinct named test passes by suite: pgxdb301, core1275, PostgreSQL553 in each
  schema, SQLite542, Redis52, CMS522. Core memory TestTransactional intentionally
  skips because memory has no host transactor; null skip entries are no-test
  packages. No SQL/Redis test skipped in the owned run.
- Existing performance benchmarks remain available; the 106-case performance
  matrix was not rerun for this cleanup. Prior numbers are marked as historical.
- All owner-file checksums still match. No code or SQL changes followed the owned
  source snapshot; final edits only record verification in root-level documents.
- Durable evidence: `plans/authorization-cleanup-verification.json`, including
  tested source digest, required proof names, public-consumer source/output,
  exact commands, current file hashes and explicit verification limits.

### Follow-ups

Graph-port ownership and convergence of legacy HTTP middleware remain separate
behavior/design work, as agreed in the audit. No correctness failure remains
open in this cleanup. Release/publishing is not part of this task.
