---
title: Workshop CLI
description: Developer tooling for Gopernicus hosts, features, and migration ledgers.
---

# Workshop CLI

`workshop/gopernicus` is the stdlib-only developer CLI. It emits initial host and feature source trees, delegates database work to host-owned runners, and is not linked into generated applications at runtime.

<img class="docs-illustration" src="/gopernicus/img/clitelescope.jpg" alt="Gopernicus character using a telescope in a city of orbiting systems" />

From a repository checkout:

```bash
cd workshop/gopernicus
go run . --help
```

Once the module has a released tag, the intended installation shape is:

```bash
go install github.com/gopernicus/gopernicus/workshop/gopernicus@latest
```

## Current command surface

Workshop currently accepts identity inputs—module path, feature name, aggregate name, and datastore choice. It emits a starting tree that can be edited as ordinary Go source.

| Available today | Planned or under consideration |
|---|---|
| bare host composition root | schema/annotation-driven app generation |
| CRUD-shaped feature anatomy | per-field entity generation |
| memory + pgx + Turso store skeletons | store emission from a model spec |
| conformance-test skeleton | OpenAPI/TypeScript client generation |
| host migration ledger commands | drift-aware regeneration/markers |

The command surface and the generation roadmap are separate concerns: the current commands are useful without generation, and future generation can build on the same module and feature contracts.

## Why the CLI stays stdlib-only

- `init` can choose a datastore without linking its driver into the CLI;
- `db migrate` delegates to `go run ./workshop/migrations` in the host;
- feature source is embedded as templates, not imported feature packages;
- nothing outside `workshop` imports the CLI;
- repository guard G11 enforces both directions.

Workshop describes architecture; it is never an application service.

## Template verification

`make check` emits a temporary host and feature, rewrites pre-tag replaces for the checkout, builds them with workspace resolution disabled, runs the emitted memory store's conformance tests, and checks emitted guard shapes.

This compile proof is essential because repository grep guards cannot inspect `.tmpl` source as compiled Go. A stale template fails CI.

## Generation roadmap

The repository's planned generation work includes store-adapter generation, entity and field specifications, regeneration/update markers, and typed clients from an OpenAPI document. These are roadmap items; the current CLI reference documents only commands that exist in the tree.

See [Commands](commands.md) for exact flags and output.
