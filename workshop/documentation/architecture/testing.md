# Testing

Gopernicus supports three levels of automated testing: store-level integration
tests against a real database, end-to-end (E2E) HTTP tests against a fully
wired server, and standard unit tests.  The framework provides infrastructure
helpers and generates test fixtures and boilerplate.

## Test infrastructure packages

All test infrastructure lives under `infrastructure/testing/`:

| Package        | Purpose                                                   |
|----------------|-----------------------------------------------------------|
| `testpgx`      | Spins up a Postgres container via testcontainers, runs migrations, provides `*TestPGX` with a pool. |
| `testredis`    | Spins up a Redis container for tests requiring cache or event bus. |
| `testhttp`     | HTTP client with JSON handling and assertion helpers for E2E tests. |
| `testserver`   | Wires the full application stack (database, Redis, HTTP handler) into `*TestServer` for E2E tests. |
| `testenv`      | Environment variable helpers for test configuration.       |
| `pgxfixtures`  | Database cleanup helpers (`TruncateTable`, `TruncateSchema`). |

## `testpgx.TestPGX` -- real database tests

`testpgx.SetupTestPGX(t, ctx, opts...)` starts a PostgreSQL 17 Alpine
container, connects a `*pgxdb.Pool`, and registers cleanup with `t.Cleanup`.
Options include:

- `WithPostgresVersion(tag)` -- override the Docker image.
- `WithExtensions(ext...)` -- enable Postgres extensions (pg_trgm is always
  enabled).
- `WithMigrations(fn)` -- run migrations after connecting.

The returned `*TestPGX` exposes `Pool`, `Container`, and `ConnStr`.  Cleanup
closes the pool and terminates the container.

Store-level integration tests use `TestPGX` directly:

```go
func TestUserStore_Create(t *testing.T) {
    ctx := context.Background()
    db := testpgx.SetupTestPGX(t, ctx, testpgx.WithMigrations(migrations.Run))
    store := userspgx.NewStore(slog.Default(), db.Pool)

    user, err := store.Create(ctx, users.CreateUser{...})
    require.NoError(t, err)
    assert.NotEmpty(t, user.UserID)
}
```

## Generated fixtures

The code generator produces a `fixtures` package with `CreateTest<Entity>`
functions for every entity.  These insert test data via **direct SQL**,
bypassing the repository layer for test isolation:

```go
func CreateTestInvitation(
    t *testing.T,
    ctx context.Context,
    db *testpgx.TestPGX,
) invitations.Invitation { ... }
```

Fixtures generate unique IDs via `cryptids.GenerateID()`, handle parent entity
dependencies as parameters, and support principal inheritance tables where
applicable.

`CreateTestEntityWithDefaults` variants are also generated with sensible
default field values, reducing boilerplate in tests that only care about a
subset of fields.

## Generated integration tests

The generator produces store-level integration tests in each pgxstore package.
These tests exercise every generated store method (Get, Create, List, Update,
Delete, soft-delete/archive/restore) against a real Postgres container using
the fixture functions.

The `IntegrationTestData` struct drives generation: it holds package info,
import paths, PK details, and a list of `IntegrationTestMethod` entries
describing each method to test (category, parameters, return types).

## E2E tests with `testhttp`

E2E tests exercise the full HTTP stack.  The generator produces test files
with the `//go:build e2e` build tag so they are excluded from normal
`go test` runs.

### Test server setup

`testserver.New(t, ctx, cfg, serverFn)` creates a `*TestServer` with:

- A Postgres container with migrations applied.
- An optional Redis container (`cfg.EnableRedis`).
- The application's HTTP handler created via `serverFn`.
- An `httptest.Server` exposing the URL.

Apps extend `TestServer` with domain helpers in their own testserver package
(e.g., `CreateTestUser`, `CreatePlatformAdmin`).

### HTTP client (`testhttp.Client`)

`testhttp.New(baseURL)` creates a client with built-in JSON encoding and
auth support:

```go
client := testhttp.New(ts.URL())
client.SetBearerToken(token)

resp := client.Post(t, "/invitations/tenant/t1", body)
resp.RequireStatus(t, 201)
createdID := resp.String(t, "invitation_id")
```

The `Response` type provides:

- `RequireStatus(t, code)` / `AssertStatus(t, code)` -- status assertions.
- `JSON(t)` -- decode body to `map[string]any`.
- `JSONInto(t, v)` -- decode body into a typed struct.
- `String(t, path)`, `Bool(t, path)`, `Value(t, path)` -- extract values by
  dot-separated path (e.g., `"data.0.user_id"`).
- `Data(t)` / `DataLen(t)` -- extract the `"data"` array from list responses.

### Generated E2E test structure

```go
//go:build e2e

func TestGeneratedInvitation_Create(t *testing.T) {
    _, _, ts := setupTestServer(t)
    client := testhttp.New(ts.URL())

    body := map[string]any{...}
    resp := client.Post(t, "/invitations/tenant/t1", body)
    resp.RequireStatus(t, 201)

    createdID := resp.String(t, "invitation_id")
    getResp := client.Get(t, "/invitations/" + createdID)
    getResp.RequireStatus(t, 200)
}
```

## Build tags

| Tag   | Scope                                          |
|-------|-------------------------------------------------|
| `e2e` | End-to-end tests requiring full server stack.   |

Store-level integration tests do not use build tags -- they run on every
`go test` invocation (provided Docker is available for testcontainers).

## Database cleanup

`pgxfixtures.TruncateTable`, `TruncateTables`, and `TruncateSchema` provide
cleanup between test cases.  `TruncateSchema` dynamically discovers tables
from `pg_tables` and truncates with CASCADE, excluding `schema_migrations`.

---

## Related

- `infrastructure/testing/testpgx/testpgx.go` -- Postgres container setup
- `infrastructure/testing/testhttp/client.go` -- HTTP test client
- `infrastructure/testing/testserver/testserver.go` -- full-stack test server
- `infrastructure/testing/testredis/testredis.go` -- Redis container setup
- `infrastructure/testing/pgxfixtures/pgxfixtures.go` -- database cleanup
- `gopernicus-cli/internal/generators/fixture_tmpl.go` -- fixture generation
- `gopernicus-cli/internal/generators/e2e_test_tmpl.go` -- E2E test generation
- `gopernicus-cli/internal/generators/integration_test_gen.go` -- integration test generation
- [Architecture Overview](overview.md)
- [Repositories](repositories.md)
- [Cases](cases.md)
