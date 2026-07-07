# Phase 2 — `features/jobs` core module

Status: RATIFIED (cut from design §3; trio paths per the re-layout)
Executor model: opus
Depends on: phase 1 (`sdk/workers`).
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §3 (anatomy,
entities/ports verbatim, jobs.go surface, Register's literal v1 body,
§3.4's LOAD-BEARING Service/Runtime wake wiring), §4 (the CronParser/
CronSchedule ports declared in jobs.go), §5.2 (Enqueue's stdlib-typed
signature as a HARD compatibility contract). Precedent: `features/auth`
(trio layout, socket file, storetest conventions, README-later).

## Work items

1. Module scaffold: `gopernicus/features/jobs` requiring EXACTLY
   `gopernicus/sdk` (+ workspace replace); go.work + Makefile MODULES;
   guard G2 already covers all `features/*` (verify; prove-can-fail once).
2. `logic/job/`: `Status` consts (pending/running/completed/failed/
   dead_letter), `Job` (satisfies `workers.Job` — compile-assert),
   `Enqueue` input, `QueueRepository` (design §3.1 verbatim, incl. the
   port-contract doc comments: Claim returns `workers.ErrNoWork`, "due"
   includes lease-expired reclaims per §6.3, selection priority DESC then
   created_at; structurally satisfies `workers.JobStore[job.Job]` —
   compile-assert).
3. `logic/schedule/`: `Spec` (exactly one of Cron/Every), `Schedule`,
   `Ensure` input, `Repository` (§3.1 verbatim — Ensure upsert-by-Name,
   ListDue, ClaimDue value-CAS, SetLastJob, Get/List/SetEnabled/Delete).
4. `jobs.go` (the entire host-facing surface, one file): `Repositories`
   (Queue; Schedules nil = queue-only), `HandlerFunc`, `CronSchedule` +
   `CronParser` ports (§4 — Parse validates 5-field cron + descriptors,
   UTC by contract), `Config` (Handlers required non-empty for a Runtime;
   Cron nil OK until a Spec.Cron appears then loud error; Workers/
   PollInterval/IdleInterval/MaxAttempts/ScheduleBatch with defaults),
   `NewService`, `Service.Enqueue(ctx, kind string, payload
   json.RawMessage) (string, error)` — STDLIB TYPES ONLY, stated in the
   doc comment as a compatibility contract (§5.2), `EnqueueJob`,
   `EnsureSchedule`, `NewRuntime(svc *Service)` — takes the BUILT Service
   (§3.4: shared wake channel by construction; never (repos, cfg) again),
   `Runtime.Run(ctx)` (queue pool + optional single-worker scheduler pool;
   drains on cancel), `Register` (validates, logs, registers NO routes,
   starts NO goroutines — §3.3's literal body).
5. `internal/logic/queuesvc/` (enqueue validation, idempotency,
   non-blocking wake send), `internal/logic/schedulesvc/` (the fire
   engine: ListDue → ClaimDue CAS → deterministic `sched_<id>_<slotUnix>`
   job ID → enqueue with ErrAlreadyExists swallowed → SetLastJob; missed
   windows fire once — next_run_at advances from now),
   `internal/logic/runtime/` (pool assembly: Runner over QueueRepository
   dispatching by Kind to Handlers + the scheduler WorkFunc).
6. Tests: table-driven, stdlib-only, in-module fakes — enqueue
   idempotency; wake signaled on Enqueue; scheduler CAS win/lose paths;
   deterministic refire ID; cron-required-but-nil loud error; Every-only
   path needs no parser; Register validation errors; compile-time
   assertions for the workers seams.

## Acceptance

```sh
cd features/jobs && go build ./... && go vet ./... && go test ./...
grep -c require features/jobs/go.mod            # exactly 1 (gopernicus/sdk)
grep -rn "features/auth\|features/cms" features/jobs/   # empty (rule 6)
make check
```

## Real-interaction check

Standing check (a). (The live proof is phase 8's protocol.)

## Execution log

### 2026-07-02 — phase 2 executed (loop leg 16; implementer on opus)

Shipped `features/jobs` (14th module): jobs.go (full §3.2 surface —
Repositories, HandlerFunc, CronParser/CronSchedule with UTC contract,
Config, NewService, Enqueue with the stdlib-types-only compatibility
contract in its doc comment, EnqueueJob, EnsureSchedule with the
cron-nil loud error, NewRuntime(svc), Runtime.Run, Register per §3.3's
literal body — validates + logs, no routes, no goroutines);
logic/{job,schedule} with compile-time workers seams;
internal/logic/{queuesvc,schedulesvc,runtime}. go.work + MODULES updated.

**Wake wiring (§3.4) proven two ways:** channel-identity test
(svc and runtime share ONE cap-1 channel by construction) AND a live
test — pool idles at 30s poll interval, an Enqueue lands, the handler
fires within 3s (only the wake path can do that). Duplicate-rejected
enqueues do NOT signal.

Divergence (flagged for jrazmi, not silently absorbed): §3.1 as written
demands Job FIELDS named ID/Status/RetryCount AND methods of the same
names for the workers.Job seam — mutually exclusive in Go. Resolved per
the design's own prose ("satisfies workers.Job via ID()/Status()/
RetryCount() methods"): methods over exported backing fields
JobID/JobStatus/Retries (exported so store modules construct Jobs).
Cheap pre-v1 rename if different backing names are preferred; phases
4/5/7/8 use the new names.

G2 prove-can-fail: injected forbidden import → guard exit 2 naming the
file → removed → green.

Acceptance (re-run FIRSTHAND): build/vet + `go test -race ./...` PASS
(4 packages ok); go.mod requires exactly 1; cross-feature imports 0;
`make check` → "all checks passed" (14 modules, 4 guards).
Real-interaction (a): minimal :8081 → 200/200; port free.

Unverified: nothing for this phase — the live proof is phase 8's
protocol.
