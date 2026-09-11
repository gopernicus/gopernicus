# Workers audit implementation

Status: COMPLETE — 2026-09-10. The owner authorized the revised S7 fixes and
simplifications, explicitly retaining generic SDK runners and middleware.
Review: [framework-audit-work.md](framework-audit-work.md).
Parent: [framework-audit.md](framework-audit.md).

## Decisions and compatibility

- Keep async, workers Pool/generic runners/store contracts and the work protocol.
  Consumers can bring custom queues without importing jobs. SDK remains stdlib-only.
- Keep worker Middleware/WithMiddleware around an iteration. Add generic job
  processing middleware around ProcessFunc(ctx, job) error, first supplied wrapper
  outermost. Middleware sees the claimed job; the runner owns its claimed ID.
- Make gating outcomes explicit: temporary deferral releases the claim to a
  specified future time without spending a failure attempt; permanent rejection
  dead-letters immediately; ordinary errors retain retry policy. A middleware
  that returns nil declares processing successful, so a closed gate must return
  an explicit disposition. Composition happens at construction and middleware
  must support concurrent calls. Keep post-persistence dead-letter hooks distinct.
- Add optional SDK deferral ports so existing custom stores remain usable; when
  a processor requests unsupported deferral, return an explicit error and leave
  the execution recoverable. Jobs memory/pgx/turso queues implement deferral and
  expose it through their repository adapters. Fenced deferral checks live lease
  ownership and refunds only the current claim's attempt. No schema/status change.
- Narrow Job to ID; fenced execution separately needs RetryCount. Remove the
  mutable returned job value, duplicate pre/post hooks, within-claim retry and
  attempt-only fenced retry. Keep generic error-aware retry and durable policies.
- Fix async closing/admission and nil logger. Remove async presets and the
  redundant shutdown timeout option; callers own Close deadlines.
- Fix Pool logging, timer cadence, fatal returns and explicit single-use guard;
  remove the unused lossy Errors stream. Retain/repair shutdown middleware,
  stats and heartbeat (name successful iterations honestly). Jobs runtime cancels
  sibling pools when a fatal error stops either one.
- Require nonempty keys only on the consumer work protocol; ordinary generated
  IDs and full-input optional-key methods retain their behavior.
- Strengthen work conformance and Service lifecycle evidence; update current
  docs and standalone AUDIT-007/RELEASING. Preserve seven stored status strings,
  host runtime APIs and existing fields. No consumer edits, module pins or releases.

## Preconditions and ownership

Branch/base: firestore-authentication / 6807ed06; 453 pre-existing dirty paths.
Snapshot: /tmp/gopernicus-s7-implementation-baseline.json. Preserve earlier
audits/concurrent work. Go 1.26.1, no root go.mod; GOCACHE:
/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter /Users/jrazmi/go/bin/goimports.
No credentials, production services, generated-file edits, commits or tags.

## Tasks

1. **Async (named implementer):** only sdk/foundation/async source/tests. Correct
   shutdown/admission and simplify configuration with deterministic regressions.
2. **Pool (parent; named SRE read-only review):** lifecycle, timer, logging and
   worker middleware corrections; update actual jobs sibling-pool ownership.
3. **Generic execution and gates (parent; named backend read-only review):**
   narrow runner contracts, processor middleware/dispositions, durable deferral
   adapters and persistence reporting. Migrate local callers and meaningful tests.
4. **Protocol/docs (parent):** strengthen work harness and real Service tests;
   SDK/jobs/site docs, architecture, migration guide and release record.
5. **Verification:** format; affected-module build/test/vet; targeted race;
   custom SDK-only queue and actual jobs gated execution; hermetic SQL adapter
   checks where feasible; make check and docs-build. Record live-service skips.

## Implemented result and review

Q1–Q10 are addressed in this scope. Async closes admission atomically and all
Close callers share the same drain; each owns its deadline. Pool uses a timer
reset after each iteration, logs ordinary failures, guards repeated Run, and
returns its first local fatal cause after drain. Middleware counters are per
wrapper and synchronized without serializing next; control error causes/scope
survive. Generic runners preserve claimed identity, return failed persistence,
run once per claim and support job middleware inside recovery/processing timeout.
Jobs runtime exposes both middleware boundaries and cancels sibling pools on
fatal failure. Optional atomic Defer is implemented by memory, pgx and Turso with
no required additions to existing custom repository interfaces or schemas.

Named backend and SRE reviews found no remaining blocker. Backend's processing
context lifetime correction is implemented: cancel the child immediately after
processing, before persistence on the live parent. Timeout docs now say
nonpositive options are ignored. SRE's detached store-I/O lifetime correction is
in the runtime and public docs. Regression tests distinguish direct cancellation
from cancellation joined with a real error. The SDK-only example has synchronized
claim/transition state and meaningful retries; it explicitly lacks durable/lease
recovery. Whole jobs runtime tests prove defer→pending→completed without burning
attempts, including fenced checkpoint/tenant preservation. A real events Poller
failure reaches the Pool logger.

### Refreshed consumer evidence

The owner refreshed GPS360 main during this implementation. Read-only review of
clean e1ab3f0 now covers cmd/workers/{echo,delivery,comps}, host jobs stores and
inspection/retry UI. SDK is pinned v0.7.1, jobs/stores v0.5.0, without replacements;
this supersedes the old v0.4.1/local-replace snapshot. No removed SDK hooks/retry
APIs are called. Host handler timeout wrappers are a real job-middleware use but
can remain unchanged; do not move them outside detached drain into worker
middleware. Preserve kind isolation, production-only schedule wiring and nil
success for intentional unchanged-content/partial-success outcomes. Ordinary
fresh IDs and retry re-enqueue are unaffected by empty logical-key rejection.

Consumer follow-up (source-reviewed, no probe/fix): the final comps race can
convert context cancellation to a failure-summary string and return nil overall
(internal/logic/domains/comps/import.go:205–256;
internal/inbound/domains/comps/jobs.go:70). Propagate cancellation at the workflow
boundary while retaining intentional per-race partial success. No consumer files,
module pins or vendor directories were modified. Recheck the current consumer
commit when upgrading; a local source review does not prove deployed behavior.

## Verification

Commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache unless noted. Root Go
commands below use workspace-qualified package paths; module commands run inside
the named directory.

| Check | Command / result |
|---|---|
| Formatter and scoped diff | `/Users/jrazmi/go/bin/goimports -w` on changed Go files; `git diff --check -- <S7 paths>` — PASS |
| Full workspace | `make check` — initial sandbox run failed at events' local httptest listener (`bind: operation not permitted`), log `/tmp/gopernicus-s7-make-check.log`. Rerun with loopback permission PASS, `/tmp/gopernicus-s7-make-check-loopback.log`: all 42 modules vet/build/test, generated-template/asset checks, scaffold cache/test, integration/live-tag compile-only vet and guards |
| Final affected race suite | `go test -race -count=1 ./sdk/foundation/async ./sdk/foundation/workers ./sdk/capabilities/work/... ./pockets/jobs ./pockets/jobs/internal/logic/runtime ./pockets/jobs/memstore` — PASS, `/tmp/gopernicus-s7-final-race.log` |
| Actual events Poller integration | `go test -race -count=1 ./pockets/events -run TestPollerStorageFailureIsVisibleThroughWorkerPool` — PASS, 1.138s; full events HTTP tests also pass in make check |
| Repeated async lifecycle | From sdk: `go build ./foundation/async`, `go test ./foundation/async -count=1`, `go vet ./foundation/async`, `go test -race ./foundation/async -count=20` — PASS |
| Workers example/lifetime/logging | From sdk: `go build ./foundation/workers`, `go test ./foundation/workers -count=1`, `go vet ./foundation/workers` — PASS; `go test -race ./foundation/workers -run 'ExampleChainJobMiddleware|TestFencedRunnerCancelsProcessingContextBeforeCompletion|TestPoolLogsFailureJoinedWithParentCancellation' -count=20` — PASS |
| Jobs and SQL modules | From pockets/jobs and each stores/{pgx,turso}: `go build ./...`, `go test ./... -count=1`, `go vet ./...` — PASS; live suites skip without env |
| Repeated deferral conformance | From pockets/jobs: `go test -race ./memstore ./storetest -run '/Deferral' -count=20` — PASS |
| Real Turso adapter/local SQLite | From `/private/tmp/gopernicus-s7-deferral-sqlite`: `GOWORK=/private/tmp/gopernicus-s7-deferral-sqlite/go.work GOPROXY=off GOSUMDB=off go test -run '/Deferral' -count=1 -v ./...`, then `go test -race -run '/Deferral' -count=10 ./...` with same env — PASS. 18 cases, four connections, canonical migrations; harness/commands/logs in that directory's README.md, test.log and race.log |
| Documentation | `make docs-build` (pnpm typecheck/build, existing lockfile/toolchain) — PASS, `/tmp/gopernicus-s7-docs-build.log`; Docusaurus's optional update check reported a local-config warning after successful build |

An early working test command found a stale workers.Job seam assertion; migrated
the test to FencedJob and all subsequent checks pass. No unresolved framework
verification failure or approval blocker remains. Initial working logs remain
under /tmp/gopernicus-s7-*; they are not the final gate results.

Not exercised: live PostgreSQL, remote Turso/HRANA, Firestore emulator/GCP or a
GPS360 consumer upgrade/build/deployment. Required env was unset. Local SQLite
proves actual Turso SQL transitions, not PostgreSQL execution or network behavior.
Full jobs/event pocket audits and pre-existing SQL latest-generation ordering
remain later work. No schema/status migration, release, tag, consumer mutation or
manual generated-file edit. Next SDK slice: S8 cacher/ratelimiter/events.

## Changed-file inventory

Compared the 1,516-path source/document snapshot at
/tmp/gopernicus-s7-implementation-baseline.json, plus newly created files owned by
this task. Snapshot scope does not include every asset in the repository; do not
treat previously existing files absent from it as new changes. No other snapshotted
source/module/generated files changed. Prior audit/concurrent dirty paths remain.
The machine-readable list is /tmp/gopernicus-s7-changed-files.json.

46 paths:

- `ARCHITECTURE.md`
- `AUDIT.md`
- `RELEASING.md`
- `plans/framework-audit-work.md`
- `plans/framework-audit.md`
- `plans/workers-cleanup.md`
- `pockets/events/poller_worker_test.go`
- `pockets/jobs/README.md`
- `pockets/jobs/domain/job/fenced.go`
- `pockets/jobs/domain/job/job.go`
- `pockets/jobs/fenced.go`
- `pockets/jobs/internal/logic/runtime/lifecycle_test.go`
- `pockets/jobs/internal/logic/runtime/runtime.go`
- `pockets/jobs/jobs.go`
- `pockets/jobs/jobs_test.go`
- `pockets/jobs/memstore/defer.go`
- `pockets/jobs/memstore/protocol_conformance_test.go`
- `pockets/jobs/middleware_test.go`
- `pockets/jobs/stores/pgx/defer.go`
- `pockets/jobs/stores/turso/defer.go`
- `pockets/jobs/storetest/defer.go`
- `pockets/jobs/storetest/fenced.go`
- `pockets/jobs/storetest/storetest.go`
- `sdk/README.md`
- `sdk/capabilities/work/work.go`
- `sdk/capabilities/work/worktest/worktest.go`
- `sdk/foundation/async/lifecycle_test.go`
- `sdk/foundation/async/pool.go`
- `sdk/foundation/async/pool_test.go`
- `sdk/foundation/workers/errors.go`
- `sdk/foundation/workers/example_test.go`
- `sdk/foundation/workers/fenced.go`
- `sdk/foundation/workers/fenced_test.go`
- `sdk/foundation/workers/lifecycle_test.go`
- `sdk/foundation/workers/middleware.go`
- `sdk/foundation/workers/outcomes_test.go`
- `sdk/foundation/workers/pool.go`
- `sdk/foundation/workers/pool_test.go`
- `sdk/foundation/workers/process.go`
- `sdk/foundation/workers/runner.go`
- `sdk/foundation/workers/runner_test.go`
- `sdk/foundation/workers/stats.go`
- `sdk/foundation/workers/work.go`
- `workshop/documentation/docs/pockets/jobs.md`
- `workshop/documentation/docs/sdk/capabilities.md`
- `workshop/documentation/docs/sdk/foundation.md`
