# Caching

Gopernicus provides a decorator-based caching layer that wraps store
implementations with transparent read caching and automatic write-through
invalidation.  The entire cache stack is opt-in: when the `*cache.Cache`
pointer is nil, every operation passes through to the underlying store with
zero overhead.

## Architecture overview

```
Repository
  -> CacheStore (generated, wraps Storer)
       -> cache.Cache (service wrapper with tracing)
            -> Cacher interface (memory / Redis / noop)
       -> Storer (the real pgx store)
```

## The `@cache` annotation

In a `queries.sql` file you can annotate a query with `@cache` and a TTL
duration.  During code generation (`gopernicus generate`) the CLI reads this
annotation and produces a `generated_cache.go` file containing:

- A `CacheConfig` struct with per-method TTL fields and a `KeyPrefix` string.
- A `DefaultCacheConfig()` function returning the generated defaults.
- A `CacheStore` struct that embeds the repository's `Storer` interface and a
  `*cache.Cache`.
- Cached read methods, write-method overrides for invalidation, and
  invalidation helpers.

If no `@cache` annotations are present, the generated `CacheStore` is still
created (with no cached methods) so the composite wiring always compiles.

A one-time bootstrap file `cache.go` is also created for adding custom cache
logic without touching generated code.

## The `Cacher` interface

All cache backends implement the `Cacher` interface defined in
`infrastructure/cache/cache.go`:

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

Backends:

| Package           | Description                                      |
|-------------------|--------------------------------------------------|
| `memorycache`     | In-memory LRU cache (default 10 000 entries).    |
| `rediscache`      | Redis-backed cache using `go-redis`.             |
| `noopcache`       | No-op -- all ops succeed but store nothing.      |

## The `Cache` service wrapper

`cache.New(cacher, opts...)` returns a `*Cache` that wraps a `Cacher` with
optional OpenTelemetry tracing (`cache.WithTracer(tracer)`).  When tracing is
enabled, every Get/Set/Delete call creates a span recording the key, hit/miss
status, and errors.

Critically, `cache.New` returns **nil** when the supplied `Cacher` is nil.  All
generic helpers (`GetJSON`, `SetJSON`, `StatusCheck`) are nil-safe: they return
zero values without error when the receiver is nil.  This is the mechanism
behind "passthrough when cache is nil" -- the generated `CacheStore` simply
falls through to `s.Storer` in every method when `s.cache == nil`.

## Cache key generation

Keys are built from three segments:

```
<KeyPrefix>:<KeySegment>:<KeyExpr>
```

- **KeyPrefix** is set per entity (e.g., `rebac:invitations`).
- **KeySegment** identifies the method (e.g., `get`, `get_by_token`).
- **KeyExpr** is the Go expression that resolves to the primary key or
  lookup key for that method call.

Example key: `rebac:invitations:get:abc123`.

## TTL

Each cached method has its own TTL field in `CacheConfig`.
`DefaultCacheConfig()` populates them from the `@cache` annotation value
(e.g., `@cache: 5m` produces `5 * time.Minute`).  A TTL of `0` means no
expiration, subject to the memory LRU eviction policy or Redis key limits.

## Automatic invalidation on write

Every generated write method (Create, Update, Delete) calls
`s.invalidateByPK(ctx, pkValue)` after the underlying store call succeeds.
Invalidation removes all cached entries matching the pattern
`<KeyPrefix>:*:<pkValue>` via `cache.DeletePattern`.

Public helpers are also generated:

- `InvalidateByID(ctx, id)` -- invalidate all cache entries for one entity.
- `InvalidateAll(ctx)` -- invalidate all entries under the key prefix.

## Nil passthrough

The generated `CacheStore` always embeds `Storer`.  When `cache` is nil
(because caching was not configured), every generated method short-circuits:

```go
func (s *CacheStore) Get(ctx context.Context, id string) (Entity, error) {
    if s.cache == nil {
        return s.Storer.Get(ctx, id)
    }
    // ... cache lookup ...
}
```

This allows the composite wiring to always use `NewCacheStore(store, c)` where
`c` can be nil without branching logic.

## JSON serialization

Cache values are stored as JSON bytes.  The generic functions
`cache.GetJSON[T]` and `cache.SetJSON[T]` handle marshal/unmarshal.  Custom
encodings are possible by implementing `EventEncoder` on the cached type, but
JSON is the default for all generated code.

## Health check

`cache.StatusCheck(ctx, c)` performs a write/read/delete cycle on a sentinel
key to verify the cache is operational.  Returns nil if `c` is nil (no cache
configured).

---

## Related

- `infrastructure/cache/cache.go` -- `Cacher` interface, `Cache` wrapper, JSON helpers
- `infrastructure/cache/memorycache/` -- in-memory LRU backend
- `infrastructure/cache/rediscache/` -- Redis backend
- `infrastructure/cache/noopcache/` -- no-op backend
- `gopernicus-cli/internal/generators/cache_tmpl.go` -- code generation template
- [Repositories](repositories.md)
- [Architecture Overview](overview.md)
