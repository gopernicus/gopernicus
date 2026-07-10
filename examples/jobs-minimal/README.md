# examples/jobs-minimal

The zero-infra proof host for `features/jobs` (design §8). It mounts the jobs
feature backed by the in-core `features/jobs/memstore` — no datastore driver, no
migrations, no external infrastructure — and runs the jobs `Runtime` in-process
next to an HTTP server. Boot it with `go run ./cmd/server` and drive it with
`curl`.

## What it proves

- **enqueue -> wake wiring (§3.4).** `Service.Enqueue` signals the runtime's pool
  over the wake channel, so a fresh job runs **sub-second** after the enqueue
  call — not at the next poll interval.
- **retry -> dead-letter.** `demo.flaky` fails until it has been retried twice
  (two `job failed` lines, then a completion); `demo.doomed` always fails and
  exhausts `MaxAttempts` (3) into `dead_letter`.
- **schedules.** One stdlib-path `Spec.Every` 15s interval and one robfig-path
  `Spec.Cron` `* * * * *` schedule, each firing `demo.print` with a
  deterministic `sched_<scheduleID>_<slotUnix>` job ID.
- **graceful drain (§7.4).** SIGTERM cancels the shared context; the HTTP server
  and the pools drain, an in-flight `demo.slow` handler **finishes** before
  `Run` returns, and the process exits 0.

## Module graph — zero datastore drivers

Its `go.mod` requires exactly `github.com/gopernicus/gopernicus/sdk`, `github.com/gopernicus/gopernicus/features/jobs`, and
`github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron` (a CPU-only library — the bcrypt
zero-infra precedent). No libsql, no pgx:

```sh
cd examples/jobs-minimal && GOWORK=off go list -m all | grep -iE 'libsql|pgx'   # empty
```

## The protocol

Port 8083 (8081/8082 are taken by the other examples).

```sh
cd examples/jobs-minimal && go run ./cmd/server        # boots, logs pool + scheduler start

# 0. liveness probe -> 200 (host-local GET /healthz; unauthenticated, memory-backed so no DB to probe)
curl -fsS -o /dev/null -w '%{http_code}\n' localhost:8083/healthz

# 1. prompt enqueue -> the handler log line appears sub-second (proves the wake wiring)
curl -fsS -X POST localhost:8083/enqueue -d '{"kind":"demo.print","payload":{"msg":"hi"}}'

# 2. retry path -> two failures then a completion
curl -fsS -X POST localhost:8083/enqueue -d '{"kind":"demo.flaky","payload":{}}'

# 3. exhaustion -> reaches dead_letter after three failures
curl -fsS -X POST localhost:8083/enqueue -d '{"kind":"demo.doomed","payload":{}}'

# 4. wait ~90s: heartbeat-15s fires ~6x, minute-cron at each minute boundary,
#    each with a deterministic sched_ job ID.

# 5. graceful drain: enqueue a slow job then Ctrl-C immediately
curl -fsS -X POST localhost:8083/enqueue -d '{"kind":"demo.slow","payload":{}}'
# ^C  -> "demo.slow finished" still appears, then "jobs runtime drained", exit 0
```

The optional full-fidelity fields `id` (idempotency key), `priority`, and
`max_attempts` route the request through `Service.EnqueueJob` instead of
`Service.Enqueue`.

## In-memory store caveat

`memstore` is in-process: a restart clears all state. The no-double-fire property
the restart demonstrates here is that the restarted scheduler computes its next
slot from *now* and does not refire a pre-restart slot — deterministic IDs plus
fire-once catch-up. True cross-restart dedup (a crash mid-slot not double-firing
against surviving state) is only provable against a **durable** store
(`stores/turso`, `stores/pgx`), where the deterministic `sched_` ID collides
on the idempotency key.
