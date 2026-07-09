# Phase Z2b — `features/authorization/stores/pgx` (module 33)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08, authorized
as a planning-only leg)**
Executor model: opus
Depends on: Z2a (the canonical migration version filename set is authored
there; this tree mirrors it exactly — gaps reproduced)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §2.5, §9,
§10, §13 Z2. Conventions template: `features/events/stores/pgx` (package
`pgx`, connector `integrations/datastores/pgxdb` under the `pgxdb` alias,
boot probe, README, env-gated conformance) + the pgxdb D2–D6 helpers.

## DoD

- Module 33 registered (go.work, `MODULES`, `STORE_MODULES` 9 → 10, a
  `test-stores` pgx leg); `make check` green at **33 modules**, hermetic
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
- **verify:** `cd features/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (33 modules) and `make guard`
- **description:** The pgx pair per `features/events/stores/pgx`
  conventions (package `pgx`, `pgxdb` connector alias). Migration
  filenames **byte-identical to Z2a's set** (the kvstore-consolidation
  vocabulary rule); PostgreSQL dialect: same tables/indexes for
  `iam_relationships` AND `iam_roles` — the roles scope pair **pinned
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
  probes expect both tables**. Register module 33: go.work,
  `MODULES`, `STORE_MODULES`, a `test-stores` pgx leg; header count
  32 → 33.

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
  order/tiebreak/cursor contract, `ExecAffecting` deletes. Env-gated
  conformance with loud skip. With Z2a green live, DP1 parity holds:
  memstore + turso + pgx, one suite, identical authorization outcomes.

## Acceptance

```sh
cd features/authorization/stores/pgx && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
make check     # 33 modules
make guard
diff <(ls features/authorization/stores/turso/migrations) <(ls features/authorization/stores/pgx/migrations)   # → empty (identical version sets)
```

Store-boundary grep:
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(authentication|cms|events|jobs)' features/authorization/stores/pgx/`
→ empty.

## Real-interaction check

Standing check (a): `make check` green (33 modules); `examples/minimal`
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
