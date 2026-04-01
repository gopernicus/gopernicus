---
sidebar_position: 2
title: "Zero to API"
---

# Zero to API

A linear walkthrough from `gopernicus init` to a running API with CRUD endpoints.
This guide uses a project called `myapp` with a `products` table — substitute
your own names throughout.

## Prerequisites

- Go 1.23+ installed
- Docker (for PostgreSQL and Redis)
- The gopernicus CLI:
  ```bash
  go install github.com/gopernicus/gopernicus-cli@latest
  ```

## 1. Create the Project

```bash
gopernicus init myapp
cd myapp
```

The interactive wizard walks you through feature selection. For this guide,
accept the defaults (all features enabled).

After init completes you have a full project structure: `app/server/`, `core/`,
`bridge/`, `workshop/`, a `Makefile`, `.env`, and `gopernicus.yml`.

## 2. Start Development Infrastructure

The scaffolded `workshop/dev/docker-compose.yml` provides PostgreSQL and Redis
for local development.

```bash
make dev-up
```

Verify the services are running:

```bash
make dev-ps
```

You should see `postgres` and `redis` both healthy. The database is configured
to match your `.env` — no manual setup needed.

## 3. Verify Project Health

```bash
gopernicus doctor
```

All five checks should pass. If any fail, the output explains how to fix them.

## 4. Run Initial Migrations

The selected features (authentication, authorization, tenancy, events, jobs)
each come with their own migration files. Apply them:

```bash
gopernicus db migrate
```

## 5. Reflect the Database Schema

The code generator needs a machine-readable snapshot of your database schema:

```bash
gopernicus db reflect
```

This writes `workshop/migrations/primary/_public.json` (consumed by the
generator) and `_public.sql` (human-readable summary).

## 6. Bootstrap Feature Repositories

The framework features selected during init have their tables mapped in
`gopernicus.yml`. Scaffold repositories for all of them at once:

```bash
gopernicus boot repos
```

This creates `queries.sql` and `bridge.yml` files for every framework table
(auth, rebac, tenancy, etc.).

## 7. Generate Code

```bash
gopernicus generate
```

This reads every `queries.sql` and `bridge.yml`, cross-references them with the
reflected schema, and produces the full Go codebase: repositories, stores, HTTP
bridges, composites, fixtures, and tests.

## 8. Start the Server

```bash
make dev
```

This starts the server with [air](https://github.com/air-verse/air) for hot
reload. You should see the boot sequence log:

```
→ telemetry initialized
→ database connected
→ redis connected
→ event bus started
→ cache initialized
→ server listening on 0.0.0.0:3000
```

Verify it's running:

```bash
curl http://localhost:3000/healthz
```

You should get a 200 OK response.

## 9. Add Your Own Entity

Now add something that isn't a framework table. Write a migration:

```bash
gopernicus db create add_products_table
```

Edit the generated migration file in `workshop/migrations/primary/`:

```sql
CREATE TABLE products (
    product_id    TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(tenant_id),
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    price_cents   INTEGER NOT NULL DEFAULT 0,
    record_state  TEXT NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_tenant ON products(tenant_id);
```

Apply, reflect, and scaffold:

```bash
gopernicus db migrate
gopernicus db reflect
```

Add the table to your domain mapping in `gopernicus.yml`:

```yaml
databases:
  primary:
    # ... existing config ...
    domains:
      # ... existing domains ...
      catalog:
        - products
```

Scaffold the repository and bridge:

```bash
gopernicus new repo catalog/products
```

This creates `core/repositories/catalog/products/queries.sql` with CRUD
operations (including GetBySlug, SoftDelete/Archive/Restore since the table has
`slug` and `record_state`) and `bridge/repositories/catalogreposbridge/productsbridge/bridge.yml`
with routes and auth schema.

Generate the Go code:

```bash
gopernicus generate
```

## 10. Wire the New Domain

Open `app/server/config/server.go` and wire the catalog domain. The generated
composite and bridge composite packages provide single-struct access to all
entities in the domain.

```go
// Initialize the catalog repositories.
catalogRepos := catalog.NewRepositories(log, infra.Pool, cacher, bus)
log.InfoContext(ctx, "init", "service", "catalog_repos")

// Initialize the catalog bridges and register routes.
catalogBridges := catalogreposbridge.NewBridges(log, catalogRepos, rateLimiter, authenticator, authorizer)
catalogBridges.AddHttpRoutes(api)
```

Add the corresponding imports at the top of the file:

```go
"github.com/your-org/myapp/core/repositories/catalog"
"github.com/your-org/myapp/bridge/repositories/catalogreposbridge"
```

Air will detect the change and rebuild automatically.

## 11. Test It

```bash
# List products (empty at first)
curl http://localhost:3000/api/v1/tenants/{tenant_id}/products

# Create a product
curl -X POST http://localhost:3000/api/v1/tenants/{tenant_id}/products \
  -H "Content-Type: application/json" \
  -d '{"name": "Widget", "slug": "widget", "price_cents": 999}'

# Get by slug
curl http://localhost:3000/api/v1/tenants/{tenant_id}/products/{product_id}
```

Note: requests require authentication headers. The specifics depend on your auth
setup — see [Authentication](/docs/gopernicus/bridge/auth/authentication) for
details on session tokens and API keys.

## Command Flow Summary

```
gopernicus init myapp          # scaffold project
make dev-up                    # start postgres + redis
gopernicus doctor              # verify health
gopernicus db migrate          # apply migrations
gopernicus db reflect          # snapshot schema
gopernicus boot repos          # scaffold framework repos
gopernicus generate            # generate Go code
make dev                       # run server with hot reload

# Adding a new entity:
gopernicus db create <name>    # create migration file
# ... edit the SQL ...
gopernicus db migrate          # apply it
gopernicus db reflect          # re-snapshot
gopernicus new repo domain/entity  # scaffold repo + bridge
gopernicus generate            # regenerate
# ... wire in server.go ...
# air auto-rebuilds
```

## What's Next

- [Adding a New Entity](/docs/guides/adding-new-entity) — deeper walkthrough with query annotations and bridge.yml customization
- [Adding a Use Case](/docs/guides/adding-use-case) — business logic beyond CRUD
- [Adding Authorization](/docs/guides/adding-auth-to-entity) — ReBAC setup
- [CLI Reference](/docs/cli/init) — all commands and flags
- [Makefile Reference](/docs/gopernicus/workshop/makefile) — all scaffolded make targets
