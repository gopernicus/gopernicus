---
title: Pocket contract
description: Anatomy, rules, extension tiers, mounting, persistence, and testing for reusable pocket modules.
---

# Pocket contract

A Gopernicus pocket is a **library-shaped hexagon**: a reusable domain capability with public entities and ports, sealed services and delivery, optional sibling adapters, and a narrow socket the host composes.

Authentication, authorization, CMS, events, and jobs are the reference pockets.

## Anatomy

```text
pockets/<name>/
  <name>.go                    public socket
  domain/<aggregate>/          public entities + outbound ports
  internal/logic/<domain>svc/  sealed policy and use cases
  internal/inbound/<name>/     sealed routes and handlers, when present
  storetest/                   exported repository conformance
  memstore/                    optional public stdlib reference store
  stores/pgx/                  separate module
  stores/turso/                separate module
  views/goth/                  separate module, only when HTML exists
```

The root socket normally exports:

- `Repositories`: all required outbound domain ports;
- `Config`: host-selected collaborators and policy with documented zero/nil semantics;
- `Service` and `NewService`: public driving use cases;
- `Register(pocket.Mount)`: optional shipped transport registration.

CMS currently retains an earlier `Register(mount, repos, config)` shape while its full public `Service` surface is completed. Do not assume every pocket has identical construction syntax; the contract is converging without hiding the difference.

## Core rules

1. **SDK-only core.** The core module's `go.mod` requires exactly `github.com/gopernicus/gopernicus/sdk`.
2. **Datastore-free.** The core never imports its stores, integrations, examples, or UI.
3. **No cross-pocket imports.** The consuming pocket declares a port; the host wires it.
4. **Ports public, services internal.** Store modules and hosts need domain contracts; implementation remains sealed.
5. **Optional presentation.** `Config.Views == nil` means the HTML surface is off; view libraries live in sibling modules.
6. **Host-owned migrations.** Store modules export canonical migration files; the host merges and applies them before boot.
7. **Transport uses SDK web primitives.** Pocket HTTP code uses the shared responders and error mapping rather than forking mechanism.
8. **Zero-infrastructure proof.** A host-supplied or public in-memory implementation must demonstrate the core without a driver.

## The mount contract

`sdk/pocket.Mount` is deliberately narrow:

```go
type Mount struct {
    Router RouteRegistrar
    Logger *slog.Logger
    Events events.Emitter
}
```

`RouteRegistrar` has one `Handle` method matching `web.WebHandler`. A pocket can register routes without importing the concrete router. `Events` is an optional best-effort emit rail, not a transaction or job queue.

The mount is not a service locator. Pocket-specific dependencies belong in that pocket's `Repositories` and `Config`.

## Four extension tiers

Prefer the shallowest tier that solves the host need.

### 1. Configure

Use zero-safe `Config` fields. Absence either chooses a documented safe default or disables the subsystem structurally.

Examples: no authentication providers means no OAuth routes; nil CMS cache disables page caching; nil jobs schedules creates a queue-only host.

### 2. Replace a component

Supply a port implementation or registered data through `Config`.

Examples: custom CMS `Views`, authentication HTML views, registered CMS content types/templates, jobs handlers.

### 3. Inject at a seam

Pass middleware or another narrow host-owned collaborator.

```go
cms.Config{
    AdminMiddleware: []web.Middleware{authService.RequireAccessToken()},
}
```

CMS does not import authentication. Authentication does not import CMS. The host performs the composition.

### 4. Extend past the pocket

Call the public `Service` from host-owned routes or workflows. The shipped transport is a convenience adapter, not the only door. If a needed capability is sealed inside `internal`, add a deliberate public seam rather than exporting the interior.

## Prefixing and route control

A host can wrap the registrar it passes to a pocket:

```go
mount := pocket.Mount{
    Router: pocket.Group{
        Prefix:     "/account",
        Middleware: []web.Middleware{hostAudit},
        Next:       router,
    },
    Logger: log,
}
```

`pocket.PrefixRegistrar` changes only paths registered with the host. `pocket.Group` also prepends middleware. Because wrappers implement the same one-method interface, a host can deny, replace, re-path, or wrap individual routes with its own registrar.

:::warning Prefixes do not rewrite rendered URLs

Prefixing registration does not change links, form actions, or redirects emitted by a pocket. The current CMS views contain host-rooted paths, so mounting the whole CMS below a prefix is not yet transparent.

:::

## Persistence contract

A durable pocket generally ships:

- Turso and pgx store modules with the same repository surface;
- identical migration filename/version sets across dialects;
- `Repositories(db)` constructors;
- `ExportMigrations(dst)` and embedded migration FS metadata;
- a core `storetest` suite every implementation runs;
- an in-memory reference or proof host.

A host may import a shipped store or implement the public repositories itself. Store modules are maintained reference implementations, not mandatory runtime layers.

## Authoring checklist

Before calling a pocket complete, verify:

- its `go.mod` requires SDK only;
- domain ports document ordering, pagination, transaction, ID, and error semantics;
- `Config` documents every nil and zero value;
- `Service` exposes host-driving use cases, not only HTTP registration;
- route surface and conventional namespace are documented;
- no `Register` call starts an unowned goroutine;
- each durable store passes the same conformance suite;
- migrations export cleanly into a host ledger;
- the pocket can run without either shipped dialect;
- `make check` guards the new core and modules.

Workshop can emit a starting anatomy; see [Create a pocket](../guides/create-pocket.md).
