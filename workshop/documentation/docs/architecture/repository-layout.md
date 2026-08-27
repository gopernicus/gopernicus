---
title: Repository layout
description: How the Gopernicus multi-module repository maps architecture to directories.
---

# Repository layout

The repository is a Go workspace of independently versionable modules. `go.work` resolves them locally; it is development glue, not a runtime container.

```text
gopernicus/
├── sdk/                         stdlib-only layered kernel
│   ├── foundation/              mechanism and vocabulary
│   ├── capabilities/            ports plus shared policy
│   └── pocket/                 host↔pocket mount contract
├── integrations/                reusable technology connectors
│   ├── datastores/{pgxdb,turso}
│   ├── cryptids/*
│   ├── filestorage/*
│   ├── kvstores/goredis
│   └── ...
├── pockets/
│   ├── authentication/          SDK-only pocket core
│   │   ├── domain/              public entities and ports
│   │   ├── internal/            sealed services and inbound adapters
│   │   ├── storetest/           exported repository conformance
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
    "github.com/gopernicus/gopernicus/sdk/foundation/web"
    "github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
    "github.com/gopernicus/gopernicus/sdk/pocket"
)
```

The root `sdk` package owns only cross-cutting error and request/trace context vocabulary. Foundation packages are flat and independent. Capabilities can use foundation mechanics but not one another. The `pocket` package is the explicit composer.

## A pocket core and its siblings

A pocket is physically split so its core remains portable:

```text
pockets/cms/                    module: .../pockets/cms
  cms.go                         public socket: Repositories, Config, Register
  domain/content/                public entity + repository contracts
  domain/media/
  internal/logic/                private use cases
  internal/inbound/              private route handlers
  storetest/                     public test support

pockets/cms/stores/pgx/         module: .../pockets/cms/stores/pgx
pockets/cms/stores/turso/       module: .../pockets/cms/stores/turso
pockets/cms/views/goth/         module: .../pockets/cms/views/goth
```

The pocket core's `go.mod` requires exactly the SDK. A store module imports the public domain ports and a datastore connector. A view module imports the pocket's public render seam and a UI implementation. Nothing forces a host to choose any sibling.

### Reading rule

- `domain/` is what outsiders may implement or exchange;
- `internal/` is the pocket's sealed policy and delivery implementation;
- `stores/` is outbound, isolated into per-technology modules;
- `views/` is optional presentation, also isolated;
- the root package is the socket hosts compose.

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
