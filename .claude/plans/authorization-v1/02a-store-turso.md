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

Salvage source for the RELATIONSHIP kind (reference-only, re-typed):
`gopernicus-original/workshop/migrations/primary/0002_rebac.sql` (schema
shape — table renamed `iam_relationships` per the 2026-07-08 owner
direction) + `core/repositories/rebac/rebacrelationships/` (SQL/repo
shapes). The ROLES kind (`iam_roles`) has no salvage — overview
refinement 12 is its spec.
Conventions template: `features/events/stores/turso` (module layout,
boot probe, README, env-gated conformance) + the feature-standard D2–D6
connector helpers (drift 5): `turso.Querier`/`Scanner`, `ExecAffecting`,
`ListPage[T]`, `NullTime`/`NullTimePtr` + timestamp bundle,
`ExportMigrations`.

## DoD

- Module 32 registered (go.work, `MODULES`, `STORE_MODULES` 8 → 9, a
  `test-stores` turso leg); `make check` green at 32 modules, hermetic
  (loud skip without `TURSO_*`).
- Canonical migrations, source `"authorization"`:
  `0001_iam_relationships.sql` + `0002_iam_roles.sql` (cut refinement 3;
  metadata table in 0001 only if Q4 = KEEP).
- BOTH kinds' repositories: the full 14-method `relationship.Storer` —
  group expansion and
  descendant lookup as **recursive CTEs, cycle-safe**, honoring **the
  same traversal bound as the engine's `MaxTraversalDepth` and the
  memstore** (review-gate fold, lead refinement 8 — the storetest
  depth-boundary pair is the parity proof); counts direct-only — AND the
  5-method `role.Storer` (plain lookups, no recursion).
- Constructor pinned (review-gate fold, steward minor 5; cut refinement
  11): `Repositories(db) (authorization.Repositories, error)` returning
  BOTH kinds wired, with the boot-time probes of `iam_relationships` AND
  `iam_roles` INSIDE it — the error names the specific missing table
  (charter checklist
  item 5; `features/jobs/stores/turso/turso.go:29` precedent), plus
  `ExportMigrations(dst)`. Kind selection is the host's wiring choice
  (README says so — overview refinement 11).
- Live leg: the full Z1 storetest — **all five named adversarial
  sub-runners AND the `Roles/*` family green against the playground DB**
  — recorded for the milestone-close NOTES artifact.

## Preconditions

- Z1 executed (suite exists and is memstore-green in `make check`).
- Read `features/events/stores/turso/turso.go` (probe idiom),
  `features/jobs/stores/turso` (Repositories shape), the original's
  `0002_rebac.sql`, and overview refinement 12 (the `iam_roles` shape)
  before authoring the schema.
- Q4's answer known (metadata table in or out of 0001).

## Tasks

### task-1: module skeleton + canonical migrations + probe + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/stores/turso/go.mod,
  features/authorization/stores/turso/turso.go,
  features/authorization/stores/turso/migrations/0001_iam_relationships.sql,
  features/authorization/stores/turso/migrations/0002_iam_roles.sql,
  features/authorization/stores/turso/README.md,
  go.work, Makefile]
- **verify:** `cd features/authorization/stores/turso && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (32 modules; go.work ↔ MODULES agreement; the `-tags=integration` vet leg picks the module up via STORE_MODULES) and `make guard`
- **description:** Follow `features/events/stores/turso` conventions
  for module layout, probe idiom, README, and env-gated conformance;
  the constructor is the pinned `Repositories(db)
  (authorization.Repositories, error)` form returning BOTH kinds, with
  the probes inside
  (steward minor 5 — the jobs turso.go:29 shape, not a bare `New(db)`);
  `ExportMigrations(dst)` via the connector helper.
  `0001_iam_relationships.sql` (source
  `"authorization"`, turso dialect): `iam_relationships`
  (resource_type, resource_id, relation, subject_type, subject_id,
  subject_relation; unique-tuple index; secondary indexes on resource,
  subject, (resource_type, relation)) — columns/indexes verified against
  the original's `0002_rebac.sql` (table renamed per the owner
  direction), divergences logged. Metadata table (`iam_relationship_
  metadata`) in 0001
  only if Q4 = KEEP (plain JSON TEXT column here — the documented
  index-capability divergence vs pgx's GIN, same filename).
  `0002_iam_roles.sql`: `iam_roles` per overview refinement 12 —
  subject_type, subject_id, role, created_at, and the scope pair
  **pinned explicitly `resource_type TEXT NOT NULL DEFAULT ''` +
  `resource_id TEXT NOT NULL DEFAULT ''`** (re-review lead major 1: the
  whole empty-string-global contract rests on it — a nullable scope
  makes two (subj, role, NULL, NULL) rows DISTINCT under the unique
  index → duplicate global grants; the `Roles/AssignIdempotent`
  constraint-level case is the proof); unique index on the full 5-tuple;
  secondary
  indexes on (subject_type, subject_id) and (role, resource_type,
  resource_id). **Boot-time
  probes** in the constructor (drift 5): both tables, erroring before
  the host serves
  traffic if the `"authorization"` source isn't applied, the message
  naming the specific missing table — README states
  the scaffold-and-own prerequisite loudly, incl. the hosts-never-renumber
  rule and the kinds-are-port-optional/schema-is-wholesale note
  (§2.1 bounding rule, owner-direction section) — **including the
  roles-only adopter line (re-review note 15): a roles-only host still
  applies the FULL `"authorization"` source, `iam_relationships`
  included; both boot probes expect both tables**. Register module 32:
  go.work, Makefile `MODULES` +
  `STORE_MODULES` + a `test-stores` turso leg (`-tags=integration`);
  header count 31 → 32.

### task-2: both kinds' `Storer` implementations + conformance

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/stores/turso/relationships.go,
  features/authorization/stores/turso/roles.go,
  features/authorization/stores/turso/conformance_test.go]
- **verify:** `cd features/authorization/stores/turso && go build ./... && go test ./... && go vet ./...` and `go vet -tags=integration ./...` (hermetic) then `make check` and `make guard`; live leg (executor-local): verify `TURSO_DATABASE_URL` equals the authorized playground URL (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` — abort on mismatch), then `TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -tags=integration ./...` — all storetest sub-runners incl. the five `Adversarial/*` names AND the `Roles/*` family PASS; record counts/durations for the NOTES artifact
- **description:** Implement all 14 `relationship.Storer` methods
  (`relationships.go`) and the 5 `role.Storer` methods (`roles.go` —
  plain SQL: Assign as the **targeted `INSERT … ON
  CONFLICT(subject_type, subject_id, role, resource_type, resource_id)
  DO NOTHING` — NEVER `INSERT OR IGNORE`** (re-review lead major 2:
  SQLite's OR IGNORE swallows EVERY constraint violation, a NOT NULL
  breach included, as a silent no-op, while pgx's ON CONFLICT DO NOTHING
  still raises it — divergent behavior in exactly the column deciding
  global-vs-scoped; libsql supports the targeted form; the
  non-equivalence is recorded in the overview's schema-impact note),
  store-stamped `created_at` via the connector timestamp helpers with a
  duplicate retaining the original (lead minor 9), `ExecAffecting`
  Unassign, exact-match `HasExactRole` (lead minor 8), two keyset
  listings; no recursion
  anywhere near this table) against
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
