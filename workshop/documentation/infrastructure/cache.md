# infrastructure/cache -- Cache Reference

Package `cache` provides a caching layer with pluggable backends (memory, Redis, noop) and optional OpenTelemetry tracing.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/cache`

## Cacher Interface

The store interface that all backends implement.

```go
type Cacher interface {
    Get(ctx context.Context, key string) ([]byte, bool, error)
    GetMany(ctx context.Context, keys []string) (map[string][]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    DeletePattern(ctx context.Context, pattern string) error
    Close() error
}
```

- `Get` returns `(value, found, error)`. `found` is false when the key does not exist.
- `GetMany` returns a map of key to value for keys that exist. Missing keys are omitted.
- `Set` with `ttl` of 0 means no expiration.
- `Delete` returns nil if the key does not exist.
- `DeletePattern` removes all keys matching a pattern (e.g., `"users:*"`). Pattern syntax is backend-specific (Redis uses glob-style).

## Cache Service

Wraps a `Cacher` with optional tracing and JSON convenience methods.

```go
c := cache.New(memorycache.New(), cache.WithTracer(tracer))
```

If `cacher` is nil, `New` returns nil. All JSON convenience functions handle nil `*Cache` gracefully (return zero value, no error).

### Methods

The `Cache` struct exposes the same methods as `Cacher` (`Get`, `GetMany`, `Set`, `Delete`, `DeletePattern`, `Close`), adding tracing spans when a tracer is configured.

### JSON Convenience Functions

Generic functions that handle marshal/unmarshal:

```go
// Store a struct as JSON
err := cache.SetJSON(c, ctx, "user:123", user, 5*time.Minute)

// Retrieve and unmarshal
user, found, err := cache.GetJSON[User](c, ctx, "user:123")
```

Both functions are nil-safe: if `c` is nil, `SetJSON` is a no-op and `GetJSON` returns `(zero, false, nil)`.

## Backends

### memorycache

In-memory cache with TTL support. Suitable for single-instance deployments and tests.

```go
import "github.com/gopernicus/gopernicus/infrastructure/cache/memorycache"

store := memorycache.New()
c := cache.New(store)
```

### rediscache

Redis-backed cache for multi-instance deployments.

```go
import "github.com/gopernicus/gopernicus/infrastructure/cache/rediscache"

store := rediscache.New(redisClient)
c := cache.New(store)
```

### noopcache

No-op cache that always returns not-found. Useful for disabling caching without nil checks.

```go
import "github.com/gopernicus/gopernicus/infrastructure/cache/noopcache"

store := noopcache.New()
c := cache.New(store)
```

## Nil Cache Pattern

When caching is optional, pass `nil` as the cacher. `cache.New(nil)` returns `nil`, and `GetJSON`/`SetJSON` handle nil `*Cache` gracefully:

```go
var cacher cache.Cacher // nil if not configured
c := cache.New(cacher)  // returns nil

// These are safe to call:
cache.SetJSON(c, ctx, "key", value, ttl) // no-op
val, found, err := cache.GetJSON[T](c, ctx, "key") // found=false, err=nil
```

## Health Check

```go
err := cache.StatusCheck(ctx, c)
```

Writes and reads a test key. Returns nil if the cache is nil (disabled) or healthy.

## Tracing

When `WithTracer(tracer)` is set, every operation creates a span with attributes:

- `cache.key` -- the key being accessed
- `cache.hit` -- boolean, whether a Get found the key
- `cache.key_count` / `cache.hit_count` -- for GetMany
- `cache.ttl_ms` -- TTL in milliseconds for Set
- `cache.pattern` -- for DeletePattern

## Related

- [infrastructure/database](../infrastructure/database.md) -- primary data source that caching complements
- [infrastructure/tracing](../infrastructure/tracing.md) -- telemetry.Tracer used for cache span instrumentation
