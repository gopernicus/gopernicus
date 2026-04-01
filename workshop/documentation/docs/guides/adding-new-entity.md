---
sidebar_position: 1
title: Adding a New Entity
---

# Adding a New Entity

End-to-end guide for adding a new database entity -- from migration SQL to generated Go code and tests.

This walkthrough uses a fictional `projects` table in the `tenancy` domain as a running example. The table is tenant-scoped (has a `tenant_id` FK to `tenants`).

---

## Prerequisites

- A running Postgres database reachable via the URL in `.env`
- The `gopernicus` CLI installed
- Familiarity with the project layout: `core/repositories/`, `bridge/repositories/`, `workshop/migrations/`

---

## Step 1: Write the Migration SQL

Create a new migration file under `workshop/migrations/primary/`. Use the CLI to generate a timestamped filename:

```sh
gopernicus db create add_projects
```

This creates `workshop/migrations/primary/<timestamp>_add_projects.sql`. Write your DDL:

```sql
CREATE TABLE public.projects (
    project_id   VARCHAR NOT NULL,
    tenant_id    VARCHAR NOT NULL,
    name         VARCHAR NOT NULL,
    description  TEXT,
    record_state VARCHAR NOT NULL DEFAULT 'active',
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT projects_pk PRIMARY KEY (project_id),
    CONSTRAINT projects_tenant_fk FOREIGN KEY (tenant_id)
        REFERENCES tenants(tenant_id)
);
```

Apply the migration:

```sh
gopernicus db migrate
```

---

## Step 2: Reflect the Schema

Reflect the live database schema into the JSON file consumed by the code generator:

```sh
gopernicus db reflect
```

This connects to the database (using the `PRIMARY_POSTGRES_URL` env var from `gopernicus.yml`), introspects every schema listed in the manifest, and writes two files per schema:

- `workshop/migrations/primary/_public.json` -- machine-readable schema (columns, types, FKs, indexes)
- `workshop/migrations/primary/_public.sql` -- human-readable SQL summary

Verify the new table appears in `_public.json` before proceeding.

---

## Step 3: Add the Table to a Domain in gopernicus.yml

Open `gopernicus.yml` and add `projects` to the appropriate domain under `databases.primary.domains`:

```yaml
databases:
  primary:
    driver: postgres/pgx
    url_env_var: PRIMARY_POSTGRES_URL
    domains:
      tenancy: [tenants, projects]
```

The domain name determines the directory structure: `core/repositories/tenancy/projects/`.

---

## Step 4: Scaffold the Repository

Run the scaffold command with the `domain/entity` argument:

```sh
gopernicus new repo tenancy/projects
```

The CLI looks up `projects` in `_public.json`, detects the `tenant_id` FK to `tenants`, and generates `core/repositories/tenancy/projects/queries.sql` with full CRUD operations including:

- `List` -- tenant-scoped with filter, search, order, and pagination annotations
- `Get` -- by primary key with tenant scoping
- `Create` -- with `@fields` and `@event` annotations
- `Update` -- excludes PK, `record_state`, `created_at`, and `tenant_id` from the field set
- `SoftDelete`, `Archive`, `Restore` -- if the table has a `record_state` column
- `Delete` -- hard delete

The scaffold also generates `bridge.yml` in the bridge package directory with route definitions, ordered middleware arrays (authenticate, authorize, rate_limit), and auth schema (`auth_relations`, `auth_permissions`). Because the table has a `tenant_id` FK to `tenants`, the scaffold automatically:
- Nests routes under `/tenants/{tenant_id}/projects` in `bridge.yml`
- Adds `tenant_id = @tenant_id AND` to WHERE clauses in `queries.sql`
- Generates `auth_relations` and `auth_permissions` that inherit from the tenant

Use `--table` to override the table name if the entity name does not match:

```sh
gopernicus new repo tenancy/projects --table project_records
```

---

## Step 5: Customize queries.sql Annotations

Open `core/repositories/tenancy/projects/queries.sql` and review the generated data annotations. Common customizations:

- **Add custom queries** below the generated CRUD block (e.g., `GetByName`, `ListActive`)
- **Adjust `@filter:conditions`** to restrict which columns are filterable
- **Adjust `@search`** to add or remove full-text search columns
- **Adjust `@fields`** to add or remove columns from create/update inputs
- **Remove queries** that should not exist (the bridge.yml controls which have HTTP routes)

Custom queries follow the same annotation format. A query without a corresponding route in `bridge.yml` becomes a repository method only:

```sql
-- @func: GetByName
SELECT *
FROM projects
WHERE tenant_id = @tenant_id AND name = @name
;
```

---

## Step 5b: Customize bridge.yml

Open the bridge package's `bridge.yml` to review and customize route definitions,
middleware, and auth schema:

```yaml
routes:
  list:
    method: GET
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - rate_limit
      - authorize: prefilter(tenant:tenant_id, read)
  get:
    method: GET
    path: /tenants/{tenant_id}/projects/{project_id}
    middleware:
      - authenticate
      - authorize: check(read)
      - with_permissions
  create:
    method: POST
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - authorize: check(create)

auth_relations:
  - tenant(tenant)
  - owner(user, service_account)

auth_permissions:
  - list(tenant->list)
  - create(tenant->manage)
  - read(owner|tenant->read)
  - update(owner|tenant->manage)
  - delete(owner|tenant->manage)
  - manage(owner|tenant->manage)
```

Common customizations:
- **Add or remove middleware** from route middleware arrays
- **Add `unique_to_id`** middleware for slug-based lookups
- **Add `max_body_size`** middleware for upload routes
- **Adjust `auth_relations`** and `auth_permissions`
- **Add `with_permissions`** to routes that should return caller's permissions

---

## Step 6: Run the Code Generator

```sh
gopernicus generate tenancy
```

Or regenerate everything:

```sh
gopernicus generate
```

The generator produces two categories of files:

**Regenerated files** (overwritten every run -- never edit these):
- `generated.go` -- entity structs (`Project`, `CreateProject`, `UpdateProject`), filter structs, Repository methods on `*Repository`, errors, cursor helpers
- `<entity>pgx/generated.go` -- pgx store implementation
- `<entity>pgx/generated_test.go` -- integration test scaffolds
- `bridge/.../generated.go` -- HTTP handlers on `*Bridge`, generated request/response types, `addGeneratedRoutes()`
- `generated_authschema.go` -- authorization schema (in bridge composite directory)
- `generated_composite.go` -- domain-level composites

**Bootstrap files** (created once, never overwritten -- you own these):
- `repository.go` -- Storer interface (with `gopernicus:start`/`end` markers), Repository struct, NewRepository
- `<entity>pgx/store.go` -- custom store methods
- `<entity>pgx/store_test.go` -- custom integration tests
- `bridge/.../bridge.yml` -- route and middleware configuration
- `bridge/.../bridge.go` -- flat Bridge struct and constructor
- `bridge/.../routes.go` -- `AddHttpRoutes()` calling `addGeneratedRoutes()` + custom
- `bridge/.../http.go` -- custom HTTP handlers
- `bridge/.../authschema.go` -- authorization schema customization

Use `--dry-run` to preview changes without writing files:

```sh
gopernicus generate tenancy --dry-run
```

---

## Step 7: Customize Bootstrap Files

**repository.go** -- Add custom methods above the markers, customize construction:

```go
type Storer interface {
    // Custom methods above markers:
    GetByName(ctx context.Context, tenantID, name string) (Project, error)

    // gopernicus:start
    List(ctx context.Context, filter FilterList, ...) ([]Project, error)
    Get(ctx context.Context, projectID string) (Project, error)
    Create(ctx context.Context, input CreateProject) (Project, error)
    // ... generated methods
    // gopernicus:end
}

type Repository struct {
    store      Storer
    generateID func() string
    bus        events.Bus
}

func NewRepository(store Storer, opts ...Option) *Repository {
    r := &Repository{
        store:      store,
        generateID: cryptids.GenerateID,
    }
    for _, opt := range opts {
        opt(r)
    }
    return r
}
```

**bridge.go** -- Flat Bridge struct with all dependencies:

```go
type Bridge struct {
    repository    *projects.Repository
    log           *slog.Logger
    rateLimiter   *ratelimiter.RateLimiter
    authenticator *authentication.Authenticator
    authorizer    *authorization.Authorizer
    jsonErrors    httpmid.ErrorRenderer
}
```

---

## Step 8: Wire the Domain in server.go (if new domain)

If this is a new domain (not already wired), register its bridges in your server configuration. For an existing domain like `tenancy`, the generated composite handles wiring automatically.

For a brand-new domain, you need to register the composite bridge's routes on the API router group. The composite exposes an `AddHttpRoutes` method that registers all entity routes for the domain.

---

## Step 9: Test with Generated Integration Tests

The generator creates integration test scaffolds in `<entity>pgx/generated_test.go` (build tag: `integration`). Complete the migration helper in `store_test.go`:

```go
//go:embed ../../../../workshop/migrations/primary/*.sql
var testMigrations embed.FS

func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
    return pgxdb.RunMigrations(ctx, pool, testMigrations, "workshop/migrations/primary")
}
```

Run integration tests:

```sh
go test -tags integration ./core/repositories/tenancy/projects/projectspgx/...
```

---

## Checklist

- [ ] Migration SQL written and applied (`gopernicus db create`, `gopernicus db migrate`)
- [ ] Schema reflected (`gopernicus db reflect`)
- [ ] Table added to domain in `gopernicus.yml`
- [ ] Repository scaffolded (`gopernicus new repo domain/entity`)
- [ ] `queries.sql` reviewed and customized (data annotations only)
- [ ] `bridge.yml` reviewed and customized (routes, middleware, auth schema)
- [ ] Code generated (`gopernicus generate`)
- [ ] Bootstrap files customized (`repository.go`, bridge `bridge.go`, `routes.go`)
- [ ] Domain wired in server (if new domain)
- [ ] Integration tests passing

---

## Related

- [CLI: db](../cli/db.md)
- [CLI: new](../cli/new.md)
- [CLI: generate](../cli/generate.md)
- [Query Annotations](../gopernicus/topics/code-generation/annotations.md)
- [Schema Conventions](../gopernicus/topics/code-generation/schema-conventions.md)
- [Adding Authorization to an Entity](adding-auth-to-entity.md)
