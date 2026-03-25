# Code Generation Overview

`gopernicus generate` reads annotated `queries.sql` files and `bridge.yml`
configuration, cross-references them with the reflected database schema, and
produces Go code for every layer of the hexagonal architecture: repository,
store, bridge, cache, composite, fixtures, and E2E tests.

## Running the Generator

```bash
gopernicus generate              # generate all domains
gopernicus generate --domain auth  # generate only the auth domain
gopernicus generate --dry-run    # preview without writing files
gopernicus generate --verbose    # show detailed output
gopernicus generate --force-bootstrap  # re-create bootstrap files
```

## Pipeline

The generation pipeline follows these steps in order:

1. **Load reflected schemas** -- Reads `_<schema>.json` files from
   `workshop/migrations/<dbname>/`. These are produced by `gopernicus db reflect`
   and contain table definitions, column types, primary keys, foreign keys, and
   indexes.

2. **Discover queries.sql files** -- Walks `core/repositories/` (or a single
   domain subdirectory when `--domain` is set), collecting every `queries.sql`.

3. **Discover bridge.yml files** -- Walks `bridge/repositories/` collecting
   every `bridge.yml` that declares routes, middleware, and auth schema.

4. **Resolve module path** -- Reads `go.mod` to determine the project module
   path for import generation.

5. **Per-entity generation** -- For each `queries.sql`:
   - Parse data annotations and SQL (`queryfile.Parse`).
   - Infer the database table from the directory name against the reflected schema.
   - Resolve types: map SQL params to Go types, expand `$filters`/`$order`/`$lim`
     inline specs into typed structs, resolve `@fields` and `@search` annotations.
   - Generate the **repository layer** (entity struct, input structs, filter
     structs, Repository methods with pagination) -- types use final names
     directly (e.g., `Entity`, `Storer`, `Repository`, `OrderByFields`).
   - Generate the **pgxstore layer** (SQL builder, named-args mapping, error
     mapping, `pgx.RowToStructByName` scanning).
   - Generate **integration tests** for the pgxstore (build-tagged `integration`).
   - Conditionally generate the **cache layer** (when any `@cache` annotation
     exists).
   - Conditionally generate the **bridge layer** (when a matching `bridge.yml`
     exists with routes): HTTP handlers on `*Bridge`, query-param parsing, filter
     conversion, request models, `addGeneratedRoutes()`, and OpenAPI spec.

6. **Domain composite generation** -- For each domain directory that has at least
   one entity, generate the composite package that wires all repositories
   together with a single constructor (`NewRepositories`) and a `Repositories`
   struct directly.

7. **Authorization schema generation** -- When the manifest enables a built-in
   authorization provider (`features.authorization: gopernicus`), generate
   `generated_authschema.go` in each bridge composite directory from
   `auth_relations` and `auth_permissions` defined in `bridge.yml` files.

8. **Bridge composite generation** -- For each domain that has entities with
   routes defined in `bridge.yml`, generate a bridge composite that wires all
   entity bridges together and aggregates `AddHttpRoutes` and `OpenAPISpec`.

9. **Fixture generation** -- A single cross-domain `core/testing/fixtures/`
   package is generated with `CreateTest<Entity>` functions that insert test data
   via raw SQL, respecting foreign key ordering.

10. **E2E test generation** -- For entities with HTTP routes, per-entity test
    files are generated under `testing/e2e/` (build-tagged `e2e`), along with a
    bootstrap `setup_test.go`.

## What Gets Generated Per Entity

Given a `queries.sql` for a table named `invitations` in the `rebac` domain:

| Layer | Generated artifacts |
|-------|-------------------|
| Repository | `generated.go` -- entity struct (`Invitation`), input structs, filter structs, Repository methods on `*Repository`, cursor helpers, `OrderByFields`, max limits, errors |
| Repository (bootstrap) | `repository.go` -- Storer (with markers), Repository struct, NewRepository; `fop.go` -- ordering defaults |
| Store | `<entity>pgx/generated.go` -- Store struct, all query methods with SQL building |
| Store (bootstrap) | `<entity>pgx/store.go` -- custom method stub |
| Integration tests | `<entity>pgx/generated_test.go` -- CRUD tests using fixtures |
| Integration tests (bootstrap) | `<entity>pgx/store_test.go` -- migrateTestDB helper |
| Cache | `generated_cache.go` -- CacheStore, CacheConfig, invalidation stubs |
| Cache (bootstrap) | `cache.go` -- custom cache methods |
| Bridge | `<entity>bridge/generated.go` -- HTTP handlers on `*Bridge`, query-param structs, filter parsing, `addGeneratedRoutes()`, OpenAPI spec |
| Bridge (bootstrap) | `<entity>bridge/bridge.yml`, `bridge.go`, `routes.go`, `http.go`, `fop.go` -- customizable configuration and handlers |

## The Domain Composite Pattern

Each domain (e.g., `rebac`) gets a composite package at the domain root:

- `generated_composite.go` -- `Repositories` struct with all entity
  repositories, `NewRepositories` constructor that wires store -> cache
  -> repository with event bus. Always regenerated. The struct uses its final
  name directly -- no `GeneratedRepositories` type.

The bridge composite follows the same pattern under
`bridge/repositories/<domain>reposbridge/`:

- `generated_composite.go` -- `Bridges` struct, constructor,
  `AddHttpRoutes`, and `OpenAPISpec`.
- `generated_authschema.go` -- auth schema from `bridge.yml` files.
- `authschema.go` -- bootstrap customization point.

## Template Files

The generator uses Go `text/template` templates defined in `*_tmpl.go` files:

| Template file | Purpose |
|--------------|---------|
| `repository_tmpl.go` | Entity, input, filter structs; Repository methods |
| `pgxstore_tmpl.go` | pgx Store with SQL builders |
| `integration_test_tmpl.go` | Store integration tests |
| `cache_tmpl.go` | CacheStore decorator |
| `bridge_tmpl.go` | HTTP handlers, query parsing, `addGeneratedRoutes()`, OpenAPI |
| `composite_tmpl.go` | Domain repository composite |
| `bridge_composite_tmpl.go` | Domain bridge composite |
| `authschema_tmpl.go` | Authorization schema from `bridge.yml` annotations |
| `fixture_tmpl.go` | Test fixture SQL inserts |
| `e2e_test_tmpl.go` | E2E HTTP tests |
| `app_tmpl.go` | Application wiring |

## Always-Regenerated vs. Bootstrap Files

Files prefixed with `generated` (or `generated_`) are overwritten on every run.
All other files are **bootstrap** files: created once and never touched again.
This lets you safely customize repository methods, bridge handlers, route
configuration, and middleware without losing changes.

Use `--force-bootstrap` to re-create bootstrap files (overwrites any
customizations).

---

**Related:**
- [Query Annotations Reference](query-annotations.md)
- [YAML Configuration Reference](yaml-configuration.md)
- [Generated File Map](generated-file-map.md)
