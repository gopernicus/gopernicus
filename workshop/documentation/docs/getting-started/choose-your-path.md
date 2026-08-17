---
title: Choose your path
description: A role-based map through the Gopernicus documentation.
---

# Choose your path

Gopernicus has several extension levels. Start with the path closest to the change you need, then move inward only when the existing seam is insufficient.

## I am composing an application

Your primary artifact is a host `main` package.

1. Run the [quickstart](quickstart.md).
2. Learn the [dependency rule](../architecture/overview.md#the-one-rule).
3. Review the [feature catalog](../features/overview.md) and [integration catalog](../integrations/catalog.md).
4. Follow [Compose a host](../guides/compose-host.md).
5. Choose a persistence posture in [Persistence and migrations](../guides/persistence.md).

Most application customization belongs here: choose implementations, configure feature seams, add host routes, wrap a route registrar, implement a repository, or supply views.

## I am adding app-local business logic

Use the host hexagon pattern:

1. Read [Hexagonal host applications](../architecture/hexagonal-apps.md).
2. Put entities, ports, and services in `internal/logic/domains/<domain>`.
3. Put HTTP and rendering adapters in `internal/inbound`.
4. Put database and provider adapters in `internal/outbound`.
5. Compose all three from `cmd`.

App-local code does not need to become a reusable package. Keep it private until independent reuse and pluggability are real requirements.

## I am authoring a reusable feature

A feature is a library-shaped hexagon with a public rim and sealed interior.

1. Read the [Feature contract](../architecture/feature-contract.md) completely.
2. Use [Create a feature](../guides/create-feature.md).
3. Start with `gopernicus new feature` if the emitted CRUD-shaped anatomy fits.
4. Keep the core SDK-only and put each external datastore or view dependency in a sibling module.
5. Publish a conformance suite for every outbound repository contract.

The feature contract is intentionally stricter than an ordinary app-local domain because independent hosts must be able to compose it safely.

## I need a reusable facility

Decide whether the concern belongs in the SDK before adding another package:

- pure mechanism or vocabulary with zero service semantics → `sdk/foundation`;
- a narrow behavioral port plus shared observable policy → `sdk/capabilities`;
- a third-party library or vendor API contract → `integrations`;
- a complete domain capability with entities, ports, and use cases → `features`;
- one application's policy → keep it app-local.

Read [SDK overview](../sdk/overview.md), especially the admission test.

## I am building a separate React client

Start with the [React and TanStack guide](../ui/react.md), then use the [web foundation](../sdk/web.md) and [Compose a host](../guides/compose-host.md) pages to define the Go side. Keep browser routing and server-state caching in the React application; keep authorization, persistence, and response contracts in the Go host.

## I am arriving from an earlier design

Do not map packages one-for-one. Begin by identifying responsibilities:

| Earlier responsibility | Current home |
|---|---|
| generic HTTP/config/logging mechanism | SDK foundation |
| cache, email, OAuth, event bus, tracing | SDK capability + optional integration |
| authentication, authorization, CMS | feature module |
| database driver | datastore integration |
| feature SQL and migrations | feature `stores/<dialect>` sibling |
| app-specific use case | host `internal/logic` |
| generated route/repository customization | explicit feature seam, host adapter, or future generation tooling |

Workshop currently emits a host or starting feature anatomy. Application, store, and client generation remain roadmap work; see the [Workshop overview](../workshop/overview.md) for the current command surface and planned additions.
