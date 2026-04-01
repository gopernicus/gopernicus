# gopernicus new

Scaffold new repositories, use cases, and infrastructure adapters from templates or reflected database schemas.

## Overview

`gopernicus new` has three subcommands:

- `gopernicus new repo` -- scaffold a repository from reflected schema.
- `gopernicus new case` -- scaffold a use case with core logic and HTTP bridge.
- `gopernicus new adapter` -- scaffold a custom infrastructure adapter implementing a framework interface.

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
- **GetBySlug / GetIDBySlug** -- only if the table has a globally unique `slug` column. Composite unique slugs (e.g. `UNIQUE(tenant_id, slug)`) are not detected and require a custom query.
- **Create** -- excludes auto-managed columns (`created_at`, `updated_at`, auto-increment PK).
- **Update** -- excludes PK, `created_at`, `record_state`, tenant ID, and parent FK.
- **SoftDelete / Archive / Restore** -- only if the table has a `record_state` column.
- **Delete** -- hard delete by primary key.

### Scaffolded bridge.yml

The generated `bridge.yml` includes route definitions with ordered middleware
arrays and auth schema configuration:

- **Routes** -- each CRUD operation gets a route entry with method, path, and
  middleware array (authenticate, rate_limit, authorize). List routes use prefilter authorization. Mutation routes include a `max_body_size` middleware.
- **auth_relations** -- relation definitions for the resource type. Includes `owner` plus the nearest parent relation.
- **auth_permissions** -- permission definitions mapping actions (list, create, read, update, delete, manage) to relations. Uses parent traversal (e.g. `tenant->list`) when a parent exists.
- **auth_create** -- on Create routes, relationship tuples for automatic ownership and parent association.
- **params_to_input** -- on Create routes, maps URL path parameters (tenant_id, parent FK) into the create input struct.

### Parent Detection

The scaffolder detects parent relationships using two conventions:

- **Tenant scoping**: a `tenant_id` foreign key referencing the `tenants` table. Routes are nested under `/tenants/{tenant_id}/` in `bridge.yml`.
- **Generic parent**: a foreign key column prefixed with `parent_` (e.g. `parent_question_id`). Routes include the parent in the path (e.g. `/tenants/{tenant_id}/questions/{parent_question_id}/takes`). Not all foreign keys are treated as parents -- only those with the explicit `parent_` prefix.

An entity can have both tenant and parent (e.g. takes), just tenant (e.g. questions), just parent (e.g. api_keys), or neither.

### Search Detection

The scaffolder finds text/string columns and adds them to a `@search: ilike(...)` annotation on the List query. Columns named with `hash`, `secret`, `token`, `password`, or `key_prefix` are excluded from search.

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
| `bridge.go` | Bridge struct wrapping the core Case, with logger, rate limiter, and JSON error renderer. Includes option pattern for configuration. |
| `http.go` | `AddHttpRoutes` method that registers routes under `/cases/<kebab-name>/`. |
| `model.go` | Stub for HTTP request/response types with validation. |

### Route Convention

Case routes register under `/cases/<kebab-name>/` to avoid conflicts with
generated CRUD routes. The expected mount point is `api.Group("/cases")`,
producing paths like `/api/v1/cases/<kebab-name>/...`.

### Name Normalization

The case name is normalized: converted to lowercase with hyphens removed. The
kebab-case form is used for route prefixes (e.g. `tenantadmin` produces
`/cases/tenantadmin/`, while `audio-recorder` produces package `audiorecorder`
and route `/cases/audio-recorder/`).

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

---

## gopernicus new adapter

Scaffold a custom infrastructure adapter that implements a framework interface,
along with a compliance test that validates the implementation against the
framework's test suite.

### Usage

```
gopernicus new adapter <type> <name>
```

### Arguments

| Argument | Description |
|---|---|
| `<type>` | Required. The adapter type (see table below). |
| `<name>` | Required. The adapter name (e.g. `redis`, `rabbitmq`, `s3`). Converted to lowercase. Used as both the package name and directory name. |

### Available Types

| Type | Interface | Location | Methods |
|---|---|---|---|
| `cache` | `cache.Cacher` | `infrastructure/cache/<name>/` | Get, GetMany, Set, Delete, DeletePattern, Close |
| `events` | `events.Bus` | `infrastructure/events/<name>/` | Emit, Subscribe, Close |
| `ratelimiter` | `ratelimiter.Storer` | `infrastructure/ratelimiter/<name>/` | Allow, Reset, Close |
| `hasher` | `cryptids.PasswordHasher` | `infrastructure/cryptids/<name>/` | Hash, Compare |
| `token` | `cryptids.TokenSigner` | `infrastructure/cryptids/<name>/` | Sign, Verify |
| `storage` | `storage.Client` | `infrastructure/storage/<name>/` | Upload, Download, Delete, Exists, List, DownloadRange, GetObjectSize, InitiateResumableUpload, SignedURL |
| `emailer` | `emailer.Client` | `infrastructure/communications/emailer/<name>/` | Send |

### What Gets Created

Two files are generated in the adapter directory:

| File | Purpose |
|---|---|
| `<name>.go` | Source file with struct definition, `New()` factory with option pattern, and method stubs for the interface. Includes a compile-time interface check (`var _ InterfaceRef = (*Struct)(nil)`). |
| `<name>_test.go` | Compliance test that instantiates the adapter and runs the framework's test suite (e.g. `cachetest.RunSuite`, `eventstest.RunSuite`). |

The command refuses to scaffold if the target directory already exists.

### Compliance Tests

Each adapter type has a corresponding test suite in the framework that validates
interface contracts. The generated test file imports the framework's test package
and runs the suite against your implementation:

```go
func TestCompliance(t *testing.T) {
    s := redis.New()
    defer s.Close()
    cachetest.RunSuite(t, s)
}
```

This ensures your adapter satisfies the full behavioral contract, not just the
type signature.

### Examples

```bash
# Scaffold a Redis cache adapter
gopernicus new adapter cache redis

# Scaffold a RabbitMQ event bus adapter
gopernicus new adapter events rabbitmq

# Scaffold an S3 storage adapter
gopernicus new adapter storage s3

# Scaffold a custom email delivery adapter
gopernicus new adapter emailer mailgun

# After scaffolding, implement the TODO stubs and run tests
go test ./infrastructure/cache/redis/...
```

### After Scaffolding

1. Implement the `TODO` stubs in `<name>.go` with your adapter's logic.
2. Add constructor parameters or options for configuration (connection strings, credentials, etc.).
3. Run the compliance tests: `go test ./<adapter-dir>/...`

---

## Related

- [generate](generate.md) -- generate Go code after scaffolding a repo
- [boot](boot.md) -- batch-scaffold all repos for a domain
- [db](db.md) -- reflect schema before scaffolding repos
- [init](init.md) -- initial project setup
