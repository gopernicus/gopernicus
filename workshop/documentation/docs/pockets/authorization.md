---
title: Authorization
description: Roles and relationships as one table of facts, checked at the door and protected at the write.
---

# Authorization

`pockets/authorization` answers one question: **may this principal do this action
on this resource?** It stores the facts that answer it, evaluates them in one
consistent read, and guards HTTP routes with the result. It does not decide
policy for you, run migrations, or start workers.

This page explains the decisions behind the pocket. The
[README](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/README.md)
is the exhaustive reference.

## Two questions, two owners

The pocket keeps two questions separate because they have different answers,
different failure modes and different owners.

| Question | Who answers | When | On failure |
| --- | --- | --- | --- |
| **Can this principal perform the requested action?** | The inbound adapter, through `Require` or a `Decisions` call | Before the handler runs | 401 / 403 / 503 |
| **Will the data be left in an acceptable state?** | The store, through its configured `IntegrityPolicy` | Inside the write transaction | `ErrInvariantBlocked` (wraps `sdk.ErrConflict`) |

The first question is *authorization*. It is about a principal and it runs at
the door. The second question is *integrity*. It is about the data, it has no
principal, and it runs on every ordinary write no matter who asked. A project
must keep at least one owner. That is not a permission rule, it is a data rule,
so it lives on the store.

Because of this split, **writers are principal-free**. Once inbound has admitted
a request, the mutation service, the role and relationship writers and the raw
tuple store never re-check the caller. Application services take an admitted
command and enforce validation and integrity only. Other transports, such as
jobs or CLIs, must admit their own callers before calling a writer.

## One table of facts

Everything the pocket knows is a **tuple**: a scope, a relation and a subject.

```go
tuples.Tuple{
    Scope:    tuples.On("project", "p1"),                  // or tuples.Global()
    Relation: "owner",
    Subject:  tuples.SubjectRef{Type: "user", ID: "u1"},    // or group:g1#member
}
```

Read it as "on project p1, user u1 is an owner". In SQL that is one row in
`iam_tuples`, unique on the whole tuple. There is no separate roles table. A
global role such as `admin` is the same shape with an explicit global scope. A
resource role and a relationship are also the same shape. `Roles` and
`Relationships` are two views over the same rows, so a fact written through
either facade participates in every read.

Consequences worth knowing:

- Facts are independent. `owner` and `member` on the same project coexist, and
  adding one never replaces the other. Writing the same fact twice is a no-op.
- Scope is always explicit. A global grant is never a wildcard and never falls
  through to a resource check on its own.
- A concrete subject (`group:g1`) and a userset (`group:g1#member`) are different
  subjects. Usersets expand only where a model says they may.
- Strings are exact. No trimming, no case folding, at most 256 bytes.

## Reading facts

### Exact roles need no model

```go
components, err := authorization.New(authorization.Repositories{Tuples: store.Tuples()})

isAdmin, err := components.Roles.HasRole(ctx, principal, "admin")            // global fact
owns, err := components.Roles.HasRoleIn(ctx, principal, "owner", project)    // fact on this resource
either, err := components.Roles.HasRoleInOrGlobal(ctx, principal, "admin", project)
```

These are exact lookups. No graph traversal, no userset expansion, no fallback
from resource to global unless you ask for both.

### Named permissions and traversal need a model

Supply `authorization.WithModel(decisions.Model{...})` when policy is more than
an exact fact. The model declares, per resource type, which relations exist,
what subject shapes they accept, and which named permissions are made of which
expressions.

| Expression | Meaning |
| --- | --- |
| `Role("admin")` | Exact global fact |
| `RoleIn("owner")` | Exact fact on the resource being checked |
| `Direct("member")` | Membership in a declared relation, with model-permitted userset expansion |
| `Through("parent", "view")` | Follow the resource's `parent` targets and ask for `view` there |
| `Permission("edit")` | Another named permission |
| `All(...)`, `Any(...)` | Ordered conjunction and disjunction |

```go
result, err := components.Decisions.Check(ctx, model.CheckRequest{
    Principal: principal, Permission: "view", Resource: project,
})
```

`Decisions` also offers `Evaluate` for an ad hoc expression, `CheckExplain`,
`CheckBatch`, `FilterAuthorized`, `FilterPage` and resource lookup. The model
is validated at construction and is immutable afterwards. A permission label is
not a role catalog: opaque labels remain legal to write, and only explicit
subject-shape rules reject a tuple.

### One read, one answer

Every operation reads from **one coherent snapshot** of the facts. Two separate
calls joined with Go's `&&` can observe two different committed states, so put
jointly-governing facts in one expression:

```go
components.Decisions.Evaluate(ctx, principal, decisions.All(
    decisions.Role("active"),
    decisions.Any(decisions.Role("admin"), decisions.RoleIn("owner", project)),
))
```

Evaluation is ordered and short-circuits, so an unread branch can never
introduce an error. Depth, fan-out and work are bounded by `WithLimits`.
Exhausting a budget returns `model.ErrEvaluationLimit`, an indeterminate error.
**Errors fail closed**: there is no partial allow and no truncated result that
looks complete.

On PostgreSQL an ambient transaction must actually be `REPEATABLE READ` or
`SERIALIZABLE`; the adapter checks and rejects `READ COMMITTED` before reading.
`pgxdb.TransactSnapshot` gives you a read-write workflow at the right level.
SQLite's `BEGIN IMMEDIATE` is fine as is.

## Guarding routes

`components.HTTP.Require` is the single middleware entry point. Compose the
route's whole policy as one predicate tree so it shares one snapshot and one
budget:

```go
import authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"

document := authorizationhttp.Path("document", "documentID")
guard := components.HTTP.Require(authorizationhttp.Any(
    authorizationhttp.HasRole("admin", authorizationhttp.Global()),
    authorizationhttp.All(
        authorizationhttp.HasRole("reviewer", document),
        authorizationhttp.Can("publish", document),
    ),
))
router.PATCH("/documents/{documentID}", handler, guard)
```

| Predicate | Needs a model | Meaning |
| --- | --- | --- |
| `HasRole(label, target)` | No | Exact fact on `Global()`, `Fixed(type, id)`, `Path(type, param)` or `Resource(type, resolver)` |
| `HasRelationship(label, target)` | Yes | Declared relation with userset expansion |
| `Can(permission, target)` | Yes | Declared named permission, including its traversal |

`Require` validates every branch at mount and panics on an invalid one, so a
bad policy fails at boot, not on the first request. At request time: no
principal returns **401**, a denial returns **403**, an exhausted budget returns
**503**, and any other error returns **500** with no internal detail. Reuse a
target value across predicates to resolve it once per request.

Two things `Require` deliberately does not do. It does not open a transaction
around the handler, so a permission revoked after admission does not cancel the
work in flight. And it does not distinguish which branch denied. If a route must
return 404 for invisibility and 403 for a missing action, write that as explicit
policy rather than inspecting reasons after the fact.

### Host-owned denial responses

Denial is 403 by default. To hide that a resource exists, hand the guard the same
handler your router uses for missing resources:

```go
guard := components.HTTP.Require(
    authorizationhttp.Can("view", authorizationhttp.Path("document", "documentID")),
    authorizationhttp.WithDeniedHandler(http.NotFoundHandler()),
)
```

The handler runs only after an error-free denial and never continues to the
protected handler. Authentication and evaluation errors keep their own status.

### Decision logging

Pass `authorization.WithLogger(logger)`. When the handler accepts `DEBUG`, each
completed `Decisions` operation emits one `authorization decision` record with
the operation, outcome and duration. Batches log aggregate counts; expressions,
result IDs and raw errors are never logged. Nested predicates, cache fallback and
retries do not produce duplicates. This is operational logging, not audit.

## Writing facts

Inbound authorizes the exact command, then calls a writer. Three writers exist,
and all of them enforce the store's integrity policy:

| Writer | Use it for |
| --- | --- |
| `RoleWriter`, `RelationshipWriter` | Ordinary assign, unassign, grant and revoke |
| `Mutations` | Atomic commands: `AssignRole`, `GrantRelationship`, `OpBatch` swaps, `OpReconcile` desired state, teardown |
| Raw `tuples.Storer` | Direct fact changes that join your own transaction |

`Mutations` requires `Repositories.Mutations`; without it `Components.Mutations`
is nil and the other writers remain. Atomic commands own their serialized
transaction and reject an ambient one. Raw writes join your transaction.

### Integrity policy

Configure the data rules once, on the store:

```go
store := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{
    Rules: []mutations.IntegrityRule{
        {ResourceType: "project", Relation: "owner", MinSubjects: 1},
    },
}))
```

The SQL adapters take the same `WithIntegrityPolicy` option. The default is
empty; `mutations.DefaultIntegrityPolicy()` opts into "one concrete owner on
every resource". Rules are checked against the **post-state inside the
serialized write**, so two concurrent removals cannot both delete the last owner
even if both callers were admitted. Usersets do not satisfy a concrete minimum.
A new protected resource must establish its required subjects in its first
write. The only bypass is `Mutations.TeardownResourceAuthorization`, which
requires a reason and preserves global facts; use it after the resource itself
is gone.

### Audit

Store `WithAudit()` records every committed change as one `added` or `removed`
row in `iam_audit`, **in the same commit as the fact**. If the audit row cannot
be written the fact rolls back. No-ops, refusals and rollbacks leave no history.

Every recording write needs attribution on the context:

```go
ctx = audit.WithSource(ctx, audit.Source{ActorType: "user", ActorID: principal.ID})
// or, from a host workflow:
ctx = audit.WithSource(ctx, audit.Source{System: "invitation-acceptance"})
```

Attribution is data, not access. The bundled role routes attach their
authenticated actor themselves. Retention and who may read the audit trail are
yours.

## The cache and eventual consistency

SQL is always the authority. The optional **TupleCache** mirrors raw facts in
Redis so checks can skip the database, and it is built as a transactional
outbox so the mirror can never silently diverge:

1. `0002_iam_tuple_cache.sql` adds triggers on `iam_tuples` that write each
   before/after fact into `iam_tuple_outbox` **inside the writing transaction**.
   Every writer participates, including raw SQL through the adapters.
2. A relay you run, `components.TupleCache.Poll`, reads the outbox, publishes the
   delta to Redis, and only then acknowledges the event IDs. Call `Notify()`
   after your outermost commit as a wake-up hint.
3. A read that hits a missing, expired, oversized or in-flight cache entry
   retries the **whole operation** on a durable SQL snapshot. Reads inside an
   ambient transaction always bypass Redis. Integrity and audit never use the
   cache.

The consistency window is the host-chosen `MaxStaleness`. A revocation may be
unseen for at most that long; a slow relay cannot extend it, because eligibility
is measured from the authoritative observation. Wire it like this:

```go
repos, err := pgx.Repositories(ctx, db, pgx.WithTupleCache())   // fills Repositories.TupleSource
backend, err := goredis.NewTupleCache(client, "authorization")
components, err := authorization.New(repos,
    authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: 5 * time.Second}),
)
// In a supervised host worker:
for {
    err := components.TupleCache.Poll(ctx)
    ...
}
```

The Redis adapter holds one complete hash on one shard, so Redis Cluster is
unsupported. Oversized reads fall back to SQL rather than returning a partial
result. Constructors start no goroutines; shutdown is yours.

## Bundled role administration

Optionally mount `POST /authorization/roles`, `POST /authorization/roles/unassign`,
`GET /authorization/roles/by-subject` and `GET /authorization/roles/by-resource`
by passing `authorization.WithRoleRoutes(authorizationhttp.RoleRoutes{Gate, WritePolicy})`.
The gate is your authentication, coarse access and CSRF stack. The write policy
receives the validated request and admits or refuses the exact command before
any write. A nil gate leaves the routes unmounted; a write policy without a gate
fails construction. Scope is explicit in the JSON:

```json
{"subject_type":"user","subject_id":"u1","role":"owner",
 "scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}
```

## Wiring it up

```go
store := memory.New(memory.WithIntegrityPolicy(policy))  // or the pgx / turso Repositories
components, err := authorization.New(
    authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()},
    authorization.WithModel(schema),
    authorization.WithLogger(logger),
)
```

`examples/auth-cms` is a complete host: a model, a narrowed integrity policy,
audit attribution from host workflows, and `Require` guarding both its own
routes and the authentication pocket's machine routes.

| Module | Version | Notes |
| --- | --- | --- |
| `pockets/authorization` | v0.22.0 | Core, `stores/memory`, `stores/storetest` conformance |
| `pockets/authorization/stores/pgx` | v0.16.0 | `migrations/0001_iam_tuples.sql`, optional `tuple_cache_migrations/0002_iam_tuple_cache.sql` |
| `pockets/authorization/stores/turso` | v0.15.0 | Same two files for SQLite/Turso |
| `pockets/authorization/stores/goredis` | v0.4.0 | TupleCache backend, protocol 2 |

Hosts apply the schema before boot; constructors validate it and never migrate.
The authorization Firestore adapter has been retired. Custom adapters run the
shared `stores/storetest` suites.

Read next: the
[README](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/README.md)
for the full API and consistency table,
[schema setup and upgrade notes](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/stores/UPGRADE.md)
for adopting each release, and
[benchmarks](https://github.com/gopernicus/gopernicus/blob/main/pockets/authorization/BENCHMARKS.md).
