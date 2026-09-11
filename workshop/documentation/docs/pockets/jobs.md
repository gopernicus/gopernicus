---
title: Jobs
description: Durable queueing, schedules, retries, dead letters, and fenced keyed work.
---

# Jobs

`pockets/jobs` is a datastore-free durable queue and scheduling pocket built on `sdk/pkg/workers`. It provides ordinary jobs and schedules plus a hardened fenced queue that implements the SDK keyed-work protocol.

Jobs currently registers no HTTP routes. `/jobs/*` is reserved for a future admin surface.

## Ordinary queue and schedules

- enqueue with an idempotency ID;
- priority and scheduled-for ordering;
- atomic lease-based claim;
- stale-claim recovery when a lease expires;
- bounded attempts and dead-letter transition;
- interval schedules without a parser;
- optional five-field cron/descriptor schedules;
- multi-runtime schedule firing via compare-and-swap and deterministic job IDs.

Handlers are at-least-once. A process can finish the side effect and crash before completion is recorded, so handlers should be idempotent where possible.

`MaxAttempts` supplied to `EnqueueJob` is the persisted ordinary-job failure ceiling. Jobs
without a positive stored limit use `jobs.WithMaxAttempts`; permanent rejection
overrides both. Ordinary failed jobs are immediately eligible for retry. The
fenced runtime provides configurable retry backoff.

Ordinary terminal jobs cannot be revived by a late completion/failure. Repeating
the same terminal outcome is an unchanged success; the opposite outcome returns
`sdk.ErrConflict`. Running jobs still have no per-claim fence on the ordinary
queue. Choose the fenced queue when stale workers must be prevented from writing.

Memory stores copy payloads on admission/checkpoint and return detached job
values, including timestamp pointers. Mutating a returned job cannot change
persisted state.

Ordinary admission rejects malformed JSON with `sdk.ErrInvalidInput` and stores
`{}` for nil/empty payloads. Valid nonempty JSON bytes are preserved. Fenced
admission and checkpoints continue accepting opaque bytes.

## Service and runtime are separate

```go
parts, err := jobs.New(repos, jobs.WithMaxAttempts(3),
    jobs.WithCronParser(robfigcron.New()))
if err != nil {
    return err
}

runtime, err := queue.NewRuntime(parts.Queue,
    map[string]queue.HandlerFunc{"thumbnail": createThumbnail},
    queue.WithScheduler(parts.Schedules), queue.WithRuntimeLogger(log))
if err != nil {
    return err
}

// The host owns start, cancellation and waiting for graceful completion.
return runtime.Run(ctx)
```

Import `queue` from `pockets/jobs/logic/queue` and `schedules` from
`pockets/jobs/logic/schedules`. Their services, types and consumed ports are
public. The root only assembles `Components{Queue, Schedules}`; hosts may also
construct each service directly. `Schedules` is nil when scheduling is disabled.

`queue.NewRuntime` accepts the built queue service so the worker pool shares its
wake channel. A successful enqueue wakes local workers immediately; polling is
the cross-process backstop. No construction starts workers or registers routes.

The host owns runtime start, cancellation and drain. The ordinary runtime detaches
cancellation for admitted iterations so they can finish and persist. Bound handler
execution and store I/O separately; handler timeouts do not bound claim or terminal
writes. A fatal queue/scheduler error cancels its sibling pool and returns after
both finish. Each runtime is single-use.

### Staged handler wiring

Build services first, then pass their handlers directly to runtime construction:

```go
parts, err := jobs.New(repos)
if err != nil {
    return err
}

// Other services can now depend on parts.Queue.
handlers := map[string]queue.HandlerFunc{"thumbnail": createThumbnail}
runtime, err := queue.NewRuntime(parts.Queue, handlers,
    queue.WithScheduler(parts.Schedules), queue.WithRuntimeLogger(log))
```

`parts.Schedules.EnsureSchedule` works before runtime construction when schedules
are configured. `NewRuntime` validates and copies the handler map; both pools use
that snapshot's kinds. Empty maps return `ErrHandlersRequired`; empty keys or nil
handlers return `ErrInvalidHandler`. Later map edits cannot change an existing
runtime. Do not mutate the map during construction; captured handler state remains
host-owned.

## Nil semantics

| Field | Meaning |
|---|---|
| `Repositories.Queue` | ordinary queue off; either Queue or FencedQueue is required |
| `Repositories.Schedules` | queue-only host; scheduler pool omitted |
| `Repositories.FencedQueue` | keyed/fenced surface off |
| `NewRuntime handlers` | runtime construction fails when empty |
| `jobs.WithCronParser` | valid until a cron schedule is used; interval schedules still work |
| `queue.WithRuntimeWorkerMiddleware` / `queue.WithRuntimeJobMiddleware` | no wrappers; queue iteration / claimed-job processing |
| sizing/timing fields | zero chooses safe defaults |

## Fenced keyed work

The optional fenced repository adds stronger delivery semantics:

- lease-fenced complete/fail/checkpoint operations reject stale workers;
- a PII-free logical key supports enqueue-once and atomic replace/supersede;
- claimed payload can be checkpointed before a side effect so retries replay identical bytes;
- capped exponential backoff and immediate permanent failures;
- terminal callbacks run only after dead-letter state is durable;
- bounded purge of terminal work.

The public `queue.Service` implements `work.Enqueuer`, `work.Replacer`, and `work.StatusReader`. Consuming pockets depend on SDK interfaces, never on jobs. Executor callbacks and rich job aggregates remain host-side wiring.

Fenced admission rejects empty kinds before mutation. Failed replacement leaves
the existing generation and its lease intact. Dead-letter hooks run after the
terminal transition, but are best effort: a crash or hook failure is not retried.
Hosts needing durable cleanup must provide a recoverable cleanup workflow.

New keyed generations preserve insertion order even when clocks repeat or move
backward. Stores adjust `CreatedAt` and initial `UpdatedAt` past the latest stored
generation under their admission lock/transaction, at the database's precision.
These timestamps may be ahead of wall time; `ScheduledFor` and lease clocks are
independent, so the adjustment does not delay work. Existing history is not
rewritten, and all SQL writers need the updated adapter.

`NewFencedRuntime` owns execution of this surface. Its process timeout must be
shorter than the claim lease, allowing extra margin for store latency. Middleware
and handlers must cooperate with cancellation. Shutdown cancels processing and
leaves the claim recoverable; it does not record a shutdown failure. Store I/O
needs its own bounds.

Protocol keys for `EnqueueOnce`, `Replace` and `LatestStatusByKey` must be nonempty
(`sdk.ErrInvalidInput` otherwise). Empty execution IDs for ordinary `EnqueueJob`
still request a generated ID; these are separate concepts.

## Middleware and job gates

Worker middleware wraps one queue iteration, before claim and through persistence.
Job middleware wraps processing after claim. The first wrapper supplied is
outermost; shared state must support concurrent calls.

- Ordinary: `queue.WithRuntimeWorkerMiddleware` and `queue.WithRuntimeJobMiddleware`, where job
  middleware receives `queue.Job`.
- Fenced: `queue.WithFencedWorkerMiddleware` and `queue.WithFencedJobMiddleware`, where job
  middleware receives `queue.FencedClaim`, including tenant and checkpoint access.

These use SDK `workers.Middleware` / `workers.JobMiddleware[T]`; custom queues can
use the same SDK mechanism without adopting jobs. Existing host handler wrappers
remain valid. Worker middleware applies only to the queue pool; hosts control
scheduler wiring separately.

A worker gate returns `workers.ErrNoWork` to avoid claiming. A job gate returns
`workers.DeferUntil(futureTime, reason)` to wait without spending a failure
attempt, or `workers.Reject(reason)` to dead-letter immediately. `queue.Permanent`
forwards to Reject. Other errors follow normal retry policy. Nil means successful
processing, including a completed check that found no changes.

All built-in stores support atomic deferral. Custom stores can implement optional
`queue.QueueDeferrer` / `queue.FencedQueueDeferrer`; otherwise requesting deferral
returns an unsupported error and leaves the claim for recovery. Fenced deferral
validates live ownership, releases the lease and refunds only this claim's
increment. Existing attempts and payload checkpoints are preserved. Ordinary
deferral cannot provide ownership fencing without a lease token.

Execution deadlines belong around job processing. Middleware's after-processing
code runs before persistence; terminal hooks remain separate and run only after
a successful dead-letter transition. See the [SDK execution contract](../sdk/pkg.md#workers-versus-jobs)
for composition and an example.

## Scheduling semantics

A claim atomically checks the expected slot, kind and recurrence, advances the
schedule, and persists an immutable pending occurrence. It captures the current
payload and tenant at claim. A schedule may have one pending occurrence at a time.
The engine lists pending work by handler kind, enqueues its stable JobID, and
acknowledges only success or an already-existing ID. Failures and restarts retry
that same occurrence, even with separate schedule and queue stores.

Schedule edits, disabling and deletion affect future admission; admitted work
retains its original kind and payload. Acknowledgement records matching
LastRunAt/LastJobID atomically and cannot overwrite a newer occurrence. Missed
windows coalesce to one occurrence and compute the next time from now.

`Spec.Every` accepts whole-second intervals of at least one second. Fractional or
negative intervals fail validation instead of being truncated by SQL. Cron results
must be nonzero and strictly advance time; `integrations/scheduling/robfig-cron`
provides the usual UTC parser. Occurrence IDs retain fractional slot precision.

Delivery and handler effects are at least once. Retain ordinary queue IDs during
dispatch/retry and make side effects idempotent. `Schedules.ListPending` exposes
unacknowledged occurrences for host-owned inspection and alerting. Persisted
attempt counts rotate failed deliveries behind less-attempted pending work; the
engine returns errors rather than silently dropping work. Invalid legacy
recurrences or missing cron configuration can still fill the pre-claim due batch;
disable/fix those schedules or restore the parser. Memory stores do not survive process
loss. New SQL migration 0004 must precede new schedulers; stop old writers during
rollout, and drain pending occurrences before rollback.

## Stores

Use the public `pockets/jobs/stores/memory` for zero-infrastructure hosts, or select pgx/Turso sibling modules. All run the pocket conformance suite. The SQL migration source includes the ordinary queue, schedules, and fenced queue.

See `examples/jobs-minimal` for the executable queue/retry/dead-letter/schedule protocol.
