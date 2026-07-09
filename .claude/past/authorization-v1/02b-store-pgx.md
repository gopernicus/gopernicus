# Phase Z2b — `features/authorization/stores/pgx` (module 34)

Status: **RATIFIED 2026-07-09 (jrazmi) — Q1-Q7 at recommendations; EXECUTING**
Executor model: opus
Depends on: Z2a (the canonical migration version filename set is authored
there; this tree mirrors it exactly — gaps reproduced)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2.5, §9,
§10, §13 Z2. Conventions template: `features/events/stores/pgx` (package
`pgx`, connector `integrations/datastores/pgxdb` under the `pgxdb` alias,
boot probe, README, env-gated conformance) + the pgxdb D2–D6 helpers.

## DoD

- Module 34 registered (go.work, `MODULES`, `STORE_MODULES` 9 → 10, a
  `test-stores` pgx leg); `make check` green at **34 modules**, hermetic
  (loud skip without `POSTGRES_TEST_DSN`).
- Migration filenames/versions **identical to Z2a's tree**
  (`0001_iam_relationships.sql` + `0002_iam_roles.sql`); source
  `"authorization"`; PostgreSQL dialect (TIMESTAMPTZ where timestamps
  apply; if Q4 = KEEP, metadata as **JSONB + a GIN index** — the
  documented divergence vs turso's TEXT, same filename).
- BOTH kinds' repositories: the full 14-method `relationship.Storer` —
  recursive-CTE expansion +
  descendant lookup, cycle-safe (`WITH RECURSIVE` + UNION dedup),
  **UNBOUNDED like Z2a and the memstore** (2026-07-08 owner ruling,
  codex fold A1, superseding lead refinement 8: `MaxTraversalDepth` is
  engine-only); counts direct-only — AND the 5-method `role.Storer`
  (plain lookups, no recursion).
- Constructor pinned (review-gate fold, steward minor 5; cut refinement
  11): `Repositories(db) (authorization.Repositories, error)` returning
  BOTH kinds wired, with the
  boot-time probes of both tables INSIDE it — the error names the
  specific missing table (charter checklist item 5; the jobs
  turso.go:29 precedent), plus `ExportMigrations(dst)`.
- Live leg: the full Z1 storetest — **all five named adversarial
  sub-runners AND the `Roles/*` family green against dockered
  postgres:17** — recorded for the
  milestone-close NOTES artifact. With Z2a, both store trees pass ONE
  suite (DP1 parity).

## Preconditions

- Z2a executed (canonical filename set exists).
- Q4 + Q6 + Q7 ratified (same gates as Z2a — the pgx tree mirrors Z2a's
  metadata disposition, inline `relationship_id` DEFAULT
  (`gen_random_uuid()::text`), all-empty-batch UNNEST omit branch, and
  silent-no-op conflict semantics).
- Read `features/events/stores/pgx/postgres.go` and Z2a's landed SQL
  before authoring — the pgx tree is a dialect translation, never a
  redesign.

## Tasks

### task-1: module skeleton + mirrored migrations + probe + registration

- **depends_on:** []
- **model:** opus
- **files:** [features/authorization/stores/pgx/go.mod,
  features/authorization/stores/pgx/postgres.go,
  features/authorization/stores/pgx/migrations/0001_iam_relationships.sql,
  features/authorization/stores/pgx/migrations/0002_iam_roles.sql,
  features/authorization/stores/pgx/README.md,
  go.work, Makefile]
- **verify:** `cd features/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (34 modules) and `make guard`
- **description:** The pgx pair per `features/events/stores/pgx`
  conventions (package `pgx`, `pgxdb` connector alias). Migration
  filenames **byte-identical to Z2a's set** (the kvstore-consolidation
  vocabulary rule); PostgreSQL dialect: same tables/indexes for
  `iam_relationships` AND `iam_roles` — the `iam_relationships` PK carries
  the **INLINE DEFAULT `relationship_id TEXT PRIMARY KEY DEFAULT
  gen_random_uuid()::text`** (Q6, 2026-07-09; the proven phase-04 pgx 0012
  expression, inline — fresh source, no `_id_defaults` retrofit; the turso
  DEFAULT expression differs per dialect but the omit-branch behavior is
  identical) — the roles scope pair **pinned
  explicitly `resource_type TEXT NOT NULL DEFAULT ''` +
  `resource_id TEXT NOT NULL DEFAULT ''`** (re-review lead major 1: a
  nullable scope makes two (subj, role, NULL, NULL) rows DISTINCT under
  PostgreSQL's unique-index NULL semantics → duplicate global grants),
  unique 5-tuple index (refinement 12),
  TIMESTAMPTZ, and — only if Q4 = KEEP — metadata as JSONB with a named
  GIN index (log the JSONB choice explicitly; the events-store
  JSON-not-JSONB precedent was for an opaque payload column — here the
  GIN index is the ratified point of the divergence, design §2.5).
  Constructor is the pinned `Repositories(db)
  (authorization.Repositories, error)` form returning BOTH kinds, with
  the boot-time probes of both tables
  inside (steward minor 5; error names the missing table; **plus
  `iam_relationship_metadata` if Q4 = KEEP — codex fold A8**); README with
  the scaffold-and-own
  prerequisite + the divergence note + the kinds-are-port-optional/
  schema-is-wholesale note — **including the roles-only adopter line
  (re-review note 15): a roles-only host still applies the FULL
  `"authorization"` source, `iam_relationships` included; both boot
  probes expect both tables**. Register module 34: go.work,
  `MODULES`, `STORE_MODULES`, a `test-stores` pgx leg; header count
  33 → 34.

### task-2: both kinds' `Storer` implementations + conformance

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/stores/pgx/relationships.go,
  features/authorization/stores/pgx/roles.go,
  features/authorization/stores/pgx/conformance_test.go]
- **verify:** `cd features/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic) then `make check` and `make guard`; live leg (executor-local): `docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable' go test ./...` — all storetest sub-runners incl. the five `Adversarial/*` names AND the `Roles/*` family PASS; container removed, port freed; record for the NOTES artifact
- **description:** All 14 relationship methods (`relationships.go`) and
  the 5 role methods (`roles.go` — idempotent targeted
  `ON CONFLICT (subject_type, subject_id, role, resource_type,
  resource_id) DO NOTHING`
  Assign (matching Z2a's targeted form — re-review lead major 2; a NOT
  NULL breach still raises), store-stamped `created_at` via the
  connector timestamp helpers with a duplicate retaining the original
  (lead minor 9), `ExecAffecting` Unassign, exact-match `HasExactRole`
  (lead minor 8), two keyset
  listings; no recursion) over `pgxdb` with its D2–D6 helpers,
  mirroring Z2a's SQL semantics in the PostgreSQL dialect: recursive
  CTEs for `CheckRelationWithGroupExpansion` +
  `LookupDescendantResourceIDs` (cycle-safe; PostgreSQL and SQLite
  differ in recursive-CTE behavior — do NOT port the turso SQL
  blindly, re-derive and let the shared suite prove equivalence — this
  is design risk 3's whole point) **UNBOUNDED, matching Z2a and the
  memstore (2026-07-08 owner ruling, codex fold A1 — no depth term in
  the CTE; cycle safety is UNION dedup, `MaxTraversalDepth` stays
  engine-only)**,
  direct-only
  `CountByResourceAndRelation`, keyset `ListPage[T]` with the identical
  order/tiebreak/cursor contract, `ExecAffecting` deletes. **`Create
  Relationships` UNNEST bulk insert + the Database branch (Q6, 2026-07-09,
  mirroring Z2a's turso omit-branch in the pgx dialect): the normal path
  is the original's `INSERT … SELECT … FROM UNNEST(@relationship_ids::…[],
  …) ON CONFLICT DO NOTHING`
  (`../gopernicus-original/…/rebacrelationshipspgx/store.go:200-225`, bare
  ON CONFLICT — second-relation is a silent skip per Q7); ALL-empty batch
  ⇒ DROP the `relationship_ids` array + the relationship_id column from
  the UNNEST insert (the inline DEFAULT fills each PK); ALL-non-empty ⇒
  include it; a MIXED batch is a loud store error. NO `RETURNING` (the
  port is error-only — data-integration major 1). The single-strategy
  invariant is guaranteed at the service (task-4), verified here, never
  trusted by reading `ids[0]`.** Env-gated
  conformance with loud skip. With Z2a green live, DP1 parity holds:
  memstore + turso + pgx, one suite, identical authorization outcomes.

## Acceptance

```sh
cd features/authorization/stores/pgx && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
make check     # 34 modules
make guard
diff <(ls features/authorization/stores/turso/migrations) <(ls features/authorization/stores/pgx/migrations)   # → empty (identical version sets)
```

Store-boundary grep:
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/stores/pgx/`
→ empty.

## Real-interaction check

Standing check (a): `make check` green (34 modules); `examples/minimal`
:8081 → 200s; kill; port free.

## Execution log

### 2026-07-08 — cross-milestone note: pgx-crud-v1 LANDED (read before executing)

pgx-crud-v1 executed to completion this date (P1–P6). This phase's list
citations read accordingly: keyset listing is **`pgxdb.List[T]`/
`ListQuery[T]`** (legacy `ListPage[T]` is DELETED) — NamedArgs
throughout, db-tagged store-local row structs + `toDomain` +
`crud.MapPage` (never db tags on domain types), order allow-lists from
the domain rim (Q1 standard), reverse-probe prev pages, offset mode,
`WithCount` counts via the BaseSQL `COUNT(*)` wrap. The store README
convention section (`integrations/datastores/pgxdb/README.md`) documents
the toolkit; `features/authentication/stores/pgx` is the pattern-setter
implementation. Z1's storetest carries the standard six-case family per
paginated port. A note, not a rewrite — this plan stays DRAFT under its
own ratification.

### 2026-07-09 — task-1 DONE (module 34 skeleton + mirrored migrations + probes)

`features/authorization/stores/pgx` on the events-pgx template (package
`pgx`, `pgxdb` connector alias): go.mod (sibling replaces; standalone
`GOWORK=off go build` verified), the pinned HYBRID `Repositories(db)
(authorization.Repositories, error)` with BOTH boot probes inside via
`to_regclass` (error names the specific missing table),
`ExportMigrations(dst)`. Migrations **filename-identical to Z2a's
canonical set** (`diff` of the two migration dirs → empty). **Exactly two
dialect deltas vs the turso tree, both pinned:** (1) the Q6 PK DEFAULT —
`gen_random_uuid()::text` in place of turso's
`lower(hex(randomblob(16)))`; (2) `created_at TIMESTAMPTZ NOT NULL` in
place of turso's ISO-8601 TEXT. Both dialects: store-stamped, NO DDL
default (the Z2a divergence-log item-4 convention, kept semantically
identical). Everything else structurally identical: subject_relation
`NOT NULL DEFAULT ''`, both unique indexes, the three secondaries,
`iam_roles` 5-tuple unique + scope pair `NOT NULL DEFAULT ''` +
(resource_type, resource_id, created_at) secondary; **no metadata table**
(Q4 TRIM). README carries the scaffold-and-own/never-renumber/
schema-wholesale/roles-only-adopter obligations. Registered module 34:
go.work + `MODULES` + `STORE_MODULES` (9→10) + the plain env-gated
test-stores pgx leg; Makefile header 33→34. `make check` green @ 34;
`make guard` green. (Tidy note: `jackc/pgx/v5` demoted to indirect at
task-1 — only the pgxdb wrapper used; task-2 promoted it back direct.)

### 2026-07-09 — task-2 DONE — **Z2b CLOSED, live leg green on dockered postgres:17 — DP1 parity holds**

`relationships.go` (all 14 methods) + `roles.go` (all 5) +
`conformance_test.go` (plain env-gated on `POSTGRES_TEST_DSN`, loud skip,
`TRUNCATE … RESTART IDENTITY CASCADE` isolation per `newRepos`) +
`postgres_test.go` (hermetic `TestExportMigrations`, the pgx-store
convention); postgres.go's task-1 placeholder block deleted. **The CTEs
were RE-DERIVED in the PostgreSQL dialect** (design risk 3's mandate —
never a port of the turso SQL): `WITH RECURSIVE` + UNION dedup (never
UNION ALL), UNBOUNDED, no depth term; NamedArgs, `::text`-cast recursive
seed, `= ANY(@ids::text[])` membership, `SELECT EXISTS(...)` scanned to
bool. **The UNNEST bulk insert was DERIVED FRESH** — the plan's salvage
citation (`…/rebacrelationshipspgx/store.go:200-225`) does not exist on
this machine and menagerie's copy is the SQLite-DE-GENERATED version (the
UNNEST form survives only in its comments): `INSERT … SELECT … FROM
UNNEST(@arr::text[], …) AS u(...) ON CONFLICT DO NOTHING` (Q7 silent
no-op), `created_at` broadcast as one scalar `@created_at::timestamptz`
per batch; ALL-empty ⇒ the ids array AND the relationship_id column
dropped (the DDL DEFAULT fills each PK) / ALL-non-empty ⇒ included /
MIXED ⇒ loud `ErrInvalidInput`; **NO RETURNING**. Direct-only count;
pinned CheckBatchDirect map; keyset listings via `pgxdb.List[T]` with
db-tagged row structs + `toDomain` + `crud.MapPage` (authentication-pgx
pattern), store-local created_at-only allow-list (the Z2a premise-false
carryover). Roles: targeted `ON CONFLICT(5 cols) DO NOTHING` Assign
(NOT NULL breaches still raise), duplicate retains original CreatedAt,
`ExecAffecting` Unassign, exact-scope `HasExactRole`.

**Divergence (judgment call, logged): the roles keyset tiebreak.**
PostgreSQL forbids NUL in text, so turso's `char(0)` 5-tuple join is
illegal; and the pgx List helper quotes the PK via `QuoteIdentifier`, so
the tiebreak cannot be a raw expression. Solution: a derived `role_key`
column (`subject_type || chr(1) || … || resource_id`) exposed via a
wrapping subquery, with `PKOf` ECHOING the DB-scanned `role_key` (never
recomputed in Go) — the cursor PK matches the column byte-for-byte;
cursors are backend-local, so the chr(1)-vs-\x00 separator difference is
invisible to the port contract. Proven live by `Roles/ListPagination`.
**Timestamp precision (DP1 risk):** `.UTC()` on bind and on `toDomain`
read, the pattern-setter way; no precision failure observed, no storetest
weakening.

**Live leg (recorded for the milestone-close NOTES artifact):** dockered
`postgres:17` on :55432 (`--rm`, named container);
`POSTGRES_TEST_DSN=… go test -v -count=1 ./...` — **ALL PASS, 21 leaf
tests, `TestConformance` 0.93s**: `Relationship/*` (0.43s) CRUDRoundTrip,
DuplicateTupleNoOp, SecondRelationSilentNoOp, DeleteVariants,
CheckBatchDirect, CountDirectOnly, Lookups, ListingPagination,
**DBGeneratedIDOnEmpty** — 9 PASS; `Adversarial/*` (0.23s)
**MembershipCycle, DeepNesting, DiamondDedup, NestedUserset,
Unrestricted** — 5 PASS; `Roles/*` (0.27s) AssignIdempotent,
UnassignIdempotent, HasExactRole, DistinctAssignmentsCoexist,
ListPagination, GlobalFallback — 6 PASS; `TestExportMigrations` PASS.
Container removed; port 55432 confirmed freed. **With Z2a: memstore +
turso + pgx all pass the ONE shared suite — DP1 parity holds; the
flagship provably authorizes identically across all three backends.**

**Acceptance + real-interaction check (standing a):** hermetic module
build/test/vet + standalone `GOWORK=off` build green; `make check` green
@ **34 modules**; `make guard` green; migration filename parity diff →
empty; rule-6 grep clean BOTH directions; store-boundary grep empty;
`gofmt -l` clean. `examples/minimal` booted (:8081) → `GET /` 200,
`GET /products/widget-3000` 200; killed, port freed. **Z2b acceptance
met. Next leg: Z4 (`04-consumer-proof.md`).**
