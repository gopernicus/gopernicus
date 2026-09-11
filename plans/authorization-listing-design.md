# Authorization-aware listing: design and implementation plan

Implementation completed and verified 2026-09-11 in
[authorization-audit-implementation.md](authorization-audit-implementation.md).
The original audit/proposal below remains historical evidence; its pre-fix
behavior and earlier authorization status are superseded by that plan.

Status: AUDIT/PLAN COMPLETE — 2026-09-10; implementation NOT authorized in this
phase. Parent: [authentication-authorization-audit.md](authentication-authorization-audit.md).
The [review brief](authorization-listing-audit.md) defines the original questions;
[measured findings](authorization-listing-findings.md) preserve the detailed
framework probes. This document answers the three deployment cases and proposes
the smallest host-facing design. It does not introduce an API or SQL adapter.

## Recommended shape

Keep authorization-specific listing in the authorization pocket. A host exposes
one ordinary operation, such as `Documents.ListVisible(ctx, principal, query)`.
Its adapter chooses how to intersect authorization with the business query. The
domain owns tenant scope, search, sorting and result vocabulary; the composition
root supplies the authorizer and repositories. SDK-only domain code can depend
on its own small listing port without importing the authorization pocket.

Keep two portable strategies and investigate one opt-in SQL strategy. There is
no universally efficient strategy for every permission density and search query.
Do not add an automatic strategy selector, generic SDK query planner, SQL DSL,
authorization cache or another repository hierarchy as part of the first fixes.

| Deployment | Complete allowed IDs, then business query | Ordered business candidates, then checks | Authorization in business SQL |
| --- | --- | --- | --- |
| Different stores | Works when the entire allowed set fits the bound; transfer the set across the boundary. | Works with bounded candidates and batch checks; sparse permissions may require many round trips. | Requires an explicit local projection/index or another consistency design; there is no cross-store SQL join. |
| Same SQL database, separate queries | Same portable API; SQL applies the complete set before business ordering/pagination. | Same portable API; adapters can participate in a common transaction. | Deliberately unused; colocation does not require a join. |
| Same SQL database, joining | Still a valid choice for small sets. | Still a valid choice for selective searches. | An explicit supported predicate or semijoin can preserve database search, sort, counts and pagination; it must implement the full selected policy. |

ReBAC does not rule out SQL joins. The existing PostgreSQL store already uses
recursive SQL and joins for relationship evaluation. What is missing is a
supported way to combine an arbitrary host query with the complete permission
semantics. A direct-grant join is useful for a restricted policy; it is not a
general compiler for the current engine.

## The ordinary host operation

The following is a proposed host sketch, not newly implemented framework API:

```go
// In host domain vocabulary; principal is the existing SDK principal.
type DocumentLister interface {
    ListVisible(context.Context, sdk.Principal, DocumentQuery) (DocumentPage, error)
}

// The outbound adapter owns the authorization and datastore dependencies.
// The HTTP endpoint resolves identity and parses the normal business query,
// then calls this method once. It does not assemble permission IDs itself.
```

The implementation should make its strategy explicit at construction or in a
small method appropriate to that endpoint. Both portable methods retain the
same business tenant/search predicates. A host-level administrator bypass is
evaluated in the same adapter and skips only the authorization restriction.
It must not accidentally remove tenant isolation or ordinary business filters.

### A. Complete bounded permission set

1. Resolve host bypass or call current `LookupResources`. A successful result is
   either unrestricted or the complete allowed ID set within configured limits.
2. Handle unrestricted, none and lookup failure separately. An error, including
   an expansion/result-limit error, is not an unrestricted or empty success.
3. For a restricted set, pass all IDs to the business repository. Apply them
   together with tenant, search and other predicates **before** sorting/counting
   and pagination. Use SQL parameters, not interpolated IDs.
4. Return the business repository's stable page. A complete ID filter permits
   normal server-side business sort; it does not require client-side sorting.

For a bounded container set, the SQL can be as simple as:

```sql
SELECT d.id, d.name
FROM documents AS d
WHERE d.tenant_id = $1
  AND d.container_id = ANY($2)
  AND (lower(d.name), d.id) > ($3, $4)
ORDER BY lower(d.name), d.id
LIMIT $5;
```

The host owns column types, first-page handling, collation and cursor encoding.
A separate unrestricted branch omits only the ID predicate. A non-nil empty
permission set returns no rows. Do not rely on a repository's nil-slice convention
without an explicit unrestricted decision at this boundary.

Current `LookupResourcesIn(Limit: 0)` returns **one default-sized ID page**, not
the complete set. Loading those rows and sorting them cannot produce a globally
correct business page. Looping every authorization page to reconstruct the full
set also defeats the complete-set budget and has cross-page consistency costs;
do not make that an invisible fallback on overflow.

### B. Ordered candidates and batched checks

Use current `authorization.FilterPage` in the adapter. Its `Source` queries the
business repository in the desired stable order, with an exclusive cursor for
every candidate row. `ID` extracts the authorization resource ID. Tenant/search
filters already constrain the candidate source. Authorization preserves that
order while consuming candidates until the page is full, exhausted or bounded.

The required source shape is useful and should remain explicit:

```go
source := func(ctx context.Context, after string, limit int) (authorization.CandidatePage[Document], error) {
    // Decode and validate after against the host query.
    // Read <= limit rows in the exact composite sort order.
    // Give each row its own continuation, including denied rows.
    // Return HasMore for the candidate query; do not attach its total/facets.
}
```

The worked implementation must contain the actual codec and SQL, not leave those
comments as production boilerplate. For `(lower(name), id)` sorting, encode the
actual normalized sort value and ID used by SQL. Define nulls, collation and
ascending/descending behavior. Bind continuation to the tenant, filters, sort,
principal and permission where the host requires that query identity.

`FilterPage` currently takes `*authorization.Service` and reads private limits.
That is usable behind a host-owned listing port. If the example requires a custom
batch authorizer or host bypass wrapper, introduce a narrow batch-filter function
and explicit scan settings in the pocket. Demonstrate the reduced caller code
before adding that seam; do not promote authorization policy into SDK.

### C. Same-database predicate/semijoin

Prototype this only after check/batch/lookup agreement is repaired. For a
document policy consisting solely of direct grants, a worked SQL predicate can
use `EXISTS` rather than multiplying business rows through grant joins:

```sql
SELECT d.id, d.name
FROM documents AS d
WHERE d.tenant_id = $1
  AND EXISTS (
      SELECT 1 FROM document_grants AS g
      WHERE g.document_id = d.id AND g.principal_id = $2
  )
ORDER BY lower(d.name), d.id
LIMIT $3;
```

This is explanatory host SQL over a toy grant table, not the framework schema.
For the framework, either compile the supported model faithfully or explicitly
reject unsupported rules at construction. Include direct grants, usersets,
inherited permissions, roles with global grants, tenant scope, duplicate-producing
business joins, model changes and revocations in the parity oracle. An adapter
must never silently use only the supported branches of a larger model.

The first deliverable is one real host example with a constrained supported
model, reference checks and query plans. A general compiler is a separate scope
decision. Shared database layout must be explicit; keep dialect-specific details
in the integration/store or host adapter. SDK gains no SQL-shaped contract.

## Pagination, counts, privacy and consistency

| Topic | Contract to preserve or make explicit |
| --- | --- |
| ID pages | Lexically ordered authorization IDs. They are not name/date/rank-sorted business pages. Current cursor binds principal, permission, type, owning kind and model digest. |
| Candidate pages | Last **consumed** per-row cursor; an overfetched suffix is read again on resume. Stable fixture tests found no omissions or duplicates. |
| Short/empty page | Valid when the scan budget is reached. `HasMore` means unscanned candidates remain; it cannot promise another permitted row. |
| Errors | Any decision/storage error yields an error and no partial successful page. Distinguish evaluation failure from a denied decision in HTTP/observability. |
| Result limits | A complete-set overflow is an error. Candidate scan exhaustion is resumable and can return a partial page. These are different contracts. |
| Counts/facets | A complete ID filter or equivalent SQL permission predicate can support exact authorized counts. Remote candidate filtering cannot cheaply promise them. Omit totals/facets unless explicitly computed over authorized data. |
| Offsets/previous | Current FilterPage provides forward continuation. Do not infer authorized offsets or previous-page support from generic list fields. |
| Unrestricted | Removes the permission-ID restriction only. It does not remove tenant/business restrictions or input validation. |
| Cursor privacy | An opaque cursor may contain a denied row's ID/name. Use host-owned encrypted or server-held continuation if secrecy is required; `HasMore` itself may disclose candidate existence. Signing alone does not hide contents. |
| Cursor tampering | Current lookup cursor is a readable, query-bound selector, not authenticated evidence. Modifying the after-ID did not bypass checks. FilterPage delegates query binding entirely to its source. |

Across stores, authorization and business reads do not share a transaction or
snapshot. A grant/revoke or resource move can race the list. Across HTTP pages,
even a same-database implementation normally sees changing state. The host must
choose whether ordinary read-time decisions suffice or whether its use case
needs stronger consistency; do not claim a fixed snapshot from a cursor.

Same SQL without joins can use the existing ambient transaction through the
**same connector instance** and context across participating adapters. PostgreSQL
Read Committed still gives each statement a new snapshot. A repeatable-read
transaction can give the several reads one snapshot, but the current `pgxdb`
`Transact` uses default `Begin` and does not expose isolation options. Treat any
connector enhancement as a separate, justified task. A single joined statement
has a statement snapshot, not a promise covering the next HTTP request.
[PostgreSQL isolation documentation](https://www.postgresql.org/docs/17/transaction-iso.html).

Memoizing within one page can reuse permission data across pulls, but deliberately
extends those reads' lifetime. That bounded choice must be documented and tested
with grant/revoke between pulls. Do not introduce cross-request authorization
caching while fixing repeated work.

## Evidence from current consumers

Read-only source inspection; no consumer was pulled, built, changed or run.
Paths below are external repositories and may be unavailable on another machine.
Pinned versions differ from this workspace; findings describe call patterns and
adoption risks, not a claim that current workspace code is deployed there.

| Consumer | Actual pattern and implication |
| --- | --- |
| Segovia v2, main `76b3d78`, authentication 0.10.0 / authorization 0.11.0 | `internal/inbound/domains/dashboards/pages.go:103` uses `LookupResourcesIn(Limit:0)` as complete `containerIDs`, ignoring continuation. Similar timelines/tenancy paths exist. `internal/outbound/domains/dashboards/dashboards.go:95` correctly filters in SQL before `(lower(name),id)` paging, but receives a potentially incomplete ID set. `internal/logic/domains/dashboards/ports.go:23` distinguishes nil/unrestricted from non-nil empty/none. Preserve that distinction and repair the caller/API ambiguity on adoption. |
| coordination-hub, main `84ff08a`, authentication 0.9.0 / authorization 0.7.0 | `internal/outbound/domains/coordination/membership.go:651` uses complete `LookupResources` and propagates failures; the surrounding list hydrates a bounded set, then sorts/pages. This avoids partial-ID ordering but pays full-set hydration and in-memory sort cost. |
| gps-360-go, main `e1ab3f0`, authentication 0.9.0 / authorization 0.7.0 | `internal/inbound/domains/races/http.go:137` and businessdevelopment use complete lookup with explicit unrestricted scope, errors and no-access handling. `internal/inbound/compositions/clienthub/roster.go:28` uses paged lookup with the older Truncated field; review continuation when upgrading its pinned API. |

Local roots: `/Users/jrazmi/code/segovia/segovia/v2`,
`/Users/jrazmi/code/gps/coordination-hub`,
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`.

## Measurements and their limits

The detailed findings contain real `FilterPage` memory-boundary tests and real
PostgreSQL framework-query plans. In particular, a budget of 10 still expanded
5,001 recursive states before reporting overflow; a two-ID descendant page
expanded all 5,000 descendants. Output bounds do not establish database-work
bounds. Roles made 40 exact-role calls for 20 candidates. A shared parent was
reused within a batch but reread on each source pull.

A separate disposable PostgreSQL 17 experiment compared the three host
strategies with 50,000 documents, a tenant predicate, reverse-ID name ordering,
page size 50 and direct grants. Dense grants allowed every second ID (25,000);
sparse grants every hundredth (500). Candidate pulls requested 100 rows with a
20,000-candidate bound. All three strategies returned exactly the same ordered
rows. This experiment used direct SQL, not a full ReBAC adapter.

| Grants | Strategy | SQL calls | Rows returned across calls | Result rows | Local elapsed |
| --- | --- | ---: | ---: | ---: | ---: |
| Dense | Complete IDs then SQL | 2 | 25,050 | 50 | 11.888 ms |
| Dense | Ordered candidates then checks | 2 | 150 | 50 | 1.191 ms |
| Dense | SQL EXISTS | 1 | 50 | 50 | 0.642 ms |
| Sparse | Complete IDs then SQL | 2 | 550 | 50 | 1.323 ms |
| Sparse | Ordered candidates then checks | 98 | 4,950 | 50 | 24.993 ms |
| Sparse | SQL EXISTS | 1 | 50 | 50 | 2.901 ms |

Rows returned are network/result volume, not physical rows scanned. Timings are
one local run, affected by plan/cache/order effects; they are not production
benchmarks. The dense complete set exceeds the framework's default 1,000-ID
limit: this deliberately unbounded toy query does not prove the framework would
accept it. Separate-store latency was not simulated. The experiment supports
strategy tradeoffs and correct ordering, not a general claim that joins win.

Using only the first 50 allowed IDs before the same business sort returned first
ID 100 (dense) or 5,000 (sparse), while all complete strategies returned 50,000.
This is a concrete counterexample to sorting an incomplete authorization page.

Recreate the toy fixture with integer IDs 1..50,000, names
`lpad((50001-id)::text,6,'0')`, tenant b for multiples of 7 and tenant a otherwise,
and grants at the densities above. Compare complete `id=ANY(ids)`, ordered
100-row candidate pulls plus batched grant checks, and a grant `EXISTS` predicate.
Assert the complete ordered row sequences agree. The session artifacts are
`/tmp/gopernicus-listing-strategies.go`, `.json` and `.log`; temporary scripts
are diagnostic evidence, not maintained regression tests.

## Proposed implementation sequence

1. **L1 — Correctness prerequisites.** Follow the authorization core plan to
   establish check/batch/lookup agreement, immutable configuration and meaningful
   work limits. Keep permission results/errors authoritative before convenience.
2. **L2 — Make complete sets and pages unmistakable.** Working names:
   `LookupAllResourceIDs` -> complete bounded `ResourceSet{IDs,Unrestricted}`;
   `LookupResourceIDPage` -> distinct result with continuation. Exact names may
   change in implementation review, but one result type must not imply both
   completeness and a partial page. Remove deprecated Truncated in the planned
   breaking release. Add consumer examples above the 1,000-ID threshold and
   non-ID business sort; record migration in AUDIT only when implemented.
3. **L3 — One complete portable host example.** Implement the normal endpoint,
   composition, domain port and outbound adapter using complete-set filtering
   and FilterPage over the same dataset. Include tenant/search/composite-sort
   SQL, a real query-bound cursor codec, unrestricted/none/error handling, no
   unfiltered totals, and sparse empty-page behavior. Compile and exercise real
   HTTP responses against memory and disposable PostgreSQL, with identical rows.
4. **L4 — Fix measured repeated work.** Benchmark remaining-capacity/adaptive
   pulls against current overfetch, bounded memo reuse across pulls and genuine
   role batching. Adopt only improvements that keep clarity and proved semantics.
   Explore the simpler state-only capped CTE with cycles/diamonds/boundary parity;
   inspect physical plans. Name graph/output/I/O limits separately.
5. **L5 — Minimal ergonomics.** Use L3 host code to decide a narrow batch-filter
   seam and explicit scan settings. Prefer a pocket-specific filtered result
   with `ScanLimitReached` if callers need to distinguish scan exhaustion from
   an ordinary short page. Define consumed versus fetched/evaluated diagnostics;
   do not expose permission graph internals in product responses by default.
6. **L6 — Opt-in same-SQL proof.** Add one constrained model example/adapter and
   differential checks against authoritative permission evaluation. Verify
   duplicates, inheritance, usersets, roles, counts and revocation. Document
   unsupported models and transaction/isolation requirements. Evaluate a general
   compiler only after this evidence and a host need justify it.

Acceptance includes no unauthorized items or totals, exact stable ordering and
resume without omissions/duplicates, explicit partial-page/error semantics,
bounded work claims backed by the relevant counters/plans, and small readable
host wiring. Simulate grant/revoke and moves between reads/pages to document the
chosen consistency; do not make every strategy promise a snapshot it lacks.

The established search/check, complete permission-set/search and local-index
tradeoffs also appear in [OpenFGA's search guidance](https://openfga.dev/docs/interacting/search-with-permissions).
Its filtering terminology uses the search pipeline as its reference point;
this plan names the first query explicitly to avoid that ambiguity.
`EXISTS` semantics are described in [PostgreSQL's subquery documentation](https://www.postgresql.org/docs/17/functions-subquery.html).

## Verification status and deferred scope

Framework memory, focused race and actual PostgreSQL probes passed. Core review
also ran existing PostgreSQL/libSQL integration race suites. Firestore listing
behavior is source-reviewed only. The three-strategy comparison is a direct-grant
SQL fixture; a complete host HTTP example, full ReBAC SQL predicate, remote latency
experiment, changing-state differential tests and consumer upgrades remain future
implementation work. No pocket source, migration, SDK contract or consumer was
changed by this plan.
