# Phase Z2a — `features/authorization/stores/turso` (module 32)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: opus
Depends on: Z1
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2.5
(storage, the direct-count pin, the CTE requirement), §9 (crud
re-typing), §10, §13 Z2. Split from the design's single Z2 per the A7a/A7b
precedent (cut refinement 2) — **the canonical migration version filename
set is authored HERE**; Z2b mirrors it exactly.

Salvage source (reference-only, re-typed):
`gopernicus-original/workshop/migrations/primary/0002_rebac.sql` (schema
shape) + `core/repositories/rebac/rebacrelationships/` (SQL/repo shapes).
Conventions template: `features/events/stores/turso` (module layout,
boot probe, README, env-gated conformance) + the feature-standard D2–D6
connector helpers (drift 5): `turso.Querier`/`Scanner`, `ExecAffecting`,
`ListPage[T]`, `NullTime`/`NullTimePtr` + timestamp bundle,
`ExportMigrations`.

## DoD

- Module 32 registered (go.work, `MODULES`, `STORE_MODULES` 8 → 9, a
  `test-stores` turso leg); `make check` green at 32 modules, hermetic
  (loud skip without `TURSO_*`).
- Canonical migrations, source `"authorization"`, `0001_rebac.sql`
  (cut refinement 3; + metadata table only if Q4 = KEEP).
- Full 14-method `relationship.Storer` implemented; group expansion and
  descendant lookup as **recursive CTEs, cycle-safe**, honoring **the
  same traversal bound as the engine's `MaxTraversalDepth` and the
  memstore** (review-gate fold, lead refinement 8 — the storetest
  depth-boundary pair is the parity proof); counts direct-only.
- Constructor pinned (review-gate fold, steward minor 5; cut refinement
  11): `Repositories(db) (authorization.Repositories, error)` with the
  boot-time probe of `rebac_relationships` INSIDE it (charter checklist
  item 5; `features/jobs/stores/turso/turso.go:29` precedent), plus
  `ExportMigrations(dst)`.
- Live leg: the full Z1 storetest — **all five named adversarial
  sub-runners green against the playground DB** — recorded for the
  milestone-close NOTES artifact.

## Preconditions

- Z1 executed (suite exists and is memstore-green in `make check`).
- Read `features/events/stores/turso/turso.go` (probe idiom),
  `features/jobs/stores/turso` (Repositories shape), and the original's
  `0002_rebac.sql` before authoring the schema.
- Q4's answer known (metadata table in or out of 0001).

## Tasks

### task-1: module skeleton + canonical migrations + probe + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/stores/turso/go.mod,
  features/authorization/stores/turso/turso.go,
  features/authorization/stores/turso/migrations/0001_rebac.sql,
  features/authorization/stores/turso/README.md,
  go.work, Makefile]
- **verify:** `cd features/authorization/stores/turso && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (32 modules; go.work ↔ MODULES agreement; the `-tags=integration` vet leg picks the module up via STORE_MODULES) and `make guard`
- **description:** Follow `features/events/stores/turso` conventions
  for module layout, probe idiom, README, and env-gated conformance;
  the constructor is the pinned `Repositories(db)
  (authorization.Repositories, error)` form with the probe inside
  (steward minor 5 — the jobs turso.go:29 shape, not a bare `New(db)`);
  `ExportMigrations(dst)` via the connector helper. `0001_rebac.sql` (source
  `"authorization"`, turso dialect): `rebac_relationships`
  (resource_type, resource_id, relation, subject_type, subject_id,
  subject_relation; unique-tuple index; secondary indexes on resource,
  subject, (resource_type, relation)) — columns/indexes verified against
  the original's `0002_rebac.sql`, divergences logged. Metadata table
  only if Q4 = KEEP (plain JSON TEXT column here — the documented
  index-capability divergence vs pgx's GIN, same filename). **Boot-time
  probe** in the constructor (drift 5: errors before the host serves
  traffic if the `"authorization"` source isn't applied — README states
  the scaffold-and-own prerequisite loudly, incl. the hosts-never-renumber
  rule). Register module 32: go.work, Makefile `MODULES` +
  `STORE_MODULES` + a `test-stores` turso leg (`-tags=integration`);
  header count 31 → 32.

### task-2: the `Storer` implementation + conformance

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/stores/turso/relationships.go,
  features/authorization/stores/turso/conformance_test.go]
- **verify:** `cd features/authorization/stores/turso && go build ./... && go test ./... && go vet ./...` and `go vet -tags=integration ./...` (hermetic) then `make check` and `make guard`; live leg (executor-local): verify `TURSO_DATABASE_URL` equals the authorized playground URL (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` — abort on mismatch), then `TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -tags=integration ./...` — all storetest sub-runners incl. the five `Adversarial/*` names PASS; record counts/durations for the NOTES artifact
- **description:** Implement all 14 `relationship.Storer` methods against
  the turso connector using the D2–D6 helpers (Querier/Scanner scan
  paths, `ExecAffecting` for deletes, `ListPage[T]` keyset listing with
  the exact order/tiebreak/cursor fields Z1's memstore pinned,
  `NullTime` pairs where timestamps apply). **The recursion lands here**
  (design §2.5): `CheckRelationWithGroupExpansion` and
  `LookupDescendantResourceIDs` as recursive CTEs — **cycle-safe by
  construction** (SQLite `WITH RECURSIVE` + UNION dedup / bounded-depth
  guard; the `Adversarial/MembershipCycle` live run is the proof, but
  the SQL must be safe by design, not by test luck) — and **bounded at
  the SAME traversal depth the engine's `MaxTraversalDepth` and the
  memstore use** (lead refinement 8: mirror however the original threads
  the bound into the store SQL, log the mechanism; the
  `Adversarial/DeepNesting` depth-boundary pair must pass live).
  `CountByResourceAndRelation` counts direct tuples ONLY (the §2.5
  security pin — no join into expansion anywhere near it).
  `CheckBatchDirect` returns the map shape the port pins.
  `conformance_test.go` runs `storetest.Run` env-gated behind
  `-tags=integration` with a loud skip; truncate/isolate between
  sub-runners per the auth-v2 store-phase discipline (child-before-parent
  where FKs exist; single-executor caution on the shared playground).

## Acceptance

```sh
cd features/authorization/stores/turso && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
go vet -tags=integration ./...
make check     # 32 modules
make guard
```

Rule-6/store-boundary grep: the store module imports only
`features/authorization` (its parent), `integrations/datastores/turso`,
and sdk —
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/stores/turso/`
→ empty.

Live leg recorded (playground URL asserted; all sub-runners named in the
output) — the dated NOTES.md artifact lands at Z5; keep the transcript in
this file's execution log now.

## Real-interaction check

Standing check (a): `make check` green (32 modules); `examples/minimal`
:8081 → 200s; kill; port free. No host consumes this module yet.

## Execution log

(append dated entries here)
