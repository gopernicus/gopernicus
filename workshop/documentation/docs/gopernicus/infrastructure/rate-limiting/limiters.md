---
sidebar_position: 2
title: Limiters
---

# Rate Limiting — Limiters

All store implementations satisfy `ratelimiter.Storer` and use a **sliding window** algorithm. The sliding window weights the previous window's count by how far into the current window you are, smoothing out boundary spikes compared to a fixed window.

## Choosing a store

| Store | Scope | Persistence | Use when |
|---|---|---|---|
| `memorylimiter` | single instance | none | development, single-instance apps |
| `sqlitelimiter` | single instance | survives restarts | single-instance production without Redis |
| `goredislimiter` | multi-instance | Redis TTL | distributed deployments |

## memorylimiter

`github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter`

```go
store := memorylimiter.New()
store := memorylimiter.New(
    memorylimiter.WithMaxEntries(5000),              // default 10,000
    memorylimiter.WithCleanupInterval(time.Minute),  // default 30s
)
defer store.Close()
```

State is process-local. Each instance enforces its own limits independently — in a multi-instance deployment a client hitting three instances gets 3× the headroom.

**Memory management:** Entries expire after 2× their window duration. A background goroutine cleans them on the configured interval. If `MaxEntries` is reached before cleanup runs, the least-recently-used entry is evicted immediately.

```go
stats := store.Stats()
// stats.EntryCount — current tracked keys
// stats.MaxEntries — configured cap
```

## sqlitelimiter

`github.com/gopernicus/gopernicus/infrastructure/ratelimiter/sqlitelimiter`

```go
import "github.com/gopernicus/gopernicus/infrastructure/ratelimiter/sqlitelimiter"

store, err := sqlitelimiter.New(db,
    sqlitelimiter.WithTableName("rate_limits"),      // default
    sqlitelimiter.WithCleanupInterval(time.Minute),  // default
    sqlitelimiter.WithLogger(log),                   // optional; cleanup errors are silent without it
)
defer store.Close()
```

`db` is any `sqlitelimiter.SQLExecutor` — satisfied by `*sql.DB` and `moderncdb.DB.Underlying()`. The store creates and manages its own table on first use.

Like `memorylimiter`, state is process-local. Unlike it, state survives restarts. Background cleanup removes entries whose windows have fully expired.

:::note
When using an in-memory SQLite database in tests, set `MaxOpenConns(1)` to ensure all calls share the same connection — SQLite in-memory databases are connection-scoped.
:::

## goredislimiter

`github.com/gopernicus/gopernicus/infrastructure/ratelimiter/goredislimiter`

```go
import "github.com/gopernicus/gopernicus/infrastructure/ratelimiter/goredislimiter"

store := goredislimiter.New(rdb)
store := goredislimiter.New(rdb,
    goredislimiter.WithKeyPrefix("myapp:ratelimit:"),  // default "ratelimit:"
)
```

`rdb` is a `*redis.Client` — typically from `goredisdb.New(...)`.

Rate limit state is shared across all instances in the same Redis database. The Lua script runs atomically on the Redis server, using **Redis server time** for all timestamps to avoid clock skew between application servers. Keys expire automatically via Redis TTL — no cleanup goroutine needed.

`Close` is a no-op; Redis manages key expiry.

## Compliance testing

`ratelimitertest.RunSuite` validates any `Storer` implementation:

```go
import "github.com/gopernicus/gopernicus/infrastructure/ratelimiter/ratelimitertest"

func TestStoreCompliance(t *testing.T) {
    store := memorylimiter.New()
    defer store.Close()
    ratelimitertest.RunSuite(t, store)
}
```

The suite covers: allowing under the limit, denying over the limit, reset behaviour, and key independence.
