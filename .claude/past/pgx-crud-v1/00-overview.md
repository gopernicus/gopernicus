# pgx-crud-v1 — milestone overview

Status: **RATIFIED 2026-07-08 (jrazmi) — Q1/Q2/Q3 at recommendations; execution-ready, leg 1 = P1.**
Sequencing ruling (jrazmi, same session): **pgx-crud-v1 executes BEFORE
authorization-v1** — its Z2a/Z2b store phases land on this milestone's
standards (the default sequencing below, confirmed).
Milestone: `pgx-crud-v1` — a sweeping pass over the pgx store implementations
and the `sdk/crud` List/pagination standards: pgx v5 used to its full
capabilities (NamedArgs, CollectRows/RowToStructByName, shared NamedArgs
pagination builders, UNNEST bulk writes), and List actually working end to
end — ordering, bidirectional (prev/next) cursor pagination, optional total
counts, and an optional limit/offset mode — landing in sdk first, then
implemented across all four features' pgx stores.

Model of record: **gopernicus-original** at
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original` —
`infrastructure/database/postgres/pgxdb/{cursor.go:11-84, fop.go:27-112,
escape.go}` (the NamedArgs keyset/order/limit builder trio +
QuoteIdentifier), `core/repositories/auth/users/userspgx/generated.go`
(the canonical store shape: NamedArgs, CollectRows+RowToStructByName at
:101, CollectOneRow at :131, `List(ctx, filter, orderBy, page,
forPrevious)` at :58 with reverse-probe slice reversal at :107-111, filter
builders at :358-457 incl. `= ANY(@ids)` slice binding),
`core/repositories/auth/users/generated.go:168-205` (over-fetch +
TrimPage + second forPrevious query + MarkPrevPage), and
`core/repositories/rebac/rebacrelationships/rebacrelationshipspgx/store.go:176-239`
(the UNNEST array-param bulk insert). The whole idiom set in one file:
`workshop/codegen/generators/pgxstore_tmpl.go`. Counts are **net-new** —
the original's `fop.Pagination` has no total.

Executor model policy (jrazmi, standing since jobs-v1): implementation
phases on `model: opus`; design/doc-judgment phases on `model: fable`.
Never sonnet.

## Ratified decisions (owner, 2026-07-08 — not relitigated here)

1. **Scope is pgx-first.** sdk/crud + `integrations/datastores/pgxdb` +
   all four features' `stores/pgx`. The turso connector and turso stores
   are updated ONLY as much as needed to keep compiling and passing the
   shared conformance suites after the sdk/crud changes — that semantic
   update IS in scope; full turso idiom parity is an explicitly-declared
   follow-up milestone (see Out of scope).
2. **Scan mapping:** store-local db-tagged row structs + `toDomain`
   converters per store. Domain entities stay persistence-free — **no db
   tags on `features/*/domain` types.**
3. **API shape:** extend `crud.ListRequest` (Offset / WithCount; cursor
   remains the default mode, setting Offset selects offset mode) and
   `crud.Page` (`Total *int64`). One request/page type; stores branch on
   mode. Exact field names/semantics are pinned in `01-sdk-crud.md`.
4. **Standards land in sdk first**, then are implemented across the
   features.

## Current state (verified at cut)

- `sdk/crud`: `ListRequest{Limit,Cursor,Order}`, `Page{Items,NextCursor,
  HasMore,HasPrev,PreviousCursor}`, type-preserving cursor codec with
  stale-cursor-as-first-page, TrimPage/MarkPrevPage, ParseListRequest vs
  NormalizedLimit (two deliberate limit semantics, package doc).
  **`Order`/`ParseOrder` and `MarkPrevPage`/`HasPrev`/`PreviousCursor`
  have zero effective callers** — `pgxdb.ListPage` ignores `req.Order`
  and hardcodes `created_at DESC`; no reverse-probe query exists anywhere.
- `integrations/datastores/pgxdb.ListPage` (pagination.go): positional
  args with manual placeholder renumbering, hand-scan callback,
  forward-only, `time.Time`-only cursor values. `turso.ListPage` mirrors
  it near line-for-line.
- Feature pgx stores: all positional args, all hand-scan; **zero uses of
  `pgx.NamedArgs` or `pgx.CollectRows` in the repo.** Multi-row writes
  are Exec loops: `features/cms/stores/pgx/entries.go:217-227`
  (writeFields), `:149-161` (SetTerms),
  `features/events/stores/pgx/outbox.go:120-131` (insertRecords).
- Idioms to preserve verbatim: `features/jobs/stores/pgx/queue.go:110-122`
  (FOR UPDATE SKIP LOCKED claim), `schedules.go:130-132` (ClaimDue CAS),
  `features/authentication/stores/pgx/oauth_states.go:44`
  (DELETE…RETURNING consume), `invitations.go:87-93` (UPDATE…RETURNING),
  InTx composition, MapError sentinel mapping.
- HTTP edge: `features/authentication/internal/inbound/http/machine.go:190-197`
  is the only strict list-param parser (4 JSON list endpoints share it);
  cms admin entries + public archive parse cursor only; `ParseOrder` has
  zero feature callers. cms views have "Older →" next-cursor links only
  (`features/cms/views/templ/entries_list.templ:26`, `public.templ:86`).
- Conformance: per-feature `features/<name>/storetest` suites run against
  both dialect stores (env-gated) AND three hermetic memstores in
  `make check` (`examples/minimal/internal/memstore`,
  `examples/auth-cms/internal/memstore`,
  `examples/auth-cms/internal/authmem`). **`features/jobs/memstore` is
  NOT wired into storetest today** (gap, closed in phase 5). events'
  outbox List is plain-`limit` (not crud-paged) — no pagination work
  there.

## Phases

| Phase | File | What | Size | Depends on | Model |
|---|---|---|---|---|---|
| P1 | `01-sdk-crud.md` | sdk/crud standards: ListRequest{+Offset,+WithCount}, Page{+Total}, mode rules, MapPage, ParseListRequest extension, order-vocabulary rule, tests + package doc | M | — | opus |
| P2 | `02-connectors.md` | pgxdb NamedArgs toolkit (QuoteIdentifier, keyset/order/limit builders, generic cursor-value binding) + new `List[T]` helper (order, prev-probe, offset, count, RowToStructByName); turso `List[T]` semantic twin. Additive — legacy `ListPage` stays until P6 | L | P1 | opus |
| P3 | `03-authentication.md` | Pattern-setter: order vocabulary, storetest extension + authmem, pgx store rewrite (full idiom), turso minimal migration, HTTP edge (order/offset/count params + total) | L | P2 | opus |
| P4 | `04-cms.md` | entries List (order/prev/offset/count), writeFields/SetTerms → UNNEST, full pgx idiom sweep, storetest + two example memstores, turso minimal, admin/public prev links (templ) | XL | P3 | opus |
| P5 | `05-events-jobs.md` | events pgx idiom sweep + insertRecords → UNNEST (no pagination work); jobs storetest + memstore (+ NEW hermetic memstore conformance), pgx rewrite preserving Claim/ClaimDue, turso minimal | M | P3 | opus |
| P6 | `06-cleanup-docs.md` | Delete legacy `ListPage` (both connectors), READMEs/package docs, sdk/README, authorization-v1 cross-milestone amendment note, NOTES record | S | P2–P5 | opus (cleanup) / fable (docs) |

Sequencing: P1 → P2 strictly first (standards land in sdk, then the
connectors). P3 before P4/P5 — authentication is the pattern-setter (it
owns the only strict HTTP list edge today; its store has the most
paginated ports). P4 and P5 are independent of each other and may swap.
P6 last — the legacy helpers can only be deleted once every feature phase
has migrated its call sites; every task boundary leaves all 30 modules
building (`make check` green) because P2 is additive.

**Interaction with authorization-v1 (DRAFT):** its `02a-store-turso.md` /
`02b-store-pgx.md` phases cite "keyset `ListPage[T]`" and the D2–D6
helper set. **Those store phases should execute against THIS milestone's
new standards** — default sequencing is pgx-crud-v1 P2 before
authorization-v1 Z2a/Z2b. P6 carries the dated cross-milestone amendment
note onto the authorization-v1 files. If jrazmi orders authorization-v1
first, its stores land on legacy `ListPage` and get swept in a follow-up
task appended here — flag at ratification.

## Module / API impact

- **No new modules; 30 stands.** No go.work or Makefile MODULES changes.
- **Breaking exported-API changes, deliberate:** `sdk/crud`
  (`ListRequest`/`Page` gain fields — additive; `ParseListRequest`
  signature extended — breaking, one caller in-repo; possible `ParseOrder`
  doc-semantics tightening), `integrations/datastores/pgxdb` (new
  `List`/builders; `ListPage` deleted at P6), `integrations/datastores/turso`
  (same shape). **Zero tags exist** (RELEASING.md) — no version-bump
  obligation; this is the window to break `sdk/crud`'s host-facing API
  before v0.1.0.
- `sdk/crud` stays stdlib-only (guard-sdk-stdlib; the sdk go.mod has no
  require block) — nothing pgx-specific leaks into sdk: the builders,
  NamedArgs, and RowToStructByName live in the pgxdb module.
- Querier decision (pinned in P2): **no SendBatch/Begin added** — nothing
  in the target idiom set needs pgx.Batch (the original never used it in
  production; UNNEST is the bulk-write rail). Recorded in the pgxdb README.

## Schema / datastore impact

- **Zero migrations, zero schema changes.** Ordering/pagination/counts
  are query-shape changes only; the EAV spine is untouched.
- Order-field allow-lists are restricted to existing spine columns —
  every paginated port starts at `created_at` (the current pinned order)
  and may add columns that are already indexed; **EAV `entry_fields` are
  never sortable** (ARCHITECTURE.md: a custom field needing SQL sort has
  outgrown EAV). Any wanted-but-unindexed order column is logged as a
  follow-up index migration, never added silently here.
- `WithCount` issues one extra `COUNT(*)` sharing the list's WHERE
  fragment (via subquery wrap in the shared helper / shared filter-builder
  funcs in hand-rolled queries) — opt-in per request, so no cost when off.
- Store-twin parity (turso + pgx + memstores) is proven by the extended
  per-feature storetest suites, not asserted (DP1).

## Generated-artifact impact

P4 only: `features/cms/views/templ/entries_list.templ` and `public.templ`
gain prev-page links; regenerate with `make generate` — never hand-edit
`*_templ.go`. `make check`'s templ-drift gate runs every phase.

## Live-store gates (P3–P5)

pgx legs env-gated on `POSTGRES_TEST_DSN`
(`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`);
turso legs `-tags=integration` + `TURSO_*` — the ONLY authorized database
is `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
(**verify the env URL matches before ANY run**). Loud skips mid-milestone
are fine; milestone close requires one recorded live run of every extended
conformance suite per dialect (`make test-stores`), as dated NOTES.md
artifacts — never a hermetic green. Tokens never enter CI logs.

## Goal

`crud.ListRequest{Order, Cursor/Offset, WithCount}` round-trips end to
end — orderable, bidirectionally pageable, optionally counted — through
every paginated port on pgx (idiomatic pgx v5 throughout), with turso and
the memstores provably matching via the extended conformance suites, and
the params wired through every HTTP surface that already exposes a List
endpoint.

## Definition of Done (milestone)

- sdk/crud ships the pinned request/page semantics (P1 spec) with unit
  tests for every mode/edge; package doc's two-semantics rule extended to
  the new fields; sdk still builds with an empty go.mod require block.
- `pgxdb.List` + builders: dynamic order fields (allow-listed +
  QuoteIdentifier), tuple-comparison keyset predicates via NamedArgs,
  reverse-probe prev pages, offset mode, counts, generic cursor
  order-value types (time/int/string at minimum) — hermetic unit tests
  green; `turso.List` semantically equivalent.
- All four features' pgx stores rewritten to the idiom set (NamedArgs
  everywhere, CollectRows/CollectOneRow + db-tagged row structs +
  toDomain, filter builders, UNNEST where Exec loops existed), with the
  named preserved idioms intact (SKIP LOCKED, CAS, DELETE…RETURNING,
  UPDATE…RETURNING, InTx, MapError).
- Extended storetest case families (order, prev-page, offset mode,
  counts, stale-cursor-on-order-change, cursor+offset rejection) green
  hermetically against all wired memstores in `make check` AND live
  against both dialects (`make test-stores`), recorded in NOTES.md.
- `crud.ParseOrder` and `crud.MarkPrevPage` have real callers; the
  authentication JSON list endpoints accept `order`/`offset`/`count` and
  return `total`/`has_prev`/`previous_cursor`, verified over live HTTP;
  cms admin + public archive render working prev links, verified in a
  browser.
- Legacy `ListPage` deleted from both connectors, zero callers by grep;
  docs synced; `make check` + `make guard` green at 30 modules.

## Out of scope

- **Turso idiom parity** (named-parameter equivalents, struct scanning,
  builder ergonomics) — declared follow-up milestone (working slug
  `turso-crud-parity`); turso gets exactly the semantics the extended
  conformance suites demand, nothing more.
- No new endpoints, no new routes. cms's unpaginated ports
  (terms/menus/media/inquiries) stay unpaginated; events'
  `ListUnpublished(ctx, limit)` stays a plain-limit outbox drain (it is
  not a user-facing list). No users List port invented.
- No pgx.Batch/SendBatch adoption; no Querier surface growth.
- No migrations, no new indexes (wanted indexes logged as follow-ups).
- No db tags or order metadata on `features/*/domain` entity types.
- No search/filter vocabulary expansion (the original's SearchTerm/ILIKE
  builders are cited as shape, not adopted as new ports).
- authorization-v1 execution (flagged for sequencing only).

## Risks (ordered)

1. **Lockstep conformance breakage.** Extending a feature's storetest
   instantly breaks its hermetic memstore suites in `make check` (three
   example-host memstores) and both dialect stores. Mitigation: per-feature
   phasing — each phase lands storetest cases + every backend of that
   feature inside one phase, with the storetest+memstore edit in a single
   task boundary; P2 is additive so nothing breaks repo-wide.
2. **Rewrite regressions of load-bearing SQL.** The jobs Claim
   (SKIP LOCKED) and ClaimDue (CAS) and the auth consume/RETURNING idioms
   are concurrency-correctness code. Mitigation: pinned preserve-verbatim
   lists per task; live conformance is the per-phase acceptance, not
   hermetic green.
3. **Dynamic order-field SQL injection surface.** Order columns are now
   interpolated identifiers. Mitigation: per-aggregate allow-lists +
   `QuoteIdentifier` (pgx) / allow-list membership (turso, memstores);
   P2 ships rejection tests; raw `req.Order.Field` never reaches SQL.
4. **Cross-backend semantic divergence** (order+tiebreak+cursor
   interaction, prev-probe windows, offset arithmetic). Mitigation: the
   conformance suites are the contract — every new case runs against
   memstore, turso, and pgx; the cursor codec's stale-order rule is
   asserted, not assumed.

## Open questions — RATIFIED 2026-07-08 (jrazmi, all at recommendations)

**Q1 = feature-core domain packages. Q2 = INCLUDE the cms prev-link view
task. Q3 = SSR fallback-to-default on a bad order param.** The original
question text is retained below for the record.

1. **Q1 — where the order allow-list lives.** Recommend: each paginated
   aggregate declares `map[string]crud.OrderField` + a default
   `crud.Order` in its feature-core domain package (the original's
   `users.OrderByFields` precedent), with names that coincide with column
   names; stores validate before interpolation. The strict alternative —
   core declares only abstract field names, each store owns a
   name→column remap — keeps column strings out of the core entirely but
   adds a mapping layer to six backends per feature. The recommended form
   does NOT violate the no-db-tags decision (an allow-list is query
   vocabulary, not entity persistence metadata), but it's a judgment call.
2. **Q2 — cms prev-link view work (P4 task-5).** Recommend INCLUDE: it is
   the only human-visible proof that bidirectional paging works, and it's
   small (two .templ files + view-model fields + regenerate). Trim if you
   want this milestone strictly store/API-side; the JSON proof (auth
   endpoints) stands alone.
3. **Q3 — SSR order/limit param error behavior.** JSON edges use the
   strict parser (400 on bad input). For cms SSR list pages, recommend
   fallback-to-default on a bad `order` param (clamp semantics, matching
   NormalizedLimit's store-edge philosophy) rather than a 400 page.

## Consultation notes

No lead consulted at cut — per the owner's cutting brief ("jrazmi will
run reviews on the plan file after it's cut as DRAFT"). Load-bearing
verifications were done directly: the gopernicus-original reference files
(cursor.go/fop.go/escape.go/userspgx/rebac UNNEST) read at the cited
lines; the repo's ListPage twins, all ListPage call sites, the Exec-loop
bulk writes, the conformance/memstore wiring (incl. the jobs-memstore
coverage gap), the HTTP list surfaces, and the Makefile guard/test-stores
structure surveyed against the current tree.

## Recommended reviews (before ratification)

- **lead-backend-engineer** — the P1 request/page semantics (mode
  exclusivity, offset+count interaction, MapPage), the P2 `ListQuery[T]`
  helper shape, Q1.
- **data-integration-reviewer** — keyset/tiebreak/prev-probe correctness,
  offset-vs-cursor traversal equivalence cases, count-WHERE reuse, UNNEST
  ordering semantics (outbox), turso semantic-parity minimalism.
- **architecture-steward** — Q1 (order vocabulary placement vs the
  persistence-free-domain rule), sdk connector-neutrality of the P1
  additions.
- **platform-sre** — breaking-API-with-zero-tags posture, live-leg
  discipline, `make test-stores` coverage after the storetest extensions.
- **lead-frontend-engineer** — P4 task-5 only (templ prev links,
  view-model growth, theme-override check).
- **product-manager** — scope: Q2/Q3, the per-feature phasing, whether
  the follow-up milestones (turso parity, index follow-ups) are named
  crisply enough to stay deferred.

## Execution log

### 2026-07-08 — planning leg: milestone cut (DRAFT)

Cut `00-overview.md` + phases P1–P6 per the owner's 2026-07-08 direction
(pgx-first scope, row-struct scan mapping, one request/page type — all
ratified pre-cut). Reference patterns verified against gopernicus-original
at the cited lines; repo survey recorded above (notably: events has no
crud-paged List; jobs memstore is outside storetest; three example-host
memstores are inside `make check`). No code touched. Next: review gate
(recommended reviewers above), jrazmi ratification (Q1–Q3), then leg 1 =
P1 (`01-sdk-crud.md`, opus).

### 2026-07-08 — RATIFIED (jrazmi)

Ratified same day at all recommendations: **Q1** order allow-lists live
in feature-core domain packages (column-coincident names; stores validate
before interpolation); **Q2** the cms prev-link view task stays IN
(P4 task-5 — the human-visible bidirectional-paging proof); **Q3** SSR
list pages fall back to the default order on a bad `order` param (JSON
edges keep the strict 400). Owner skipped the optional pre-ratification
review panel — the reviewers remain available per-phase if wanted.
Sequencing ruling recorded in the header: this milestone executes before
authorization-v1 (whose codex-review fold landed the same session).
NOTES.md entry same date. Next: leg 1 = P1 (`01-sdk-crud.md`, opus).
