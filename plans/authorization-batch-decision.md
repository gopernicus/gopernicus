# authorization: a SET-shaped decision — `FilterAuthorized` and `FilterPage` evaluate the candidate set per branch, not per candidate

**Module:** `pockets/authorization` (core) + `stores/{pgx,turso,memstore}` (new reader port
methods). Next tags: core minor, stores minor. No schema (the 0005 keyset index from
`authorization-lookup-paging` is sufficient; see A3).
**Status:** RATIFIED 2026-09-08 (owner: "do it"; R1 one train, R2 `CheckBatch` untouched, as recommended) — building on branch `authorization-batch-decision`. Origin: segovia v2 tenancy leg 6.3 (O13):
the owner's ruling "upstream should support batch lookups on B" after Path B was measured at
~60–80 ms per DENIED check against pgx, which made a sparse container page (12 allowed of 300
candidates) take ~18 s. segovia moved that listing to prefilter (v1's pattern); this plan makes
Path B honest for the cases the paging plan reserved it for.

## Context

`FilterAuthorized(principal, permission, type, ids)` is today `CheckBatch` over N
`CheckRequest`s (`decisionsvc/composite.go` `FilterAuthorized`): N independent evaluations of
the permission tree, each one walking every `AnyOf` branch against the store through the
per-resource reader port —

```go
type PermissionReader interface {
    CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
    GetRelationTargets(ctx, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error)
}
```

`FilterPage` (v0.10.0, B4) wraps that reader in a request-local memo of SUCCESSFUL immutable
reads, which helps only when candidates share targets (many items in one container reuse the
container's reads). A DENIED check is the expensive shape: every branch must be exhausted —
five direct relations, the userset expansion on `viewer`, `Through(parent, …)` up to the depth
cap, `Through(tenant, …)` — and each is a round trip. Measured on segovia's schema against a
581-tuple pgx store: ~60–80 ms per denied candidate, ~5 ms per round trip. The cost is the
NUMBER of round trips, not the data.

v1 (the monolith) had the same two patterns and the same lesson: segovia v1 moved its container
listings to prefilter after per-row checks produced a 2 s lag. Prefilter is the right tool for
"the principal's visible set is bounded"; but Path B exists precisely for the cases where it is
NOT (a huge tenant's manager listing a container, a search result, "my own rows"), and there it
must not cost N × (branches) round trips.

## Goal

Evaluate ONE permission for MANY resources of ONE type as a set: one store read per (branch,
hop) over the whole candidate set, instead of one per (candidate, branch, hop). Same answers as
N sequential `Check`s (Check/Filter parity), same budgets charged once per set instead of once
per candidate, no public API change beyond a documented cost model.

## Design

**S1 — the set reader port.** Two set-shaped methods beside the two per-resource ones:

```go
// FilterRelation reports which of resourceIDs carry relation for the subject,
// directly or through userset (group) expansion — the set form of
// CheckRelationWithGroupExpansion. Output is sorted, distinct, a subset of input.
FilterRelation(ctx, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error)

// RelationTargetsFor returns, for each of resourceIDs, the concrete targets of
// relation (the Through hop), in one read — the set form of GetRelationTargets.
RelationTargetsFor(ctx, resourceType string, resourceIDs []string, relation string) (map[string][]relationship.RelationTarget, error)
```

pgx: `WHERE resource_type = @t AND relation = @r AND resource_id = ANY(@ids)` plus the existing
group-expansion CTE seeded by the whole id set; the 0005 keyset index (type, relation,
resource_id) serves both. turso: `IN (…)` with the batch bound. memstore: a map walk.

**S2 — set evaluation in `authorizersvc`.** A new internal `evaluateSet(ctx, principal,
permission, type, ids)`: walk the compiled permission tree once; at each `Direct` branch call
`FilterRelation` over the REMAINING undecided ids (allowed ids leave the set as soon as one
branch admits them — `AnyOf` short-circuits per id); at each `Through(rel, perm)` hop call
`RelationTargetsFor` over the remaining ids, group by DISTINCT target, recurse `evaluateSet` on
the targets' type with the distinct target set, then map results back (an id is allowed if ANY
of its targets is). Depth, fan-out, and expansion budgets are charged against the SET
evaluation once, with the same limits as today; a budget overflow is `ErrEvaluationLimit` for
the whole call, as `CheckBatch` reports today.

**S3 — `FilterAuthorized` and `FilterPage` use it.** `Composite.FilterAuthorized` calls
`evaluateSet` when the owning kind is the relationship kind; the roles kind keeps its own
(already set-shaped) answer. `FilterPage`'s per-pull `FilterAuthorized` therefore becomes one
set evaluation per pull; the B4 memo stays for cross-pull reuse of Through targets. `CheckBatch`
keeps its per-request semantics (mixed permissions/types) and is untouched.

**S4 — parity is the proof.** A property test: for random schemas in the supported grammar,
random tuple sets, random candidate sets, `FilterAuthorized(ids)` == `{id : Check(id).Allowed}`,
and both raise `ErrEvaluationLimit` together. A benchmark pins the round-trip count: denied
candidates over a 5-branch permission with one userset and two Through hops cost
O(branches + hops) reads per set, not per candidate.

## Out of scope

- Cross-type or cross-permission batches (`CheckBatch` stays per request).
- The access-index follow-up from the paging plan (materialized reachability) — that is the
  structural answer for `LookupResources`; this plan is about the decision over a GIVEN set.
- Changing `FilterPage`'s scan bound semantics.

## Tasks

| # | task | done when |
|---|---|---|
| 1 | Port: `FilterRelation` + `RelationTargetsFor` on `PermissionReader` and `relationship.Storer`; memstore implementation; storetest specs (subset, sorted, distinct, group expansion, empty input). | core + memstore build; specs green |
| 2 | pgx + turso implementations on the 0005 index; live conformance. | both stores green live |
| 3 | `evaluateSet` with per-id `AnyOf` short-circuit, distinct-target `Through` recursion, budget charging once per set; `FilterAuthorized` routes through it. | parity property test green; `ErrEvaluationLimit` parity |
| 4 | Benchmark + README cost model ("a denied candidate costs a share of one read per branch, not a read per branch"); update the paging plan's choosing table: container listings are PREFILTER when the visible set is bounded (v1's rule), B for host-ordered candidate streams where the set is not bounded. | docs match |
| 5 | Tags: core minor + stores minor; RELEASING note; `plans/` copy. | cold-verified |

## Rulings needed (owner)

- **R1** — ship S1–S3 as one train (core + stores together, the store port changes) or land the
  port + memstore first and pgx/turso second. Recommendation: one train; the port change is the
  point.
- **R2** — keep `CheckBatch` per-request as is (recommended) or also route same-type,
  same-permission runs inside a `CheckBatch` through `evaluateSet`.

## Consumers

- segovia v2: none immediately (container listings are prefilter after O13). The first consumer
  is whichever list is genuinely unbounded for a manager — the paging plan's "huge tenant"
  case — and search results when they arrive.
