# Phase 1 — `sdk/workers` (pool + runner)

Status: RATIFIED (cut from design §2)
Executor model: opus
Depends on: nothing (first phase).
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §2 — the public
surface sketch there is the specification (Pool/NewPool/Run/Stats/Errors;
PoolOptions incl. `WithWakeChannel`; `WorkFunc`/`Middleware`;
`ErrNoWork`/`ErrWorkerShutdown`/`ErrPoolShutdown`; `Job`/`JobStore[T]`/
`Runner[T]` with `Claim`/`Complete`/`Fail`, hooks, `WithMaxAttempts`
default 3, `WithClock`). Salvage the DESIGN of
`gopernicus-original/sdk/workers/{pool,runner,model,middleware}.go` and
port/adapt its test suites — read them; re-type fresh, port no code
verbatim. Deviations §2 lists are ratified: no tracer hooks (J7), no
env-tag Options struct, `WorkerIDFromContext` naming, `Run` blocks and
drains on cancel.

## Work items

1. `sdk/workers` package (INSIDE the sdk module — stdlib only: sync,
   sync/atomic, time, log/slog, context, errors; sdk/go.mod stays
   require-free, guard G1 must stay green).
2. Pool: N workers calling a `WorkFunc` in a loop; `ErrNoWork` → idle
   backoff to `IdleInterval`; `WithWakeChannel(<-chan struct{})` —
   non-blocking coalesced wake (documented protocol: buffered cap-1
   channel, lost wakes tolerated, poll interval is the backstop); panic
   recovery per iteration; `Run(ctx)` blocks, drains in-flight work on
   cancel, then returns; `Stats()`; `Errors() <-chan error`.
3. Runner[T Job]: claim → pre-hooks → process-with-retry → post-hooks →
   Complete/Fail; empty claim propagates `ErrNoWork`; `WorkFunc()` adapts
   it to the pool.
4. Tests: adapted from the original's pool/runner suites — idle backoff,
   wake-channel promptness, drain-on-cancel, panic containment, retry →
   fail-at-max-attempts, hook ordering, concurrent workers claim distinct
   jobs (against an in-test fake store).

## Acceptance

```sh
cd sdk && go build ./... && go vet ./... && go test ./...
make check          # G1 (sdk stdlib-only) is the guard that matters here
```

## Real-interaction check

Standing check (a) from `00-overview.md`. (Workers has no runtime surface
of its own; the §8 proof-host protocol drives it live in phase 8.)

## Execution log

### 2026-07-02 — phase 1 executed (loop leg 15; implementer on opus)

Shipped `sdk/workers` (inside the sdk module, stdlib-only — go.mod stays
require-free, G1 green): errors.go (three sentinels), work.go
(WorkFunc/Middleware + WithWorkerID/WorkerIDFromContext on the sdk/logging
contextKey pattern), stats.go, pool.go (Pool/NewPool/Run/Stats/Errors +
all seven PoolOptions incl. the documented cap-1 coalesced wake protocol),
middleware.go (ConsecutiveErrorShutdown — the one concrete middleware
proving the seam), runner.go (Job/JobStore[T]/Runner[T] + hooks +
WithMaxAttempts default 3 + WithClock), adapted pool/runner/middleware
test suites.

Shape decisions vs the §2 sketch (all logged, consistent with ratified
J6/J7): no Stop() (ctx-cancel is the API); ErrPoolShutdown now stops the
WHOLE pool (sentinel means what it says; original only stopped one
worker); recovered panics surface on Errors() + Stats; **Runner does no
in-process backoff — durable retry lives in the store's Fail
(requeue-below-max / dead-letter-at-max) with re-claim on a later
iteration**, matching §2's flow and J6, dropping the original's in-test
sleeps; renames applied (Claim, Run, WorkerIDFromContext).

Acceptance (re-run FIRSTHAND): sdk build/vet PASS; `go test -race
./workers/` ok 2.2s (race-clean; suite covers idle backoff, wake latency,
drain-on-cancel + leak assertion, panic containment both layers,
retry→dead-letter and retry→succeed, hook ordering, 8 workers holding 8
distinct claims at peak); sdk/go.mod has ZERO requires; `make check` →
"all checks passed". Real-interaction (a): minimal :8081 → 200/200; port
free. Stale-editor diagnostics referencing pre-relayout paths disproven
by the firsthand build.

Unverified: nothing — workers is driven live by phase 8's proof host.
