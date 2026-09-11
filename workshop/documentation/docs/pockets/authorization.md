---
title: Authorization
description: Relationship-based authorization, roles, bounded evaluation, and guarded mutation.
---

# Authorization

`pockets/authorization` is the flagship IAM pocket. It offers independently wireable authorization kinds:

- **relationships**: schema-driven ReBAC with direct relations, exact usersets, group expansion, and through traversal;
- **roles**: opaque role assignments, either global or attached to a resource, with optional permission rules supplied by the host.

## Choose a posture first

Authorization is supported, not required.

| Posture | Host choice | Pocket dependency |
|---|---|---|
| none | leave consuming authorization seams nil; gated subsystems stay absent | none |
| host-authored | supply a narrow check closure over host data | none |
| flagship | construct this pocket with relationships, roles, or both | authorization core + chosen store |

Other pockets accept check-shaped collaborators. They do not require the flagship module.

## Construction returns components

```go
components, err := authorization.New(repos,
    authorization.WithRelationshipModel(relationshipModel),
    authorization.WithRoleModel(roleModel),
)
if err != nil {
    return err
}

if err := components.Register(mount); err != nil {
    return err
}
```

The root assembles public components with distinct responsibilities:

| Component | Public owner | Purpose |
|---|---|---|
| `Decisions` | `logic/decisions.Service` | check, explain, lookup and filtered list operations |
| `Relationships` | `logic/relationships.Service` | relationship reads, schema and ReBAC evaluation |
| `Roles` | `logic/roles.Service` | role assignment reads |
| `Mutations` | `logic/mutations.Service` | atomic actor-facing guarded changes |
| `HTTP` | `inbound/http.Adapter` | permission middleware and optional role administration routes |
| `RelationshipWriter` / `SystemMutator` | separate writer types | explicitly held trusted changes |

Only configured components are present. Shared principal, check and role-model
vocabulary lives in `logic/model`; persistence ports live with their owners.
Hosts may construct these public services and adapters directly. Their state
and implementation helpers remain private.

Bundled role-administration routes mount only with a configured host gate and
guarded mutation service. `Register` starts no background work. A host can skip
bundled routes and use the public middleware on its own routes.

## Constructor options

Repositories remain explicit in `New(repos, opts ...Option)`. Models and budgets
are coherent values: `WithRelationshipModel(schema)`, `WithRoleModel(roleModel)`
and `WithLimits(model.EvaluationLimits{...})`. Use `WithGuard` for actor policy and
`WithLogger` for diagnostics. `WithRoleRoutes(authorizationhttp.RoleRoutes{...})`
groups the optional gate, assignment pre-check and listing strategy.

Every option replaces its entire setting or group. `WithRoleRoutes(RoleRoutes{})`
clears a previous gate and assignment policy, leaving handlers disabled. An
assignment policy without a gate fails construction; an invalid list strategy
fails even without a gate. Nil options return `sdk.ErrInvalidInput` errors.
Model options snapshot source maps and slices when created; constructed services
compile immutable models. Host services, callbacks and loggers remain borrowed.

Focused constructors use the same convention:

```go
checks, err := decisions.NewService(
    decisions.Readers{Relationships: relationshipService, Roles: roleService},
    decisions.WithRoleModel(roleModel),
)
if err != nil { return err }

writes, err := mutations.NewService(
    mutationRepository,
    mutations.Services{Relationships: relationshipService, Roles: roleService},
    mutations.WithRoleModel(checks.CompiledRoleModel()),
    mutations.WithGuard(hostGuard),
)
if err != nil { return err }

adapter, err := authorizationhttp.New(
    authorizationhttp.Services{Decisions: checks, Roles: roleService, Mutations: writes.Service},
    authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: hostGate}),
)
if err != nil { return err }
```

Services with relationships inherit that service's limits when `WithLimits` is
omitted or entirely zero. Explicit limits must resolve to the same budget.
Only supply initialized interface-valued services; typed-nil dependencies fail
construction. The root assembly handles optional component wiring automatically.

## Relationship model

The host registers an immutable schema as code/data. A resource declares legal relations and permissions derived from them. Checks are pure evaluation against that compiled schema and relationship tuples.

Platform-admin and self-access are not hidden engine bypasses. Model platform administration as data and compose any self rule in the host's check closure before calling the engine.

The engine distinguishes exact usersets. A relation to `group:eng#member` does not accidentally grant through `group:eng#admin`. Evaluation is cycle-safe, cancellation-aware, and bounded by configured depth/state/result limits.

If an evaluation limit is reached, the result is indeterminate—not a denial that may be cached as policy truth. HTTP gates fail closed and can report service unavailable for limit exhaustion.

## Nil semantics

| Field | Meaning |
|---|---|
| `Repositories.Relationships` | relationship kind off when nil; requires `WithRelationshipModel` when present |
| `Repositories.Roles` | role kind off when nil |
| both kinds nil | construction error |
| `Repositories.Mutations` | optional high-integrity mutation path; required with guard/system mutation |
| `WithGuard` | nil disables actor-facing guarded mutations; never default-allow |
| Store `WithAudit()` | optional atomic change history for raw, trusted and guarded writes; host owns access and retention |
| `WithLimits` | zero fields choose safe defaults; negative values error |

## Middleware gate

The HTTP adapter produces SDK web middleware for either model-bearing kind:

```go
router.GET(
    "/projects/{id}",
    showProject,
    authenticationComponents.HTTP.RequireAccessToken(),
    components.HTTP.RequirePermissionOn("project", "view", "id"),
)
```

`RequirePermissionOn` resolves the named path parameter and validates the
resource/permission pair at route registration. `RequirePermissionFixed` covers
fixed resources; `RequirePermission` accepts a custom resolver. A roles-only host
without a role permission model cannot build permission gates. The public
`authorizationhttp.New` constructor accepts a narrow decision-service contract
for hosts constructing adapters independently.

No principal returns 401, a false decision returns 403, evaluation-limit exhaustion returns 503, and resolver/infrastructure errors fail closed.

## Mutation paths

The pocket exposes two relationship write postures:

- a baseline desired-state `RelationshipWriter` for trusted host workflows;
- optional guarded mutations with current-model validation, guardian protection and atomic writes; store-level `WithAudit()` records actual committed additions/removals with actor or system attribution.

Use guarded mutations for actor-facing access changes and sensitive administrative workflows. Use the trusted system surface only for bootstrap, migrations, or workflows whose authorization was already proven elsewhere.

## Stores and conformance

The public `stores/memory`, pgx, Turso and Firestore stores all run the same conformance suite. It covers adversarial graph shapes, exact usersets, cycles, bounded evaluation, role scoping, state convergence, raw/guarded concurrency, atomic audit history, last-owner invariants, and check/lookup parity.

Store constructors probe required tables at boot. Export the authorization migration source into the host's ledger and apply it before constructing repositories.

Audit readers are exposed as `Repositories.Audit`. Trusted/raw calls use
`audit.WithSource(ctx, audit.Source{System: "bootstrap"})` from `logic/audit`
when recording is enabled. Guarded calls attribute changes to their actor. No-op,
denied and rolled-back changes add no history. SQL migration `0007` removes the
receipt/revision tables and adds `iam_audit`; see the repository’s `AUDIT.md` entry AUDIT-026.
