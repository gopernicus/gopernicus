---
sidebar_position: 1
title: pgxdb
---

# postgres/pgxdb

PostgreSQL via [jackc/pgx](https://github.com/jackc/pgx). Returns `*pgxpool.Pool` directly — `Pool` is a type alias so callers don't need to import pgxpool.

```go
pool, err := pgxdb.New(pgxdb.Options{
    DatabaseURL: cfg.DatabaseURL,
    MaxConns:    25,
    MinConns:    5,
    MaxLifetime: time.Hour,
    MaxIdleTime: 30 * time.Minute,
    HealthCheck: time.Minute,
}, pgxdb.WithLogger(log))
```

Config fields map to environment variables via `sdk/environment` struct tags:

```go
var cfg pgxdb.Options
environment.ParseEnvTags("MYAPP", &cfg)
// reads MYAPP_DB_DATABASE_URL, MYAPP_DB_MAX_CONNS, etc.
```

## Querier

`Querier` is a pgx-specific interface — not a gopernicus-level database abstraction. It exists because both `*pgxpool.Pool` and `pgx.Tx` support the same query methods, and generated pgx stores accept `Querier` so they can execute against either without caring which:

```go
type Querier interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
    Begin(ctx context.Context) (pgx.Tx, error)
}
```

This is the transaction story for pgx stores specifically:

```go
// Normal usage — pass the pool
store := NewUserStore(pool)

// Transaction — pass the tx, same store code runs unchanged
tx, _ := pool.Begin(ctx)
store := NewUserStore(tx)
// ... operations ...
tx.Commit(ctx)
```

A SQLite store has its own equivalent pattern using `*sql.Tx`. There is no shared transaction interface across drivers.

## Error Handling

`HandlePgError` converts PostgreSQL error codes to infrastructure sentinels. Generated pgx stores call this and map the results to domain errors:

```go
// In a generated pgx store
if err := pgxdb.HandlePgError(err); err != nil {
    if errors.Is(err, pgxdb.ErrDBDuplicatedEntry) {
        return users.ErrEmailTaken
    }
    return err
}
```

Sentinels: `ErrDBNotFound`, `ErrDBDuplicatedEntry`, `ErrDBForeignKeyViolation`, `ErrDBCheckViolation`, `ErrDBNotNullViolation`.

## Query Tracing

Opt into query logging during development:

```go
pool, err := pgxdb.New(cfg, pgxdb.WithLogQueries(true))
```

Or supply a custom `pgx.QueryTracer` for OTEL instrumentation:

```go
pool, err := pgxdb.New(cfg, pgxdb.WithTracer(myTracer))
```
