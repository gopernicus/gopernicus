# Worker pool wake hint

## Problem

`workers.WorkerPool` polls on an adaptive interval (`pollInterval` when active, `idleInterval` when idle). With long idle intervals, pickup latency for a freshly-enqueued job is up to one full `idleInterval` — fine for batch work, bad UX for user-triggered work like a Slack bot pulling files into Drive.

We want callers to be able to push the pool *just enough* to skip the next idle wait when they know work just landed, without giving up the polling backstop that handles crash recovery, retries with `scheduled_for`, and missed signals.

## Goal

Two pieces ship together in this PR:

1. **`sdk/workers/WithWakeChannel(<-chan struct{})`** — generic SDK-level option. The pool selects on the channel alongside its interval timer; a value triggers an immediate work iteration.
2. **`infrastructure/events/WakeChannel(bus, topic)`** — thin helper that subscribes to a bus topic and returns a channel suitable for `WithWakeChannel`.

The two-layer split is deliberate. `sdk/workers` must not depend on `infrastructure/events` (alphabetical layering: SDK can't import infrastructure). The pool primitive stays generic and testable with a raw channel; the bus glue lives where the bus does.

The wake is **a hint, not a contract** — the pool drains opportunistically and never blocks the sender. If the wake is lost, polling still picks up the work eventually.

## Part 1 — `sdk/workers` pool change

### Public API

[sdk/workers/pool.go](sdk/workers/pool.go) — new option alongside `WithPollInterval` / `WithIdleInterval`:

```go
// WithWakeChannel registers a signal channel that the pool selects on
// alongside its interval timer. When a value is received, the next work
// iteration runs immediately.
//
// The channel is a non-durable hint: the pool drains receives opportunistically
// and never blocks the sender. Callers should use a buffered channel (cap 1)
// with non-blocking sends:
//
//	wake := make(chan struct{}, 1)
//	select { case wake <- struct{}{}: default: }
//
// Lost wake signals are tolerated — the polling backstop catches the work
// on its next idle/active tick. If wake is nil, the pool behaves exactly
// as if no option were passed.
//
// Do not close the wake channel — receiving on a closed channel returns
// immediately and would spin work iterations until ctx cancel. The intended
// lifecycle is: pool runs until ctx cancels; emitters tear themselves down
// without closing wake.
func WithWakeChannel(wake <-chan struct{}) PoolOption {
    return func(c *poolConfig) { c.wake = wake }
}
```

Add `wake <-chan struct{}` to `poolConfig` and to `WorkerPool`. `NewPool` copies `c.wake` into `pool.wake`.

### Run loop change

[sdk/workers/pool.go](sdk/workers/pool.go), inside `worker()` around [pool.go:222-235](sdk/workers/pool.go#L222-L235). Currently:

```go
for {
    select {
    case <-wp.ctx.Done():
        return
    case <-ticker.C:
        // ...work block...
    }
}
```

After:

```go
for {
    select {
    case <-wp.ctx.Done():
        return
    case <-ticker.C:
    case <-wp.wake:
    }
    // work block runs after either tick or wake
    ctx := WithWorkerID(wp.ctx, workerID)
    err := wp.workWithPanicRecovery(ctx)
    // ... existing post-work logic (stats, interval adjustment, error handling)
}
```

The work block is unchanged — it's just lifted out of the `case` and runs after the select picks either a tick or a wake. `ctx.Done` still short-circuits via `return`.

A nil wake channel is safe: Go's `select` skips nil channels, so the pool runs identically to today when no option is passed.

### Multi-worker semantics

With N workers and one wake channel, exactly one worker reads each wake signal — that's how Go channel receives work. **This is the correct behavior**: one wake = one worker checks once. The other N-1 workers continue on their normal cadence and pick up additional jobs naturally if there's a burst.

Don't try to fan out wakes to all workers. If a single job arrives, you don't want all N stomping on the queue checkout.

### Interval adjustment after a wake

The existing post-work logic already handles the adaptive interval correctly:

- `Checkout` returns work → interval becomes `pollInterval` (active mode).
- `Checkout` returns `ErrNoWork` → interval becomes `idleInterval` (back to idle).

No new code needed.

## Part 2 — `infrastructure/events` helper

### Public API

New file [infrastructure/events/wake.go](infrastructure/events/wake.go):

```go
package events

import "context"

// WakeChannel returns a buffered channel that receives a value every time
// the bus emits an event matching topic. Designed to feed
// workers.WithWakeChannel.
//
// Sends are coalesced — bursts collapse into a single wake — and never block
// the bus subscriber. Lost wakes are tolerated; the consumer (typically a
// polling worker pool) will catch up on its next interval tick.
//
// The returned Subscription's lifetime is the caller's to manage. Calling
// Unsubscribe stops further wakes from firing. Closing the bus has the same
// effect for all subscriptions it owns.
func WakeChannel(bus Bus, topic string) (<-chan struct{}, Subscription, error) {
    wake := make(chan struct{}, 1)
    sub, err := bus.Subscribe(topic, func(_ context.Context, _ Event) error {
        select {
        case wake <- struct{}{}:
        default:
        }
        return nil
    })
    if err != nil {
        return nil, nil, err
    }
    return wake, sub, nil
}
```

That's the whole helper. Sixteen lines including doc comments.

### Why split the channel and the subscription return values?

Callers feed the channel into `workers.WithWakeChannel`. The `Subscription` handle is for callers that want to cleanly unsubscribe before bus shutdown (e.g., dynamic teardown of a single worker without taking down the whole bus). For drivebot-style "subscribe at startup, lifetime ≈ process lifetime", the subscription handle can be discarded — `bus.Close` cleans up.

## Files touched

**Pool (Part 1):**
- [sdk/workers/pool.go](sdk/workers/pool.go) — `poolConfig.wake`, `WorkerPool.wake`, `NewPool` plumbing, `worker()` select rearrangement, new `WithWakeChannel` function.
- [sdk/workers/pool_test.go](sdk/workers/pool_test.go) — new tests below.

**Events helper (Part 2):**
- [infrastructure/events/wake.go](infrastructure/events/wake.go) — new file with `WakeChannel`.
- [infrastructure/events/wake_test.go](infrastructure/events/wake_test.go) — new file with tests below.

No changes to `runner.go`, `stats.go`, `middleware.go`, `errors.go`, `events.go`, or any bus implementation.

## Tests — Part 1 (`sdk/workers`)

[sdk/workers/pool_test.go](sdk/workers/pool_test.go). Use the existing test scaffolding (mock work funcs that count invocations and signal channels).

1. **`TestPool_WakeChannel_TriggersImmediateWork`** — long `pollInterval` (e.g. 5s), long `idleInterval` (e.g. 10s), wake channel registered. Send on wake. Assert work fires within 100ms.

2. **`TestPool_WakeChannel_NilIsSafe`** — `WithWakeChannel(nil)`. Assert pool runs normally.

3. **`TestPool_WakeChannel_CtxDoneTakesPrecedence`** — register wake, cancel ctx, send on wake. Assert pool exits without livelocking. (Racy by nature; the contract is "shutdown isn't blocked by wake".)

4. **`TestPool_WakeChannel_CoalescedSendsAreSafe`** — send on wake 100 times in a tight loop with the recommended `select { default }` pattern. Assert the pool doesn't deadlock and processes at least one wake-driven iteration.

5. **`TestPool_WakeChannel_OneWakePerSignalAcrossWorkers`** — pool with `WorkerCount=3`, wake buffered to 1. Send one wake. Assert exactly one extra work iteration (not three).

6. **`TestPool_WakeChannel_WakeWhileIdle_DropsToActive`** — pool in idle (long `idleInterval`). Mock returns work on next call. Send wake. Assert next post-wake tick fires on `pollInterval`, not `idleInterval`.

## Tests — Part 2 (`infrastructure/events`)

[infrastructure/events/wake_test.go](infrastructure/events/wake_test.go). Use `memorybus` for the test backend.

1. **`TestWakeChannel_EmitFiresWake`** — subscribe via `WakeChannel`, emit one matching event, assert receive on the channel within 100ms.

2. **`TestWakeChannel_BurstIsCoalesced`** — emit 100 events rapidly. Assert at most a small number of channel receives (the channel is cap-1, so most sends drop). Assert at least one. Assert no deadlock or panic.

3. **`TestWakeChannel_NonMatchingTopicIgnored`** — subscribe to `"a.foo"`, emit `"b.bar"`. Assert no receive on the channel within a short window.

4. **`TestWakeChannel_UnsubscribeStopsWakes`** — subscribe, unsubscribe, emit. Assert no receive.

5. **`TestWakeChannel_CloseClosesSub`** — subscribe via `WakeChannel`, close the bus. Assert no panic and the subscription's `Unsubscribe` is a safe no-op.

## Documentation

Update the package doc on [sdk/workers/pool.go](sdk/workers/pool.go) to mention the wake hook as an optional latency optimization on top of the polling model. The polling model remains the primary contract.

If gopernicus has higher-level docs at `workshop/documentation/docs/gopernicus/sdk/workers.md` (verify before assuming), add a short section "Reducing pickup latency with wake hints" showing the bus-driven pattern end-to-end:

```go
// in setup:
wake, _, err := events.WakeChannel(bus, "drivebot.upload.requested")
if err != nil {
    return fmt.Errorf("wake subscribe: %w", err)
}

pool := workers.NewPool(work, opts,
    workers.WithIdleInterval(5*time.Minute),  // long backstop
    workers.WithWakeChannel(wake),
)

// elsewhere, after enqueue:
bus.Emit(ctx, drivebot.UploadRequestedEvent{...})
```

Mirror that snippet (or reference it) in any `infrastructure/events` overview doc.

## Edge cases

- **Wake during in-flight work.** A worker is mid-`workFunc`. Wake fires. The select isn't re-entered until the work completes. The wake stays in the channel buffer (cap 1) and is consumed on the next loop iteration. No work lost.

- **Closed wake channel.** Receiving on a closed channel returns immediately and never blocks. If a caller closes the wake channel during shutdown, the pool spins work iterations until ctx.Done fires. **Don't close the wake channel** — documented in `WithWakeChannel`'s comment.

- **Bus down / Subscribe error.** `WakeChannel` returns the error. Callers must check it (typed return). On error, the worker pool can still be constructed without `WithWakeChannel` and falls back to pure polling — degraded but functional.

- **Sender races with shutdown.** Caller emits while pool is shutting down. Select either picks ctx.Done (pool exits, wake value GC'd) or picks wake (one final iteration, then ctx.Done on next loop). Both correct.

- **High-volume bus topics.** A topic with thousands of events/sec subscribes via `WakeChannel`. Coalescing keeps the pool sane (it processes once per work cycle regardless of inbound rate), but the bus subscriber goroutine still receives every event. If that's a hot path, consider a separate dedicated subscriber that throttles. For drivebot-volume topics it's not a concern.

## Out of scope

- A `WithWakeFunc` callback variant. Channel form matches Go idioms.
- Per-worker wake channels. One channel for the whole pool; one wake = one check.
- Bounded coalescing inside the pool. The `select { default }` pattern in `WakeChannel` and at any direct sender's call site is the contract.
- Telemetry on wake-driven vs tick-driven work. If we ever need to measure wake hit-rate, add it as a separate counter.
- A typed-event variant of `WakeChannel` (filter by event payload, not just topic). Topic-only is sufficient for the wake pattern.

## Verification

```
cd sdk/workers       && go build ./... && go test ./...
cd infrastructure/events && go build ./... && go test ./...
```

Plus a smoke test: a pool with 1-hour `idleInterval` and a `WakeChannel` subscriber, emitting one event should cause work within milliseconds.
