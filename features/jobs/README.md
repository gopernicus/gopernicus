# features/jobs — durable background jobs + cron/interval schedules

A pluggable, datastore-free jobs feature: a durable queue (enqueue with
idempotency, atomic lease-based claim, retry, dead-letter) and recurring
schedules (cron or fixed interval) fired exactly once per slot across any
number of runtime instances — no leader election, just a value-CAS and a
deterministic idempotency key. Built on `sdk/workers` (the pool/runner
facility). Design of record: `.claude/plans/roadmap/jobs-feature-design.md`.

## Layout (the trio — see `features/README.md` §2 for the contract)

```
jobs.go                  the socket: Repositories, Config, CronParser/
                         CronSchedule ports, Service, NewService, Runtime,
                         NewRuntime, Register
domain/                  the hexagon's public rim — entities + ports
  job/                   Job, Enqueue, QueueRepository (structurally
                         satisfies sdk/workers.JobStore[job.Job])
  schedule/              Schedule, Spec, Ensure, Repository
internal/
  logic/queuesvc/        enqueue validation, idempotency, wake signaling
  logic/schedulesvc/     the fire engine (ListDue → ClaimDue CAS →
                         deterministic ID → enqueue → SetLastJob)
  logic/runtime/         pool assembly over sdk/workers
memstore/                PUBLIC in-core reference stores (mutex-backed) —
                         backs the conformance suite AND zero-infra hosts
storetest/               executable spec for the two ports (RunQueue +
                         RunSchedules; stores construct WithLease(storetest.Lease))
stores/turso/            the outbound tier: per-dialect SQL + migrations
stores/pgx/              (source "jobs"), each its own module
```

(No `internal/inbound/` in v1 — jobs registers **no routes**; the
namespace **`/jobs/*` is claimed by documentation only** for a future v2
admin surface, per ratified decision J5.)

## The contracts (port doc comments are the spec; `storetest` executes them)

- **Claim** atomically hands one due job to one worker: "due" = `pending`
  with `scheduled_for <= now` **or** `running` with an expired **lease**
  (stale-claim recovery is folded into Claim — a crashed worker's job
  self-heals; lease is store configuration, `WithLease`, default 15m).
  Empty → `workers.ErrNoWork`. Selection: priority DESC, created_at, id.
- **Handlers are at-least-once — write them idempotent-preferred.** A
  reclaimed job re-runs; that is the standard contract of every
  claim-based queue.
- **Fail** requeues below `MaxAttempts`, dead-letters at it. Duplicate
  enqueue IDs → `errs.ErrAlreadyExists` (the scheduler's dedup key).
- **ClaimDue** is a pure value-CAS on `next_run_at`: N runtimes race, one
  wins, losers stay silent; the deterministic job ID
  `sched_<scheduleID>_<slotUnix>` + idempotent enqueue collapse
  crash-window refires. Missed windows fire ONCE (next advances from now).

## Config — nil semantics (charter item 12)

| field | nil/zero means | notes |
|---|---|---|
| `Repositories.Schedules` | queue-only host; Runtime skips the scheduler | — |
| `Config.Handlers` | Runtime construction errors (a runtime with nothing to run is misconfiguration) | required for `NewRuntime` |
| `Config.Cron` | fine until a `Spec.Cron` schedule appears — then a loud error | `Spec.Every` is the parser-free stdlib path |
| `Config.Workers` / `PollInterval` / `IdleInterval` / `MaxAttempts` / `ScheduleBatch` | sensible defaults (4 / defaults / 3 / 20) | — |

`CronSchedule` is a **type alias** (single-method interface), so any
parser whose `Parse` returns `interface{ Next(time.Time) time.Time }`
wires directly — `Cron: robfigcron.New()` needs no adapter
(`integrations/scheduling/robfig-cron`). Cron evaluation is UTC by
contract.

## Service / Runtime — the dual entry and the wake wiring

`NewService(repos, cfg)` is the enqueue/schedule API; `NewRuntime(svc)`
takes the BUILT Service so the two share one wake channel **by
construction** — `Enqueue` signals the pool and a fresh job runs promptly
instead of waiting out a poll interval. The host owns the run loop
(`Register` starts no goroutines): `go rt.Run(ctx)` in-process, or a
dedicated worker binary where the poll interval is the cross-process
backstop. Cancel the ctx to drain gracefully — in-flight handlers finish
and persist.

`Service.Enqueue(ctx, kind string, payload json.RawMessage) (string, error)`
is deliberately **stdlib-typed** — a compatibility contract so another
feature's own narrow enqueuer port matches it structurally with zero
imports of this module (constitution rule 6).

## Datastores — {turso, postgres} out of the box, or none at all

Both dialect stores ship and pass one `storetest` suite (live runs
recorded in NOTES.md: turso against the playground incl. the
concurrent-claim case; postgres on docker where `FOR UPDATE SKIP LOCKED`
makes contention trivial). A host may instead use `features/jobs/memstore`
(public, in-core — `examples/jobs-minimal` is the zero-infra proof) or
implement the two ports itself. Postgres conformance:
`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`
+ `POSTGRES_TEST_DSN=... go test ./...`; turso: `-tags=integration` +
`TURSO_*`.

See `examples/jobs-minimal` for the full worked host, including the
real-interaction protocol that proves the wake wiring, retry/dead-letter,
schedule determinism, and graceful drain.
