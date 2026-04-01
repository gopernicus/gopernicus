# gopernicus generate

Run code generators from `queries.sql` files and `bridge.yml` configuration.

## Overview

`gopernicus generate` scans `core/repositories/` for `queries.sql` files and
`bridge/repositories/` for `bridge.yml` files, then generates Go code by
cross-referencing each query with the reflected database schema (produced by
`gopernicus db reflect`).

The generator produces two categories of files:

- **Generated files** (`generated.go`, `generated_*.go`, `*_generated_test.go`)
  -- always overwritten on every run. These contain the types and methods derived
  from `queries.sql` annotations and `bridge.yml` configuration. Never edit these
  files directly.
- **Bootstrap files** (`repository.go`, `fop.go`, `store.go`, `bridge.go`,
  `routes.go`, `http.go`, etc.) -- created once and never overwritten. These
  files belong to you and are safe to customize.

## Usage

```
gopernicus generate [domain] [--dry-run] [--verbose] [--force-bootstrap]
```

### Arguments

| Argument | Description |
|---|---|
| `[domain]` | Optional. Restrict generation to a single domain (e.g. `auth`). When omitted, all domains under `core/repositories/` are processed. |

### Flags

| Flag | Description |
|---|---|
| `--dry-run` | Preview what would change without writing any files. |
| `--verbose`, `-v` | Print detailed output during generation. |
| `--force-bootstrap`, `-f` | Overwrite bootstrap files even if they already exist. Use with caution -- this discards your customizations. |

## Prerequisites

Before running `generate`, you need:

1. A valid `gopernicus.yml` manifest at the project root.
2. A reflected schema JSON file. Run `gopernicus db reflect` to produce
   `workshop/migrations/{db}/_public.json` (or the appropriate schema name).
3. At least one `queries.sql` file under `core/repositories/<domain>/<entity>/`.

The generator reads the manifest to locate databases and domain mappings, then
loads the reflected schema to resolve column types, primary keys, foreign keys,
and enum types for each table referenced in your queries.

## What Gets Produced

Generation produces files across several layers. Each entity (queries.sql file)
triggers generation in the repository, store, bridge, and test layers.

### Per Entity

**Repository** (`core/repositories/<domain>/<entity>/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated.go` | Always | Yes | Entity types, input types, filter types, Repository methods, errors, `OrderByFields`. |
| `repository.go` | Once | No | Storer interface (with `gopernicus:start`/`end` markers), Repository struct, NewRepository. |
| `fop.go` | Once | No | FOP (filter, order, pagination) customization hooks. |

**PGX Store** (`core/repositories/<domain>/<entity>/<entity>pgx/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated.go` | Always | Yes | SQL query implementations using pgx. |
| `store.go` | Once | No | Store struct, NewStore constructor, custom method stubs. |

**Integration Tests** (`core/repositories/<domain>/<entity>/<entity>pgx/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated_test.go` | Always | Yes | Auto-generated test cases for all store methods. |
| `store_test.go` | Once | No | Test setup, fixtures, and custom test stubs. |

**Cache** (`core/repositories/<domain>/<entity>/`) -- only when `@cache` annotations are present:

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated_cache.go` | Always | Yes | Cache-aware wrapper methods for annotated queries. |
| `cache.go` | Once | No | Cache configuration and TTL customization. |

**Bridge** (`bridge/repositories/<domain>reposbridge/<entity>bridge/`) -- when `bridge.yml` exists:

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated.go` | Always | Yes | HTTP handlers on `*Bridge`, request types, `addGeneratedRoutes()`, OpenAPI spec. |
| `bridge.go` | Once | No | Flat Bridge struct with all fields, NewBridge constructor. |
| `routes.go` | Once | No | `AddHttpRoutes()` calling `addGeneratedRoutes()` plus custom routes. |
| `http.go` | Once | No | HTTP handler customization and overrides. |
| `fop.go` | Once | No | FOP middleware configuration for bridge routes. |

### Per Domain

**Domain Composite** (`core/repositories/<domain>/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated_composite.go` | Always | Yes | Aggregates all entity repositories in the domain into a single struct. |

**Bridge Composite** (`bridge/repositories/<domain>reposbridge/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated_composite.go` | Always | Yes | Aggregates all entity bridges in the domain into a single struct. |

**Auth Schema** (`bridge/repositories/<domain>reposbridge/`) -- when an authorization provider is configured:

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated_authschema.go` | Always | Yes | Generated auth relation and permission definitions from `bridge.yml`. |
| `authschema.go` | Once | No | Customization hooks for auth schema per domain. |

### Cross-Domain

**Test Fixtures** (`workshop/testing/fixtures/`):

| File | Created | Overwritten | Description |
|---|---|---|---|
| `generated.go` | Always | Yes | Factory functions for creating test entities with resolved FK relationships. |
| `fixtures.go` | Once | No | Custom fixture helpers and overrides. |

**E2E Tests** (`workshop/testing/e2e/`) -- for entities with HTTP routes:

| File | Created | Overwritten | Description |
|---|---|---|---|
| `setup_test.go` | Once | No | E2E test bootstrap (server setup, database connection). One per project. |
| `<entity>_generated_test.go` | Always | Yes | Auto-generated HTTP roundtrip tests per entity. |
| `<entity>_test.go` | Once | No | Custom E2E test cases per entity. |

With `--force-bootstrap`, all "Once / No" files are regenerated from scratch.

## When to Run It

Run `gopernicus generate` after any of these changes:

- Creating a new `queries.sql` file (via `gopernicus new repo` or manually).
- Editing an existing `queries.sql` to add, remove, or modify query annotations.
- Editing a `bridge.yml` to change routes, middleware, or auth schema.
- Running `gopernicus db reflect` after a schema migration (new columns, type changes, etc.).
- Running `gopernicus boot repos` to scaffold new repository directories.

You do not need to run it after editing bootstrap files (`repository.go`,
`bridge.go`, `routes.go`, `store.go`, etc.) -- those are your files and are not
inputs to the generator.

## Examples

```bash
# Regenerate all repositories across all domains
gopernicus generate

# Regenerate only the auth domain
gopernicus generate auth

# Preview changes without writing files
gopernicus generate --dry-run

# Verbose output for debugging generation issues
gopernicus generate --verbose

# Force-regenerate bootstrap files (destructive)
gopernicus generate --force-bootstrap

# Combine flags
gopernicus generate auth --dry-run --verbose
```

## Typical Workflow

```bash
# 1. Write or update your database migration
vim workshop/migrations/primary/0005_add_widgets.sql

# 2. Apply the migration
gopernicus db migrate

# 3. Reflect the updated schema
gopernicus db reflect

# 4. Scaffold a new repo (creates queries.sql and bridge.yml)
gopernicus new repo catalog/widgets

# 5. Generate Go code
gopernicus generate

# 6. Customize the bootstrap files as needed
vim core/repositories/catalog/widgets/repository.go
vim bridge/repositories/catalogreposbridge/widgetsbridge/bridge.yml
```

## Notes

- The generator requires the project root to contain a `gopernicus.yml`
  manifest. It uses `project.MustFindRoot()` to locate it by walking up from
  the current directory.
- If the reflected schema is missing or stale, generated types may be incorrect.
  Always re-reflect after schema changes.
- Generated files contain a header comment indicating they are auto-generated.
  Do not edit them -- changes will be lost on the next run.
- `queries.sql` contains only data annotations (`@func`, `@filter`, `@order`,
  `@max`, `@fields`, `@cache`, `@event`, etc.). Protocol and auth annotations
  have moved to `bridge.yml`.

## Known Limitations

- The CLI help text (`gopernicus generate --help`) references `model.go` as a bootstrap file, but this file does not exist. The actual repository bootstrap files are `repository.go` and `fop.go`. This is a stale reference in the help text.

## Related

- [db](db.md) -- `gopernicus db reflect` produces the schema JSON consumed by generate
- [new](new.md) -- `gopernicus new repo` scaffolds `queries.sql` and `bridge.yml` files for generate to process
- [boot](boot.md) -- `gopernicus boot repos` batch-scaffolds repos before generation
- [init](init.md) -- project setup that precedes all generation
