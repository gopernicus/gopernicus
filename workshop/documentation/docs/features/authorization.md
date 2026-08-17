---
title: Authorization
description: Relationship-based authorization, roles, bounded evaluation, and guarded mutation.
---

# Authorization

`features/authorization` is the flagship IAM feature. It offers independently wireable authorization kinds:

- **relationships**: schema-driven ReBAC with direct relations, exact usersets, group expansion, and through traversal;
- **roles**: opaque role assignments with global and resource scopes;
- **policy**: a named future seam, not shipped.

## Choose a posture first

Authorization is supported, not required.

| Posture | Host choice | Feature dependency |
|---|---|---|
| none | leave consuming authorization seams nil; gated subsystems stay absent | none |
| host-authored | supply a narrow check closure over host data | none |
| flagship | construct this feature with relationships, roles, or both | authorization core + chosen store |

Other features accept check-shaped collaborators. They do not require the flagship module.

## Construction returns components

```go
components, err := authorization.NewService(repos, authorization.Config{
    Model: model,
})
if err != nil {
    return err
}

if err := components.Service.Register(mount); err != nil {
    return err
}
```

The `Components` bundle separates surfaces with different trust assumptions: the evaluation service, baseline relationship writer, optional guarded mutation service, and trusted system mutator.

`Register` currently mounts no routes and starts nothing. `/authorization/*` is reserved for a future admin surface.

## Relationship model

The host registers an immutable schema as code/data. A resource declares legal relations and permissions derived from them. Checks are pure evaluation against that compiled schema and relationship tuples.

Platform-admin and self-access are not hidden engine bypasses. Model platform administration as data and compose any self rule in the host's check closure before calling the engine.

The engine distinguishes exact usersets. A relation to `group:eng#member` does not accidentally grant through `group:eng#admin`. Evaluation is cycle-safe, cancellation-aware, and bounded by configured depth/state/result limits.

If an evaluation limit is reached, the result is indeterminate—not a denial that may be cached as policy truth. HTTP gates fail closed and can report service unavailable for limit exhaustion.

## Nil semantics

| Field | Meaning |
|---|---|
| `Repositories.Relationships` | relationship kind off when nil; requires `Config.Model` when present |
| `Repositories.Roles` | role kind off when nil |
| both kinds nil | construction error |
| `Repositories.Mutations` | optional high-integrity mutation path; required with guard/system mutation |
| `Config.Guard` | nil disables actor-facing guarded mutations; never default-allow |
| `Config.Audit` | optional best-effort audit; valid only with a guard |
| `Config.Limits` | zero fields choose safe defaults; negative values error |

## Middleware gate

For relationship-backed hosts, `Service.RequirePermission` creates SDK web middleware:

```go
router.GET(
    "/projects/{id}",
    showProject,
    authSvc.RequireUser,
    authorizationSvc.RequirePermission(
        "view",
        authorization.FixedResource("project", "main"),
    ),
)
```

Use a request-based resource resolver for path-dependent IDs. Constructing this middleware without the relationship kind is a boot-time panic: registration is the correct place to reveal contradictory wiring.

No principal returns 401, a false decision returns 403, evaluation-limit exhaustion returns 503, and resolver/infrastructure errors fail closed.

## Mutation paths

The feature exposes two relationship write postures:

- a baseline desired-state `RelationshipWriter` for trusted host workflows;
- an optional guarded/idempotent mutation lifecycle with mutation IDs, dependency revisions, receipts, last-owner/guardian protection, and atomic multi-write behavior.

Use guarded mutations for actor-facing access changes and sensitive administrative workflows. Use the trusted system surface only for bootstrap, migrations, or workflows whose authorization was already proven elsewhere.

## Stores and conformance

The public `memstore`, pgx store, and Turso store all run the same conformance suite. It covers adversarial graph shapes, exact usersets, cycles, bounded evaluation, role scoping, mutation replay, concurrency, revision conflicts, last-owner invariants, and check/lookup parity.

Store constructors probe required tables at boot. Export the authorization migration source into the host's ledger and apply it before constructing repositories.
