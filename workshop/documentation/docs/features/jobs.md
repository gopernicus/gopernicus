---
title: Jobs
description: Durable queueing, schedules, retries, dead letters, and fenced keyed work.
---

# Jobs

`features/jobs` is a datastore-free durable queue and scheduling feature built on `sdk/foundation/workers`. It provides ordinary jobs and schedules plus a hardened fenced queue that implements the SDK keyed-work protocol.

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

## Service and runtime are separate

```go
svc, err := jobs.NewService(repos, jobs.Config{
    Handlers: map[string]jobs.HandlerFunc{
        "thumbnail": createThumbnail,
    },
    Cron:   robfigcron.New(),
    Logger: log,
})
if err != nil {
    return err
}

runtime, err := jobs.NewRuntime(svc)
if err != nil {
    return err
}

if err := svc.Register(mount); err != nil {
    return err
}

go func() {
    if err := runtime.Run(ctx); err != nil {
        log.ErrorContext(ctx, "jobs runtime stopped", "error", err)
    }
}()
```

`NewRuntime` accepts the built service so its worker pool shares the same wake channel. A successful enqueue wakes in-process workers immediately; polling remains the cross-process backstop.

`Register` validates/logs registration and starts no goroutine. The host owns runtime start, cancellation, and drain.

## Nil semantics

| Field | Meaning |
|---|---|
| `Repositories.Queue` | ordinary queue off; either Queue or FencedQueue is required |
| `Repositories.Schedules` | queue-only host; scheduler pool omitted |
| `Repositories.FencedQueue` | keyed/fenced surface off |
| `Config.Handlers` | runtime construction fails when empty |
| `Config.Cron` | valid until a cron schedule is used; interval schedules still work |
| sizing/timing fields | zero chooses safe defaults |

## Fenced keyed work

The optional fenced repository adds stronger delivery semantics:

- lease-fenced complete/fail/checkpoint operations reject stale workers;
- a PII-free logical key supports enqueue-once and atomic replace/supersede;
- claimed payload can be checkpointed before a side effect so retries replay identical bytes;
- capped exponential backoff and immediate permanent failures;
- terminal callbacks run only after dead-letter state is durable;
- bounded purge of terminal work.

The public jobs `Service` implements `work.Enqueuer`, `work.Replacer`, and `work.StatusReader`. Consuming features depend on SDK interfaces, never on jobs. Executor callbacks and rich job aggregates remain host-side wiring.

`NewFencedRuntime` owns execution of this surface. Its process timeout must be shorter than the claim lease so a provider call cannot continue after another worker legitimately reclaims the job.

## Scheduling semantics

Claiming a due schedule is a value compare-and-swap on `next_run_at`. Competing runtimes race; one wins. A deterministic job ID per schedule slot collapses crash-window retries. Missed windows fire once and advance from the current time rather than replaying an unbounded backlog.

Cron evaluation is UTC. `integrations/scheduling/robfig-cron` implements the parser shape; `Spec.Every` needs no external library.

## Stores

Use the public `features/jobs/memstore` for zero-infrastructure hosts, or select pgx/Turso sibling modules. All run the feature conformance suite. The SQL migration source includes the ordinary queue, schedules, and fenced queue.

See `examples/jobs-minimal` for the executable queue/retry/dead-letter/schedule protocol.
