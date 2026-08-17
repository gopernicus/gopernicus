---
title: Create a feature
description: Scaffold and evolve a reusable Gopernicus feature module.
---

# Create a feature

Build a feature only when the capability needs to be reusable across hosts. For one application's policy, prefer an [app-local hexagon](../architecture/hexagonal-apps.md).

## Start with Workshop

```bash
gopernicus new feature notes \
  --module github.com/acme \
  --aggregate note \
  --dir ./features/notes
```

The emitted tree is a born-conforming CRUD-shaped starting point:

```text
notes/
  notes.go
  domain/note/
  internal/logic/notesvc/
  memstore/
  storetest/
  stores/pgx/
  stores/turso/
```

It is not generated forever. Rename, split, and replace the placeholder aggregate with the real domain language.

## Design the public rim first

The domain package is a compatibility promise. Define:

- entity/value types and their valid construction;
- repository operations the feature actually consumes;
- errors using SDK classes where transport-independent meaning matches;
- list ordering/search fields and pagination semantics;
- transaction and concurrency rules;
- empty-ID behavior when database-generated IDs are supported.

Document repository contracts in their Go doc comments. Those comments plus `storetest` form the specification store authors implement.

Avoid returning a database driver's types or accepting a generic “query anything” handle. A public port should remain meaningful across memory, pgx, and Turso.

## Seal services

Put policy in `internal/logic/<domain>svc`. The root feature package constructs those services and selectively exposes the driving surface through a public `Service`.

The root socket should make dependencies visible:

```go
type Repositories struct {
    Notes note.Repository
}

type Config struct {
    IDs cryptids.IDGenerator
}

type Service struct {
    // unexported domain service
}

func NewService(repos Repositories, cfg Config) (*Service, error)
func (s *Service) Register(m feature.Mount) error
```

Every config field must state required, safe-default, or deny-by-absence behavior.

## Add inbound delivery only when needed

Workshop's feature scaffold registers no routes. If the feature owns an HTTP surface:

1. place handlers in `internal/inbound/<feature>`;
2. accept only the one-method `feature.RouteRegistrar`;
3. use `web.Decode`, responders, render, and error mapping;
4. document the literal route table and conventional namespace;
5. return use cases on `Service` so HTTP is not the only entry point;
6. start no process-owned goroutine from `Register`.

If HTML is optional, define a technology-neutral `Views` interface in the core. Put the GOTH implementation in a sibling `views/goth` module. Nil views should remove the HTML surface structurally.

## Turn repository behavior into conformance

Build `storetest.Run` around externally observable behavior, including difficult edges:

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

- exactly one feature and one connector;
- feature-specific SQL;
- canonical migrations with the same filename set as its dialect sibling;
- boot probes where missing schema would otherwise fail mid-request;
- migration export;
- the shared conformance suite plus driver-specific tests.

The core must compile and run with neither adapter in its graph.

## Register a monorepo feature

Inside this repository, add the core and two store modules to:

- `go.work`;
- `MODULES` and `STORE_MODULES` in the root Makefile;
- `test-stores` live legs;
- the hardcoded feature list in `guard-feature-core-sdk-only`.

Workshop prints this checklist when it detects a workspace target. Add view modules and examples if your feature has them.

Finish by running `make check` and a recorded live conformance run for every supported dialect.
