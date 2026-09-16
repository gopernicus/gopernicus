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
| `logic/decisions` | One model/compiler and evaluator for roles, graph traversal, All/Any, check/explain/batch/filter/lookup |
| `logic/model` | Principals, resources, result/reason vocabulary and evaluation budgets |
| `logic/mutations` | Principal-free commands, tuple-shape validation and atomic integrity policy |
| `logic/audit` | Canonical committed changes, attribution and history reader |
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
optional history reader. Without `Repositories.Mutations`, `Components.Mutations`
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
current-model tuple shapes in `MutationRepository.Apply`; it is not an access
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

Store `WithAudit()` records one canonical `Change{Action, Tuple}` delta in the
same commit as facts. Exact duplicates, no-ops, refusals and rollbacks add no
history. Audit failure rolls back facts. Each recording-enabled write requires
`audit.WithSource`: inbound supplies `ActorType`/`ActorID` from its authenticated
principal, or a host workflow supplies `System`. Attribution is data, not access
permission. Bundled role handlers supply authenticated attribution themselves.
Records use `tuple/v2`, with event grouping, source and time. Hosts own retention
and audit access.

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
principal returns 401, denial 403 by default, evaluation exhaustion 503, and other errors
500, with no internal error details. The handler runs after successful snapshot
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
Then construct `goredis.NewTupleCache(client, namespace)` and pass
`authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: bound})`.
The Redis client must enable context timeouts. Run `components.TupleCache.Poll`
as a supervised host worker; `Notify()` after the outermost commit is only a
coalesced wake-up hint. Constructors start no goroutines.

A positive `MaxStaleness` is required. A revocation may remain unseen while
publication is pending within that bound. Eligibility starts at the authoritative
observation, so a delayed relay cannot extend it. Missing/expired/oversized cache
reads or a concurrent publication retry the **whole operation** on a durable
snapshot. Exact role reads use this same runtime. Ambient transactions bypass
Redis and borrow the coherent SQL view; integrity and audit remain durable.

Protocol 2 is part of the Redis key prefix and wire format. Scope kind is explicit.
Processes sharing a delivery stream share one mirror and MaxStaleness policy;
source/policy mismatches fail with `tuplecache.ErrBinding`. An empty/restored mirror
rebuilds from SQL, even after its original events were acknowledged. Use a fresh
source identity/namespace when restoring or cloning authoritative data as directed
by the adapter runbook.

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
