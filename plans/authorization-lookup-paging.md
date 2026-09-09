# authorization: the two enumeration paths — a paged prefilter (`LookupResourcesIn.After`) and a postfilter page-filler (`FilterPage`) (#29, the deferred half of #22)

**Status:** RELEASED 2026-09-08 — PR #42 squash @ `162d24e` → `pockets/authorization/v0.10.0`;
PR #45 (the reopened #43) squash @ `4bd2363` → `pockets/authorization/v0.11.0`; store repins
@ `a78aab2` → `stores/pgx/v0.6.0` + `stores/turso/v0.5.0`; all four tags served by the proxy on
the first poll and cold-verified `GOWORK=off`. RATIFIED 2026-09-08 (owner: "can you execute";
R1–R5 as recommended; "release it" unblocked the merges). See Execution record. Origin:
segovia v2 tenancy plan, open item
O6 ("home enumeration and pagination budget — BLOCKS D10"). #22 shipped the Limit half as
`LookupResourcesIn` + `LookupResult.Truncated` in v0.7.0 and deferred the cursor to #29 "until a
host actually needs continuation". segovia v2's home ("shared with me" = one lookup per item
type, D10) is that host, and D14 ("public to the tenant" is one userset tuple) is what makes the
list large: a member of a big tenant enumerates every item in every open space.

## Context

There are two ways a ReBAC-backed application lists "the things this principal may see", and
this repository has used both under its own names since the monolith:

- **Prefilter** — ask the engine first (`LookupResources`), then read rows `WHERE id = ANY(@ids)`.
  The house default for every "everything I can access" list in segovia v1 (dashboards, spaces,
  tenants, space contents), and the monolith generator's `pattern: prefilter`. One engine call
  replaces N checks; the risk, recorded honestly in v1's own plan, was "(a) the evaluation budget
  ... (c) `id = ANY(@ids)` gets unpleasant in the thousands". Wins when access is SPARSE relative
  to the table: a guest, a cross-tenant "mine" list, home.
- **Postfilter** — read candidate rows first (ordered, paged, searchable by the database), then
  `FilterAuthorized` them and keep reading until the page fills. The monolith's
  `fopb.PostfilterLoop` (2× overfetch, "when most records are authorized"), the generator's
  `pattern: postfilter`, and segovia v1's breadcrumb / favorites / recent-access re-checks. Wins
  when access is DENSE within a set the database already bounds: a container's contents, the
  caller's own starred rows, a search result. The pockets rewrite deliberately did NOT carry it
  (README §2.6 demand gate: "a future enumeration-shaped consumer seam must ship paired with it").

Today's relationship engine (v0.9.0) is a **reverse walk with per-node caps**:
`lookupResources` recurses over
`(resourceType, permission)` through the schema's `Through` hops (memoized), resolves each leaf
with a bounded store scan (`LookupResourceIDs` / `LookupResourceIDsByRelationTarget` /
`LookupDescendantResourceIDs`, each fetching `MaxLookupResults+1`, sorted, distinct), merges and
dedups in `add()`, and sorts each node before memoizing. `MaxLookupResults` (default 1000) is
charged per node; overflow is `ErrEvaluationLimit`, never a short list. `LookupResourcesIn`
truncates that full result in memory: "Limit does NOT reduce enumeration cost". `FilterAuthorized`
is `CheckBatch` (≤ `MaxBatchSize`), which runs one SQL per direct relation over the whole batch
when the permission has no `Through`, and N sequential `Check`s otherwise. The roles kind uses
`crud.ListRequest{Cursor}` internally to scan assignments, but still accumulates and sorts the
complete resource-id result before returning. That cursor is not a resource-id continuation and
does not make the decision surface pageable; Path A therefore needs a role-specific lookup port
as well as the relationship-store changes below.

The cliff, stated precisely: the cap is a work budget, not a product limit, and the failure is the
right one (indeterminate → error, never a silently short list). What is missing is CONTINUATION.
Without it, the users with the MOST access — admins, staff, members of large open tenants — are
the first to get a 503 from home.

## Goal

Two additive enumeration paths, each the right tool for one access shape, released in the ordered
`pockets/authorization` trains selected by R4 with their affected stores:

- **Path A — paged prefilter.** `LookupResourcesIn` honors `LookupRequest.After` and returns
  `LookupResult{IDs, HasMore, NextCursor}` in a stable total order. Top-level non-hierarchy leaf
  reads are bounded by the page size rather than by the total number of reachable resources;
  intermediate sets and self-hierarchy closure keep the explicit bounds/exceptions below.
  Check/Lookup parity holds over the UNION of pages.
- **Path B — postfilter page-filler.** `FilterPage`: a generic, budget-bounded loop that pulls
  candidate pages from a host-supplied source, `FilterAuthorized`s them, and fills one authorized
  page with a continuation the host can hand back — the monolith's `PostfilterLoop`, owned by the
  engine this time so the overfetch, the batch bound, and the scan bound are one contract.

And a documented rule for choosing: sparse ⇒ A, dense-and-bounded ⇒ B; hosts may use both on one
resource type.

## Out of scope

- A materialized access index (Zanzibar's Leopard, an `accessible_resources` table kept by
  triggers or the outbox). It is the third way and the only one that makes BOTH shapes cheap, but
  it is a write-path change with its own consistency contract; this train makes the two on-demand
  paths correct and pageable first. Recorded as a follow-up marker, not planned here.
- Paging INTERMEDIATE sets or self-hierarchy ROOT sets. In
  `dashboard.view = owner | Through(space, view)`, the set of spaces the principal may view is an
  intermediate node; Path A pages the TOP-LEVEL result and keeps
  intermediate nodes budget-bounded (`MaxLookupResults` per node, as today). A principal who can
  view more than the budget's worth of spaces still gets `ErrEvaluationLimit`; that cliff is an
  order of magnitude further away than the item cliff (see Rulings R1 for the knob). For a
  top-level permission with a same-permission self hierarchy, the complete non-descendant root
  set is likewise bounded by `MaxLookupResults` before it seeds the closure. Paging that seed set
  cannot preserve global id order: a root `z` may grant descendant `a`. Paging the recursive
  closure's work itself is explicitly deferred; its result is keyset-paged only after the closure.
- Snapshot isolation across pages. A keyset cursor sees grants that land ahead of it and misses
  ones that land behind it, the standard keyset contract; the client refetches from the start to
  see a consistent view (segovia's home does this on window focus).
- Sorting by anything but resource id. Application ordering (by name, by date) belongs to Path B,
  where the database orders.
- Changing `LookupResources` (the plain method) in any way. It stays the complete, sorted,
  budget-bounded enumeration with Check/Lookup parity; `LookupResourcesIn` is the paged surface.

## Design

### Path A — `LookupResourcesIn` with `After`

**A1 — the order is resource id ascending, byte order, and the cursor is the last id returned.**
The engine already returns every node sorted ascending with each id exactly once; the union of
pages keeps that order globally. `NextCursor` encodes `{v: 1, last_id, fingerprint}` where the
fingerprint is a digest of `(principal, permission, resourceType, owning kind, owning model
digest)`, base64url; a cursor presented against a different query, a different owning kind, or a
changed model is `ErrInvalidCursor` (`sdk.ErrInvalidInput`). The relationship kind uses the
compiled schema digest it already has. The compiled role model gains the same deterministic
digest property so a role-owned cursor has an equivalent invalidation contract. The fingerprint
is query binding, not authentication: `last_id` remains untrusted opaque client input and is
validated like any resource id. No page numbers, no offsets.

**A2 — keyset pushdown to the leaves, k-way merge at the node.** Every leaf store scan gains an
`after` argument: `LookupResourceIDs(…, after, limit)`, `LookupResourceIDsByRelationTarget(…, after,
limit)`, `LookupDescendantResourceIDs(…, relations, …, after, limit)` — `WHERE resource_id >
@after ORDER BY resource_id LIMIT @limit` on the outer result. The top-level node opens one sorted
stream per direct relation/check, one `ByRelationTarget` stream per non-self `Through` hop, and at
most one descendant-closure stream, then k-way merges by id, dedups adjacent equal ids, and emits
until `Limit` ids are produced or every stream is exhausted. Each source fetches `Limit+1` rows
after the page's global `after`; the extra row is lookahead for `HasMore`. A source need not refill
within the same output page: `Limit+1` distinct rows from every source are sufficient to produce
`Limit` distinct union rows plus lookahead. `HasMore` is true only when a buffered lookahead or an
unexhausted source remains after the emitted id. Apart from the intermediate and closure
exceptions in A3, store rows read per page are `O(streams × Limit)`.

**A3 — intermediate nodes stay bounded; hierarchy seeds must be complete.** A non-self `Through`
hop's TARGET set (the spaces the principal may view, to feed `dashboard#space`) is computed by the
existing memoized `lookupResources` with the existing per-node budget, once per page (memo per
request). This removes the item-count cliff and leaves the container-count cliff at
`MaxLookupResults` containers per principal.

For a permission with one or more same-permission self `Through` relations, first compute the
complete non-descendant ROOT union under the same `MaxLookupResults` bound. It cannot be paged
because root id order does not constrain descendant id order. Feed those roots and ALL self
relations to one closure stream. `LookupDescendantResourceIDs` therefore takes `relations
[]string`; the recursive query follows their union so paths that alternate relations are complete
without the engine's current multi-call fixpoint. Its outer `after`/`limit` pages the sorted result,
but the database still computes the closure on each page. An overflowing root set is
`ErrEvaluationLimit`. These are explicit v1 paging boundaries; R1 decides the default budget.

**A3b — the roles owner gets a real resource-id lookup.** Add
`role.Storer.LookupResourceIDsBySubjectAndRoles(ctx, subjectType, subjectID, resourceType string,
roles []string, after string, limit int) (ids []string, unrestricted bool, err error)`. It first
detects a matching global assignment, else returns sorted, distinct scoped `resource_id` values
after `after`, capped at `limit`. The roles engine passes the compiled grantor roles and therefore
pages directly in the same public order. It no longer treats the created-at cursor used by
`ListBySubject` as a resource-id stream. `Unrestricted` terminates the query with empty IDs and no
continuation, exactly as today.

**A4 — budget semantics restated.** `MaxLookupResults` bounds (i) every intermediate node, as
today, and (ii) the page size: `Limit == 0` means `MaxLookupResults`, `Limit > MaxLookupResults`
is `ErrInvalidInput`. It is never a total-results cap on a paged query. `Truncated` is retired in
favor of `HasMore` (kept as a deprecated alias set to the same value for one release). A
role-owned paged query no longer scans every assignment or charges `MaxGraphStates` for irrelevant
assignments; its indexed store lookup is bounded by page size. Plain `LookupResources` retains
its current full-scan and budget semantics.

**A5 — parity over the union.** `storetest` gains `LookupPagedParity`: for a fixture universe,
walk `LookupResourcesIn` with `Limit` ∈ {1, 2, 7, universe} until `HasMore` is false; the
concatenation must equal the plain `LookupResources` result exactly (same ids, same order, no
repeats) and every id must pass `Check`. A second case pins that a cursor from one principal is
refused for another, and that an overflowing INTERMEDIATE node still returns `ErrEvaluationLimit`
on every page. Hierarchy fixtures deliberately put a descendant lexically before its root and
alternate two self relations on one path. Role fixtures cover scoped paging, duplicate resource
ids through two granting roles, a global grant, model-digest invalidation, and pair-owner dispatch.

**A6 — pgx indexes and migration mechanics.** `idx_iam_relationships_type_relation
(resource_type, relation)` does not carry `resource_id`, so a keyset predicate would sort instead
of range-scan. The store ledger gains `0005_iam_lookup_keyset.sql` with an ordinary transactional
`CREATE INDEX IF NOT EXISTS`: `pgxdb.RunMigrations` applies the entire stream in one transaction,
so `CREATE INDEX CONCURRENTLY` is invalid in this repository's migration path. Start with
`idx_iam_relationships_type_relation_resource ON iam_relationships (resource_type, relation,
resource_id)` and a role lookup index ordered for its actual predicate; add covering columns or a
second index only if task 4's measured plans require them. The upgrade note calls out the ordinary
index build's lock duration so hosts can schedule the migration. Hosts pin the ledger verbatim
(segovia v2's `ledger_test`), so this is a host-visible migration on upgrade. The descendant CTE
keeps `UNION` dedup recursion across all self relations and applies `after`/`limit` on the outer
select; it is the stream whose per-page cost remains the closure size (A3).

**A7 — turso and memstore.** memstore: filter after + sort + slice. turso: the equivalent SQL
contract and the same index inventory in its ledger.

### Path B — `FilterPage`

**B1 — the seam.** In the core module (no store change):

```go
// Candidate is one host row plus the source-compatible cursor immediately after it.
// A cursor per row lets FilterPage stop anywhere in an over-fetched source page.
type Candidate[T any] struct {
    Item       T
    NextCursor string
}

type CandidatePage[T any] struct {
    Items   []Candidate[T]
    HasMore bool
}

// CandidateSource yields candidates in the host's stable order after cursor.
type CandidateSource[T any] func(ctx context.Context, cursor string, limit int) (CandidatePage[T], error)

type FilterPageRequest[T any] struct {
    Principal    PrincipalRef
    Permission   string
    ResourceType string
    ID           func(T) string        // the resource id of a row
    Source       CandidateSource[T]
    Limit        int                   // page size wanted; 0 = crud.DefaultLimit
    Cursor       string                // the continuation FilterPage last returned
}

func FilterPage[T any](ctx context.Context, s *Service, req FilterPageRequest[T]) (crud.Page[T], error)
```

`Limit == 0` resolves to `crud.DefaultLimit`; a negative limit or an effective limit above
`MaxBatchSize` is `sdk.ErrInvalidInput`. The source must return at most the requested candidate
count. An empty page with `HasMore: false` is normal exhaustion; an empty page with `HasMore: true`
cannot advance and is invalid.

**B2 — the loop.** Pull `min(2×Limit, MaxBatchSize, MaxFilterScan-scanned)` candidates from
`Source` at `Cursor`; `FilterAuthorized` their ids in one call; append allowed rows in source
order until `Limit` is reached. The continuation is always the `NextCursor` of the last candidate
consumed, including a mid-batch stop. Per-item cursors are required: a source that returns only a
whole-page cursor cannot both preserve the unconsumed suffix and avoid repeating the consumed
prefix. Reject a non-empty page whose first/last cursor is empty or whose final cursor equals the
input cursor as `sdk.ErrInvalidInput`, rather than spinning. In fact every candidate cursor must
be non-empty and differ from the cursor immediately before it; track cursors seen during one
`FilterPage` call and reject a cycle. Continue pulling while the output is short, the source has
more, and scan budget remains.

**B3 — the scan bound.** `MaxFilterScan` (a new `EvaluationLimits` field, default 20,000 = 20 ×
`DefaultMaxBatchSize` candidates per call): every source request is clamped to the remaining scan
budget, so one call never overshoots it. When sparse access reaches the bound without filling the page, return the
partial page and the cursor after the last scanned candidate. `HasMore` is true only if the
current source page has an unconsumed suffix or the source reported more candidates; if the
source is exhausted exactly at the bound it is false. Here `HasMore` means the continuation has
unscanned candidates, not that another authorized row is guaranteed, so following any response
with `HasMore` may produce a final empty page when the remaining candidates are all denied. This
is the property the monolith's loop lacked and the reason B is safe to expose as an engine seam.

**B4 — share successful store reads, not decisions or global lookup sets.** `FilterAuthorized`
over a `Through` permission is N sequential `Check`s today. Run those same checks with the same
fresh budget per candidate, but wrap the batch's `PermissionReader` in a request-local memo that
caches successful immutable results for exact read arguments (`GetRelationTargets`, direct
relation checks, and any other reader call the evaluator makes). Repeated candidates in one
container still read their distinct candidate targets, while repeated evaluation of the shared
target reuses its store reads. Copy cached slices/maps on entry or return so callers cannot mutate
memo state; never cache errors or context cancellation. This preserves graph/fanout/depth charging
and therefore sequential-`Check` semantics. Do not call `lookupResources` to build a global target
set: that would make a small bounded container listing fail when the principal's total
accessible-container set exceeds `MaxLookupResults`. No public API change; correctness tests and a
benchmark pin the optimization.

**B5 — what B is for, in the README.** A container's contents (a space's dashboards, a tenant's
root spaces), a search result, the caller's own rows (favorites, recents): anything the database
already bounds and orders. Not for "everything of type X I may see" — that is A, and the README
says why in one table (sparse vs dense, who orders, what the cost scales with).

### Choosing, and how segovia v2 consumes both

| list | shape | path |
|---|---|---|
| home: "shared with me" per item type (D10) | sparse, cross-container, engine order is fine | A, one `LookupResourcesIn` per type, cursor per section |
| `GET /tenants`, `GET /spaces`, `GET /dashboards` (the flat "mine" lists) | sparse | A |
| a space's children / dashboards, a tenant's roots | dense, bounded by the container, host order (name) | B over the store's `BySpace` / `Children` / `Roots` as a keyset source |
| breadcrumb navigability, item rosters | tiny, already fetched | plain `FilterAuthorized` / `Check`, as today |

**Amended 2026-09-08 (`authorization-batch-decision`, task 4).** The container
row above was written when a `FilterAuthorized` cost N × (branches + hops) round
trips, which is what made a sparse container page unusable (segovia v2 O13: 12
allowed of 300 candidates, ~18 s) and moved those listings to prefilter. That
cost is gone — one `FilterAuthorized` is now ONE set evaluation, `O(branches +
hops)` reads for the whole candidate set — so the rule is about which SET is
smaller, not about which path is safe:

| list | rule |
|---|---|
| a container listing where the principal's visible set of that type is BOUNDED (the ordinary member of a few tenants/spaces) | **PREFILTER** — v1's rule, and what segovia v2 shipped in O13. One `LookupResourcesIn` and the host's own `WHERE id = ANY(...)` beats scanning candidates. |
| a host-ordered candidate stream whose visible set is NOT bounded — a manager inside a huge tenant, a search result, "my own rows" across containers | **B (`FilterPage`)**, which is the case this plan reserved it for and which the set decision now makes honest. |

## Tasks

| # | task | where | done when |
|---|---|---|---|
| 1 | **Port and cursor contract.** The three relationship `Lookup*` methods gain `after`; descendant lookup accepts all self relations. `role.Storer` gains `LookupResourceIDsBySubjectAndRoles` from A3b. Document sorted/distinct output, exclusive `after`, and `limit` rows max. Add `LookupRequest.After`, `LookupResult.HasMore` + `NextCursor` (+ deprecated `Truncated` alias), `ErrInvalidCursor`, owner/model-bound cursor codec, and a deterministic role-model digest. Rewrite the README section and remove the deferred-#22 text. | `domain/{relationship,role}`, `internal/logic/{authorizersvc,decisionsvc}`, `README.md` | builds; both memstore ports compile and cursor binding tests are green |
| 2 | **Engine: A2–A4.** Add relationship leaf streams, complete bounded hierarchy-root collection, one union-relation closure stream, k-way merge/dedup, and `Limit`/`HasMore`. Add the role-owned resource-id page path from A3b. `Composite.LookupResourcesIn` validates once and dispatches to the single owning kind exactly as `Check`/`LookupResources` do; it never merges kinds. Keep plain `LookupResources` unchanged. | `internal/logic/authorizersvc/lookup.go`, `internal/logic/decisionsvc/{composite,roles}.go` | pocket `go test ./...` green |
| 3 | **Conformance: A5.** Add store keyset contract cases plus engine `LookupPagedParity`, cursor-refusal/model-change, hierarchy-order/interleaving, role-owned, global-role, and intermediate/root-overflow cases; register store cases for memstore, pgx, and turso. | `storetest/`, engine tests, each store's `conformance_test.go` | memstore and pocket tests green |
| 4 | **pgx + turso: A2, A3b, A6, A7.** Add `after` to relationship lookup SQL, union-relation descendant closure, and the role lookup query. Add transactional `0005_iam_lookup_keyset.sql` to both ledgers. Measure all lookup shapes at 1e6 rows; assert useful index range scans where the query permits one and record closure cost separately. `stores/UPGRADE.md` names the migration, lock behavior, and host re-export. | `stores/{pgx,turso}/{relationships,roles}.go`, both `migrations/0005_*.sql` | live pgx conformance green; explain assertions prove the selected indexes; turso integration green |
| 5 | **Path B: B1–B3.** Add `Candidate`, `CandidatePage`, `CandidateSource`, `FilterPageRequest`, `FilterPage`, and `EvaluationLimits.MaxFilterScan` (+ default, validation). Tests cover dense, sparse scan-bound, exact-bound exhaustion, mid-batch fill without skips/repeats, source over-return, malformed/non-advancing/cyclic cursors, valid empty exhaustion, invalid empty-with-more, cancellation, and oversize `Limit`. | `filter_page.go`, `internal/logic/authorizersvc/limits.go`, tests | green; README §"Enumerating" gains the choosing table and the candidate-cursor contract |
| 6 | **B4: memoized batch reader.** Run every homogeneous relationship-owned batch item through the ordinary evaluator with its own fresh budget, backed by a batch-local reader that memoizes successful exact store reads and never invokes global lookup. Add result/error parity tests against sequential `Check`, including graph/fanout limits and a principal over the global lookup-result budget. Benchmark a 1-container / 500-item fixture. | `internal/logic/authorizersvc/service.go` + `_test.go` | parity green; benchmark observes one shared-target store evaluation plus the distinct candidate target reads |
| 7 | **Release trains.** Under the recommended R4 ruling, ship tasks 5–6 as core `v0.10.0`, then tasks 1–4 as core `v0.11.0` plus `stores/pgx/v0.6.0` and `stores/turso/v0.5.0`. For each: proxy poll, cold `GOWORK=off` verification, RELEASING entry, and plan execution record; copy the final plan to `plans/` and close #29 after Path A tags resolve. | `RELEASING.md`, `stores/*/go.mod`, `plans/` | every affected module builds/tests with `GOWORK=off` against resolved tags |

Ordering: 5 and 6 are core-only and land first (they unblock segovia's container listings and give
O6 a working fallback); 1–4 are the multi-module train #22 priced. Under the recommended R4
ruling, task 7 tags the first release before the second train begins, then tags the second after
all store ports and migrations are available.

## Risks

- **The intermediate-set cliff remains** (A3). A principal who can view more containers than the
  per-node budget still gets `ErrEvaluationLimit` on home. Mitigation now: R1's default; a later
  train pages the descendant walk or lands the access index.
- **A hierarchy-bearing top-level pair also needs a complete bounded root set.** This is required
  for global id order because a lexically late root can grant a lexically early descendant. More
  than `MaxLookupResults` non-descendant roots is `ErrEvaluationLimit`; the access-index follow-up
  is the structural fix.
- **Keyset order is by id, which is opaque and random** (nanoid). Pages are stable but
  meaningless to a human; any user-facing order is Path B's or the client's after fetch. Stated in
  the README so nobody sorts a paged prefilter client-side and expects consistency across pages.
- **A `Through` hop with a very large target set makes every page pay `ByRelationTarget` over
  `ANY(@target_ids)`** for the whole set. It is bounded by the per-node budget, but no single index
  order is automatically optimal for both target filtering and global resource-id order. Task 4
  measures the real plans before freezing the index inventory.
- **Host ledgers pin store migrations verbatim.** The `0005` index is a required re-export in
  every host on upgrade (segovia v2's `ledger_test` fails until it is copied). Called out in
  `stores/UPGRADE.md`; because the runner is transactional, PostgreSQL uses ordinary `CREATE
  INDEX` and hosts schedule the lock-bearing migration deliberately.
- **B requires a cursor at every candidate boundary.** Host adapters must expose the same keyset
  cursor their store would use after each row. This is more work than returning only a page-end
  cursor, but it is the state a stateless helper needs to stop mid-batch without skips or repeats.

## Rulings needed (owner)

- **R1 — the intermediate budget.** Keep `MaxLookupResults` at 1000 for intermediate nodes, or
  raise the default to 5000 now that the top level pages? Recommendation: keep 1000 in the engine
  default, document that a host with large open tenants sets `Limits.MaxLookupResults` itself
  (segovia v2 sets 5000 in leg 6), and let the access-index follow-up be the structural fix.
- **R2 — cursor invalidation on model change.** Bind the cursor to the owning model digest — the
  relationship schema digest or a new deterministic role-model digest (a deploy that changes the
  owner/model invalidates in-flight cursors → the client restarts from page one), or tolerate and
  accept a possibly inconsistent union. Recommendation: bind both kinds symmetrically.
- **R3 — B's home.** In `pockets/authorization` (needs the `Service`) as planned, or as an sdk
  `crud` helper taking a filter func. Recommendation: the pocket; it owns `MaxBatchSize` and the
  memo in B4.
- **R4 — one train or two.** Ship 5+6 (core-only) as v0.10.0 first and 1–4 as v0.11.0 with the
  stores, or hold everything for one v0.10.0. Recommendation: two, in that order; segovia takes
  B immediately and A when it lands, and neither blocks the other.
- **R5 — retire `Truncated` now or after one release.** Recommendation: alias for one release.

## Consumers

- segovia v2 leg 6 (home, D10a): A per item type with `Limit: 50`, cursor per section; the
  `MaxLookupResults` host override per R1; the overflow case added to its verify list.
- segovia v2 container listings (`/spaces/{id}/spaces`, `/spaces/{id}/dashboards`,
  `/tenants/{id}/spaces`): B over the tenancy and dashboards stores' keyset queries — replacing
  today's unbounded `FilterAuthorized` over the whole container.
- gps-360-go `/client-hubs` (the #22 origin): A replaces the host-side cap of 50.
- Coordination Hub `myCampaigns`: A, once it moves onto v0.11+ under recommended R4.

## Verify (when built)

```
# pocket, hermetic
cd pockets/authorization
go build ./...
go test ./...
go vet ./...
go test -bench BenchmarkCheckBatchThrough -run ^$ ./internal/logic/authorizersvc/
# pgx, live (disposable postgres:17): conformance incl. LookupPagedParity, and the EXPLAIN check
POSTGRES_TEST_DSN=... go test ./stores/pgx/... -run 'Conformance|Explain'
# turso, live
go test -tags=integration ./stores/turso/...
# cold verify after tagging
GOMODCACHE=$(mktemp -d) GOWORK=off go build ./... # per module, against pinned tags
GOMODCACHE=$(mktemp -d) GOWORK=off go test ./...  # per module, against pinned tags
GOMODCACHE=$(mktemp -d) GOWORK=off go vet ./...   # per module, against pinned tags
```

A host-shaped proof, in segovia v2's `topology_live_test.go` family: a tenant with one open
space holding 2,500 dashboards and a member who holds nothing directly; home pages through all
2,500 at `Limit: 50` with no error and no repeats; the same member with 1,200 viewable spaces
gets `ErrEvaluationLimit` (the documented A3 cliff) under the engine default and pages cleanly
under the host override.

## Execution record (2026-09-08)

RATIFIED 2026-09-08 (owner, in-session: "can you execute"); rulings R1–R5 taken as recommended
(keep `MaxLookupResults` 1000; bind cursors to the owning model digest, both kinds; `FilterPage`
in the pocket; two trains, B first; `Truncated` aliased one release). Built as two stacked PRs:

- **PR #42** (`authorization-lookup-paging-b` → main) — tasks 5–6, core `v0.10.0`.
- **PR #43** (`authorization-lookup-paging-a` → #42's branch) — tasks 1–4, core `v0.11.0` +
  `stores/pgx/v0.6.0` + `stores/turso/v0.5.0`.

Merge and tags are OWNER steps this session: the auto-mode classifier refused `gh pr merge`
(both with and without `--delete-branch`). The post-merge sequence is written into the
session's closing block ("Run next").

- **Task 5** — `filter_page.go` (`Candidate`, `CandidatePage`, `CandidateSource`,
  `FilterPageRequest`, `FilterPage`); `EvaluationLimits.MaxFilterScan` + `DefaultMaxFilterScan`
  (20 000); root `Service.limits`; README limits row + "The postfilter page-filler" section.
  Decisions: a call that consumed nothing returns an empty `NextCursor` (not the input cursor);
  no new sentinels — every refusal wraps `sdk.ErrInvalidInput` inline; `MaxFilterScan` is read
  off the resolved limits in the root package, never charged by `budget`.
- **Task 6** — `batch_reader.go` (`memoReader`: exact-argument memo of successful
  `GetRelationTargets` / `CheckRelationWithGroupExpansion`, copies both ways, never caches errors
  or post-cancellation results); `checkBatchSequential` runs `s.check` with a fresh budget per
  request over one memo. Parity tests vs sequential `Check` incl. every limit dimension, zero
  `Lookup*` calls under a tight `MaxLookupResults`, read-count pins (container reads = 1, item
  reads = N). Bench 1×500: `container-through` 1.38 → 1.02 ms/op; `container-direct` ~6% slower
  on the in-memory fake (map growth + mandated copies; wins against any real store).
- **Task 1** — ports: `LookupResourceIDs`/`LookupResourceIDsByRelationTarget` gain `after`;
  `LookupDescendantResourceIDs(…, relations []string, …, after, limit)`;
  `role.Storer.LookupResourceIDsBySubjectAndRoles`. Byte order made CONTRACTUAL in the Bounding
  note (the engine merges streams by Go string compare). `LookupRequest.After`,
  `LookupResult{HasMore, NextCursor}`, deprecated `Truncated`, `ErrInvalidCursor` (re-exported
  from `codes.go`), `cursor.go` codec (`LookupFingerprint`, `EncodeLookupCursor`,
  `DecodeLookupCursor`; base64url JSON `{v:1,id,fp}`, fp = hex of the first 16 sha256 bytes over
  a length-prefixed encoding of kind/model digest/principal/permission/type), README rewrite.
- **Task 2** — `lookupResources` split into `lookupRoots` + a single-call `expandSelfHierarchy`
  over ALL self relations (the engine-side fixpoint loop is gone; the store closes over the
  union). `LookupResourcesPage`: one stream per direct relation / non-self Through hop / the
  closure, each read with `after` and `limit+1`; the union is sort+dedup of bounded streams
  (equivalent to the plan's k-way merge — a stream that returned `limit+1` rows forces
  `len(union) > limit`, a shorter stream is exhausted, so no id between `after` and the page's
  last id can be missing). Intermediate targets and the hierarchy ROOT set stay complete and
  bounded (A3). Roles: `roleEngine.LookupResourcesPage` over the store lookup;
  `CompiledRoleModel.Digest()` (`RoleModelEncodingVersion`). `Composite.LookupResourcesIn`
  validates once (`Limit > MaxLookupResults` → `sdk.ErrInvalidInput`), dispatches to the ONE
  owner, binds the cursor to the owner's name + digest.
- **Task 3** — `storetest/keyset.go`: `Relationship/LookupKeyset/{Direct, ByRelationTarget,
  Descendants, DescendantsFollowTheRelationUnion}`, `Roles/RolesLookupKeyset/*`,
  `Parity/LookupPagedParity/{OracleUniverse, HierarchyDescendantBeforeRoot,
  CursorIsBoundToItsQuery, IntermediateOverflowIsErrorOnEveryPage}`,
  `Parity/RolesPagedParity/*`, `Composed/PagedPairOwnershipDispatch`. Byte-order fixture ids
  `B a _x ~z Z é`. Mutation-tested (inclusive `after`, locale sort, single-relation closure all
  fail). No harness registration change was needed.
- **Task 4** — pgx pins `COLLATE "C"` per lookup query and projects the collated expression
  (PostgreSQL requires a `SELECT DISTINCT` ORDER BY expression in the select list);
  `0005_iam_lookup_keyset.sql` in both ledgers (`idx_iam_relationships_type_relation_resource`
  on `(resource_type, relation, resource_id[ COLLATE "C"])`,
  `idx_iam_roles_subject_resource_lookup` on `(subject_type, subject_id, resource_type,
  resource_id, role)`), ordinary transactional `CREATE INDEX` with the SHARE-lock note and the
  CONCURRENTLY-by-hand escape. `explain_live_test.go` at 1 007 964 relationship rows + 100 000
  role rows on an `en_US.utf8` cluster: `ByRelationTarget` BitmapAnd(subject idx, NEW idx) 18 ms;
  role lookup Index Only Scan via the NEW idx 11 ms; expansion-join lookup reaches the table via
  the NEW idx 38 ms; closure (≈11k ids) 31 ms — cost is the closure, as A3 says. No covering or
  second index needed. `stores/UPGRADE.md` note.
- **Docs** — README tour stop 3b rewritten to the paged contract; the shipped v0.9.0
  `stores/UPGRADE.md` heading dated (was still "next tag").
- **Task 7 (release, on "release it")** — main had moved (#44, authentication v0.10.0), so both
  branches were rebased (RELEASING conflict: this train's note placed above #44's). PR #42
  squash @ `162d24e` → `pockets/authorization/v0.10.0` (proxy first poll; cold build/vet/test
  `GOWORK=off`). Deleting #42's branch auto-closed the stacked #43 and GitHub refuses to
  retarget a closed PR, so the same commits (rebased onto main) went up as **PR #45** → squash
  @ `4bd2363` → `pockets/authorization/v0.11.0` (first poll) → store repins `v0.9.0 → v0.11.0`
  @ `a78aab2` → `stores/pgx/v0.6.0` + `stores/turso/v0.5.0` (first poll) → all three modules
  cold-built and vetted `GOWORK=off` from a fresh `GOMODCACHE`, both store tags cold-resolved
  from a fresh module, the v0.11.0 core cold-tested → RELEASING hashes filled, `UPGRADE.md`
  note dated, plan copied to `plans/`, #29 closed. Lesson: merge a stacked PR's base WITHOUT
  `--delete-branch`, or retarget the child first — a deleted base closes the child for good.

Verification: pocket `go build/vet`, `go test -race` green on both branches; `make guard` green;
pgx live default + `POSTGRES_TEST_SCHEMA=auth_keyset` legs green (≈35 s each) incl.
`TestLookupPlansAtScale`; turso playground: everything except `TestConformance$` green (121 s),
the keyset slice of `TestConformance` green (191 s); full `TestConformance$` green (1145 s, its
own invocation with `-timeout 60m`). CI green on both PRs. Observed and not folded: one transient pgx `LookupKeyset` failure and one
turso FAIL with no test-level line while another agent was mid-edit on `storetest/` — both
re-ran clean three times.
