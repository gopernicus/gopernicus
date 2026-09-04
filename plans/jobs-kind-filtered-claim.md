# pockets/jobs: Claim and the scheduler pool filter by the kinds a runtime actually handles (#37)

Status: RELEASED 2026-09-04 — PR #38 squash @ `eac19a0`; three tags
cold-verified (see the Execution record). Ratified the same day (owner:
"start implementing"). Written from a direct read of `pockets/jobs` at
`ee9bcde` (main); every claim below was re-verified against that tree; the
owner's post-plan review (expected-kind CAS, OR grouping, assertion removal)
is folded in. See the Execution record at the end.

## Context

Issue #37 (gps-360-go, observed Sep 2–4 2026) reports that both dispatch halves
of the jobs pocket ignore the worker's handler registry:

1. **The queue claim takes the oldest due job of ANY kind.** The pgx claim
   statement (`stores/pgx/queue.go` `Claim`) selects on status/scheduled_for/
   stale-lease only; the runtime (`internal/logic/runtime/runtime.go` `process`)
   looks the kind up in `Handlers` AFTER the claim and returns
   `"no handler registered for kind"` wrapped in `sdk.ErrInvalidInput`, which
   the `workers.Runner` routes through `Fail` — burning one attempt, and
   dead-lettering at `MaxAttempts`. A stale echo worker dead-lettered the first
   `delivery.import` jobs; a comps worker later dead-lettered three fired
   delivery jobs.
2. **The scheduler pool fires EVERY due schedule row.** `schedulesvc.WorkFunc`
   calls `ListDue(ctx, now, batch)` with no kind predicate and fires each row.
   With `Repositories.Schedules` wired in two binaries, whichever polls first
   fires the other's rows, and half 1 then destroys the fired jobs when the
   owning worker is down.
3. **Running every worker only softens it.** With both workers up, the comps
   worker still won a `delivery.import` claim and spent one of its attempts
   failing it. A job with few attempts can dead-letter with every worker
   healthy.

Confirmed additionally in planning, not in the issue:

- **The fenced runtime has the same gap.** `stores/{pgx,turso}/fenced.go`
  `Claim` and `memstore/fenced.go` `Claim` select the oldest due job of any
  kind; `fenced.go` `NewFencedRuntime`'s `process` returns `errUnhandledKind`
  after the claim, which the fenced runner's retry decider treats as a
  transient retry until `MaxAttempts`, then dead-letters. Same failure, same
  fix.
- **The scheduler's kind decision must survive its two-step CAS.** `Ensure`
  can update a schedule's kind without moving `next_run_at` when its recurrence
  spec is unchanged. A kind-filtered `ListDue` alone therefore has a TOCTOU
  gap: a row listed as kind A can become kind B before runtime A calls
  `ClaimDue`, whose current CAS does not compare kind. The claim CAS needs an
  expected-kind guard as part of this fix.
- There are no implementers or direct callers of the three ports outside
  `pockets/jobs` in this repo (`examples/jobs-minimal` and `examples/auth-cms`
  wire `Repositories` and the runtimes only). gps-360-go's own test doubles,
  if any, are downstream.
- The pgx `job_queue` already carries `idx_job_queue_kind (kind, status)` and
  the partial claim index; `fenced_job_queue` has the partial claim index only.

The net effect today: N worker binaries sharing one queue must all be up for
any of them to be healthy, which defeats "independently deployable binary per
kind of work" — the pocket's own README §Service/Runtime pitch.

## Goal

A runtime claims only jobs whose kind it registered a handler for, and its
scheduler pool fires only schedule rows whose kind it registered, with that
kind rechecked atomically when the schedule slot is claimed. A job or
schedule of a kind no running binary handles WAITS for the binary that owns
it; it is never claimed, never charged an attempt, never dead-lettered by a
binary that cannot process it. Applies to the unfenced queue, the fenced
queue, and the scheduler. The kinds are derived from the registry the runtime
already has (`Config.Handlers` / `FencedRuntimeConfig.Handlers`); no new host
configuration.

## Out of scope

- A `Config.Kinds` / `ScheduleKinds` override that lets a runtime claim or
  fire kinds it does not handle (decision 2). File it when a real host needs a
  central scheduler binary.
- A stale-pending / orphaned-kind report or admin surface (decision 8 —
  follow-up issue; `QueueRepository.List` with `ListFilter{Kind, Status}`
  already lets a host query pending-by-kind).
- Any change to `sdk/foundation/workers` (`JobStore`, `FencedStore`, the
  runners, the pool). The kernel is untouched (decision 1).
- Schema/index changes (decision 6).
- The `/jobs/*` admin route reservation.
- gps-360-go adoption (repin + retire the deploy-together stopgap in its
  plans/43 §4.5a) — downstream/owner.

## Schema / datastore impact

None. No migration in either store. The existing pgx `idx_job_queue_kind
(kind, status)` and the partial claim indexes cover the filtered scan at
current volumes; the filtered claim in Postgres adds one `kind = ANY($n)`
predicate to a subquery that already returns one row under SKIP LOCKED.
Revisit (a composite partial index `(kind, scheduled_for, priority DESC,
created_at) WHERE status='pending'`) only if a host profile shows it.

## Module / API impact

| Module | Change | Bump |
|---|---|---|
| `pockets/jobs` | Port signatures: `job.QueueRepository.Claim`, `job.FencedQueueRepository.Claim`, and `schedule.Repository.ListDue` each gain a trailing `kinds []string`; `schedule.Repository.ClaimDue` gains a trailing `expectedKind string` so the scheduler's list filter remains true at the CAS; `memstore` and `storetest` follow; runtime/scheduler pass the registry's kinds; `Register` logs them | **MINOR v0.5.0** (breaking ports for any external store implementer; pre-1.0 minor by repo convention) |
| `pockets/jobs/stores/pgx` | Filtered variants of the three selection statements plus an expected-kind guard on the schedule CAS; conformance passes the new cases; go.mod pin `pockets/jobs v0.3.0 → v0.5.0` | **MINOR v0.5.0** |
| `pockets/jobs/stores/turso` | Same; go.mod pin `v0.3.0 → v0.5.0` | **MINOR v0.4.0** |
| `sdk` | none | — |
| examples | compile-only; no source change expected (verify by grep + build) | — |

One train, three tags. Hosts that hand-wrote a `QueueRepository`,
`FencedQueueRepository`, or `schedule.Repository` double must add the new
parameters (upgrade note in RELEASING.md).

## Generated-artifact impact

None. No templ, no generated code in this pocket.

## Design decisions (settled here, implementer does not relitigate)

1. **The filter is a port parameter; the sdk kernel stays untouched.**
   `Claim(ctx, workerID, now, kinds []string)` (unfenced),
   `Claim(ctx, now, leaseID, leaseFor, kinds []string)` (fenced),
   `ListDue(ctx, now, limit, kinds []string)`. This breaks the documented
   "share the kernel's exact signatures" identity in `domain/job/job.go` and
   `domain/job/fenced.go`; the runtime restores it with a private adapter
   (`kindScopedQueue` / `kindScopedFenced` in `internal/logic/runtime` and
   `fenced.go` respectively) that closes over the kinds and satisfies
   `workers.JobStore[job.Job]` / `workers.FencedStore[job.Job]`. Remove the two
   compile-time assertions that the repository ports satisfy the kernel ports
   (they no longer do), and rewrite the port docs accurately: these are
   pocket-specific repository ports whose queue lifecycle is adapted to the
   narrower kernel ports by closing over registered kinds. Do not call either
   repository a "strict superset" of the kernel interface after `Claim` changes
   signature. Rejected alternatives:
   a store constructor option (`WithKinds`) — the kinds live on the runtime,
   and a second configuration point drifts; an optional-interface upgrade
   (`ClaimKinds`) — two claim methods on one port is clutter and all three
   in-repo stores are ours, so the conformance suite carries the contract.
2. **Kinds are derived from `Handlers`, never configured.** `NewRuntime` and
   `NewFencedRuntime` build a sorted, deduplicated `[]string` from the handler
   map keys (sorted so log lines and SQL args are deterministic). `NewService`
   derives the same slice from `cfg.Handlers` for the scheduler. There is no
   case where a runtime should claim a kind it cannot process. No
   `Config.Kinds`.
3. **`nil`/empty `kinds` means "no filter"** — the repo's zero-value-does-not-
   filter convention (`job.ListFilter`). It keeps the existing unfiltered
   statements byte-identical for direct callers and lets the conformance
   suite prove both arms. The runtimes ALWAYS pass a non-empty slice: an empty
   registry is already rejected at construction (`ErrHandlersRequired`), and
   `NewService` skips the scheduler-kinds derivation only when it skips the
   scheduler.
4. **Scheduler filtering follows the same registry, including at the CAS.**
   `schedulesvc.Deps` gains `Kinds []string`; `WorkFunc` passes it to `ListDue`.
   `schedule.Repository.ClaimDue` gains a trailing `expectedKind string`, and
   `fire` passes the kind from the listed schedule. The atomic CAS requires the
   row's current kind to still equal `expectedKind` in addition to the existing
   id / `next_run_at` / enabled guards. This closes the list-to-CAS race where
   `Ensure` can change a row from kind A to B without changing `next_run_at`:
   runtime A must lose that CAS, leaving the slot for runtime B to list and
   claim. A schedule row whose kind this runtime does not handle is not listed,
   not CAS-claimed, and not fired; its `next_run_at` stays put until the owning
   binary polls. This means a due slot can fire late by up to the owning
   binary's poll cadence when that binary is down — that is the correct posture
   (the job would have waited anyway) and is documented.
5. **The post-claim "no handler" path stays as defense.** Both runtimes keep
   their existing unknown-kind failure branch (it is now unreachable unless a
   store ignores the filter). No behavior change there; the tests for it stay.
6. **SQL: the unfiltered statements stay verbatim; the filtered arm is a
   sibling, and the full due disjunction is parenthesized before the kind
   predicate.** The pgx `Queue.Claim` statement is under the pgx-crud-v1 P5
   "preserved VERBATIM, positional args" directive; honor it by leaving the
   `len(kinds) == 0` path exactly as is. The filtered sibling's selector MUST be
   shaped as `WHERE ((status = 'pending' AND scheduled_for <= $2) OR (status =
   'running' AND claimed_at < $3)) AND kind = ANY($4)`, with `kinds` bound as
   `[]string` (pgx encodes it as `text[]`). Do not merely append `AND kind` to
   the current unparenthesized `pending OR running` expression: SQL precedence
   would apply it only to the running arm and leave pending jobs unfiltered.
   Fenced pgx uses the same full-disjunction grouping followed by `AND kind =
   ANY(@kinds)` (NamedArgs). Schedules pgx uses `AND kind = ANY(@kinds)` in
   `ListDue`; its `ClaimDue` keeps the existing positional argument meanings and
   adds `AND kind = $5` for `expectedKind`.

   Turso/SQLite has no array parameters: on the filtered path build `AND kind
   IN (?, ?, …)` with one bound placeholder per kind (never interpolate kind
   values into SQL). In BOTH claim statements the inner selector must be
   `WHERE ((pending due) OR (running stale)) AND kind IN (...)`, and the
   repeated outer race guard must be `AND ((pending due) OR (running stale))
   AND kind IN (...)`; the kind args are consequently bound twice. SQLite has
   no SKIP LOCKED, so the outer repeat must see the same due grouping and kind
   predicate. `ClaimDue` adds `AND kind = ?` with
   `expectedKind`. No `json_each` — one fewer libsql feature to depend on. No
   new index (see Schema impact).
7. **Stale-claim recovery honors the filter too.** A running job with an
   expired lease is reclaimable only by a claimer whose kinds include it. The
   conformance case proves it.
8. **Silence is the accepted trade, made visible at start-up.** Today a job
   with no handler anywhere fails loudly within minutes; after this change it
   waits indefinitely. In-train mitigation: `Service.Register` adds a
   `kinds` attr (the sorted slice) to its existing "registered jobs pocket"
   INFO line, and `NewRuntime`/`NewFencedRuntime` log one INFO line
   `"jobs runtime: claiming kinds"` with `kinds` and `pool`. README documents
   the orphan semantics and points at `List(ListFilter{Kind, Status:
   pending})` as the operator query. A pending-age/orphan report is filed as
   a follow-up issue on release, not built here.
9. **Version and train.** `pockets/jobs/v0.5.0` first; poll the proxy `.info`
   URL until it resolves (RELEASING lesson: a `go mod tidy` before the proxy
   sees the tag caches a negative lookup); then bump both stores' `go.mod`
   pins to v0.5.0, tidy, tag `stores/pgx/v0.5.0` and `stores/turso/v0.4.0`,
   poll each. Cold-verify from a throwaway module that builds a runtime
   against each store.

## Risks

- **Hidden orphans** (decision 8). A misnamed kind in a schedule row or an
  enqueue now waits forever instead of dead-lettering. Mitigated by the
  start-up kinds log and docs; fully addressed only by the follow-up report.
- **Third-party doubles break at compile time** — the intended, loud failure.
  gps-360-go: repin + fix doubles is downstream/owner.
- **Turso conformance is live-only** (`-tags=integration`). The Turso
  playground note applies: only the one truncate-safe URL may run the suite;
  verify the URL before running.
- **pgx `[]string` → `text[]` encoding with the positional statement**: the
  filtered path is a NEW statement string, so the verbatim directive on the
  old one is not violated, but the implementer must keep `$1..$3` meanings
  identical and add `$4` only.
- **Determinism of the kinds slice**: sort it once at construction so
  repeated SQL text/args and log lines are stable across runs.

## Tasks

### task-1: Ports + memstore + conformance cases

- **depends_on:** []
- **model:** opus
- **files:** [pockets/jobs/domain/job/job.go, pockets/jobs/domain/job/fenced.go, pockets/jobs/domain/schedule/schedule.go, pockets/jobs/memstore/queue.go, pockets/jobs/memstore/fenced.go, pockets/jobs/memstore/schedules.go, pockets/jobs/storetest/storetest.go, pockets/jobs/storetest/fenced.go, pockets/jobs/jobs_test.go, pockets/jobs/internal/logic/queuesvc/service_test.go, pockets/jobs/internal/logic/runtime/runtime_test.go, pockets/jobs/internal/logic/schedulesvc/service_test.go]
- **verify:** `cd pockets/jobs && go build ./... && go vet ./... && go test -race ./...` (memstore conformance green; stores NOT yet updated — they build under go.work, so expect stores to fail to compile until task-3/4; run only the core module here)
- **description:** Add the trailing `kinds []string` parameter to the two Claim
  methods and `ListDue`, with doc comments per decisions 1, 3, 4, 7 (nil = no
  filter; the filter applies to the stale-reclaim arm; selection order
  unchanged within the filtered set). Add trailing `expectedKind string` to
  `ClaimDue`; its CAS loses when the stored kind differs. Remove the now-false
  `QueueRepository`/`FencedQueueRepository` kernel-interface compile assertions
  and their otherwise-unused `workers` imports, and rewrite the two "exact
  kernel signature" doc paragraphs per decision 1. memstore: skip
  jobs/schedules whose kind is not in a
  non-empty `kinds` (a small `kindAllowed(kinds, kind)` helper in memstore,
  linear scan — the slices are handler-count sized). storetest: add
  `ClaimKindFilter` to `RunQueue` and `RunFencedQueue`, and
  `ListDueKindFilter` and `ClaimDueKindGuard` to `RunSchedules`. Each claim/list
  case seeds two kinds where the
  EXCLUDED kind would win the unfiltered ordering (older created_at AND higher
  priority), then proves: filtered claim returns the included kind; a filter
  naming no seeded kind returns `workers.ErrNoWork` and leaves both rows
  pending with attempts untouched; `nil` returns the unfiltered winner; a
  running job past its lease of the excluded kind is NOT reclaimed by a
  claimer whose kinds exclude it (queue case uses `storetest.Lease` with the
  same wall-clock sleep the existing reclaim case uses; fenced case uses an
  expired `leaseFor`). The schedule CAS case lists/retains the old slot, updates
  the same schedule name from kind `a` to kind `b` without changing its spec,
  proves a `ClaimDue(..., "a")` loses and leaves `NextRunAt` unchanged, then
  proves `ClaimDue(..., "b")` wins. Existing Claim/ListDue cases pass `nil`;
  existing ClaimDue cases pass the seeded schedule's kind. Update every
  existing method implementation, test double, and call site inside the core
  module in THIS task, including `jobs_test.go`, `queuesvc/service_test.go`,
  `runtime/runtime_test.go`, and `schedulesvc/service_test.go`, so task-1's
  standalone core build/test gate is green before task-2 begins.

### task-2: Runtime + scheduler wiring + start-up visibility + core tests

- **depends_on:** [task-1]
- **model:** opus
- **files:** [pockets/jobs/internal/logic/runtime/runtime.go, pockets/jobs/internal/logic/schedulesvc/service.go, pockets/jobs/jobs.go, pockets/jobs/fenced.go, pockets/jobs/jobs_test.go, pockets/jobs/fenced_test.go, pockets/jobs/internal/logic/schedulesvc/service_test.go (if present)]
- **verify:** `cd pockets/jobs && go build ./... && go vet ./... && go test -race ./... && go test -race -count=5 -run 'Kind|Sibling|Scheduler' ./...`
- **description:** Per decisions 1–5, 8. `runtime.Deps` gains `Kinds
  []string`; `runtime.New` wraps `d.Queue` in a private `kindScopedQueue`
  whose `Claim(ctx, workerID, now)` calls `Claim(ctx, workerID, now, kinds)`
  and delegates `Complete`/`Fail`, and hands that to `workers.NewRunner`.
  `fenced.go` does the same with a `kindScopedFenced` over
  `svc.fencedQueue` for `workers.NewFencedRunner` (delegating every
  `FencedStore` method). A shared unexported `handlerKinds(map keys) []string`
  (sorted, deduped) lives in `jobs.go`; `NewRuntime` and `NewFencedRuntime`
  call it and emit the decision-8 INFO line. `NewService` passes
  `handlerKinds(cfg.Handlers)` into `schedulesvc.Deps.Kinds`; `WorkFunc`
  passes it to `ListDue`, and `fire` passes `sch.Kind` as `ClaimDue`'s
  `expectedKind`. Update the schedule-service fake to record/assert that kind.
  `Register` adds the `kinds` attr. Tests
  (`jobs_test.go`): **the issue's regression** — one memstore queue shared by
  two Services with disjoint handlers (`a`, `b`); enqueue one job of each;
  run only runtime A briefly → job `a` completed, job `b` still pending with
  `RetryCount == 0` and `WorkerName == ""`; then run runtime B → `b`
  completes. **Scheduler regression** — one memstore schedules repo shared
  by the two Services; ensure one due schedule of each kind; run only A's
  runtime → exactly one job enqueued (kind `a`), schedule `b`'s `NextRunAt`
  unchanged and `LastJobID` empty; run B → `b` fires. `fenced_test.go`: the
  same two-runtime regression on a shared memstore fenced queue (job `b`
  keeps `Retries == 0` and stays pending). Keep the existing unknown-kind
  tests as-is (decision 5). Assert the Register log carries `kinds` via the
  existing capturing-handler pattern if one exists in this module; otherwise
  skip the log assertion (do not add a new test helper for it).

### task-3: pgx store

- **depends_on:** [task-1]
- **model:** opus
- **files:** [pockets/jobs/stores/pgx/queue.go, pockets/jobs/stores/pgx/fenced.go, pockets/jobs/stores/pgx/schedules.go, pockets/jobs/stores/pgx/README.md]
- **verify:** `cd pockets/jobs/stores/pgx && go build ./... && go vet ./... && go test ./...` with the Makefile pgx-leg database env set (see `Makefile` line ~84 for the leg); then `make` the pgx leg target for this store
- **description:** Decision 6. `Queue.Claim`: `len(kinds) == 0` → the
  existing statement and positional call, byte-identical; else a sibling
  statement string with `AND kind = ANY($4)` inside the claim subquery and
  `kinds` bound as the fourth positional arg. Keep the P5 directive comment
  and extend it with one sentence naming the filtered sibling.
  `FencedQueue.Claim` uses the same explicitly parenthesized full due
  disjunction before `AND kind = ANY(@kinds)`. `Schedules.ListDue` appends `AND
  kind = ANY(@kinds)` on the filtered path (NamedArgs); `Schedules.ClaimDue`
  keeps `$1..$4` meanings and adds `AND kind = $5`, binding `expectedKind` as
  the fifth argument. Conformance already runs the new storetest cases — they
  must pass without harness changes. README: one
  sentence under the claim/ListDue notes that the kind filter is applied
  server-side via `= ANY(text[])` and needs no new index.

### task-4: turso store

- **depends_on:** [task-1]
- **model:** opus
- **files:** [pockets/jobs/stores/turso/queue.go, pockets/jobs/stores/turso/fenced.go, pockets/jobs/stores/turso/schedules.go, pockets/jobs/stores/turso/helpers.go]
- **verify:** `cd pockets/jobs/stores/turso && go build ./... && go vet ./... && go test ./...`; then the live suite `go test -tags=integration ./...` against the truncate-safe playground URL ONLY (verify `TURSO_URL` matches the playground note before running)
- **description:** Decision 6. A `helpers.go` `kindsIn(kinds []string) (clause
  string, args []any)` returning `" AND kind IN (?,?,…)"` plus the bound
  values (empty clause for nil). Both claim statements become built strings
  on the filtered path with the whole due `OR` wrapped in parentheses before
  the kind clause, in both the inner selector AND the repeated outer `WHERE`
  (arg order documented inline — the outer repeat means the kinds are bound
  twice). Unfiltered path keeps the existing `const` statements. `ListDue`:
  append the clause before `ORDER BY`. `ClaimDue`: add the mandatory trailing
  kind predicate and bind `expectedKind`. Conformance
  (`conformance_integration_test.go`) picks up the new cases unchanged.

### task-5: Docs, sweep, release train

- **depends_on:** [task-2, task-3, task-4]
- **model:** opus
- **files:** [pockets/jobs/README.md, workshop/documentation/docs/pockets/jobs.md, RELEASING.md, pockets/jobs/stores/pgx/go.mod, pockets/jobs/stores/turso/go.mod, .claude/plans/jobs-kind-filtered-claim.md, plans/jobs-kind-filtered-claim.md]
- **verify:** `make check && make guard`; `grep -rn --include='*.go' '\.Claim(\|\.ListDue(\|\.ClaimDue(' examples workshop` returns nothing outside tests that already compile
- **description:** README §"The contracts": Claim and ListDue bullets gain
  the kind-filter sentence (nil = unfiltered; runtimes always filter by their
  registry; stale reclaim honors it), and ClaimDue documents its expected-kind
  CAS guard. §"Service / Runtime": replace the
  "dedicated worker binary" sentence with the now-true statement that N
  binaries sharing one queue each claim only their kinds, plus the orphan
  paragraph from decision 8 (waits, never dead-lettered by a foreign binary;
  operator query via `List`; start-up `kinds` log). §fenced: one line that
  the fenced runtime filters identically.
  `workshop/documentation/docs/pockets/jobs.md`: verified in planning — its
  claim bullets and the scheduler CAS sentence (line ~87, "Competing runtimes
  race; one wins") stay true within a kind; add one sentence to that
  scheduler paragraph saying a runtime lists only schedules of the kinds it
  registered and rechecks the kind in the CAS. Nothing else there changes.
  RELEASING.md: chronology entries for the
  three tags in the existing format, an upgrade note ("hosts with hand-written
  repository doubles add the `kinds []string` parameters and the `ClaimDue`
  expected-kind parameter; hosts running one worker binary see no behavior
  change; hosts running several stop
  dead-lettering each other's jobs — retire any deploy-together stopgap"),
  and the pin table rows. Release per decision 9 (core tag → proxy poll →
  store pins + tidy → store tags → proxy polls → cold verify from a throwaway
  module). Close #37 with a release comment; open the decision-8 follow-up
  issue ("jobs: stale-pending / orphaned-kind visibility") linking #37.
  Append the execution record here and copy to tracked `plans/`. Never
  record "merged" or "tagged" before it has happened.

## Sequencing

task-1 → {task-2, task-3, task-4} in parallel → task-5. task-3 and task-4
depend only on the port shape from task-1 and can run alongside task-2. Under
`go.work` the stores compile against the local core, so the whole tree is
green before any tag is cut.

## Consultation notes

None pre-plan. The change is specified nearly to the line by the issue plus
the code read. Initial planning settled SQLite's repeated outer predicate and
stale-reclaim filtering. Post-plan review additionally tightened decision 1's
interface terminology/removal of compile assertions, decision 4's atomic
expected-kind CAS, decision 6's mandatory `OR` grouping, and task-1's
compilation boundary. Reviews below are post-plan calls.

## Open questions

1. **YOUR CALL — orphan visibility in-train or follow-up?** The plan settles
   on start-up logging + docs in-train and a follow-up issue for a
   stale-pending report (decision 8). Flip to in-train only if you want a
   additional port method (`CountPending(ctx, kinds…)` or a `PendingSince`
   scan) in the same release — that widens all three stores and the
   conformance suite.
2. **Turso predicate style** — dynamic `IN (?,…)` chosen over `json_each`
   (decision 6). Reverse if you would rather keep the statements `const`.

## Recommended reviews

- **lead-backend-engineer** — the port-parameter vs adapter choice (decision
  1), the `nil` = unfiltered convention (decision 3), the kind-scoped adapters
  in the runtimes.
- **data-integration-reviewer** — the pgx positional-sibling statement, the
  SQLite double-predicate (decision 6), index adequacy (Schema impact), and
  the new conformance cases' isolation.
- **platform-sre** — the silent-orphan trade (decision 8) and the three-tag
  train ordering (decision 9).

## Notes

- Hosts running exactly one worker binary with every kind registered see no
  behavior change beyond the extra INFO lines.
- The fix is the complete answer to "independently deployable binary per
  kind of work" for the queue and scheduler; a separate scheduler-only binary
  remains impossible (Handlers required) and is out of scope.
- Pre-existing, deliberately untouched: the `fenced_test.go` gofmt violation
  (~line 420) noted in the workers-idle-observability record.

## Execution record (2026-09-04)

**EXECUTED, MERGED, TAGGED.** Tasks 1–4 and the docs half of task-5 landed
via PR #38, squash `eac19a0` (28 files incl. `kinds_test.go`); CI green
(both workflows). Verify, all green:
`pockets/jobs` build/vet/`go test -race` (new cases also `-race -count=5`);
a mutation check (kinds disabled in the queue, fenced, and scheduler seams)
reproduced the issue's exact failure in all three regression tests
(`dead_letter`, `retries=1`, foreign schedule fired) before the seams were
restored; pgx conformance on a throwaway `postgres:17` in BOTH the default
and `POSTGRES_TEST_SCHEMA` modes; turso live conformance against the
authorized playground (176s, full suite, the four new cases confirmed run
verbosely); `make check && make guard` exit 0, zero guard changes, zero
`go.mod`/`go.sum` diff. No example or workshop code calls the ports
directly. Deviations from the task text: the kind-scoped adapters landed in
task-1 rather than task-2 (the core module could not compile without them);
the `Register` log test asserts the message only (the module's
`captureHandler` records messages, not attrs, per the task's fallback).
Release (owner: "push and release"): `pockets/jobs/v0.5.0` @ `eac19a0`,
proxy-resolved first poll (15s); pin commit `de453fb` (both stores
`pockets/jobs v0.3.0 → v0.5.0`, and `sdk v0.5.0 → v0.7.1` by MVS via tidy);
`stores/pgx/v0.5.0` + `stores/turso/v0.4.0` @ `de453fb`, both resolved first
poll; cold-verified from a throwaway module with a fresh `GOMODCACHE`
(`GOWORK=off`): all six store types satisfy the new ports and
`jobs.NewService` links. #37 closed via the PR + release comment; follow-up
#39 (stale-pending / orphaned-kind visibility) opened per decision 8.
gps-360-go adoption (repin + double fixes + stopgap retirement) is
downstream/owner.
Noted, untouched (sdk out of scope): `sdk/foundation/workers/fenced.go`
lines ~32–34 still say the jobs FencedQueueRepository is a superset with the
compile assertion "there" — now stale.
