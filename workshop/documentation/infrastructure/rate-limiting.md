# infrastructure/rate-limiting -- Rate Limiter Reference

Package `ratelimiter` provides rate limiting with pluggable backends, configurable limits per subject type, and API key overrides.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/ratelimiter`

## RateLimiter

The main service that combines a store backend with a limit resolver.

```go
store := memorylimiter.New()
resolver := ratelimiter.NewDefaultResolver()
limiter := ratelimiter.New(store, resolver, log)
```

### Methods

```go
// Use resolver to determine limit from request context
result, err := limiter.Allow(ctx, key, resolveReq)

// Use explicit limit (route-specific overrides)
result, err := limiter.AllowWithLimit(ctx, key, ratelimiter.PerMinute(5))

// Reset a key (admin override)
err := limiter.Reset(ctx, key)

// Clean shutdown
err := limiter.Close()
```

## Storer Interface

The store backend that all implementations satisfy.

```go
type Storer interface {
    Allow(ctx context.Context, key string, limit Limit) (Result, error)
    Reset(ctx context.Context, key string) error
    Close() error
}
```

### Available Backends

- `memorylimiter` -- In-memory sliding window. Suitable for single-instance deployments.

```go
import "github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter"

store := memorylimiter.New()
```

## Limit Type

```go
type Limit struct {
    Requests int           // max requests in window
    Window   time.Duration // time window
    Burst    int           // optional burst above base limit (0 = no burst)
}
```

### Helper Constructors

```go
ratelimiter.PerSecond(10)                    // 10 req/s
ratelimiter.PerMinute(100)                   // 100 req/min
ratelimiter.PerHour(1000)                    // 1000 req/h
ratelimiter.DefaultLimit()                   // 100 req/min
ratelimiter.PerMinute(100).WithBurst(10)     // 100/min + 10 burst
```

## Result Type

Returned by `Allow` and `AllowWithLimit`.

```go
type Result struct {
    Allowed    bool          // whether the request should proceed
    Remaining  int           // requests remaining in current window
    ResetAt    time.Time     // when the current window resets
    RetryAfter time.Duration // wait time before retrying (meaningful when Allowed=false)
}
```

## LimitResolver

Determines the rate limit based on the request subject.

```go
type LimitResolver interface {
    Resolve(ctx context.Context, req ResolveRequest) Limit
}
```

### ResolveRequest

```go
type ResolveRequest struct {
    SubjectType string    // "user", "service_account", "anonymous"
    SubjectID   string    // user or service account ID (empty for anonymous)
    APIKey      *APIKeyInfo // non-nil when authenticated via API key
    ClientIP    string    // fallback for anonymous keying
}
```

### DefaultLimitResolver

Hardcoded defaults with API key overrides. Resolution order:

1. API key explicit override (`RateLimitPerMinute` from API key schema)
2. Default based on subject type

```go
resolver := ratelimiter.NewDefaultResolver()
```

Default limits:

| Subject | Limit | Burst |
|---|---|---|
| User | 100 req/min | +10 |
| Service Account | 500 req/min | +50 |
| Anonymous | 60 req/min | +5 |

Custom defaults:

```go
resolver := &ratelimiter.DefaultLimitResolver{
    UserLimit:           ratelimiter.PerMinute(200).WithBurst(20),
    ServiceAccountLimit: ratelimiter.PerMinute(1000).WithBurst(100),
    AnonymousLimit:      ratelimiter.PerMinute(30).WithBurst(3),
}
```

## Error Sentinels

```go
var (
    ErrRateLimitExceeded = errors.New("rate limit exceeded")
    ErrLimiterClosed     = errors.New("rate limiter closed")
)
```

## Middleware Integration

The rate limiter is typically used via `httpmid.RateLimit` middleware, which extracts the subject from the request context and calls `limiter.Allow`. When rate-limited, it returns a 429 response with `Retry-After` and `X-RateLimit-*` headers.

## Related

- [sdk/web](../sdk/web.md) -- `web.ErrTooManyRequests` for manual 429 responses
