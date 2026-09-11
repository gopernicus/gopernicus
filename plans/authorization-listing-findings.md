# Authorization-aware listing: measured findings

Status: AUDIT COMPLETE — 2026-09-10; no implementation.
Parent: [design and implementation plan](authorization-listing-design.md).

Verdict: keep the portable complete-set and candidate-filtering
paths, but make their contracts unmistakable before adding host-facing convenience
or a SQL fast path. Current FilterPage passed the bounded behavioral probes; the
most material confirmed defect is a false PostgreSQL physical-work bound.

Scope: read-only review of current firestore-authentication / 6807ed06, following
.claude/agents/lead-backend-engineer.md and plans/authorization-listing-audit.md.
The reviewer made no repository changes. This report is preserved here for the
next context; diagnostic probes and overlays remain in /tmp. No auth source changed.
A fresh parent-owned PostgreSQL container was used ONLY through schema
listing_review. The root has verified removal of the owned service. No external
consumers edited.

## Ranked findings

### 1. PostgreSQL's claimed expansion work bound is false (confirmed execution)

`pockets/authorization/stores/pgx/relationships.go:42-77` says graph work depends
on the configured budget rather than graph size. Its recursive UNION includes
depth, then a DISTINCT states step precedes the state-cap LIMIT. Actual plans for
the production FilterRelation query show the recursive closure consumed in full
before the limit. All cases correctly returned ErrExpansionBudgetExceeded; the
error is semantic, but it does not prevent the physical work described below.

| Membership rows | Budget | Recursive Union output | Capped output | EXPLAIN execution |
|---:|---:|---:|---:|---:|
| 100 | 10 | 101 | 11 | 0.241 ms |
| 1,000 | 10 | 1,001 | 11 | 1.134 ms |
| 5,000 | 10 | 5,001 | 11 | 5.281 ms |

These are one local fixture's measured plan times, not throughput promises. The
row counts are the important evidence. The public store call was also invoked;
EXPLAIN used the actual helper plus the production FilterRelation query tail.

Minimal investigation to schedule: reuse UNION on distinct (type,id,relation)
states without the extra depth column, materialize at most budget+1 states before
matching, and remove the redundant blocking DISTINCT. A disposable SQL variant
consumed exactly 11 recursive rows at all three sizes (0.194/0.113/0.138 ms).
This is a promising simplification, not approved code or a full correctness proof.
Require cycles, diamonds, boundary overflow, positive results, both check/set
methods, SQL planner coverage and Turso parity before adopting it. Even a state
cap does not promise a fixed number of underlying index/table reads.

Evidence: /tmp/gopernicus-authorization-listing-pg-probes.log and
/tmp/gopernicus-authz-plan-bounded-{100,1000,5000}.json; experimental plans use
/tmp/gopernicus-authz-plan-experimental-state-cap-*.json.

### 2. Lookup pagination bounds output, not closure or membership work (confirmed/source)

At `stores/pgx/relationships.go:850-885`, the descendant CTE computes the closure
before applying exclusive after, DISTINCT/order and limit. With 5,000 descendants,
LIMIT 2 returned [f00001 f00002]; after=f04000 returned [f04001 f04002]. Both plans
expanded 5,000 recursive rows. Measured server times were 121.384 and 111.727 ms
in the final run. Paging repeats this work. This is explicitly acknowledged in
that method's comment; the broad “top-level reads are bounded by page” claim in
`pockets/authorization/README.md:393-401` must distinguish rows from work.

The same distinction extends beyond descendants:

- PostgreSQL direct lookup uses unbounded reachableCTE before result limiting
  (`stores/pgx/relationships.go:785-795`); the other budget dimensions are not
  carried by the lookup port (`domain/relationship/relationship.go:260-264`).
- Turso has the same recursive-closure-then-page shape at
  `stores/turso/relationships.go:785-827` (source review, not live-measured here).
- Firestore's direct lookup calls expand(..., 0) at
  `stores/firestore/relationships.go:429-442`: complete unbounded subject
  expansion followed by chunked streams. Descendants compute the full Go closure
  then page it at :503-509 / `lookups.go:236-290`.
- Firestore distinct-ID stream limits do not bound physical documents: duplicate
  resource IDs may require several physical pages, and k-way merge primes every
  chunk (`lookups.go:91-173`). One logical store read is not one network RPC.
- Through targets and self-hierarchy roots are deliberately complete per page,
  so MaxLookupResults exhaustion at an intermediate set repeats on every page
  (`internal/logic/authorizersvc/lookup.go:334-359`). No cursor removes that cliff.

Keep keyset ordering and complete roots; do not push root pagination through a
hierarchy because lexical root order does not determine descendant order. State
separate limits for returned IDs, candidate rows, graph states and physical I/O.
Treat a hard datastore work budget as a deliberate future adapter contract, with
context deadlines as the current operational bound; do not claim one exists.

### 3. Memo and role cost claims need narrowing (confirmed counters)

`FilterPage` calls FilterAuthorized independently on every pull at
`filter_page.go:178`; each call constructs a fresh memo at
`internal/logic/authorizersvc/service.go:227`. A shared parent is reused within
one set evaluation, not across the pulls of one FilterPage. Historical claims
about reuse across pulls do not describe current code.

For roles, the “already-set-shaped” comment at
`internal/logic/decisionsvc/composite.go:240-243` is wrong. The role branch creates
N check requests, `roles.go:78-90` evaluates them sequentially, and
`rolesvc/service.go:91-107` does a scoped then global probe. With one granting
role and 20 candidates, the fixture measured 40 HasExactRole calls both when all
were denied and when the principal held the role globally.

The relationship README's O(branches+hops) discussion (:452-488) must be scoped
to one relationship FilterAuthorized call and logical port calls. It does not
cover roles, repeated pulls, physical SQL rows, Firestore chunks or graph depth
that depends on data. Changing the memo lifetime would introduce bounded stale
reads within a page; name that consistency choice before implementing it. Do not
add cross-request authorization caching as an incidental optimization.

### 4. Complete-set and ID-page names/results invite a real caller mistake

Current complete `LookupResources` and paged `LookupResourcesIn` share the same
LookupResult type. `Limit:0` on the latter means one default-sized page, not all
IDs (`decisionsvc/composite.go:327-348`). Parent's consumer audit found Segovia
using that page as a complete SQL prefilter; that concrete caller evidence was
provided by the parent, not independently executed here.

The README combines both in one prefilter column (:436-440) and says user-facing
ordering belongs to the client (:389-392). That is incorrect guidance for a
COMPLETE bounded ID set: apply the set to business SQL before search, sorting,
counts and pagination to retain correct server-side ordering. Sorting one ID
page's loaded rows cannot produce a globally ordered business page.

Recommended naming direction for the implementation plan: `LookupAllResourceIDs`
returns a complete bounded `ResourceSet{IDs,Unrestricted}` or an error;
`LookupResourceIDPage` returns a distinct page type with continuation. The exact
names are negotiable; the all/page distinction and different result types are
valuable. Eliminate the compatibility-only Truncated field in the planned
breaking release rather than introducing another incomplete-result flag.

### 5. Continuation metadata can reveal denied candidates (confirmed; host policy)

FilterPage intentionally returns the source cursor after the last consumed
candidate, including a denied one (`filter_page.go:187-209`). The probe produced:

    {"items":[],"next_cursor":"private-b","has_more":true}

No unauthorized item or total was returned. Nevertheless the cursor reveals the
denied row ID in this source encoding, and HasMore reveals candidate existence.
Opaque means the framework does not interpret it; it does not mean encrypted.
The helper cannot promise confidentiality while returning arbitrary source
cursors. If a host requires it, use an authenticated encrypted/server-held
continuation and explicitly accept or redesign the candidate-existence hint.
Simply changing HasMore to mean “another allowed item” requires additional scans
and may be incompatible with the hard per-call scan budget.

Lookup's own base64 JSON cursor is also readable; it includes last allowed ID and
query fingerprint (`authorizersvc/cursor.go:24-64`). Foreign-principal replay was
rejected. Changing just id from d02 to d04 with the same fingerprint was accepted
and returned only checked d05, exactly as the source's unauthenticated-selector
contract permits. No authorization bypass was observed. FilterPage itself does
not bind source cursors to principal, permission, business filters or sort; source
codec/wrapper policy owns that. Prefer query binding in the worked host example.

Small documentation mismatch: LookupRequest says Unrestricted ignores Limit and
After (`authorizersvc/model.go:133-134`; README:404-406), but the composite validates
limit/cursor before consulting roles (:327-348). Keep validation and correct the
docs; unrestricted means skip the authorization ID restriction, never tenant or
business restrictions.

## Measured portable fixture

100 host-ordered candidates; requested output 10; MaxFilterScan 20. The source emits
an exclusive per-row cursor, and counters wrap the real memory relationship store.
The entire concatenated result is asserted to equal the expected allowed rows in
source order without duplicates or omissions.

| Shape | First page items | First source rows/calls | First relationship port calls | All pages / rows fetched |
|---|---:|---:|---:|---:|
| All direct grants | 10 | 20 / 1 | 1 FilterRelation | 10 / 190 |
| One direct grant per 10 | 2 | 20 / 1 | 1 FilterRelation | 5 / 100 |
| No grants | 0 | 20 / 1 | 1 FilterRelation | 5 / 100 |
| All inherit shared parent | 10 | 20 / 1 | 1 targets + 1 parent filter | 10 / 190 |
| Same parent; source returns one row per pull | 10 | 10 / 10 | 10 targets + 10 same-parent filters | 10 / 100 |

Dense overfetch is intentional, not a pagination defect: the continuation is after
the consumed item, so the next request fetches/checks the unused suffix again.
Its cost is visible (190 source rows for 100 returned). A later performance task
can compare requesting only the remaining page capacity or adapting pull size;
do not add knobs before the worked host needs them.

An empty sparse page has a continuation and HasMore while candidates remain. A
backend decision error returned the zero page and propagated the original error;
no partial page was released. Total is nil/omitted, previous-navigation fields
remain empty, and FilterPage does not support authorized totals, offsets, previous
pages or facets by implication. Never copy the source's unfiltered totals/facets
onto this result.

The 100-iteration memory benchmark measured 67,750 ns/op, 57,093 B/op, 99 allocs/op
for dense and 13,580 ns/op, 19,664 B/op, 70 allocs/op for sparse. Different grant
counts also change memstore traversal cost, so these are diagnostic samples,
not a density-versus-latency conclusion for SQL or remote authorization.

## Small host API and staged tasks

1. Document one host-owned `ListDocuments(ctx, principal, query)` operation.
   Keep request identity, tenant/business filters and sort in host vocabulary.
   Its outbound adapter chooses complete-ID filtering or ordered candidate checks;
   callers receive one normal result with explicit continuation semantics.
2. Make all-ID versus paged-ID operations/results unmistakable. Teach complete
   bounded access first for a small authorized container set, retaining SQL sort,
   totals and business joins. A failed/exhausted lookup is an error, not an empty
   unrestricted filter. Add a regression above the default 1000-ID page threshold.
3. Preserve portable FilterPage for host-ordered bounded candidates. Keep it in
   the authorization pocket. If hosts need a custom policy wrapper, replace the
   concrete Service dependency with a narrow host-supplied batch-filter seam and
   explicit scan limits; do not extract an SDK query planner. Current concrete
   Service/private limits also prevent a host bypass wrapper from being used here.
   SDK-only domain code can consume its own listing port; the outbound/host-pocket
   adapter invokes authorization. No broad layering change is required.
4. Consider a dedicated filtered-result contract exposing ScanLimitReached (and
   perhaps candidates examined) for operational/UI use. Define whether counts
   describe fetched/evaluated or consumed candidates. Keep HasMore documented as
   candidate continuation, or name it MoreCandidates. Do not promise an exact
   authorized total for arbitrary remote policy.
5. Correct cost/consistency docs and add bounded physical-work evidence to the
   PostgreSQL budget task. Prototype the simpler state-only CTE, then prove parity.
   Review a real role batch operation if the worked host uses roles with postfilter;
   one global-probe-per-page optimization alone does not solve scoped N+1 reads.
6. Parent owns the three-strategy comparison. Same-DB/no-join uses the same portable
   orchestration; optional shared transactions require a named isolation/snapshot
   policy. Across systems and across requests there is no shared snapshot. Even
   Firestore's per-store-call ReadSnapshot does not make the whole engine evaluation
   or business read one snapshot. The same-SQL join path must match full permissions,
   global roles, usersets, inheritance, tenant filters and join deduplication; a
   direct-grant join is only a explicitly restricted model, not general ReBAC.
7. Add a worked composite-sort source and query-bound continuation, including
   denied-final-candidate and sparse empty-page cases. State cursor confidentiality
   and count/facet policy. Exercise grant/revoke between pulls/pages as changes in
   state, not as promised snapshots.

## Reproduction and limits

Go 1.26.1; GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Commands from modules:

    # pockets/authorization
    go test -overlay=/tmp/gopernicus-authorization-listing-overlay.json -run TestAuditListing -bench BenchmarkAuditListingDirect -benchtime=100x -benchmem -v .
    go test -race -overlay=/tmp/gopernicus-authorization-listing-overlay.json -run 'TestAuditListing|TestFilterPage|TestLookupResourcesInThroughTheFacade' -v .

    # pockets/authorization/stores/pgx, authorized loopback escalation
    go test -overlay=/tmp/gopernicus-authorization-listing-pg-overlay.json -run TestAuditListingSQLWork -v .

All passed, no skips. Logs are /tmp/gopernicus-authorization-listing-probes.log,
/tmp/gopernicus-authorization-listing-race.log and
/tmp/gopernicus-authorization-listing-pg-probes.log. Overlay source files are
/tmp/gopernicus-authorization-listing-probe_test.go and
/tmp/gopernicus-authorization-listing-pg-probe_test.go. Plan JSON lives under
/tmp/gopernicus-authz-plan-*.json. No service was started/stopped by this reviewer;
only owned listing_review fixture rows and that schema's migration ledger changed.

Firestore and Turso were source-reviewed, not exercised against a service in this
slice. No claim of whole authorization correctness, production query latency,
full three-strategy benchmark, or consumer execution is made. Root and the other
reviewer own those separate audits. No authentication/authorization fix implemented.
