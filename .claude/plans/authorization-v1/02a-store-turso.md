# Phase Z2a — `features/authorization/stores/turso` (module 33)

Status: **RATIFIED 2026-07-09 (jrazmi) — Q1-Q7 at recommendations; EXECUTING**
Executor model: opus
Depends on: Z1
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2.5
(storage, the direct-count pin, the CTE requirement), §9 (crud
re-typing), §10, §13 Z2. Split from the design's single Z2 per the A7a/A7b
precedent (cut refinement 2) — **the canonical migration version filename
set is authored HERE**; Z2b mirrors it exactly.

Salvage source for the RELATIONSHIP kind (reference-only, re-typed):
`../gopernicus-original/workshop/migrations/primary/0002_rebac.sql`
(sibling of this repo's root — path corrected 2026-07-08, codex fold A7; schema
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

- Module 33 registered (go.work, `MODULES`, `STORE_MODULES` 8 → 9, a
  `test-stores` turso leg); `make check` green at 33 modules, hermetic
  (loud skip without `TURSO_*`).
- Canonical migrations, source `"authorization"`:
  `0001_iam_relationships.sql` + `0002_iam_roles.sql` (cut refinement 3;
  metadata table in 0001 only if Q4 = KEEP).
- BOTH kinds' repositories: the full 14-method `relationship.Storer` —
  group expansion and
  descendant lookup as **recursive CTEs, cycle-safe by UNION dedup,
  UNBOUNDED** (2026-07-08 owner ruling, codex fold A1, superseding lead
  refinement 8: `MaxTraversalDepth` is engine-only, matching the
  original's unbounded store CTE; the depth-boundary storetest pair is
  dropped); counts direct-only — AND the
  5-method `role.Storer` (plain lookups, no recursion).
- Constructor pinned (review-gate fold, steward minor 5; cut refinement
  11): `Repositories(db) (authorization.Repositories, error)` returning
  BOTH kinds wired, with the boot-time probes of `iam_relationships` AND
  `iam_roles` INSIDE it — the error names the specific missing table
  (charter checklist
  item 5; **the DELIBERATE hybrid surface of refinement 11 — the
  `Repositories(...)` bundle name of `features/jobs/stores/turso/
  turso.go:29` combined with the `(…, error)` boot-probe return of
  `features/events/stores/turso/turso.go:43`; jobs alone has NO error, so
  it is not a full precedent, citation corrected 2026-07-09**), plus
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
- **Q6 + Q7 ratified (Z2a execution gates): Q6 sets the inline
  `relationship_id` DDL DEFAULT (task-1) and the all-empty-batch
  omit-column create branch + its conformance case (task-2); Q7 sets the
  second-relation silent-no-op the store's bare `ON CONFLICT DO NOTHING`
  and the conformance re-read assertion depend on.**

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
- **verify:** `cd features/authorization/stores/turso && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (33 modules; go.work ↔ MODULES agreement; the `-tags=integration` vet leg picks the module up via STORE_MODULES) and `make guard`
- **description:** Follow `features/events/stores/turso` conventions
  for module layout, probe idiom, README, and env-gated conformance;
  the constructor is the pinned `Repositories(db)
  (authorization.Repositories, error)` form returning BOTH kinds, with
  the probes inside
  (steward minor 5 — the refinement-11 hybrid: the `Repositories(...)`
  bundle name with events' `(…, error)` probe return, not jobs' error-less
  form and not a single-Store `New(db)`);
  `ExportMigrations(dst)` via the connector helper.
  `0001_iam_relationships.sql` (source
  `"authorization"`, turso dialect): `iam_relationships`
  (**relationship_id PK + created_at — immutable rows, no updated_at;
  made explicit 2026-07-08, codex fold A4: the keyset listings need the
  time order column and the PK tiebreak**; **the PK carries an INLINE
  DEFAULT — `relationship_id TEXT PRIMARY KEY DEFAULT
  (lower(hex(randomblob(16))))` (Q6, 2026-07-09; the proven phase-04
  turso 0012 expression, inline because this is a fresh source — no
  separate `_id_defaults` retrofit file). Under `cryptids.Database` the
  store omits the relationship_id column and this DEFAULT fills it; see
  task-2 for the omit-branch mechanics**; resource_type, resource_id,
  relation, subject_type, subject_id, **subject_relation TEXT NOT NULL
  DEFAULT ''** — codex fold A3, the iam_roles NOT-NULL-scope precedent
  applied: the original's nullable column + `COALESCE(subject_relation,
  '')` unique indexes collapse to a plain NOT NULL column so duplicate
  direct tuples cannot coexist under either dialect's NULL semantics;
  divergence-from-original logged; **TWO unique indexes: the unique-tuple
  index on (resource_type, resource_id, relation, subject_type,
  subject_id, subject_relation) AND the unique-SUBJECT index on
  (resource_type, resource_id, subject_type, subject_id,
  subject_relation)** — one relation per subject per resource, the
  original's `idx_rebac_relationships_unique_subject` ADOPTED by the
  2026-07-08 owner ruling (codex fold A2); secondary indexes on resource,
  subject, (resource_type, relation)) — columns/indexes verified against
  the original's `0002_rebac.sql` (table renamed per the owner
  direction), remaining divergences logged. Metadata table (`iam_relationship_
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
  indexes on (subject_type, subject_id) and **(resource_type,
  resource_id, created_at)** (changed 2026-07-08, codex fold A6:
  `ListByResource` filters (resource_type, resource_id) with a
  created_at keyset — the previously pinned role-led index served no
  pinned query; exact-match lookups ride the unique 5-tuple index).
  **Boot-time
  probes** in the constructor (drift 5): both tables (**plus
  `iam_relationship_metadata` if Q4 = KEEP — codex fold A8**), erroring before
  the host serves
  traffic if the `"authorization"` source isn't applied, the message
  naming the specific missing table — README states
  the scaffold-and-own prerequisite loudly, incl. the hosts-never-renumber
  rule and the kinds-are-port-optional/schema-is-wholesale note
  (§2.1 bounding rule, owner-direction section) — **including the
  roles-only adopter line (re-review note 15): a roles-only host still
  applies the FULL `"authorization"` source, `iam_relationships`
  included; both boot probes expect both tables**. Register module 33:
  go.work, Makefile `MODULES` +
  `STORE_MODULES` + a `test-stores` turso leg (`-tags=integration`);
  header count 32 → 33.

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
  construction via UNION dedup** (SQLite `WITH RECURSIVE`; the
  `Adversarial/MembershipCycle` live run is the proof, but
  the SQL must be safe by design, not by test luck) — and **UNBOUNDED,
  deliberately** (2026-07-08 owner ruling, codex fold A1, superseding
  lead refinement 8: the original's CTE carries no depth term —
  `../gopernicus-original/core/repositories/rebac/rebacrelationships/rebacrelationshipspgx/store.go:22-30`
  — and `MaxTraversalDepth` bounds only the engine's Go recursion; no
  depth parameter enters the store; the depth-boundary pair is dropped
  from `Adversarial/DeepNesting`).
  `CountByResourceAndRelation` counts direct tuples ONLY (the §2.5
  security pin — no join into expansion anywhere near it).
  **`CreateRelationships` batch shape + the Database branch (Q6,
  2026-07-09): the normal path is a multi-row `INSERT INTO
  iam_relationships (relationship_id, …) VALUES (?,…),(?,…) ON CONFLICT
  DO NOTHING` (libsql has NO UNNEST — this is the turso analog of pgx's
  UNNEST bulk insert; the bare `ON CONFLICT DO NOTHING` is the original's
  faithful shape, and the second-relation-same-subject conflict is a
  SILENT skip per Q7, not an error). The store branches on the batch's
  ids: ALL-empty ⇒ OMIT the relationship_id column from the column list
  and every VALUES row for the whole batch (the inline DEFAULT fills the
  PK); ALL-non-empty ⇒ include it; a MIXED batch is a loud store error
  (the single-strategy invariant is the service's guarantee — task-4 —
  which the store verifies, never trusts by reading `ids[0]`). NO
  `RETURNING` — the port is error-only, so a key read-back has no consumer
  and would under-return on ON-CONFLICT skips (data-integration major 1).
  A conformance case exercises the libsql multi-row-VALUES + ON CONFLICT
  DO NOTHING + omitted-DEFAULT-PK combination, which is novel to the turso
  stores (existing turso RETURNING use is single-row, no ON CONFLICT).**
  `CheckBatchDirect` returns the map shape the port pins.
  `conformance_test.go` runs `storetest.Run` env-gated behind
  `-tags=integration` with a loud skip; truncate/isolate between
  sub-runners per the auth-v2 store-phase discipline (child-before-parent
  where FKs exist; single-executor caution on the shared playground).

## Acceptance

```sh
cd features/authorization/stores/turso && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
go vet -tags=integration ./...
make check     # 33 modules
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

Standing check (a): `make check` green (33 modules); `examples/minimal`
:8081 → 200s; kill; port free. No host consumes this module yet.

## Execution log

### 2026-07-08 — cross-milestone note: pgx-crud-v1 LANDED (read before executing)

pgx-crud-v1 executed to completion this date (P1–P6). This phase's list
citations read accordingly: the connector helper is **`turso.List[T]`/
`ListQuery[T]`** (legacy `ListPage[T]` is DELETED), driven by a
per-aggregate order allow-list (`map[string]crud.OrderField` + default
`crud.Order` declared in the feature-core domain package — the Q1
standard) and the full `crud.ListRequest` (order, bidirectional cursors,
offset mode, `WithCount` → `Page.Total`). The two keyset listings in
task-2 implement the extended contract, and Z1's storetest should carry
the standard six-case family per paginated port (`Order`, `PrevPage`,
`OffsetMode`, `WithCount`, `StaleCursorOrderChange`,
`CursorOffsetExclusive` — `features/authentication/storetest` is the
pattern). D2–D6 helper references otherwise stand. A note, not a
rewrite — this plan stays DRAFT under its own ratification.

### 2026-07-09 — task-1 DONE (module 33 skeleton + canonical migrations + probes)

`features/authorization/stores/turso` authored on the events-turso template:
go.mod (sibling replaces; standalone `GOWORK=off go build` verified), the
pinned HYBRID constructor `Repositories(db) (authorization.Repositories,
error)` with BOTH boot probes inside (the error names the specific missing
table), `ExportMigrations(dst)`. Canonical migration set authored (source
`"authorization"`): `0001_iam_relationships.sql` — `relationship_id TEXT
NOT NULL PRIMARY KEY DEFAULT (lower(hex(randomblob(16))))` (the inline Q6
DEFAULT), `subject_relation TEXT NOT NULL DEFAULT ''` (A3), BOTH unique
indexes (full-tuple + unique-SUBJECT, A2), secondaries on resource /
subject / (resource_type, relation); NO metadata table (Q4 TRIM — every
KEEP branch dead). `0002_iam_roles.sql` — 5-tuple unique index (the Assign
ON CONFLICT target), scope pair `NOT NULL DEFAULT ''` (lead major 1),
secondaries (subject_type, subject_id) + (resource_type, resource_id,
created_at) (A6); no id column (roles kind takes no id strategy). README:
scaffold-and-own stated loudly, hosts-never-renumber,
kinds-port-optional/schema-wholesale, the roles-only adopter line
(re-review note 15). Registered module 33: go.work + `MODULES` +
`STORE_MODULES` (8→9) + the test-stores turso `-tags=integration` leg;
Makefile header 32→33. `make check` green @ 33; `make guard` green.

**Divergences vs the original's `0002_rebac.sql` (logged):** (1) table
renamed per the 2026-07-08 owner direction; (2) inline PK DEFAULT (Q6)
where the original had a plain NOT NULL PK; (3) subject_relation NOT NULL
+ plain-column unique indexes replacing nullable + COALESCE (A3); (4)
**created_at carries NO DDL default** (original: `strftime(...)`) —
store-stamped via the connector timestamp helpers, the events/jobs
convention + the rim's one-timestamp-per-batch pin; (5) the original's
partial index `idx_rebac_rel_group_member (... WHERE relation='member')`
dropped — the plan's three-secondary enumeration governs; (6)
`iam_relationship_metadata` dropped entirely (Q4 TRIM); (7) index names
re-prefixed `idx_iam_relationships_*`, `IF NOT EXISTS` added (repo
convention).

**Premise-false (logged):** the salvage path `../gopernicus-original/…`
does not exist on this machine; the DDL was verified against the identical
schema copy at
`/Users/jrazmi/code/menagerie/workshop/migrations/primary/0002_rebac.sql`.

### 2026-07-09 — task-2 DONE — **Z2a CLOSED, live leg green on the playground**

`relationships.go` (all 14 `relationship.Storer` methods) + `roles.go`
(all 5 `role.Storer` methods) + `conformance_test.go`
(`//go:build integration`, loud env skip, migrate + truncate-both-tables
isolation per `newRepos`); turso.go's task-1 placeholder block deleted.
The recursion: `reachableCTE` (CheckRelationWithGroupExpansion /
CheckBatchDirect / LookupResourceIDs) + the descendants CTE
(LookupDescendantResourceIDs) — `WITH RECURSIVE`, **UNION dedup (never
UNION ALL), UNBOUNDED, no depth term anywhere in the store** (A1);
**derived fresh from the port contract + Z1 memstore semantics**
(`expandSubjectGroups` / the fixpoint descendant walk; menagerie's
`rebacrelationshipsstore/store.go` inspected as cross-check only — logged
derived, not salvaged). `CountByResourceAndRelation` counts direct tuples
only (§2.5 pin). `CreateRelationships`: multi-row VALUES + the bare
`ON CONFLICT DO NOTHING` (Q7 silent no-op), one store-stamped created_at
per batch, ALL-empty ⇒ relationship_id column omitted (the DDL DEFAULT
fills the PK) / ALL-non-empty ⇒ included / MIXED ⇒ loud `ErrInvalidInput`;
**NO RETURNING**. Roles `Assign` = the targeted `ON CONFLICT(subject_type,
subject_id, role, resource_type, resource_id) DO NOTHING` (never INSERT OR
IGNORE — lead major 2), duplicate retains the original CreatedAt;
`ExecAffecting` Unassign; exact-scope `HasExactRole`; keyset listings with
the memstore's exact tiebreaks (relationship_id; roles: a `char(0)`
5-tuple composite mirroring the memstore's `\x00` join). Also added
`turso_test.go` (hermetic `TestExportMigrations` — the universal
store-module convention; additive beyond the task file list, logged).

**Live leg (recorded for the milestone-close NOTES artifact):**
`TURSO_DATABASE_URL` asserted ==
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
pre-run (the only truncate-authorized DB; token never printed).
`go test -tags=integration -v -count=1 ./...` — **ALL PASS, 21 leaf
tests, 40.944s** (`TestConformance` 40.75s):

- `Relationship/*` (17.34s): CRUDRoundTrip, DuplicateTupleNoOp,
  SecondRelationSilentNoOp, DeleteVariants, CheckBatchDirect,
  CountDirectOnly, Lookups, ListingPagination, **DBGeneratedIDOnEmpty** —
  9 PASS (DBGeneratedIDOnEmpty + SecondRelationSilentNoOp exercise the
  novel multi-row-VALUES + ON CONFLICT + omitted-DEFAULT-PK combination
  live).
- `Adversarial/*` (11.68s): **MembershipCycle, DeepNesting, DiamondDedup,
  NestedUserset, Unrestricted** — all five named sub-runners PASS
  (MembershipCycle = live CTE termination on cyclic data).
- `Roles/*` (11.72s): AssignIdempotent, UnassignIdempotent, HasExactRole,
  DistinctAssignmentsCoexist, ListPagination, GlobalFallback — 6 PASS.
- `TestExportMigrations` — PASS (hermetic).

**Premise-false / notes (logged):** (1) the cross-milestone note's "order
allow-list declared in the feature-core domain package" — Z1 exported NO
`map[string]crud.OrderField`; the memstore hardcodes created_at as the
only sortable field, so the allow-list is store-local
(`orderFields`/`defaultOrder`, created_at only, default DESC) matching the
memstore contract exactly; (2) the note's "standard six-case family per
paginated port" does not exist in Z1's storetest — its pagination coverage
is `ListingPagination`/`ListPagination` (cursor paging,
full-coverage/no-overlap, empty-page shape), which ran and passed live;
(3) the ALL-non-empty and MIXED `CreateRelationships` branches are
hermetically untested by storetest (every case leaves `RelationshipID`
empty) — the engine-mint path lands coverage at Z4/Z2b parity.

**Acceptance + real-interaction check (standing a):** hermetic module
build/test/vet + `go vet -tags=integration` green; `make check` green @
**33 modules**; `make guard` green (all seven); rule-6 grep clean BOTH
directions; store-boundary grep empty; `gofmt -l` clean.
`examples/minimal` booted (:8081) → `GET /` 200,
`GET /products/widget-3000` 200; killed, port freed. **Z2a acceptance met.
Next leg: Z2b (`02b-store-pgx.md`) — mirrors this canonical filename set
exactly.**
