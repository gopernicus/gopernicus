# Phases 0-1 — contracts and generic jobs hardening

Read `00-overview.md`, `sdk/foundation/workers`, `features/jobs`, the current
authentication `domain/deliveryjob`, delivery worker, store conformance, and both
features' migrations before changing code.

## Phase 0 outcome

Freeze the observable authentication guarantees and the reusable jobs contracts
before adapters or deletion. Characterization tests must fail if the new runtime
would reissue a secret on retry, leak enumeration, accept a stale claim, or turn
resend into duplicate active work.

### AV3D-0.1 — ratify modes and construction matrix

Specify public delivery-mode vocabulary, enablement, production acknowledgments,
required ports, host lifecycle ownership, and incompatible combinations.

Acceptance:

- empty/unknown mode fails loudly;
- `off` fails when any enabled flow can deliver;
- `jobs` requires the narrow generic queue capabilities, an encrypter, and a
  runtime acknowledgment in production;
- `in_process` rejects durable-status claims and requires explicit production
  crash-loss acknowledgment;
- transport capability checks remain unchanged; and
- `Register` starts no worker.

Verify with table-driven authentication construction tests and public doc
comments.

### AV3D-0.2 — characterize existing delivery behavior

Extract black-box tests from the current worker/service before refactoring:

- duplicate admission creates one active execution;
- replace creates fresh work and supersedes prior active work;
- opaque initialization is off request path;
- checkpoint precedes provider send and retry reuses byte-identical payload;
- crash-after-send may duplicate the same secret, never mint a new one;
- unknown/ineligible identifier skips without provider call;
- transient/permanent/terminal paths, backoff, timeout, discard, purge, and
  status are observable without secret leakage; and
- queue/provider failure responses preserve known/unknown parity.

The suite must be transport-neutral so phases 3 and 4 run the same cases.

### AV3D-0.3 — freeze generic jobs extension vocabulary

Add domain/port specifications and conformance test skeletons for:

- unique execution ID plus optional logical key;
- atomic enqueue-once and atomic replace/supersede by logical key;
- `canceled`/`superseded` terminal state and latest-by-key status;
- worker-owned claim fencing on checkpoint/complete/fail;
- atomic encrypted-payload checkpoint while a claim is current;
- retry-at scheduling, permanent failure, resulting transition, and dead-letter
  hook;
- bounded terminal purge; and
- deterministic conflict/not-found/already-exists behavior.

Prefer stdlib-typed service methods for cross-feature structural ports. Domain-rich
host APIs may remain alongside them.

### Phase 0 gate

Run authentication delivery characterization, jobs storetest compile, sdk/workers
tests, `make check`, and `make guard`. No queue implementation is deleted yet.

## Phase 1 outcome

Make generic jobs strong enough to carry authentication delivery without an
auth-specific persistence or worker domain.

### AV3D-1.1 — lease-fenced kernel and queue transitions

Change the worker/store contract so completion and failure identify the current
claim owner. A reclaimed or superseded claim returns conflict. The runner must not
report a stale completion as success.

Prove:

- two claimers never own one current lease;
- after lease expiry/reclaim, the old owner cannot checkpoint, complete, fail, or
  reschedule;
- the current owner can complete/fail idempotently; and
- cancellation during shutdown leaves work reclaimable without the old worker
  later clobbering it.

Update every sdk/workers and jobs test double in the same task.

### AV3D-1.2 — logical-key admission and supersession

Add logical-key storage and atomic operations:

- enqueue-once returns the current active job for the same key;
- replace terminally supersedes all active generations and inserts one new job;
- distinct execution IDs retain history;
- latest-by-key is deterministic; and
- replacement fences old claims, even if already running.

Document the irreducible race: replacement cannot retract a provider call already
accepted. Conformance must cover concurrent enqueue/replace and one-winner
behavior.

### AV3D-1.3 — claimed-payload checkpoint

Add a payload update that succeeds only for the running job and current worker.
It preserves job identity, attempts, logical key, schedule, and status. The updated
bytes must round-trip exactly in every store.

Prove checkpoint-before-side-effect and recovery:

- checkpoint failure prevents the side effect;
- after checkpoint + crash, reclaim reads the checkpointed bytes;
- a stale or superseded worker cannot checkpoint; and
- concurrent checkpoint/replacement has one valid outcome with no lost current
  generation.

### AV3D-1.4 — retry policy, terminal callback, and purge

Support context-cancellable retry-at scheduling and permanent failure without
busy-looping. Expose the successful resulting transition so a per-kind terminal
hook runs only after dead-letter is recorded. Hook failure is logged/reported but
does not resurrect the job.

Add bounded purge by terminal time and limit. Default behavior for existing jobs
consumers must be documented and compatibility-tested.

### AV3D-1.5 — memory/pgx/turso stores and live race proof

Implement the phase-1 contract in memory, pgx, and turso with matching canonical
migration filenames. Extend the shared storetest suite rather than adding
dialect-only semantics.

Required proof:

- repeated stale-claim/reclaim races;
- concurrent enqueue-once and replace;
- checkpoint versus replace;
- retry-at ordering and permanent dead-letter;
- latest-by-key and bounded purge;
- byte-exact encrypted payload round-trip; and
- fresh/reset live pgx and turso runs with recorded skips/failures.

### Phase 1 gate

Run jobs and sdk/workers suites under `-race`, both live store suites, migration
parity, `make check`, and `make guard`. Authentication still uses its existing
queue until this gate is green.

## Execution log

Append dated entries with task ID, files, exact commands, pass/skip/failure, live
DSN availability, and any premise adaptation.

### 2026-07-13 — preflight (pre-AV3D-0.1)

- Board state: `AV3-9.1`–`AV3-9.6` checked complete; `AV3-9.7`/`AV3-9.8` open
  (`.claude/plans/authv3/TASKS.md`).
- `git status --short`: **clean**. Premise adaptation: the overview expected a
  dirty auth-v3 worktree; the auth-v3 work was committed as `d6a52ae authv3`
  on `main`. No unrelated changes to preserve; that commit is not rewritten.
- Baselines (all pass):
  - `cd features/jobs && go build ./... && go test ./... && go vet ./...`
  - `cd features/authentication && go build ./... && go test ./... && go vet ./...`
  - `cd sdk && go build ./... && go test ./foundation/workers/... && go vet ./foundation/workers/...`
  - `make guard` — all guards green
  - `make check` — "all checks passed"
- Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` unset. Live
  pgx/turso proofs in AV3D-1.5/3.5 will record loud skips unless env is
  provided.
- Ratification: owner ratified the two-mode decision and production
  acknowledgment posture via the 2026-07-13 loop directive (jobs = durable
  delivery execution; events = optional observation only; `in_process` is an
  explicitly selected bounded pool; production fails closed on incomplete jobs
  wiring or unacknowledged ephemeral delivery).

### 2026-07-13 — AV3D-0.1 (ratify delivery modes and construction matrix)

Contract only: froze the public delivery-mode vocabulary, config fields, and loud
`NewService` construction validation. No runtimes, processor, or generic-jobs
changes (deferred to phases 1-4). Authentication imports no jobs feature; the
jobs-facing seam remains the existing stdlib-typed `Repositories.DeliveryJobs`
structural port until the generic-jobs vocabulary lands (AV3D-0.3 / phase 1).

Frozen names (chosen after repo-compatibility check):

- `DeliveryMode` (`"off"` / `"in_process"` / `"jobs"`), `Config.DeliveryMode`
  (`env:"AUTH_DELIVERY_MODE"`), REQUIRED enum with no default (the `RuntimeMode`
  precedent).
- Config acks: `DeliveryWorkerAcknowledged` **renamed/split** into
  `DeliveryJobsAcknowledged` (jobs runtime) + `DeliveryEphemeralAcknowledged`
  (in_process crash-loss).
- Errors: `ErrDeliveryModeRequired`, `ErrDeliveryModeInvalid`,
  `ErrDeliveryOffButDeliverable`, `ErrDeliveryQueueRequired`,
  `ErrInProcessDurableDeliveryRepository`, `ErrDeliveryJobsUnacknowledged`
  (renamed from `ErrDeliveryWorkerUnacknowledged`), `ErrDeliveryEphemeralUnacknowledged`.

Matrix (validated in `NewService`, `DeliveryMode` gate placed right after
`validateRuntimeMode`; capability/ack matrix in the delivery block):

- empty → `ErrDeliveryModeRequired`; unknown → `ErrDeliveryModeInvalid`.
- `off`: rejects a wired queue (`ErrDeliveryOffButDeliverable`); an enabled
  passwordless flow under `off` is `ErrPasswordlessDeliveryRequired` (the existing
  gate, kept meaningful). Verification/forgot-password/invitations route through
  the same queue, so `off` uniformly disables them rather than erroring.
- `jobs`: requires `Repositories.DeliveryJobs` (`ErrDeliveryQueueRequired`) +
  `DeliveryEncrypter`; production also requires a durable store
  (`ErrNonDurableDeliveryRepository`, jobs-mode-only now) + `DeliveryJobsAcknowledged`
  (`ErrDeliveryJobsUnacknowledged`).
- `in_process`: rejects a repository that positively claims durability
  (`ErrInProcessDurableDeliveryRepository`, every RuntimeMode); production also
  requires `DeliveryEphemeralAcknowledged` (`ErrDeliveryEphemeralUnacknowledged`).
- transport capability checks (`validateDeliveryTransports`) UNCHANGED.
- `Register` starts no worker (behavioral test asserts the queue's `Claim` is
  never called after `Register`).

Files changed:

- `features/authentication/security.go` — `DeliveryMode` type/consts + doc; new
  errors; `validateDeliveryMode`, `deliveryClaimsDurable`; renamed worker-ack error.
- `features/authentication/authentication.go` — `Config.DeliveryMode` +
  `DeliveryJobsAcknowledged`/`DeliveryEphemeralAcknowledged`; `NewService`
  delivery-mode gate + matrix block.
- `features/authentication/delivery_mode_test.go` — NEW table-driven matrix +
  `Register`-starts-no-worker + mode-after-RuntimeMode ordering tests.
- `features/authentication/security_test.go`, `auth_test.go` — added the required
  `DeliveryMode` to existing construction configs; renamed ack field/error.
- `features/authentication/README.md` — Config table + posture doc for the new
  vocabulary.
- `examples/auth-cms/cmd/server/main.go` — host configured `DeliveryMode: jobs` +
  `DeliveryJobsAcknowledged: true` (dev tolerates the in-memory stand-in; its
  production-negative matrix asserts durable-store/ack semantics).
- `examples/auth-cms/cmd/server/{main_test.go,production_test.go}`,
  `examples/auth-cms/README.md` — field/error renames + a `DeliveryMode` assertion.

Premise adaptation: the overview's "Public-boundary direction" also calls for
REMOVING `Repositories.DeliveryJobs` / `domain/deliveryjob.Repository` and
introducing generic stdlib queue/processor seams — that is phase 1-5 work
(characterization lands before deletion, per the execution protocol), so this
contract task keeps the existing `DeliveryJobs` slot as the `jobs`-mode queue
capability and does not delete it. `ErrPasswordlessDeliveryRequired` is retained
(the `off`+passwordless gate keeps it reachable); under `jobs` mode the queue
requirement is now enforced upstream by `ErrDeliveryQueueRequired`. auth-cms is
configured `jobs` (not `in_process`) because its production-negative suite proves
the same wiring flipped to production fails closed on the non-durable store and
unacknowledged runtime — jobs-mode semantics; dev tolerates the in-memory outbox.

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test ./... && go vet ./...`
- `cd examples/auth-cms && go build ./... && go test ./...`
- `make guard` — all guards green.
- `make check` — "all checks passed" (host wiring touched).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` unset. This is a
hermetic construction-contract task; no live-store proof required (deferred to
AV3D-1.5/3.5).

### 2026-07-13 — AV3D-0.2 (characterize existing delivery behavior)

Extracted a TRANSPORT-NEUTRAL characterization suite from the current delivery
worker/queue BEFORE any refactor. The reusable case table + assertions live behind
a narrow `Harness` seam (submit / drain / advance / purge / status) so phases 3
(generic-jobs mode) and 4 (bounded in-process mode) run the identical cases against
their runtimes; the current bespoke worker is the first `Harness` implementation.
Tests + minimal test-support only — no production behavior changed.

Placement (per repo conventions — mirrors the `storetest` reference-suite shape):

- `features/authentication/internal/logic/delivery/deliverychar/` — NEW regular
  (non-test) package, stdlib+sdk only, importing NEITHER the delivery package it
  characterizes nor any driver (so the delivery-package test can adapt it with no
  import cycle, exactly like `storetest`). Holds the neutral vocabulary
  (`Submission`, `Rendered`, `Send`, `Observation`), the recording `Provider`
  (a `notify.Notifier` every mode wires unchanged), the neutral `Initializer`
  stub, `Scenario`, the `Harness`/`Factory` seam, and `Run(t, Factory)` executing
  12 cases as subtests.
- `features/authentication/internal/logic/delivery/characterization_test.go` — the
  first `Harness` adapter (`workerHarness`) over `delivery.Worker` + `delivery.Service`
  + the existing worker_test doubles, and `TestCharacterization` calling
  `deliverychar.Run`.

Cases (each maps to a frozen guarantee): duplicate admission → one execution;
replace supersedes prior active work; opaque initialization is off the request
path (no resolve/send on submit); checkpoint precedes send + retry reuses a
byte-identical secret (initializer resolves exactly once across retries);
crash-after-send replays the SAME secret (never mints a new one); unknown/
ineligible identifier skips with no provider call and a non-failed terminal;
transient retry-then-succeed; permanent/terminal failure + challenge discard;
provider-timeout boundedness (a blocked send returns within the deadline);
purge retention; status is lifecycle-only (a full dump never contains the secret;
unknown key reveals no work); known/unknown REQUEST-PATH parity.

Files changed:

- `features/authentication/internal/logic/delivery/deliverychar/deliverychar.go` — NEW
  neutral suite vocabulary + `Provider`/`Initializer`/`Scenario`/`Harness`/`Factory`.
- `features/authentication/internal/logic/delivery/deliverychar/cases.go` — NEW
  `Run(t, Factory)` + the 12 case functions + the bounded `settle` driver.
- `features/authentication/internal/logic/delivery/characterization_test.go` — NEW
  `workerHarness` adapter + `TestCharacterization`.
- `features/authentication/internal/logic/delivery/worker_test.go` — test-support
  fix only: the local `memRepo.GetLatestByIdempotencyKey` now tiebreaks by `id`
  on equal `created_at`, matching the canonical stores
  (`stores/{pgx,turso}`: `ORDER BY created_at DESC, id DESC`) and the `storetest`
  reference. See the discrepancy note below.

Premise adaptation: the phase-file case list names "queue/provider failure
responses preserve known/unknown parity". Characterized as REQUEST-PATH parity
(both known and unknown admit to the identical pending shape with zero provider
calls on submit, and both reach a non-failed terminal under steady state). The
post-delivery lifecycle deliberately DIVERGES only under a genuine provider outage
(a known deliverable can end `failed` while an unknown ends succeeded/skipped) —
that is the documented at-least-once/skip semantics and a transport-failure
condition, not a steady-state request-path enumeration signal, so it is described
in the case comment and NOT asserted as parity. No guarantee weakened.

Discovered guarantee discrepancy (test-double only, NOT production): the delivery
worker's opaque-init path Replaces the opaque job with the rendered job in the same
clock tick, so the tombstone and the live job share `created_at`. The canonical
`GetLatestByIdempotencyKey` contract resolves ties by `id DESC` (both real stores
and the `storetest` reference implement it), so status correctly returns the live
rendered job. The delivery package's LOCAL worker_test `memRepo` double omitted that
tiebreak and non-deterministically returned the canceled opaque tombstone (a
terminal, non-failed row that reads as "delivered"), which surfaced as flaky
early-termination in the characterization `settle`. Fix aligned the double with the
frozen contract; the real stores were already correct, so no production change and
no contract weakened. Existing worker_test cases never exercised
`GetLatestByIdempotencyKey` on a same-tick tie, so they were unaffected.

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test ./... && go vet ./...`
- `cd features/authentication && go test -race -count=5 -run TestCharacterization ./internal/logic/delivery/` (deterministic across 5 iterations under -race)
- `make guard` — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. This is a hermetic behavioral-characterization task run
against the in-test reference double; no live-store proof required (deferred to
AV3D-1.5/3.5). The suite is store-agnostic — a future adapter can run it against a
live-backed runtime unchanged.

### 2026-07-13 — AV3D-0.3 (freeze generic jobs extension vocabulary)

SPECIFICATION ONLY: froze the generic-jobs hardened-queue extension vocabulary —
domain/port specs, entity vocabulary, the stdlib-typed cross-feature seam, and the
conformance-suite skeleton — with NO implementation and NO behavior change to the
current queue. Existing jobs consumers compile unchanged; every addition is
additive (new constants, new zero-valued Job/Enqueue fields, new interfaces, a new
skeleton suite). No store implements the new port yet (that is phase 1); the
skeleton is gated to skip until then.

Frozen names (chosen after repo-compatibility check against the current
`job.QueueRepository`/`workers.JobStore[Job]` superset and the authentication
`deliveryjob.Repository` this will eventually replace):

- Terminal states: `job.StatusCanceled` (`"canceled"`, explicit Cancel) and
  `job.StatusSuperseded` (`"superseded"`, replaced under a logical key) — both
  distinct so latest-by-key can tell an explicit cancel from a resend-supersede.
- `job.Job` fields: `LogicalKey` (optional PII-free idempotency/supersession key,
  distinct from `JobID` the unique execution ID), `LeaseID`+`LeasedUntil` (the
  per-claim fence, distinct from the reusable `WorkerName`), `TerminalAt *time.Time`
  (the purge cursor). Predicates `Job.Terminal()` and `Job.Leased(now)`.
- `job.Enqueue.LogicalKey` (optional; inert on the current `QueueRepository.Enqueue`).
- `job.FencedQueueRepository` (domain-rich port; new file `domain/job/fenced.go`):
  `EnqueueOnce` (enqueue-once by key, returns the active generation), `Replace`
  (supersede active generations + insert one fresh), `Claim(now, leaseID, leaseFor)`
  (lease-fenced), `Checkpoint(id, leaseID, payload, now)` (byte-exact, fenced),
  `Complete`/`Reschedule`(retry-at)/`Fail`(permanent dead-letter) all fenced by
  `leaseID`, `Cancel(id, now)`, `PurgeTerminal(before, limit)`,
  `GetLatestByKey`, `Get`. Error semantics via sdk kinds: reclaimed/superseded/
  terminal → `sdk.ErrConflict`; absent → `sdk.ErrNotFound`; duplicate execution ID
  → `sdk.ErrAlreadyExists`; empty queue → `workers.ErrNoWork`; terminal completions
  idempotent-nil from the last holder.
- Stdlib-typed cross-feature seam (new file `features/jobs/primitives.go`; the
  projection authentication matches structurally without importing jobs, rule 6):
  `KeyedEnqueuer` (`EnqueueOnce`/`Replace` over `string`+`json.RawMessage`, returns
  the execution ID), `KeyStatusReader` (`LatestStatusByKey` → lifecycle string),
  `Checkpointer` (`Checkpoint(executionID, leaseID, payload)`), and the frozen
  per-kind terminal hook type `jobs.DeadLetterFunc`. The Service satisfies these at
  phase 1 (compile assertions intentionally deferred; the domain-rich `EnqueueJob`
  and the port remain available alongside).

Conformance skeleton (`features/jobs/storetest/fenced.go`): `RunFencedQueue(t,
newRepo func(*testing.T) job.FencedQueueRepository)` with 11 cases mapping 1:1 to
the frozen guarantees — unique-ID/optional-key, enqueue-once-returns-active,
replace-supersedes, latest-by-key, canceled/superseded terminal, claim fencing on
stale lease (checkpoint/complete/fail → conflict), byte-exact checkpoint while
claim current, retry-at scheduling, permanent-failure dead-letter, bounded terminal
purge, and deterministic conflict/not-found/already-exists. Assertions are written
in full so they become load-bearing unchanged when phase 1 wires a real factory;
`requireFenced` skips each case cleanly until then. `storetest/reference_test.go`
adds `TestReferenceFencedQueue` passing a nil factory (phase 1 AV3D-1.5 swaps in
`memstore.NewFencedQueue(...)`), so all 11 cases record as SKIP, not a false green.

Files changed:

- `features/jobs/domain/job/job.go` — `StatusCanceled`/`StatusSuperseded`; Job
  `LogicalKey`/`LeaseID`/`LeasedUntil`/`TerminalAt` fields + `Terminal()`/`Leased()`
  predicates; `Enqueue.LogicalKey`.
- `features/jobs/domain/job/fenced.go` — NEW `FencedQueueRepository` port + doc/error
  spec.
- `features/jobs/primitives.go` — NEW stdlib-typed seam (`KeyedEnqueuer`,
  `KeyStatusReader`, `Checkpointer`) + `DeadLetterFunc`.
- `features/jobs/storetest/fenced.go` — NEW `RunFencedQueue` skeleton (11 gated
  cases) + `requireFenced`/`mustEnqueueOnce`.
- `features/jobs/storetest/reference_test.go` — `TestReferenceFencedQueue` (nil
  factory → skips).

Premise adaptation: the phase-0 brief and overview also call for eventually
REMOVING authentication's `Repositories.DeliveryJobs` / `deliveryjob.Repository`
and hardening the existing `QueueRepository`/`workers.JobStore` in place (fencing
`Claim`/`Complete`/`Fail`). Those are phase-1 (AV3D-1.1..1.5) and phase-5 changes —
they mutate live signatures and every test double, which this additive spec task
must not do (execution protocol: contracts/conformance land before adapters or
deletion; "no behavior change to the current queue in this task"). So the frozen
fenced contract lands as a SEPARATE `FencedQueueRepository` alongside the untouched
`QueueRepository`; phase 1 reconciles/merges the two (and updates sdk/workers +
jobs doubles) under `-race`. `sdk/errs` in the standing prompt maps to the root
`sdk` package's error kinds in this repo (`sdk.ErrConflict`, `sdk.ErrNotFound`,
`sdk.ErrAlreadyExists`); those are the frozen error semantics.

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test ./... && go vet ./...`
- `cd features/jobs && go test -run TestReferenceFencedQueue -v ./storetest/` — all
  11 fenced cases SKIP cleanly (visible, not absent); the module stays green.

Phase 0 gate (AV3D-0.3 closes phase 0 — all PASS):

- `cd features/authentication && go test ./internal/logic/delivery/...` — PASS
  (delivery characterization unchanged).
- jobs storetest compile + run — PASS (`go build ./...` + `go test ./storetest/`).
- `cd sdk && go test ./foundation/workers/...` — PASS (unchanged behavior; no
  sdk/workers edit in this task).
- `make check` — "all checks passed" (per-module vet/build/test + integration-tag
  compile-only vet + all guards).
- `make guard` — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. Hermetic specification task — no live-store proof
required; the fenced conformance suite runs live against pgx/turso at AV3D-1.5.

### 2026-07-13 — AV3D-1.1 (lease-fenced kernel and queue transitions)

Implemented the lease/claim fence in the kernel and the fenced queue's transitions,
ADDITIVELY, and wired the memory reference so the fencing conformance case now runs.
Completion and failure identify the current claim owner by a per-claim lease token;
a reclaimed or superseded holder is fenced with `sdk.ErrConflict`; the fenced runner
never reports a stale completion as success.

QueueRepository/FencedQueueRepository RECONCILIATION DECISION (the call the phase file
left to this task) — **keep the two side by side; do not merge in 1.1**:

- The existing unfenced `workers.JobStore` / `job.QueueRepository` / `memstore.Queue`
  (`NewQueue`) / `stores/pgx` / `stores/turso` and the cron/schedule `runtime`
  (a `workers.Runner` over `QueueRepository`) are UNTOUCHED. A single Go store type
  cannot expose both `Claim(workerID,now)`/`Complete(id,now)`/`Fail(id,now,reason,max)`
  and the lease-fenced `Claim(now,leaseID,leaseFor)`/`Complete(id,leaseID,now)`/
  `Fail(id,leaseID,reason,now)` shapes (same method names, different signatures), so
  the fence lands on the frozen `FencedQueueRepository` path as a SEPARATE type
  (`memstore.FencedQueue`, `NewFencedQueue`).
- Rationale: the phased plan scopes pgx/turso re-implementation to AV3D-1.5 and the
  retry-at/permanent-failure semantics (fenced `Fail` = permanent dead-letter, no
  `maxAttempts`; retry is `Reschedule`) to AV3D-1.4. Merging `QueueRepository` into the
  fenced shape now would (a) break every `JobStore`/`QueueRepository` implementer
  repo-wide — including the two live SQL stores reserved for 1.5 — and (b) change the
  cron runtime's retry model out of phase. The additive path keeps existing consumers
  (`examples/jobs-minimal`, the schedule runtime) compiling and green while proving the
  fence. The eventual retirement of the bespoke `QueueRepository`/`Runner` path is the
  phase-5 migration, per AV3D-0.3's own "phase 1 reconciles/merges the two" note —
  "reconcile" here means "make the fenced path exist and pass", not "delete the old one
  mid-phase".

Default-behavior note for existing consumers where semantics change: NONE. No existing
symbol's signature or behavior changed. `workers.JobStore`, `workers.Runner`,
`job.QueueRepository`, `memstore.Queue`, both SQL stores, `queuesvc`, `runtime`, and
`jobs.NewService/NewRuntime` are byte-for-byte behaviorally identical. The fenced
kernel (`workers.FencedStore` + `workers.FencedRunner`) and `memstore.FencedQueue` are
NEW surface with no current production consumer; authentication still runs its bespoke
queue (untouched, per the phase-1 gate).

Kernel change (sdk/foundation/workers): added `FencedStore[T]` (Claim stamps a
caller-supplied fresh lease; Complete/Fail require it, returning `sdk.ErrConflict` on a
reclaimed/superseded lease and idempotent-nil from the last holder) and
`FencedRunner[T]` (mints a fresh per-claim lease, threads it through Complete/Fail,
recognizes `sdk.ErrConflict` as a fenced no-op rather than a success, and on context
cancellation leaves the in-flight job reclaimable — it does not Complete or Fail). This
is the first `sdk/foundation/workers → sdk` (root kernel) import; G12b allows foundation
→ root, and `make guard`/`make check` stay green. Within-claim retry and pre/post hooks
were deliberately NOT duplicated onto the fenced path (YAGNI until a consumer needs them);
retry-at/dead-letter-hook are AV3D-1.4.

Store change (features/jobs/memstore): `FencedQueue` implements the full
`job.FencedQueueRepository` (a strict superset of `workers.FencedStore`, asserted at
compile time in both `domain/job/fenced.go` and the store). The lease-fenced transitions
(Claim with stale-lease reclaim, Checkpoint/Complete/Fail/Reschedule fenced by
`heldBy(leaseID, now)`, idempotent terminal completions) are the AV3D-1.1 deliverable and
are load-bearing. `EnqueueOnce`/`Replace`/`Cancel`/`PurgeTerminal`/`GetLatestByKey` are
implemented so the port is satisfied, but their shared conformance cases stay SKIPPED
(new `deferredCase` gate) until AV3D-1.2/1.3/1.4 activate them with pgx/turso parity — no
false green claimed for later-task behavior.

Proof mapping (acceptance → test):

- two claimers never own one current lease + old owner fenced on reclaim → storetest
  `ClaimFencingOnStaleLease` (now RUNS, real 3.1s lease-expiry sleep; stale
  Checkpoint/Complete/Fail all → `sdk.ErrConflict`), plus store double invariants in
  `sdk/foundation/workers/fenced_test.go`.
- current owner completes/fails idempotently → `ClaimFencingOnStaleLease` (added a
  repeat `Complete` from the current holder → nil) + `TestFencedRunner_*`.
- runner must not report a stale completion as success →
  `TestFencedRunner_StaleCompletionNotReportedAsSuccess` (a reclaim lands mid-process; the
  stale lease's Complete is fenced and the current execution is intact).
- cancellation during shutdown leaves work reclaimable without later clobber →
  `TestFencedRunner_ShutdownLeavesReclaimable`.

Files changed:

- `sdk/foundation/workers/fenced.go` — NEW `FencedStore[T]` port + `FencedRunner[T]`
  (lease-token minting, conflict-aware Complete/Fail, shutdown-reclaimable path).
- `sdk/foundation/workers/fenced_test.go` — NEW fenced store double + 5 runner tests.
- `features/jobs/domain/job/fenced.go` — added the `workers.FencedStore[Job]` compile
  assertion (import of `sdk/foundation/workers`).
- `features/jobs/memstore/fenced.go` — NEW `FencedQueue` / `NewFencedQueue` implementing
  `job.FencedQueueRepository`.
- `features/jobs/storetest/fenced.go` — added the `deferredCase` per-task gate; marked the
  eight logical-key/checkpoint/retry-at/purge cases deferred; un-gated
  `ClaimFencingOnStaleLease` and strengthened it with the idempotent-repeat assertion.
- `features/jobs/storetest/reference_test.go` — `TestReferenceFencedQueue` now wires
  `memstore.NewFencedQueue()` (was a nil factory).

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...`
- `cd sdk && go build ./... && go test -race ./foundation/workers/... && go vet ./foundation/workers/...`
- `cd examples/jobs-minimal && go build ./... && go test ./...`
- `make guard` — all guards green.
- `make check` — "all checks passed".
- `cd features/jobs && go test -run TestReferenceFencedQueue -v ./storetest/` — 1 case
  RUN+PASS (`ClaimFencingOnStaleLease`, 3.10s), 10 cases SKIP (visible deferred gates).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. Hermetic task run against the in-core memstore reference; the
fenced conformance suite (and its stale-claim/reclaim races) runs live against pgx/turso
at AV3D-1.5, where the two SQL stores implement the same port under the same suite.

Premise adaptation: the phase-file line "Change the worker/store contract" is realized
ADDITIVELY (new `FencedStore`/`FencedRunner` alongside the untouched `JobStore`/`Runner`),
not by mutating the existing signatures — the only reading that keeps pgx/turso (1.5) and
the retry model (1.4) in their phases while still fencing completion/failure now. "Update
EVERY sdk/workers and jobs test double" is satisfied by keeping the unchanged unfenced
doubles compiling and adding fenced doubles for the new path; no existing double needed a
signature change because no existing signature changed.

### 2026-07-13 — AV3D-1.2 (logical-key admission and atomic supersession)

Made the logical-key admission/supersession conformance cases LOAD-BEARING against the
memstore reference and documented the irreducible replacement race in the port. No
production behavior changed: the AV3D-1.1 memstore already implemented the logical-key
methods correctly, so this task un-gated the six AV3D-1.2 `deferredCase` entries, added
three concurrency cases (run under `-race`), corrected one latent test-setup ordering
bug, and expanded the port doc. The unfenced legacy path (`QueueRepository`/`memstore.Queue`
/SQL stores/cron runtime) is untouched. SQL stores (pgx/turso) still land at AV3D-1.5.

Cases activated (deferredCase removed; RUN+PASS against `memstore.NewFencedQueue`):
`UniqueExecutionIDAndLogicalKey`, `EnqueueOnceReturnsActive`, `ReplaceSupersedesActive`,
`LatestByKeyDeterministic`, `TerminalCanceledAndSuperseded`, `ConflictNotFoundAlreadyExists`.
Still deferred (correctly SKIP): `CheckpointWhileClaimCurrent` (AV3D-1.3, byte-exact
payload round-trip while the claim is CURRENT), `RetryAtScheduling` / `PermanentFailureDeadLetter`
/ `BoundedTerminalPurge` (AV3D-1.4). `ClaimFencingOnStaleLease` remains the AV3D-1.1
load-bearing case (3.10s real lease-expiry).

Concurrency cases added (new, AV3D-1.2, load-bearing, `-race`):

- `ConcurrentEnqueueOnceSameKey` — 16 goroutines EnqueueOnce one key; asserts every
  caller receives the SAME single execution id (a lost-update double insert would hand
  out distinct ids) and that the one admitted generation is the active latest-by-key.
- `ConcurrentReplaceVsEnqueueOnce` — Replace racing EnqueueOnce on a seeded active key;
  asserts exactly one active generation with Replace deterministically winning the active
  slot, the concurrent EnqueueOnce creating no third generation (it returns the seed or the
  replacement), and the seed left superseded/terminal — one-winner behavior under either
  interleaving.
- `ReplaceFencesRunningClaim` — Replace supersedes a RUNNING claim, then the still-live
  old lease holder's Checkpoint/Complete/Fail each return `sdk.ErrConflict` (supersession,
  not lease expiry, retires the claim — no `time.Sleep`), replacement is the one active
  generation. This is the "replacement fences old claims even if already running" acceptance
  point; Checkpoint/Complete/Fail here are used in their CONFLICT sense (1.2 fencing), not
  the byte-exact/retry-at conformance reserved for 1.3/1.4.

Irreducible race documented in the port (`domain/job/fenced.go`, `FencedQueueRepository.Replace`):
the forward-reference "documented at AV3D-1.2" was replaced with the explicit statement that
Replace fences the superseded worker's queue transitions but CANNOT retract a provider call
that worker already made — a provider send is an external side effect the queue never
observes — so delivery is at-least-once across a replacement, not exactly-once; single-use
redemption at the auth layer is preserved, and the queue's only guarantee is that no
superseded worker records state against or resurrects the retired generation. The
`ReplaceFencesRunningClaim` case comment states the same race honestly.

Memstore correction: NONE required — the AV3D-1.1 `memstore.FencedQueue` implementation was
already correct for every now-live case (single-mutex serialization gives one-winner
concurrency by construction; `Replace` clears the lease and stamps `StatusSuperseded`, and
`heldBy` requires `StatusRunning` + matching unexpired lease, so a superseded holder is
fenced). No implementation line changed; no contract weakened.

Test-setup correction (not a contract change): `testFencedErrorKinds` seeded a leftover
pending `dup` execution BEFORE the "a completed job cannot be canceled" block, so `Claim`
(oldest-due-first) would have returned `dup` instead of the job under test, leaving `done`
pending and turning the intended terminal-conflict Cancel into a benign nil. Reordered the
block to run against an otherwise-empty store and added a `claimed.ID() == done.ID()` guard;
every assertion is preserved (the deterministic `Claim` oldest-first ordering is the reason
the reorder is required, and it is now asserted rather than assumed).

Files changed:

- `features/jobs/domain/job/fenced.go` — expanded `Replace` doc with the explicit irreducible
  -race statement (replaced the AV3D-1.2 forward reference).
- `features/jobs/storetest/fenced.go` — removed six AV3D-1.2 `deferredCase` gates; added
  `sync` import; added `ConcurrentEnqueueOnceSameKey` / `ConcurrentReplaceVsEnqueueOnce` /
  `ReplaceFencesRunningClaim` cases + their `t.Run` registrations; reordered
  `testFencedErrorKinds`; updated the `deferredCase` doc to note 1.2 activation.
- `features/jobs/memstore/fenced.go` — UNCHANGED (already correct; noted for the record).

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...` — PASS.
- `cd features/jobs && go test -race -run TestReferenceFencedQueue -v ./storetest/` — the six
  logical-key/error-kind cases + three concurrency cases RUN+PASS; `ClaimFencingOnStaleLease`
  RUN+PASS (3.10s); only the four 1.3/1.4 cases SKIP (visible deferred gates).
- `cd features/jobs && go test -race -count=10 -run 'TestReferenceFencedQueue/(Concurrent|ReplaceFencesRunningClaim|LatestByKeyDeterministic)' ./storetest/` — deterministic across 10 iterations under `-race` (no flake, no data race).
- `cd sdk && go test -race ./foundation/workers/...` — PASS (unchanged; no sdk edit this task).
- `make guard` — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. Hermetic task run against the in-core memstore reference; the same
logical-key/supersession/concurrency cases run live against pgx/turso under `-race` at
AV3D-1.5, where the two SQL stores implement the same port under the same suite.

Premise adaptation: the phase-file line "replacement fences old claims, even if already
running" is proven via `ReplaceFencesRunningClaim`, which invokes `Checkpoint`/`Complete`/`Fail`
purely for their supersession-CONFLICT behavior — the byte-exact checkpoint round-trip
(AV3D-1.3) and retry-at/dead-letter conformance (AV3D-1.4) stay deferred, so no later-task
behavior is claimed green. No memstore correction was needed (the 1.1 implementation was
already conformant); the only code change to the reference module is the honest test-setup
reorder documented above.

### 2026-07-13 — AV3D-1.3 (claimed-payload checkpoint)

Made the claimed-payload checkpoint conformance LOAD-BEARING against the memstore reference
and proved the four AV3D-1.3 acceptance points (byte-exact round-trip incl. non-UTF8;
checkpoint-before-side-effect ordering; checkpoint+crash reclaim reads the checkpointed bytes;
concurrent checkpoint vs replace has one valid outcome with no lost current generation). No
production behavior changed: the AV3D-1.1 memstore already implemented Checkpoint correctly
(replaces only the payload under the current lease; heldBy fences a stale/superseded holder
with `sdk.ErrConflict`), so this task un-gated the AV3D-1.3 `deferredCase`, strengthened the
existing checkpoint case, added three new cases (two run a real lease-expiry sleep / a `-race`
concurrency), and made the consumer-ordering guarantee explicit in the port doc. The unfenced
legacy path (`QueueRepository`/`memstore.Queue`/SQL stores/cron runtime) is untouched. SQL
stores (pgx/turso) still land at AV3D-1.5.

FencedRunner checkpoint seam DECISION — **not added** (the call the task left open): the
checkpoint-before-side-effect guarantee is a CONSUMER-ordering property, and its proof is
STRUCTURAL — the queue supplies the seam (Checkpoint returns `sdk.ErrConflict` when the lease
is stale/superseded) and a correctly-ordered consumer must not send past that error. No
consumer wires a runner-level checkpoint yet (the auth processor is phase 2), so adding a
checkpoint capability to `sdk/foundation/workers.FencedRunner`/`FencedStore` now would be
speculative surface — the AV3D-1.1 YAGNI precedent (within-claim retry/hooks deliberately not
duplicated onto the fenced path until a consumer needs them). Instead the ordering is (a)
documented on `FencedQueueRepository.Checkpoint` as an explicit CONSUMER-ORDERING GUARANTEE
and (b) proven at the conformance level by the new `CheckpointBeforeSideEffect` case (a modeled
`checkpointThenSend` consumer that runs the side effect only on a nil checkpoint and is proven
NOT to send when the checkpoint is fenced). sdk was NOT touched this task; the sdk/workers gate
still runs green (below).

Cases activated (deferredCase removed; RUN+PASS against `memstore.NewFencedQueue`):

- `CheckpointWhileClaimCurrent` (strengthened) — checkpoint under the current lease replaces
  ONLY the payload, byte-for-byte, using a NON-UTF8 ciphertext (`{0x00,0x01,0xff,0xfe,0x80,
  0x7f,0x00,0xab}`) compared with `bytes.Equal`, and preserves identity, attempts (`Retries`),
  logical key, schedule, and `StatusRunning`; a stale lease → `sdk.ErrConflict`. The non-UTF8
  payload locks the AV3D-1.5 SQL stores to a BYTEA/BLOB column (never TEXT).
- `CheckpointBeforeSideEffect` (new) — the structural consumer-ordering proof described above.
- `CheckpointCrashReclaimReadsBytes` (new) — checkpoint, then a crash (lease lapses with no
  Complete/Fail, real `Lease+100ms` sleep), then a fresh Claim reclaims the SAME execution and
  reads the checkpointed non-UTF8 bytes verbatim (retry after crash resends the identical
  rendered secret).
- `ConcurrentCheckpointVsReplace` (new, `-race`) — a Checkpoint under the running claim racing
  a Replace of the same key resolves to exactly one legal outcome (checkpoint nil then the seed
  carries the bytes into its superseded tombstone, OR the checkpoint fenced with
  `sdk.ErrConflict`); in both interleavings the replacement is the single active generation
  (no lost current generation) and the seed is superseded/terminal.

"A stale or superseded worker cannot checkpoint" is covered by the strengthened
`CheckpointWhileClaimCurrent` (stale lease), the existing `ClaimFencingOnStaleLease`/
`ReplaceFencesRunningClaim` (AV3D-1.1/1.2, superseded), and the new `ConcurrentCheckpointVsReplace`.

Still deferred (correctly SKIP): `RetryAtScheduling` / `PermanentFailureDeadLetter` /
`BoundedTerminalPurge` (AV3D-1.4).

Files changed:

- `features/jobs/domain/job/fenced.go` — expanded `Checkpoint` doc: byte-exact incl. non-UTF8,
  and an explicit CONSUMER-ORDERING GUARANTEE (send only on a nil checkpoint).
- `features/jobs/storetest/fenced.go` — removed the AV3D-1.3 `deferredCase`; added the `bytes`
  import; strengthened `testFencedCheckpoint`; added `testFencedCheckpointBeforeSideEffect`,
  `testFencedCheckpointCrashReclaim`, `testFencedConcurrentCheckpointVsReplace` + their `t.Run`
  registrations; updated the `deferredCase` doc to note AV3D-1.3 activation.
- `features/jobs/memstore/fenced.go` — UNCHANGED (already correct; noted for the record).

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...` — PASS.
- `cd features/jobs && go test -race -run TestReferenceFencedQueue -v ./storetest/` — the four
  checkpoint cases (`CheckpointWhileClaimCurrent`, `CheckpointBeforeSideEffect`,
  `CheckpointCrashReclaimReadsBytes` (3.10s), `ConcurrentCheckpointVsReplace`) RUN+PASS; only
  the three AV3D-1.4 cases SKIP; all AV3D-1.1/1.2 cases still RUN+PASS.
- `cd features/jobs && go test -race -count=10 -run 'TestReferenceFencedQueue/(ConcurrentCheckpointVsReplace|CheckpointBeforeSideEffect)' ./storetest/` — deterministic across 10 iterations under `-race` (no flake, no data race).
- `cd sdk && go build ./... && go test -race ./foundation/workers/... && go vet ./foundation/workers/...` — PASS (sdk untouched this task).
- `make guard` — all guards green. `make check` NOT required (sdk not touched); guards are the boundary gate for this jobs-only task.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. Hermetic task run against the in-core memstore reference; the same
checkpoint/crash-reclaim/concurrent-checkpoint-vs-replace cases run live against pgx/turso under
`-race` at AV3D-1.5, where the two SQL stores implement the same port (the non-UTF8 payload case
forces their checkpoint column to a byte-exact BYTEA/BLOB).

Premise adaptation: the phase-file bullet "checkpoint failure prevents the side effect" is a
consumer-ordering guarantee proven STRUCTURALLY (modeled consumer + port doc), not by adding a
FencedRunner checkpoint seam — see the DECISION above. No memstore correction was needed (the
1.1 implementation was already conformant); the only reference-module change is additive test
surface plus the port-doc clarification.

### 2026-07-13 — AV3D-1.4 (retry policy, terminal callback, and bounded purge)

Activated the three remaining `deferredCase` fenced-conformance cases against the memstore
reference and hardened the sdk FencedRunner with context-cancellable retry-at rescheduling, a
bounded process timeout, and a post-dead-letter hook. The full fenced suite now RUNS with ZERO
deferred skips (only the real lease-expiry sleeps remain, ~3.1s each). SQL stores (pgx/turso)
still land at AV3D-1.5; the unfenced legacy path (`QueueRepository`/`workers.Runner`/`memstore.Queue`
/cron `runtime`/`examples/jobs-minimal`) is byte-for-byte unchanged.

Store level (memstore already implemented Reschedule/Fail/PurgeTerminal at AV3D-1.1, so NO
memstore code changed; the cases were un-gated and STRENGTHENED to be fully load-bearing):

- `RetryAtScheduling` — un-gated + strengthened: Reschedule moves the claimed job back to
  pending at a future retry-at, clears the lease (the old holder's Complete now → `sdk.ErrConflict`),
  records the reason, and is not re-served before retry-at (the no-busy-loop store proof — a
  per-minute poll never reclaims it early) but is at/after.
- `PermanentFailureDeadLetter` — un-gated: Fail dead-letters terminally and the job is never
  claimed again.
- `BoundedTerminalPurge` — un-gated + strengthened: purge is bounded BY LIMIT (two batches of 2
  drain three due terminal jobs as 2 then 1) and BY TERMINAL TIME (a job canceled AFTER the
  cutoff survives), and never touches the non-terminal live job.
- Removed the now-dead `deferredCase` helper (zero cases deferred) and refreshed the
  reference_test.go + file doc comments to record AV3D-1.4 activation.

Kernel level (sdk/foundation/workers/fenced.go) — the AV3D-1.1 scope note is now realized:

- `FencedStore` gains `Reschedule(id, leaseID, availableAt, reason, now)` — the same signature as
  `job.FencedQueueRepository.Reschedule`, so the superset compile assertion still holds and a
  FencedQueueRepository remains a drop-in FencedStore.
- `WithFencedRetry(FencedRetryFunc)` — on a process error the policy decides per attempt
  (`job.RetryCount()` after the claim increment) whether to Reschedule to `clock()+delay` (a
  DURABLE retry-at, NO in-runner sleep, so there is no backoff busy-loop to cancel — the strongest
  form of "context-cancellable backoff") or to dead-letter permanently. DEFAULT (no policy):
  first-failure dead-letter, the unchanged AV3D-1.1 behavior.
- `WithFencedProcessTimeout(d)` — wraps each ProcessFunc attempt in a child-context deadline that
  must sit inside the claim lease (provider timeout inside the lease, a standing invariant). This
  is the sole runner-owned wait, and it is context-cancellable: pool shutdown cancels the parent
  and the attempt returns promptly.
- `SetDeadLetterHook(FencedDeadLetterFunc[T])` — fires ONLY after a permanent Fail transition is
  recorded (Fail returned nil); NOT on a retry-at reschedule and NOT on a fenced Fail (this worker
  did not record the transition); a hook error is logged and swallowed, never resurrecting the job.
  A method (not an option) because the hook is generic over T, mirroring `Runner.AddPostProcessHooks`.

Seam (features/jobs): `primitives.go` adds a compile-time assertion that the frozen
`jobs.DeadLetterFunc` is exactly `workers.FencedDeadLetterFunc[job.Job]` (direct conversion, no
adapter), so a phase-3 durable-jobs runtime registers it via `FencedRunner.SetDeadLetterHook`; the
"inert until AV3D-1.4" doc is updated to reference the seam. Wiring a concrete features/jobs fenced
runtime is deferred to phase 3 (03-durable-jobs-mode.md), consistent with the AV3D-1.1/1.3 YAGNI +
phase-boundary discipline — the kernel owns the "fire after Fail records" mechanism generically now.

DEFAULT-behavior compatibility (documented + tested): the unfenced path is untouched (its runtime
tests still pass). The fenced runner's default is proven unchanged by
`TestFencedRunner_ProcessFailureFailsUnderLease` (no retry policy → first-failure dead-letter).

Proof mapping (acceptance → test):

- retry-at not claimed early (no busy-loop) → storetest `RetryAtScheduling` (store) +
  `TestFencedRunner_RetryReschedulesInsteadOfDeadLetter` (runner Reschedules to clock()+delay,
  clears the lease, does not dead-letter, does not fire the hook).
- permanent failure + bounded purge → storetest `PermanentFailureDeadLetter` / `BoundedTerminalPurge`.
- dead-letter hook fires only after the transition is recorded →
  `TestFencedRunner_RetryExhaustionDeadLetters` (hook observes status already `dead_letter`);
  not on a fenced Fail → `TestFencedRunner_DeadLetterHookNotFiredOnFencedFail`; never resurrects →
  `TestFencedRunner_DeadLetterHookErrorSwallowed`.
- provider timeout inside the lease + context-cancellable "backoff wait" returns promptly →
  `TestFencedRunner_ProcessTimeoutBoundsAttempt` (a blocked process is bounded well inside the
  2s lease by the 50ms timeout) + `TestFencedRunner_ProcessTimeoutCancelledPromptly` (parent
  cancellation returns in <1s of a 5s timeout, job left reclaimable).

Files changed:

- `features/jobs/storetest/fenced.go` — removed the three AV3D-1.4 `deferredCase` gates and the
  now-dead `deferredCase` helper; strengthened `testFencedRetryAt` (lease-cleared/reason/fenced
  old holder/not-early) and `testFencedPurge` (limit + terminal-time bounding, two-batch drain).
- `features/jobs/storetest/reference_test.go` — doc comment: all cases load-bearing, none deferred.
- `sdk/foundation/workers/fenced.go` — `FencedStore.Reschedule`; `FencedRetryFunc` /
  `FencedDeadLetterFunc[T]`; `WithFencedRetry` / `WithFencedProcessTimeout` options;
  `SetDeadLetterHook`; retry-at + process-timeout + post-Fail-hook wired into the work loop.
- `sdk/foundation/workers/fenced_test.go` — `Reschedule` on the fake store; 6 new runner tests.
- `features/jobs/primitives.go` — `DeadLetterFunc`↔`workers.FencedDeadLetterFunc[job.Job]` compile
  seam; refreshed the DeadLetterFunc doc.

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...` — PASS.
- `cd features/jobs && go test -race -run TestReferenceFencedQueue -v ./storetest/` — all 17 cases
  RUN+PASS (ClaimFencingOnStaleLease 3.10s, CheckpointCrashReclaimReadsBytes 3.10s), ZERO skips.
- `cd sdk && go build ./... && go test -race ./foundation/workers/... && go vet ./foundation/workers/...` — PASS
  (new runner tests deterministic across `-count=5 -race`).
- `cd examples/jobs-minimal && go build ./... && go test ./...` — PASS (no behavior change).
- `make guard` — all guards green. `make check` — "all checks passed" (sdk touched).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_*` (`TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN`) unset. Hermetic task against the in-core memstore reference; the same retry-at
/permanent-dead-letter/bounded-purge cases run live against pgx/turso under `-race` at AV3D-1.5.

Premise adaptation: the phase-file line "backoff waits are context-cancellable" is realized as
retry-at rescheduling with NO in-runner backoff sleep (the store will not re-serve before retry-at)
— a stronger guarantee than a cancellable sleep — so the only runner-owned cancellable wait is the
optional `WithFencedProcessTimeout` (the provider-timeout-inside-lease invariant), which is the
"backoff wait returns promptly" proof. The per-kind `jobs.DeadLetterFunc` is realized as the
kernel's generic post-Fail hook + a compile-time seam; the concrete durable-jobs runtime that
registers it is phase 3, not this task (no auth/jobs fenced runtime exists to wire yet).

### 2026-07-13 — AV3D-1.5 (pgx/turso fenced stores + live race proof; closes phase 1)

Implemented the frozen `job.FencedQueueRepository` in the pgx and turso store modules (memory
was already the AV3D-1.1..1.4 reference) and wired the shared `storetest.RunFencedQueue` suite for
both dialects following each store's existing conformance pattern (pgx env-gated by
`POSTGRES_TEST_DSN`; turso `//go:build integration` + `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`). No
dialect-only semantics were added — both SQL stores run the IDENTICAL 17-case suite the memstore
runs. The unfenced legacy path (`QueueRepository` / `job_queue` / cron runtime) is byte-for-byte
untouched; the fenced store is a SEPARATE type over a SEPARATE `fenced_job_queue` table (the
AV3D-1.1 coexistence decision), so no existing consumer signature changed.

Byte-exactness (the non-negotiable): the fenced checkpoint payload column is **BYTEA** (postgres)
/ **BLOB** (turso), never TEXT/JSON. The suite's `CheckpointWhileClaimCurrent` /
`CheckpointCrashReclaimReadsBytes` cases store arbitrary non-UTF8 ciphertext
(`{0x00,0x01,0xff,0xfe,0x80,0x7f,0x00,0xab}` etc.) and assert a `bytes.Equal` round-trip; a binary
column is the only thing that satisfies it. Payload is passed as raw `[]byte` (nil → empty blob for
the NOT NULL column), so encrypted bytes round-trip verbatim.

Concurrency/atomicity (dialect-appropriate primitives, identical semantics — the established
pattern where pgx uses `FOR UPDATE SKIP LOCKED` and turso uses single-writer serialization):

- **pgx**: keyed admission (`EnqueueOnce`/`Replace`) serializes per `logical_key` on a
  transaction-scoped `pg_advisory_xact_lock(hashtext(key))` so its read-then-write is atomic even
  for a brand-new key with no row to lock; the fenced transitions lock the target row `FOR UPDATE`
  and enforce the `lease_id` fence; `Claim` is one `UPDATE … WHERE job_id=(SELECT … FOR UPDATE SKIP
  LOCKED) … RETURNING`.
- **turso**: keyed admission and every fenced transition run inside `InTx` (BEGIN IMMEDIATE takes
  the write lock up front so SQLite's single-writer model serializes contenders and each
  read-then-write is atomic); contention surfaces as bounded `retryBusy` waiting, never a failed op.
- Backing invariant in BOTH schemas: a partial UNIQUE index `uq_fenced_job_queue_active_key ON
  fenced_job_queue (logical_key) WHERE logical_key IS NOT NULL AND status IN ('pending','running')`
  enforces at-most-one active generation per key at the DB level (keyless jobs store `logical_key`
  NULL, distinct in the index). Both stores insert via `INSERT … RETURNING` so a returned generation
  carries the stored (dialect-precision) timestamps a later `Get` reads back.

Migration parity (greenfield — canonical CREATE, not an ALTER): each tree gains
`0003_fenced_job_queue.sql` (postgres BYTEA/TIMESTAMPTZ; turso BLOB/TEXT), so both jobs dialect
trees now ship the identical filename set `{0001_job_queue.sql, 0002_job_schedules.sql,
0003_fenced_job_queue.sql}`. `diff <(ls .../pgx/migrations) <(ls .../turso/migrations)` → empty
(PARITY: identical filename sets). The existing `TestExportMigrations` in both stores exports the
new file too (part of each module's `go test ./...`, PASS).

Premise adaptation (suite hygiene, no guarantee weakened): `testFencedRetryAt` now truncates its
base `now` to microsecond (`time.Now().UTC().Truncate(time.Microsecond)`). That case compares a
CALLER-supplied `retry-at` to the value read back from the store; postgres `TIMESTAMPTZ` is
microsecond-precision, so a nanosecond caller value cannot round-trip `.Equal`. Truncation aligns
the base instant to a precision every store (memory ns, turso ns TEXT, pgx µs) represents exactly —
a precision alignment, NOT a relaxation of the schedule contract (the `CheckpointWhileClaimCurrent`
`ScheduledFor` equality needs no change because `EnqueueOnce` returns the stored row via RETURNING,
so seed and Get carry identical stored precision). Byte-exactness was NOT relaxed anywhere.

Scope note: this task delivers the store implementations + conformance wiring only. It does NOT add
a `FencedQueue` to `jobs.Repositories` (that consumption is phase 3/5), so existing `Repositories`
consumers are unaffected; the fenced store is reached via `NewFencedQueueStore(db)`.

Files changed:

- `features/jobs/stores/pgx/migrations/0003_fenced_job_queue.sql` — NEW canonical fenced table
  (BYTEA payload, extended status CHECK, active-key partial unique index, claim/key/terminal indexes).
- `features/jobs/stores/pgx/fenced.go` — NEW `FencedQueue`/`NewFencedQueueStore` implementing
  `job.FencedQueueRepository` over PostgreSQL (advisory-lock keyed admission, FOR UPDATE fence,
  INSERT…RETURNING).
- `features/jobs/stores/pgx/conformance_test.go` — added `fenced_job_queue` to `jobTables`; added
  `TestConformance_FencedQueue` (env-gated by `POSTGRES_TEST_DSN`, loud skip otherwise).
- `features/jobs/stores/turso/migrations/0003_fenced_job_queue.sql` — NEW canonical fenced table
  (BLOB payload; identical filename to the pgx 0003).
- `features/jobs/stores/turso/fenced.go` — NEW `FencedQueue`/`NewFencedQueueStore` implementing
  `job.FencedQueueRepository` over libSQL (BEGIN IMMEDIATE tx + busy-retry, INSERT…RETURNING).
- `features/jobs/stores/turso/conformance_integration_test.go` — added `fenced_job_queue` to
  `jobTables`; added `TestConformance_FencedQueue` (integration-tag + `TURSO_*`-gated, loud skip).
- `features/jobs/storetest/fenced.go` — microsecond truncation in `testFencedRetryAt` (cross-dialect
  precision alignment; no assertion weakened).

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...` — PASS
  (`storetest` fenced suite RUNS all 17 cases, ~7–10s incl. the real 3.1s lease-expiry sleeps).
- `cd features/jobs/stores/pgx && go build ./... && go test ./... && go vet ./...` — PASS
  (`TestConformance_FencedQueue` **SKIP** — `POSTGRES_TEST_DSN not set — postgres conformance NOT
  verified`).
- `cd features/jobs/stores/turso && go build ./... && go test ./... && go vet ./... && go vet
  -tags=integration ./...` — PASS (integration `TestConformance_FencedQueue` **SKIP** —
  `TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified`).
- `make check` — "all checks passed" (per-module vet/build/test + integration-tag compile-only vet
  including `vet -tags=integration features/jobs/stores/turso` + all guards).
- `make guard` — all guards green.

Phase 1 gate (AV3D-1.5 closes phase 1):

- `cd features/jobs && go test -race ./...` — PASS. `cd sdk && go test -race ./foundation/workers/...`
  — PASS.
- Both live store suites — recorded LOUD SKIPS (env unset): pgx `TestConformance_FencedQueue` and
  turso integration `TestConformance_FencedQueue` skip with the "NOT verified" messages above. The
  live pgx/turso race/reclaim/byte-exact proof therefore remains an **OPEN OWNER GATE** — the stores
  are implemented and compile/vet green under `-tags=integration`, but no live database was available
  in this environment to execute the 17-case suite against real PostgreSQL/Turso.
- Migration parity: `diff <(ls features/jobs/stores/pgx/migrations) <(ls
  features/jobs/stores/turso/migrations)` → **empty** (identical filename set
  `0001_job_queue.sql / 0002_job_schedules.sql / 0003_fenced_job_queue.sql`).
- `make check` — "all checks passed". `make guard` — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN` unset.
Required proof status: repeated stale-claim/reclaim, concurrent enqueue-once/replace, checkpoint-vs
-replace, retry-at ordering + permanent dead-letter, latest-by-key + bounded purge, and byte-exact
non-UTF8 payload round-trip are all EXECUTED live against the memstore reference under `-race` and
are WIRED to run identically against live pgx/turso; the fresh/reset live pgx and turso runs are
recorded as loud skips pending owner-provided env (the standing open gate from the preflight).
