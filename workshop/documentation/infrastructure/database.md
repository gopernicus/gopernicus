# infrastructure/database -- PostgreSQL Reference

Package `pgxdb` provides PostgreSQL connection pooling and error handling built on `pgx/v5` and `pgxpool`.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb`

## Pool

`pgxdb.Pool` is a type alias for `pgxpool.Pool`, so callers do not need to import pgxpool directly.

```go
type Pool = pgxpool.Pool
```

## Creating a Pool

### pgxdb.New

Creates a connection pool from configuration and functional options. Pings the database to verify connectivity.

```go
pool, err := pgxdb.New(pgxdb.Options{
    DatabaseURL: "postgres://user:pass@localhost:5432/mydb",
    MaxConns:    25,
    MinConns:    5,
    MaxLifetime: time.Hour,
    MaxIdleTime: 30 * time.Minute,
    HealthCheck: time.Minute,
}, pgxdb.WithLogger(log), pgxdb.WithLogQueries(true))
```

### pgxdb.NewTestDB

Creates a pool with relaxed settings for tests.

```go
pool, err := pgxdb.NewTestDB("postgres://localhost:5432/testdb")
```

## Options

```go
type Options struct {
    DatabaseURL string        `env:"DB_DATABASE_URL" required:"true"`
    MaxConns    int           `env:"DB_MAX_CONNS" default:"25"`
    MinConns    int           `env:"DB_MIN_CONNS" default:"5"`
    MaxLifetime time.Duration `env:"DB_MAX_LIFETIME" default:"1h"`
    MaxIdleTime time.Duration `env:"DB_MAX_IDLE_TIME" default:"30m"`
    HealthCheck time.Duration `env:"DB_HEALTH_CHECK" default:"1m"`
}
```

Options use `env` tags compatible with `sdk/environment.ParseEnvTags`.

## Functional Options

| Option | Description |
|---|---|
| `WithLogger(log)` | Custom `*slog.Logger` |
| `WithTracer(tracer)` | Custom `pgx.QueryTracer` for instrumentation |
| `WithDatabaseURL(url)` | Override URL from Options |
| `WithMaxConns(n)` | Override max connections |
| `WithMinConns(n)` | Override min idle connections |
| `WithMaxLifetime(d)` | Override connection max lifetime |
| `WithMaxIdleTime(d)` | Override connection max idle time |
| `WithHealthCheck(d)` | Override health check period |
| `WithConnectTimeout(d)` | Timeout for initial connection (default 10s) |
| `WithLogQueries(bool)` | Enable automatic query logging via `LoggingQueryTracer` |

## Querier Interface

The critical abstraction that enables transaction composition. Both `*pgxpool.Pool` and `pgx.Tx` satisfy this interface, so generated stores can work with either.

```go
type Querier interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
```

Generated stores accept `Querier` as a constructor parameter:

```go
// Generated store constructor pattern:
func NewUserStore(q pgxdb.Querier) *UserStore { ... }

// Normal usage (pool):
store := NewUserStore(pool)

// Transaction usage:
tx, _ := pool.Begin(ctx)
store := NewUserStore(tx)
// ... operations ...
tx.Commit(ctx)
```

## Health Check

```go
err := pgxdb.StatusCheck(ctx, pool)
```

Uses the context deadline if present, otherwise applies a 1-second timeout.

## Error Handling

`HandlePgError` converts raw PostgreSQL errors to infrastructure-level sentinels.

```go
err = pgxdb.HandlePgError(err)
```

### Infrastructure Sentinels

| Sentinel | PostgreSQL Code | Description |
|---|---|---|
| `ErrDBNotFound` | (no rows) | Alias for `pgx.ErrNoRows` |
| `ErrDBDuplicatedEntry` | `23505` | UNIQUE constraint violation |
| `ErrDBForeignKeyViolation` | `23503` | FOREIGN KEY violation |
| `ErrDBCheckViolation` | `23514` | CHECK constraint violation |
| `ErrDBNotNullViolation` | `23502` | NOT NULL violation |
| `ErrUndefinedTable` | `42P01` | Table does not exist |

Generated stores call `HandlePgError` and then map these to domain errors (`errs.ErrNotFound`, `errs.ErrAlreadyExists`, etc.).

## Related

- [sdk/errs](../sdk/errs.md) -- domain error sentinels that stores map infrastructure errors to
- [sdk/fop](../sdk/fop.md) -- pagination and ordering types consumed by generated queries
