---
sidebar_position: 1
title: Overview
---

# Infrastructure — Rate Limiting

`github.com/gopernicus/gopernicus/infrastructure/ratelimiter`

Rate limiting in Gopernicus is composed of three parts:

- **[Limiters](./limiters)** — `Storer` implementations that track request counts per key (memory, Redis, SQLite)
- **`LimitResolver`** — decides what limit applies to a given subject (user tier, API key, anonymous)
- **`RateLimiter`** — the service that wires a store and resolver together

For **blocking** rate limiting in background workers — where you want to wait until capacity is available rather than reject — see [Throttler](./throttler).

## Limit and Result

```go
type Limit struct {
    Requests int
    Window   time.Duration
    Burst    int  // additive allowance on top of Requests; 0 = no burst
}

type Result struct {
    Allowed    bool
    Remaining  int
    ResetAt    time.Time
    RetryAfter time.Duration  // only meaningful when Allowed is false
}
```

Convenience constructors:

```go
ratelimiter.PerSecond(10)
ratelimiter.PerMinute(100)
ratelimiter.PerHour(1000)
ratelimiter.PerMinute(100).WithBurst(20)
ratelimiter.DefaultLimit()                // 100 req/min, no burst
```

## Storer interface

```go
type Storer interface {
    Allow(ctx context.Context, key string, limit Limit) (Result, error)
    Reset(ctx context.Context, key string) error
    Close() error
}
```

`Allow` is called on every request. `Reset` clears state for a key (admin overrides, test teardown). `Close` stops background goroutines and releases resources.

See [Stores](./stores) for available implementations.

## LimitResolver

`LimitResolver` is the extension point for subject-aware limits:

```go
type LimitResolver interface {
    Resolve(ctx context.Context, req ResolveRequest) Limit
}
```

`ResolveRequest` carries the authenticated subject:

```go
type ResolveRequest struct {
    SubjectType string     // "user", "service_account", "anonymous"
    SubjectID   string     // empty for anonymous
    APIKey      *APIKeyInfo
    ClientIP    string
}
```

### DefaultLimitResolver

```go
resolver := ratelimiter.NewDefaultResolver()
```

Resolution order:
1. **API key explicit override** — if the key has `RateLimitPerMinute` set, that wins
2. **Subject type defaults**:

| Subject | Default |
|---|---|
| `user` | 100 req/min + 10 burst |
| `service_account` | 500 req/min + 50 burst |
| `anonymous` | 60 req/min + 5 burst |

To add subscription tiers, implement `LimitResolver` and check tier before falling back to defaults.

## RateLimiter service

`RateLimiter` wraps a `Storer` and a `LimitResolver`:

```go
store    := memorylimiter.New()
resolver := ratelimiter.NewDefaultResolver()
limiter  := ratelimiter.New(store, resolver, ratelimiter.WithLogger(log))
defer limiter.Close()
```

Logging is opt-in via `WithLogger`. Without it, the rate limiter operates silently and returns all errors to the caller.

Two call paths:

```go
// Resolver-based: limit is determined from the subject
result, err := limiter.Allow(ctx, key, resolveReq)

// Explicit: bypass the resolver — for route-specific overrides
result, err := limiter.AllowWithLimit(ctx, key, ratelimiter.PerMinute(5))
```

The bridge middleware (`httpmid.RateLimit`) consumes `*RateLimiter` and handles HTTP response headers, 429 responses, and key extraction. See [Bridge / Middleware](/docs/gopernicus/bridge/middleware) for details.

## See also

- [Limiters](./limiters) — memorylimiter, goredislimiter, sqlitelimiter, compliance testing
- [Throttler](./throttler) — blocking rate limiting for background workers
- [Bridge / Middleware](/docs/gopernicus/bridge/middleware) — `httpmid.RateLimit` HTTP middleware
