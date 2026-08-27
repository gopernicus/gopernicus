---
title: Meet Gopernicus
description: The mental model for Gopernicus packages and host composition.
slug: /intro
---

# Meet Gopernicus

Gopernicus is a collection of opinionated Go packages for building applications with explicit boundaries. It provides SDK contracts and mechanisms, reusable pocket modules, technology integrations, and optional UI packages. A host application decides which of those pieces it uses and owns the composition root.

<img class="docs-illustration docs-illustration--small" src="/gopernicus/img/gopernicusicon.jpg" alt="Gopernicus character studying an orbit map" />

The host's `main` package chooses the router, middleware, pocket modules, repositories, providers, migrations, background runtimes, and shutdown order. Gopernicus supplies typed seams for those decisions; it does not require a global registry or a particular UI runtime.

:::info Main branch

The documentation follows the repository's `main` branch. Module tags and public APIs are still evolving, so pages distinguish current behavior from planned work.

:::

:::warning Work in progress

Gopernicus is open source under the [MIT License](https://github.com/gopernicus/gopernicus/blob/main/LICENSE), but it is not stable. APIs, package boundaries, and module versions may change while the project is developed.

:::

## The package collection

<div class="docs-cards">
  <a class="docs-card" href="./sdk/overview"><strong>SDK</strong><span>A stdlib-only kernel with foundation mechanics, capabilities, and the pocket-mount contract.</span></a>
  <a class="docs-card" href="./pockets/overview"><strong>Pockets</strong><span>Datastore-free modules for authentication, authorization, CMS, events, and jobs.</span></a>
  <a class="docs-card" href="./integrations/catalog"><strong>Integrations</strong><span>Separate modules for databases, Redis, OAuth, storage, email, tracing, IDs, and scheduling.</span></a>
  <a class="docs-card" href="./ui/react"><strong>UI options</strong><span>Use the API alone, connect a React/TanStack client, or add the optional GOTH presentation system.</span></a>
</div>

## Dependencies point inward

<div class="dependency-flow">
  <div><strong>Your host</strong><small>composition</small></div><span>→</span>
  <div><strong>Pockets & integrations</strong><small>policy + adapters</small></div><span>→</span>
  <div><strong>SDK</strong><small>stdlib only</small></div>
</div>

The dependency rule is architectural:

- `sdk` imports only the standard library and its own legal lower tiers;
- a pocket core requires only `sdk`; it does not import a datastore, another pocket, a UI implementation, or a host;
- integrations depend inward on SDK ports and never on pockets or examples;
- pocket store and view implementations are sibling modules, so unused drivers and UI libraries stay out of a host's graph;
- examples are hosts and may import the pieces they intentionally compose.

Repository guards exercise these rules as part of `make check`.

## Package boundaries

The packages can be composed at different boundaries:

| Application shape | Gopernicus provides | The host or client provides |
|---|---|---|
| API only | HTTP foundation, JSON responses, pocket routes, capabilities, stores, and OpenAPI description | application routes, client applications, deployment, and UI |
| API plus React | the same API and contracts | React components, TanStack Query/Router, browser state, and asset pipeline |
| API plus Go UI | HTTP foundation plus an optional UI package and pocket view adapter | page composition, theme choices, asset routes, and lifecycle |

These are composition choices, not separate editions. See [React and TanStack](ui/react.md) for the API-only client pattern and [GOTH UI](ui/goth.md) for the optional Go presentation package.

## Earlier design notes

The repository grew from an earlier layered design with Core, Bridge, Infrastructure, App, and schema-driven generation. The current package layout uses SDK tiers, pocket cores, sibling adapters, integrations, and host-owned composition. The [architecture overview](architecture/overview.md) documents the current rules; the [Workshop pages](workshop/overview.md) document what the CLI does today and the generation work that remains planned.

## Where to begin

- Run the [zero-infrastructure quickstart](getting-started/quickstart.md) to see a complete pocket host.
- Read the [architecture overview](architecture/overview.md) before designing an app-local domain or reusable pocket.
- Use [choose your path](getting-started/choose-your-path.md) for a task-oriented route through the docs.
- Read [React and TanStack](ui/react.md) if Gopernicus will provide an API to a separate web client.
- Browse the [worked examples](examples.md) when you need wiring that compiles.

The rest of this site distinguishes contract from example. Public types and nil semantics are contracts; example hosts demonstrate one composition, not the only composition.
