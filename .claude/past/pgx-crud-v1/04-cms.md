# Phase P4 — cms: entries pagination + EAV bulk writes + idiom sweep

Status: **RATIFIED 2026-07-08 (jrazmi — Q1/Q2/Q3 at recommendations; see 00-overview.md)**
Executor model: opus
Depends on: P3 (the pattern; P3's storetest case family is copied here)

## Context

cms is the biggest store and the only feature with a human-visible list
surface. Entries (the EAV spine) is its one crud-paginated port —
`List`/`ListByTerm` share `listWhere` onto `pgxdb.ListPage`
(`features/cms/stores/pgx/entries.go:127`). Its writes hold the repo's
worst Exec loops: `writeFields` (entries.go:217-227, one INSERT per EAV
field, inside InTx from Create/Update) and `SetTerms` (entries.go:149-161,
DELETE + one INSERT per term). Two example-host memstores run this
feature's storetest hermetically in `make check`
(`examples/minimal/internal/memstore`, `examples/auth-cms/internal/memstore`).
The admin entries list and the public archive parse only `cursor`; both
shipped views have an "Older →" link and no prev control
(`features/cms/views/templ/entries_list.templ:26`, `public.templ:86`).

## Goal

Entries lists are orderable, bidirectionally pageable, offset-capable and
countable across pgx/turso/both memstores; EAV multi-row writes are
single UNNEST statements on pgx; the whole cms pgx store is on the idiom
set; the admin and public list pages page both directions in a browser.

## Definition of Done

- Entries order allow-list in `features/cms/domain/content` (minimum
  `created_at` DESC; candidates like `updated_at`/`published_at` only if
  the spine columns exist AND are indexed — **EAV `entry_fields` are
  never sortable**, per ARCHITECTURE.md's Registry-model rule).
- storetest: the P3 six-case family applied to Entries `List` and
  `ListByTerm`; both example memstores green hermetically.
- pgx: entries on `pgxdb.List` + row structs; `writeFields` and
  `SetTerms` each a single UNNEST array-param statement
  (rebacrelationshipspgx/store.go:176-239 is the shape oracle), InTx
  boundaries unchanged; assets/inquiries/menus/terms swept to
  NamedArgs + CollectRows/CollectOneRow.
- turso: entries call site on `turso.List` — nothing more.
- Admin entries + public archive wire `order`/`limit` (SSR clamp
  semantics per Q3) and render working prev links; templ regenerated via
  `make generate`, never hand-edited.

## Out of scope

- Paginating terms/menus/media/inquiries (unpaginated ports stay so).
- Home's fixed `Limit: crud.MaxLimit` fetch (unchanged).
- EAV schema or Registry changes of any kind; no new indexes.
- turso idiom parity.

## Schema / datastore impact

None. UNNEST changes write shape, not schema. `ListByTerm`'s join is
inside `BaseSQL`, so the count subquery wrap covers it for free.

## Generated-artifact impact

`features/cms/views/templ/entries_list.templ` + `public.templ` change;
regenerate with `make generate`; `make check`'s drift gate must be clean.
`examples/cms/internal/theme` may override these templates — task-5
inspects and updates any override.

## Risks

1. UNNEST rewrite of EAV writes must preserve field-type fidelity (the
   `entry_fields` value/type columns) — the existing storetest round-trip
   cases plus live conformance are the acceptance.
2. Views-port growth: prev-link data must reach the templates. Prefer
   additive view-model struct fields (non-breaking); if the Views port
   signature itself must change, STOP and flag — that's an FS-contract
   surface (feature core public API + views module + possible host theme).

## Tasks

### task-1: order vocabulary + storetest + both memstores (one boundary)

- **depends_on:** []
- **model:** opus
- **files:** [features/cms/domain/content/order.go, features/cms/storetest/storetest.go, examples/minimal/internal/memstore/memstore.go, examples/auth-cms/internal/memstore/memstore.go]
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...`; `cd examples/minimal && go test ./...`; `cd examples/auth-cms && go test ./...`; then `make check` (dialect stores must skip-not-fail hermetically)
- **description:** Declare the entries order allow-list + default in the
  content domain package (match existing file conventions). Copy P3's
  six-case family onto `testEntriesPagination`/`testEntriesCursorEdges`
  ground for both `List` and `ListByTerm`, reusing `collectEntries`.
  Extend both memstores' `entryPageOf` helpers (memstore.go:392 / :397)
  for order, prev probes, offset, count. Dialect stores now fail the new
  cases live (expected until tasks 2–4); no `make test-stores` mid-phase.

### task-2: pgx entries — List onto pgxdb.List + UNNEST bulk writes

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/cms/stores/pgx/entries.go, features/cms/stores/pgx/helpers.go]
- **verify:** `cd features/cms/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic skip); live leg: `docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...` — extended suite green; container removed
- **description:** Rewrite `List`/`ListByTerm` onto `pgxdb.List[T]` with
  a db-tagged entry row struct + toDomain + `crud.MapPage`; convert
  `listWhere` into a NamedArgs filter builder shared by list and (via
  the helper's subquery wrap) count. Replace the `writeFields` loop with
  one `INSERT INTO entry_fields … SELECT … FROM UNNEST(@…[], …)`
  (arrays per column, entry_id repeated) and `SetTerms` with DELETE +
  one UNNEST insert — both keeping their existing InTx boundaries and
  MapError semantics. NamedArgs + Collect* throughout the file.

### task-3: pgx idiom sweep — remaining cms store files

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/cms/stores/pgx/assets.go, features/cms/stores/pgx/inquiries.go, features/cms/stores/pgx/menus.go, features/cms/stores/pgx/terms.go, features/cms/stores/pgx/postgres.go]
- **verify:** hermetic module verify as task-2, then the same live pgx leg — full suite green; then `make check`
- **description:** Convert every remaining query to NamedArgs +
  CollectRows/CollectOneRow over row structs + toDomain (unpaginated
  ports keep their shapes — no pagination is added). Preserve every InTx
  boundary and ExecAffecting zero-rows mapping. No behavior change; the
  pre-existing storetest cases are the regression net.

### task-4: turso minimal migration

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/cms/stores/turso/entries.go, features/cms/stores/turso/helpers.go]
- **verify:** `cd features/cms/stores/turso && go build ./... && go test ./... && go vet ./... && go vet -tags=integration ./...` then `make check`; live leg (playground discipline — URL must be `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`): `go test -tags=integration ./...` — extended suite green, recorded
- **description:** Migrate the entries `turso.ListPage` call site to
  `turso.List` with the order allow-list and full ListRequest; hand-scan
  and all other turso idioms untouched.

### task-5: HTTP edge + prev links in the shipped views

- **depends_on:** [task-2, task-4]
- **model:** opus
- **files:** [features/cms/internal/inbound/http/entries.go, features/cms/internal/inbound/http/public.go, features/cms/views/templ/entries_list.templ, features/cms/views/templ/public.templ, examples/cms/internal/theme]
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...`; `make generate` then `git status` shows only intended `*_templ.go` regeneration; `cd features/cms/views/templ && go build ./... && go test ./...`; `make check` and `make guard`; then the real-interaction protocol below
- **description:** Admin entries list: parse `order` (ParseOrder against
  the content allow-list; bad value falls back to default per Q3) and
  `limit` (clamped — SSR keeps NormalizedLimit semantics), keep `cursor`.
  Public archive: keep cursor-only paging but thread the page's
  `HasPrev`/`PreviousCursor` through. Add a "← Newer" link beside the
  existing "Older →" in both templates, driven by
  HasPrev/PreviousCursor (empty PreviousCursor + HasPrev ⇒ link to the
  bare list URL — the first page). Extend the view-model structs
  additively; inspect `examples/cms/internal/theme` for overriding
  templates and mirror the change if one overrides these views. Never
  hand-edit `*_templ.go`. Do not add routes.

## Acceptance

```sh
cd features/cms && go build ./... && go vet ./... && go test ./...
cd examples/minimal && go test ./... && cd ../auth-cms && go test ./...
make check && make guard
grep -rn "ListPage" features/cms/stores/                      # → empty
grep -rn "INSERT INTO entry_fields" features/cms/stores/pgx/  # → exactly the one UNNEST statement
```

Live: task-2/3 pgx leg + task-4 turso leg recorded (dated) for NOTES.

## Real-interaction check

`make run` (examples/cms on Turso; migrations pre-boot). In a browser:

- Admin entries list: seed enough entries to page; click "Older →" to
  page 2 → "← Newer" appears and returns exactly page 1; add
  `?order=created_at:asc` → order flips and paging still works both
  directions; a garbage `?order=nope` renders the default order (no 500).
- Public: `/category/{slug}` archive pages both directions the same way.
- Kill the server, port free. Green tests alone do not close task-5.

## Execution log

### 2026-07-08 — phase 4 executed (implementer on opus, one mid-phase
owner ruling); PHASE COMPLETE

Tasks 1–4 landed first: content order vocabulary (created_at only —
updated_at un-indexed, published_at nullable + composite-indexed only,
both deliberately excluded, the P3 precedent); the six-case family on
Entries List + ListByTerm with both example memstores' `entryPageOf`
extended in the same boundary (storetest/reference_test.go rebuilt to
the full matrix — same forced amendment as P3's); pgx entries on
`pgxdb.List[entryRow]` with the `entryFilter` NamedArgs builder shared
by list+count, `writeFields` → ONE `INSERT…SELECT…FROM
UNNEST(@keys::text[],@kinds,@values)`, `SetTerms` → DELETE + one UNNEST
insert (InTx + MapError preserved); assets/inquiries/menus/terms swept
to NamedArgs + Collect*/queryOne; turso entries semantics-only onto
`turso.List`.

**task-5 tripped the plan's pre-declared STOP:** prev links could not be
threaded additively — the Views port passed pagination as a bare
`nextCursor string`. Owner ruled mid-phase (2026-07-08): **the Views
port grows via a single `Pager` view-model struct**
{NextCursor, HasPrev, PreviousCursor, Order, BaseHref} — breaking once,
additive forever after (Limit deliberately omitted: links carry
cursor+order only, ?limit is a one-shot SSR override). Landed:
`EntriesList(…, pager Pager)` / `Archive(…, pager Pager)`, root
re-export `cms.Pager`, shared `PagerNav` templ ("← Newer" beside
"Older →", empty-PreviousCursor→BaseHref rule, order carried in both
links, invalid order never propagated), handlers (admin: ParseOrder
with Q3 fallback + NormalizedLimit clamp; public: cursor-only with
HasPrev threaded), and the examples/cms ACME theme's Archive override
at the new signature. En passant fix: the old "Older →" href pointed at
`/…/new?cursor=…` — a real pre-existing bug, now built off BaseHref.
`*_templ.go` regenerated via `make generate` (idempotence verified;
regenerated files staged so the drift gate passes pre-commit).

**Live legs (2026-07-08, for NOTES):** pgx — docker postgres:17, run at
task-2 AND task-3: **40 subtests PASS, 0 fail** (12 new family cases =
6 × {List, ByTerm}); container removed, port free. turso — playground
URL byte-verified (hard gate), `-tags=integration`: **ok, 170.6s, 40
subtests PASS, 0 fail**; no tokens in logs.

**Real-interaction (browser, driven by the main session via Playwright,
28 seeded articles in a scratch category):** admin — page 1 (25 items,
Older-only) → click "Older →" → page 2 (3 items) → click "← Newer" →
**exactly page 1** (25 IDs identical); `?order=created_at:asc` → oldest
first, links carry `order=created_at%3Aasc`, asc round-trips both
directions; `?order=nope` → **200**, default DESC, no order in links;
public `/category/…` (through the ACME theme override) — both
directions round-trip exactly, screenshots captured. Curl-level SSR
evidence run by the executor beforehand agreed on every point. Seeded
rows + term deleted after (playground left at 0 articles); server
killed, :8085 free. Main session also fresh-verified: cms module +
both memstores + `make check` (all 30) + `make guard` green,
`grep ListPage features/cms/stores/` → empty, exactly one
`INSERT INTO entry_fields` (the UNNEST). Next: P5
(`05-events-jobs.md`).
