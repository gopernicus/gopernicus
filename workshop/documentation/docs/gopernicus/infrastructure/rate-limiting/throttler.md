---
sidebar_position: 3
title: Throttler
---

# Rate Limiting — Throttler

`github.com/gopernicus/gopernicus/infrastructure/ratelimiter/throttler`

A `Throttler` **blocks** until capacity is available, rather than returning a rejection. This is designed for background workers that need to respect rate limits without dropping work — for example, a worker syncing data from an external API that enforces a 15 req/sec limit.

Compare the two approaches:

| | Rate Limiter | Throttler |
|---|---|---|
| Caller | HTTP request handler | Background worker |
| When limit is hit | Returns `429 Too Many Requests` | Blocks until capacity is available |
| Work dropped? | Yes (caller must retry) | No (caller waits) |

## Throttler interface

```go
type Throttler interface {
    Acquire(ctx context.Context, key string, limit ratelimiter.Limit) error
    Close() error
}
```

`Acquire` blocks until allowed. It returns an error only if the context is cancelled — never for rate limit exhaustion.

## New (Storer-backed)

```go
import "github.com/gopernicus/gopernicus/infrastructure/ratelimiter/throttler"

th := throttler.New(store, log)
defer th.Close()

// In your worker:
if err := th.Acquire(ctx, "github-api", ratelimiter.PerSecond(15)); err != nil {
    return err // context cancelled
}
// Proceed with the API call
```

`New` wraps any `ratelimiter.Storer`. On each denied request it reads `Result.RetryAfter` and sleeps that duration before retrying. Works with `memorylimiter`, `sqlitelimiter`, or `goredislimiter`.

Use this when your workers run on a single instance or you're already using a shared store.

## NewTokenBucket (Redis-backed, even metering)

```go
th := throttler.NewTokenBucket(rdb, log)
th := throttler.NewTokenBucket(rdb, log,
    throttler.WithTokenBucketKeyPrefix("myapp:throttle:tb:"),  // default "throttle:tb:"
)
```

The token bucket meters requests **evenly over time** rather than allowing a burst at the start of each window. At 15 req/sec, requests are spaced ~66ms apart instead of allowing 15 in the first millisecond and then starving for the rest of the second.

The Lua script runs atomically on Redis and calculates the precise wait time until the next token is available, so workers sleep the minimum necessary duration.

**When to use `NewTokenBucket` over `New`:**
- External APIs with strict burst limits that reject even momentary spikes
- Workers running across multiple instances that need shared, even-rate metering
- When you care about uniform throughput, not just average rate

## Usage in a worker

```go
func (w *SyncWorker) Run(ctx context.Context) error {
    th := throttler.NewTokenBucket(rdb, log)
    defer th.Close()

    limit := ratelimiter.PerSecond(15).WithBurst(5)

    for _, item := range items {
        if err := th.Acquire(ctx, "github-sync", limit); err != nil {
            return err // context cancelled, shut down cleanly
        }

        if err := w.processItem(ctx, item); err != nil {
            // handle error, continue loop
        }
    }
    return nil
}
```

## See also

- [Limiters](./limiters) — `Storer` implementations to back `throttler.New`
- [SDK / Workers](/docs/gopernicus/sdk/workers) — worker pool and job runner that throttlers are typically used within
