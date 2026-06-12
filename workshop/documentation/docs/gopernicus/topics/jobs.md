---
title: Jobs (queue + scheduler)
---

# Background jobs and recurring schedules

The `job-queue` feature ships two entities and the wiring around them:

- **`job_queue`** — durable one-shot jobs, claimed with `FOR UPDATE SKIP
  LOCKED`, retried with exponential backoff, dead-lettered at max retries.
- **`job_schedules`** — cron-defined recurring jobs that fire rows *into*
  `job_queue`. A scheduled job is just a queue row; one handler path
  processes both.

The scaffolded `server.go` wires both pools: a **queue worker pool**
(dispatches on `event_type`) and a **scheduler pool** (fires due
schedules). They start with the server and drain on shutdown.

## Enqueuing one-shot jobs

Create a `job_queue` row through the repository; the worker pool picks it
up on its next poll:

```go
_, err := jobsRepos.JobQueue.Create(ctx, jobqueue.CreateJobQueue{
    JobID:      id,                  // unique — doubles as the idempotency key
    EventType:  "report.build",      // dispatch key for the handler map
    Payload:    payload,             // json.RawMessage
    OccurredAt: time.Now().UTC(),
    Status:     "PENDING",
    MaxRetries: 3,
})
```

## Handling jobs

Register a handler per `event_type` in the scaffolded `jobHandlers` map in
`server.go`:

```go
jobHandlers := map[string]func(context.Context, jobqueue.JobQueue) error{
    "report.build": func(ctx context.Context, job jobqueue.JobQueue) error {
        return buildReport(ctx, job.Payload)
    },
}
```

A handler error counts as a failed attempt: the runner retries with
exponential backoff and dead-letters the job after `MaxRetries`.

## Recurring jobs

Declare schedules in the composition root — `EnsureSchedule` upserts by
unique name and is idempotent at boot (concurrent instances race safely):

```go
if err := jobsRepos.JobSchedule.EnsureSchedule(ctx,
    "nightly-digest",     // unique name
    "0 9 * * *",          // standard 5-field cron (UTC); @hourly etc. also work
    "digest.send",        // event_type enqueued into job_queue
    nil,                  // payload (json.RawMessage), {} when nil
); err != nil {
    return nil, fmt.Errorf("ensuring schedules: %w", err)
}
```

Operators keep runtime control: flip `enabled`, change `cron_expr`, or
edit `payload` through the `JobSchedule` repository — the scheduler reads
the table, not the code. Changing the cron in code recomputes the next
slot on the next boot; an unchanged declaration never touches the row.

### Delivery semantics

- **Exactly-once enqueue per (schedule, slot).** Claiming is a
  compare-and-set on `next_run_at`, so with N instances exactly one wins
  each slot — no leader election. The fired job id is deterministic
  (`sched_<schedule_id>_<slot-unix>`), so a crash between claim and
  enqueue refires into the queue's unique key and is swallowed.
- **Missed slots fire once.** The next run is computed from *now*, not
  from the missed slot: a 3-hour outage on an hourly schedule produces
  one catch-up job, not three.
- **Due-ness uses the database clock** (`next_run_at <= now()`), so
  instance clock skew cannot double-fire or skip.
- **Poison schedules are skipped, not wedging.** An invalid `cron_expr`
  (only possible via direct DB edits) logs an error and the tick moves on.

### Scheduler engine

The engine lives in the framework (`core/jobs/scheduler`) and binds to
your project's types the same way `workers.Runner` does — through a
generic row contract:

```go
engine := scheduler.New(jobsRepos.JobSchedule, jobsRepos.EnqueueScheduled, log)
pool := workers.NewPool(engine.WorkFunc(), opts, workers.WithWorkerCount(1))
```

`jobschedules.Repository` satisfies the claim-path port and
`jobs.Repositories.EnqueueScheduled` adapts the queue, both shipped in
your project's `core/repositories/jobs/` tree. One scheduler worker per
instance is enough — more only add poll traffic.

## Configuration

Both pools read `sdk/workers.Options` env tags under their own prefixes:

```
APP_JOBS_WORKER_COUNT=5
APP_JOBS_WORKER_POLL_INTERVAL=5s
APP_JOBS_WORKER_IDLE_INTERVAL=30s
APP_SCHEDULER_WORKER_POLL_INTERVAL=15s
APP_SCHEDULER_WORKER_IDLE_INTERVAL=60s
```

Cron granularity is minutes, so the scheduler's poll interval only
affects firing latency within a minute.

See [SDK — Workers](../sdk/workers.md) for the pool/runner machinery and
wake-channel latency hints.
