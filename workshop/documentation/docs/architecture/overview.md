---
title: Architecture overview
description: The dependency model, module taxonomy, and composition rules of Gopernicus.
---

# Architecture overview

Gopernicus contains two related parts:

1. a collection of opinionated packages—`sdk`, `integrations`, `features`, and `ui`;
2. a hexagonal pattern for the application-specific code in a host.

Both use the same rule: business policy sits inward of delivery and infrastructure, and composition happens at the edge.

## The one rule

> A package may import inward toward policy and contracts. It must not import outward toward a concrete delivery mechanism, vendor, or host.

At repository scale:

<div class="dependency-flow">
  <div><strong>examples / cmd</strong><small>hosts</small></div><span>→</span>
  <div><strong>features / integrations</strong><small>hexagons + connectors</small></div><span>→</span>
  <div><strong>sdk</strong><small>kernel</small></div>
</div>

At application scale:

<div class="dependency-flow">
  <div><strong>inbound</strong><small>HTTP / views</small></div><span>→</span>
  <div><strong>logic</strong><small>domains + app</small></div><span>←</span>
  <div><strong>outbound</strong><small>stores / providers</small></div>
</div>

`cmd` is allowed to see every side because it is the composition root. Logic does not see an HTTP request, SQL driver, OAuth SDK, or concrete feature adapter unless that concept is genuinely part of its own contract.

## The SDK is layered internally

The SDK is dependency-free as a Go module, but it is not a flat utility bag:

```text
sdk/                          kernel: cross-cutting errors + context vocabulary
  foundation/<package>       pure mechanism and vocabulary
  capabilities/<package>     behavioral ports + observable policy
  feature/                   sanctioned host↔feature composition
```

- the root kernel imports no SDK subpackage;
- a foundation package may import only the root kernel, never another foundation package;
- a capability may import the kernel and foundation, never another capability;
- capability-to-capability composition leaves the SDK;
- `sdk/feature` is the one package allowed to express the host/feature composition contract.

This makes package placement predictive. “Used by many things” is not enough to enter the SDK; the concern must pass the SDK [admission test](../sdk/overview.md#admission-test).

## Module taxonomy

| Kind | Owns | May depend on |
|---|---|---|
| SDK | portable vocabulary, mechanism, behavioral policy | standard library + legal inward SDK tiers |
| integration | one third-party library/family or external vendor contract | SDK, wrapped dependency |
| feature core | a reusable domain capability and its public ports | SDK only |
| feature store | one feature's port implementations and SQL | feature core, SDK, one datastore connector |
| feature view | one feature's `Views` implementation | feature core, SDK, one UI implementation |
| UI implementation | reusable presentation system, components, assets | its view/runtime libraries and optionally SDK |
| host/example | provider choice, lifecycle, app policy | anything it intentionally composes |
| Workshop | source emitter and developer workflow | standard library; templates do not become runtime dependencies |

A separate Go module is a dependency boundary. External libraries and optional adapters earn modules so that importing a feature core cannot silently add a driver or UI stack.

## Explicit composition, no container

There is no `init()` registry and no application-wide service locator. A host constructs values in dependency order:

```go
db, err := pgxdb.Open(dbConfig)
if err != nil { return err }

repos, err := authpgx.Repositories(db)
if err != nil { return err }

auth, err := authentication.NewService(repos, authentication.Config{
    RuntimeMode: mode,
    Hasher:      passwordHasher,
    Mailer:      sender,
    TokenSigner: tokenSigner,
    // challenge protection and delivery posture omitted here
})
if err != nil { return err }

if err := auth.Register(feature.Mount{
    Router: router,
    Logger: logger,
    Events: bus,
}); err != nil { return err }
```

The exact authentication config is richer than this abbreviated shape. What matters here is direction: the host chooses concrete adapters, passes only typed seams inward, and owns every returned lifecycle.

## Cross-feature composition

Feature cores never import one another. When feature A needs behavior feature B happens to provide:

1. A declares the narrow port it consumes, using stable types;
2. B exposes a public service method or middleware;
3. the host wires B into A, directly when structural typing matches or through a small host adapter.

Examples:

- CMS accepts `AdminMiddleware`; the host passes authentication's `RequireUser`.
- Authentication durable delivery depends on SDK work protocol interfaces; the jobs `Service` implements them.
- Events stream middleware requires a principal; the host passes authentication middleware that stores one in context.

This prevents an optional feature from becoming a hidden dependency of another feature.

## Host-owned lifecycle

Features register routes but do not own the process:

- migrations are exported by store modules and applied by the host before boot;
- jobs and events expose runtimes or work functions; the host starts and stops them;
- UI packages expose renderers and assets; the host serves them;
- database, event bus, tracer, and provider shutdown stay in `main`;
- health routes describe the host's actual dependencies and therefore belong to the host.

Next, read [Repository layout](repository-layout.md) for the physical map or [Hexagonal host applications](hexagonal-apps.md) to design app-local logic.
