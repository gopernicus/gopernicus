# Phase 1 — decision engine correctness

Status: DRAFT; ready after phase 0.
Depends on: all phase-0 tasks.

## Goal

Make the read side exact, immutable, bounded, deterministic, and internally
consistent before building new mutation behavior.

## Task AZ3-1.1 — exact relation-aware userset expansion across all readers

Touch: relationship reader port, engine, memstore, pgx/turso relationship SQL,
storetest.

Implement:

- Replace the hard-coded `member` expansion with relation-aware traversal driven
  by the exact userset relation declared on the grant and schema.
- Carry stored subject relation through group/userset expansion, direct and
  batch readers, lookup, relation existence, and every projection where it
  affects exact tuple identity. Decision callers remain concrete principals.
- Validate a created tuple against the full allowed `(subject type, relation)`
  pair. Concrete `group` is not accepted where only `group#member` is allowed;
  `group#admin` never matches `group#member`.
- Compile the permitted userset-relation expansion graph and make cycles
  relation-aware. A userset may itself contain another exact userset where the
  schema allows it: this is nested userset membership traversal, and it is in
  scope; v3 defines no userset rewrite operators (union/intersection/exclusion,
  computed/tuple-to-userset) and no userset-valued decision request. Relations
  used as navigational `Through` edges reject
  userset targets and operate only on concrete resource references.
- Update both SQL CTEs to include relation state in the recursive key so UNION
  de-duplication is cycle-safe without conflating usersets.
- Add adversarial cases for member/admin separation, missing relation rejection,
  nested mixed usersets, cycles per relation, and direct concrete group grants.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Userset|RelationAware|Nested|Cycle'
cd features/authorization/stores/pgx && go test ./...
cd features/authorization/stores/turso && go test ./...
make guard
```

Acceptance: the declared stored userset relation changes authorization outcomes
in all three implementations, no broader relation can satisfy a narrower one,
and a decision request cannot supply a userset.

## Task AZ3-1.2 — immutable compiled schema wiring and deterministic validation

Depends on: AZ3-1.1.
Touch: NewService, schema queries, compiler, tests.

Implement:

- Wire NewService exclusively to the phase-0 compiler; never retain caller data.
- Make `GetSchema` return a deep snapshot and add a stable digest accessor.
- Precompute sorted permission/relation indexes and Through target definitions so
  runtime does not range mutable maps.
- Reject ambiguous mixed-target Through rules rather than treating “permission
  exists on one target” as sufficient.
- Make schema composition duplicate/override/remove behavior explicit and
  deterministic; add tests for every merge case.
- Run concurrent mutation attempts against source/snapshot maps under `-race`.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Schema|Snapshot|Digest|Merge|Concurrent'
make guard
```

Acceptance: runtime policy is immutable and repeated compile of equivalent input
produces identical digest, validation errors, and schema projection.

## Task AZ3-1.3 — bounded traversal, cancellation, and path-correct cycle handling

Depends on: AZ3-1.1, AZ3-0.3.
Touch: check engine, lookup engine, reader port queries, all stores, tests.

Implement:

- Add one per-decision semantic budget object shared across nested checks.
  Charge expanded graph states/edges, targets, results, depth, and batch work.
  Query count is observed separately and may have an adapter-local emergency
  ceiling, but it is not part of cross-store outcome parity.
- Use path-local cycle detection for recursive Check and explicit memoization
  only where a completed sub-result is safe to reuse.
- In Lookup, distinguish active recursion stack from completed memoized results;
  never leave a visited key that causes a sibling Through relation to return an
  empty result.
- Check context cancellation before recursion and store calls.
- Bound SQL recursive expansion and result counts consistently with memory.
  If a dialect cannot return a reliable “limit exceeded” signal, revise the
  port/query rather than truncating silently.
- Pin depth boundary (`>=` versus `>`) and include exact boundary tests.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Budget|Depth|Fanout|Cancel|Diamond|SiblingThrough'
make guard
```

Acceptance: the same semantic budget produces the same allow/deny/error class on
memory, pgx, and turso; exhaustion never appears as an ordinary denial or
complete list. Adapter query telemetry need not be numerically equal.

## Task AZ3-1.4 — complete LookupResources and Check/Lookup parity

Depends on: AZ3-1.3.
Touch: lookup engine, relationship reader port/store queries, hierarchy tests,
storetest.

Implement:

- Close D1(c): seed self-referential descendant expansion from every root granted
  by the permission, including non-self Through-derived roots, not direct-only
  roots.
- Include the root IDs and descendants exactly once with deterministic ordering.
- Add a generic conformance oracle: for a finite fixture universe, each resource
  allowed by Check appears in LookupResources and each looked-up ID passes Check.
- Cover direct, group/userset, non-self Through, self hierarchy, mixed
  Direct/Through, multiple Through relations to one target permission, diamonds,
  cycles, max-depth, and limit exhaustion.
- Keep `FilterAuthorized` order-preserving and `CheckBatch` position-preserving.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Lookup|Parity|Hierarchy|Filter|Batch'
make guard
```

Acceptance: the documented Check/Lookup divergence is removed, not merely
reworded.

## Task AZ3-1.5 — effective role enumeration and validation symmetry

Depends on: AZ3-0.1.
Touch: role port/service/stores/storetest.

Implement:

- Add `ListEffectiveRoleGrantsByResource` that unions direct scoped assignments
  with global assignments satisfying scoped `HasRole`. Return explicit
  provenance (`direct`, `global`, or both) and de-duplicate one subject+role in a
  deterministic order before pagination; do not rewrite a global assignment as
  though it were stored at the requested resource scope.
- Keep direct-scope listing as a clearly named raw method if needed; do not call
  it effective.
- Apply the same subject/scope/role validation to assign, unassign, HasRole, and
  both listing families.
- Keep role names opaque in the core. Validate non-empty role/subject fields and
  exact global-or-fully-scoped shape symmetrically; host/admin policy owns any
  catalog of known names or allowed assignments.
- Add receipts/tests showing scoped revoke while the same global role remains as
  `same_role_grant_remains=true`. Do not claim generic access remains: the host
  may compose access from other role/ReBAC rules.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Role|Effective|Global|Scope|Catalog'
make guard
```

Acceptance: HasRole and effective enumeration describe the same grant set.

## Task AZ3-1.6 — stable decision reasons and bounded explain surface

Depends on: AZ3-1.2 through AZ3-1.5.
Touch: result vocabulary, engine, optional explain API, tests.

Implement:

- Replace free-form reason dependence with stable coarse reason codes; retain a
  human message only as non-contract debug text.
- Ensure batch reports the relation/path that actually granted.
- Sort lookup IDs, schema query output, and validation output.
- Add an opt-in bounded explain trace that records rule/path decisions without
  raw infrastructure errors. It shares the evaluation budget and is never
  automatically logged or exposed to ordinary callers.
- Prove explain cannot change the decision and fails with the same limit class.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Reason|Explain|Determin|Batch'
make guard
```

Acceptance: equivalent state produces stable results and an explain request
cannot create a separate, more permissive evaluator.

## Phase acceptance

- Exact usersets pass on memory and compile against both SQL adapters.
- Check/Batch/Filter/Lookup parity suite is green under `-race`.
- Every supported traversal is bounded and cancellation-aware.
- The schema is immutable and deterministic.
- `make check` and `make guard` pass.

## Stop conditions

- SQL needs to ignore userset relation to remain performant.
- Lookup can only bound work by returning unmarked partial results.
- Exact usersets require a hard-coded relation name.
- Explain needs a second evaluator.

## Execution log

Append only during execution.
