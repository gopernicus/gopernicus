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
- Migration filenames/versions **identical to Z2a's tree**; source
  `"authorization"`; PostgreSQL dialect (TIMESTAMPTZ where timestamps
  apply; if Q4 = KEEP, metadata as **JSONB + a GIN index** — the
  documented divergence vs turso's TEXT, same filename).
- Full 14-method `relationship.Storer`; recursive-CTE expansion +
  descendant lookup, cycle-safe (`WITH RECURSIVE` + UNION dedup),
  honoring **the same traversal bound as the engine's
  `MaxTraversalDepth`, the memstore, and Z2a** (review-gate fold, lead
  refinement 8); counts direct-only.
- Constructor pinned (review-gate fold, steward minor 5; cut refinement
  11): `Repositories(db) (authorization.Repositories, error)` with the
  boot-time probe INSIDE it (charter checklist item 5; the jobs
  turso.go:29 precedent), plus `ExportMigrations(dst)`.
- Live leg: the full Z1 storetest — **all five named adversarial
  sub-runners green against dockered postgres:17** — recorded for the
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
  features/authorization/stores/pgx/migrations/0001_rebac.sql,
  features/authorization/stores/pgx/README.md,
  go.work, Makefile]
- **verify:** `cd features/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic loud skip) then `make check` (33 modules) and `make guard`
- **description:** The pgx pair per `features/events/stores/pgx`
  conventions (package `pgx`, `pgxdb` connector alias). Migration
  filenames **byte-identical to Z2a's set** (the kvstore-consolidation
  vocabulary rule); PostgreSQL dialect: same tables/indexes,
  TIMESTAMPTZ, and — only if Q4 = KEEP — metadata as JSONB with a named
  GIN index (log the JSONB choice explicitly; the events-store
  JSON-not-JSONB precedent was for an opaque payload column — here the
  GIN index is the ratified point of the divergence, design §2.5).
  Constructor is the pinned `Repositories(db)
  (authorization.Repositories, error)` form with the boot-time probe
  inside (steward minor 5); README with the scaffold-and-own
  prerequisite + the divergence note. Register module 33: go.work,
  `MODULES`, `STORE_MODULES`, a `test-stores` pgx leg; header count
  32 → 33.

### task-2: the `Storer` implementation + conformance

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authorization/stores/pgx/relationships.go,
  features/authorization/stores/pgx/conformance_test.go]
- **verify:** `cd features/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic) then `make check` and `make guard`; live leg (executor-local): `docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable' go test ./...` — all storetest sub-runners incl. the five `Adversarial/*` names PASS; container removed, port freed; record for the NOTES artifact
- **description:** All 14 methods over `pgxdb` with its D2–D6 helpers,
  mirroring Z2a's SQL semantics in the PostgreSQL dialect: recursive
  CTEs for `CheckRelationWithGroupExpansion` +
  `LookupDescendantResourceIDs` (cycle-safe; PostgreSQL and SQLite
  differ in recursive-CTE behavior — do NOT port the turso SQL
  blindly, re-derive and let the shared suite prove equivalence — this
  is design risk 3's whole point) **bounded at the same traversal depth
  as the engine/memstore/Z2a (lead refinement 8; the
  `Adversarial/DeepNesting` depth-boundary pair must pass live)**,
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

(append dated entries here)
