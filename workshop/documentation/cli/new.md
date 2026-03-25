# gopernicus new

Scaffold new repositories and use cases from templates or reflected database schemas.

## Overview

`gopernicus new` has two subcommands:

- `gopernicus new repo` -- scaffold a repository from reflected schema.
- `gopernicus new case` -- scaffold a use case with core logic and HTTP bridge.

---

## gopernicus new repo

Scaffold a new repository directory with a `queries.sql` file containing
default CRUD operations derived from the reflected database schema, and a
`bridge.yml` file with route definitions, middleware, and auth schema.

### Usage

```
gopernicus new repo <domain/entity> [--db <name>] [--table <name>]
```

### Arguments

| Argument | Description |
|---|---|
| `<domain/entity>` | Required. The domain and entity name separated by a slash (e.g. `auth/users`, `catalog/products`). The domain determines the directory under `core/repositories/`. |

### Flags

| Flag | Description |
|---|---|
| `--db <name>` | Database name from gopernicus.yml. Defaults to `primary`. |
| `--table <name>` | Override the table name used to look up the reflected schema. By default the CLI pluralizes the entity name. |

### How It Works

1. Parses the `domain/entity` argument. The domain part is required.
2. Determines the table name: uses `--table` if provided, otherwise pluralizes the entity name.
3. Looks up the table in the reflected schema JSON files (`workshop/migrations/{db}/_public.json`). Tries the table name first, then the raw entity name as a fallback.
4. **If the table is found**: creates `core/repositories/<domain>/<table>/queries.sql` with full CRUD scaffolding (List, Get, Create, Update, Delete, plus SoftDelete/Archive/Restore if the table has a `record_state` column, and GetBySlug/GetIDBySlug if the table has a unique `slug` column). Also creates a `bridge.yml` in the bridge package directory with route definitions, ordered middleware arrays, and auth schema.
5. **If the table is a known framework table**: writes all embedded bootstrap files (queries.sql, repository.go, store.go, bridge.yml, bridge.go, routes.go) from the framework's built-in repo templates. Existing files are skipped.
6. **If the table is NOT found**: creates a custom repo with a stub `queries.sql` containing placeholder comments for you to fill in manually.

### Scaffolded queries.sql

The generated `queries.sql` includes data annotation comments that drive code
generation. Only data annotations appear here (`@func`, `@filter`, `@search`,
`@order`, `@max`, `@fields`, `@cache`, `@event`). The scaffolded operations include:

- **List** -- paginated, filterable, searchable (string columns).
- **Get** -- by primary key.
- **GetBySlug / GetIDBySlug** -- only if the table has a globally unique `slug` column.
- **Create** -- excludes auto-managed columns (`created_at`, `updated_at`, auto-increment PK).
- **Update** -- excludes PK, `created_at`, `record_state`, and tenant ID.
- **SoftDelete / Archive / Restore** -- only if the table has a `record_state` column.
- **Delete** -- hard delete by primary key.

### Scaffolded bridge.yml

The generated `bridge.yml` includes route definitions with ordered middleware
arrays and auth schema configuration:

- **Routes** -- each CRUD operation gets a route entry with method, path, and
  middleware array (authenticate, rate_limit, authorize).
- **auth_relations** -- relation definitions for the resource type.
- **auth_permissions** -- permission definitions mapping actions to relations.
- **auth_create** -- on Create routes, relationship tuples for automatic
  ownership and parent association.

### Parent Detection

The scaffolder detects parent relationships using two conventions:

- **Tenant scoping**: a `tenant_id` foreign key referencing the `tenants` table. Routes are nested under `/tenants/{tenant_id}/` in `bridge.yml`.
- **Generic parent**: a foreign key column prefixed with `parent_` (e.g. `parent_org_id`). Not all foreign keys are treated as parents -- only those with the explicit `parent_` prefix.

### UniqueToID Middleware

If the table has a unique `slug` column, the scaffold generates `unique_to_id`
middleware in `bridge.yml` for routes that accept the entity ID parameter. This
enables clients to use either the ID or the slug in URLs.

### Examples

```bash
# Scaffold a repo for the users table in the auth domain
gopernicus new repo auth/users

# Override the table name
gopernicus new repo auth/users --table user_accounts

# Use a non-default database
gopernicus new repo analytics/metrics --db analytics

# After scaffolding, generate Go code
gopernicus generate auth
```

---

## gopernicus new case

Scaffold a new use case with core business logic and an HTTP bridge layer.

### Usage

```
gopernicus new case <name>
```

### Arguments

| Argument | Description |
|---|---|
| `<name>` | Required. The case name (e.g. `tenantadmin`, `audiorecorder`). Converted to lowercase with hyphens removed. |

### What Gets Created

Two directory trees are created:

**Core case** (`core/cases/<name>/`):

| File | Purpose |
|---|---|
| `case.go` | Case struct with constructor, logger, event bus, and placeholder dependency interfaces. Follows the "accept interfaces, return structs" pattern. |
| `errors.go` | Stub for domain errors. Wrap with `sdk/errs` sentinels for proper HTTP status mapping. |
| `events.go` | Stub for domain events emitted by this case. |

**Bridge case** (`bridge/cases/<name>bridge/`):

| File | Purpose |
|---|---|
| `bridge.go` | Bridge struct wrapping the core Case, with logger, rate limiter, and JSON error renderer. |
| `http.go` | `AddHttpRoutes` method that registers routes under `/cases/<kebab-name>/`. |
| `model.go` | Stub for HTTP request/response types with validation. |

### Route Convention

Case routes register under `/cases/<kebab-name>/` to avoid conflicts with
generated CRUD routes. The expected mount point is `api.Group("/cases")`,
producing paths like `/api/v1/cases/<kebab-name>/...`.

### Examples

```bash
# Scaffold a tenant admin case
gopernicus new case tenantadmin

# Scaffold an audio recorder case
gopernicus new case audiorecorder
```

### After Scaffolding

The CLI prints next steps after creation:

1. Add dependency interfaces to `core/cases/<name>/case.go`.
2. Implement operations in `case.go`.
3. Add routes in `bridge/cases/<name>bridge/http.go`.
4. Wire the bridge into your server configuration:
   ```go
   cases := api.Group("/cases")
   tenantadminBridge.AddHttpRoutes(cases)
   ```

## Related

- [generate](generate.md) -- generate Go code after scaffolding a repo
- [boot](boot.md) -- batch-scaffold all repos for a domain
- [db](db.md) -- reflect schema before scaffolding repos
- [init](init.md) -- initial project setup
- [Adding a New Entity](../guides/adding-new-entity.md)
- [Adding a Use Case](../guides/adding-use-case.md)
