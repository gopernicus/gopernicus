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

### 2026-07-14 — AZ3-1.1 — exact relation-aware userset expansion across all readers — PASS (live-proven both dialects)

Outcome: stored userset relation is load-bearing at runtime in memory, pgx,
and turso. `group#admin` never satisfied by `group#member`; concrete-group
grants name only the group entity; nested exact usersets traverse; cycles are
relation-aware. Critical finding #1 closed on the read side.

- CTE shape (both dialects): recursive key is now the triple
  `reachable(atype, aid, arelation)` seeded `(subject_type, subject_id, '')`;
  each step joins `r.subject_{type,id,relation} = (atype,aid,arelation)` and
  emits `(r.resource_type, r.resource_id, r.relation)`; UNION dedups on the
  full triple (cycle-safe without conflating usersets); the hard-coded
  `WHERE relation = 'member'` is gone; grant-matching joins add
  `AND r.subject_relation = reachable.arelation`. memstore mirrors with a
  `[3]string` visited set (`expandSubjectGroups`→`expandReachable`).
- `ValidateRelation` now validates the full `(subject type, relation)` pair
  (signature gained `subjectRelation` on engine + root Service — only port-
  adjacent signature change; `relationship.Storer` port unchanged since
  relation-awareness reads the existing `subject_relation` column).
- Concrete-only tightening: `CheckRelationExists`,
  `LookupResourceIDsByRelationTarget`, `LookupDescendantResourceIDs` now
  require `subject_relation = ''`; `checkThrough` skips userset targets
  (defensive until AZ3-1.2 wires the compiler).
- storetest: fixtureSchema extended (group#admin, concrete group vs
  group#member/group#admin/org#member as distinct doc.viewer subjects); new
  shared cases NestedMixedUsersetsRelationAware,
  MemberAdminUsersetSeparation, CyclePerRelationIsRelationAware,
  RelationAwareConcreteGroupGrant, MissingUsersetRelationRejected; legacy
  cases converted off the decorative behavior. New hermetic root-package
  userset_test.go so the focused `-run` gate exercises the semantics.
- Verify — agent ran everything; orchestrator independently re-ran: hermetic
  module `-race -count=1` + focused filter green; LIVE pgx on fresh
  C-collation DB (`az3_verify`, TEMPLATE template0 LC_COLLATE 'C') with
  `-race -count=1`: all nine Adversarial relation-aware subtests RUN and PASS
  live, DB dropped after; LIVE turso (`http://127.0.0.1:8080`,
  `-tags=integration -race -count=1`) green; `make guard` exit 0; auth-cms
  green; both containers left running.
- Premise adaptations: (1) no reader-port signature change needed (concrete-
  principal boundary keeps relation-awareness store-internal). (2) Existing
  adversarial/memstore fixtures encoded the decorative bug (concrete-group
  grants reaching members) and were converted to exact usersets — the old
  assertions were the bug, not the contract. (3) Focused-filter gap closed
  with root-package named tests since shared cases nest under
  TestConformance. No stop condition hit; no schema/migration change needed.

### 2026-07-14 — AZ3-1.2 — immutable compiled schema wiring and deterministic validation — PASS

Outcome: `Compile` is now the sole construction boot gate; `Service` holds an
immutable `*CompiledSchema` (the `schema Schema` field is gone) and no runtime
path ranges the caller's mutable maps. Critical finding #2 (mutable
`GetSchema`) closed.

- compiler.go gained precomputed sorted `relationPermissions`/
  `relationTargets` indexes (derived, excluded from digest) + engine-facing
  read-only accessors. service.go: `NewService`→`Compile` (wrapped
  `ErrInvalidSchema`); Check/checkPermission/checkThrough/CheckBatch/
  ValidateRelation/GetPermissionsForRelation read compiled state; lookup.go
  walks precomputed Through targets. schema_validator.go: `validateThrough`
  now strict — Through permission must exist on EVERY possible resource
  target (mixed-target partial rejected, missing targets sorted).
- Public surface: `GetSchema() (SchemaSnapshot, error)` (was `(Schema,
  error)`; breaking, pre-tag sanctioned; auth-cms doesn't consume it),
  new `SchemaDigest() (string, error)`, root `SchemaSnapshot` alias.
- ValidateSchema decision: retained as an internal structural helper (not
  root-re-exported) sharing the cycle/unsatisfiable/Through helpers with
  Compile — no divergent validators (Compile is a strict superset), zero test
  churn; recorded in its doc comment.
- Merge semantics pinned + tested: duplicate type = union of members;
  colliding relation/permission = last-writer-wins; `Remove()` = delete;
  contributor order honored. Tests:
  TestNewSchemaMergeDuplicateTypeUnionsMembers,
  TestNewSchemaMergeOverrideReplacesRelation,
  TestNewSchemaMergePermissionOverrideAndRemove,
  TestNewSchemaMergeIsDeterministic; plus compile_strict_test.go
  (mixed-target reject/accept, projection/error determinism),
  wiring_immutable_test.go (non-retention; concurrent source mutation against
  live Service under -race), schema_snapshot_test.go (root snapshot copy-out,
  digest stability).
- Verify (agent ran; orchestrator re-ran): focused `-race -count=1` filter +
  full module `-race -count=1` green (7 pkgs); stores/pgx + turso hermetic
  build/vet/test green; auth-cms green; `make guard` exit 0. Live legs not
  run — no store SQL touched (phase-2 territory).
- Premise adaptations: (1) checkThrough userset skip retained as fail-closed
  defense with updated justification (off-schema data skipped, never
  traversed). (2) Strict all-targets Through broke no existing schema (the
  one multi-target fixture already conforms). (3) ValidateSchema retention
  choice as above.

### 2026-07-14 — AZ3-1.3 — bounded traversal, cancellation, and path-correct cycle handling — PASS (live-proven both dialects)

Outcome: one per-decision semantic budget charged across nested checks;
path-local cycle detection; sibling-Through visited-key suppression bug fixed;
cancellation checked before every store call/recursion; SQL lookup bounding
with a distinguishable overflow signal. All AZ3-0.3 enforcement obligations
discharged. High finding #7 (no work budget) and the lookup half of finding
#4 closed.

- New budget.go: `budget{limits, states map[stateKey]struct{}}` built once per
  top-level Check/LookupResources, threaded through
  checkPermission/checkThrough/lookupResources/lookupThrough. Charges:
  MaxGraphStates (distinct (resource,permission) states, diamond-deduped),
  MaxRelationTargets (per-hop fan-out), MaxBatchSize (CheckBatch/Filter
  rejected pre-store), MaxLookupResults (fetch cap = max+1, per enumeration
  node). Query count deliberately uncharged (adapter telemetry).
- Depth boundary PINNED: `depth > MaxThroughDepth → ErrEvaluationLimit`;
  depth counts Through hops from 0, so MaxThroughDepth = max permitted hops;
  == is the last permitted hop. Test TestDepthBoundaryExactlyThroughDepth +
  parity Budget/DepthBoundaryParity.
- Sibling-Through fix: lookup's single `visited` set (empty-result on any
  revisit) replaced by `stack` (active recursion path → cycle guard) + `memo`
  (completed results → safely reused; recursion is a DAG since the compiler
  rejects genuine Through cycles). TestSiblingThroughLookupNotSuppressed +
  Budget/SiblingThroughLookupParity.
- BREAKING port change (pre-tag): the three `Lookup*` reader methods gained
  `limit int`; stores return at most `limit` rows (SQL `LIMIT`, memstore cap);
  engine passes max+1 and treats a full fetch as overflow →
  `ErrEvaluationLimit`, never a truncated-complete slice. memstore + pgx +
  turso + all test fakes updated.
- 503 refinement DONE here (middleware.go): RequirePermission maps
  ErrEvaluationLimit → 503 (fail-closed), other errors stay 500.
- New tests — root: TestDepthBoundaryExactlyThroughDepth,
  TestBudgetGraphStatesExhaustion, TestDiamondGraphStateDedup,
  TestFanoutExhaustion, TestBudgetBatchSizeRejected,
  TestBudgetLookupResultsExhaustion, TestCancelBeforeStoreCall,
  TestSiblingThroughLookupNotSuppressed. storetest: Budget/
  {DepthBoundaryParity,FanoutParity,LookupResultCapParity,
  SiblingThroughLookupParity} + cap assertion in Relationship/Lookups.
- Verify — agent ran all; orchestrator independently re-ran: hermetic module
  `-race -count=1` (7 pkgs) + focused filter green; auth-cms green; LIVE pgx
  (fresh C-collation `az3_verify`, `-race -count=1 -v`): all four Budget
  parity subtests PASS live, DB dropped; LIVE turso (`-tags=integration
  -race -count=1 -v`): all four PASS live; `make guard` exit 0; containers
  left running.
- Premise adaptations: (1) check-path reachable EXISTS intentionally has no
  distinct-state cap (an EXISTS cannot reliably signal overflow-vs-deny
  across dialects; budget enforced at reliable seams; outcome parity kept).
  (2) Lookup result cap is per enumeration node, not a shared running total
  (first pass double-charged nested Through against parent — corrected).
  (3) Check-result memoization deferred as optimization (path-local stack is
  correct for OR-reachability; budgets bound work). (4) Cancellation returns
  the raw context error (fail-closed ordinary failure). (5) Superseded the
  "budget never threaded into a store" doc note for enumeration only.

### 2026-07-14 — AZ3-1.4 — complete LookupResources and Check/Lookup parity — PASS (live-proven both dialects)

Outcome: D1(b)/D1(c) divergence REMOVED (high finding #4 fully closed with
AZ3-1.3's sibling fix). Lookup now seeds self-referential descendant
expansion from every root the permission grants, including non-self
Through-derived roots. Bidirectional Check/Lookup oracle proves parity on all
three stores, live on both dialects.

- Design: lookupResources rewritten in two phases — (1) root set = union of
  every non-self grant (direct + Through to other types / same type under a
  different permission, recursing on a distinct memo key); (2) new
  `expandSelfHierarchy` walks descendants from the FULL root set for each
  same-permission self-referential relation (discriminator tightened to
  `targetType == resourceType && check.Permission == permission`, matching
  the compiler's findCycle sanction), with a fixpoint loop for interleaving
  multiple self relations. lookupThrough/lookupDirectOnly removed. NO
  port/store change — LookupDescendantResourceIDs already took a root-ID set;
  the engine's seeding was the bug.
- Deterministic ordering: LookupResources returns sorted-ascending IDs
  exactly once (AZ3-1.6's direction applied early; documented).
- Oracle: storetest/oracle.go `Parity` family wired into Run — finite
  fixture universe covering direct, nested userset, non-self Through, self
  hierarchy (direct- and org-seeded), mixed Direct/Through, two Through
  relations to one target permission, diamonds, cycles; asserts completeness
  (every Check-allow appears in Lookup) AND soundness (every looked-up ID
  passes Check) + sorted/exactly-once; generous limits so exhaustion cannot
  mask incompleteness; separate LimitExhaustionIsError subtest (tight cap →
  ErrEvaluationLimit, never truncation). FilterAuthorized order-preservation
  and CheckBatch position-preservation unchanged, still covered.
- Files: lookup.go rewrite; storetest/oracle.go (new); storetest.go wiring;
  hierarchy_test.go (boundary test flipped to expect descendants:
  TestHierarchyLookupOrgSeededRootExpandsDescendants; new
  TestHierarchyLookupMultipleSelfRelationsFixpoint); authorization.go +
  README.md doc updates (D1 boundary text replaced with parity-closed).
- Verify — agent ran all; orchestrator independently re-ran: hermetic module
  `-race -count=1` (7 pkgs) + focused filter green; auth-cms green; LIVE pgx
  (fresh C-collation DB, `-race -count=1 -v`): Parity/{CheckLookupOracle,
  D1cOrgSeededDescendants,LimitExhaustionIsError} all PASS live, DB dropped;
  LIVE turso same three PASS live; `make guard` exit 0; containers running.
- Premise adaptations: (1) no breaking port change needed. (2) self-loop
  discriminator tightened as above (same-type/different-permission Through
  now recurses on its own memo key instead of being treated as hierarchy).
  (3) sorted output landed here because the oracle depends on it.

### 2026-07-14 — AZ3-1.5 — effective role enumeration and validation symmetry — PASS (live-proven both dialects)

Outcome: medium finding "role decision and listing disagree" closed. HasRole
and the new effective enumeration describe the same grant set on all three
stores.

- Port: `role.Storer.ListEffectiveByResource(ctx, resourceType, resourceID,
  req) (crud.Page[EffectiveGrant], error)`; `EffectiveGrant{SubjectType,
  SubjectID, Role, Direct, Global}` with `Provenance()` →
  direct/global/both. Global grants are NEVER rewritten as scoped rows —
  provenance is reported, scope projection stays absent. Service:
  `ListEffectiveRoleGrantsByResource` (+ root wrapper and alias).
  `ListByResource` kept as the clearly-documented RAW direct-scope listing
  (no rename needed).
- Ordering/pagination: deterministic keyset on derived `grant_key` =
  (subject_type, subject_id, role) joined by chr(1), ascending, deduped
  before pagination; `EffectiveOrderFields`/`DefaultEffectiveOrder`;
  memstore mirrors SQL via new `pageMemByKey`. SQL: shared
  `effectiveRolesBaseSQL` GROUP BY with MAX(CASE…) provenance flags;
  dialect-local boolean literals.
- Validation symmetry: rolesvc validators split into
  validateSubjectFields/validateResourceScope/validateAssignment, applied to
  assign, unassign, HasRole, ListBySubject, ListByResource, and effective
  listing. Roles stay opaque (default #5) — structural shape only, no
  catalog. Global-request edge: requested scope global → no fallback branch,
  all grants Direct (mirrors HasRole).
- RECEIPTS SPLIT (explicit): `same_role_grant_remains` receipt FIELD waits
  for AZ3-3.3; this task landed the queryable truth beneath it —
  storetest `ScopedRevokeGlobalRoleRemains` proves post raw-scoped-unassign
  HasRole still grants via global fallback and effective enumeration
  re-attributes to `global` provenance. No claim that generic access remains.
- Tests: storetest Roles/{EffectiveEnumerationAgreesWithHasRole,
  ScopedRevokeGlobalRoleRemains, EffectivePagination};
  rolesvc TestEffectiveEnumerationAgreesWithHasRole,
  TestListValidationSymmetry.
- Verify — agent ran all; orchestrator independently re-ran: hermetic module
  `-race -count=1` (7 pkgs) + focused filter green; auth-cms green; LIVE pgx
  (fresh C-collation DB): three new Roles subtests PASS live, DB dropped;
  LIVE turso: same three PASS live; `make guard` exit 0.
- Premise adaptations: (1) effective listing pages by grant_key, not
  created_at (ambiguous under dedup) — extends the keyset convention. (2)
  EffectivePagination uses a fresh store: a global grant is effective for
  every scoped resource (real property surfaced by testing). (3) FLAGGED
  follow-up: auth-cms demo.go still demonstrates the RAW listing; switching
  it to the effective method is a phase-4 (AZ3-4.x) candidate, not silently
  changed here.

### 2026-07-14 — AZ3-1.6 — stable decision reasons and bounded explain surface — PASS (phase 1 gate green)

Outcome: stable coarse reason codes, batch grant-path fix, final sorting-sweep
gap closed, opt-in single-evaluator explain. Medium finding "results are
operationally unstable" closed.

- ReasonCode: `Reason` type moved to authorizersvc/reasons.go (engine is the
  lowest shared package; ErrEvaluationLimit precedent); root codes.go
  re-exports type + all constants as aliases; one coherent extension
  `ReasonGranted`. `CheckResult.ReasonCode` carries granted/denied only;
  free-text `Reason` kept as non-contract debug.
- Batch fix: checkBatchOptimized's allowedMap→`grantedBy map[string]string`
  recording the first direct check (compiled sorted order) that actually
  granted per resource — batch reasons no longer name a relation that did
  not grant (TestCheckBatchNamesActualGrantingRelation: owner-only grant now
  reports direct:owner, previously direct:editor).
- Explain: `CheckExplain(ctx, req) (CheckResult, Explanation, error)`;
  `Explanation{Decision, Steps []ExplainStep{ResourceType, ResourceID,
  Permission, Relation, Kind, Depth, Outcome}}` — coarse codes only, never
  raw infra errors. Single evaluator GUARANTEED structurally: Check and
  CheckExplain both call private `check(ctx,req,b)`; only difference is
  whether `budget.trace` is non-nil. Shares the one budget →
  TestExplainFailsWithSameLimitClass proves identical ErrEvaluationLimit;
  TestExplainCannotChangeDecision proves identical decisions. Opt-in method,
  not reachable from middleware, never auto-logged.
- Sorting sweep: confirmed already-sorted surfaces (lookup IDs, Compile
  errors, GetPermissionsForRelation, snapshot listings, validateThrough);
  ONE real gap fixed — ValidateSchema aggregated errors in map order, now
  dedupeSortStrings'ed (TestValidateSchemaErrorsDeterministicOrder).
  FilterAuthorized/CheckBatch deliberately left positional.
- New tests: TestReasonCodeStableGrantedDenied,
  TestExplainCannotChangeDecision, TestExplainTraceRecordsCoarseSteps,
  TestExplainFailsWithSameLimitClass,
  TestCheckBatchNamesActualGrantingRelation,
  TestValidateSchemaErrorsDeterministicOrder, TestExplainPublicSurface.
- Live legs NOT run — justified: engine + vocabulary only; no SQL/memstore/
  adapter/migration touched; hermetic store build/vet/test green.
- Verify — agent ran all; orchestrator re-ran: module `-race -count=1`
  (7 pkgs) green; five keystone tests fresh PASS; stores + auth-cms hermetic
  green; `make check` "all checks passed"; `make guard` exit 0.
- Premise adaptations: Reason type home = engine; ReasonCode deliberately
  coarse-binary (grant granularity lives in debug text + explain steps);
  explain step order follows evaluation order (not a contract — only the
  decision is deterministic).

### Phase 1 acceptance — 2026-07-14 — GREEN

All six tasks logged PASS. (1) Exact usersets pass on memory and both SQL
adapters (AZ3-1.1 live-proven). (2) Check/Batch/Filter/Lookup parity suite
green under -race incl. the bidirectional oracle (AZ3-1.4 live-proven). (3)
Every supported traversal bounded and cancellation-aware (AZ3-1.3
live-proven). (4) Schema immutable and deterministic (AZ3-1.2 + 1.6). (5)
Orchestrator re-ran `make check` ("all checks passed") and `make guard`
(exit 0) from repo root. No stop condition encountered. Phase 2 may begin.
