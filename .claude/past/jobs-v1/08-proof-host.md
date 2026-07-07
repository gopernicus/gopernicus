# Phase 8 — `examples/jobs-minimal` (the zero-infra proof host)

Status: RATIFIED (cut from design §8; A1-style separate host)
Executor model: opus
Depends on: phases 2 + 3 + 4.
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §8 (the host
spec + real-interaction protocol) and §7.4 (host owns the run loop;
`Register` starts nothing; in-process topology: `go runtime.Run(ctx)` next
to the HTTP server; ctx-cancel graceful drain). Precedent:
`examples/minimal` (main.go shape, port/env conventions — pick a port that
collides with nothing; 8083 suggested since 8081/8082 are taken).

## Work items

1. Module `gopernicus/examples/jobs-minimal` (sdk + features/jobs +
   integrations/scheduling/robfig-cron — a CPU-only lib, the bcrypt
   zero-infra precedent); go.work + Makefile MODULES. Stores from
   `features/jobs/memstore` (R3).
2. `cmd/server/main.go`: two handlers — `"demo.print"` (logs payload) and
   `"demo.flaky"` (fails until RetryCount ≥ 2; a forced-exhaustion variant
   reaches dead_letter); one `Spec.Every` 15s schedule (stdlib path) + one
   `Spec.Cron` `* * * * *` schedule (robfig path); a HOST-OWNED
   `POST /enqueue` route calling `svc.Enqueue` (deliberately not a feature
   route — v1 claims none); `jobs.Register` + `NewService` + `NewRuntime`;
   `go runtime.Run(ctx)`; SIGTERM → cancel → drain.
3. README: what it proves, the protocol commands, the module-graph claim
   (zero datastore drivers).
4. Tests where the host has logic worth testing (handlers are closures —
   keep it light; the protocol below is the real proof).

## Acceptance

```sh
cd examples/jobs-minimal && go build ./... && go vet ./... && go test ./...
GOWORK=off go list -m all | grep -iE 'libsql|pgx'    # empty — no drivers
make check
```

## Real-interaction check — THE milestone gate

Standing check (a), then the FULL §8 protocol from `00-overview.md`
(boot → prompt enqueue→handler log (sub-second, proving the wake wiring) →
flaky retry → dead_letter → ~90s of schedule fires with deterministic
sched_ IDs → mid-window restart with NO double-fire → SIGTERM graceful
drain with a slow job in flight). Record exact commands, ports, and log
lines. Kill; port free.

## Execution log

### 2026-07-02 — phase 8 executed (loop leg 21; implementer on opus) — THE §8 PROTOCOL PASSES

Shipped `examples/jobs-minimal` (18th module, :8083, zero datastore
drivers — GOWORK=off graph is sdk + features/jobs + robfig-cron only):
four handlers (print/flaky/doomed/slow), Every-15s + minute-cron
schedules, host-owned POST /enqueue, `go rt.Run(ctx)` beside the HTTP
server, SIGTERM→drain→exit 0. README documents the protocol + the
in-memory caveat.

**CronSchedule ruling (phase-3 finding) — option (b) TYPE ALIAS chosen
and implemented:** one line in features/jobs/jobs.go; `Cron:
robfigcron.New()` now wires directly, zero adapter; features/jobs and
robfig-cron tests both green after. Rationale: a single-method port whose
purpose is third-party pluggability is exactly what aliases are for.

**Full §8 protocol transcript (implementer run, key evidence):**
(1) boot logs pool jobs-queue×4 + jobs-scheduler×1;
(2) enqueue→handler→completed ALL WITHIN THE SAME MILLISECOND (18:10:24.297,
req 223µs) — wake wiring proven, not poll pickup;
(3) flaky: two failures then success at retry_count=2; doomed: three
failures then silence at MaxAttempts=3 (dead_letter);
(4) 6 Every fires + 1 cron fire with deterministic sched_<id>_<slotUnix>
IDs; kill + restart → next computed from NOW, no burst catch-up, none of
run 1's seven slots refired. HONEST caveat logged: memstore wipes state on
restart so this proves deterministic IDs + fire-once catch-up, NOT true
cross-restart dedup (that needs the durable stores — README says so);
(5) demo.slow SIGTERMed ~1.2s into a 5s handler → handler ran its full
5s → "jobs runtime drained" → EXIT=0; port freed.

Loop-leg firsthand spot-run: booted :8083, enqueue → job completed within
the second (54µs handler), SIGTERM clean, port free; make check → "all
checks passed" (18 modules); minimal :8081 → 200/200.

Ergonomics flag (not fixed, logged): the Runtime pools log via
slog.Default — no Config knob for an isolated runtime logger; candidate
future Config field.

Unverified: true cross-restart schedule dedup (durable-store territory,
correctly out of an in-memory host's reach).
