# authorization stores: baseline writes join the ambient `crud.Transactor` transaction

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

**Status:** RELEASED 2026-09-08 — PR #41 squash @ `495787c`; `pockets/authorization/v0.9.0` @
`495787c`, `stores/pgx/v0.5.0` + `stores/turso/v0.4.0` @ `a5ca592`, cold-verified (see the
Execution record). RATIFIED 2026-09-08 (owner, in-session: "release with your recommendations" —
Rulings 1–3 taken as proposed below). Built 2026-09-08 on branch
`authorization-stores-ambient-transaction` (checkpoint `21a8190`). Originally DRAFT 2026-09-08,
drafted from the segovia v2 tenancy
design (`segovia/.claude/plans/v2-tenancy.md`, D15 "application rows are canonical; navigational
tuples are an atomic projection", and the `authorization/stores/pgx` line under "Upstream flags").
It is the SECOND of the two upstream prerequisites that plan named; the first
(`DecisionView.CheckPermission`, `.claude/plans/authorization-decisionview-permission.md`)
shipped 2026-09-08 as `pockets/authorization/v0.8.0` + `stores/pgx/v0.4.0` +
`stores/turso/v0.3.0`. The three questions under "Rulings needed" were the
owner's; everything else is a proposal the owner may amend. (Ruled 2026-09-08: all three as proposed.)

## Context

The host-facing baseline writer, `authorization.RelationshipWriter`, is the trusted
application-side capability for topology projections: segovia's tenancy domain projects every
space row's containment into `space:S#tenant@tenant:T` and `space:S#parent@space:P` tuples
through `SetRelationTargets` (`segovia/v2/internal/outbound/domains/tenancy/authorization.go`).
The writer passes `ctx` straight to the relationship store, and the store ignores what the
context carries:

- `stores/pgx/relationships.go` runs every read and write on `s.db` (the pool) and
  `SetRelationTargets` opens ITS OWN transaction with `s.db.InTx` (advisory xact lock,
  conflict probe, delete-surplus, insert-missing). `stores/turso/relationships.go` is the same
  shape under `retryBusy` + `BEGIN IMMEDIATE`. Neither store calls the connector's
  `QuerierFrom(ctx)` or `TxFromContext(ctx)` anywhere.
- The connectors have carried the sdk transaction seam since transactor-connectors
  (2026-08-14; `pgxdb v0.2.0`, `turso v0.2.0`): `(*DB).Transact(ctx, fn)` begins a
  transaction, stashes the `*Tx` under a private context key, commits on nil and rolls back on
  error or panic; `TxFromContext(ctx)` retrieves it; `QuerierFrom(ctx)` answers the ambient
  transaction when there is one and the pool otherwise; a nested `Transact` fails loud with
  `ErrNestedTransact`. `InTx(ctx, fn func(*Tx) error)` is the older dialect-typed twin and
  ALWAYS begins a fresh transaction — it is what the stores use today.
- segovia's own app stores already take the querier from context (`s.db.QuerierFrom(ctx)` in
  `tenants.go` / `spaces.go`), and the Coordination Hub's compositions own the `Transact` call
  and hand the ambient ctx to their repositories (`coordination/cascade.go`, `query.go`,
  `review.go`, `threadservice.go`). The tenancy service was written so "the Transact wrap
  slots in when the upstream writer leg lands" (v2-tenancy.md, Leg 2 tasks).

So today a host that wraps `CreateSpace` in `Transact` gets exactly the split D15 forbids: the
space row is written on the ambient transaction and rolls back with it, while
`SetRelationTargets` commits on its own connection and STAYS. A tuple pointing at a row that
never existed, or a moved tuple beside an un-moved row, "can authorize through one container
while returning metadata from another" (D15). The plan's ruling was that item and space moves
do not ship until the store honors the ambient transaction. This plan makes it honor it.

## Goal

When the context handed to a baseline relationship write carries a connector-owned ambient
transaction, the write runs ON that transaction — no second connection, no nested begin — so
the host's application row and the tuple that projects it commit or roll back together. When
the context carries none, behavior is byte-for-byte what it is today.

Concretely, after this leg a segovia move is:

```go
err := s.tx.Transact(ctx, func(ctx context.Context) error {
	if err := s.spaces.Move(ctx, spaceID, newParentID); err != nil { return err }   // app row
	return s.topology.SetContainment(ctx, spaceID, tenantID, newParentID)          // tuples
})
```

and an error from either line — including the store's own `sdk.ErrConflict` from
`SetRelationTargets` — is returned by the callback, leaving BOTH the row and the tuple where
they were. Hosts must propagate write errors from the callback; returning nil requests commit.

## Out of scope

- **The guarded path.** `SystemMutator` / `ApplyGuarded` keeps its own `InTx` transaction, its
  anchor `FOR UPDATE` locks, receipts and replay ledger. Joining an ambient transaction would
  change receipt durability (a receipt rolling back with a host row) and the guardian's view of
  scope revisions; that is a separate design if demand appears. What this plan DOES do about it
  is D5 below: refuse loudly when a guarded mutation is attempted inside an ambient transaction,
  instead of splitting atomicity silently.
- **Protected teardown.** `SystemMutator.TeardownAuthorizationScope` stays the documented
  two-operation, cross-pocket contract (README "Resource teardown"; v2-tenancy D15 keeps
  deletion two-step: mark the row non-readable, then teardown with retry and a reason).
- **Nested-transaction semantics.** `Transact` inside `Transact` stays `ErrNestedTransact`; the
  stores never call `Transact` themselves, so they never trip it.
- **memstore.** It has no connector and no transaction concept; its mutex-atomic
  `SetRelationTargets` is unchanged and the new conformance family skips it loudly.
- **Read-side engine behavior.** The engine's checks and lookups are not redesigned; they merely
  run on the ambient querier when one is present (D2), which is what a host doing
  read-then-write inside one transaction expects.

## Design

### D1 — the port contract names ambient transactions

`domain/relationship.Storer` and `domain/role.Storer` gain a contract paragraph (doc only, no
signature change): *when the context carries the connector's Transact-owned transaction, every
method of the store runs on that transaction and never opens, commits, or rolls back one of its
own; the enclosing `Transact` decides the outcome from its callback's return value. Hosts must
return write errors from that callback to roll back the workflow. Outside one, behavior is
unchanged. A `SetRelationTargets` that must serialize concurrent callers does so with a lock scoped to the
ambient transaction, so the serialization lasts until the host's commit.* The
`RelationshipWriter` doc in `relationship_writer.go` and README §"Baseline desired-state
writer" restate it from the host's side, together with the guarded-path exclusion (D5).

The contract is dialect-agnostic on purpose: it is phrased in terms of "the connector's ambient
transaction", so a third-party store over another connector knows what to honor without the
port naming `pgxdb` or `turso`.

### D2 — every store method takes its querier from the context

Mechanical, in both dialect stores: every `s.db.Exec` / `s.db.Query` / `s.db.QueryRow` in
`relationships.go` and `roles.go` becomes `s.db.QuerierFrom(ctx).…`. Reads included: a host
that reads the current parent, decides, and writes inside ONE transaction must see its own
uncommitted state, and PostgreSQL would otherwise hand the read to a second connection that
cannot. `createRelationships(ctx, db pgxdb.Querier, …)` already takes a `Querier`, so
`CreateRelationships` passes `s.db.QuerierFrom(ctx)` and the helper is untouched.

The replacement also covers every helper argument that currently passes `s.db`: both dialects'
`relationTargets`, `createRelationships`, connector `List`, and `ExecAffecting`, plus turso's
`existsQuery` and `queryStrings`. In particular, `Unassign` uses `ExecAffecting`, and the
relationship and role listings use `List`; these are transaction escapes even though they do
not spell `s.db.Exec` or `s.db.Query`. Helpers retain their querier parameters; callers supply
the ambient querier. Audit every remaining `s.db` reference as specified in R1.

Roles are in scope for consistency (a store where relationship writes join the transaction and
role assignments do not is a trap), but they are the cheap half; see Ruling 2 if the owner
prefers relationships only.

### D3 — `SetRelationTargets` joins the ambient transaction instead of opening one

The reconciliation body (lock, conflict probe, delete-surplus, insert-missing) is lifted out of
the `InTx` closure into a function over the connector's `*Tx`. Dispatch:

```go
if tx, ok := pgxdb.TxFromContext(ctx); ok {
	return s.setRelationTargetsTx(ctx, tx, …)   // ambient: no begin, no commit, no rollback
}
return s.db.InTx(ctx, func(tx *pgxdb.Tx) error { return s.setRelationTargetsTx(ctx, tx, …) })
```

Two dialect points, both deliberate:

- **pgx: the advisory lock widens to the host's transaction.** `pg_advisory_xact_lock` is
  released at the end of the transaction it was taken in. Inside an ambient transaction that is
  the host's commit, so a concurrent `SetRelationTargets` on the same key from another session
  waits for the whole workflow. This is the property D15 wants (a competing move cannot slip
  between the row and the tuple), and it is what the contract paragraph in D1 promises. A host
  that holds a transaction open for a long time serializes its peers for that long; documented
  in the upgrade note, not mitigated.
- **turso: no `retryBusy` on the ambient path.** `retryBusy` re-runs the whole `InTx`, which is
  only sound when the store owns the transaction. Inside a host's `BEGIN IMMEDIATE` transaction
  the write lock is already held, so `SQLITE_BUSY` is not expected; if one surfaces it is
  returned as-is. The host must return it from the `Transact` callback to roll back. The
  standalone path keeps its retry loop unchanged.

An error from the body on the ambient path — the conflict sentinel `sdk.ErrConflict` included
— is returned to the host, which must propagate it from the `Transact` callback to roll back
the row and tuple together. The store does not mark the transaction rollback-only. In
particular, the conflict probe returns a Go error after a successful SELECT; it does not put
PostgreSQL in an aborted-transaction state. If the host handles that error and returns nil,
`Transact` commits preceding work. Document this obligation in the port and host-facing docs;
do not introduce savepoints or store-owned rollback on the ambient path.

### D4 — conformance proves the join, not just the rollback

A rollback alone proves nothing (a write that never happened also "rolled back"). The new
storetest family proves the write is IN the transaction from both sides, and it needs a
transactor, which the current `Run(t, newRepos)` signature cannot supply. Additive entry point
in `storetest`:

```go
// RunTransactional executes the ambient-transaction family. newRepos returns a
// FRESH Repositories and the crud.Transactor of the SAME connector the
// repositories were built over. A nil transactor skips the family loudly.
func RunTransactional(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor))
```

(`storetest` already imports `sdk/foundation/crud`; guards G2/FS1 are untouched — no driver
enters the suite.) Create the fixture once per leaf test, before entering `Transact`. For
outside visibility checks, use those SAME repositories with the original, nonambient context;
the store then selects the pool and another connection. Do not call `newRepos` to obtain an
observer: the existing fixtures clear tables, which can destroy seeded state or block against
the active transaction. The fixture must permit at least two simultaneous connections, and
visibility checks use bounded contexts so a connection or locking mistake fails promptly.

Specs, each over a fresh store:

| spec | proves |
|---|---|
| `CreateJoinsTransaction` | `Transact{CreateRelationships; return injected error}` → tuple absent afterwards; `Transact{CreateRelationships; nil}` → present. |
| `SetRelationTargetsJoinsTransaction` | same two shapes for `SetRelationTargets`, plus: `GetRelationTargets` on the SAME store sees the new state with the ambient context and the old state with the original, nonambient context until commit. This is the two-sided proof of the join. |
| `SetRelationTargetsConflictRollsBackHostWork` | `Transact{CreateRelationships(A); return SetRelationTargets that conflicts}` → conflict error surfaces from `Transact` AND tuple A is gone. Propagating the store's conflict undid the host's earlier write in the same transaction. |
| `DeletesJoinTransaction` | separately exercise `DeleteRelationshipTarget`, `DeleteRelationship`, `DeleteResourceRelationships`, and `DeleteByResourceAndSubject`: the ambient read sees the deletion, while rollback leaves the seeded tuples in place. |
| `RolesJoinTransaction` | separately exercise `Assign` and `Unassign`: ambient reads see each change, rollback restores the prior state, and a successful callback persists it (skipped when `Roles` is nil, or dropped under Ruling 2). |
| `ReadsJoinTransaction` | after uncommitted relationship and role changes, every read method sees the ambient state while the same method with the original context sees committed state. Cover the helper-backed paths enumerated below; skip nil kinds independently. |
| `MutationRefusesAmbientTransaction` | separately call `Apply` and `ApplyGuarded` inside `Transact`: the error matches both `ErrGuardedInsideTransaction` and `sdk.ErrInvalidInput`, the receipt is nil, no guard or semantic validator runs, and no mutation tuples or roles change. Returning the refusal from the callback also rolls back a preceding baseline write. Adapter-local assertions additionally verify unchanged revision anchors and receipt rows. Skip when `Mutations` is nil. |
| `StandaloneUnchanged` | with no ambient transaction, `SetRelationTargets` still serializes concurrent callers and still rolls back its own conflict (re-runs the existing `SetRelationTargetsConcurrentCallsDoNotUnion` / `…ConflictRollsBack` bodies through the new dispatch). |

`ReadsJoinTransaction` covers `CheckRelationWithGroupExpansion`, `CheckBatchDirect` (both
bounded and unbounded branches for each), `CheckRelationExists`, `GetRelationTargets`,
`CountByResourceAndRelation`, both relationship listings, `LookupResourceIDs`,
`LookupResourceIDsByRelationTarget`, and `LookupDescendantResourceIDs`. For roles it covers
`HasExactRole`, `ListBySubject`, `ListByResource`, and `ListEffectiveByResource`. Listing cases
include `WithCount` and a cursor follow-up so connector count and reverse-probe queries also
use the ambient querier. The refusal case checks mutation state while the outer transaction
is still open as well as after rollback; it must not rely on rollback to hide mutation effects.
Because `MutationRepository` exposes no anchor or receipt reader, put direct SQL assertions for
those tables in each adapter's `conformance_test.go`, alongside the shared suite registration.
This keeps drivers out of `storetest` and avoids adding a production port for test inspection.

Registered from each store's conformance test beside `storetest.Run`: pgx under
`POSTGRES_TEST_DSN`, turso under `-tags=integration` + `TURSO_DATABASE_URL`. The pocket's own
hermetic `go test ./...` has no transactor for memstore and skips the family with the same loud
`t.Skip` the nil-kind paths use.

**The segovia-shaped live test** the tenancy plan asked for ("a live test proving an app row
write plus its tuple roll back together") is pgx-only and lives beside the other deterministic
pgx tests (`stores/pgx/ambient_live_test.go`): the test creates a throwaway app table in the
test schema (`app_spaces(id text primary key, parent_id text)`), then

1. `Transact{INSERT app row; SetRelationTargets(space#parent); return injected error}` →
   neither the row nor the tuple exists;
2. `Transact{INSERT app row; SetRelationTargets; nil}` → both exist;
3. during (2), before the closure returns, a second pool connection sees NEITHER, and a
   concurrent `SetRelationTargets` on the same key from that connection blocks until the
   `Transact` commits (channel-timed, no sleeps), and afterwards the stored targets are the
   second caller's, never a union — the widened advisory lock in D3, proven.

### D5 — guarded mutations refuse to run inside an ambient transaction

`stores/pgx/mutations.go` `apply` (and the turso twin) checks `TxFromContext(ctx)` before
`InTx`; if a transaction is present it returns `mutation.ErrGuardedInsideTransaction`
(wrapping `sdk.ErrInvalidInput`, defined in `domain/mutation`, re-exported from
`codes.go` beside `ErrStaleRevision`) and touches nothing. Rationale is the seam's own
nesting rule: a guarded mutation on its own connection inside a host transaction is a silent
atomicity split — the receipt and tuples commit even when the host rolls back — and "silent
behavior cannot become an error" later, while an error can graduate into defined behavior
(joining) if a consumer needs it. No known host does this today (the Hub writes no tuples;
segovia's only guarded call, the `tenant#owner` seed in `CreateTenant`, runs outside any
`Transact` and stays there — its `DeriveMutationID` makes a retry after a lost row a replay,
which is the designed recovery). Ruling 3 covers this.

The refusal is in the shared `apply`, so it covers both trusted `Apply` (including protected
teardown) and `ApplyGuarded`. D4 tests both entry points and verifies that neither a guard nor
a semantic validator runs, and that no mutation state changes before the refusal is returned.

## Tasks

| # | task | files | verify |
|---|---|---|---|
| 1 | Port contract paragraph on `relationship.Storer` and `role.Storer`; `RelationshipWriter` doc; README §"Baseline desired-state writer" gains "Ambient transactions"; `mutation.ErrGuardedInsideTransaction` + re-export. | `domain/relationship/relationship.go`, `domain/role/role.go`, `relationship_writer.go`, `README.md`, `domain/mutation/*.go`, `codes.go` | builds; doc reads from a third-party store author's chair |
| 2 | `storetest.RunTransactional` and the eight specs in D4 (`storetest/transactional.go`), including every baseline write/read method and both mutation refusal entry points. Use the same fixture with ambient/nonambient contexts for visibility checks. The two standalone bodies are factored so `Run` and `RunTransactional` share them. | `storetest/storetest.go`, `storetest/transactional.go` | pocket `go test ./...` green with the family skipped for memstore |
| 3 | pgx: D2 querier-from-context across `relationships.go` and `roles.go`; D3 dispatch + lifted body for `SetRelationTargets`; D5 refusal in `mutations.go`. Register `RunTransactional` in `conformance_test.go`. | `stores/pgx/{relationships,roles,mutations}.go`, `stores/pgx/conformance_test.go` | `POSTGRES_TEST_DSN` conformance green incl. the new family; existing deterministic live tests unchanged |
| 4 | pgx: `ambient_live_test.go` — the segovia-shaped row+tuple test and the widened-lock proof (D4, last paragraph). | `stores/pgx/ambient_live_test.go` | green under `POSTGRES_TEST_DSN`; skips loudly without it |
| 5 | turso: D2 + D3 (ambient path without `retryBusy`) + D5. Move the connector pin `integrations/datastores/turso v0.1.0 → v0.3.0` — **v0.1.0 predates the seam** (Transact/QuerierFrom arrived in v0.2.0), so the store cannot compile against its own pin with `GOWORK=off` until it moves; v0.3.0 is the current tag (list search; no behavior change relevant here). Register `RunTransactional`. | `stores/turso/{relationships,roles,mutations}.go`, `stores/turso/go.mod`, `stores/turso/conformance_test.go` | `-tags=integration` conformance green incl. the new family (the pre-existing `TestSchemaProbe` playground failure is known and unrelated) |
| 6 | Release train (see Rulings 1): core tag, proxy poll, store pins to the new core (+ the turso connector pin from task 5), store tags, cold verify `GOWORK=off` from a fresh `GOMODCACHE`, RELEASING.md entry + `stores/UPGRADE.md` note, plan copied to `plans/`. | `RELEASING.md`, `stores/UPGRADE.md`, `stores/*/go.mod` | `go build ./...` per module with `GOWORK=off` against the pinned tags |

Task order 1 → 2 → 3 → 5 (4 alongside 3); the two dialects are independent once 1 and 2 land.

## Risks

- **R1 — the mechanical swap misses a call site.** A method left on `s.db` is a method that
  silently escapes the transaction. Mitigation: after task 3/5 audit EVERY `s.db` reference in
  `relationships.go` and `roles.go`, including helper arguments. Only `QuerierFrom(ctx)` and
  the standalone `SetRelationTargets` `InTx` dispatch may remain. Separately audit `m.db`
  references in `mutations.go`: its own `InTx` must be behind D5's refusal. The D4 specs cover
  every baseline write and read method, including indirect helper paths.
- **R2 — PostgreSQL aborted-transaction state.** Once a statement fails inside the ambient
  transaction, every later statement on it fails with 25P02 until rollback. The stores already
  return the first error and never continue past it; the host must return that error from the
  `Transact` callback. A Go-level conflict from a successful probe does NOT abort PostgreSQL;
  swallowing it and returning nil commits prior host work (D3). Document; no store-side
  recovery or rollback-only marker attempted.
- **R3 — the widened advisory lock.** A host that does slow work inside `Transact` after
  `SetRelationTargets` serializes every peer on that key for the duration. Acceptable and
  documented (D3); the ambient live test proves the blocking is bounded by the commit, not
  leaked.
- **R4 — turso busy inside an ambient transaction.** Without the retry loop a `SQLITE_BUSY`
  surfaces to the host. `BEGIN IMMEDIATE` makes it unexpected; if the playground shows it, the
  fix is a bounded statement-level retry on the ambient path, not a transaction re-run.
- **R5 — D5 is a behavior change for an unknown host.** Any host today calling `SystemMutator`
  inside `Transact` starts getting `ErrGuardedInsideTransaction`. None known; the upgrade note
  names the sentinel and the fix (move the guarded call outside the transaction, or file for the
  joining design).
- **R6 — third-party stores.** None exist; the contract paragraph is still an obligation on
  future store authors and belongs in the upgrade note with the conformance family they must
  pass.

## Rulings needed (owner)

1. **Version.** Proposal: `pockets/authorization/v0.9.0` (the port contract expands for store
   authors and `storetest` gains an exported entry point — a host-contract expansion by the
   repo's own minor rule, even though no signature changes) with `stores/pgx/v0.5.0` and
   `stores/turso/v0.4.0` (behavior change: writes join the ambient transaction; the turso
   connector pin moves to v0.3.0). Store pins `pockets/authorization v0.8.0 → v0.9.0`. The
   defensible alternative is core `v0.8.1` (doc + test-only) if the owner reads the port
   paragraph as documentation of existing intent rather than new contract.
2. **Roles in or out.** Proposal: IN (D2, one mechanical file per dialect, one spec). Out
   leaves role assignment pool-bound beside transaction-joining relationships.
3. **D5 refusal.** Proposal: refuse loudly with `ErrGuardedInsideTransaction`. Alternative:
   leave the guarded path silently on its own connection and document the split. The seam's
   nesting ruling argues for refusing.

## Consumers

segovia v2 leg 4 (`segovia/.claude/plans/v2-tenancy.md`, D15, "Moves and item topology"):

- `tenancy.Config` gains `Transactor crud.Transactor` (the host passes its `*pgxdb.DB`);
  `CreateSpace` and the new `MoveSpace` wrap row-write + `SetContainment` in one
  `Transact`. The adapter's `SetContainment` needs no change — it already passes `ctx`
  through. The "loud partial failure" interim in Leg 2 is retired by the wrap.
- Precondition, CONFIRMED 2026-09-08 in `segovia/v2/cmd/server/main.go`: one `pgxdb.Open`
  result is the `db` handed to both `AuthorizationRepositories(db)` and `mountTenancy(…, db, …)`,
  so the tenancy stores and the authorization store share one pool and therefore one ambient
  transaction. (Two pools over one DSN would never share a transaction; that is not the case.)
- `SeedTenantOwner` (guarded) stays OUTSIDE `Transact`; after D5 the host would be told if it
  slipped inside.
- Pin bump: `pockets/authorization` and `stores/pgx` to the tags from Ruling 1.

## Verify (when built)

```sh
# run from the repository root; each module command keeps that working directory
# pocket, hermetic (memstore; transactional family skipped loudly)
(cd pockets/authorization && go build ./... && go vet ./... && go test ./...)
# pgx, live (disposable postgres:17, the same shape the v0.8.0 train used)
(cd pockets/authorization/stores/pgx && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5439/postgres?sslmode=disable' go test ./... -run 'TestConformance|TestTransactional|TestAmbient' -v)
# turso, live
(cd pockets/authorization/stores/turso && TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -tags=integration -timeout 25m ./...)
# audit all DB references, including helper arguments (R1)
# relationship/role stores: only QuerierFrom(ctx) and standalone SetRelationTargets InTx
rg -n 's\.db' pockets/authorization/stores/{pgx,turso}/{relationships,roles}.go
# mutation stores: each InTx must follow the ambient-transaction refusal (D5)
rg -n 'm\.db|TxFromContext' pockets/authorization/stores/{pgx,turso}/mutations.go
# cold verify after tagging
GOWORK=off GOMODCACHE=$(mktemp -d) go build ./...   # in each store module
```

## Execution record (2026-09-08)

Tasks 1–6 executed as specified, under Rulings 1–3 as proposed (core `v0.9.0`,
stores pgx `v0.5.0` / turso `v0.4.0`; roles IN; D5 refuses).

- **Task 1** — contract paragraphs on `relationship.Storer` and `role.Storer`;
  `RelationshipWriter` doc; README "Ambient transactions — the row and its tuple
  commit together" + a `Transactional/*` bullet in "Store parity";
  `mutation.ErrGuardedInsideTransaction` (wraps `sdk.ErrInvalidInput`) re-exported
  from `codes.go`.
- **Task 2** — `storetest/transactional.go`: `RunTransactional` and the eight
  specs (`CreateJoinsTransaction`, `SetRelationTargetsJoinsTransaction`,
  `SetRelationTargetsConflictRollsBackHostWork`, `DeletesJoinTransaction` × 4
  methods, `RolesJoinTransaction` Assign/Unassign, `ReadsJoinTransaction` over
  every read method incl. both CTE budget branches and count+cursor listings,
  `MutationRefusesAmbientTransaction` for `Apply` and `ApplyGuarded`,
  `StandaloneUnchanged`). Outside reads use the SAME fixture with a 15s-bounded
  transaction-free context. The two standalone bodies are factored into
  `specSetRelationTargetsConcurrentCallsDoNotUnion` /
  `specSetRelationTargetsConflictRollsBack`, shared by `Run`. memstore registers
  the family with a nil transactor (loud skip).
- **Task 3** — pgx: every statement/helper/`List`/`ExecAffecting` on
  `s.db.QuerierFrom(ctx)`; `setRelationTargetsTx` over the open `*pgxdb.Tx`
  with `TxFromContext` dispatch; D5 refusal in `apply` before `cmd.Validate`.
  `TestTransactional` + `TestTransactionalRefusalLeavesLedgerUntouched`
  (direct-SQL receipts/anchors/revision sum, checked while the host transaction
  is open and after) in `conformance_test.go`.
- **Task 4** — `stores/pgx/ambient_live_test.go`
  `TestAmbientRowAndTupleCommitTogether`: throwaway `app_spaces` table in the
  test schema; injected error → neither row nor tuple; nil → both; while open, a
  pool reader sees neither and a competing `SetRelationTargets` blocks on the
  advisory lock (observed via `pg_stat_activity wait_event = 'advisory'`, no
  fixed sleeps) until the commit, after which the targets are the competitor's
  (`[P2]`, never a union).
- **Task 5** — turso: same swap (`queryStrings`/`existsQuery` take
  `tursodb.Querier`); `setRelationTargetsTx`; ambient path bypasses
  `retryBusy`; D5 refusal; connector pin `v0.1.0 → v0.3.0`; `TestTransactional`
  + ledger test under `-tags=integration`.
- **Task 6** — PR #41 squash @ `495787c` → `pockets/authorization/v0.9.0` @
  `495787c` (proxy served it on the first poll) → store repins
  `v0.8.0 → v0.9.0` @ `a5ca592` → `stores/pgx/v0.5.0` + `stores/turso/v0.4.0`
  @ `a5ca592` → cold `GOWORK=off` build + vet of all three modules from a fresh
  `GOMODCACHE` → RELEASING.md train entry + upgrade note; `stores/UPGRADE.md`
  store-port note shipped in the PR.

Verification: pocket hermetic green (family skipped loudly on memstore); pgx
live default + `POSTGRES_TEST_SCHEMA` legs green; turso playground:
`TestConformance` green (1466s — it now needs its own invocation; `-timeout 25m`
for the whole package kills the run mid-`TestTransactional`), `TestTransactional`
and every other live test green, `TestSchemaProbe` = the known playground
constraint gap; `make guard` 22/22; R1 audit clean (only `QuerierFrom(ctx)` and
the standalone `InTx` dispatch remain in `relationships.go`/`roles.go`;
`mutations.go` checks `TxFromContext` before its `InTx`).

Observed during execution, not folded: the first connect to the playground can
fail the migration status check with a transient "failed to execute SQL" —
rerun before diagnosing.
