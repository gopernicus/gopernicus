---
sidebar_position: 2
title: Cache
---

# Cache

The cache package provides a key-value caching abstraction with TTL support. It separates the storage contract (`Cacher`) from the service layer (`Cache`), which adds tracing and JSON convenience on top of any backend.

## Two Layers

### Cacher

`Cacher` is the interface that backend implementations satisfy:

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

`Get` returns a `(value, found, error)` triple — `found` is false when the key doesn't exist, which is distinct from an error. `GetMany` returns only the keys that exist; missing keys are simply absent from the map. `TTL` of `0` means no expiration. `DeletePattern` uses glob-style wildcards (`users:*`).

### Cache

`Cache` wraps any `Cacher` and adds optional OTEL tracing and JSON helpers:

```go
store := memorycache.New(memorycache.Config{MaxEntries: 5000})
c := cache.New(store, cache.WithTracer(tracer))
```

When a tracer is configured, every operation gets a span with relevant attributes — `cache.key`, `cache.hit`, `cache.ttl_ms`, and so on. Without a tracer, operations pass through directly with no overhead.

## JSON Helpers

Raw `[]byte` operations are the baseline, but most cache usage is structured data. Two package-level generics handle marshaling:

```go
// Store a struct — marshals to JSON automatically
err := cache.SetJSON(c, ctx, "users:123", user, 5*time.Minute)

// Retrieve it — unmarshals back to the type
user, found, err := cache.GetJSON[User](c, ctx, "users:123")
```

Both functions are nil-safe — if the `*Cache` is nil they return zero values without error, which makes optional caching easy to wire.

## Status Check

```go
err := cache.StatusCheck(ctx, c)
```

Writes a test key, reads it back, and deletes it. Returns nil if healthy. Safe to use in health check endpoints — also nil-safe.

## Implementations

| Package | When to use |
|---|---|
| `memorycache/` | Single-instance deployments, local development |
| `rediscache/` | Multi-instance deployments, shared cache |
| `noopcache/` | Caching disabled, feature flagging |

### memorycache

LRU cache backed by `container/list`. Thread-safe. Configurable capacity with automatic eviction of the least-recently-used entry:

```go
store := memorycache.New(memorycache.Config{
    MaxEntries: 10000, // defaults to 10000 if unset or <= 0
})
```

`DeletePattern` uses glob matching with `*` wildcards. Not suitable for multi-instance deployments — each instance has its own independent cache.

### rediscache

Redis-backed via `go-redis`. `GetMany` uses `MGET` for a single round trip. `DeletePattern` uses `SCAN` to avoid blocking:

```go
store := rediscache.New(redisClient,
    rediscache.WithKeyPrefix("myapp:"),  // defaults to "cache:"
)
```

Key prefix is applied to all operations. Useful for namespacing in shared Redis instances. The Redis client lifecycle is managed externally — `Close` is a no-op.

### noopcache

All operations succeed and store nothing. Useful when caching is disabled via configuration without changing call sites:

```go
var store cache.Cacher
if cfg.CacheEnabled {
    store = rediscache.New(redisClient)
} else {
    store = noopcache.New()
}
c := cache.New(store)
```

## Compliance Suite

`cachetest` provides a compliance suite that verifies any `Cacher` implementation satisfies the full contract. Run it in your adapter's test file:

```go
func TestCompliance(t *testing.T) {
    store := memorycache.New(memorycache.Config{})
    defer store.Close()
    cachetest.RunSuite(t, store)
}
```

## Custom Backends

To scaffold a new `Cacher` implementation:

```
gopernicus new adapter cache myrediscluster
```

Generates a new sub-package with method stubs and a compliance test wired to `cachetest.RunSuite`.
