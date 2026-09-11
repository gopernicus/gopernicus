---
title: Architecture overview
description: The dependency model, module taxonomy, and composition rules of Gopernicus.
---

# Architecture overview

Gopernicus contains two related parts:

1. a collection of opinionated packages—`sdk`, `integrations`, `pockets`, and `ui`;
2. a hexagonal pattern for the application-specific code in a host.

Both use the same rule: business policy sits inward of delivery and infrastructure, and composition happens at the edge.

## The one rule

> A package may import inward toward policy and contracts. It must not import outward toward a concrete delivery mechanism, vendor, or host.

At repository scale:

<div class="dependency-flow">
  <div><strong>examples / cmd</strong><small>hosts</small></div><span>→</span>
  <div><strong>pockets / integrations</strong><small>hexagons + connectors</small></div><span>→</span>
  <div><strong>sdk</strong><small>kernel</small></div>
</div>

At application scale:

<div class="dependency-flow">
  <div><strong>inbound</strong><small>HTTP / views</small></div><span>→</span>
  <div><strong>logic</strong><small>domains + app</small></div><span>←</span>
  <div><strong>outbound</strong><small>stores / providers</small></div>
</div>

`cmd` is allowed to see every side because it is the composition root. Logic does not see an HTTP request, SQL driver, OAuth SDK, or concrete pocket adapter unless that concept is genuinely part of its own contract.

## The SDK is layered internally

The SDK is dependency-free as a Go module, but it is not a flat utility bag:

```text
sdk/                          kernel: cross-cutting errors + context vocabulary
  pkg/<package>       pure mechanism and vocabulary
  capabilities/<package>     behavioral ports + observable policy
```

- the root kernel imports no SDK subpackage;
- a package under `pkg/` may import only the root kernel, never another package under `pkg/`;
- a capability may import the kernel and pkg, never another capability;
- capability-to-capability composition leaves the SDK;

The separate `pockets` module expresses the shared host/pocket composition contract.
It depends on SDK and imports no concrete pockets.

This makes package placement predictive. “Used by many things” is not enough to enter the SDK; the concern must pass the SDK [admission test](../sdk/overview.md#admission-test).

## Module taxonomy

| Kind | Owns | May depend on |
|---|---|---|
| SDK | portable vocabulary, mechanism, behavioral policy | standard library + legal inward SDK tiers |
| integration | one third-party library/family or external vendor contract | SDK, wrapped dependency |
| shared pockets | host mounting contract over SDK | SDK only |
| pocket core | a reusable domain capability and its public ports | SDK + shared pockets |
| pocket store | one pocket's port implementations and SQL | pocket core, SDK, one datastore connector |
| pocket view | one pocket's `Views` implementation | pocket core, SDK, one UI implementation |
| UI implementation | reusable presentation system, components, assets | its view/runtime libraries and optionally SDK |
| host/example | provider choice, lifecycle, app policy | anything it intentionally composes |
| Workshop | source emitter and developer workflow | standard library; templates do not become runtime dependencies |

A separate Go module is a dependency boundary. External libraries and optional adapters earn modules so that importing a pocket core cannot silently add a driver or UI stack.

## Explicit composition, no container

There is no `init()` registry and no application-wide service locator. A host constructs values in dependency order:

```go
db, err := pgxdb.Open(ctx, dbConfig)
if err != nil { return err }

repos, err := authpgx.Repositories(ctx, db)
if err != nil { return err }

auth, err := authentication.New(
    repos, tokenSigner, mode, deliveryMode,
    authentication.WithPassword(authentication.PasswordConfig{Hasher: passwordHasher}),
    authentication.WithIdentity(authentication.IdentityConfig{ChallengeProtector: protector}),
    authentication.WithDelivery(deliverySettings),
    authentication.WithBrowser(browserSettings),
)
if err != nil { return err }

if err := auth.HTTP.Register(pockets.Mount{
    Router: router,
    Logger: logger,
    Events: bus,
}); err != nil { return err }
```

The host assembles coherent `DeliveryConfig` and `BrowserConfig` values for its
chosen mode, credentials and cookie policy. Options replace complete groups and
constructors validate their final combination. The host chooses concrete adapters,
passes typed collaborators inward and owns each returned lifecycle.

## Cross-pocket composition

Pocket cores never import one another. When pocket A needs behavior pocket B happens to provide:

1. A declares the narrow port it consumes, using stable types;
2. B exposes a public service method or middleware;
3. the host wires B into A, directly when structural typing matches or through a small host adapter.

Examples:

- CMS accepts `AdminMiddleware`; the host passes authentication's `RequireAccessToken()`.
- Authentication durable delivery depends on SDK work protocol interfaces; the jobs `queue.Service` implements them.
- Events stream middleware requires a principal; the host passes authentication middleware that stores one in context.

This prevents an optional pocket from becoming a hidden dependency of another pocket.

## Host-owned lifecycle

Pockets register routes but do not own the process:

- migrations are exported by store modules and applied by the host before boot;
- jobs and events expose runtimes or work functions; the host starts and stops them;
- UI packages expose renderers and assets; the host serves them;
- database, event bus, tracer, and provider shutdown stay in `main`;
- health routes describe the host's actual dependencies and therefore belong to the host.

Next, read [Repository layout](repository-layout.md) for the physical map or [Hexagonal host applications](hexagonal-apps.md) to design app-local logic.
