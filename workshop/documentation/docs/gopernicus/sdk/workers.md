---
sidebar_position: 11
title: Workers
---

# SDK — Workers

`sdk/workers` provides a durable background job system built on a polling loop. It wraps `context`, `sync`, `time`, `log/slog`, and `runtime/debug` from the standard library.

**Use Workers when:** jobs need to be persisted, retried on failure, and tracked through a lifecycle (claimed → processing → complete/failed). For fire-and-forget concurrency with no persistence, see [Async](./async.md).

---

## Architecture

The package has two independent components that compose together:

```
WorkerPool          — manages N goroutines, adaptive polling, panic recovery
    ↓ executes
Runner[T]           — orchestrates job lifecycle: checkout → process → complete/fail
    ↓ depends on
JobStore[T]         — your implementation (database, queue, etc.)
ProcessFunc[T]      — your business logic
```

`WorkerPool` knows nothing about jobs. `Runner` knows nothing about goroutines. They connect via `runner.WorkFunc()`, which returns a `WorkFunc` the pool can call in its loop.

---

## WorkerPool

### Creating a pool

`Options` carries environment-tag-based config (compatible with `sdk/environment`). `PoolOption` functions override individual fields:

```go
opts := workers.Options{} // reads WORKER_NAME, WORKER_COUNT, WORKER_POLL_INTERVAL, WORKER_IDLE_INTERVAL
environment.ParseEnvTags("APP", &opts)

pool := workers.NewPool(runner.WorkFunc(), opts,
    workers.WithName("email-worker"),
    workers.WithWorkerCount(3),
    workers.WithLogger(log),
)
```

Defaults: 5 workers, 5s poll interval, 30s idle interval.

### Starting and stopping

`Start` is **blocking** — it launches workers and returns only when all of them exit. Run it in a goroutine:

```go
go func() {
    pool.Start(ctx)
}()

// later — stops all workers gracefully
pool.Stop()
```

Critical errors (from `ErrPoolShutdown`) are sent to the `Errors()` channel:

```go
go func() {
    for err := range pool.Errors() {
        log.Error("pool shutdown error", "err", err)
    }
}()
```

### Adaptive polling

Workers start at 1ms, then switch behavior based on the work result:

| Result | Next interval |
|---|---|
| `nil` (success) | `pollInterval` (default 5s) |
| `ErrNoWork` | `idleInterval` (default 30s) |
| other error | `pollInterval` |
| `ErrWorkerShutdown` | worker exits |
| `ErrPoolShutdown` | all workers exit |

### Reducing pickup latency with wake hints

Adaptive polling works well for batch work, but user-triggered jobs (e.g. a Slack bot pulling files into Drive) suffer up to one full `idleInterval` of latency when the pool is idle.

`WithWakeChannel` lets callers push a hint that work just landed. The pool selects on the wake channel alongside its interval timer — a signal triggers an immediate work iteration without giving up the polling backstop that handles retries and crash recovery.

```go
// Create a wake channel (buffered, cap 1).
wake := make(chan struct{}, 1)

pool := workers.NewPool(runner.WorkFunc(), opts,
    workers.WithIdleInterval(5*time.Minute),  // long backstop
    workers.WithWakeChannel(wake),
)

// After enqueuing work, send a non-blocking wake signal.
select {
case wake <- struct{}{}:
default: // channel full — pool will pick it up
}
```

The wake is a hint, not a contract. Sends are coalesced (bursts collapse into one wake) and never block. If the signal is lost, polling catches up on the next tick.

For event-driven wake signals, use `events.WakeChannel` to bridge a bus topic to the pool:

```go
import "github.com/gopernicus/gopernicus/infrastructure/events"

wake, sub, err := events.WakeChannel(bus, "drivebot.upload.requested")
if err != nil {
    return fmt.Errorf("wake subscribe: %w", err)
}
defer sub.Unsubscribe()

pool := workers.NewPool(runner.WorkFunc(), opts,
    workers.WithIdleInterval(5*time.Minute),
    workers.WithWakeChannel(wake),
)
```

Now every `drivebot.upload.requested` event wakes the pool immediately.

---

## Sentinels

Three sentinel errors control pool and worker lifecycle. Return them from a `WorkFunc`, middleware, or `JobStore.Checkout`:

| Sentinel | Effect |
|---|---|
| `ErrNoWork` | Switch this worker to idle interval; not counted as an error |
| `ErrWorkerShutdown` | Stop this worker gracefully; pool continues |
| `ErrPoolShutdown` | Stop all workers; error sent to `Errors()` channel |

Any other non-nil error is logged, counted, and the worker continues.

---

## Runner

`Runner[T]` orchestrates the full job lifecycle. It is generic over any type that satisfies the `Job` interface:

```go
type Job interface {
    GetID() string
    GetStatus() string
    GetRetryCount() int
}
```

### Implementing JobStore

Your data layer implements `JobStore[T]`. The canonical implementation uses `FOR UPDATE SKIP LOCKED` for atomic checkout:

```go
type JobStore[T Job] interface {
    Checkout(ctx context.Context, workerID string, now time.Time) (T, error)
    Complete(ctx context.Context, jobID string, now time.Time) error
    Fail(ctx context.Context, jobID string, now time.Time, reason string, maxRetries int) error
}
```

Return `workers.ErrNoWork` from `Checkout` when the queue is empty.

### Creating a runner

```go
runner := workers.NewRunner(
    store,       // your JobStore[T] implementation
    processFunc, // your ProcessFunc[T]
    log,
    workers.WithMaxAttempts(3),
    workers.WithTracer(tracer), // optional — see Tracing below
    workers.WithClock(fakeClock), // optional — inject a custom clock for testing
)
```

### Job lifecycle

```
Checkout → PreHooks → Process (with retry) → PostHooks → Complete/Fail
```

Pre- and post-process hooks run regardless of whether processing succeeds:

```go
runner.AddPreProcessHooks(func(ctx context.Context, job MyJob) error {
    // runs after checkout, before process — validation, setup
    return nil
})

runner.AddPostProcessHooks(func(ctx context.Context, job MyJob, err error) error {
    // runs after process, before complete/fail — cleanup, metrics
    // err is nil on success
    return nil
})
```

### Retry behavior

`WithMaxAttempts` sets the **total number of attempts**. `WithMaxAttempts(3)` means the job will be tried up to 3 times. The default is 3.

Retries use exponential backoff (1s, 2s, 4s, ...). Context cancellation is checked before each retry.

---

## Composing pool and runner

```go
// 1. Implement your domain types
type EmailJob struct { /* must satisfy Job interface */ }

type emailStore struct{ db *pgx.Pool }
func (s *emailStore) Checkout(ctx context.Context, workerID string, now time.Time) (EmailJob, error) { ... }
func (s *emailStore) Complete(ctx context.Context, jobID string, now time.Time) error { ... }
func (s *emailStore) Fail(ctx context.Context, jobID string, now time.Time, reason string, max int) error { ... }

// 2. Define processing logic
processEmail := func(ctx context.Context, job EmailJob) (EmailJob, error) {
    return job, sendEmail(ctx, job)
}

// 3. Build the runner
runner := workers.NewRunner[EmailJob](store, processEmail, log,
    workers.WithMaxAttempts(3),
)

// 4. Build the pool
var opts workers.Options
environment.ParseEnvTags("EMAIL_WORKER", &opts)

pool := workers.NewPool(runner.WorkFunc(), opts,
    workers.WithMiddleware(
        workers.ConsecutiveErrorShutdown(10),
    ),
)

// 5. Start (blocking — run in goroutine)
go pool.Start(ctx)

// 6. Handle critical errors
go func() {
    for err := range pool.Errors() {
        log.Error("email worker pool error", "err", err)
    }
}()
```

---

## Middleware

Middleware wraps `WorkFunc` with cross-cutting behavior. Applied first-to-last on the way in, last-to-first on the way out.

### Built-in middleware

**TracingMiddleware** — creates a span per worker iteration (pool-level tracing):

```go
workers.WithMiddleware(workers.TracingMiddleware(tracer))
```

**ConsecutiveErrorShutdown** — stops a worker after N consecutive errors:

```go
workers.WithMiddleware(workers.ConsecutiveErrorShutdown(10))
```

> **Note:** `ConsecutiveErrorShutdown` creates a single error-count map shared across all workers via closure. Do not reuse the same value across multiple pools — each pool needs its own `ConsecutiveErrorShutdown(n)` call.

### Custom middleware

```go
func RateLimitMiddleware(limiter *rate.Limiter) workers.Middleware {
    return func(next workers.WorkFunc) workers.WorkFunc {
        return func(ctx context.Context) error {
            if err := limiter.Wait(ctx); err != nil {
                return err
            }
            return next(ctx)
        }
    }
}
```

---

## Tracing

There are two integration points, serving different purposes:

| | Pool middleware (`TracingMiddleware`) | Runner (`WithTracer`) |
|---|---|---|
| **Granularity** | One span per poll iteration | Spans for checkout, process, complete, fail |
| **Use when** | Tracing worker throughput and error rate | Tracing per-job operations |
| **Complementary?** | Yes — use both for full coverage |

---

## Stats

```go
stats := pool.Stats()
// stats.ActiveWorkers — goroutines currently running
// stats.Iterations   — total work function calls
// stats.Errors       — non-nil, non-ErrNoWork errors
// stats.Panics       — recovered panics
```

---

## Workers vs Async

| | Workers | Async |
|---|---|---|
| **Model** | Polling loop with job persistence | Fire-and-forget task submission |
| **Job tracking** | Checkout → complete/fail in a store | None |
| **Retries** | Built-in with exponential backoff | None |
| **Use for** | Email queues, background processing, scheduled tasks | Cache invalidation, side effects, parallel I/O |
