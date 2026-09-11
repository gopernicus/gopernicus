---
title: Repository layout
description: How the Gopernicus multi-module repository maps architecture to directories.
---

# Repository layout

The repository is a Go workspace of independently versionable modules. `go.work` resolves them locally; it is development glue, not a runtime container.

```text
gopernicus/
├── sdk/                         stdlib-only layered kernel
│   ├── pkg/              mechanism and vocabulary
│   └── capabilities/            ports plus shared policy
├── integrations/                reusable technology connectors
│   ├── datastores/{pgxdb,turso}
│   ├── cryptids/*
│   ├── filestorage/*
│   ├── kvstores/goredis
│   └── ...
├── pockets/                     shared host-contract module (SDK only)
│   ├── authentication/          SDK/shared-contract pocket core
│   │   ├── logic/               public services, owned types and ports
│   │   ├── inbound/http/        public HTTP adapter and middleware
│   │   ├── stores/storetest/    exported repository conformance
│   │   ├── stores/{pgx,turso}/  independent store modules
│   │   └── views/goth/          independent presentation module
│   └── ...
├── ui/goth/                     optional Go presentation system
├── examples/                    complete composition roots
└── workshop/gopernicus/         stdlib-only scaffolding CLI
```

## The SDK tree

The package path communicates the dependency tier:

```go
import (
    "github.com/gopernicus/gopernicus/sdk"
    "github.com/gopernicus/gopernicus/sdk/pkg/web"
    "github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
    "github.com/gopernicus/gopernicus/pockets"
)
```

The root `sdk` package owns only cross-cutting error and request/trace context vocabulary. Packages under `pkg/` are flat and independent. Capabilities can use pkg mechanisms but not one another. The separate `pockets` module owns the shared host contract and depends on SDK. It imports no concrete pockets.

## A pocket core and its siblings

A pocket is physically split so its core remains portable:

```text
pockets/jobs/                    module: .../pockets/jobs
  jobs.go, config.go             root composition
  logic/queue/                  public queue service, types, ports and runtimes
  logic/schedules/              public schedule service, types and ports
  stores/memory/                optional adapter within core module
  stores/storetest/             shared conformance within core module

pockets/jobs/stores/pgx/         module: .../pockets/jobs/stores/pgx
pockets/jobs/stores/turso/       module: .../pockets/jobs/stores/turso
```

The core's `go.mod` permits only SDK and the shared pockets contract. A store
module imports public logic-owned ports and a datastore connector. HTTP pockets
expose `inbound/http`; optional view modules implement its public render seam.
CMS retains its earlier domain/internal layout pending its deferred audit.

### Reading rule

- `logic/` owns supported services, types and consumed ports;
- `inbound/http/` exposes HTTP adapters, reusable middleware and supported handlers;
- `internal/` holds genuinely private helpers or engines, only when needed;
- `stores/` groups outbound adapters; drivers have separate modules;
- `views/` is optional presentation in separate modules;
- the root constructs and connects named components.

## Integrations are library-shaped

An integration isolates one external dependency boundary, not necessarily one SDK port. `integrations/kvstores/goredis` wraps one go-redis client and implements event bus, cache, and rate-limit ports because one library genuinely serves all three. `integrations/tracing/otel` groups the coherent OpenTelemetry family.

Conversely, pocket-specific SQL does not belong in a generic datastore integration. `pgxdb` knows PostgreSQL mechanics; `pockets/cms/stores/pgx` knows CMS tables and queries.

## UI is not a pocket

`ui/goth` owns presentation primitives, semantic tokens, controllers, and assets. It owns no business schema and registers no routes. Pocket view modules translate a pocket's view models into GOTH renderers; hosts can use GOTH directly for their own pages.

```text
pocket core ← pocket views/goth → ui/goth
       ↑                ↑             ↑
       └──────────── host ────────────┘
```

## Examples are architecture proofs

Examples intentionally have different module graphs:

- `minimal` proves CMS can run without a database driver;
- `cms` proves the same core against Turso with custom views;
- `auth-cms` proves cross-pocket wiring without pocket-to-pocket imports;
- `jobs-minimal` proves the durable-job protocol against memory;
- `goth-showcase` proves UI independence.

If a claimed boundary cannot be demonstrated by a host with the unwanted dependency absent from `go.mod`, the boundary is not real yet.

## Workspace versus consumer modules

Inside this repository, `go.work` makes imports resolve to sibling directories. An external application should require the modules it uses and pin their released tags. Pre-tag development may require temporary `replace` directives; Workshop's emitted README explains that posture.
