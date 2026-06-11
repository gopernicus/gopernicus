---
sidebar_position: 6
title: Testing
---

# Testing

Gopernicus organizes test infrastructure as part of the Workshop layer — testing support is a development-time concern, not something that ships in production binaries. The generator produces test scaffolding alongside your entities, and the `workshop/testing` packages provide the utilities to run it.

## Test tiers

Gopernicus uses Go build tags to separate test tiers:

| Tier | Build tag | What it tests | Docker required? |
|---|---|---|---|
| Unit | *(none)* | Pure logic, no I/O | No |
| Integration | `//go:build integration` | Store methods against a real database | Yes |
| E2E | `//go:build e2e` | HTTP endpoints through the full application stack | Yes |

```bash
# Unit tests only
go test ./...

# Integration tests (requires Docker for testcontainers)
go test -tags integration ./...

# E2E tests (requires Docker)
go test -tags e2e ./...
```

## Package structure

```
workshop/testing/
├── testenv/          # Composite environment — wires DB, cache, events, logger
├── testpgx/          # PostgreSQL container via testcontainers
├── testredis/        # Redis container via testcontainers
├── testhttp/         # HTTP client with JSON helpers
├── testserver/       # Full application server for E2E
├── pgxfixtures/      # Database cleanup utilities
├── fixtures/         # Generated + custom fixture factories
│   ├── generated.go  # Auto-generated per entity (regenerated)
│   └── fixtures.go   # Custom helpers (created once, never overwritten)
└── e2e/              # Generated E2E test files + setup bootstrap
    ├── setup_test.go           # Bootstrap — implement once (never overwritten)
    └── *_generated_test.go     # Route tests per entity (regenerated)
```

## testenv — composite environment

`testenv.New()` is the single entry point for wiring up test infrastructure. It creates a PostgreSQL container, an in-memory event bus, an in-memory cache, and a logger — all cleaned up automatically via `t.Cleanup()`.

```go
env := testenv.New(t, ctx,
    testenv.WithMigrations(migrations.Migrate),
    testenv.WithRedis(),                       // optional — default is in-memory cache
    testenv.WithSeed(func(pool *pgxdb.Pool) {  // optional — seed data after migrations
        // insert reference data
    }),
    testenv.WithLogLevel("DEBUG"),              // default: "ERROR"
    testenv.WithPostgresVersion("postgres:16-alpine"),
    testenv.WithExtensions("uuid-ossp"),        // pg_trgm included by default
)

// Use env.PGX.Pool, env.EventBus, env.Cache, env.Log
```

`TestEnv` fields:

| Field | Type | Description |
|---|---|---|
| `PGX` | `*testpgx.TestPGX` | PostgreSQL pool, container, and connection string |
| `Redis` | `*testredis.TestRedis` | Redis client and container (nil if not enabled) |
| `EventBus` | `events.Bus` | In-memory synchronous event bus |
| `Cache` | `cache.Cacher` | In-memory cache (10,000 entries) |
| `Log` | `*slog.Logger` | Structured logger |

## testpgx — PostgreSQL container

If you only need a database (no cache, no events), use `testpgx` directly:

```go
db := testpgx.SetupTestPGX(t, ctx,
    testpgx.WithMigrations(migrations.Migrate),
)

// db.Pool   — *pgxdb.Pool
// db.ConnStr — connection string
```

Uses `postgres:17-alpine` via testcontainers-go. Container and pool cleanup are registered automatically.

## testredis — Redis container

```go
redis := testredis.SetupTestRedis(t, ctx)

// redis.Client — *goredisdb.Client
// redis.Addr   — address string for config
```

Uses `redis:7-alpine` via testcontainers-go. Client and container cleanup are automatic.

## testhttp — HTTP test client

A JSON-aware HTTP client for E2E tests with auth header support and dot-path response traversal:

```go
client := testhttp.New(ts.URL())
client.SetBearerToken(token)

resp := client.Get(t, "/users")
resp.RequireStatus(t, 200)

// Dot-path extraction — supports nested keys and array indexing
name := resp.String(t, "data.0.name")
count := resp.DataLen(t)
```

Methods: `Get`, `Post`, `Put`, `Patch`, `Delete`. Request bodies are automatically marshaled to JSON.

Response helpers:

| Method | Returns | Description |
|---|---|---|
| `RequireStatus(t, code)` | — | Fatals if status doesn't match |
| `AssertStatus(t, code)` | — | Non-fatal status assertion |
| `JSON(t)` | `map[string]any` | Full response body |
| `JSONInto(t, &v)` | — | Decode into typed struct |
| `String(t, path)` | `string` | Dot-path string extraction |
| `Bool(t, path)` | `bool` | Dot-path bool extraction |
| `Value(t, path)` | `any` | Dot-path generic extraction |
| `Data(t)` | `[]any` | The `"data"` array from list responses |
| `DataLen(t)` | `int` | Length of `"data"` array |

## testserver — full application stack

Stands up a real HTTP server backed by your application's handler:

```go
cfg := testserver.DefaultConfig()
cfg.MigrateFn = migrations.Migrate
cfg.EnableRedis = true

ts := testserver.New(t, ctx, cfg, func(
    ctx context.Context,
    log *slog.Logger,
    infra testserver.Infrastructure,
) (http.Handler, error) {
    // Wire your application exactly as app.Run() does,
    // using infra.Pool and infra.RedisAddr
    return handler, nil
})

// ts.URL()           — base URL
// ts.APIURL("/users") — /api/v1/users
// ts.AuthURL("/login") — /api/v1/auth/login
```

The `ServerFactory` callback receives an `Infrastructure` struct with the database pool and Redis address. You wire your application handler the same way your `app.Run()` does, so E2E tests exercise the real middleware chain.

## pgxfixtures — database cleanup

Utilities for resetting database state between tests:

```go
pgxfixtures.TruncatePublicSchema(t, ctx, pool) // truncate all tables (CASCADE)
pgxfixtures.TruncateTable(t, ctx, pool, "users")
pgxfixtures.TruncateTables(t, ctx, pool, "users", "sessions")
pgxfixtures.DeleteByID(t, ctx, pool, "users", "user_id", id)
```

`TruncateSchema` dynamically discovers tables from `pg_tables` and excludes `schema_migrations`.

## Generated test files

The generator produces three categories of test files:

### Fixture factories (`fixtures/generated.go`)

Per entity, two functions:

```go
// Direct SQL INSERT — requires explicit parent IDs
user := fixtures.CreateTestUser(t, ctx, db)

// Auto-creates all FK dependencies first
user := fixtures.CreateTestUserWithDefaults(t, ctx, db)
```

Fixtures bypass the repository layer entirely (direct SQL INSERT + SELECT) for test isolation. IDs are generated via `cryptids.GenerateID()`.

### Integration tests (`*pgx/generated_test.go`)

Each store package gets tests for `Create`, `Get`, `List`, `Delete`, and `SoftDelete` (when `record_state` exists). These run against a real PostgreSQL container:

```go
//go:build integration

func TestGeneratedUserStore_Get(t *testing.T) {
    ctx, db, store := setupTestStore(t)
    pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)

    created := fixtures.CreateTestUserWithDefaults(t, ctx, db)
    result, err := store.Get(ctx, created.UserID)
    require.NoError(t, err)
    assert.Equal(t, created.UserID, result.UserID)
}
```

The generated tests call `WithDefaults`, which auto-creates every transitive FK ancestor needed for the insert. The fixtures package is cumulative across all domains, so `gopernicus generate <one-domain>` does not wipe other domains' helpers.

When a query declares scope parameters in its SQL (`@parent_world_id`, `@parent_service_account_id`, etc.), the generator threads those into the test call. So a `Get` that compiles to `Store.Get(ctx, id, parentWorldID)` is exercised as `store.Get(ctx, created.UserID, *created.ParentWorldID)` — reading the scope value off the fixture struct.

The `setupTestStore` helper, `migrateTestDB` function, and `testPGXOptions` variable are defined in a bootstrap file (`store_test.go`) that the generator creates once.

#### Bootstrap (`*pgx/store_test.go`)

Two hooks live in the bootstrap, both of which `setupTestStore` consumes:

```go
//go:build integration

package userspgx

import (
    "context"

    "github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
    "github.com/gopernicus/gopernicus/workshop/testing/testpgx"

    "your-module/workshop/testing/migrationsfx"
)

// migrateTestDB applies project migrations to the freshly-spawned container.
func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
    return pgxdb.RunMigrations(ctx, pool, migrationsfx.Primary(), "migrations")
}

// testPGXOptions is appended to the testpgx.SetupTestPGX option list.
// Use it when the default postgres:17-alpine image is missing extensions
// your migrations need.
var testPGXOptions = []testpgx.Option{
    testpgx.WithPostgresVersion("pgvector/pgvector:pg17"),
    testpgx.WithExtensions("vector", "pg_trgm"),
}
```

`setupTestStore` does the equivalent of:

```go
opts := append([]testpgx.Option{testpgx.WithMigrations(migrateTestDB)}, testPGXOptions...)
db := testpgx.SetupTestPGX(t, ctx, opts...)
```

so anything you add to `testPGXOptions` flows through every generated integration test in that package without editing the generated file.

#### Opting out

The escape-hatch ladder, in order:

1. **Do nothing** — the generator's built-in defaults (below) satisfy NOT NULL, simple `IN (…)` CHECKs, and length caps on their own.
2. **`@fixture-default`** — when a constraint needs a specific value the generator can't infer (an arbitrary CHECK predicate, a JSON shape requirement), pin that column's fixture value and keep the generated probes running:

   ```sql
   -- @fixture-default: payload {"backend":"db","ciphertext":"dGVzdA=="}
   -- @fixture-default: kind entity
   ```

   One column per line; see the [annotations reference](../topics/code-generation/annotations.md#fixture-default) for the grammar and per-type value handling.
3. **`@skip-integration-test`** — last resort, when no fixed per-column value can satisfy the invariant.

Some entities have cross-column invariants no fixed fixture value can satisfy — typically CHECK constraints that pin a mutually exclusive group of nullable FKs (e.g. a `nodes` table where `kind='entity'` requires exactly one of three tier columns). Add `-- @skip-integration-test` at the top of `queries.sql` to suppress the generated probes. The next regeneration replaces `generated_test.go` with a **setup-only** version: no test functions, but the `setupTestStore()` helper stays available. Hand-write smoke tests in `store_test.go` (the bootstrap) using it directly.

The same setup-only emission applies automatically to entities where no standard probe is usable from the generic fixtures (no single-PK `Get`, or scope params reading columns the fixtures seed as NULL) — the generation output notes each one.

:::caution Upgrading
Before v0.3.2, skipping removed `generated_test.go` entirely, so projects often copied `setupTestStore` into `store_test.go`. The setup-only file re-introduces the generated helper — delete your copy or the package fails to compile with a duplicate `setupTestStore`.
:::

#### Fixture defaults the generator already knows about

The fixture generator reads the reflected schema to pick sensible defaults so generated inserts pass NOT NULL and CHECK constraints out of the box:

- **CHECK constraints** with an `IN (…)` or `col = 'value'` shape: the first allowed string is used. So `principal_type` defaults to `"user"` when the constraint is `principal_type IN ('user', 'service_account')`.
- **`varchar(N)` columns** with small `N`: the default is `testUniqueID[:N]` so it fits without overflow.
- **`json` / `jsonb` columns** (Go type `json.RawMessage`): defaults to `json.RawMessage("{}")` because Postgres rejects empty bytes as invalid JSON.
- **Principal inheritance** (when an entity's PK is a FK to `principals`): the `principal_type` is set to the singular form of the table name (e.g. `service_accounts` → `service_account`) to match the canonical CHECK on the `principals` table.

### E2E tests (`e2e/*_generated_test.go`)

Each bridged entity gets HTTP-level tests for its CRUD routes:

```go
//go:build e2e

func TestGeneratedUser_Get(t *testing.T) {
    ctx, db, ts := setupTestServer(t)
    client := testhttp.New(ts.URL())

    created := fixtures.CreateTestUserWithDefaults(t, ctx, db)

    resp := client.Get(t, "/users/"+created.UserID)
    resp.RequireStatus(t, 200)
    assert.Equal(t, created.UserID, resp.String(t, "user_id"))
}
```

The `setupTestServer` function is defined in `e2e/setup_test.go` — a bootstrap file you implement once to wire your full application.

## Bootstrap vs regenerated files

| Category | Created once (safe to edit) | Regenerated (do not edit) |
|---|---|---|
| Fixtures | `fixtures/fixtures.go` | `fixtures/generated.go` |
| Integration | `*pgx/store_test.go` | `*pgx/generated_test.go` |
| E2E | `e2e/setup_test.go` | `e2e/*_generated_test.go` |

Bootstrap files are created by the generator the first time, then never overwritten. Add your custom test helpers and scenario-specific tests in these files.

## Docker requirement

Both integration and E2E tests rely on [testcontainers-go](https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/) to manage PostgreSQL and Redis containers. Docker (or a compatible runtime) must be running on the machine executing tests.

Container output is suppressed by default. Set `TESTCONTAINERS_VERBOSE=true` for diagnostic logging from the container runtime.
