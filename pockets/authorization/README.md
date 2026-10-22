# Authorization

One tuple authority supports exact roles and optional relationship-based access
control (ReBAC). A subject can be both `owner` and `member` of a resource: adding
one fact never replaces the other. Applications choose how those facts grant
permissions. The pocket supplies no default allow policy and runs no migrations
at startup.

## Packages and capabilities

| Package | Owns |
| --- | --- |
| `authorization` | `New`, options, repositories and named components |
| `logic/tuples` | Canonical facts, exact raw reads, snapshots and trusted storage ports |
| `logic/roles` | Exact concrete membership, assignment listing and trusted role writer |
| `logic/relationships` | Resource-scoped fact facade, graph read ports and trusted relationship writer |
| `logic/decisions` | One model/compiler and evaluator for roles, graph traversal, All/Any, check/explain/batch/filter/lookup; `NewLoweredEvaluator` for guard policies over any protocol `Checker` |
| `logic/authorizer` | `Engine`: the implementation of record of `sdk/capabilities/authorization` over `decisions` (`Components.Authorizer`) |
| `logic/model` | Principals, resources, result/reason vocabulary and evaluation budgets |
| `logic/mutations` | Principal-free commands, tuple-shape validation and atomic integrity policy |
| `logic/audit` | Canonical committed changes, attribution and history reader |
| `logic/tuplecache` | Optional raw fact mirror runtime: `Policy`, `Source`, `Backend`, poll/rebuild |
| `inbound/http` | Composable authorization guards and optional role administration |
| `stores/memory`, `stores/storetest` | Reference authority and shared conformance suites |

PostgreSQL, Turso/SQLite and Redis adapters are separate modules. PostgreSQL and
Turso persist the authority; Redis provides an optional raw TupleCache mirror.
The authorization Firestore adapter is removed. The shared Firestore connector
and authentication Firestore adapter remain separate supported modules.

## Canonical identity

```go
fact := tuples.Tuple{
    Scope: tuples.On("project", "p1"),
    Relation: "owner",
    Subject: tuples.SubjectRef{Type: "user", ID: "u1"},
}
```

The complete scope, relation and exact subject identify a fact. `tuples.Global()`
is an explicit global scope; `tuples.On(type, id)` is an exact resource scope.
Zero scope and partially populated resource coordinates are invalid. Global
scope is never a wildcard or an implicit graph root.

A concrete `group:g1` and a userset `group:g1#member` are different subjects.
Reference strings are exact, valid UTF-8, control-free and at most 256 bytes;
there is no trimming or case folding. SQL stores one row in `iam_tuples`, unique
on the full identity. Exact re-insertion is idempotent; distinct labels coexist.
There are no separate role rows, synthetic fact IDs or creation timestamps.

`roles.Assignment.Tuple()` and `relationships.CreateRelationship.Tuple()` produce
the same identity. A resource role written through either facade participates in
model-permitted graph checks, Through traversal, lookup and integrity counts.
Writer origin never filters authorization reads.

## Exact roles need no model

```go
store := memory.New()
components, err := authorization.New(authorization.Repositories{
    Tuples: store.Tuples(),
})
if err != nil { return err }

principal := model.PrincipalRef{Type: "user", ID: userID}
resource := model.Resource{Type: "project", ID: projectID}
global, err := components.Roles.HasRole(ctx, principal, "admin")
scoped, err := components.Roles.HasRoleIn(ctx, principal, "owner", resource)
either, err := components.Roles.HasRoleInOrGlobal(ctx, principal, "admin", resource)
```

`HasRole` checks one exact global fact. `HasRoleIn` checks one exact resource
fact. Neither expands usersets, traverses relationships or consults a role
catalog. `HasRoleInOrGlobal` is the explicit convenience for both probes in one
snapshot. A global grant alone **does not** satisfy `HasRoleIn`.

Use one expression when several facts jointly control a decision; separate
calls joined with Go's `&&` may sample different committed states:

```go
result, err := components.Decisions.Evaluate(ctx, principal,
    decisions.All(
        decisions.Role("active"),
        decisions.Any(decisions.Role("admin"), decisions.RoleIn("owner", resource)),
    ),
)
```

`ListRoleAssignmentsBySubject` selects an exact concrete subject.
`ListRoleAssignmentsByScope` selects concrete facts in exactly that scope.
Listings are views of canonical facts, without writer provenance or implied
global applicability. No effective-grants API merges scopes.

## One optional permission model

```go
policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
    "project": {
        Relations: map[string]decisions.RelationDef{
            "owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
            "member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
        },
        Permissions: map[string]decisions.Expression{
            "view": decisions.Any(decisions.Direct("owner"), decisions.Direct("member")),
            "audit": decisions.RoleIn("auditor"),
            "manage": decisions.Any(decisions.Role("admin"), decisions.RoleIn("owner")),
        },
    },
}}
components, err := authorization.New(repos,
    authorization.WithModel(policy),
)
if err != nil { return err }
result, err := components.Decisions.Check(ctx, model.CheckRequest{
    Principal: principal, Permission: "view", Resource: resource,
})
```

`Check` and `CheckBatch` return `sdkauthorization.Decision{Allowed}`. The
decision's reason codes reach the decision log and `CheckExplain`, never the
result. `model.Resource`, `model.CheckRequest`, `model.LookupRequest` and
`model.PrincipalRef` are deprecated aliases of the `sdk/capabilities/authorization`
types (`PrincipalRef` is `sdk.Principal`), so nothing is converted; the
decision service validates them with the pocket's opaque-reference grammar
(`model.ValidateCheckRequest`, `model.ValidatePrincipal`), not their protocol
`Validate` methods.

`Role(label)` means exact global membership. `RoleIn(label)` within a named
permission means exact membership on the checked resource; ad hoc `Evaluate`
requires an explicit resource. `Direct(relation)` expands only model-permitted
exact usersets. `Through(relation, permission)` follows resource targets to their
permission. `Permission(name)` references another named permission. All and Any
compose these operations; global applicability must appear explicitly in policy.

The complete expression is validated before reads, including skipped branches.
Empty or malformed All/Any is invalid. Evaluation preserves declaration order:
All stops on false, Any on true, and an encountered error aborts. An unread branch
cannot introduce a datastore error. Graph cycles, depth, fan-out and work have
finite configured bounds. Limit exhaustion is `model.ErrEvaluationLimit`, an
indeterminate error wrapping `sdk.ErrUnavailable`; it never becomes an allow or
an apparently complete truncated result.

Model options capture maps/slices when created. Compiled models and their public
snapshots are immutable. Named permissions do not impose a role catalog:
structurally valid opaque labels remain legal. Explicit relation subject-shape
constraints apply to additions through every model-bound writer and mutation
facade. Raw tuple stores apply structural validation and the configured integrity
policy, without named-model shape constraints.

## Construction and snapshots

`Repositories.Tuples` is required and supplies roles, the resource fact facade
and decisions. `Mutations` adds the atomic mutation capability. `Audit` is an
optional history reader. `TupleSource` supplies authoritative snapshots and
committed changes to the optional TupleCache; the SQL adapters' `Repositories`
constructors populate it when their `WithTupleCache()` option is set. Without `Repositories.Mutations`, `Components.Mutations`
is nil and raw writers remain available. Direct `mutations.NewService(nil)`
returns `ErrMutationsNotConfigured`; it never constructs an unusable writer.
`Decisions`, `Roles` and `Relationships` are available without a named model.

Options replace whole values in order: `WithModel`, `WithLimits`,
`WithLogger`, `WithRoleRoutes`, and `WithTupleCache`. Nil options and typed-nil
dependencies fail construction. Limits default finite zero dimensions and reject
negative values. No constructor starts a worker or owns a database connection.

Direct construction uses the same services:

```go
checks, err := decisions.NewService(tupleStore, decisions.WithModel(policy))
if err != nil { return err }
roleReads, err := roles.NewService(tupleStore)
if err != nil { return err }
compiled, err := decisions.Compile(policy)
if err != nil { return err }
writes, err := mutations.NewService(mutationRepository,
    mutations.WithModel(compiled),
)
if err != nil { return err }
```

Mutation services accept an optional compiled shape model and `WithLimits`; they
do not require a decision service or a principal. `relationships.NewService`
accepts the canonical tuple store and an optional
`WithValidator(checks.CompiledModel())`. Its result separates read service and
trusted writer. `roles.NewWriter` offers the same optional validation seam.
Root composition binds the model to both writers.

All reads contributing to one role check, expression, permission check, explain,
batch, filter or lookup operation use one coherent tuple view. Failed snapshot
completion and cancellation discard provisional results. Custom decision readers
must provide `tuples.Snapshotter`.
Borrowed readers are sequential, callback-scoped, and fail after closure.

A PostgreSQL ambient transaction must actually use REPEATABLE READ or SERIALIZABLE.
Use `pgxdb.DB.TransactSnapshot` for a read-write REPEATABLE READ workflow. The
adapter inspects the bound transaction's isolation and rejects default READ
COMMITTED with `tuples.ErrSnapshotIsolation` before evaluation. SQLite's ambient
BEGIN IMMEDIATE is suitable. Both preserve pending writes and leave commit or
rollback to the host. No detached transaction or silent isolation upgrade occurs.
Raw writes continue to join ordinary ambient transactions; atomic mutation commands
reject ambient transactions because they own their serialized write boundary.

## Lists and budgets

`Check`, `CheckExplain`, `CheckBatch` and `FilterAuthorized` share evaluator
semantics. Compatible exact probes and graph reads are batched inside the same
view. Reads and evaluation still grow with input and graph size.
`LookupAllResourceIDs`, paged lookup and `FilterPage` distinguish finite results
from explicit unrestricted policy. All/Any combine result sets by intersection
and union. Authorization IDs are not a business-row ordering or pagination key;
apply tenant/search/order constraints in the host's query or supply candidates to
`FilterPage`.

`model.EvaluationLimits` bounds depth, states, steps, relation targets, input batch,
lookup output and candidate scanning. Raw `tuples.Query.After` is a pointer to
its last returned tuple; custom adapters need no private cursor codec.
`tuples.Compare` orders scope kind, resource type/ID, relation and exact subject
components in byte order. User-facing list cursors remain opaque and versioned;
malformed and obsolete encodings are rejected. PostgreSQL pins query expressions
to C; SQLite uses BINARY.

Relationship listings exclude global facts and retain both concrete and userset
subjects. Subject and resource filters are applied before pagination, and facade
projections preserve tuple cursors and counts.

The raw store relationship facades share these tuple projections and reject
invalid selectors and unsupported search. Their standalone listings/counts use a
coherent snapshot. Raw SQL facades retain the host's ambient transaction isolation,
including PostgreSQL READ COMMITTED; use a snapshot transaction when consistency
across statements is required. Root `Relationships`, exact role checks and decision
operations retain their stricter snapshot-isolation requirements.

## Direct-grant short circuit and read costs

Every store keeps forward and reverse access paths to each tuple. The **forward
set** of a resource and relation ("every viewer of space S") answers who can;
its size is the number of grantees on one resource. The **reverse set** of a
subject ("everything Alice holds") answers what a principal can do; its size is
the principal's total grants. A **userset** subject such as `group:G#member` is
itself a set, and following one is expansion. `Direct` relations expand
**subject-first**: from the principal's reverse set outward to every userset it
reaches. `Through` hops are already resource-first: they read the resource's
forward set for the hop relation.

The rule of thumb: forward reads are cheap when resources have few grantees and
principals have many grants (principal-heavy); reverse reads are cheap in the
opposite, resource-heavy shape.

**Direct-grant short circuit (on by default, no API).** Before expanding a
`Direct` relation, the engine probes the forward set for the principal's own
concrete tuple. A hit grants without expansion. A miss denies without expansion
when the relation admits no userset subject. Otherwise the check expands
subject-first exactly as before. The probe applies only when the model allows
the principal's concrete type on that relation, and it reads the same snapshot
as the expansion. Batches probe in rounds: the optimized `CheckBatch` and
`FilterAuthorized` path (one resource type, one principal, no `Through`) sends
one exact read per Direct relation, which SQL stores split into statements of
100 tuples; other batches send one exact read per scheduling round, reading each
distinct fact once however many requests probe it. Answers are
identical wherever both paths decide; the store conformance suite proves this
over random models, cycles and off-model rows.

**Reading the explain trace.** A short-circuit hit records one step with
`Kind: "direct"` and `Outcome: "direct_hit"` (`model.ReasonDirectHit`) and no
expansion. A short-circuit miss on a relation without usersets records
`Kind: "direct"`, `Outcome: "denied"`. A step answered by expansion records
`granted` or `denied` as before. `CheckResult.ReasonCode` and
`Explanation.Decision` remain `granted` or `denied`, and `Reason` remains
`direct:<relation>`; `direct_hit` appears only in `ExplainStep.Outcome`.

**Budgets.** The probe charges no evaluation step and no graph state, so a
decision just under `MaxEvaluationSteps` is unaffected. It changes answers only
at the budget edge, in the caller's favor: a principal whose expansion exceeds
`MaxGraphStates` used to receive `ErrEvaluationLimit` even when directly
granted; now a direct hit grants, and a miss on a relation without usersets
denies. A miss on a relation that admits usersets still expands and still fails
with `ErrEvaluationLimit` on overflow, never a deny.

**Cost of a miss.** A miss on a relation that admits usersets pays the probe and
then the full expansion: about one extra round trip per single check (about
30 µs on loopback PostgreSQL, about 1 µs on the in-memory cache). On SQL stores
`CheckBatch` and `FilterAuthorized` add statements for every Direct level they
probe: in the Turso benchmark graph (dashboard → space → tenant) a round of one
distinct space probe per request plus a round of four distinct tenant probes add
`ceil(n/100) + 1` statements, so a batch of 50, 128, 366 or 600 requests goes
from 5, 5, 5 or 9 to 7, 8, 10 or 16 statements. Each statement is a round trip on a remote Turso database.

**Wide resources.** With the TupleCache on, the probe decodes the resource's
whole forward set (`logic/tuplecache/reader.go`): in the
[benchmarks](BENCHMARKS.md#direct-hit-short-circuit-v0260--published-2026-10-14) a
check on a resource with 10,001 grantees went from about 4 µs to about 1 ms and
allocates about 3.1 MB per check. On PostgreSQL the probe is an indexed
`EXISTS`, and on a vacuumed and analyzed table that same check went from about
148 µs to about 112 µs. On a freshly loaded or un-vacuumed table, the generic
plan PostgreSQL caches for the exact-probe statement ties the primary key
against `idx_iam_tuples_relation_resource`, and the narrower index wins: it
filters every grantee of the resource (about 0.83 ms at 10,001 grantees until
autovacuum runs). Resource-scoped `RoleIn` checks (the HTTP `HasRole` guard) and batch
`ContainsMany` reads already share this exposure. After a bulk load, run
`VACUUM ANALYZE iam_tuples` rather than waiting for autovacuum. Grant very wide access (for
example "every user can view the lobby") through a userset such as a group
rather than thousands of direct tuples.

## Writes, integrity and audit

Inbound adapters authorize the exact command before invoking a writer. Every
writer is principal-free; application services do not repeat the access check.
Configure data rules once on the store:

```go
integrity := mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{
    {ResourceType: "project", Relation: "owner", MinSubjects: 1},
}}
store := memory.New(memory.WithIntegrityPolicy(integrity))
components, err := authorization.New(authorization.Repositories{
    Tuples: store.Tuples(), Mutations: store.Mutations(),
})
```

SQL adapters offer the same `WithIntegrityPolicy` option. The default policy is
empty. `DefaultIntegrityPolicy()` explicitly requires one concrete `owner` on
every resource type. Empty `ResourceType` matches all resource types; zero
`MinSubjects` means one, and negative values are invalid. Matching rules use the
largest minimum. Resource minima do not apply to global facts.

| Capability | Behavior |
| --- | --- |
| `RelationshipWriter`, `RoleWriter` | Exact/desired-state writes; validate bound model shapes and enforce the store's integrity policy |
| `Mutations` | Atomic commands with model-shape validation, integrity and optional audit in one serialized write boundary |
| Raw `tuples.Storer` | Structural validation and the same configured integrity policy; joins supported ambient transactions |

There is one `mutations.Service`, with `Apply(ctx, command)` and typed methods
such as `AssignRole(ctx, command)`. There is no principal guard, transactional
permission view or separate system mutator. A pure `SemanticValidator` can enforce
fresh-operation batch admission and current-model tuple shapes in `MutationRepository.Apply`; it is not an access
callback.

Integrity uses the current serialized post-state and applies through all ordinary
write paths, including roles, relationships, raw `ApplyTuples`, reconciliation,
`DeleteScope`, purge and natural no-ops. Concurrent removals cannot both delete
the last required subject. Usersets do not satisfy a concrete-subject minimum.
A fresh protected scope must establish all its required subjects in its first
write, or in one batch. An explicit userset-only shape cannot support a concrete
minimum. Direct SQL outside these adapters is outside this enforcement contract.

Typed role commands require an explicit `Scope`. `OpBatch` applies exact additions
and removals atomically; use it for a swap. `OpReconcile` replaces subjects for one
named scope/relation and leaves other labels intact. Duplicates within one set
are idempotent. A fact in both add and remove sets is invalid. Commands and
affected rows are bounded; adapters never split one command into separate commits.

`Mutations.TeardownResourceAuthorization` is the explicit integrity exception.
It requires a bounded reason and removes resource facts while preserving global
facts. The host must first delete or logically retire the resource; ordinary
purge and raw scope deletion cannot bypass configured minima.

Results contain `applied`, `no_change` or `not_found`. Integrity refusals return
`ErrInvariantBlocked`, wrapping `sdk.ErrConflict`. Shape, cancellation and commit
errors also return no successful result.

### Durable operation replay

`Mutations.Apply` accepts an optional `Command.OperationID`. Empty IDs retain
state-based semantics. A nonempty ID is global to the authority, so namespace it
by producer (`invitation:<id>`); it must be valid UTF-8 without control characters
and at most 256 bytes. Typed relationship/role helpers do not accept operation
IDs, and resource teardown refuses them.

The mutation store binds each ID to the command's normalized requested facts with
a frozen `operation/v1` SHA-256 fingerprint. Facts, enabled audit, and the operation
record commit atomically, including `no_change` and `not_found` outcomes. Errors
and refusals retain no record. The ID and execution limits are excluded from the
fingerprint; tuple order and duplicates do not change it. A changed requested
delta returns `ErrOperationMismatch` (wrapping `sdk.ErrConflict`) without writing.

A matching retry returns the original `Outcome` with `Replayed=true` and writes
no tuples, audit rows or cache capture. Current-model validation, integrity policy
and configurable batch/affected-row limits apply only to fresh operations.
Structural validation and the hard command bound still apply to retries; an
audit-enabled store still requires `audit.WithSource`. `Superseded` is true only
on a replay whose requested facts are no longer in effect, checked under the
same write lock. It is a snapshot and does not prevent a later revoke.

SQL hosts using `Repositories` must apply `0003_iam_operations.sql` and
`0004_iam_operations_fenced.sql` before boot;
the constructor rejects missing or noncanonical ledger tables. Standalone
`RelationshipRepository` remains ledger-free. See [schema adoption](stores/UPGRADE.md).
Keep operation rows across purge, teardown and restarts. V1 never deletes them:
deleting a record that a producer can retry allows its old grant to run again.
Hosts' user-erasure procedures must account for this table and its hashes over
subject IDs; hashes can still be linked to known requests. There is no retention
or purge API in v1.

#### Fence and Recorded

Wire `Repositories.Operations` (a `mutations.OperationLedger` over the same
authority as `Mutations`; the bundled stores expose it as `Operations()` or set
it in `Repositories`) to enable `Components.Mutations.Fence` and `Recorded`.
Without it both return `ErrOperationLedgerNotConfigured`; `Operations` without
`Mutations` fails construction with `ErrOperationsWithoutMutations`. IDs follow
the `OperationID` rules above.

`Fence(ctx, id)` writes an `OutcomeFenced` record under the same write lock as
`Apply`, so the two are totally ordered: either the fence lands first and no
command can ever commit under that ID, or a command already committed and the
fence reports its outcome. It writes no tuple or audit row, needs no
`audit.WithSource`, refuses an ambient transaction
(`ErrMutationInsideTransaction`), and is idempotent — retry it after an
unknown-outcome failure.

| `FenceResult` | meaning |
| --- | --- |
| `{OutcomeFenced, Recorded: false}` | this call fenced the ID |
| `{OutcomeFenced, Recorded: true}` | an earlier fence holds |
| `{applied \| no_change \| not_found, Recorded: true}` | a command already committed under the ID; revoke through your ordinary path |

Any `Apply` under a fenced ID returns `Result{Outcome: OutcomeFenced, Replayed:
true, Superseded: true}` whatever its command shape: no fingerprint comparison,
no validation, no write. `Recorded(ctx, id)` reads the retained outcome without
the write lock; use it to skip a host precondition for an operation that already
committed. `false` is never proof the operation will not commit later — only
`Fence` is — and a lagging replica can report `false`, so read from the primary.
`Recorded` may run inside an ambient transaction and reads through it; `Fence`
never does.

A host that wires a Fencer should call `Mutations.Recorded(ctx, "boot-probe")`
at startup and require `(false, nil)`. A missing `Repositories.Operations` then
fails boot with `ErrOperationLedgerNotConfigured` instead of failing the first
cancel of an accepting invitation.

For invitation hosts, map an `OutcomeFenced` replay to authentication's
`ErrGrantFenced`, then `Replayed && Superseded` and `ErrOperationMismatch` to
`ErrGrantSuperseded`; return nil for a grant still in effect. This lets
authentication finish or cancel an accepting claim without restoring revoked
access. Keep the command stable between attempts: changing a replacement batch's
remove set changes its fingerprint. Fence with exactly the namespaced ID the
grant uses (`invitation:<id>`); a different namespace fences nothing.

During a rolling deploy an older binary meets a fenced record as a fingerprint
mismatch and returns `ErrOperationMismatch`, which the mapping above already
treats as superseded, so no access is granted. A committed `not_found` outcome
replays forever; a host that treats it as a failure keeps failing that
operation — map it to the terminal state your workflow expects.

Store `WithAudit()` records one canonical `Change{Action, Tuple}` delta in the
same commit as facts. Exact duplicates, no-ops, refusals and rollbacks add no
history. Audit failure rolls back facts. Each recording-enabled write requires
`audit.WithSource`: inbound supplies `ActorType`/`ActorID` from its authenticated
principal, or a host workflow supplies `System`. Attribution is data, not access
permission. Bundled role handlers supply authenticated attribution themselves.
Records use `tuple/v2`, with event grouping, source and time. Hosts own retention
and audit access.

## The authorization protocol

`sdk/capabilities/authorization` (imported here as `sdkauthorization`) is the
Zanzibar read protocol: `Checker` (`Check`, all-or-nothing `CheckBatch`),
`Lookup` (strictly paged `LookupResources`) and the composite `Authorizer`. This
pocket both implements and consumes it.

**Implementing: `Components.Authorizer`.** `authorizer.Engine` serves the
protocol over `Components.Decisions`, with `SchemaReader` and `Limiter`
(`MaxBatchSize`, `MaxLookupResults`). It adds behaviour only:

- every request passes the decision service's opaque-reference grammar
  (`model.ValidateCheckRequest`, `model.ValidateLookupRequest`) and nothing
  stricter: the adapter accepts whatever the pocket accepts natively;
- an undeclared resource type, permission or relation is `sdk.ErrInvalidInput`.
  `Components.Decisions` still denies an undeclared permission with a nil error;
- `Check` answers a permission or a declared relation (through `Direct`, with
  userset expansion);
- `authorizer.WithGlobalResource(type, id, relationFor)` names where global roles
  appear: `relationFor(label)` on `type:id` is an exact `tuples.Global()` read of
  `label`, never a permission of that name. `Components.Authorizer` has none;
  build your own with `authorizer.New(components.Decisions, ...)`;
- a batch mixing permissions with relations or global roles runs as one snapshot
  (`decisions.Service.EvaluateBatch`); an oversize batch is `sdk.ErrInvalidInput`;
- `LookupResources` enumerates permissions only. A universal grant (a satisfied
  global `Role` branch) cannot be enumerated: it returns `authorizer.ErrUnrestricted`,
  which wraps `sdk.ErrUnavailable` but is **not transient — do not retry**. Hosts
  that rely on `Unrestricted` keep calling `Components.Decisions`.

`Unrestricted`, reasons and explain, `Expression`/`Require`, exact roles at any
scope, the ledger, audit, TupleCache and `FilterPage` stay pocket-owned.

**Portability is opt-in.** Grammar belongs to each implementation: the protocol
checks structure only, this engine accepts `a.b`, `a@b` or `é`, and a vendor
integration rejects what its own grammar cannot represent as
`sdk.ErrInvalidInput`. Before moving to a vendor, run
`sdkauthorization.ValidatePortable(req)` over your requests in tests: it applies
the strict grammar every known implementation can represent (types and
relations `^[a-z][a-z0-9_]{1,62}[a-z0-9]$`, relations at most 50 bytes, IDs
`^[a-zA-Z0-9/_|\-=+]+$` at most 256 bytes, `len(type)+1+len(id) <= 256`).
`ValidatePortableRef` and `ValidatePortableObject` check one identifier or one
object.

**Consuming: lowered guard policies.** `decisions.NewLoweredEvaluator(checker,
opts...)` evaluates `Require` policies over any protocol `Checker` (this
pocket's engine adapter or a vendor integration), with no stores. Each leaf
becomes one `Check`: `Can` a permission, `HasRelationship` and `HasRole` on a
resource the relation of that label, `HasRole(label, Global())` the relation
`relationFor(label)` on the `decisions.WithGlobalResource` resource. `Through`
cannot be lowered. The walk keeps the native order and short circuit; leaves whose
resource is known without running a store-reading resolver (`Global`, `Fixed`,
`Path`, or an input already resolved) go out as one `CheckBatch`, so the common
`Any(HasRole(…, Global()), Can(…, Path(…)))` costs one round trip. `Path` inputs
resolve early and hold their errors until reached; `Resource` resolvers run only
when reached. Identifiers pass through unchanged: the `Checker` decides what it
can represent, and its `sdk.ErrInvalidInput` surfaces as
`decisions.ErrLoweredPolicyMisconfigured` (500).

```go
evaluator, err := decisions.NewLoweredEvaluator(checker,
    decisions.WithGlobalResource("platform", "global", func(label string) string { return "role_" + label }))
guard, err := authorizationhttp.New(authorizationhttp.Services{Decisions: evaluator})
mw := guard.Require(policy, authorizationhttp.WithLoweredPolicy())
```

- **Opt in per route.** A lowered policy is a sequence of protocol calls, not one
  snapshot, so `Require` panics at mount over an evaluator whose
  `LoweredPolicy()` is true unless the route passes `WithLoweredPolicy()`.
- **Decorators must forward `LoweredPolicy` and `SchemaReader`.** A logging or
  metrics wrapper that hides `LoweredPolicy` makes `Require` treat the evaluator
  as native; one that hides `SchemaReader` moves name validation from mount to
  evaluation.
- **Without a `SchemaReader`**, an undeclared name is caught only at evaluation
  and fails with `decisions.ErrLoweredPolicyMisconfigured` (500), never a
  client 400.
- **Declare your role labels as relations.** Through the protocol an undeclared
  relation is an error. A host moving a guard onto `NewLoweredEvaluator` must
  declare every scoped `HasRole` label as a relation, and each global label's
  `relationFor(label)` on the global resource type. A host that stays on the
  native evaluator needs no change.

## HTTP

### Composable route guards

Use `Require` with `All` (every check) and `Any` (at least one check):

```go
import authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"

organization := authorizationhttp.Path("organization", "organizationID")
document := authorizationhttp.Path("document", "documentID")

guard := components.HTTP.Require(authorizationhttp.Any(
    authorizationhttp.HasRole("admin", authorizationhttp.Global()),
    authorizationhttp.All(
        authorizationhttp.HasRole("member", organization),
        authorizationhttp.HasRelationship("editor", document),
        authorizationhttp.Can("publish", document),
    ),
))
router.PATCH("/organizations/{organizationID}/documents/{documentID}", handler, guard)
```

| Predicate | Meaning |
| --- | --- |
| `HasRole(label, target)` | Exact concrete membership; no userset expansion or implicit global fallback; no model required |
| `HasRelationship(label, target)` | Declared relation membership with model-permitted userset expansion (`Direct`) |
| `Can(permission, target)` | Declared named permission, including its explicit traversal rules |

Targets are explicit: `Global()` for a global role, `Fixed(type, id)` for a known
resource, `Path(type, parameter)` for a route parameter, or `Resource(type,
resolver)` for a custom request input. Graph and named-permission predicates
require a resource target. The immediate roles-service APIs remain separate:
`Roles.HasRole` reads global membership and `Roles.HasRoleIn` reads scoped membership.

`Require` validates all branches at mount, including branches that might later
short-circuit. Invalid labels, undeclared graph/permission coordinates, missing
resolvers, zero targets and empty `All`/`Any` groups panic before traffic. The
predicate tree has the same depth/node bounds as core expressions.

One request evaluates the complete policy in one coherent tuple operation with
a shared evaluation budget. Checks run in declaration order: `All` stops at its
first denial, `Any` at its first allowance, and either stops on an encountered
error. Skipped branches do not resolve their resource inputs or read tuples.
Reusing a target value resolves it once per request, including a cache fallback;
independently constructed targets are separate inputs. A resolved resource must
match its declared type. Only a resolver's `ErrAlternativeNotApplicable` makes
that predicate false; other failures abort the policy.

Custom resolvers must be read-only, concurrency-safe and respect the request
context. They execute while the authorization snapshot is open; their external
reads do not automatically join it. Leave connection-pool capacity for such input
reads or use an independent source. Authentication runs before the guard. Missing
principal returns 401, denial 403 by default, evaluation exhaustion or any other
`sdk.ErrUnavailable` 503, and other errors 500, with no internal error details. The handler runs after successful snapshot
completion and receives the original request.

Transport-independent callers can use the same engine:

```go
result, err := components.Decisions.Evaluate(ctx, principal, decisions.Any(
    decisions.Role("admin"),
    decisions.On(documentResource, decisions.Permission("publish")),
))
```

`decisions.BindResource(type, key, leaf)` and `EvaluateResolved` provide lazy
inputs outside HTTP. Bindings apply to individual leaves. Runtime slots are
rejected in named models; existing model digests remain unchanged.

`Adapter.Require` is the single middleware entry point. Express compound policy
inside one `All(...)` or `Any(...)` so every reached branch shares the snapshot
and budget. `authorizationhttp.New(Services{Decisions: service})` also supports
standalone use; the decision dependency requires only `ValidateExpression` and
`EvaluateResolved`. The evaluator owns model validation and evaluation limits.

### Authorization boundary and concurrency

Application inbound adapters own permission checks. After `Require` admits a
request, the handler can call application logic without another role or
permission check. Logic still validates commands, enforces tenant restrictions
and preserves business invariants. Other entry points (RPC, jobs, CLI) must
explicitly authorize callers or be wired as trusted capabilities. For body-based
policies, the inbound handler can prepare the command, authorize that exact
command, then call the service.

| Operation | Consistency boundary |
| --- | --- |
| `Require` | One coherent decision operation before the protected handler. An admitted request may finish after revocation; no transaction spans the handler automatically. |
| Cached admission | The same decision semantics, with revocation visibility delayed by at most the configured eligibility bound. Use durable evaluation when this delay is unacceptable. |
| Complete-set listing | One authorization snapshot supplies an ID restriction for the subsequent business query; these are separate reads. |
| `FilterPage` | One authorization snapshot across all candidate pulls in that call. Business-source reads are not automatically in that snapshot. Continuations evaluate again. |
| Same-database membership `EXISTS` | Facts and business rows in that SQL statement share its snapshot. A separate host bypass check does not share it. |
| Atomic tuple command | Shape validation, current-state integrity, tuple changes and enabled audit share one serialized write operation after inbound admission. |

Revocation is not cancellation: a successful ordinary check is not retracted
while application code runs. With an uncached durable read, a decision beginning
after a committed revoke observes it; a caller-owned older snapshot may instead
observe its own transaction state. TupleCache has its documented staleness window.

A tuple command admitted before a concurrent permission revoke may still commit.
The mutation transaction does not re-check the caller. It evaluates configured
integrity rules against the current serialized facts, so two removals cannot
violate a minimum even if both callers were admitted earlier. This protects tuple
data rules; it does not serialize unrelated business data or external effects.
Unknown commit outcomes are never permission to blindly repeat an external effect.

Opening an ambient transaction around middleware does not make admission and a
later write one permission decision. Atomic mutation commands own their write
boundary and reject ambient transactions. Raw writers join supported ambient
transactions and still enforce configured integrity. See
[Writes, integrity and audit](#writes-integrity-and-audit).

### Host denial responses

Choose the denied response per mounted policy. For a route that conceals resource
existence, reuse the framework's predicates and supply the host's 404 handler:

```go
guard := components.HTTP.Require(
    authorizationhttp.Can("view", authorizationhttp.Path("document", "documentID")),
    authorizationhttp.WithDeniedHandler(http.NotFoundHandler()),
)
```

A custom `http.HandlerFunc` can write the host's usual JSON or HTML response.
Use the same renderer as other not-found responses. The hook runs only after an
error-free denial and successful completion of the decision operation; it receives
the original request and never continues to the protected handler. A caller-owned
ambient transaction may still be open. Authentication and
evaluation failures keep their 401/500/503 responses. Handlers are borrowed and
must support concurrent requests; nil handlers/options panic at mount.

This response applies to the complete policy. It does not identify which branch
denied access. A route that must distinguish failed visibility (404) from failed
action permission (403) needs an explicit policy for that distinction; do not
inspect `CheckResult.Reason` or perform another lookup in the response handler
and assume both decisions share a snapshot.

### Decision logging

The existing logger option also enables decision records when its handler accepts
DEBUG. Hosts own the format, filtering, request correlation and redaction:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
components, err := authorization.New(repositories, authorization.WithLogger(logger))
```

Standalone decision services accept `decisions.WithLogger(logger)`. Nil captures
`slog.Default()` at construction. Direct role/relationship facade reads and raw
store reads do not emit these decision-operation records. Each completed public
`decisions.Service` operation emits
one `authorization decision` record with its operation, final outcome and duration.
Delegating methods, nested predicates, cache fallback and lookup retries do not
produce duplicate records. Batch/filter/lookup records contain aggregate counts;
expressions, result ID lists, traces and raw error messages are not logged.
Validation and evaluation failures are error outcomes, separate from denials.
Single-check metadata includes bounded principal/resource identifiers; hosts can
redact these through their `slog.Handler`.

Logging performs no extra authorization reads and does not obtain explanations.
Disabled DEBUG skips timing and attribute construction. Caller-bound `*With`
operations describe evaluation inside the caller's view, not a committed
transaction. These operational records are separate from durable mutation audit
and from `WithDiagnosticObserver`'s scoped-denial transition probes.

### Bundled role administration

A host gate enables bundled role administration and must provide authentication,
authorization and any required browser-origin/CSRF protection. A nil gate leaves
all role handlers disabled. Enabled routes also require `RoleRoutes.WritePolicy`
and the role read/mutation services. `WritePolicy` without a gate is invalid.
After strict decoding and validation, the callback receives one value-only
`RoleWriteRequest` containing `Principal`, `Operation` (assign or unassign),
`Subject`, `Role` and `Scope`. Returning nil admits the exact command; an error refuses
before any write. The service subsequently enforces data integrity without a
second principal check. A coarse gate alone never admits role writes.

```go
authorization.WithRoleRoutes(authorizationhttp.RoleRoutes{
    Gate: hostRoleGate,
    WritePolicy: hostRoleWritePolicy,
})
```

| Method | Bundled route |
| --- | --- |
| POST | `/authorization/roles` |
| POST | `/authorization/roles/unassign` |
| GET | `/authorization/roles/by-subject` |
| GET | `/authorization/roles/by-resource` |

Assignment JSON names scope explicitly:

```json
{"subject_type":"user","subject_id":"u1","role":"owner","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}
```

Global scope is `{"kind":"global"}`. Omitted scope, partial coordinates and old
flat resource fields are invalid. Unassignment returns only `outcome`; the
`/roles/effective` route and `same_role_grant_remains` field are removed.

Hosts can construct the public HTTP adapter directly and mount its gated handlers
on their own paths.

## Optional TupleCache

TupleCache mirrors **all raw canonical facts**, including global and resource
roles and usersets. SQL remains authoritative. It caches no permission answers,
expanded memberships or business-resource lists. A model change therefore does
not require rewriting cached facts.

SQL triggers capture each committed canonical delta in the same transaction.
The relay publishes full before/after facts into Redis forward/reverse sets and
acknowledges exact event IDs only after publication. Full recovery reads current
SQL facts. Ordinary SQL-only writers participate through those same triggers.

Construct the SQL repositories with their store `WithTupleCache()` option after
applying the optional cache baseline in the [schema setup guide](stores/UPGRADE.md).
Then construct `goredis.NewTupleCache(client)` (an optional
`goredis.WithNamespace` adds a readable key prefix) and pass
`authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: bound})`.
The Redis client must enable context timeouts. Run `components.TupleCache.Poll`
as a supervised host worker; `Notify()` after the outermost commit is only a
coalesced wake-up hint. Constructors start no goroutines.

`MaxStaleness` defaults to **5 seconds** when zero: a revocation may remain
unseen for up to 5s. Negative values are rejected. A host that sets it must use
the same value on every replica sharing an authorization database. A revocation
may remain unseen while publication is pending within that bound. `PollInterval`
defaults to `min(1s, MaxStaleness/4)` (1s with the default); `Notify()` still
delivers local writes immediately, and replicas may use different intervals. Eligibility starts at the authoritative
observation, so a delayed relay cannot extend it. Missing/expired/oversized cache
reads or a concurrent publication retry the **whole operation** on a durable
snapshot. Exact role reads use this same runtime. Ambient transactions bypass
Redis and borrow the coherent SQL view; integrity and audit remain durable.

Protocol 2 is part of the binding and wire format. Scope kind is explicit.
The backend keys each mirror on its binding (protocol, source identity and
MaxStaleness), so processes share a mirror exactly when their bindings match and
different bindings use different mirrors. `tuplecache.ErrBinding` now only reports
a backend returning a foreign mirror. An authorization database has a single
delivery receipt, so every replica on it must run the same `MaxStaleness`;
different bindings over one database keep rebuilding each other in full on every
poll. When changing `MaxStaleness`, deploy quickly, or turn the cache off,
deploy, and turn it back on. An empty/restored mirror rebuilds from SQL,
even after its original events were acknowledged. Use a fresh source identity when
restoring or cloning authoritative data as directed by the adapter runbook.

The Redis adapter uses one complete hash on one shard; Redis Cluster is unsupported.
Default limits reject reads above 1 MiB and delta publication inputs plus affected
sets above 4 MiB. Over-limit reads fall back; no partial result escapes. Full
rebuilds bound fields/upload chunks but still materialize current facts and event
IDs. A rebuild longer than MaxStaleness cannot publish usable authority. One global
receipt means an unrelated publication can force a durable retry.

For an oversized historical backlog, `Rebuild(ctx)` reads current facts and exact
pending IDs without decoding every obsolete payload. It publishes before
acknowledging. It cannot make a currently oversized set fit or bypass freshness.
Do not delete backlog rows or split one transaction into partial publications.

At shutdown cancel/join workers, close the runtime and drain requests before
closing borrowed clients. `Stats`, `LastPoll` and `LastSuccessfulPoll` expose
fallbacks, capacity failures, publication conflicts, timings and captured counts.
Measure SQL fallback and rebuild capacity alongside Redis latency.

## Access review

Recipes for a periodic review of who holds what, with the bundled APIs as they are today.
`Roles` and `Audit` are `Components` fields; `Repositories.Audit` must be wired and the store
constructed with `WithAudit()` for history to exist.

```go
// Every global role holder (the bundled HTTP listings exclude the global scope).
page, err := components.Roles.ListRoleAssignmentsByScope(ctx, tuples.Global(), list.Request{})

// Direct assignments on one resource; usersets are not expanded.
page, err = components.Roles.ListRoleAssignmentsByScope(ctx, tuples.On("project", id), list.Request{})

// Everything one subject holds directly, across scopes.
page, err = components.Roles.ListRoleAssignmentsBySubject(ctx, principal, list.Request{})

// Change history for one resource, newest first; same shape by subject or by actor.
page, err = repos.Audit.List(ctx, audit.Filter{ResourceType: "project", ResourceID: id}, list.Request{})
```

Limits today: there is no effective-access listing that merges scopes or expands usersets,
no reverse listing ("who can `manage` this resource"), and no bundled HTTP route or export
for the audit history — a host serves its own. Denied decisions and refused writes are not in
the history. Pagination follows `sdk/pkg/list` (cursor or offset per the store).

## Schema setup and verification

SQL adapters ship a fresh `0001_iam_tuples.sql` defining canonical facts and audit.
TupleCache adds one optional `0002_iam_tuple_cache.sql` source after the base.
Hosts own pre-boot application of both. There is no bundled old-schema conversion,
preflight or downgrade stream.

The optional `WithDiagnosticObserver` can count
`decisions.DiagnosticGlobalGrantNotApplied` (`global_grant_not_applied`) when a
scoped-only expression denies despite a matching global fact. It is disabled by
default. An enabled owned operation probes at most eight matching global facts
inside its existing snapshot and emits at most one event after successful
completion. Events contain no IDs or labels; probe failures never grant access.
Caller-bound decision views do not emit because their caller owns completion.

- [Schema setup](stores/UPGRADE.md)
- [PostgreSQL](stores/pgx/README.md), [Turso](stores/turso/README.md), [Redis](stores/goredis/README.md)
- [Conformance and benchmarks](BENCHMARKS.md)
- Repository [AUDIT.md](../../AUDIT.md) records consumer-visible changes.

Run the shared `stores/storetest` suites for custom adapters. Local tests and
benchmarks do not establish a production latency or capacity guarantee.
