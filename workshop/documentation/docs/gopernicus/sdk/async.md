---
sidebar_position: 2
title: Async
---

# SDK — Async

`sdk/async` provides a bounded goroutine pool with panic recovery. It wraps `sync`, `sync/atomic`, and `runtime` from the standard library.

Use `async.Pool` when you need fire-and-forget concurrency with a hard cap on how many goroutines run at once. For background jobs that need scheduling, retries, or middleware, see [Workers](./workers.md).

## Creating a pool

```go
pool := async.NewPool(
    async.WithMaxConcurrency(50),
    async.WithLogger(log),
)
defer pool.Close(ctx)
```

Defaults: `MaxConcurrency=100`, `DropOnFull=false`, `ShutdownTimeout=30s`.

## Presets

Two presets cover the most common cases:

```go
// I/O-bound tasks: high concurrency (1000), drop on full, 30s shutdown
pool := async.NewPool(async.IOPreset()...)

// CPU-bound tasks: GOMAXPROCS concurrency, blocking, 60s shutdown
pool := async.NewPool(async.CPUPreset()...)

// Presets can be combined with overrides
pool := async.NewPool(append(async.IOPreset(), async.WithLogger(log))...)
```

## Submitting tasks

`Go` submits a task and returns immediately. `GoContext` respects context cancellation while waiting for a slot.

```go
pool.Go(func() {
    invalidateCache(userID)
})

pool.GoContext(ctx, func() {
    sendWebhook(payload)
})
```

Both return `false` if the task was dropped or the pool is closed.

## Backpressure

By default the pool blocks when at max concurrency until a slot is free. Setting `WithDropOnFull(true)` makes `Go` return immediately instead, dropping the task and logging a warning.

```go
pool := async.NewPool(
    async.WithMaxConcurrency(100),
    async.WithDropOnFull(true),
)
```

Choose blocking for tasks that must not be lost; drop-on-full for best-effort side effects like cache invalidation.

## Shutdown

`Close` marks the pool as closed, waits for in-flight tasks up to `ShutdownTimeout`, and logs a warning if the timeout is exceeded. New tasks submitted after `Close` return `false` without executing.

```go
if err := pool.Close(ctx); err != nil {
    // ctx was cancelled before shutdown completed
}
```

`Wait` blocks until all current tasks finish without closing the pool — useful in tests or when you need a checkpoint mid-execution.

## Panic recovery

Panics inside `Go` and `GoContext` are recovered automatically. The stack trace is logged at error level and the `Panics` counter increments. The pool continues accepting tasks.

## Stats

```go
stats := pool.Stats()
// stats.Active  — goroutines currently running
// stats.Total   — total tasks started since creation
// stats.Dropped — tasks dropped (DropOnFull=true only)
// stats.Panics  — recovered panics
```
