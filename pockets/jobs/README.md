# pockets/jobs — durable background jobs + cron/interval schedules

A pluggable, datastore-free jobs pocket: a durable queue (enqueue with
idempotency, atomic lease-based claim, retry, dead-letter) and recurring
schedules (cron or fixed interval) coordinated across runtime instances through
atomic admission of pending occurrences and stable job IDs. Pending work retries
after queue failures or scheduler restarts, including with separate stores.
Built on `sdk/pkg/workers` (the pool/runner
facility). Design of record: `.claude/plans/roadmap/jobs-pocket-design.md`.

## Layout

```
jobs.go                 optional New / Components assembly
logic/queue/            Job and queue ports, Service admission/checkpoint,
                        ordinary Runtime and FencedRuntime, middleware and tests
logic/schedules/        recurrence vocabulary/ports, Service, occurrence recovery
stores/memory/          usable stdlib-only queue and schedule stores
stores/storetest/       shared queue/schedule conformance tests
stores/{pgx,turso}/     independent driver modules and migrations
```

Hosts may use either logic package directly. Root assembly returns named `Queue`
and optional `Schedules` services; it does not repeat their methods. Runtime and
queue state stay private. There is no mandatory `domain` or `internal` layer,
HTTP adapter, or placeholder `Register` method. Jobs starts no work until the
host runs a runtime.

Memory and conformance remain packages in the jobs module. Driver adapters keep
separate modules; importing jobs does not import a driver.

## The contracts (port doc comments are the spec; `storetest` executes them)

- **Claim** atomically hands one due job to one worker: "due" = `pending`
  with `scheduled_for <= now` **or** `running` with an expired **lease**
  (stale-claim recovery is folded into Claim — a crashed worker's job
  self-heals; lease is store configuration, `WithLease`, default 15m).
  Empty → `workers.ErrNoWork`. Selection: priority DESC, created_at, id.
  **Claim takes the caller's `kinds`** (#37): both the pending arm and the
  stale-reclaim arm select only among those kinds; `nil` is unfiltered. A
  runtime always passes the sorted key set of its `Handlers`, so it never
  claims — or spends an attempt on — a job it cannot process.
- **Handlers are at-least-once — write them idempotent-preferred.** A
  reclaimed job re-runs; that is the standard contract of every
  claim-based queue.
- **Fail** requeues below the job's persisted `MaxAttempts`, dead-letters at it.
  Jobs without a positive stored limit use the runtime's configured default.
  Permanent rejection overrides either ceiling. Ordinary retries are immediately
  eligible; use the fenced runtime for configurable retry backoff.
  Duplicate enqueue IDs → `sdk.ErrAlreadyExists` (the scheduler's dedup key).
- **Terminal states stay terminal.** Repeating Complete on completed work or
  Fail on dead-lettered work leaves it unchanged; the opposite transition
  returns `sdk.ErrConflict`. The ordinary queue still cannot distinguish a
  stale worker from the current worker while the job is running; use the fenced
  queue when per-claim ownership is required.
- **Memory values are snapshots.** Enqueue/Replace/Checkpoint own payload copies;
  returned jobs and list items own separate payloads and timestamp pointers.
  Editing a handler's job cannot alter queued state or bypass a checkpoint fence.
- **Ordinary payloads are JSON.** The service rejects invalid JSON with
  `sdk.ErrInvalidInput`, normalizes nil/empty input to `{}`, and preserves valid
  nonempty bytes. Fenced payloads remain opaque bytes.
- **ListDue takes `kinds` too**, applied inside the query before the
  limit, so a runtime lists only the schedules it can fire.
- **ClaimDue** checks the expected ID, slot, kind and spec, advances the schedule
  and persists an immutable occurrence in one transaction. The current payload
  and tenant are captured at claim. One pending occurrence per schedule prevents
  overlapping advancement while delivery is unresolved.
- **ListPending → enqueue → AckOccurrence** retries admitted work with its stable
  JobID. Acknowledgement atomically records LastRunAt/LastJobID and removes the
  pending occurrence; repeated acknowledgements cannot overwrite newer metadata.
  Edits, disabling and deletion stop future admission but do not cancel already
  admitted occurrences. Pending kind filters use the admitted kind.
- **Recurrence** uses whole-second Every intervals >= 1s, or a cron parser that
  returns a nonzero time strictly after now. Missed windows coalesce to one
  occurrence; the next time is computed from now. IDs include fractional slot
  precision, even when a clock or parser returns a fractional timestamp.


## Public construction and optionality

```go
components, err := jobs.New(jobs.Repositories{
    Queue: queueStore,
    Schedules: scheduleStore, // optional
}, jobs.WithMaxAttempts(3), jobs.WithCronParser(cronParser),
    jobs.WithScheduleBatchSize(20))
if err != nil { return err }

runtime, err := queue.NewRuntime(components.Queue,
    map[string]queue.HandlerFunc{"report": processReport},
    queue.WithScheduler(components.Schedules),
    queue.WithRuntimePolicy(queue.RuntimePolicy{Workers: 4}))
if err != nil { return err }
return runtime.Run(ctx)
```

`queue.NewService(queue.Repositories{Queue: store})` builds the
same real admission service directly. `schedules.NewService(store, schedules.WithEnqueuer(queueService),
schedules.WithCronParser(parser))` builds its occurrence
engine directly. A host can also supply its own narrow Enqueuer or the runtime's
Scheduler interface. No root facade is required for either extension.

| Configuration | Nil/zero behavior |
|---|---|
| `Repositories.Queue` / `FencedQueue` | at least one must be supplied; each enables its own protocol |
| `Repositories.Schedules` | nil omits scheduling; it also requires the ordinary queue |
| `jobs.WithMaxAttempts` | ordinary admission defaults to 3 attempts |
| `NewRuntime handlers` | runtime construction requires valid nonempty entries |
| `queue.RuntimePolicy.Workers` | defaults to 4 |
| `queue.WithRuntimeWorkerMiddleware` / `queue.WithRuntimeJobMiddleware` | nil adds no wrappers; first wrapper is outermost |
| `PollInterval` / `IdleInterval` / `Heartbeat` | SDK pool cadence defaults; heartbeat disabled |
| `jobs.WithCronParser` / `schedules.WithCronParser` | fixed intervals work; cron expressions return `schedules.ErrCronRequired` |
| `jobs.WithScheduleBatchSize` / `schedules.WithBatchSize` | defaults to 20 rows for each due-schedule and pending-occurrence query |
| `schedules.WithEnqueuer` | nil permits schedule management; running WorkFunc returns invalid input |

**Staged wiring and snapshots.** Build the admission service before constructing
services its handlers need. Fill the handler map, then pass it to
`queue.NewRuntime(queueService, handlers, queue.WithScheduler(scheduleService))`. Each runtime validates
and copies its handler registry and kind set; later edits cannot change that
runtime. Do not mutate the input map or middleware slices concurrently with
construction. Captured callback state remains host-owned.

**Wake ownership.** The ordinary queue service and each runtime built from it
share the same buffered wake signal by construction. A successful enqueue signals
it; no host wiring can accidentally substitute a different channel.

**Shutdown.** Canceling the ordinary runtime stops new polling iterations and
allows active handlers and persistence to finish. The runtime joins queue and
scheduler errors and cancels a sibling pool after a fatal failure. Hosts must
bound handler execution and store I/O; an unbounded in-flight call can prevent
draining. Fenced runtime cancellation instead cancels active processing and
leaves the lease reclaimable.

**Kind ownership.** Both queue claims and schedule processing use the sorted
registered handler kinds. Work for another binary remains pending without
spending attempts. Runtime construction logs those kinds. Monitor old pending
work: a deployment that never registers an admitted kind will leave it waiting.

## The fenced delivery surface — the hardened queue (AV3D)

Keyed admissions preserve generation order even if a writer's clock repeats or
moves backward. Memory/Turso advance creation timestamps by at least a nanosecond
past the latest stored generation when needed; PostgreSQL uses its microsecond
precision. This happens under each store's existing admission lock/transaction,
including prior terminal rows. New jobs remain claimable according to
`ScheduledFor`; lease/terminal times still use their ordinary clocks. Adjusted
`CreatedAt` and initial `UpdatedAt` preserve order and may be ahead of wall time.
Existing rows are not rewritten. Upgrade all SQL writers to get this guarantee.

A second, opt-in queue substrate (`Repositories.FencedQueue`) hardens the basic
queue for a consuming pocket that needs durable, at-least-once, replaceable work
with a claim-fenced payload checkpoint — authentication's durable delivery is the
first consumer. It adds what the basic queue could not safely provide:

1. **Lease fencing.** `Complete`/`Fail`/`Checkpoint` are fenced by the lease that
   owns the claim — a stale or superseded worker fails with `sdk.ErrConflict`
   rather than clobbering the execution another worker has reclaimed.
2. **Logical-key admission + supersession.** A PII-free logical key (distinct from
   the unique execution ID) drives atomic **enqueue-once** and **replace** — a
   repeated start coalesces onto one active execution; an explicit resend supersedes
   older active work under the same key.
3. **Claimed-payload checkpoint.** A worker persists its rendered payload
   atomically before the side effect, so every retry replays the byte-identical
   payload (this is what lets auth resend the same rendered secret).
4. **Bounded retry + terminal callback + purge.** Capped exponential backoff, a
   `Permanent(reason)` disposition that dead-letters on the first attempt, a
   per-kind `DeadLetterFunc` fired **only after** the dead-letter transition is
   durably recorded — with `j.FailureReason` populated with the recorded terminal
   reason — and a bounded `Service.PurgeTerminal(ctx, before, limit)`.

Fenced admission rejects an empty kind before changing stored work. A failed
replacement, including a duplicate execution ID, preserves the existing active
generation and its lease. Terminal hooks are best effort: a crash after Fail or
a hook error does not retry the hook. Hosts needing durable cleanup must make
that work recoverable separately.

The consumer-facing seam is the **canonical keyed-work protocol**
(`sdk/capabilities/work`): the jobs `Service` is its **implementation of record**,
satisfying `work.Enqueuer` (`EnqueueOnce`), `work.Replacer` (`Replace`), and
`work.StatusReader` (`LatestStatusByKey` — lifecycle status only, never
payload/secret) by compile-time assertion. A consuming pocket depends on the sdk
`work` ports, never on this module, so pockets never import pockets. Payload is
opaque `[]byte`. Protocol keys must be nonempty (`sdk.ErrInvalidInput` otherwise),
and deduplication is across kinds under that key. The richer `EnqueueOnceIn` /
`ReplaceIn` methods retain their optional-key semantics. The Service deep-copies it with a central `bytes.Clone` at the
protocol boundary — so an admitted unit's bytes are a store-independent snapshot
(a later caller mutation cannot alter admitted work, for every backing store, by
construction; `worktest` pins it under `-race`). The executor-side
`Service.Checkpoint` is out of the protocol (D3):
a consuming processor redeclares it structurally. The domain-rich
`job.FencedQueueRepository` and the host-registered `DeadLetterFunc`/`FencedRuntime`
handlers carry `queue.Job` because they are host-side wiring, not the cross-pocket
seam.

`queue.NewFencedRuntime(components.Queue, handlers, queue.WithFencedRuntimePolicy(policy))` builds the lease-fenced pool
that claims due jobs, hands each handler a checkpoint-capable `FencedClaim`, and
applies the retry/dead-letter policy. Like `Runtime`, it starts nothing —
Construction starts no goroutine. The host runs `rt.Run(ctx)` and handles its error.
Cancellation reaches processing; the runner waits for it to return and leaves
the claim recoverable, without recording a shutdown failure. This differs from
the ordinary runtime's detached drain.
`ProcessTimeout` must be shorter than `LeaseFor` (`ErrProcessTimeoutExceedsLease`),
with additional margin for Claim latency and persistence. Timeouts require
cooperative middleware/handlers; they cannot stop a function that ignores context.
It filters by kind exactly as `Runtime` does: the fenced `Claim` takes the
runtime's registered `kinds`, on both the pending and expired-lease arms.

## Worker middleware and job middleware

SDK workers remain usable with a custom queue and no jobs dependency. Jobs exposes
the same two boundaries through runtime configuration:

| Runtime | Before claim / around the iteration | After claim / around the handler |
|---|---|---|
| Ordinary | `queue.WithRuntimeWorkerMiddleware(middleware...)` | `queue.WithRuntimeJobMiddleware(middleware...)` |
| Fenced | `queue.WithFencedWorkerMiddleware(middleware...)` | `queue.WithFencedJobMiddleware(middleware...)` |

Worker middleware applies to the queue pool; it does not pause the separate
scheduler. Hosts still decide whether to wire scheduling. A worker gate returns
`workers.ErrNoWork` to avoid claiming. Execution-specific deadlines belong in
job middleware; ordinary runtime drain detaches cancellation inside worker
middleware. Existing `HandlerFunc` wrappers continue to work.

For example, a fenced job gate can inspect the claim's host-defined tenant:

```go
func tenantGate(next workers.ProcessFunc[queue.FencedClaim]) workers.ProcessFunc[queue.FencedClaim] {
    return func(ctx context.Context, claim queue.FencedClaim) error {
        if tenantPaused(claim.TenantID) {
            return workers.DeferUntil(time.Now().Add(time.Minute), "tenant paused")
        }
        return next(ctx, claim)
    }
}

// Pass queue.WithFencedJobMiddleware(tenantGate) to queue.NewFencedRuntime.
```

The first wrapper is outermost; shared state must support concurrent calls.
`DeferUntil` must name a future time. It releases the claim and schedules another
attempt without spending the failure budget. `workers.Reject(reason)` (also
`queue.Permanent(reason)`) dead-letters immediately. Other errors follow normal
retry policy. Returning nil declares success, including an intentional no-op
such as unchanged source content; it must not mean "wait and try later."

Memory, pgx and Turso queues implement optional `job.QueueDeferrer` /
`job.FencedQueueDeferrer`. Custom repositories can retain the base interfaces;
requesting deferral without implementing the optional port returns
`workers.ErrDeferralUnsupported` and leaves the claim for recovery. Fenced
deferral atomically validates the live lease and refunds only the current claim's
attempt, retaining previous retries and checkpoint bytes. Ordinary deferral has
no lease token to protect against a stale worker. Middleware after `next` runs
before persistence; a `DeadLetterFunc` remains a separate post-persistence hook.

## Datastores — {turso, postgres} out of the box, or none at all

Both dialect stores ship and pass one `storetest` suite (live runs
recorded in NOTES.md: turso against the playground incl. the
concurrent-claim case; postgres on docker where `FOR UPDATE SKIP LOCKED`
makes contention trivial). The canonical migration set is four files per
dialect with identical filename sets: `0001_job_queue`, `0002_job_schedules`,
`0003_fenced_job_queue`, and `0004_job_schedule_occurrences`. A host may instead use `pockets/jobs/stores/memory`
(public, in-core — `examples/jobs-minimal` is the zero-infra proof) or
implement the two ports itself. Postgres conformance:
`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`
+ `POSTGRES_TEST_DSN=... go test ./...`; turso: `-tags=integration` +
`TURSO_*`.

See `examples/jobs-minimal` for the full worked host, including the
real-interaction protocol that proves the wake wiring, retry/dead-letter,
schedule determinism, and graceful drain.

## Pending dispatch and operations

Schedule delivery is at least once. An enqueue can commit before its response is
lost; the next attempt uses the same ID. Preserve ordinary queue IDs through the
retry window and keep handler effects idempotent. Pending occurrences work with
separate stores without a generic transaction manager. Memory stores model the
protocol but do not survive process loss.

Inspect `Schedules.ListPending(ctx, limit, kinds)` for unresolved dispatch,
including occurrences whose schedule was removed or changed kind. `Queue.List`
exposes kind/status filtering; fenced inspection uses known IDs/keys or host SQL.
Compare oldest pending work with the deployed handler inventory and worker
heartbeats. Tenant-scoped inspection is host-owned. Unknown kinds
remain waiting intentionally; the framework cannot know whether a host plans to
add that handler. Hosts own alert thresholds and operator authorization.

A tick records attempts before delivery and orders pending work by attempt count,
then slot/ID. This rotates failed deliveries so healthy later work is attempted,
even with a fixed clock. Joined failures return to the worker. Invalid legacy
recurrences or a missing cron parser fail before admission and can still occupy
the due batch: disable/fix those schedules or restore the parser. Hosts own
operator repair and dead-letter policy; no framework admin UI is added.

Apply migration 0004 before new scheduler writers and stop old scheduler writers
before enabling the new protocol. Existing schedules require no backfill; past
lost occurrences cannot be recovered automatically. Drain pending work before
rolling back to an older scheduler. See root AUDIT.md for custom-store migration.
