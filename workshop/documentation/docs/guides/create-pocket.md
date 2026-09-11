---
title: Create a pocket
description: Scaffold and evolve a reusable Gopernicus pocket module.
---

# Create a pocket

Build a pocket only when the capability needs to be reusable across hosts. For one application's policy, prefer an [app-local hexagon](../architecture/hexagonal-apps.md).

## Start with Workshop

```bash
gopernicus new pocket notes \
  --module github.com/acme \
  --aggregate note \
  --dir ./pockets/notes
```

The emitted tree is a born-conforming CRUD-shaped starting point:

```text
notes/
  notes.go
  logic/note/
  stores/memory/
  stores/storetest/
  stores/pgx/
  stores/turso/
```

It is not generated forever. Rename, split, and replace the placeholder aggregate with the real domain language.

## Design the public logic contracts first

The public logic package is a compatibility promise. Define:

- entity/value types and their valid construction;
- repository operations the pocket actually consumes;
- errors using SDK classes where transport-independent meaning matches;
- list ordering/search fields and pagination semantics;
- transaction and concurrency rules;
- empty-ID behavior when database-generated IDs are supported.

Document repository contracts in their Go doc comments. Those comments plus `storetest` form the specification store authors implement.

Avoid returning a database driver's types or accepting a generic “query anything” handle. A public port should remain meaningful across memory, pgx, and Turso.

## Expose complete use cases

The scaffold's `note.Service` lives in `logic/note`, beside its entity and
`Storer` port. It implements use cases directly with private fields. The root
only assembles it from the host's repositories. Hosts may use either constructor:

```go
// Root composition:
func NewService(repos Repositories, opts ...note.Option) (*note.Service, error)

// Public logic/note package:
func NewService(store Storer, opts ...Option) (*Service, error)
```

A larger pocket can return named `Components` instead of combining every method
onto a root service. Keep logic independent of transport and root composition.

Required dependencies stay explicit. The generated `WithIDs` option selects the
ID generator; omitting it uses the SDK default. Options configure private state
before construction and document defaults, nil values and ordering. Coherent
policy or connection records can remain configuration structs when useful.

## Add inbound delivery only when needed

Workshop's pocket scaffold has no routes or placeholder `Register` method. If the pocket owns an HTTP surface:

1. place the public adapter in `inbound/http`, with private handler helpers;
2. accept only the one-method `pockets.RouteRegistrar`;
3. use `web.Decode`, responders, render, and error mapping;
4. document the literal route table and conventional namespace;
5. expose use cases through public logic services and middleware through the adapter;
6. start no process-owned goroutine from `Register`.

If HTML is optional, define a technology-neutral `Views` interface in the core. Put the GOTH implementation in a sibling `views/goth` module. Nil views should remove the HTML surface structurally.

## Turn repository behavior into conformance

Build `storetest.Run` in `stores/storetest` around externally observable behavior, including difficult edges:

- create/get/list/delete and error classes;
- ordering, search, cursors, offset, counts, and page limits;
- transaction rollback and atomic multi-write operations;
- duplicate and missing-reference behavior;
- context cancellation;
- concurrent claim or revision races where relevant;
- database-generated ID behavior.

Run the same suite against memory, pgx, Turso, and any future store.

## Keep adapters in sibling modules

Each datastore module owns:

- exactly one pocket and one connector;
- pocket-specific SQL;
- canonical migrations with the same filename set as its dialect sibling;
- boot probes where missing schema would otherwise fail mid-request;
- migration export;
- the shared conformance suite plus driver-specific tests.

The core must compile and run with neither adapter in its graph.

## Register a monorepo pocket

Inside this repository, add the core and two store modules to:

- `go.work`;
- `MODULES` and `STORE_MODULES` in the root Makefile;
- `test-stores` live legs;
- the hardcoded pocket list in `guard-pocket-dependencies`.

Workshop prints this checklist when it detects a workspace target. Add view modules and examples if your pocket has them.

Finish by running `make check` and a recorded live conformance run for every supported dialect.
