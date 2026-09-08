# authorization: `DecisionView.CheckPermission` — the hierarchy walk inside the guarded mutation

**Status:** RELEASED 2026-09-08 — PR #40 squash @ `3c446c0`; `pockets/authorization/v0.8.0` @ `3c446c0`, `stores/pgx/v0.4.0` + `stores/turso/v0.3.0` @ `9e5a713`, cold-verified (see the Execution record). EXECUTED 2026-09-05 on branch `authorization-decisionview-permission` (checkpoint `3483352`). RATIFIED 2026-09-05 (owner, in-session). Drafted 2026-09-04 from the segovia v2
tenancy design (`segovia/.claude/plans/v2-tenancy.md`, D13, owner-ruled: "make sure
DecisionView is traversing the ReBAC hierarchy"). Revised 2026-09-05 after review:
revision/read ordering, bounded userset expansion, roles-ownership wiring, and deterministic
concurrency coverage are required below. Ratification folded two findings from the
pre-ratification code survey (both marked **FOLD 2026-09-05** below): the legacy
`CheckRelation` carries the D4 snapshot hazard today and is fixed by delegation, and D5's
deterministic test is guard-driven rather than hook-driven. Rulings on the two open questions
are recorded under "Rulings".

## Context

The guarded mutation path (`MutationRepository.ApplyGuarded`) hands the host's `MutationGuard`
a `mutation.DecisionView` whose only relationship read is `CheckRelation`: a direct-tuple test
with exact-userset expansion on ONE scope, dependency-recorded. It cannot follow a `Through`.
So in any schema where authority is inherited from a container — segovia v2's
`dashboard.manage = owner | Through(space, manage)`, `space.manage = … | Through(parent, manage)
| Through(tenant, manage)` — the actor who legitimately manages an item (a space manager, a
tenant owner) holds no direct tuple on it, and the guard cannot see their authority. The
read-side `Check` has always walked the hierarchy; the guard side was left at the direct
primitive because every earlier host (coordination-hub, auth-cms) made the mutated resource
its own root.

The host's alternatives are both wrong in kind: re-walking the ancestor chain in host code
duplicates the schema's semantics and drifts from it, and dropping to the baseline writer
after a detached `Service.Check` reopens the check-then-write race the guarded path exists to
close. The traversal belongs in the engine. This plan puts it there, ONCE: the read-side walk
(`authorizersvc.Service.checkPermission` / `checkThrough` / `checkDirectRelation`,
`internal/logic/authorizersvc/service.go:197-323`) becomes generic over the two store-port
methods it already uses, and the mutation-side view supplies those two methods
dependency-recorded.

Facts the design rests on (surveyed 2026-09-04, source of record):

- The walk reads the store through exactly two methods:
  `CheckRelationWithGroupExpansion(ctx, rt, rid, relation, st, sid, maxExpansionStates)` and
  `GetRelationTargets(ctx, rt, rid, relation) ([]relationship.RelationTarget, error)`
  (`domain/relationship/relationship.go:279,283`). Depth, graph-state, fan-out budgets and the
  path-local cycle stack are per-call state on `budget` (`budget.go`), not on the store.
- All three `decisionView`s (`memstore/mutations.go:548`, `stores/pgx/mutations_eval.go:490`,
  `stores/turso/mutations_eval.go:451`) implement `CheckRelation` with expansion AND record every
  expansion scope (`recordExpansionScopes`). None has a tx-bound relation-targets read; the pgx
  and turso read-side `GetRelationTargets` run on the pool, not the tx. memstore's
  `GetRelationTargets` takes the store lock, which the view already holds.
- `composeGuard` (`mutation_service.go:130`) passes the store's view straight to the host guard
  and captures only `actor, guard, cmd`; `s.relationships *authorizersvc.Service` (the compiled
  schema + limits) sits unused at the call site (`mutation_service.go:477`).
- `authorization.DecisionView` is a type alias of `mutation.DecisionView`
  (`authorization.go`), which is what host guards name in `AuthorizeMutation`.
- pgx's `pgxdb.InTx` uses `pool.Begin` without selecting an isolation level
  (`integrations/datastores/pgxdb/tx.go`). Under the stock `READ COMMITTED` default,
  successive statements can observe different committed states. Merely using `v.tx`
  does not make a relationship read and a later revision read one snapshot.
- Read-side direct checks forward `MaxGraphStates` into userset expansion; the existing
  guard primitive is unbounded. Reusing it without a bound would change the decision's
  error/allow behavior, not just its cost.
- **FOLD 2026-09-05.** The legacy pgx/turso `CheckRelation` ALREADY carries the D4 snapshot
  hazard: it runs the EXISTS query in one statement and then re-runs the reachable CTE in a
  second statement (`recordExpansionScopes`) to record expansion scopes. A membership revoke
  committing between the two pairs a pre-revoke allow with post-revoke revisions, and
  validation accepts it. D4 fixes it by delegation (below); it is not a new surface.

## Goal

A host `MutationGuard` can ask `view.CheckPermission(ctx, scope, permission, principalType,
principalID)` and get the SAME answer the read-side `Check` gives for that principal on that
resource — direct relations, exact usersets, every `Through` hop, the same budgets — evaluated
inside the mutation transaction, with every scope the walk navigated recorded as a locked,
revision-validated dependency. Green under the pocket suite, the storetest oracle on all three
stores, and `make guard`; released as one `pockets/authorization` + `stores/{pgx,turso}` train.

## Out of scope

- Roles-kind pairs. `CheckPermission` answers relationship-model pairs only; a pair the
  `RoleModel` declares returns a stable error (see Open questions) — the guard keeps `HasRole`.
- A userset-valued principal at the guard (structurally impossible on the read side; stays so).
- Any change to `Apply` (trusted path), receipts, revisions, or the guardian.
- A `CheckExplain` trace inside the guard.
- Ambient-transaction baseline writes (segovia D15's other upstream ask) — a separate plan.

## Design

### D1 — one walk, generic over a two-method reader

`internal/logic/authorizersvc` gains an exported interface, the exact subset the walk uses:

```go
// PermissionReader is what the relationship walk reads through: the read-side
// store on Check, a transaction-bound dependency-recording view inside a
// guarded mutation. relationship.Storer satisfies it structurally.
type PermissionReader interface {
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error)
}
```

`budget` carries the reader for the call (`newBudget(limits)` → `newBudget(limits, reader)`);
`checkDirectRelation` and `checkThrough` read `b.reader` instead of `s.store`. `Check`,
`CheckExplain`, and the `LookupResources` budget constructor pass `s.store`;
`CheckBatch`'s sequential fallback and `FilterAuthorized` continue through their existing
check entry points. One new exported entry point:

```go
// EvaluateWith runs the ordinary permission evaluation for req over reader
// instead of the service's own store. Same schema, same limits, same walk.
func (s *Service) EvaluateWith(ctx context.Context, reader PermissionReader, req CheckRequest) (CheckResult, error)
```

No behavior change on the read side: the refactor is proven by the existing suite
(`hierarchy_test.go`, `userset_test.go`, `budget_test.go`, the storetest Check/Lookup oracle).

### D2 — the store port gains target reads and bounded expansion; the host view gains `CheckPermission`

Two interfaces where there was one, both in `domain/mutation`:

```go
// StoreDecisionView is what a MutationRepository supplies to ApplyGuarded's
// callback: the transaction-bound, dependency-recording primitives.
type StoreDecisionView interface {
	// CheckRelation retains the existing unbounded primitive for current callers.
	// FOLD 2026-09-05: every store implements it as CheckRelationBounded(…, 0), so
	// the snapshot-consistent dependency collection covers the legacy path too.
	CheckRelation(ctx, scope ScopeKey, relation, subjectType, subjectID string) (bool, error)
	// CheckRelationBounded uses the read-side expansion-state accounting and
	// overflow semantics, recording the resource and every expansion dependency.
	// Overflow returns relationship.ErrExpansionBudgetExceeded, never an allow
	// or a truncated deny. A non-positive bound retains the legacy unbounded mode.
	CheckRelationBounded(ctx context.Context, scope ScopeKey, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
	// RelationTargets returns the subjects holding relation on the resource named
	// by scope. Record its revision BEFORE reading targets, or obtain both from
	// one statement snapshot; never pair old targets with a newer revision.
	// Userset targets are returned as stored (the walk skips them).
	RelationTargets(ctx, scope ScopeKey, relation string) ([]relationship.RelationTarget, error)
	HasRole(ctx, scope ScopeKey, role, subjectType, subjectID string) (bool, error)
	Dependencies() []Dependency
}

// DecisionView is what a host MutationGuard receives: the store view plus the
// schema-driven permission walk the core composes over it.
type DecisionView interface {
	StoreDecisionView
	// CheckPermission reports whether principal holds permission on the resource
	// named by scope by the SAME evaluation the read-side Check performs, every
	// navigated scope recorded as a dependency. Budget exhaustion is
	// ErrEvaluationLimit (a command error: the mutation writes nothing), never a deny.
	CheckPermission(ctx, scope ScopeKey, permission, principalType, principalID string) (bool, error)
}

type Guard func(ctx context.Context, view StoreDecisionView) error   // was DecisionView
```

`authorization.DecisionView` stays the alias of `mutation.DecisionView`, so a host guard's
`AuthorizeMutation(ctx, attempt, view authorization.DecisionView)` compiles unchanged and gains
the method. `authorization.StoreDecisionView` is exported for store authors and tests.

### D3 — the core composes the permission view

`mutation_view.go` (package `authorization`):

```go
// permissionView is the DecisionView a host guard receives: the store's view
// plus CheckPermission, which drives the engine's one relationship walk over
// the view's two dependency-recording primitives.
type permissionView struct {
	mutation.StoreDecisionView
	eng *authorizersvc.Service
	roleModel *decisionsvc.CompiledRoleModel
}

func (v permissionView) CheckPermission(ctx context.Context, scope mutation.ScopeKey, permission, pt, pid string) (bool, error) {
	if scope.Kind != mutation.ScopeResource { return false, <stable invalid-input error> }
	// Validate scope and the concrete-principal CheckRequest before dispatch.
	if v.roleModel.DeclaresPermission(scope.Type, permission) {
		return false, ErrPermissionOwnedByRoles
	}
	if v.eng == nil { return false, ErrRelationshipsNotConfigured }
	res, err := v.eng.EvaluateWith(ctx, viewReader{v.StoreDecisionView}, authorizersvc.CheckRequest{...})
	return res.Allowed, err
}

// viewReader adapts the store view to PermissionReader. Forward maxExpansionStates
// unchanged to CheckRelationBounded; GetRelationTargets calls RelationTargets.
type viewReader struct{ v mutation.StoreDecisionView }
```

`composeGuard(actor, guard, cmd, eng, roleModel)` captures both `s.relationships` and
`s.roleModel`, and wraps the store view with `permissionView` using named fields.
`CompiledRoleModel.DeclaresPermission` is nil-safe. Check ownership before the nil-engine
branch so mixed-model and roles-only deployments return the same proposed
`ErrPermissionOwnedByRoles` sentinel (wrapping `sdk.ErrInvalidInput`) without reading a store.
Undeclared pairs retain the relationship engine's ordinary outcome when it is configured
(the same `CheckRequest` validation and unknown-symbol handling `Check` applies). The refusal
is RULED (see Rulings, 2026-09-05).

The adapter must preserve expansion overflow: the existing engine mapping converts
`relationship.ErrExpansionBudgetExceeded` into `ErrEvaluationLimit`. The host guard propagates
the error, and the mutation returns no receipt and changes no rows or revisions. Expansion
parity is part of this release; it cannot be deferred while promising the same decisions.

### D4 — each store implements transaction-bound reads with consistent dependencies

- memstore `RelationTargets`: `record(scope)` followed by a non-locking
  `getRelationTargetsLocked` helper shared with the read-side method. The view already holds
  `st.mu` for both reads.
- pgx / turso `RelationTargets`: call `record(scope)` FIRST, then select
  `subject_type, subject_id, subject_relation` through `v.tx`. Reuse the read-side query/helper
  and preserve its traversal ordering. Propagate either read's error. A previously recorded
  dependency keeps its first revision; do not replace it with a later observation.
- Implement `CheckRelationBounded` in all stores using the read-side bounded expansion
  semantics, including seed counting and the overflow signal. memstore uses its existing
  bounded expansion helper under the held mutex. SQL adapters reuse their bounded expansion
  logic on `v.tx`, with dependency collection from the same expansion result. In pgx, obtain
  the reached scopes and their revisions from the same statement snapshot as the bounded
  expansion; do not evaluate a boolean and then rerun expansion/revision reads against a
  potentially newer snapshot. Record the target resource before the grant read as well.
  Dependency collection must also remain bounded; do not follow a bounded decision with an
  unbounded expansion query. Preserve the existing unbounded `CheckRelation` contract.
- **FOLD 2026-09-05 — legacy `CheckRelation` delegates.** In all three stores `CheckRelation`
  becomes `return v.CheckRelationBounded(ctx, scope, relation, st, sid, 0)`. Bound 0 selects
  the unbounded CTE (the legacy semantics: never `ErrExpansionBudgetExceeded`), but the
  reached scopes and their revisions now come from the same statement snapshot as the
  grant read, closing the pre-existing two-statement hazard noted in Context. The pgx/turso
  `recordExpansionScopes` helpers are retired by this delegation.

The ordering matters: with read-then-record, a parent edge can be returned, revoked by a
concurrent transaction, and then paired with the post-revoke revision. Validation would
incorrectly accept that stale edge. Record-before-read detects any intervening revision
change; obtaining data and revision in one statement snapshot is also valid.

Dependency completeness requires both the full set of consulted scopes AND revisions
consistent with those reads. Commit locks and validates those dependencies as today. If a
revoke invalidates an observed dependency before validation, the write aborts as stale; if
the guard already observes the revoke, it denies. If the guarded write serializes before
the revoke, both operations may legitimately commit.

### D5 — concurrency tests prove the invalidating interleaving

The portable storetest races cover inherited folder-grant revocation and parent-edge
revocation. Assert that the revoke commits and the guarded mutation either applies or
aborts cleanly with no row or receipt. Permit both operations to commit when the guarded
write serializes first. These stress tests check outcome consistency but cannot, from the
final rows alone, distinguish a valid earlier write from a stale allow.

Add deterministic pgx coverage. **FOLD 2026-09-05 — the guard IS the interposition point;
no test-only hook goes into store code.** The test's repository-level `Guard` closure calls
the `StoreDecisionView` primitives in exactly the order the engine walk would, with the
pause between them:

1. `view.RelationTargets(dashboard, "space")` — the Through-only branch's first read.
2. Pause. A second connection revokes the `dashboard#space@space` parent edge through the
   ordinary `Apply` path and the test waits for that commit.
3. Resume: `view.CheckRelationBounded(space, "manage", actor, MaxGraphStates)` — the old
   target still leads to an otherwise valid grant; the guard returns nil (allow).

With record-before-read the dashboard dependency carries the pre-revoke revision, so commit
validation returns `ErrStaleRevision` with no committed mutation row, receipt, or mutation
revision bump. With read-then-record it carries the post-revoke revision and the stale allow
commits — the test fails. A second test forces an inherited folder grant (a membership edge
under an expansion scope) to be revoked after the guard's successful bounded read and before
validation, with the same stale outcome. Keep these interleaving tests pgx-specific:
memstore's mutex and turso's write serialization prevent a concurrent writer from committing
inside the guard boundary. They exercise the store primitives directly, which is why they
live in `stores/pgx` against the repository and not behind the host `CheckPermission`.

## Tasks

| # | task | files | verify |
|---|---|---|---|
| 1 | `PermissionReader`; `budget.reader`; `checkDirectRelation`/`checkThrough` read `b.reader`; `EvaluateWith`; update all budget constructors. No read-side behavior change. | `internal/logic/authorizersvc/{service,budget,model,explain,lookup}.go` | existing pocket suite green; a new unit test proves `EvaluateWith` over a fake reader equals `Check` over the store for direct, userset, Through, diamond, cycle, and budget cases |
| 2 | Split `StoreDecisionView` / `DecisionView`; `Guard` takes `StoreDecisionView`; add `RelationTargets` and `CheckRelationBounded`; document both view types and migrate repository callbacks/fakes. | `domain/mutation/repository.go`, `authorization.go` (aliases), `README.md`, affected tests | compiles once tasks 3–5 land |
| 3 | `permissionView` + `viewReader`; `composeGuard` takes the relationship engine AND compiled role model; forward the expansion bound; define the roles-owned refusal sentinel. | `mutation_view.go` (new), `mutation_service.go`, `codes.go` | host-guard tests: inherited allow, direct deny, all hop/expansion dependencies; mixed-model and roles-only ownership refusal before reads; malformed input; budget errors propagate without writes |
| 4 | memstore `RelationTargets` (record before non-locking read) and `CheckRelationBounded` (bounded expansion plus dependencies under the mutex); `CheckRelation` delegates with bound 0. | `memstore/{memstore,mutations}.go` | task-3 tests; real adapter parity at and over the expansion-state limit |
| 5 | pgx + turso `RelationTargets` (record before tx-bound read) and `CheckRelationBounded` with bounded, snapshot-consistent dependency collection (one statement returns grant + reached scopes + revisions); `CheckRelation` delegates with bound 0 and `recordExpansionScopes` is retired. | `stores/{pgx,turso}/mutations_eval.go`, shared expansion helpers as needed | live dialect tests; real adapter expansion parity; deterministic guard-driven pgx parent-edge and folder-grant revoke tests from D5 |
| 6 | storetest: `specGuardedPermissionThrough`, real `Service.Check`/host `CheckPermission` expansion-budget parity, and portable Through revoke races for folder grants and parent edges. Register in `runMutations`. | `storetest/mutations.go` | all three stores green; allow both commits when the guarded write serializes first; over-budget guards return `ErrEvaluationLimit` and persist no rows, receipts, or revision changes |
| 7 | Release train: `pockets/authorization/v0.8.0` (a store-port change: minor, with an UPGRADE note for third-party store authors and for host tests that hand-roll a `DecisionView` fake); `stores/pgx/v0.4.0`, `stores/turso/v0.3.0` with `go.mod` pins moved from `v0.6.0` to `v0.8.0`. RELEASING.md entry + UPGRADE.md notes. | `RELEASING.md`, `stores/*/go.mod`, `stores/UPGRADE.md` | `go build ./...` per module with `go.work` off (`GOWORK=off`) against the pinned tags |

## Risks

- **R1 — the read-side refactor.** Moving the store reference onto the budget touches the hottest
  path in the pocket. Mitigation: no logic moves, only the receiver of two calls; the full suite
  plus the storetest Check/Lookup parity oracle is the regression net.
- **R2 — expansion budget inside the guard.** The legacy `CheckRelation` remains unbounded,
  but `CheckPermission` MUST use `CheckRelationBounded` and forward `MaxGraphStates`.
  Mitigation: real guard-adapter parity tests on every store at and beyond the limit, for
  direct usersets and usersets reached through a container. Fake-reader parity alone does
  not cover this seam. Dependency collection must not reintroduce unbounded expansion.
- **R3 — memstore locking.** The view runs under the store mutex; the new read must be the
  non-locking sibling or the guard deadlocks. The task-3 tests on memstore catch it immediately.
- **R4 — third-party stores.** None exist; the port split is still a documented minor break for
  store implementers and for host tests with a hand-rolled `DecisionView` fake. Upgrade notes
  must cover both new primitives and the repository callback's `StoreDecisionView` signature.
- **R5 — PostgreSQL statement snapshots.** `v.tx` alone does not guarantee a fixed snapshot.
  Record-before-read for known scopes, consistent expansion/revision collection, and D5's
  forced revoke interleavings are release requirements. Portable races are supplementary.
- **R6 — roles ownership.** The relationship engine cannot identify roles-owned pairs.
  Capture the compiled role model explicitly and test refusal before any store read in
  mixed-model and roles-only deployments.

## Rulings (owner, 2026-09-05)

1. **Roles-owned pairs: REFUSE.** `CheckPermission` on a pair the `RoleModel` declares returns
   `ErrPermissionOwnedByRoles` (wrapping `sdk.ErrInvalidInput`), checked before any store
   read, in mixed-model and roles-only deployments alike. The guard keeps `HasRole` for
   those. Routing roles inside the mutation boundary is a separate feature if demand appears.
2. **Version: `pockets/authorization/v0.8.0`** (store-port change, minor pre-1.0) with
   `stores/pgx/v0.4.0` and `stores/turso/v0.3.0`, store pins `v0.6.0 → v0.8.0`.
3. **Both survey folds accepted** (legacy `CheckRelation` delegation; guard-driven D5 test).

## Consumers

segovia v2 leg 3 (`segovia/.claude/plans/v2-tenancy.md`, D13): the host guard becomes
platform-admin short-circuit, the change-shape matrix, then
`view.CheckPermission(scope, manage | manage_access, actor)`.

## Execution record (2026-09-05)

Tasks 1–6 executed as specified; task 7 prepared (docs written, pins NOT moved
— the pin move needs the core tag on the proxy first).

- **Task 1** — `PermissionReader`, `budget.reader`, `EvaluateWith`
  (`internal/logic/authorizersvc/{service,budget,explain,lookup}.go`);
  `evaluate_with_test.go` proves fake-reader parity for direct, userset,
  Through, diamond, cycle, depth, graph-state and expansion budgets, with the
  engine built over an EMPTY store so any leak to `s.store` would deny.
- **Task 2** — `StoreDecisionView` / `DecisionView` split, `Guard` takes
  `StoreDecisionView`, `authorization.StoreDecisionView` alias, README.
- **Task 3** — `mutation_view.go` (`permissionView`, `viewReader`),
  `composeGuard(actor, guard, cmd, engine, roleModel)`,
  `ErrPermissionOwnedByRoles`; `mutation_view_test.go` over memstore
  (inherited allow at 1 and 3 hops, userset through a container, deny writes
  nothing, roles refusal before reads in mixed + roles-only, malformed input,
  budget errors without writes, read-side parity oracle).
- **Task 4** — memstore `RelationTargets` (record → `getRelationTargetsLocked`)
  and `CheckRelationBounded` (`checkRelationExpandScopesLocked` gains the
  bound); `CheckRelation` delegates with bound 0.
- **Task 5** — pgx + turso: `relationTargets` shared by pool and tx through a
  `rowQuerier` seam; `CheckRelationBounded` is ONE statement (reached states +
  `COALESCE(revision,0)` + per-state grant EXISTS) with `recordObserved`
  (first revision wins); `recordExpansionScopes` retired. Deterministic pgx
  tests `stores/pgx/mutations_permission_live_test.go`: parent edge revoked
  after the target read → `ErrStaleRevision`, no row/receipt, revision
  reflects only the revoke; inherited grant revoked after the check → stale;
  bounded overflow parity with the read side at bounds 0/3/4/5/6 (the
  reachable set counts the target grant state: 5 for a 3-group chain).
- **Task 6** — `storetest/mutations_permission.go`:
  `GuardedPermissionWalksThrough`, `GuardedPermissionExpansionBudgetParity`
  (MaxGraphStates 4 → `ErrEvaluationLimit`, no row, no revision change; 5/6 →
  applied), `ConcurrentGuardedPermissionThroughRevokeRaces` (parent edge;
  inherited userset grant; 6 rounds each). Registered in `runMutations`.

Verification (all on the branch):

| check | result |
|---|---|
| `go build/vet/test ./...` in `pockets/authorization` | green |
| `stores/pgx` live (`POSTGRES_TEST_DSN`, disposable `postgres:17` on :5439) | green, incl. the 3 new deterministic tests |
| `stores/turso` live (`-tags=integration -timeout 25m`, playground DB) | new specs green (200s); ONE pre-existing failure `TestSchemaProbe` (two CHECK constraints absent on the playground tables) — reproduced identically on `main` in a worktree, unrelated to this train |
| `examples/auth-cms` build/vet/test, `workshop/gopernicus` build | green |
| `make guard` | green |

**MERGED, TAGGED (2026-09-08).** PR #40 squash-merged @ `3c446c0`;
`pockets/authorization/v0.8.0` tagged there and resolved on the proxy; store
pins moved `v0.6.0 → v0.8.0` (sdk `v0.5.0 → v0.7.0` by MVS) in `9e5a713` with
both stores building and vetting under `GOWORK=off`; `stores/pgx/v0.4.0` and
`stores/turso/v0.3.0` tagged @ `9e5a713`. Cold-verified: a throwaway module in
a fresh `GOMODCACHE` with `GOWORK=off` `go get`s all three tags from the proxy
and compiles `var _ authorization.StoreDecisionView = authorization.DecisionView(nil)`
with both stores imported. RELEASING.md stamped; this plan copied to `plans/`.
