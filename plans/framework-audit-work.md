# SDK audit S7: Async, workers and work

Status: IMPLEMENTED — 2026-09-10. Review completed 2026-09-09; the revised
implementation is recorded in [workers-cleanup.md](workers-cleanup.md).
Design revised with owner clarification — 2026-09-10: retain generic SDK runners
and middleware as intentional extension points; supersedes their removal proposal.
Parent: [framework-audit.md](framework-audit.md).

## Scope and approach

Audit `sdk/foundation/async`, `sdk/foundation/workers`, and
`sdk/capabilities/work`, including the public conformance harness. Follow their
actual use in the jobs pocket and representative consumers. The findings below
record the pre-implementation review. Implemented changes and verification live
in workers-cleanup.md; AUDIT-007 now records consumer migrations.

Review package existence and names as well as correctness. Preserve useful
concurrency and durable-execution guarantees; do not confuse a generic polling
loop with persistence. Distinguish the jobs runtime's unfenced graceful drain
from fenced cancellation and lease recovery before proposing consolidation.
Following these bridges does not constitute the full jobs-pocket audit.

## Owner clarification: Reuse and middleware

The owner explicitly wants consumers to bring their own jobs/work to SDK workers
without adopting the jobs pocket. Retain the generic Pool and the small runner/
store contracts that let custom queues reuse claim/process/complete/fail execution.
Jobs supplies its entities, repositories, scheduling and policies. A single current
consumer is evidence about migration and usage, not a reason by itself to remove
an intended framework extension point.

The owner also wants composable middleware, analogous to HTTP middleware, with
job gating as a concrete intended use. Retain Middleware/WithMiddleware. The
audit must distinguish a deliberately supported extension seam from an unused
convenience or duplicate mechanism. Still simplify unused requirements, redundant
retry configuration and overlapping hook APIs when their intended purpose is
preserved by the surviving contracts.

Before implementing middleware changes, specify the execution boundary:

- Pool middleware wraps one WorkFunc iteration. Around Runner.WorkFunc it runs
  before claim and after the whole lifecycle; it can gate polling on a shared
  condition but cannot inspect a job that has not been claimed.
- Processing middleware wraps a generic job processor, can inspect its job and
  decide whether to invoke next. This is the appropriate seam for job-dependent
  gates. Keep composition before/after processing separate from hooks that require
  a successfully persisted transition, such as the fenced dead-letter hook.
- A skipped processor returning nil must not accidentally mark unprocessed work
  complete. Define a temporary deferral versus permanent rejection, retry-budget
  treatment and lease disposition before advertising per-job gating. A temporary
  gate must not silently inherit ordinary failure/dead-letter policy.

The existing pre-process hooks only log errors and continue; they are not gates.
The accepted design uses a minimal processor middleware contract and explicit
defer/reject outcomes, demonstrated by custom SDK-only and actual jobs examples.
This does not authorize a generic policy engine or preserving every old hook.
The implemented API is documented in workers-cleanup.md and AUDIT-007.

## Tasks

1. Confirm branch, dirty state, module environment, source inventory and current
   contracts. Snapshot existing files before any edits.
2. Read all source/tests and trace framework, Segovia v2, Coordination Hub and
   GPS360 usage. Parent owns async and work contracts. Named backend reviewer
   examines Runner/fencing and jobs bridges; named SRE reviewer examines pool,
   middleware, stats and host lifecycle. Named implementer builds a usage
   inventory and disposable probes; reviewers do not edit source.
3. Run SDK build/test/vet and guards, plus focused race tests. Use disposable
   probes outside repository source for untested admission, shutdown, retry and
   fencing paths. Verify claims against observable behavior where practical.
4. Record ranked defects, documentation drift and simplification choices with
   triggers, source references, consumer impact and retained guarantees.
5. Complete the parent handoff with verification, changed files, unresolved
   questions and the proposed implementation sequence.

## Preconditions and constraints

- Branch/base: `firestore-authentication` / `6807ed06`. There are 452 pre-existing
  dirty/untracked paths; these three SDK packages initially have no changes.
  Baseline: `/tmp/gopernicus-s7-review-baseline.json` (1,515 file hashes).
- Go 1.26.1; no root go.mod. Run `./...` inside sdk or use workspace paths.
  `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`; formatter:
  `/Users/jrazmi/go/bin/goimports`.
- Preserve all existing edits. No consumer edits, credentials, production
  mutation, generated-file edits, commits, tags or module changes. Datastore
  environment variables are unset; live adapters need a separate disposable
  setup. Existing HTTP tests require loopback access.
- Read consumers' instructions and current module pins before interpreting
  usage. Original Gopernicus is reference material, not a restoration target.

## Coverage and actual usage

Read all three packages and their tests: async has 311 runtime lines; workers
has 1,289 (774 in the two generic runners); work has 105 runtime lines and a
354-line public test harness. Six test files contain 2,486 lines. Followed the
jobs runtime, repository ports, memory protocol harness, selected SQL ordering
paths, authentication delivery bridge and events outbox polling. The named
backend and SRE reviewers independently checked their assigned source/tests.

Read-only AST inventory: `/tmp/gopernicus-s7-usage.md`, with combined JSON/TSV and
reproducible per-package scripts. Counts are reference occurrences, not calls;
receiver-method counts are lower bounds because dependency types are partly
stubbed. Source inspection confirms the decisions below. There is no claim
about unknown external consumers.

| Surface | Actual adoption | Implication |
|---|---|---|
| async, including both presets | No outside-package Go references in framework or three consumers | Low observed migration cost; judge the core mechanism separately from its unused tuning conveniences |
| workers.Pool | Jobs queue, scheduler, fenced runtime, auth-cms outbox and Segovia outbox | Independent polling mechanism earns SDK placement |
| Runner / FencedRunner | One constructor each, both inside jobs | Retain generic execution for the owner's explicit custom-queue extension goal |
| Runner hooks, within-claim retry, attempt-only fenced retry | Package tests only | Review intended purposes and simplify overlapping mechanisms; preserve the chosen middleware seam |
| Middleware / ConsecutiveErrorShutdown | No effective production usage; jobs uses WithMiddleware() only as a no-op option | Middleware is an intentional extension point; the supplied shutdown middleware still needs correction |
| Pool.Errors | No inspected production readers | Prefer one terminal error path through Run |
| Heartbeat | Jobs passes it through; GPS360 configures it through jobs | Keep this operational feature |
| work interfaces/status | Jobs and authentication, auth-cms and Segovia delivery bridges | Preserve the small SDK protocol and shared persisted vocabulary |
| worktest | jobs/memstore protocol tests only | Useful test boundary, but current passing suite proves less than its labels imply |

Consumer pins checked during this review:

| Consumer | SDK | Jobs | Resolution |
|---|---|---|---|
| Segovia v2 | v0.8.0 | v0.4.2 | Tagged modules, no replace |
| Coordination Hub | v0.7.0 | v0.3.0 | Tagged modules, no replace |
| GPS360 | v0.7.1 | v0.4.1 | Both replaced with this local framework checkout |

Segovia constructs a Pool in cmd/server/main.go:260. It constructs a fenced jobs
runtime at :224 and ordinary jobs runtime at :346. Hub uses NewFencedRuntime in
cmd/workers/main.go:219; GPS360 uses NewRuntime in cmd/workers/echo/main.go:280.
Consumers' instructions were inspected; none were built, edited or upgraded.

Bounded original-framework comparison found the same async admission/Close
structure, so it offers no fix to restore. Its workers API had overlapping
Options/functional options and Start/Stop methods; the current Run(ctx) is
clearer. Old runner hooks/tracing/retry surfaces explain inheritance, not present
consumer demand. The original had no fenced runner. Preserve the current
direction; details and source paths are in the usage report.

## Ranked findings

Priorities distinguish consequence from adoption. Async and the unused worker
middleware have genuine defects despite having no inspected production users.
The runner and outbox logging paths are already exercised by real hosts.

| ID | Priority / classification | Finding | Evidence / correction |
|---|---|---|---|
| Q1 | High consequence, currently unadopted; concurrency | async admission is not synchronized with closing | Blocked Go and GoContext calls start new work after Close returns. Close does not wake blocked submitters. Serialize the final admission/WaitGroup registration with closing, recheck after acquiring capacity, and select blocked admission against a shared closing signal |
| Q2 | Medium; shutdown reporting | async Close reports success while work remains | Timeout returns nil; a concurrent or repeated Close returns nil without waiting. Use shared drain completion and each caller's context; nil should mean drained |
| Q3 | Low, currently unadopted; construction | async.WithLogger(nil) panics | Closing even an empty pool panics. Apply the same nil-to-default fallback as workers |
| Q4 | High operational impact; persistence reporting | Job transition failures are swallowed and ordinary Runner logs false completion | Complete failure still produces job-completed and nil; both runners discard unexpected terminal/retry-write errors. Return unexpected persistence errors and log success only after it is recorded |
| Q5 | High operational impact; observability | Pool does not log ordinary WorkFunc errors | Real events.Poller returns storage/publish errors to Pool; they are counted but the cause disappears. Log ordinary failures centrally with pool/worker/cause |
| Q6 | Medium; cadence contract | Stable polling interval permits zero delay after slow work | WithPollInterval(10ms), a 30ms iteration is immediately followed by the next. Reset a timer after every iteration if preserving the documented delay contract |
| Q7 | Medium, currently unadopted; control flow | ConsecutiveErrorShutdown changes pool-wide shutdown into worker-only shutdown | Threshold 1 turns ErrPoolShutdown into ErrWorkerShutdown and discards its cause. Its counter map is also shared across wrappers with matching worker IDs. Preserve middleware; correct control-error precedence, cause preservation and state ownership |
| Q8 | Medium; lifecycle/API | Critical error stream is lossy and Pool is secretly single-use | Buffered panic can displace fatal stop; Run still returns nil. Second Run panics closing the same channel. Return fatal stop from Run, remove the unused stream, and explicitly guard one Run per Pool |
| Q9 | Medium; verification gap | worktest does not verify lifecycle projection or concurrent atomic admission | Repeated reads of one pending generation cannot prove correct latest selection or all lifecycle states. Strengthen observable protocol cases and actual Service lifecycle tests without adding executor methods to work |
| Q10 | Low; misleading contracts | Docs overstate durability, timeout bounds and current architecture | async tasks are not automatically request-scoped; contexts only bound cooperative work; repository/runner compatibility and unimplemented-store comments are stale. Describe current behavior plainly |

### Q1–Q3: A single, explicit async lifecycle

Source: sdk/foundation/async/pool.go:165–225, :260–309. Go/GoContext check closed
before waiting for capacity and register work afterward with no lock shared
with Close. Close can begin Wait before that Add. The deterministic probe uses
two occupied slots, starts a blocked submission, closes with a canceled context,
then frees one slot while an anchor task remains active: the blocked call was
not released by closing, and the new task starts in the closed pool. The same
observation holds for both admission methods. This also exposes an unsynchronized
WaitGroup Add/Wait path; no claim of a reproduced WaitGroup panic is needed to
establish the admission defect.

The timeout branch at :299–304 returns nil. The closed shortcut at :270–274
returns nil while the first Close still waits. Separate probes demonstrate both
outcomes. Deadline timer and ctx.Done also compete to report different results
for the same exhausted caller budget. WithLogger(nil) reaches the empty-pool
close log at :293 and panics.

Recommendation: keep the coherent bounded in-process pool. Use one admission
implementation (Go delegates with a background context), one close signal and
one drain result. Closing rejects/wakes waiting submissions. Every Close waits
on the same drain signal within its own context. Keep Wait as a reusable batch
barrier: finish submitting the batch before calling Wait; use Close for shutdown
concurrent with producers. State that precondition explicitly rather than
promising more than WaitGroup permits. Context on GoContext controls
admission; the closure owns the task's execution context and cancellation.

Remove IOPreset/CPUPreset and WithShutdownTimeout: neither preset is evidenced
as an optimization for its broad workload category, and Close(ctx) already has
a host-owned shutdown budget. Close with a background context would wait until
drained; callers wanting a bound supply a deadline. This is an explicit proposed
behavior/API change, not a fix already applied. Preserve concurrency limits,
blocking/drop choice, panic recovery and counters. Package deletion would be
possible given current non-adoption, but its cohesive concurrency mechanism is
useful enough to retain; merging it with a polling pool would obscure ownership.

### Q4: Persistence outcome must remain visible

Source: workers/runner.go:232–245, :329–343; workers/fenced.go:292–301,
:327–340, :376–415. A process returning nil is not evidence that Complete was
persisted. Unexpected Complete/Fail/Reschedule errors should reach WorkFunc's
caller so Pool counts/logs them while continuing ordinary polling. Do not add
an immediate retry loop around state writes: lease recovery remains responsible
for unresolved executions. Preserve nil for a successfully recorded process
failure and the deliberately abandoned/fenced-claim cases. Fenced sdk.ErrConflict
is an expected loss of ownership; preserve its distinct log and avoid claiming
completion. Dead-letter hooks still run only after successful permanent Fail.

Both generic runners also complete processed.ID(), rather than fixing the
claimed identity. A process returning a different/zero job with nil error can
target the wrong execution. The two production processing closures always return
their original job; deleting this needless return value eliminates that SDK-only
footgun. Public FencedRunner additionally accepts a timeout at/beyond its lease;
jobs.NewFencedRuntime already rejects it correctly. Validate the relationship at
the retained SDK constructor too, with its construction error behavior made clear.

### Q5–Q8: Keep a small, honest polling pool

Source: workers/work.go:18–25; pool.go:169, :184–229, :271–307, :360–375;
middleware.go:13–34. events/poller.go:86–105 returns failures from ListUnpublished,
Emit and MarkPublished. Segovia's direct Pool and the auth-cms outbox Pool do
not install another logger around this function. Central error logging therefore
fixes an existing blind spot, not merely a documentation discrepancy. Coordinate
runner logs to avoid duplicate full-cause messages; suppress only explained
shutdown cancellation noise, not arbitrary errors during shutdown.

A reset-after-work timer gives the existing poll/idle options their documented
meaning. Retain the short initial delay, cancellation precedence and immediate
wake hints. This reduces maximum throughput for slow iterations compared with
the current ticker and needs migration notes. Keep nil/coalesced wake behavior;
closing the producer-owned wake channel is explicitly unsupported today, so
that misuse is not a new correctness finding.

Retain Middleware/WithMiddleware and explicitly define chain ordering, concurrent
invocation and short-circuit behavior. Fix ConsecutiveErrorShutdown so control
errors retain their scope, threshold shutdown retains the triggering cause, and
counters belong to the wrapped function rather than unrelated wrappers. Add
focused middleware composition and gating examples/tests. The no-op jobs
poolInterval helper can still simplify: pool options already default nonpositive
intervals. No generalized configuration system is needed. See the owner
clarification above for the separate job-processing gate contract still to define.

Return ErrPoolShutdown (with its cause) from Run after workers drain. Keep
ordinary errors and recovered panics recoverable, logged and counted. Remove
Errors() instead of introducing a second reliable delivery mechanism. Guard
single use with a clear returned error; do not add restart support without a
consumer. When a jobs runtime owns queue and scheduler pools, a fatal exit from
either must cancel its sibling before waiting, or the joined Run can hang.

Retain Stats and optional heartbeat. Atomic counters are approximate concurrent
snapshots, which is sufficient here. The heartbeat's claims field actually
counts nil-returning iterations (an outbox iteration can emit many events).
Rename/document it as successful iterations rather than pretending Pool can
count domain jobs. This is a log-consumer compatibility change.

### Q9: Preserve the work boundary; improve evidence

The three segregated ports, seven status strings and Terminal/Known predicates
are simple and genuinely shared. Keep their names and persisted values. Do not
merge work with workers or move it into jobs: authentication and host bridges
use it to avoid depending on the jobs implementation. The executor lifecycle
does not belong in this producer/status protocol.

worktest/worktest.go:139–185 only observes pending; :294–312 checks only the
immediate prior generation after repeated replacement. The suite never races
EnqueueOnce/Replace, changes lifecycle state, exercises re-admission after
terminal state, or verifies replacement payload cloning. Strengthen concurrency,
idempotent payload preservation, all prior replacement generations and byte
ownership using existing protocol/Inspector methods. Put the real status matrix
and lifecycle transitions in Service-over-store tests; avoid expanding production
ports with test/executor controls. Separate jobs/storetest already covers richer
repository concurrency, but that does not make every future work implementation
conformant or prove the Service projection.

Independent public-API proof wraps the actual jobs.Service/memstore with an
adapter that always reports pending for existing keys. Both entire worktest
suites pass. Claim+Complete then produces completed from the real Service and
pending from the wrapper. Add lifecycle tests for states the implementation can
actually produce; an adapter need not emit every allowed vocabulary value.

Clarify key scope and ownership in docs: kind does not automatically namespace
logicalKey, and hosts must construct keys accordingly. Empty-key semantics are
not stated by SDK work; the jobs repository explicitly allows unkeyed admission.
Decide/document that edge before claiming adapter equivalence, without silently
changing the repository's separate unkeyed feature.

Carry the known durable-ordering follow-up into the full jobs audit: pgx
fenced.go:340–342 and turso fenced.go:380–382 select greatest created_at then
job_id, while insertion uses wall-clock time with no per-key monotonic correction.
Equal timestamps plus a lower new ID, or a clock regression, can select the
superseded generation. The S4 memstore correction at memstore/fenced.go:325–334
does not fix either SQL adapter. Their actual live behavior was not tested here.

### Q10 and architectural recommendation: Retain generic execution contracts

Retain generic ordinary/fenced execution and narrow store contracts in SDK so
consumers can bring custom queues. Simplify the contracts in place: remove the
unused Job.Status requirement, fix execution identity independently of processing
results, and assess each retry/hook feature against its intended purpose. Prefer
composable processing middleware to overlapping pre/post callback APIs where it
preserves behavior. Post-persistence hooks have a distinct ordering guarantee and
must not be casually folded into a wrapper around processing.

Keep the jobs-specific kinds-filter adapters where required by its richer ports;
adapting a domain repository to a reusable SDK contract is a valid hexagonal
boundary. Their existence alone is not evidence of needless abstraction. Retain
SDK middleware as an explicit framework extension seam. The earlier proposal
to move all runners into concrete jobs internals and remove middleware is
superseded by the owner's 2026-09-10 clarification.

Preserve existing public jobs runtime constructors/configuration and job field
names; a field rename is unnecessary migration work. Preserve panic-to-process-
error handling, durable attempt ceilings, fenced error-aware backoff and terminal
hooks. Preserve unfenced admitted-work drain and fenced canceled-work lease
recovery as two explicit lifecycles. Share only an obvious small helper when
needed; do not replace two clear functions with a mode-switching generic engine.

This preserves SDK ownership of reusable execution mechanics, inward dependencies
and the stdlib-only SDK. Jobs owns its domain and policy. The implementation plan
must demonstrate a small custom queue using SDK workers without importing jobs,
as well as preserve actual jobs runtime behavior. Existing host runtime APIs can
remain stable while the SDK contracts become smaller and clearer.

Update current package/SDK/jobs/site docs together with implementation:

- async/pool.go:4–15: in-process work can outlive a request; no automatic task
  context is supplied. workers alone does not guarantee eventual delivery.
- workers/fenced.go:32 claims direct repository satisfaction, contradicted by
  the current kinds-filter adapter.
- jobs/domain/job/fenced.go:24 says no implementation exists despite three stores.
- workers retry/timeout comments overstate enforced bounds. Context deadlines
  require cooperative functions; timeout starts after Claim. Unfenced drain also
  detaches Claim and persistence I/O, so handler timeouts alone cannot bound it.
- jobs/fenced.go:410 and jobs README:155 must describe cancellation/lease recovery
  rather than use an ambiguous graceful-drain label.
- work/work.go:6–10 should explain why this package ships no default, rather than
  claiming a process-local keyed queue is impossible. Its current test harness
  is already backed by a process-local jobs queue.

## Proposed implementation sequence

1. Fix async admission/closing and simplify its timeout/preset surface; add
   deterministic blocked-admission, concurrent-close, timeout and panic tests.
2. Fix the polling pool's cadence/logging/lifecycle and supplied shutdown
   middleware while retaining the middleware extension point. Simplify error
   reporting through Run. Exercise a real events Poller failure, middleware
   short-circuit/control behavior and fatal queue/scheduler sibling shutdown.
3. Simplify retained generic SDK runners/store contracts and fix persistence
   reporting. Specify processor middleware and deferred/rejected gate outcomes
   before implementing per-job gating or retiring overlapping hook APIs. Prove
   custom queue use without jobs and preserve distinct cancellation, fencing,
   checkpoint and durable-retry behavior in actual jobs Service/runtime tests.
   This is not the full pocket/store audit.
4. Strengthen work conformance and Service lifecycle tests, refresh architecture
   and user docs, and record implemented breaking changes in AUDIT-007 and
   RELEASING.md. No status/schema migration is proposed for this SDK slice.
5. Run formatter, affected-module build/test/vet, focused race tests, guards and
   full make check for the cross-module implementation milestone; run the docs
   build if changed. Consumer upgrades/releases remain separate.

The owner approved the revised sequence on 2026-09-10. It was implemented under
workers-cleanup.md, retaining generic SDK runners and both middleware boundaries.

## Historical review verification (before implementation)

At the review checkpoint, all implementation source, existing tests, manifests and consumer files remain
unchanged. Comparison with the 1,515-file hash snapshot confirms exactly:

- `plans/framework-audit-work.md` — new review, findings and proposed sequence.
- `plans/framework-audit.md` — progress/index/handoff update.

Commands used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`:

| Check | Exact command / outcome |
|---|---|
| SDK baseline, from sdk | `go build ./... && go test ./... && go vet ./...` — PASS; test results reused valid cache entries |
| Boundary guards, root | `make guard` — PASS; `/tmp/gopernicus-s7-guard.log` |
| Uncached SDK/memstore race checks, root | `go test -race -count=1 ./sdk/foundation/async ./sdk/foundation/workers ./sdk/capabilities/work/... ./pockets/jobs/runtime ./pockets/jobs/memstore` — all existing target packages PASS, but the command exited 1 because the runtime path was wrong; retained in `/tmp/gopernicus-s7-race.log` |
| Corrected actual jobs runtime + public bridge, root | `go test -race -count=1 ./pockets/jobs ./pockets/jobs/internal/logic/runtime` — PASS; `/tmp/gopernicus-s7-jobs-race.log` |
| Async behavior probes, root | `go test -race -v /tmp/gopernicus_s7_async_probe_test.go` — observations reproduced; `/tmp/gopernicus-s7-async-probes.log` |
| Pool behavior probes, root | `go test -race -v /tmp/gopernicus_s7_pool_probe_test.go` — initial four observations reproduced; `/tmp/gopernicus-s7-pool-probes.log` |
| Additional lossy-fatal probe, root | `go test -race -v /tmp/gopernicus_s7_pool_probe_test.go -run '^TestObserveFatalStopDroppedFromErrors$'` — reproduced; `/tmp/gopernicus-s7-pool-fatal-probe.log` |
| Runner and real-Service conformance probes, from sdk | `go test /tmp/gopernicus-s7-runner-probe_test.go -v -count=1` and `go test -race /tmp/gopernicus-s7-runner-probe_test.go -count=1` — observations reproduced; `/tmp/gopernicus-s7-runner-probe{,-race}.log` and `.md` report |

Probe PASS means the asserted faulty behavior/coverage gap was observed; it does
not mean production behavior is correct. Async and pool ordering probes use
channels and testing/synctest, not scheduling guesses. Runner probes distinguish
unexpected storage failures from intentional lease conflicts. No unresolved
verification command failure remains after correcting the runtime path.

No new full make check, docs build, consumer build, live SQL/Firestore test,
browser/HTTP exercise, deployment or release was performed for this review.
The preceding S6 full workspace/docs gates remain recorded in web-cleanup.md.
This slice exercised actual public Go behavior and jobs.Service/memstore; it did
not prove external datastore or live-host behavior. No generated files changed.

Design clarification on 2026-09-10 updated only this file and the parent index.
No Go changes or new Go verification runs accompanied that documentation update.

## Current handoff

Read workers-cleanup.md for final implementation, verification and changed files,
and AUDIT-007 for consumer migration. Generic SDK runners and middleware remain;
job middleware uses an error-only processor and optional atomic deferral ports.
Do not revive the superseded runner/middleware removal proposal. Full jobs/events
pocket audits and durable SQL latest-generation ordering remain open. S8
cacher/ratelimiter/events is the next SDK review.

The owner refreshed GPS360 main during implementation. Read-only inspection of
clean e1ab3f0 (echo/delivery/comps workers and jobs UI) supersedes the original
GPS360 usage/pin snapshot: SDK v0.7.1, jobs/stores v0.5.0, no local replacements.
Host timeout wrappers support the new job-middleware boundary; none of the removed
SDK APIs are used there. Preserve ordinary queues, kind filtering, production-only
schedule wiring and intentional no-change/partial-success outcomes. Potential
last-race cancellation swallowed as a successful comps job is recorded as a
consumer follow-up, not a framework regression or a completed consumer fix.
