# Authorization

Authorization provides opt-in relationship and role services. A host can use
relationship permissions, opaque role assignments, modeled role permissions, or
both permission models. Hosts own the policy, repositories and HTTP middleware.
The pocket provides no default allow policy and performs no startup migrations.

## Public packages

| Package | Owns |
| --- | --- |
| `authorization` | `New`, typed `Option` values, `Repositories`, named `Components`, optional mounting |
| `logic/relationships` | Tuple/subject types, schema DSL/compiler, store ports, ReBAC/read `Service`, trusted `RelationshipWriter` |
| `logic/roles` | Assignment types, store ports, role read `Service`, separately constructed trusted raw `Writer` |
| `logic/decisions` | Permission `Service` across models, complete-ID/paged-ID helpers and `FilterPage` |
| `logic/mutations` | Atomic commands/ports, guarded `Service`, `MutationGuard`, `DecisionView`, guardian policy, trusted `SystemMutator` |
| `logic/model` | Principals, resources, checks/results, explanations, budgets, immutable role permission models |
| `logic/audit` | Committed audit records, attribution and reader port |
| `inbound/http` (`authorizationhttp`) | Validated `Adapter`, permission middleware, optional role handlers |
| `stores/memory` | In-memory reference stores |
| `stores/storetest` | Shared store conformance suites |

`stores/pgx`, `stores/turso` and `stores/firestore` are separate adapter modules.
The core imports no store technology. Public services own their contracts and
behavior; the root composes them without a parallel forwarding service.
`internal/decisioncursor` contains shared cursor encoding mechanics only.

## Compose once, pass the capabilities callers need

```go
import (
    "github.com/gopernicus/gopernicus/pockets/authorization"
    authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
    "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

components, err := authorization.New(repos,
    authorization.WithRelationshipModel(relationships.NewSchema([]relationships.ResourceSchema{{
        Name: "document",
        Def: relationships.ResourceTypeDef{
            Relations: map[string]relationships.RelationDef{
                "viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
            },
            Permissions: map[string]relationships.PermissionRule{
                "view": relationships.AnyOf(relationships.Direct("viewer")),
            },
        },
    }})),
    authorization.WithGuard(hostMutationGuard),
)
if err != nil {
    return err
}

result, err := components.Decisions.Check(ctx, authmodel.CheckRequest{
    Principal: authmodel.PrincipalRef{Type: "user", ID: userID},
    Permission: "view",
    Resource: authmodel.Resource{Type: "document", ID: documentID},
})
```

`Components` contains `Decisions`, `Relationships`, `Roles`, `Mutations`, `HTTP`,
`RelationshipWriter`, `SystemMutator` and optional `ReadCache`. Give request handlers the concrete
service or narrow local interface they need. Retain the full bundle and trusted
writers at host composition.

- A relationship repository requires a relationship schema, and vice versa.
- A roles repository can be used without a role model. `Roles` then supports
  opaque role checks and listing; no role permission engine is created.
- `RoleModel` requires a roles repository. A host with neither a relationship
  schema nor a role model has no `Decisions` component.
- A missing relationship or roles repository leaves that named component nil.
- A nil `Guard` disables actor-facing mutations. A nonnil guard requires an
  atomic mutation repository. Baseline writes do not require that repository.
- `Logger` is captured at construction, defaulting to `slog.Default()`.
  Mounting does not replace it.

Typed-nil dependencies are rejected at construction. Permission methods on a nil
`Decisions` component report `model.ErrNoDecisionKind`; ordinary nil role reads
report `roles.ErrRolesNotConfigured`. Prefer selecting the services your host
actually configured at boot.

## Construction options

The root constructor is `New(repos Repositories, opts ...Option)`. Required
per-kind ports stay in `Repositories`; optional policy uses these named values:

| Option | Value and behavior |
| --- | --- |
| `WithRelationshipModel` | Complete relationship schema; required with the relationship repository |
| `WithRoleModel` | Complete role permission model; empty restores opaque role facts |
| `WithLimits` | Common `model.EvaluationLimits`; zero dimensions default when used |
| `WithGuard` | Actor-facing atomic mutation policy; nil disables actor writes |
| `WithCacher` | Borrowed `cacher.Storer` and `decisions.CachePolicy`; nil disables caching and ignores policy/source |
| `WithLogger` | Borrowed operational logger; nil uses `slog.Default()` |
| `WithRoleRoutes` | Complete `authorizationhttp.RoleRoutes` gate, assignment policy and listing defaults |

Options apply in order and replace their entire value/group. They do not merge
nonzero fields. Replacing role routes with `RoleRoutes{}` removes the prior gate
and assignment policy, leaving handlers disabled. An assignment policy without a
gate is invalid. A nonzero invalid list strategy fails even with no gate; a valid
orphaned strategy is ignored. Limits on an opaque, unguarded roles-only host retain
the existing unused-budget behavior.

All four construction families reject nil options with an error wrapping
`sdk.ErrInvalidInput`. A valid option carrying a nil logger, guard or disabled
route gate keeps its documented meaning. Source model maps/slices are captured
by value; services, immutable compiled models and callbacks remain borrowed.
There is no supported option application against a running service.

## Construct services directly

The same implementations are available without root composition:

```go
relationshipParts, err := relationships.NewService(
    relationshipStore, relationshipSchema, relationships.WithLimits(limits),
)
if err != nil { return err }

roleService, err := roles.NewService(roleStore)
if err != nil { return err }

decisionService, err := decisions.NewService(decisions.Readers{
    Relationships: relationshipParts.Service,
    Roles: roleService,
}, decisions.WithRoleModel(roleModel))
if err != nil { return err }

mutationParts, err := mutations.NewService(mutationRepository, mutations.Services{
    Relationships: relationshipParts.Service,
    Roles: roleService,
},
    mutations.WithRoleModel(decisionService.CompiledRoleModel()),
    mutations.WithGuard(hostMutationGuard),
)
if err != nil { return err }
```

Imports in this example are the corresponding `logic/roles`, `logic/decisions`
and `logic/mutations` packages. Services with a supplied relationship engine
inherit its limits when their entire `Limits` value is zero. Explicit limits
must resolve to exactly the engine's limits; mismatched budgets fail construction.
A mutation service also rejects an independently compiled role model that
conflicts with the supplied relationship model.

`relationships.NewService` and `mutations.NewService` return small capability
bundles because their trusted writers must be held separately. `roles.NewService`
and `decisions.NewService` return their actual service and an error.

## Models and decisions

Relationship tuples are keyed by resource type, resource ID, relation, subject
type, subject ID and optional subject relation. A concrete `group:g1` and a
userset `group:g1#member` are different subjects. Only the exact userset expands;
`#member` never expands `#admin`, and a concrete group reference does not
implicitly mean group membership. The current schema controls every permission
read, including reads inside mutation guards and lookup validation.

`relationships.Through("parent", "view")` follows navigation to another
resource's permission. Self-referential hierarchies are supported; graph cycles
and work limits are handled by the evaluator. `relationships.AnyOf` groups alternative permission checks.

Model options snapshot their source maps and slices when created; constructors
compile independent immutable models. Changing the input maps after option
creation or construction cannot change decisions. `Relationships.GetSchema()`
returns an immutable snapshot; its collection accessors return copies.
`SchemaDigest()` and `GetSchema()` return values directly, without an error.

A `model.RoleModel` declares roles and the permissions they grant per resource
type. An exact scoped role grant is checked first, then the principal's global
assignment. A global assignment applies only to permissions whose model names
that role. Resource-independent permissions can use a singleton resource type.
Opaque role reads and unassignment remain useful for inspecting/removing old
facts; modeled permission decisions and guarded assignments use the current
model.

Each `(resource type, permission)` pair belongs to exactly one model. Declaring
it in both is `model.ErrModelConflict`. A type may exist in both models with
different permissions. The decision service dispatches to the owning model;
it does not combine unrelated role and relationship answers. Host-specific
administrator or self-access rules belong in host policy.

`Check`, `CheckExplain`, `CheckBatch` and `FilterAuthorized` fail closed on errors.
A denied result is different from an indeterminate error. `ReasonCode` gives a
stable classification; explanation traces describe the owning evaluator.

## Lists and evaluation budgets

`model.EvaluationLimits` bounds traversal depth, distinct graph states,
evaluation steps, relation fan-out, batch size, lookup results and candidate
scanning. Zero fields select finite defaults; negatives are rejected. Exhausted
evaluation returns `model.ErrEvaluationLimit` (`sdk.ErrUnavailable`), never a
complete-looking truncated result. Store query counts remain adapter telemetry,
not an interchangeable semantic budget.

Relationship `CheckBatch` batches both direct checks and `Through` reads when
the model-scoped reader implements `relationships.RelationSetReader` (the bundled
stores and their cache snapshots do). Compatible pending reads are fetched
together, then the ordinary evaluator resumes each request with its own depth,
cycle state, work budget and short-circuit order. This also accelerates
`FilterAuthorized` and lookup verification; no verification bypass is needed.
The relations may have any declared name and may target different resource types.

Read counts depend on the encountered branches, traversal stages and adapter
chunks instead of one read per candidate per hop. Data volume and evaluation
work still grow with the graph. Custom readers without the optional capability
retain sequential checks with shared fact reads. The batch does not enumerate
the principal's entire accessible resource set.

Optional TupleCache preserves batched reads for `Decisions.CheckBatch` and
`Decisions.FilterAuthorized`, including durable snapshot retries when the mirror
is unavailable or changes during evaluation. Cached raw tuples are filtered by
the current model on every operation. Lookups retain their durable-read behavior;
see [Optional TupleCache](#optional-tuplecache) for freshness and capacity limits.

Three list workflows are supported:

1. `Decisions.LookupAllResourceIDs` returns a bounded complete `ResourceSet` for
   hosts that can filter their own storage query with IDs. It fails when the
   complete set exceeds the configured cap.
2. `Decisions.LookupResourceIDPage` enumerates one page of authorized IDs. Treat
   it as enumeration, not as permission to claim a globally complete set.
3. `decisions.FilterPage` scans a host-owned ordered candidate source, checks
   authorization in batches, and fills a visible page within a scan budget.
   Resume using its cursor. `ScanLimitReached` identifies a partial page that
   ended at the scan budget rather than at the end of the underlying collection.

Cursors bind the query, principal, resource type, permission, owning model and
model digest. Reusing them for a changed query or model fails with an invalid
cursor error. They are continuation state, not authorization grants.

Relationship lookups use one durable read snapshot per call in the Turso
(SQLite), PostgreSQL and memory adapters. Candidate discovery, verification
batches and page lookahead see the same state. A concurrent revocation is visible
to the next call; separate pages do not share a snapshot. SQL lookups inside an
ambient transaction retain that transaction's pending writes and isolation level
without committing or rolling it back.

Custom model-scoped readers can implement `relationships.LookupSnapshotter`.
Readers without it (including Firestore), and ambient transactions with weaker
isolation, retain discovery/verification checks. If a discovered grant is denied
during verification, the pocket retries the entire lookup twice with fresh
attempt-local state. Three mismatches return `model.ErrEnumerationContended`,
wrapping `sdk.ErrUnavailable` (HTTP 503), with no partial IDs or cursor. Hosts can
use `errors.Is` to add `Retry-After`. Store errors, cancellation and exhausted
evaluation budgets are returned immediately. Unrelated writes do not themselves
invalidate enumeration; there is no global relationship revision guard.

The host owns content ordering, SQL joins, counts and pagination semantics.
`examples/auth-cms` exercises separate-store ID filtering, ordered candidates and
SQL pushdown. Do not join authorization rows with ad hoc semantics: a correct
join must preserve exact usersets, scope fallback, current model ownership and
limit behavior. Shared helpers make the separate-store paths usable without
making a particular database mandatory.

## Choose the write capability deliberately

`relationships.RelationshipWriter` supports trusted baseline state operations:
`CreateRelationships`, `SetRelationTargets`, `DeleteRelationship`, and
`DeleteResourceRelationships`. The last three take a `model.Resource`.
Additions validate the current schema. These operations bypass actor guards and
guardian minimums; they are intended for host-managed facts and provisioning.
They use the store's ambient transaction and audit contracts. A read service
cannot return or manufacture its writer.

`roles.Writer` is an explicit trusted raw role writer constructed from a raw
`roles.Storer`. It validates tuple structure, but has no role model, guard or
guardian policy. Ordinary actor-facing assignment and unassignment belong to
`mutations.Service`.

The guarded mutation service provides typed assignment, unassignment, grant,
revoke, replace and resource-purge operations. The host guard receives an actor,
an immutable proposed change and a `mutations.DecisionView`. It must authorize
against that view: its reads execute within the repository's atomic operation,
so checks and changes cannot race each other through unrelated outer reads.
Repository retries receive a fresh proposed change. There is no mutation receipt
ledger or idempotency key; repeated commands apply normal tuple/no-op semantics.

`mutations.Result` reports the committed outcome (`applied`, `no_change` or
`not_found`). Policy, semantic and invariant failures are errors.
`mutations.ReasonFor` classifies mutation errors. Unassignment also reports
whether the same role remains effective through another scope.

Guardian rules protect configured minimum anchor counts. Rules must match the
host model. Actor purge is bounded by `MaxBatchSize` and keeps guardian rules.
Trusted resource deletion uses `SystemMutator.TeardownResourceAuthorization`
with a required bounded reason. `SystemMutator.Apply` cannot bypass that reason
requirement by submitting a generic teardown command.

## HTTP adapters and host-owned routes

```go
import authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"

adapter, err := authorizationhttp.New(authorizationhttp.Services{
    Decisions: components.Decisions,
    Roles: components.Roles,
    Mutations: components.Mutations,
}, authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
    Gate: hostRoleAdministrationMiddleware,
}))
if err != nil { return err }

mux.Handle("POST /admin/roles", adapter.AssignRole())
mux.Handle("GET /admin/roles", adapter.RolesBySubject())
mux.Handle("GET /documents/{id}", adapter.RequirePermissionOn(
    "document", "view", "id",
)(documentHandler))
```

Pass only configured services; omit absent optional interface fields instead of
putting typed-nil pointers in them. Root composition handles this wiring.

The adapter validates dependencies, budgets, role-route posture and list strategy.
Individual role handler builders retain the configured gate, including when the
host chooses different routes. A nil gate disables all five role handlers, whose
individual builders then return not-found handlers. The host gate is responsible
for authentication, authorization and browser-origin/CSRF protection when needed.
Handlers still reject requests without a concrete SDK principal.

For bundled paths, use `adapter.Register(router)` or root
`components.Register(mount)`. Pass `authorization.WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: gate})`
to root construction to enable:

| Method | Bundled path |
| --- | --- |
| POST | `/authorization/roles` |
| POST | `/authorization/roles/unassign` |
| GET | `/authorization/roles/by-subject` |
| GET | `/authorization/roles/by-resource` |
| GET | `/authorization/roles/effective` |

A role gate requires role reads and guarded writes. An assignment-only legality
policy can use `RoleRoutes.AssignmentPolicy`; it requires
enabled role routes and supplements the atomic guard. The default list strategy
is cursor; offset is also supported. List strategy is validated even when routes
are disabled. Enabled bundled routes require a usable router.

Permission middleware includes `RequirePermission`, `RequirePermissionOn`,
`RequirePermissionFixed` and `RequireAnyPermission`, with `FixedResource`,
`PathResource` and `GateSpec` helpers. Static invalid coordinates fail during
route setup. Runtime errors use `RespondError`: no principal is 401, a denied
permission is 403, an exhausted evaluation is 503, and an infrastructure failure
is 500. Host custom routes can call the same mapper.

## Stores, audit and upgrades

Relationships and role assignments are composite-keyed facts without synthetic
IDs or creation timestamps. Their deterministic raw listing order uses the tuple
fields. Role listings distinguish exact assignment rows from effective grants
that merge scoped and global provenance.

Committed audit history is opt-in at store construction. `iam_audit` records
applied changes and attribution atomically with the write. It is neither an
idempotent receipt ledger nor a denied-attempt log. Hosts own audit access,
retention and export; `Repositories.Audit` supplies reads when available.

Run the shared `stores/storetest` suites for store correctness. Host-owned
migrations and dialect requirements are documented in:

- [Store upgrade runbook](stores/UPGRADE.md)
- [Store conversion guidance](stores/CONVERSION.md)
- [PostgreSQL](stores/pgx/README.md)
- [Turso](stores/turso/README.md)
- [Firestore](stores/firestore/README.md) and its [upgrade guide](stores/firestore/UPGRADE.md)

The package reorganization changes imports, construction and receiver ownership;
it changes no SQL schema, tuple format, cursor encoding or audit storage format.
Consumer migration notes live in the repository's `AUDIT.md`.

## Optional TupleCache

TupleCache maintains **raw relationships** in Redis. The configured Turso or
PostgreSQL store remains authoritative. Each tuple mutation commits its complete
before/after payload into a transactional outbox. Delivery updates only the raw
forward/reverse sets containing that tuple, then deletes the acknowledged events.
Redis population and recovery read current authoritative tuples; processed events
are not needed for reconstruction. No permission answers, expanded memberships,
role facts or authorized-resource lists are cached.

`WithTupleCache(backend, policy)` enables raw cached reads for `Decisions.Check`,
`CheckBatch`, `CheckExplain` and `FilterAuthorized`. Permission evaluation and model
filtering run on every call. Lookups/enumeration, direct relationship and role
services, mutation guards, audit and authentication retain their durable paths.
Candidate filtering uses the same cached batch path on relationship-only hosts;
one filter call validates one mirror receipt and retries the entire candidate
batch against the durable store if the mirror is unavailable or changes.
A role read in a cached operation retries the whole operation in one authoritative
snapshot, including relationship reads in a mixed batch.

### Construction and delivery

Apply the base **authorization** migrations through 0007, then the separate
optional **authorization-cache** source through 0002 in the same database/schema.
Construct the SQL repository bundle with its store's `WithTupleCache()` option.
This validates the schema and supplies `Repositories.TupleSource`. Ordinary SQL
writers are captured by the installed triggers, including processes without the
cache option. See the [Turso](stores/turso/README.md) and
[PostgreSQL](stores/pgx/README.md) runbooks for privileges and raw-write constraints.

```go
// repos comes from the configured SQL store with its WithTupleCache() option.
// client is a host-owned *redis.Client. Choose maxStaleness explicitly.
backend, err := goredis.NewTupleCache(client, "my-app/relationships")
if err != nil {
    return err
}
components, err := authorization.New(repos,
    authorization.WithRelationshipModel(model),
    authorization.WithTupleCache(backend, tuplecache.Policy{
        MaxStaleness: maxStaleness,
    }),
)
if err != nil {
    return err
}
relay := components.TupleCache
pool := workers.NewPool(relay.Poll,
    workers.WithPollInterval(relay.PollInterval()),
    workers.WithIdleInterval(relay.PollInterval()),
    workers.WithWakeChannel(relay.WakeChannel()),
)
// Run pool.Run(ctx) as a supervised host worker and handle its returned error.
// After the OUTERMOST tuple-writing transaction successfully commits:
relay.Notify()
```

The imports are `pockets/authorization`, `pockets/authorization/logic/tuplecache`,
`pockets/authorization/stores/goredis` and `sdk/pkg/workers` under the framework
module prefix. [Redis adapter details](stores/goredis/README.md) document storage,
persistence and capacity. `memory.NewTupleCache()` is a reference backend for
local tests; the standalone memory and Firestore authorities have no TupleSource.

Constructors start no goroutines. Each runtime uses durable reads until its first
successful `Poll`. `Notify()` is a non-blocking, coalesced channel hint, not a second
copy of the mutation; the outbox is the durable queue. The host sends the hint only
after its outer transaction commits. Periodic polling handles external writers,
lost notifications and failed deliveries. A committed SQL mutation stays committed
if Redis delivery fails. Call `Poll(ctx)` after commit when the caller must await
that publication, and handle any error as a delivery error, not a rolled-back
mutation. Concurrent local Poll calls may return `workers.ErrNoWork`.

### Read guarantees and limits

`MaxStaleness` is a required positive host choice: a committed revocation can
remain unseen while delivery is pending, within that bound. Eligibility is measured
from the start of an authoritative source observation, not from a later Redis
write. A stuck relay cannot extend it. An expired/unavailable mirror causes a
whole-operation durable snapshot retry. A publication during evaluation also
causes a whole-operation retry, so one returned decision cannot mix publications.
Ambient source transactions bypass Redis and retain their existing store view.

Processes sharing one mirror **must use the same MaxStaleness**. The stable mirror
binding includes this policy; a mismatch returns `tuplecache.ErrBinding` instead
of letting one process weaken another's bound. Use one Redis mirror/namespace per
source delivery stream. Multiple relay processes may share it; independently
maintained namespaces for the same source compete over its delivery receipt and
cause repeated rebuilds. To change the shared policy, stop/drain old runtimes and
rebuild into a fresh namespace before reopening readers.

Delivery receipts coordinate atomic publication, operation consistency and recovery.
They are not generations in tuple keys: unchanged sets remain available after
ordinary mutations, without refilling. An empty or older restored Redis mirror is
rebuilt from current source tuples, even when the original outbox rows are gone.
A restored Redis dataset can still be served within its previously certified
freshness interval before the relay detects it. For stricter reads, use an
uncached service against the authoritative store.

The Redis adapter keeps the complete mirror in one hash on one shard. It reads
and decodes whole relation sets before the evaluator charges its graph-state
budget; that budget is not a memory/byte bound. Large sets and large pending
batches need host capacity measurements. A full snapshot/build taking longer than
MaxStaleness stays unavailable rather than publishing already-expired authority.
SQL remains the fallback. This implementation does not support Redis Cluster.

At shutdown cancel and join the host worker, close the runtime, and drain requests
before closing the borrowed Redis client or database. `Close()` stops cache use;
it does not own or join workers. Snapshot callback readers are sequential and
must not escape into goroutines or outlive the callback. Inspect `Stats()` for
hits, durable fallbacks, publications, rebuilds and poll failures.

### Upgrade from the previous cache

Stop old cache-enabled binaries before applying optional migration 0002. It removes
the global counter and replaces generation triggers with complete tuple capture;
published migration 0001 remains unchanged. Replace `WithCacher` / `ReadCache` /
`WithCacheReads` with the APIs above and give Redis a fresh dedicated namespace.
Old cache keys may expire separately. Firestore and memory hosts should remove the
old cache options and use durable reads. Do not modify optional delivery metadata
or bypass triggers while readers/relays are running; follow the store runbooks for
authoritative restores, cloning and imports.

Local SQL/Redis behavior and outstanding deployment checks are recorded in the
[TupleCache implementation plan](../../plans/authorization-tuple-cache.md).
