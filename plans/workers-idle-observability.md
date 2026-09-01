# workers: idle observability — Debug no-work line + opt-in pool heartbeat (#32)

## Context

An idle `sdk/foundation/workers` pool is completely silent. The runner narrates
work richly at INFO, and the pool logs start/stop — but an empty claim
(`ErrNoWork`) is swallowed inside `Pool.worker` with no log at any level, and
the `Stats` the pool already tracks print exactly once, at shutdown. Between
boot and the first job, a workless worker process emits nothing, and an
operator cannot tell "alive and polling" from "wedged" without killing it.
The host cannot fix this on its side: the pool owns the loop, and the consumer
never sees an empty iteration happen. Found by gps-360-go's `cmd/workers/io`
(its plans/40): boot prints init + four `worker started` lines, then — with no
credential and no queued jobs — nothing, forever.

Issue #32 asks for exactly two additions: a Debug line per no-work iteration,
which leaves INFO-and-higher production output unchanged, and an opt-in
heartbeat (`WithHeartbeat(interval)`, zero = off) — one INFO line per interval
snapshotting iterations / claims / errors since the last beat, so a MISSING
heartbeat is the alarm rather than silence being the steady state.
Per-iteration INFO logging is explicitly NOT the ask.

## Goal

`sdk/foundation/workers.Pool` gains the Debug no-work line and
`WithHeartbeat`, green under the sdk suite (`-race` on the workers package)
and `make guard`, tagged `sdk/v0.7.1` (patch, narrowly scoped optional
observability; current tag `sdk/v0.7.0`). Issue #32 closes; gps-360-go adopts
on its repin (owner, downstream).

## Out of scope

- **No per-iteration INFO logging** (the issue's explicit non-ask — four
  workers polling every 5–30s would drown the lines that matter).
- No Runner (`runner.go`) or fenced (`fenced.go`) changes — the gap is the
  pool loop's, and the runner already narrates actual work.
- No metrics export (expvar/Prometheus/OTel) — this is logs-only; a metrics
  seam is a different issue if a host ever files it.
- No parity change to `sdk/foundation/async`'s pool — different facility,
  no idle-polling loop, not named by #32.
- No cadence changes: poll/idle intervals, wake-channel semantics, drain
  contract all untouched.
- No host-side or example-host adoption in this train (gps-360-go adopts
  downstream on repin).

## Schema / datastore impact

**None.** No SQL, no migrations, no store ports.

## Module / API impact

One module, `sdk`, additive option plus opt-in behavior. No new imports beyond
what `pool.go` already uses (`time`, `log/slog`), no `go.mod` changes, no new
boundary — **no new guard is owed**.

New exported symbols (`sdk/foundation/workers`):

- `func WithHeartbeat(interval time.Duration) PoolOption` — ≤ 0 disables
  (the default; today's behavior).

`Stats` does **not** grow a field. An exported struct field would break external
positional composite literals and would change the existing shutdown log even
with no option enabled. The heartbeat's claims counter stays private to the
pool; `async` has its own unrelated stats implementation.

Release: tag `sdk/v0.7.1` (patch: zero-preserving optional host configuration,
per `RELEASING.md`). Add the required chronology entry and sdk upgrade note to
`RELEASING.md` before tagging. Poll the
module proxy `.info` URL before any downstream `go mod tidy` (2026-08-27
lesson). `sdk/go.mod` must show zero diff at the end — tripwire that the
change stayed dependency-free.

## Generated-artifact impact

None. No `.templ` sources; pure Go in `sdk/foundation/workers`.

## Design decisions (settled here, implementer does not relitigate)

1. **Debug no-work line — exactly the issue's shape.** In `Pool.worker`, in
   the existing `errors.Is(err, ErrNoWork)` branch that switches to the idle
   interval:
   `p.log.DebugContext(ctx, "iteration: no work", "pool", p.name, "worker_id", workerID)`.
   Free in production; `LOG_LEVEL=DEBUG` turns a worker chatty exactly when
   someone is staring at it. Fires whether or not the heartbeat is enabled.

2. **"Claims" is an explicit private counter, never derived from separately
   sampled atomics.** `stats` gains a private `claims` atomic incremented when
   `WorkFunc` returns `nil`. That is the pool-level definition: a successful
   work iteration. For `Runner.WorkFunc`, the package's intended adapter, nil
   means a job was claimed and handled; claim-store errors return non-nil and
   therefore do not become phantom claims. `ErrNoWork`, ordinary errors,
   shutdown sentinels, and recovered panics do not increment it. The heartbeat
   logs `Δclaims` directly. This avoids both the semantic bug where
   `iterations - noWork` counts claim-store failures as claims and the
   cross-atomic snapshot bug that can produce a phantom positive claim followed
   by a negative delta. The counter remains internal: issue #32 does not require
   a public `Stats` change. Recovered panics already count into `Errors` via
   `runOnce`'s returned error, so the heartbeat's `errors` delta covers panics;
   no separate attr.

3. **`WithHeartbeat(interval)` semantics:** `interval <= 0` = off, the
   default — unlike `WithPollInterval`/`WithIdleInterval` there is no
   fallback-to-default clamp, because off IS the default and zero must keep
   meaning "no heartbeat" for existing hosts. No minimum clamp; the doc
   comment notes a sub-second interval is operator noise, not a guardrail.

4. **One heartbeat goroutine per pool, INFO, deltas.** Started by `Run` only
   when enabled; one line per interval:
   `p.log.InfoContext(ctx, "pool alive", "pool", p.name, "active_workers", …, "iterations", Δ, "claims", Δ, "errors", Δ)`
   — deltas since the last beat (the goroutine keeps its previous `Stats`
   snapshot plus the private claims value locally; single reader, no extra
   synchronization). `Run` captures that baseline synchronously before it
   launches workers or the heartbeat goroutine, then passes it into the loop;
   goroutine scheduling therefore cannot erase startup work from the first
   beat. Individual counters are monotonic but not a transactional tuple, so an
   iteration completing exactly on a tick may fall on either adjacent beat;
   no delta can go negative. It beats even when every delta is zero — an
   all-zero beat from an idle pool is the entire point. First beat lands one
   full interval after start (the
   `worker pool starting` line already covers boot); existing cumulative
   totals stay where they are today: `Stats()` and the shutdown line. The
   issue asks only for claims deltas, so the private claims total is not added
   to either public surface.

5. **Lifecycle — the heartbeat does NOT join `p.wg`.** Trap: if every worker
   exits via `ErrWorkerShutdown`, the derived ctx is never cancelled — a
   heartbeat parked in `p.wg` would tick forever and `Run` would deadlock on
   `Wait`. Ordering in `Run`: start heartbeat with its own done channel →
   `p.wg.Wait()` (workers) → `cancel()` → receive on the heartbeat's done
   channel → `close(p.errors)` → `worker pool stopped` log. Regression-tested
   (task-2).

6. **The heartbeat stops at ctx-done and does not beat during drain.** A
   canceled context and a ticker can be ready simultaneously, so the ticker
   arm must check `ctx.Err()` and return before logging, mirroring the worker
   loop's cancellation-precedence check. In-flight iterations finishing after
   cancellation are narrated by the runner and summarized by the shutdown
   stats line; a drain-time beat adds nothing. `Run`'s doc comment does not
   change — the drain contract is untouched.

## Risks

1. **Run-deadlock trap under all-workers-`ErrWorkerShutdown`** — the reason
   for decision 5; closed by an explicit regression test, not by review
   alone.
2. **Log-assertion races in tests** — `slog` handlers must be
   goroutine-safe; the tests use one mutex-guarded capturing handler (records
   appended under lock, read after `Run` returns), not a bare
   `bytes.Buffer`. The workers package runs under `-race`.
3. **Timing-sensitive heartbeat tests** — assert "at least one beat" with a
   generous interval-to-runtime ratio (e.g. 20ms beat, ~150ms run bounded by
   ctx timeout); never assert an exact beat count.
4. **Heartbeat snapshot semantics** — counters are independent atomics, so a
   completion concurrent with a tick may be reported in either adjacent beat.
   Tests assert monotonic/non-negative deltas and totals over the captured
   records, not an exact per-tick partition.

## Tasks

### task-1: Debug no-work line

- **depends_on:** []
- **model:** opus
- **files:** [sdk/foundation/workers/pool.go, sdk/foundation/workers/pool_test.go]
- **verify:** `cd sdk && go build ./... && go vet ./... && go test -race ./foundation/workers/... && go test ./...`
- **description:** Emit the decision-1 `DebugContext` line in `Pool.worker`'s
  existing `ErrNoWork` branch. Tests: a mutex-guarded capturing slog handler
  (added to pool_test.go beside `silentLogger`, enabled at Debug and retaining
  `slog.Record.Clone()` values); an always-`ErrNoWork` WorkFunc run briefly
  emits ≥1 Debug `"iteration: no work"` record carrying `pool` and
  `worker_id` attrs; a nil-returning WorkFunc emits no no-work record.

### task-2: WithHeartbeat option + heartbeat goroutine

- **depends_on:** [task-1]
- **model:** opus
- **files:** [sdk/foundation/workers/pool.go, sdk/foundation/workers/stats.go, sdk/foundation/workers/pool_test.go]
- **verify:** `cd sdk && go build ./... && go vet ./... && go test -race ./foundation/workers/... && go test ./...`
- **description:** Add `WithHeartbeat` (decision 3, doc comment carrying the
  zero-is-off contract and the missing-heartbeat-is-the-alarm framing),
  `poolConfig.heartbeat` + `Pool.heartbeat` fields, the private
  `stats.claims` atomic, and the per-pool heartbeat goroutine per decisions
  2–6, wired into `Run` with the exact decision-5 ordering and a baseline
  captured synchronously before goroutine launch. Tests: default and
  `WithHeartbeat(0)` emit no
  `"pool alive"` record; `WithHeartbeat(20ms)` over an always-`ErrNoWork`
  pool emits ≥1 INFO `"pool alive"`, **every** captured beat has zero
  `claims`, and the
  `pool`/`active_workers`/`iterations`/`claims`/`errors` attrs present; a
  WorkFunc returning ordinary errors yields zero claims and positive errors;
  a WorkFunc that succeeds exactly once before returning `ErrNoWork` proves
  the synchronously captured first-beat baseline by yielding total captured
  `claims == 1`; all numeric deltas are non-negative. **The decision-5
  regression** — every worker returns `ErrWorkerShutdown` with the heartbeat
  enabled and `Run` still returns promptly (bound with a test deadline);
  **the decision-6 regression** — block an in-flight WorkFunc, wait for one
  heartbeat record, cancel immediately while the next tick is still a full
  interval away, hold the drain open for several heartbeat intervals, and
  assert the heartbeat count does not advance; then release the work and assert
  `Run` returns cleanly, the heartbeat has stopped, and the shutdown log remains
  last.

### task-3: Docs touch-up + full sweep + release record

- **depends_on:** [task-2]
- **model:** opus
- **files:** [sdk/README.md, RELEASING.md, .claude/plans/workers-idle-observability.md]
- **verify:** `make check && make guard`
- **description:** Append the heartbeat to `sdk/README.md`'s `workers` table
  row (a few words: "opt-in idle heartbeat"; the row is already dense —
  surgical). `workshop/documentation/docs/sdk/foundation.md` stays untouched
  (it documents no per-option detail — verified). Run the full cross-module
  sweep and guards (expected: zero guard changes — no new boundary). Confirm
  `sdk/go.mod` shows zero diff. Before tagging, add the required
  `sdk/v0.7.1` chronology entry and sdk upgrade note to `RELEASING.md`, calling
  out the Debug-level behavior and the opt-in/default-off heartbeat. After the
  release commit is merged, create and push annotated tag `sdk/v0.7.1`, poll
  `https://proxy.golang.org/github.com/gopernicus/gopernicus/sdk/@v/v0.7.1.info`
  until it resolves, then close #32. Append an execution-status note to this
  plan with the merge commit, tag, proxy result, issue closure, and
  gps-360-go adoption left downstream/owner. Never record "merged" or
  "tagged" before those actions have occurred.

## Sequencing

A straight line: task-1 → task-2 → task-3. task-2 builds on task-1's
capturing handler and adds the private claims counter used by the heartbeat.

## Consultation notes

None pre-plan — the change is two small additive seams in one file, specified
nearly to the line by the issue; the one real subtlety found in planning (the
heartbeat/`p.wg` deadlock under all-workers-`ErrWorkerShutdown`) is settled as
decision 5 with its own regression test. Reviews below are post-plan calls.

## Open questions

None blocking. Two points settled here rather than left open: the heartbeat
logs **deltas only** (plus `active_workers`), not cumulative totals — totals
for the existing public counters remain available via `Stats()` and the
shutdown line, while claims intentionally remains heartbeat-only; and the log
attr `claims` means nil-returning WorkFunc iterations, which maps to
claimed-and-handled jobs for `Runner.WorkFunc`. Revisit the vocabulary only if
another WorkFunc adapter needs a distinct outcome model.

## Recommended reviews

- **lead-backend-engineer** — the decision-5 `Run` ordering and the
  option/stats surface.
- **platform-sre** — the heartbeat as an operational alarm seam (level,
  attrs, cadence guidance) and the v0.7.1 tagging step.

## Notes

- The heartbeat is a no-op for existing hosts because its zero value is off.
  The Debug addition is intentionally visible to existing hosts whose logger
  enables Debug; INFO-and-higher production output, the exported `Stats`
  shape, and the shutdown stats rendering remain unchanged.
- The issue's non-blocking framing holds: gps-360-go runs fine without this
  and adopts on repin; nothing here gates another train.

## Execution record (2026-09-01)

**EXECUTED, MERGED, TAGGED.** All three tasks landed via PR #33, squash
`418414f` (five files: `pool.go`, `stats.go`, `pool_test.go`, `sdk/README.md`,
`RELEASING.md`). Verify green at every step: full sdk suite, workers under
`-race` (new tests also `-race -count=5`, no flakes), `make check && make
guard` (zero guard changes — no new boundary), `sdk/go.mod` zero diff. Tag
`sdk/v0.7.1` (annotated) @ `418414f`; **cold-resolution verified on the module
proxy first poll** (`.info` returned the tag hash). Issue #32 closed (auto via
the PR's Closes link; release comment added). RELEASING.md phrasing flipped
from "next tag" to `TAGGED @ 418414f` in the post-tag docs commit, which also
carries this record and the tracked `plans/` copy. gps-360-go adoption remains
downstream/owner (adopt on repin; a host-side "alive" ticker stopgap can be
retired). Pre-existing, deliberately untouched: the `fenced_test.go` gofmt
violation (~line 420) — a separate one-line fix outside this train.
