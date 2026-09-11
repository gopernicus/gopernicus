# Authorization-aware listing: audit brief

Status: AUDIT/PLAN COMPLETE — 2026-09-10; implementation deferred by owner.
The completed [design and implementation plan](authorization-listing-design.md)
answers the three deployment cases with host placement, consumer evidence,
ordering/pagination/counts and actual measurements. The
[measured findings](authorization-listing-findings.md) preserve detailed framework
probes and limits. This file retains the original brief and review checklist.
Parent: [framework-audit.md](framework-audit.md).

## What exists today

- pockets/authorization/authorization.go exposes LookupResources (complete,
  budget-bounded allowed IDs), LookupResourcesIn (paged, ID-ordered enumeration),
  FilterAuthorized (batch filtering) and their lookup types.
- pockets/authorization/filter_page.go owns generic FilterPage, CandidateSource,
  per-row Candidate.NextCursor and FilterPageRequest. It fills from host-ordered
  candidates until full, exhausted or MaxFilterScan is reached. It returns the
  last consumed cursor, not the end of an overfetched batch. HasMore means more
  candidates, not a guarantee that another authorized row exists. Errors propagate.
- sdk/pkg/list (formerly sdk/foundation/list) owns general requests/pages,
  sorting/search vocabulary and cursor helpers; it has no authorization policy.
- pockets/authorization/stores/pgx/relationships.go already uses WITH RECURSIVE
  and JOIN for graph traversal. ReBAC does not preclude SQL. The portable facade
  does not currently provide a general predicate compiler for arbitrary host SQL.
- Existing implementation context: authorization-lookup-paging.md,
  authorization-batch-decision.md, authorization-gates-and-lookup.md and the
  authorization README's enumeration/postfilter sections. Their performance and
  correctness claims are inputs to verify, not assumed outcomes of this review.
- Initial read-only host references: coordination-hub's coordination membership
  outbound adapter uses LookupResources; gps-360-go's races and businessdevelopment
  inbound handlers use LookupResources. The paging/batch plans cite Segovia's
  bounded container-list prefiltering. Inspect current consumers in depth later.

## Working architectural recommendation

Keep authorization-specific orchestration in pockets/authorization. Reusability
across hosts alone does not justify promoting policy into SDK. Keep SDK list
primitives independent. If an implementation-independent authorization port or
generic bounded scan helper earns an SDK home, demonstrate the actual alternative
consumers and simpler caller API before extracting it. Do not preemptively add a
generic query planner, repository abstraction or SQL-shaped SDK contract.

Ordinary business-data joins remain available. Authorization can also be joined
or evaluated via recursive SQL when the needed data and semantics share a DB.
Multiple datastores or an external authorization service require another way to
intersect the permission set with filtered/sorted business data. A same-DB fast
path is an option to assess, not a feature assumed to exist or a new SDK default.

## Review goals and host scenarios

Owner follow-up explicitly requires comparing all three deployment/query choices:

| Case | What to evaluate |
| --- | --- |
| Different stores | Intersect authorization and business results through ports; network/read cost, consistency and pagination across independent systems. |
| Same SQL database, without joining | Keep the engine and business queries separate even when colocated; assess portable pre/post filtering, transaction boundaries and resulting host code. |
| Same SQL database, joining | Assess an explicit SQL adapter/predicate or equivalent supported integration that can combine policy with business filtering, sort, counts and pagination; require semantic parity with the engine. |

This comparison is now covered by the linked design. Do not infer
that sharing a database mandates joins, or that avoiding joins implies different
stores. Choose examples that make those decisions explicit for host developers.

1. Start with one normal host API: list documents using search, caller-selected
   stable sort and pagination, returning only rows the principal may view. Design
   the minimum host code before choosing abstractions. Include repository wiring
   and an HTTP list endpoint, keeping request identity/policy host-owned.
2. Compare bounded complete allowed-ID sets applied before database pagination,
   host-ordered candidates plus batched checks, and optional same-store SQL
   intersection. Measure sparse/dense access, large memberships, graph depth,
   scan/read counts and latency. Do not assume one strategy always wins.
3. Separate authorization-ID paging from business-result paging. A page of IDs
   sorted lexically is not a globally correct date/name/rank-sorted business page.
   A complete bounded ID set can be filtered/sorted/paged in SQL. Do not present
   an incomplete authorization ID page as the full permission filter.
4. Pin short/empty page, HasMore, scan budget, continuation and resume semantics;
   preserve per-row source cursors. Decide whether callers need explicit scan-bound
   metadata rather than conflating it with ordinary pagination. Verify no skipped
   or repeated rows with stable data and document changes between requests.
5. Handle unrestricted access distinctly from an empty allowed set. Review fail-
   closed handling of errors/exhaustion; retain host tenant/business filters when
   authorization is unrestricted. No unauthorized rows, totals or facet leakage.
6. Define consistency expectations for grant/revoke and resource changes during
   listing; separate bounded stale results from claimed snapshots. Review cursor
   query/model binding, rechecks and cache invalidation against host needs.
7. Counts, offsets, previous navigation and search facets require deliberate
   semantics. Do not forward unfiltered totals as authorized totals or promise
   efficient exact counts for arbitrary remote policies. Make supported behavior
   obvious in the API and examples.
8. Audit current documentation: its prefilter table says the engine orders and
   user-facing ordering is client-side. Distinguish paged-ID enumeration from a
   complete bounded set intersected in SQL, which can retain server-side order.
   Check read-cost claims and the compatibility-only Truncated field against source.
9. Exercise real SQL and an in-memory/remote-shaped boundary with the same host
   example, so portability and any SQL optimization have explicit limits. Finish
   with migration notes for implemented breaking changes, not this investigation.
10. Review continuation privacy: FilterPage can return the source cursor after a
    denied candidate. An opaque API value is not necessarily confidential; assess
    cursor payloads and candidate-existence hints from HasMore against host policy.
11. Distinguish bounded output from bounded database work. PostgreSQL descendant
    lookup currently recomputes the complete recursive closure per page. Capture
    that exception in measurements, alongside graph/intermediate-set budgets.
12. Exercise host placement and cursor ergonomics: a source must emit a cursor
    for each row, including composite sorts; FilterPage currently takes a concrete
    *authorization.Service and its private limits. SDK-only host domain logic
    cannot call it directly. Assess the smallest adapter or host-owned port that
    keeps the ordinary list method easy to use; promotion is a conclusion to test.
13. Recheck memoization claims: the historical batch plan describes reuse between
    pulls, while current FilterAuthorized constructs a fresh memo per invocation.
    Measure repeated parent/group reads across short pulls instead of assuming
    the plan's historical performance description still matches execution.
14. Require full semantic parity for a same-database SQL path: inherited grants,
    usersets, global roles, tenant restrictions and business joins producing
    duplicate rows must match authoritative checks. A direct-grant join alone
    does not implement the permission model.

## Source references checked for initial discussion

- Go package/layout convention: https://go.dev/doc/modules/layout and
  https://go.dev/ref/spec#Keywords (pkg is not a keyword; directories hold packages).
- SQL recursive traversal: https://www.postgresql.org/docs/current/queries-with.html.
- External authorization/search intersection:
  https://openfga.dev/docs/interacting/search-with-permissions. Its terminology
  uses prefilter relative to the search pipeline; state which set is filtered
  first when comparing it to Gopernicus's authorization-first prefilter wording.

The original brief was a source-location pass. The completed audit adds memory
boundary/race probes, actual PostgreSQL query plans, a three-strategy SQL fixture
and read-only consumer inspection. It does not claim consumer execution or a
production-ready full ReBAC SQL predicate. See the linked reports for exact
passes, measurements and unverified/deferred work.
