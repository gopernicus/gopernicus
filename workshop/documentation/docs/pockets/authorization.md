---
title: Authorization
description: Exact roles and relationship permissions over one canonical tuple authority.
---

# Authorization

A user can hold both `owner` and `member` on the same project. Authorization stores
these as independent facts and lets the host decide which facts grant permission.
Exact roles need no graph model. Applications that need relationship traversal can
use the same facts through one permission evaluator.

Other pockets accept narrow check collaborators; this pocket remains optional.
Hosts own policy, database connections, migrations and HTTP gates.

## One fact model

```go
fact := tuples.Tuple{
    Scope: tuples.On("project", "p1"),
    Relation: "owner",
    Subject: tuples.SubjectRef{Type: "user", ID: "u1"},
}
```

The whole scope, relation and subject identify the fact. Exact duplicates are
idempotent; different labels coexist. `tuples.Global()` explicitly names global
scope. Zero scope is invalid. Global grants are never wildcards.

Concrete `group:g1` and userset `group:g1#member` are different subjects. A userset
expands only where the model permits that exact relation. Role and relationship
facades read and write the same canonical authority, `iam_tuples` in SQL.

## Exact membership

```go
components, err := authorization.New(repos) // repos.Tuples is required
if err != nil { return err }

held, err := components.Roles.HasRole(ctx, principal, "admin")
heldHere, err := components.Roles.HasRoleIn(ctx, principal, "owner", resource)
```

`HasRole` checks exact global membership. `HasRoleIn` checks exact membership on
one resource, with no global fallback or userset expansion. The explicit
`HasRoleInOrGlobal` helper probes both scopes in one snapshot.

Use one expression when facts jointly govern a result:

```go
result, err := components.Decisions.Evaluate(ctx, principal,
    decisions.All(
        decisions.Role("active"),
        decisions.Any(decisions.Role("admin"), decisions.RoleIn("owner", resource)),
    ),
)
```

Separate calls joined with Go's `&&` can observe different committed states.
The expression evaluates against one coherent view.

## Named permissions and graph traversal

`authorization.WithModel(decisions.Model{...})` adds one optional model. Resource
relations declare allowed subject shapes; named permissions contain expressions.

| Expression | Meaning |
| --- | --- |
| `Role("admin")` | Exact global fact |
| `RoleIn("owner")` | Exact fact on the resource being checked |
| `Direct("member")` | Model-permitted concrete/userset membership |
| `Through("parent", "view")` | Follow resource targets to their permission |
| `Permission("edit")` | Another named permission |
| `All(...)`, `Any(...)` | Ordered conjunction/disjunction |

The entire expression is validated before I/O. Evaluation short-circuits in
order, honors cancellation and rejects empty/malformed expressions. Depth,
states, steps, relation fan-out, batches, lookup results and candidate scans are
bounded. Errors fail closed; exhausted budgets return an indeterminate error,
never a partial allow.

Models are immutable snapshots. A permission label does not create an implicit
write-time role catalog. Structurally valid opaque assignments remain legal;
explicit subject-shape constraints are enforced through model-bound writers.

## Components and construction

| Component | Purpose |
| --- | --- |
| `Decisions` | Evaluate, check, explain, batch, filter and lookup |
| `Roles` | Exact checks and concrete assignment listings |
| `Relationships` | Resource-scoped fact facade over the canonical tuples |
| `Mutations` | Optional principal-free atomic tuple commands |
| `HTTP` | Permission middleware and optional role administration |
| `RelationshipWriter`, `RoleWriter` | Ordinary writers with configured store integrity |
| `TupleCache` | Optional raw mirror runtime |

`Repositories.Tuples` is required and backs both role and relationship facades.
`Mutations` and `Audit` are optional capabilities. Without `Repositories.Mutations`,
`Components.Mutations` is nil; raw writers remain available. Direct
`mutations.NewService(nil)` returns `ErrMutationsNotConfigured`. Options replace
whole values in order. Nil options and typed-nil dependencies fail construction.
Constructors start no workers.

Focused constructors take canonical ports directly:

```go
checks, err := decisions.NewService(tupleStore, decisions.WithModel(policy))
if err != nil { return err }
compiled, err := decisions.Compile(policy)
if err != nil { return err }
writes, err := mutations.NewService(mutationRepository,
    mutations.WithModel(compiled),
)
if err != nil { return err }
```

All contributing reads share one snapshot. PostgreSQL ambient decisions require
REPEATABLE READ or SERIALIZABLE; `pgxdb.TransactSnapshot` provides a read-write
REPEATABLE READ workflow. Default READ COMMITTED is rejected before evaluation.
SQLite BEGIN IMMEDIATE preserves the host's pending writes. Callback readers
cannot outlive the callback. Atomic mutation commands own their serialized
boundary and reject ambient transactions; raw writes can join ambient work.

## Writes, integrity and audit

Inbound adapters authorize the exact command before calling a writer. One
`mutations.Service` accepts commands without a principal or guard; its optional
compiled model constrains tuple shape. The writer never repeats the access check.

```go
store := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{
    Rules: []mutations.IntegrityRule{
        {ResourceType: "project", Relation: "owner", MinSubjects: 1},
    },
}))
```

SQL adapters expose the same option. The default integrity policy is empty;
`DefaultIntegrityPolicy()` explicitly protects one concrete owner per resource.
An empty resource type matches all resource types, and a zero minimum means one.
Usersets do not satisfy a concrete-subject minimum. Global facts are unaffected.

All ordinary writers enforce the store's configured integrity policy, including
role/relationship facades, raw tuple changes, reconciliation, scope deletion and
no-ops. They validate the current serialized post-state; two concurrent removals
cannot violate a minimum. Establish all required subjects together for a new
protected scope. Only explicit `TeardownResourceAuthorization`, with a required
reason, bypasses minima; it preserves global facts. Direct SQL outside the
adapters is outside this enforcement contract.

Use an exact `OpBatch` remove/add delta for a swap, or `OpReconcile` for the
subjects of one named relation. Other labels remain intact. Command and affected
row bounds preserve atomicity. Integrity refusals wrap `sdk.ErrConflict`.

Store `WithAudit()` records one canonical `Change{Action, Tuple}` delta atomically.
No-op, refused and rolled-back writes add no history. Every enabled write supplies
`audit.WithSource` with authenticated actor or system attribution. Bundled HTTP
role handlers supply their authenticated actor. Attribution does not grant access.
Hosts own audit access and retention.

Permission revocation after admission does not retract an admitted operation.
Integrity still runs against current serialized facts at the write. An ambient
transaction around middleware does not make admission and a later write atomic.

## HTTP

Compose route policy with the public `inbound/http` adapter:

```go
document := authorizationhttp.Path("document", "documentID")
guard := components.HTTP.Require(authorizationhttp.Any(
    authorizationhttp.HasRole("admin", authorizationhttp.Global()),
    authorizationhttp.All(
        authorizationhttp.HasRole("reviewer", document),
        authorizationhttp.Can("publish", document),
    ),
))
```

`All` requires every check; `Any` requires at least one. `HasRole(label, target)`
checks exact concrete membership and works without a model.
`HasRelationship(label, target)` checks a declared relation with userset expansion;
`Can(permission, target)` checks a declared named permission. Use `Global`,
`Fixed(type, id)`, `Path(type, parameter)` or `Resource(type, resolver)` targets.
Only exact roles accept global scope.

`Require` validates every branch at mount and evaluates one coherent tuple
snapshot with one shared budget. Checks and resource resolution are ordered and
short-circuit; encountered errors abort. Reuse a target value to resolve it once
per request, including cache fallback. Custom resolvers are read-only and must
respect cancellation; their own datastore reads do not automatically join the
tuple snapshot. Only resolver `ErrAlternativeNotApplicable` means a false check.

No principal returns 401; denial defaults to 403; budget exhaustion returns 503;
other errors return 500 and fail closed. Empty groups and malformed or undeclared
checks are rejected at registration. `Adapter.Require` is the single middleware
entry point. Put compound policy inside one `All` or `Any` expression to share
one snapshot and evaluation budget. HTTP depends only on expression validation
and evaluation; the decision service owns the policy rules and limits.

### Host denial responses

Choose 403 or a host-specific response per mounted policy. To conceal resource
existence with the same 404 response as other missing resources:

```go
guard := components.HTTP.Require(
    authorizationhttp.Can("view", authorizationhttp.Path("document", "documentID")),
    authorizationhttp.WithDeniedHandler(http.NotFoundHandler()),
)
```

The hook receives the original request after an error-free denial. It never calls
the protected handler; authentication and evaluation errors keep their normal
responses. The handler is borrowed and must support concurrent requests. It
applies to the whole policy, not an individual `All`/`Any` branch.

### Decision logging

Use the host's logger; no forwarding wrappers or additional permission reads are
needed:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
components, err := authorization.New(repos, authorization.WithLogger(logger))
```

Direct construction accepts `decisions.WithLogger(logger)`. Each completed public
`decisions.Service` operation emits one DEBUG `authorization decision` record,
including denied and failed operations. Nested predicates, cache fallback and
internal retries do not create duplicates. Batch and lookup results use aggregate
counts; expressions, result ID lists and raw errors are not logged. Hosts control
filtering, correlation and redaction through their slog handler. Direct role or
relationship facade reads do not emit these records.

These logs describe decisions. Store `WithAudit()` separately records committed
tuple changes, and `WithDiagnosticObserver` supplies transition diagnostics.

### Bundled role administration

Bundled role routes require the role read/mutation services, a host
`RoleRoutes.Gate` and `RoleRoutes.WritePolicy`. The gate owns authentication,
route access and browser-origin defenses; the policy receives the validated exact
assign/unassign request (principal, operation, subject, role and scope). Returning
nil admits; an error stops before any write. Missing policy fails construction, and a
nil gate leaves routes unmounted. Assignment JSON uses explicit scope:

```json
{"subject_type":"user","subject_id":"u1","role":"owner","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}
```

Global scope is `{"kind":"global"}`. The old flat resource fields, effective-role
route and `same_role_grant_remains` field are removed.

## Stores and TupleCache

Memory, PostgreSQL and Turso/SQLite share conformance tests. Authorization
Firestore is removed; unrelated Firestore modules remain.

TupleCache optionally mirrors raw global/resource facts in Redis. SQL remains
authoritative; permission answers are evaluated on every operation. Roles and
graph expressions share cache freshness. Expired, unavailable or changing mirrors
retry the whole operation against SQL. Integrity and audit use durable state.

A positive host-chosen `MaxStaleness` bounds revocation lag. The host supervises
polling, shutdown and capacity. Cache protocol 2 has a versioned Redis prefix and
explicit scope. Upgrade core, SQL adapter, Redis adapter and the required pgxdb
connector together. SQL setup applies one fresh `0001_iam_tuples.sql` containing
facts and audit, then optionally `tuple_cache_migrations/0002_iam_tuple_cache.sql`.
There is no bundled old-schema conversion or downgrade stream.

Raw tuple lookup resumes from `Query.After *Tuple`; `tuples.Compare` defines the
canonical order. User-facing list cursors remain opaque and versioned. Custom
adapters need no private tuple-key import.

See the repository's [authorization reference](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/README.md),
[schema setup and API adoption map](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/stores/UPGRADE.md)
and [benchmarks](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/BENCHMARKS.md)
for complete APIs, removed-API replacements and verification.
