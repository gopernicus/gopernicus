---
sidebar_position: 1
title: Overview
---

# Code Generation

Gopernicus generates a data access layer from two source files: `queries.sql` (annotated SQL) and `bridge.yml` (HTTP configuration). You write the SQL, declare what you want with annotations, and the generator produces repository interfaces, pgx store implementations, HTTP bridge handlers, cache wrappers, test fixtures, and E2E tests.

The generator is intentionally not a full ORM. It reads your SQL as written, cross-references it against the reflected database schema for type information, and produces Go code that executes that SQL with proper types and error handling. If an annotation doesn't cover your use case, write the SQL and Go code directly — the generated layer is designed to coexist with hand-written code.

## Inputs

The generator consumes four things:

| Input | Location | Created by |
|---|---|---|
| `queries.sql` | `core/repositories/{domain}/{entity}/` | `gopernicus new repo` or by hand |
| `bridge.yml` | `bridge/repositories/{domain}bridge/{entity}bridge/` | `gopernicus new repo` or by hand |
| Reflected schema | `workshop/migrations/{db}/_public.json` | `gopernicus db reflect` |
| Project manifest | `gopernicus.yml` | `gopernicus init` |

`queries.sql` defines what queries exist and how they behave. `bridge.yml` defines how those queries are exposed over HTTP. The reflected schema provides column types, constraints, and relationships. The manifest tells the generator which databases, domains, and features are in play.

## Running the generator

```bash
# Generate everything
gopernicus generate

# Generate one domain only
gopernicus generate auth

# Preview without writing files
gopernicus generate --dry-run

# Verbose output
gopernicus generate --verbose

# Overwrite bootstrap files (use with care)
gopernicus generate --force-bootstrap
```

The generator requires a reflected schema. If you've changed your database, run `gopernicus db migrate` and `gopernicus db reflect` first.

## What it produces

Each entity gets files across three layers — repository (domain contract), pgx store (database implementation), and bridge (HTTP handlers). Files fall into two categories:

**Regenerated** files are overwritten every time you run `gopernicus generate`. They contain no custom logic — everything is derived from `queries.sql`, `bridge.yml`, and the schema. Don't edit these; your changes will be lost.

**Bootstrap** files are created once on the first generation run and never overwritten after that. They're yours to customize — add methods, change defaults, wire middleware. The generator won't touch them again unless you pass `--force-bootstrap`.

### Per-entity files

```
core/repositories/auth/users/
├── queries.sql              # Your annotated SQL (hand-written)
├── repository.go            # Bootstrap — Storer interface, Repository struct
├── generated.go             # Regenerated — entity types, input structs, errors
├── fop.go                   # Bootstrap — default order, direction, limit
├── cache.go                 # Bootstrap — custom cache methods
├── generated_cache.go       # Regenerated — CacheStore read-through wrapper
└── userspgx/
    ├── store.go             # Bootstrap — custom store methods
    ├── generated.go         # Regenerated — pgx Store implementation
    └── generated_test.go    # Regenerated — integration tests

bridge/repositories/authreposbridge/usersbridge/
├── bridge.go                # Bootstrap — Bridge struct, constructor
├── routes.go                # Bootstrap — route registration
├── http.go                  # Bootstrap — HTTP handler helpers
├── fop.go                   # Bootstrap — bridge FOP helpers
├── generated.go             # Regenerated — HTTP handlers
└── bridge.yml               # Your HTTP config (hand-written)
```

### Domain-level files

The generator also produces composites that wire all entities in a domain together:

| File | Layer | Purpose |
|---|---|---|
| `core/repositories/{domain}/generated_composite.go` | Core | Aggregates all repos into a `Repositories` struct |
| `bridge/repositories/{domain}bridge/generated_composite.go` | Bridge | Aggregates all bridges, exposes `AddHttpRoutes` |
| `workshop/testing/fixtures/generated.go` | Testing | Factory functions for test data |
| `workshop/testing/e2e/{entity}_generated_test.go` | Testing | HTTP-level CRUD tests |

## The pipeline

Generation runs in three phases:

### 1. Parse

The parser reads each `queries.sql` and extracts query blocks. Each `@func` annotation starts a new block. The parser collects the SQL text, named parameters (`@param_name`), and all annotations (`@filter`, `@order`, `@fields`, `@cache`, `@event`, etc.).

### 2. Resolve

The resolver cross-references parsed queries with the reflected database schema. It maps column names to Go types, resolves filter and order field specs, infers parameter types, and determines return types. Type resolution follows a priority cascade:

1. Explicit `@type:param` annotation (highest priority)
2. Column match in the primary table
3. Column match in any table (for JOINs)
4. SQL context (comparison with known columns)
5. Name heuristics (`_at` suffix → `time.Time`)
6. Fallback to `string`

After resolution, every parameter and return field has a concrete Go type.

### 3. Generate

Templates consume the resolved data and produce Go source files. Each file is formatted with `go/format` before being written to disk. The generator checks whether a file is bootstrap or regenerated — bootstrap files are skipped if they already exist.

## Function signature inference

You don't declare function signatures — the generator derives them from your SQL and annotations:

| Query shape | Generated signature |
|---|---|
| `SELECT` with `@filter` + `@order` + `LIMIT` | `List(ctx, filter, order, page) ([]Entity, Pagination, error)` |
| `SELECT` with single `@param` | `Get(ctx, id) (Entity, error)` |
| `INSERT` with `@fields` and `RETURNING` | `Create(ctx, input) (Entity, error)` |
| `UPDATE` with `@fields` and `RETURNING` | `Update(ctx, id, input) (Entity, error)` |
| `UPDATE` without `RETURNING` | `SoftDelete(ctx, id) error` |
| `DELETE` | `Delete(ctx, id) error` |

The exact shapes depend on your annotations, parameters, and SQL structure. See [Annotations](./annotations.md) for the full reference.

## Related docs

| Topic | What it covers |
|---|---|
| [Annotations](./annotations.md) | `queries.sql` annotation reference — `@func`, `@filter`, `@search`, `@fields`, `@order`, and more |
| [Schema Conventions](./schema-conventions.md) | Column naming patterns the generator recognizes — `parent_`, `tenant_id`, `record_state`, timestamps |
| [Bridge Configuration](./bridge-configuration.md) | `bridge.yml` reference — routes, middleware, auth schema |
| [Extending Generated Code](../extending-generated-code.md) | Adding custom methods alongside generated ones |
| [Core / Repositories](../../core/repositories.md) | The generated output from the repository layer's perspective |
| [Bridge / Repositories](../../bridge/repositories.md) | The generated output from the HTTP layer's perspective |
