---
title: Pocket contract
description: Anatomy, rules, extension tiers, mounting, persistence, and testing for reusable pocket modules.
---

# Pocket contract

A Gopernicus pocket is a reusable domain capability with a deliberate public use-case API, public domain contracts, private implementation details, and optional adapters that the host composes.

Authentication, authorization, CMS, events, and jobs are the reference pockets.

## Anatomy

```text
pockets/<name>/
  <name>.go, constructor.go, ... compact construction/composition
  logic/<concern>/             public services, owned types and consumed ports
  inbound/http/                public middleware, handlers and route registration
  internal/<concern>/          private engines or helpers only when needed
  stores/storetest/            exported conformance; package in core module
  stores/memory/               optional usable memory adapter; same module
  stores/pgx/                  separate module
  stores/turso/                separate module
  views/goth/                  separate module, only when HTML exists
```

A focused service owns its use cases and private state. Root constructors assemble
services, HTTP adapters and runtimes from explicit repositories/configuration.
Multi-component pockets expose a named `Components` bundle; a single-service
pocket can return the service directly. Hosts can also construct public components
individually. Every public constructor validates its required dependencies.

HTTP adapters live in `inbound/http`, so hosts can mount bundled routes, reuse
middleware or supported handlers on their own routes, or call logic directly.
Logic never depends on HTTP, inbound or root composition. Domain types and ports
belong with their owning service; a shared model package exists only when needed.
Neither `domain` nor `internal` is a compulsory directory.

Authorization's ordinary services retain guarded mutations; trusted relationship
writers and system mutators remain separate capabilities. CMS retains its earlier
API and directory layout while its audit is deferred.

The `stores` directory itself contains no Go package or `go.mod`. Only the driver adapters are separate modules; memory and conformance remain in the core module. Importing a pocket does not automatically import those packages.

## Core rules

1. **SDK and shared contract only.** A concrete core may require `github.com/gopernicus/gopernicus/sdk` and the exact `github.com/gopernicus/gopernicus/pockets` module. Shared pockets requires SDK only and never imports concrete pockets.
2. **Datastore-free services.** Production service code never imports concrete stores, integrations, examples, or UI. Memory/conformance packages and core tests may use their own store support without driver dependencies.
3. **No cross-pocket imports.** The consuming pocket declares a port; the host wires it.
4. **Deliberate public API.** Hosts and stores can import the contracts they need. Public services keep private state and implementation helpers. Public logic has no HTTP, inbound or root-composition dependencies.
5. **Optional presentation.** absent views (for authentication, `BrowserConfig.Views == nil`) keep the HTML surface off; view libraries live in sibling modules.
6. **Host-owned migrations.** Store modules export canonical migration files; the host merges and applies them before boot.
7. **Transport uses SDK web primitives.** Pocket HTTP code uses the shared responders and error mapping rather than forking mechanism.
8. **Zero-infrastructure proof.** A host-supplied or public in-memory implementation must demonstrate the core without a driver.

## The mount contract

`pockets.Mount` is deliberately narrow:

```go
type Mount struct {
    Router RouteRegistrar
    Logger *slog.Logger
    Events events.Emitter
}
```

`RouteRegistrar` has one `Handle` method matching `web.WebHandler`. A pocket can register routes without importing the concrete router. `Events` is an optional best-effort emit rail, not a transaction or job queue.

The mount is not a service locator. Pocket-specific dependencies belong in explicit constructor arguments or a
meaningfully named repositories/services record. Optional policy belongs in typed
constructor options.

## Four extension tiers

Prefer the shallowest tier that solves the host need.

### 1. Configure

Use typed `WithFoo(...)` options for optional settings, with coherent policy
records supplied as named option values. For example, authentication accepts
`WithPassword(PasswordConfig{...})` and `WithBrowser(BrowserConfig{...})`. Keep
required dependencies explicit. Each option replaces its whole group, including
zero values; it does not merge partial settings. Options apply to private construction state; they cannot reconfigure a
running service. Absence chooses a documented default or disables the subsystem
structurally. A nil option is invalid programming input, distinct from a valid
option with a documented nil argument.

Examples: no authentication providers means no OAuth routes; nil CMS cache disables page caching; nil jobs schedules creates a queue-only host.

### 2. Replace a component

Supply a port implementation or registered data through the constructor's named
option or required argument. Deferred CMS retains its `Config` surface.

Examples: custom CMS `Views`, authentication HTML views, registered CMS content types/templates, jobs handlers.

### 3. Inject at a seam

Pass middleware or another narrow host-owned collaborator.

```go
cms.Config{
    AdminMiddleware: []web.Middleware{authenticationComponents.HTTP.RequireAccessToken()},
}
```

CMS does not import authentication. Authentication does not import CMS. The host performs the composition.

### 4. Extend past the pocket

Call public logic services from host-owned routes or workflows, or construct and reuse the public HTTP adapters. Keep private helpers private; support public components deliberately rather than mechanically exporting every internal function.

## Prefixing and route control

A host can wrap the registrar it passes to a pocket:

```go
mount := pockets.Mount{
    Router: pockets.Group{
        Prefix:     "/account",
        Middleware: []web.Middleware{hostAudit},
        Next:       router,
    },
    Logger: log,
}
```

`pockets.PrefixRegistrar` changes only paths registered with the host. `pockets.Group` also prepends middleware. Because wrappers implement the same one-method interface, a host can deny, replace, re-path, or wrap individual routes with its own registrar.

:::warning Prefixes do not rewrite rendered URLs

Prefixing registration does not change links, form actions, or redirects emitted by a pocket. The current CMS views contain host-rooted paths, so mounting the whole CMS below a prefix is not yet transparent.

:::

## Persistence contract

A durable pocket generally ships:

- Turso and pgx store modules with the same repository surface;
- identical migration filename/version sets across dialects;
- `Repositories(db)` constructors;
- `ExportMigrations(dst)` and embedded migration FS metadata;
- a core `stores/storetest` suite every implementation runs;
- an in-memory reference or proof host.

A host may import a shipped store or implement the public repositories itself. Store modules are maintained reference implementations, not mandatory runtime layers.

## Authoring checklist

Before calling a pocket complete, verify:

- its `go.mod` permits only SDK and the shared pockets contract;
- domain ports document ordering, pagination, transaction, ID, and error semantics;
- constructor inputs, options and policy records document every nil/zero value;
- `Service` exposes host-driving use cases, not only HTTP registration;
- route surface and conventional namespace are documented;
- no `Register` call starts an unowned goroutine;
- each durable store passes the same conformance suite;
- migrations export cleanly into a host ledger;
- the pocket can run without either shipped dialect;
- `make check` guards the new core and modules.

Workshop can emit a starting anatomy; see [Create a pocket](../guides/create-pocket.md).
