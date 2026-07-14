# Phase 4 — bounded in-process mode

Depends on phase 2. This is a deliberate simple-deployment mode, not a fake
durable queue.

## Outcome

Run the same authentication delivery processor in a host-owned fixed worker pool
with bounded admission, bounded retries, bounded memory, graceful shutdown, and
honest restart loss.

### AV3D-4.1 — fixed pool and bounded admission

Implement a fixed worker count and finite queue capacity. Never start one
goroutine per request. Enqueue either admits within a configured short admission
deadline or returns a typed capacity/closed error.

`RunDelivery(ctx)` owns the pool lifecycle. Cancellation stops admission, lets
in-flight provider calls observe cancellation, and drains only within a configured
shutdown bound. `Register` starts no goroutines.

### AV3D-4.2 — process-local idempotency, replacement, and status retention

Implement submit-once/replace/latest semantics under one lock/arbiter using logical
keys and generations. Replacement prevents queued old generations from starting
and fences old checkpoints/completions where possible.

Status/history storage has explicit maximum entries and TTL. Eviction returns
unknown status; it never grows with total process lifetime. Document that restart
loses queued work, de-duplication, and status.

### AV3D-4.3 — checkpoint, retry, timeout, and terminal cleanup

Checkpoint the rendered envelope in the in-memory work item before send so retry
within the same process reuses the same secret. Apply the same provider timeout,
error classification, attempt cap, capped context-aware backoff, observer, and
terminal challenge discard as jobs mode.

There is no claim lease across restart. Tests must not imply durability or
cross-instance single execution.

### AV3D-4.4 — enumeration and saturation proof

For forgot-password/passwordless, capacity, closed-runtime, and shutdown outcomes
must be independent of whether the identifier exists because no account lookup has
yet occurred. Apply the same response class and timing policy to known and unknown
inputs; never silently return accepted after dropping work.

For account-resolved operations, surface dispatch failure according to the
operation's committed/notification contract. Record committed-but-not-notified
states honestly where applicable.

### AV3D-4.5 — construction and real-interaction proof

Prove invalid capacities, workers, attempts, timeouts, retention, and shutdown
bounds fail at construction. Production without explicit ephemeral acknowledgment
fails. Multi-instance documentation must warn that each process has independent
de-duplication and queues.

Run real mail/notifier checks for normal, saturation, retry, cancellation, and
shutdown. Use leak/race checks to prove goroutine count stays bounded.

### Phase 4 gate

Run authentication and bounded-runtime suites under `-race`, goroutine/leak proof,
real interaction, `make check`, and `make guard`.

## Execution log

Append task evidence including configured bounds and measured maximum worker/
goroutine counts.

### 2026-07-13 — AV3D-4.1 (fixed pool and bounded admission)

Implemented the bounded in-process delivery runtime for DeliveryMode "in_process":
a FIXED worker pool draining a FINITE admission queue, with typed capacity/closed
admission errors under a short deadline, a host-owned `RunDelivery(ctx)` lifecycle
with bounded drain and in-flight cancellation propagation, and construction that
starts no goroutine. Never one goroutine per request. The submit-once/replace/status
ARBITER (idempotency, generations, status retention) is deliberately deferred to
AV3D-4.2, and the in-memory checkpoint/retry/terminal-cleanup to AV3D-4.3 — the
smallest coherent slice that ships the pool + admission + lifecycle without broken
semantics (boundaries recorded below).

TWO-OBJECT DESIGN (breaks the construction cycle with NO mutable late-binding). The
in_process delivery has an unavoidable cycle: `delivery.Service` needs a `Dispatcher`
BEFORE `authService` exists, but the processor the pool runs (the `command.Engine`
whose Initializer IS `authService`) is built AFTER it. Rather than late-mutate a
single runtime, admission and execution are two objects sharing one bounded buffer:

- `delivery.InProcessQueue` (NEW, implements the frozen `delivery.Dispatcher`) — the
  admission side, built EARLY and wired into `delivery.Service` exactly like the jobs
  dispatcher. `Submit`/`Replace` admit one sealed item into a buffered channel within
  the admission deadline or return a typed error; `LatestStatus` returns an honest
  unknown (no retention yet). Never closes its channel (senders never panic); a `done`
  channel signals shutdown.
- `delivery.InProcessRuntime` (NEW) — the execution side, built LATE over the shared
  queue and the built processor. `Run(ctx)` launches exactly `Workers` goroutines,
  blocks on `<-ctx.Done()`, then `beginShutdown()` (stops admission) and drains
  in-flight within the shutdown deadline via a bounded `waitBounded` on the WaitGroup.
  The worker context is DERIVED from `Run`'s ctx, so cancellation propagates into each
  `Handle` and in-flight provider calls observe it; a worker never begins a NEW
  provider call after cancellation (a just-received item is dropped — ephemeral).

PROCESSOR REUSE: the runtime accepts an `InProcessProcessor` interface (`Handle(ctx,
executionID, payload, attempt, checkpoint) error`), which `*delivery.JobsProcessor`
satisfies — the SAME transport-neutral `command.Engine` both modes run, so no delivery
policy is duplicated and the interface lets the pool/admission/lifecycle be proven with
a fake, independent of the real engine. AV3D-4.1 passes a no-op checkpoint (the
rendered-payload in-memory checkpoint is AV3D-4.3) and logs+drops a non-completing
outcome (retry/terminal are AV3D-4.3), coarse and secret-free.

CONSTRUCTION (features/authentication/authentication.go): in_process now BUILDS a
delivery queue (previously it built none — the mode was non-functional). The
DeliveryEncrypter is REQUIRED for in_process (the bounded queue seals its payload — the
same fail-closed posture as jobs mode; strengthening, not weakening), and in_process
now satisfies the passwordless outbox requirement. The AV3D-0.1 durable-claim rejection
and production crash-loss acknowledgment gates are unchanged. `Service.RunDelivery(ctx)`
is the new host lifecycle seam (no-op in every other mode); `Register` starts nothing.

CONFIGURED BOUNDS (package defaults, AV3D-4.1): workers 2, capacity 256, admission
deadline 250ms, shutdown deadline 15s. Host-configurable knobs + construction negatives
for invalid values are AV3D-4.5; 4.1 ships the defaults behind the config structs.

MEASURED MAX WORKER / GOROUTINE COUNTS (all under -race):

- Fixed-pool proof (`TestInProcessRuntimeFixedPoolSize`, workers=3, 12 items admitted):
  peak concurrent `Handle` = EXACTLY 3 (asserted `maxSeen == workers` and `> workers`
  fails); never 12. Deterministic across `-count=5`.
- Goroutine bound (instrumented throwaway, since removed; workers=4, 40 items admitted):
  base goroutines 2 → peak 7 during the run = delta 5 (4 workers + 1 `Run` supervisor),
  peak worker concurrency = EXACTLY 4 (never 40 = one-per-request). Confirms the pool is
  fixed and no goroutine-per-request occurs.
- Admission deadline (`TestInProcessQueueAdmissionCapacity`, capacity=1, deadline=60ms):
  a full-queue Submit returns `ErrDeliveryCapacity` (wraps `sdk.ErrConflict`) after
  ≥60ms and well under the deadline+500ms bound — bounded, never unbounded, never a
  silent drop.
- Shutdown drain bound (`TestInProcessRuntimeShutdownDrainBounded`, workers=1,
  deadline=80ms, provider IGNORING cancellation): `Run` returns within the 80ms window
  regardless of the stuck provider (the stuck goroutine is released afterward to avoid
  a leak).
- Cancellation propagation (`TestInProcessRuntimeCancellationPropagates`): the in-flight
  `Handle`'s ctx is observed canceled on shutdown (`observed == true`).
- Register-starts-nothing: delivery-layer `TestInProcessRuntimeStartsNothingUntilRun`
  (0 items handled before `Run`, all handled after); auth-layer
  `TestInProcessRegisterStartsNoRuntime` (a clean `RunDelivery` nil on cancel proves
  construction/Register left the pool un-owned — a running pool would have returned
  `ErrInProcessAlreadyRunning`).

NON-NEGOTIABLES HELD: fixed workers + finite queue + bounded shutdown (measured above);
no goroutine per request (peak concurrency == N, not item count); `Register` starts no
goroutines (proven both layers); the payload is always sealed (in_process requires the
encrypter, CommandCodec on the delivery.Service); bounded mode never claims durability
or cross-instance coordination (doc comments on `InProcessQueue`/`InProcessRuntime`/
`RunDelivery` state restart loses queued work, de-duplication, and status); the
authentication core imports no jobs package (the runtime is sdk-only: context/fmt/
log/slog/sync/atomic/time + root `sdk`).

AV3D-4.1 BOUNDARIES RECORDED (deferred, not broken):

- `Replace` admits like `Submit` (no supersession/generations) — AV3D-4.2's arbiter.
- `LatestStatus` returns `sdk.ErrNotFound` (no process-local status retention) —
  AV3D-4.2. An honest unknown, never a fabricated pending/succeeded/failed.
- The pool checkpoint is a no-op and a retry/permanent outcome is logged+dropped —
  AV3D-4.3 adds the in-memory rendered-payload checkpoint, process-local retry, and
  terminal challenge discard.

TYPED-ERROR KIND NOTE: the sdk kernel exposes no dedicated unavailable/backpressure
kind, so `ErrDeliveryCapacity`/`ErrDeliveryClosed` wrap `sdk.ErrConflict` (a request
that cannot be admitted in the runtime's current bounded state). Uniform for known and
unknown identifiers (admission precedes any lookup), so no enumeration signal. Flagged
for the AV3-9.7 reviewer wave to adopt a dedicated kind if the kernel grows one.

Files changed:

- `features/authentication/internal/logic/delivery/inprocess.go` — NEW: `InProcessQueue`
  (Dispatcher) + `InProcessRuntime` + `InProcessProcessor` seam, bounds/defaults, typed
  `ErrDeliveryCapacity`/`ErrDeliveryClosed`/construction errors, `waitBounded`.
- `features/authentication/internal/logic/delivery/inprocess_test.go` — NEW: fixed-pool,
  admission-capacity, closed-admission, shutdown-drain-bound, cancellation-propagation,
  starts-nothing-until-Run, construction-negative, single-run, and status-unknown proofs
  (stdlib fakes only; all under -race).
- `features/authentication/authentication.go` — in_process encrypter requirement +
  passwordless outbox; build `InProcessQueue` (deliveryQueue switch) and `InProcessRuntime`
  (runtime switch) over authService; `Service.inProcessRuntime` field; `RunDelivery(ctx)`.
- `features/authentication/delivery_mode_test.go` — matrix: replaced the no-encrypter
  in_process success case with an "in_process requires an encrypter" negative + an
  "in_process with an encrypter constructs" positive; added
  `TestInProcessRegisterStartsNoRuntime` and `TestRunDeliveryNoopWithoutInProcessMode`.
- `features/authentication/delivery_jobs_test.go` — added the encrypter to the in_process
  construction in `TestDeliveryJobRuntime_UnavailableWithoutDispatcher` (in_process now
  seals its bounded-queue payload).

Commands run (all PASS):

- `cd features/authentication && go build ./...` — exit 0.
- `cd features/authentication && go test -race ./...` — exit 0 (all packages ok, incl.
  the new in-process suite; the delivery suite green under `-race -count=5`).
- `cd features/authentication && go vet ./...` — exit 0.
- `make guard` — exit 0, all thirteen guards green (rule 6: the runtime is sdk-only,
  imports no sibling feature).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset — largely irrelevant for this mode (in_process is process-local
and datastore-free; there is no durable store to exercise). The real mail/notifier +
leak/race real-interaction proof is AV3D-4.5; this task's proofs are hermetic under
`-race` with stdlib fakes.

Premise adaptations:

- The overview's "Replace RunDeliveryWorker with a host-owned runtime only for in_process"
  is realized ADDITIVELY: `RunDelivery` is the new in_process lifecycle seam;
  `RunDeliveryWorker` (bespoke DeliveryJobs path) stays until phase 5, so no existing host
  or test churned.
- The `sdk/foundation/workers.Pool` was NOT reused: it is a store-POLLING pool (adaptive
  poll/idle intervals + wake channel), a poor fit for a channel-fed in-memory bounded
  queue, and it provides no bounded ADMISSION (the dominant 4.1 deliverable) nor the
  drain-within-a-shutdown-deadline semantics. A focused, rigorously-bounded pool over the
  channel is the smaller, clearer fit; the "if it fits" escape in the prompt applies.
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds (`sdk.ErrConflict`,
  `sdk.ErrInvalidInput`, `sdk.ErrNotFound`).

### 2026-07-13 — AV3D-4.2 (process-local idempotency, replacement, and status retention)

Filled the DELIBERATE AV3D-4.1 boundaries: `InProcessQueue` gained a process-local
ARBITER under ONE lock (`q.mu`, the same lock already guarding admission-closed state)
keyed by logical key + monotonic GENERATION, plus BOUNDED latest-by-key status retention.
`Replace` no longer admits like `Submit`; `LatestStatus` no longer returns a flat unknown.
The AV3D-4.1 fixed pool, bounded admission, lifecycle, and cancellation/shutdown behavior
are unchanged (all 4.1 proofs still green, some with premise updates recorded below).

ARBITER (one lock, generations). Each logical key maps to ONE `keyRecord` holding the
CURRENT (highest) generation, its opaque execution ID, its generic lifecycle state, an
`active` flag (queued/processing = coalesce target, never eviction-eligible), an
`admitting` flag + `ready` channel (per-key admission serialization), and retention
bookkeeping (`seq`, `updatedAt`). Generation fencing is a single comparison: a queued or
in-flight item whose `gen` no longer equals its key's record `gen` was superseded.

- SUBMIT-ONCE: `Submit` coalesces onto an active generation — a duplicate returns the
  active execution ID and admits NO second item. If the active generation is still
  `admitting` (its bounded channel send has not committed), a concurrent same-key `Submit`
  WAITS on `ready` (bounded by the one admission-deadline timer) and re-evaluates rather
  than coalescing onto a reservation that may still roll back — this is what makes
  concurrent submit one-winner instead of racing a rollback.
- REPLACE: overwrites the key's record with a fresh generation, so every prior generation
  is immediately non-current. A superseded QUEUED generation never starts (`claim` returns
  false → the worker skips it); a superseded IN-FLIGHT generation's `checkpoint` and
  `settle` are FENCED (`ErrDeliverySuperseded`, wraps `sdk.ErrConflict`) so a stale
  execution records no transition over the current one. Documented race honesty on
  `Replace`: replacement cannot retract a provider call already in flight (the fence only
  blocks RECORDING), matching the overview's at-least-once posture.
- LATEST-BY-KEY: `LatestStatus` returns the current generation's generic state
  deterministically; an unknown / never-admitted / evicted / TTL-expired key returns
  `sdk.ErrNotFound` (normalized through the existing unknown projection). No fabricated
  pending/succeeded/failed.

ADMISSION-WITHOUT-LOCK-CONTENTION: `admit` holds `q.mu` ONLY for the fast generational
decision (coalesce/reserve/commit-or-rollback) and NEVER during the bounded channel send,
so a full-queue send never serializes admission for other keys. The whole call is bounded
by a single admission-deadline timer shared across the coalesce-wait retry loop, so total
admission time stays within one deadline even when a same-key reservation is in progress.
On send failure the reservation rolls back (deleted) unless a later `Replace` already
advanced the key past it.

STATUS RETENTION (bounded, never grows with process lifetime): the record map has an
explicit MAXIMUM ENTRY count and a TTL (defaults 4096 entries / 30m; construction knobs
`StatusMaxEntries`/`StatusTTL` on `InProcessQueueConfig` — full knob validation incl.
maxEntries ≥ capacity is AV3D-4.5). `evictLocked` runs on every admission: it sweeps
TERMINAL entries past TTL, then evicts the oldest TERMINAL entry while over the max. An
ACTIVE (queued/processing) generation is NEVER evicted or TTL-expired, so retention never
breaks a live generation's fence (the active set is itself bounded by capacity + pool).
`LatestStatus` lazily evicts an expired terminal entry on read.

CHECKPOINT/SETTLE FOR THE POOL (AV3D-4.2 slice): the runtime now `claim`s each item
(skips superseded), passes a FENCED checkpoint into the processor, and `settle`s the
outcome fenced (completed→completed, permanent→dead_letter, transient→failed). The
rendered-payload PERSISTENCE inside checkpoint and the bounded RETRY of a transient
failure remain AV3D-4.3 (the fence is the 4.2 deliverable; a non-completing outcome is
recorded for status then dropped, as 4.1 dropped it).

CONFIGURED BOUNDS (AV3D-4.2 additions): status max entries 4096, status TTL 30m (atop the
4.1 defaults workers 2 / capacity 256 / admission 250ms / shutdown 15s).

MEASURED / PROVEN (all under `-race`, `TestInProcess*` deterministic across `-count=5`):

- `TestInProcessQueueSubmitOnceCoalesces`: a duplicate `Submit` returns the SAME execution
  ID and the channel holds EXACTLY 1 item (no second admission).
- `TestInProcessQueueReplaceSupersedesQueued`: `Replace` mints a new execution ID; the
  superseded queued generation NEVER completes; the replacement completes and owns status.
- `TestInProcessQueueReplaceFencesInFlight`: an in-flight generation pinned in `Handle`,
  superseded by `Replace`, gets `ErrDeliverySuperseded` from its post-release checkpoint,
  records no completion; the replacement completes on a second worker and owns
  latest-by-key status.
- `TestInProcessQueueLatestByKeyDeterministic`: 16 independent keys each resolve to their
  own terminal completed status.
- `TestInProcessQueueRetentionMaxEntries`: 200 keys through a max of 8 — the map stays ≤ 8
  after every settle and lands at EXACTLY 8 (retention does not grow with lifetime).
- `TestInProcessQueueRetentionTTL`: a terminal status reads completed pre-TTL and
  `sdk.ErrNotFound` (map back to 0 entries) after the fake clock advances past the TTL.
- `TestInProcessQueueConcurrentSubmitReplaceOneWinner`: 32 concurrent Submit+Replace on
  one key settle to EXACTLY 1 retained generation with a deterministic completed status;
  `-race` covers the shared arbiter.

DOC: `InProcessQueue`, `Submit`, `Replace`, `LatestStatus`, and `Service.RunDelivery`
now state the process-local de-duplication/generation/status is EPHEMERAL — restart loses
queued work, de-duplication, and status — and never claims durability or cross-instance
coordination.

NON-NEGOTIABLES HELD: bounded status retention (max entries + TTL, proven never to grow);
stale/superseded work cannot record a transition (generation fence on claim/checkpoint/
settle, proven queued AND in-flight); no goroutine per request (the 4.1 fixed pool is
unchanged); bounded mode never claims durability (docs); no plaintext secret in status
(the record carries only a lifecycle word + opaque execution ID; the payload stays sealed
and is never interpreted by the arbiter). Authentication core imports no jobs package (the
arbiter is sdk-only: context/fmt/log/slog/sync/atomic/time + root `sdk`). No contract
weakened.

AV3D-4.2 BOUNDARIES RECORDED (deferred, not broken):

- The fenced checkpoint persists NO rendered payload yet — the in-memory rendered-payload
  checkpoint that makes a process-local retry reuse the same secret is AV3D-4.3.
- A transient (non-permanent) failure is recorded (`failed`) and DROPPED, not retried —
  the bounded process-local retry loop is AV3D-4.3; 4.2 marks the generation inactive.
- Construction validation of the new retention knobs (maxEntries ≥ capacity, positive TTL)
  is AV3D-4.5; 4.2 ships the knobs behind defaults.

Files changed:

- `features/authentication/internal/logic/delivery/inprocess.go` — arbiter on
  `InProcessQueue`: `keyRecord`, `inProcessItem.gen`, `StatusMaxEntries`/`StatusTTL`
  config + defaults, `ErrDeliverySuperseded`, `errInProcessStatusUnknown`; rewrote
  `admit` (coalesce/reserve/commit-rollback under one lock, send outside lock, shared
  deadline timer), `sendItem`, `Submit`/`Replace`/`LatestStatus`; new `evictLocked`/
  `expiredLocked`/`claim`/`checkpoint`/`settle`/`entryCount`; `process` now claims +
  fences + records; removed `noopCheckpoint`.
- `features/authentication/internal/logic/delivery/inprocess_test.go` — added the AV3D-4.2
  suite (coalesce, replace-supersedes-queued, replace-fences-in-flight, latest-by-key
  deterministic, retention max-entries, retention TTL, concurrent one-winner) with
  `successProcessor`/`fenceProbeProcessor`; updated three 4.1 tests to distinct keys
  (coalescing changed their premise) and rewrote the status-unknown test.
- `features/authentication/authentication.go` — `RunDelivery` doc updated: the
  process-local de-duplication/generation/status is ephemeral, lost on restart.

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` —
  exit 0 (all packages ok under `-race`).
- `cd features/authentication && go test -race -count=5 -run 'TestInProcess' ./internal/logic/delivery/` — deterministic, exit 0.
- `make guard` — exit 0, all thirteen guards green (the arbiter is sdk-only, imports no
  sibling feature).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset — irrelevant for this mode (in_process is process-local and
datastore-free; there is no durable store to exercise). Real mail/notifier + leak/race
real-interaction proof remains AV3D-4.5; this task's proofs are hermetic under `-race`
with stdlib fakes.

Premise adaptations:

- Three AV3D-4.1 tests submitted several items under ONE key ("k"/"lk") to exercise the
  pool/admission; AV3D-4.2 coalescing collapses same-key duplicates, so those were changed
  to DISTINCT keys (`TestInProcessRuntimeFixedPoolSize`, `TestInProcessQueueAdmissionCapacity`,
  `TestInProcessRuntimeStartsNothingUntilRun`) — a premise fix, not a weakened assertion.
- `TestInProcessQueueLatestStatusUnknown` previously asserted every key was unknown (the
  4.1 no-retention boundary); it now asserts an unknown/never-admitted key is unknown,
  while retention behavior is proven by the new retention/status tests.
- Retention max-entries and TTL are proven by driving the arbiter directly (Submit →
  drain the channel → `settle`) rather than timing the pool, so the bound is deterministic
  and independent of scheduling.

### 2026-07-13 — AV3D-4.3 (checkpoint, retry, timeout, and terminal cleanup)

Filled the DELIBERATE AV3D-4.2 boundaries: the bounded pool now runs the SAME transport-neutral
`command.Engine` processor (`*delivery.JobsProcessor`) jobs mode runs, so it applies the IDENTICAL
provider timeout, error classification (engine outcome → transient vs permanent), attempt cap,
capped context-cancellable backoff, observer transitions, and terminal challenge discard — no
delivery policy is duplicated. The AV3D-4.1 fixed pool + bounded admission + lifecycle and the
AV3D-4.2 arbiter (submit-once/replace/latest, generation fencing, bounded retention) are unchanged;
this task adds the in-memory rendered-payload checkpoint, the process-local retry loop, and the
post-dead-letter discard. NO contract weakened.

IN-MEMORY CHECKPOINT (reuse the same secret across a process-local retry). `process` now maintains
a local `payload` starting as the admitted bytes (opaque for an enumeration-safe start, rendered
otherwise). The FENCED checkpoint closure the processor calls before any send now ALSO records the
freshly rendered SEALED bytes into that local (`q.checkpoint(item)` still fences by generation
first — a superseded/evicted generation returns `ErrDeliverySuperseded` and MUST NOT send). A
transient failure then retries with the checkpointed rendered payload, so the engine opens a
RENDERED (non-opaque) envelope on retry, skips re-initialization, and delivers the byte-identical
secret. Proven end to end against the REAL processor (`TestInProcessRetryReusesSameSecret`): the
opaque start initializes EXACTLY once, and the failed + retried provider sends are byte-identical
and carry the checkpointed secret — never a re-mint.

RETRY / ATTEMPT CAP / BACKOFF (parity with jobs mode; the engine is the classification authority).
`process` loops: `Handle` nil → `settle` completed; `HandleErrorPermanent(err)` → dead-letter
immediately; `attempt >= MaxAttempts` → dead-letter (cap); else a transient failure waits the
capped `backoff(attempt)` on the worker slot, then retries. Config gained `MaxAttempts` (default 5,
mirrors `command.Engine`'s `defaultMaxAttempts`) and `Backoff func(attempt) time.Duration` (default
capped exponential base 5s → cap 5m, mirrors `command.Engine`'s `defaultBackoff`). The
`command.Engine` also caps internally (returns `OutcomePermanent` at its own `MaxAttempts`), so in
PRODUCTION the two caps default equal (5) and the engine returns permanent on the final attempt (no
stray "retried" observation); a smaller runtime cap in a test simply dead-letters one attempt
earlier. Proofs: `TestInProcessAttemptCapDeadLettersThenDiscards` (cap=3 → exactly 3 handles →
dead_letter → discard) and `TestInProcessPermanentDeadLettersImmediately` (permanent → 1 handle,
no retry).

BACKOFF DECISION — occupies a worker slot, does NOT reschedule (documented on
`InProcessRuntimeConfig.Backoff`). `backoffWait(ctx, d)` is a SINGLE `time.Timer` select against
`ctx.Done()` — no busy-loop, NO per-retry goroutine, no leak — so a shutdown interrupts a parked
backoff promptly and the worker returns. This is bounded either way: attempts are capped and each
backoff is capped, so total worker occupation per unit of work is bounded. Proof
(`TestInProcessBackoffCancellationPrompt`): a 10s backoff, cancelled mid-wait, returns Run in ≪ 5s
with only attempt 1 spent.

SUPERSEDED-DURING-BACKOFF CANNOT TRANSITION. A rendered retry skips the engine checkpoint (so the
checkpoint fence does not re-fire on retry); `process` therefore re-checks `q.current(item)` BOTH
right after a transient failure (catches a fenced checkpoint / already-superseded generation, no
backoff) AND after the backoff wait (catches a Replace during the wait). A superseded generation
stops — no fresh provider call, no recorded transition — and the replacement owns latest-by-key
status. Proof (`TestInProcessSupersededDuringBackoffCannotTransition`, workers=2): the first
generation is pinned in its backoff, superseded by `Replace`; it is handled EXACTLY once (never
resends) while the replacement completes and owns `completed` status.

TERMINAL DISCARD (only after the recorded terminal; idempotent; failure does not resurrect). The
`InProcessProcessor` seam gained `Discard(ctx, executionID, payload)` (`*JobsProcessor` already
satisfies it). `deadLetter` records the terminal `dead_letter` FENCED (returns false for a
superseded generation → no discard of the current generation's challenge), and ONLY on a recorded
terminal does the runtime call `processor.Discard` with the (checkpointed) payload. A discard error
is logged, never re-queued. Proofs: the cap/permanent tests assert `Discard` observes the
already-recorded `dead_letter` status and runs exactly once; the observer test asserts the real
processor's `Discard` voids the challenge once.

PROVIDER TIMEOUT (inherited from the shared engine). No timeout knob was added to the runtime — the
per-send provider deadline lives in the `command.Engine` both modes share. Proof
(`TestInProcessProviderTimeoutBounded`): a provider that HANGS (blocks on its send ctx) with a 20ms
engine `ProviderTimeout` and a cap of 3 reaches `dead_letter` in ≪ 2s with exactly 3 bounded sends
— an unbounded send would hang the first attempt forever and never terminalize.

OBSERVER PARITY (via the reused processor; best-effort). in_process construction
(`authentication.go`) now wires `DeliveryEventsEmitter` into the in_process `JobsProcessor` exactly
as jobs mode does, so `Handle` emits delivered/skipped/retried and `Discard` emits dead_lettered
(AFTER the recorded terminal) through `SafeObserve`. Proofs: `TestInProcessObserverTransitions`
(retried + dead_lettered observed end to end, challenge discarded once) and
`TestInProcessObserverFailureChangesNothing` (a nil, erroring, AND panicking observer each leaves
the outcome unchanged — exactly one send, status succeeded).

NO CLAIM LEASE ACROSS RESTART — every new test asserts only process-local behavior; none implies
durability or cross-instance single execution. The ephemeral docs from 4.1/4.2 stand.

CONFIGURED BOUNDS (AV3D-4.3 additions): MaxAttempts 5, backoff base 5s doubling to cap 5m (atop the
4.1/4.2 defaults workers 2 / capacity 256 / admission 250ms / shutdown 15s / status 4096 entries /
30m TTL). Full construction validation of the new knobs (positive attempts, positive/ordered
backoff bounds) is AV3D-4.5; 4.3 ships them behind defaults.

DELIVERYCHAR DECISION (the "strongly consider" call): DEFERRED to AV3D-4.5, not run now. The
transport-neutral `deliverychar.Run` suite runs ALL cases including `CrashAfterSendReplaysSameSecret`
(Scenario `CrashCompletions` + `LeaseFor`) and `PurgeRespectsRetention` (Scenario `PurgeRetention` +
a manual `Purge` sweep) — both model DURABLE reclaim (a claim lease, crash-after-send replay, an
operator-driven purge) that the ephemeral in_process mode deliberately does NOT provide. Satisfying
them would require either faking cross-restart reclaim (implying durability the task forbids) or a
suite subset the current `Run` has no seam for. Rather than distort the honest guarantees, 4.3 proves
the same OBSERVABLE properties (checkpoint-before-send + identical-secret retry, transient retry,
permanent terminal + discard, provider-timeout bound, status-lifecycle-only) with focused
runtime-level tests against the REAL processor. Wiring the in_process Harness (and the honest mapping
of the durable-only cases, e.g. `CrashCompletions` → dropped ephemeral work) is an AV3D-4.5
deliverable where the phase gate requires it.

MEASURED / PROVEN (all under `-race`, `TestInProcess*` deterministic across `-count=5`):

- `TestInProcessRetryReusesSameSecret`: 1 init, 2 byte-identical sends carrying the checkpointed
  secret (checkpoint-before-send reuse through the real engine).
- `TestInProcessAttemptCapDeadLettersThenDiscards`: cap=3 → exactly 3 handles → `dead_letter`
  recorded → discard observes `dead_letter` and runs exactly once.
- `TestInProcessPermanentDeadLettersImmediately`: permanent → 1 handle (no retry), discard once.
- `TestInProcessBackoffCancellationPrompt`: 10s backoff cancelled mid-wait → Run returns in ≪ 5s,
  only 1 attempt spent (context-cancellable, no busy-loop, no per-retry goroutine).
- `TestInProcessSupersededDuringBackoffCannotTransition` (workers=2): superseded generation handled
  exactly once, no resend, no transition; replacement completes and owns status.
- `TestInProcessProviderTimeoutBounded`: hanging provider, 20ms timeout, cap=3 → `dead_letter` in
  ≪ 2s with exactly 3 bounded sends.
- `TestInProcessObserverTransitions` / `TestInProcessObserverFailureChangesNothing`: retried +
  dead_lettered observed; nil/erroring/panicking observer changes no outcome.

NON-NEGOTIABLES HELD: retry resends the identical secret (in-memory checkpoint, proven end to end);
bounded attempts/backoff/timeout (cap + capped-exponential + engine timeout, all measured); superseded
work cannot transition (double `current` fence around backoff + `deadLetter`/`checkpoint`/`settle`
generation fence); discard only after a recorded terminal (fenced `deadLetter` gates it), idempotent,
failure does not resurrect; events never required (best-effort via `SafeObserve`, reused processor);
no goroutine per request AND no goroutine per retry (backoff parks the existing worker on a single
timer). Authentication core imports no jobs package (the runtime is sdk-only:
context/errors/fmt/log-slog/sync/atomic/time + root `sdk`; the processor it runs is the same
`command.Engine` both modes share).

Files changed:

- `features/authentication/internal/logic/delivery/inprocess.go` — retry defaults
  (`defaultInProcessMaxAttempts`/`BackoffBase`/`BackoffCap`); `InProcessProcessor` gains `Discard`;
  `InProcessRuntimeConfig`/`InProcessRuntime` gain `MaxAttempts`/`Backoff`; `NewInProcessRuntime`
  fills them; `process` rewritten to the checkpoint-reuse + bounded-retry + fenced-supersede loop;
  new `deadLetter`/`current` (queue) and `deadLetter`/`backoffWait`/`defaultInProcessBackoff`
  (runtime); `settle` doc updated.
- `features/authentication/internal/logic/delivery/inprocess_retry_test.go` — NEW: the AV3D-4.3
  suite (checkpoint reuse, attempt-cap dead-letter+discard ordering, permanent immediate dead-letter,
  backoff cancellation promptness, superseded-during-backoff, provider-timeout bound, observer
  transitions + observer-failure-changes-nothing) against the REAL processor + stdlib fakes.
- `features/authentication/internal/logic/delivery/inprocess_test.go` — added a no-op `Discard` to
  the four AV3D-4.1/4.2 fakes (the seam gained `Discard`).
- `features/authentication/authentication.go` — in_process now wires `DeliveryEventsEmitter` into
  the in_process `JobsProcessor` (observer parity with jobs mode).

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` — exit 0
  (all packages ok under `-race`).
- `cd features/authentication && go test -race -count=5 -run 'TestInProcess' ./internal/logic/delivery/`
  — deterministic, exit 0.
- `make guard` — exit 0, all thirteen guards green (the runtime is sdk-only, imports no sibling feature).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN` unset —
irrelevant for this mode (in_process is process-local and datastore-free). Real mail/notifier +
leak/race real-interaction proof remains AV3D-4.5; this task's proofs are hermetic under `-race`
against the REAL processor with stdlib provider/initializer/observer fakes.

Premise adaptations:

- The runtime owns the attempt cap and backoff TIMING while the `command.Engine` owns the
  transient-vs-permanent CLASSIFICATION — the exact split AV3D-3.4 chose for jobs mode (there the
  FencedRuntime owns the capped-exponential retry-at). The engine's own internal cap defaults equal
  to the runtime's (5), so they agree; the runtime cap is the LOOP authority (a fake processor that
  never classifies permanent still terminates).
- Backoff OCCUPIES A WORKER SLOT (single-timer cancellable wait) rather than rescheduling onto a
  timer wheel — the smallest correct fit for the ephemeral simple-deployment mode, with no per-retry
  goroutine. Bounded because attempts and each backoff are capped. Documented on the config field.
- deliverychar.Run deferred to AV3D-4.5 (rationale above): its durable-reclaim/purge cases do not
  fit the ephemeral mode honestly at 4.3.
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here (`sdk.ErrConflict`,
  `sdk.ErrNotFound`, `sdk.ErrInvalidInput`).

### 2026-07-13 — AV3D-4.4 (enumeration and saturation proof)

Proved that in DeliveryMode "in_process", the enumeration-safe unauthenticated starts
(forgot-password, passwordless) admit-or-reject IDENTICALLY for a known and an unknown
identifier under capacity-exhaustion, a closed runtime, and shutdown — because NO account
lookup occurs on the request path before admission — and made the HTTP mapping of a
dropped admission HONEST (503, never a 202-accepted lie). Also proved the account-resolved
producers surface vs. record dispatch failure per the AV3D-2.4 inventory contract. No
contract weakened; no bounded-runtime code changed (the mechanism was already correct — 4.4
is a proof task plus an HTTP-mapping alignment).

ENUMERATION UNDER PRESSURE (structural + identical-error-class, not wall-clock). The proofs
drive the REAL `delivery.InProcessQueue`/`InProcessRuntime` through a real `delivery.Service`
wired into a real `authsvc.Service` (the harness), then assert BOTH properties the task
names: (1) the SAME typed error class for known and unknown, and (2) the structural
invariant that NO resolver call happened before admission. The structural proof is a
resolver-call counter on the test identifier store (`fakeIdentifiers.loginCalls` already
existed for GetLogin; added a symmetric `recoveryCalls` for GetRecovery — forgot-password's
off-path resolver), reset immediately before the saturated call and asserted ZERO after —
so a future regression that sneaks a lookup ahead of admission trips the test. This is the
honest proof the phase file asks for (identical class + structural no-lookup), not a fragile
timing comparison.

- `TestInProcessForgotPasswordCapacityEnumerationParity` (authsvc, `-race`): a real
  InProcessQueue saturated to capacity (every slot filled with distinct filler keys; the
  runtime is never started so nothing drains) rejects BOTH `ForgotPassword("known")` and
  `ForgotPassword("ghost")` with `delivery.ErrDeliveryCapacity` (identical class), and
  GetLogin==0 && GetRecovery==0 for both (no lookup before admission).
- `TestInProcessPasswordlessCapacityEnumerationParity` (authsvc, `-race`): same saturation
  parity + no-lookup for `StartPasswordless` (email kind enabled white-box).
- `TestInProcessClosedRuntimeEnumerationParity` (authsvc, `-race`): a runtime that was Run
  then cancelled leaves the queue CLOSED (Run always calls beginShutdown before returning);
  all four of {forgot,passwordless}×{known,unknown} reject with `delivery.ErrDeliveryClosed`,
  no resolver call for any — the shutdown outcome is independent of existence.

NEVER ACCEPTED AFTER DROP (honest HTTP class; was NOT 503 before — ALIGNED). At the service
boundary the starts already returned a non-nil failure (`TestInProcessStartsNeverAcceptAfterDrop`
pins that: nil is never returned after a dropped admission). But the HTTP layer mapped the
capacity/closed errors (which wrap `sdk.ErrConflict`) DISHONESTLY: forgot-password collapsed
ALL errors to 500, and passwordless routed them through `RespondJSONDomainError` → 409
Conflict. Neither is a 202 lie, so the critical non-negotiable held — but neither is the
honest backpressure class. ALIGNED both transports (JSON + form) to map a bounded-delivery
admission rejection to 503 Service Unavailable via one shared, kind-based helper
`deliveryUnavailable(err)` (`errors.Is` ErrDeliveryCapacity/ErrDeliveryClosed). The mapping
is by error KIND, identical for known and unknown (admission precedes any lookup), so it
adds no enumeration signal; every OTHER forgot-password error still 500s, so nothing else
moved.

- `TestForgotPasswordSaturationReturns503NotAccepted` (inbound, `-race`, capacity+closed
  subtests): `POST /auth/password/forgot` for known and unknown both return 503 (asserted
  NOT 202), same class for both.
- `TestPasswordlessStartSaturationReturns503NotAccepted` (inbound, `-race`, capacity+closed
  subtests): `POST /auth/passwordless/start` for known and unknown both return 503 (NOT
  202), same class for both.

ACCOUNT-RESOLVED OPERATIONS (per the AV3D-2.4 inventory contract):
- SURFACED-error (`TestIdentifierChangeStartSurfacesDispatchFailure`, authsvc): the
  identifier-change PROOF (inventory site 6, Replace) surfaces a dispatch failure to the
  caller — with the outbox seam failing, `StartIdentifierChange` returns
  `delivery.ErrDeliveryCapacity` (errors.Is), not a swallowed success.
- COMMITTED-then-notify (`TestIdentifierChangeNoticeDispatchFailureCommitStands`, authsvc):
  the identifier-change NOTICE (inventory site 7, best-effort) is enqueued at confirm time
  AFTER the change commits. With the notice dispatch failing, `ConfirmIdentifierChange`
  returns nil (NOT falsely failed), the new identifier is claimed+verified (the commit is
  NOT rolled back), the `email_changed` security event is recorded, AND the
  committed-but-not-notified state is recorded honestly as a coarse WARN
  ("identifier-change notice enqueue failed", `error_kind` only) — asserted PII-free (no
  address, no proof code in the log). This is the existing WARN path (`enqueueIdentifierChangeNotices`),
  proven under the in_process error class.

NON-NEGOTIABLES HELD: no account resolution on the unauthenticated request paths (resolver
counters ZERO under saturation/closed for both flows, known and unknown); known/unknown
parity in BOTH response class AND code path (identical typed error at the service boundary,
identical 503 at HTTP, structurally no branch on existence before admission); never
accepted-after-drop (service returns non-nil; HTTP returns 503, asserted not 202); no PII in
errors/logs (the best-effort WARN is `error_kind` only, canary substrings asserted absent).

Files changed:

- `features/authentication/internal/inbound/authentication/sessions.go` — NEW shared
  `deliveryUnavailable(err)` helper (errors.Is ErrDeliveryCapacity/ErrDeliveryClosed);
  `forgotPasswordJSON` now maps a bounded-delivery admission rejection to 503, every other
  error still 500.
- `features/authentication/internal/inbound/authentication/passwordless.go` —
  `passwordlessStartJSON` maps a bounded-delivery admission rejection to 503 before the
  generic domain-error fallback (was 409 via sdk.ErrConflict).
- `features/authentication/internal/inbound/authentication/forms.go` — `formFailure` maps
  the admission rejection to 503 (was 409); `forgotPasswordForm` renders 503 (was 500) for
  it. Both preserve the generic enumeration-resistant copy.
- `features/authentication/internal/inbound/authentication/saturation_test.go` — NEW:
  `capacityQueue` rejecting seam, `newSaturationHandler`, and the two 503-not-202 known/
  unknown parity transport tests (capacity + closed subtests).
- `features/authentication/internal/logic/authsvc/inprocess_saturation_test.go` — NEW:
  `errQueue`/`noopInProcessProcessor` fakes, `saturatedInProcessService`/
  `closedInProcessService` helpers over the REAL InProcessQueue/Runtime, and the six proofs
  (forgot+passwordless capacity parity, closed parity, never-accept-after-drop, surfaced
  dispatch failure, committed-then-notify commit-stands + PII-free WARN).
- `features/authentication/internal/logic/authsvc/service_test.go` — additive
  `fakeIdentifiers.recoveryCalls` counter + increment in GetRecovery (symmetric with the
  existing loginCalls) for the no-lookup-before-admission structural proof.

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` —
  exit 0 (all packages ok under `-race`).
- `cd features/authentication && go test -race -run 'TestInProcess|TestIdentifierChange|TestForgotPasswordSaturation|TestPasswordlessStartSaturation' ./internal/logic/authsvc/ ./internal/inbound/authentication/`
  — green; the six authsvc proofs and both transport proofs verbose-confirmed to RUN + PASS.
- `make guard` — exit 0, all thirteen guards green (the tests are sdk-only + the real
  delivery/authsvc packages; no sibling-feature import).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset — irrelevant for this mode (in_process is process-local and datastore-free). The
real mail/notifier + leak/race real-interaction proof remains AV3D-4.5; these proofs are
hermetic under `-race` against the REAL InProcessQueue/Runtime and real authsvc.Service.

Premise adaptations:

- The phase file's "same response class ... never silently return accepted after dropping
  work" required an HTTP-MAPPING FIX (documented above): the pre-existing 500/409 mappings
  were not-a-202 (so the non-negotiable held) but not the honest 503 backpressure class; 4.4
  aligns forgot-password AND passwordless (JSON + form) to 503 via one kind-based helper,
  preserving known/unknown parity.
- "no timing side-channel introduced by existence" is proven STRUCTURALLY (resolver-call
  counters == 0, identical typed error class) rather than by wall-clock comparison, exactly
  as the task directs — a fragile timing assertion would be a false-confidence proof.
- The saturated-queue proof fills the REAL bounded channel with distinct filler keys and
  never starts the runtime (nothing drains), so admission deterministically hits capacity;
  the closed proof drives the REAL runtime through Run→cancel so `beginShutdown` (unexported)
  is reached honestly rather than poked.
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here (`sdk.ErrConflict`,
  `sdk.ErrNotFound`).

### 2026-07-13 — AV3D-4.5 (construction and real-interaction proof)

Closed phase 4: exposed the in-process tuning knobs on the auth Config with fail-closed
construction validation, added the honest multi-instance documentation, wired the deferred
`deliverychar.Harness` against the REAL bounded runtime, drove a REAL in_process host over
HTTP through every named scenario, and proved the goroutine bound + leak-freedom — then ran
the full phase 4 gate. NO contract weakened.

CONSTRUCTION VALIDATION (fail-closed, nil-safe). The in-process bounds were package
defaults behind zero-value config structs (AV3D-4.1..4.3 deferred knob validation here). Now
`auth.Config.InProcessDelivery` (a new `InProcessDeliveryConfig`) exposes Workers,
QueueCapacity, AdmissionDeadline, ShutdownDeadline, MaxAttempts, StatusMaxEntries, StatusTTL.
Every knob is NIL-SAFE (a ZERO value selects the package default — the zero struct is a fully
valid, fully defaulted configuration), and every INVALID bound FAILS AT CONSTRUCTION
(`auth.NewService`) with a typed error wrapping `sdk.ErrInvalidInput`, never silently coerced
to a default:

- negative Workers → `delivery.ErrInProcessWorkersInvalid`
- negative QueueCapacity → `delivery.ErrInProcessCapacityInvalid`
- negative AdmissionDeadline → `delivery.ErrInProcessAdmissionDeadlineInvalid`
- negative ShutdownDeadline → `delivery.ErrInProcessShutdownDeadlineInvalid`
- negative MaxAttempts → `delivery.ErrInProcessMaxAttemptsInvalid`
- negative StatusMaxEntries → `delivery.ErrInProcessStatusMaxEntriesInvalid`
- negative StatusTTL → `delivery.ErrInProcessStatusTTLInvalid`
- effective StatusMaxEntries < effective QueueCapacity → `delivery.ErrInProcessStatusRetentionTooSmall`
  (a queued/in-flight generation must never be retention-evicted — the cross-field invariant
  the AV3D-4.2 code comment flagged for 4.5)

Validation lives in the delivery package (`InProcessQueueConfig.Validate` /
`InProcessRuntimeConfig.Validate`, the cross-field capacity check where the defaults live), is
called from `auth.NewService`'s `DeliveryModeInProcess` matrix case, and `NewInProcessRuntime`
also self-validates (defense in depth). The auth Config knobs thread through two nil-safe
mapping helpers (`inProcessQueueConfig`/`inProcessRuntimeConfig`) into the queue and runtime
construction sites. Production without `DeliveryEphemeralAcknowledged` still fails (unchanged
AV3D-0.1 gate). `delivery_mode_test.go` gained 8 negative cases + 1 tuned-in-bounds positive
case (all pass); the frozen AV3D-0.1 matrix rows are untouched.

MULTI-INSTANCE DOCUMENTATION (honest ephemeral). `InProcessDeliveryConfig`, the
`DeliveryModeInProcess` const, and `Service.RunDelivery` now state plainly that the bounds,
queue, submit-once de-duplication, replace/generation arbiter, and latest-by-key status are
PER PROCESS with NO cross-instance coordination — two instances each keep their own, so the
same logical delivery admitted on two instances is de-duplicated on NEITHER and both can
render and both can send (a user may receive two messages); the durable, cross-instance
de-duplicated posture is `DeliveryModeJobs`.

DELIVERYCHAR HARNESS (the deferred 4.3 decision, resolved). Wired
`newInProcessHarness` + `TestInProcessCharacterization` (`inprocess_char_test.go`, package
delivery) running `deliverychar.Run` against the REAL `delivery.InProcessQueue` arbiter
(Submit/Replace/LatestStatus through a real `delivery.Service{CommandCodec}`) and the REAL
`InProcessRuntime.process` delivery loop over the SAME `command.Engine` both other modes run.
Drain runs each admitted item through `process` synchronously (the concurrent Run pool's fixed
size / shutdown drain / cancellation / no-goroutine-per-request are proven by the AV3D-4.1
pool tests); backoff is forced to zero (the suite asserts identical-secret retry and send
counts, NOT timing — timing is `TestInProcessBackoffCancellationPrompt`). Result: 9/12 cases
PASS; 3 SKIPPED HONESTLY with an in-test justification naming the dedicated test that proves
the property the ephemeral mode CAN honor (silently dropping the suite would not be honest):

- `CrashAfterSendReplaysSameSecret` — durable claim-lease + cross-restart reclaim; ephemeral
  mode has neither. Identical-secret retry it CAN do → `TestInProcessRetryReusesSameSecret`.
- `PurgeRespectsRetention` — operator-driven purge; ephemeral mode reclaims terminal status
  via automatic bounded retention → `TestInProcessQueueRetentionMaxEntries`/`...RetentionTTL`.
- `ProviderTimeoutIsBounded` — models a timed-out send RESCHEDULED to a durable pending row;
  the runtime bounds the send identically (engine ProviderTimeout →
  `TestInProcessProviderTimeoutBounded`) but retries in-worker to terminal rather than
  rescheduling, so "still pending after one drain" is out of scope.

REAL MAIL/NOTIFIER INTERACTION over HTTP (choice recorded). Built a test-scoped in_process
host by flipping the host's own `buildAuthConfig` to `DeliveryModeInProcess` (the acceptable
"temporary in_process wiring driven over real HTTP" the task allows; `examples/auth-cms`
run() itself still runs jobs mode), with a recording mailer standing in for the console
sender, in `examples/auth-cms/cmd/server/inprocess_delivery_test.go`:

- NORMAL delivery: a real `RegisterUser` enqueues the rendered verification into the bounded
  queue; the host-owned pool (`RunDelivery`) renders and sends it; the recording mailer
  observes exactly the message a recipient would receive (correct address, non-empty body).
- SATURATION 503 over REAL HTTP: capacity 1, pool NOT running (nothing drains); once the slot
  is full, `POST /auth/password/forgot` returns 503 Service Unavailable within the admission
  deadline — never a 202-accepted lie.
- FORGOT-PASSWORD admission over REAL HTTP: an unknown address returns 202 (enumeration-safe;
  the pool resolves→skips off the request path).
- RETRY: a provider that fails the first send then succeeds; the pool retries and the retried
  send carries the byte-identical rendered message (the checkpointed secret), never re-minted.
- CANCELLATION + SHUTDOWN DRAIN: `RunDelivery` returns within the bounded window on cancel.

CONFIGURED BOUNDS AND MEASURED MAX WORKER/GOROUTINE COUNTS. Leak proof
(`TestInProcessHostShutdownDrainAndNoLeak`, CONFIGURED Workers=3, ShutdownDeadline=2s, 12
real registrations driven): MEASURED baseline=2 goroutines, peak DURING=6, DELTA=4 = exactly
3 workers + 1 Run supervisor — NEVER one-per-request (a per-request pool would spike toward
12); after cancel + drain the count RETURNS TO BASELINE (no leak across normal delivery,
retry, saturation, or shutdown). Assertion caps the during-delta at workers+4 and requires
post-shutdown <= baseline. Package defaults unchanged (workers 2 / capacity 256 / admission
250ms / shutdown 15s / status 4096 entries / 30m TTL / attempts 5 / backoff 5s→5m).

PHASE 4 GATE (all PASS):

- `cd features/authentication && go build ./... && go vet ./... && go test -race ./...` —
  exit 0 (authentication + bounded-runtime suites green under `-race`).
- `go test -race ./internal/logic/delivery/ -run TestInProcessCharacterization -count=5` —
  deterministic, exit 0 (9 pass, 3 documented skips).
- `cd examples/auth-cms && go test -race ./cmd/server/ -run TestInProcessHost... -count=3/5` —
  exit 0 (all five real-host scenarios green under `-race` with repeats; retry ~5s on the
  default backoff, run once under `-race`).
- goroutine/leak proof — measured above, returns to baseline.
- `make guard` — exit 0, all 13 guards green (the harness + validation are sdk-only + the real
  delivery/auth packages; no sibling-feature import; auth core imports no jobs package).
- `make check` — "all checks passed" (templ drift + per-module build/vet/test + guards + the
  integration-tag compile-only vet).

NON-NEGOTIABLES HELD: fail-closed construction (every invalid bound is a loud typed error,
never coerced); bounded everything (validated knobs + measured fixed pool); honest ephemeral
documentation (per-process, two-instances-can-each-send, on every public surface); no
goroutine per request (measured delta = N+1, not item count); `Register` starts no goroutine
(unchanged, re-proven by the host boot); authentication imports no jobs package
(`make guard`). No contract weakened.

Files changed:

- `features/authentication/internal/logic/delivery/inprocess.go` — 8 typed construction
  errors (`ErrInProcess{Capacity,AdmissionDeadline,StatusMaxEntries,StatusTTL,Workers,ShutdownDeadline,MaxAttempts}Invalid`
  + `ErrInProcessStatusRetentionTooSmall`, each wrapping `sdk.ErrInvalidInput`);
  `InProcessQueueConfig.Validate` (negatives + cross-field maxEntries>=capacity) and
  `InProcessRuntimeConfig.Validate` (negatives); `NewInProcessRuntime` self-validates.
- `features/authentication/authentication.go` — `InProcessDeliveryConfig` type + multi-instance
  doc; `Config.InProcessDelivery` field; `inProcessQueueConfig`/`inProcessRuntimeConfig`
  nil-safe mapping helpers; in_process matrix case validates both configs; construction sites
  thread the mapped configs; `RunDelivery` doc gained the two-instances-can-each-send warning.
- `features/authentication/security.go` — `DeliveryModeInProcess` const doc gained the
  per-process/no-cross-instance-coordination warning.
- `features/authentication/delivery_mode_test.go` — imported the delivery package; added 8
  construction-negative knob cases + 1 tuned-in-bounds positive case to the frozen matrix.
- `features/authentication/internal/logic/delivery/inprocess_char_test.go` — NEW:
  `newInProcessHarness` (honest per-case skips) + `inProcessHarness` (real queue arbiter +
  real `process` loop) + `TestInProcessCharacterization` running `deliverychar.Run`.
- `examples/auth-cms/cmd/server/inprocess_delivery_test.go` — NEW: `bootInProcess`/
  `mountInProcess`/`postForgot`/`runDelivery`/`recordingSender` helpers + the five real-host
  in_process proofs (normal delivery, HTTP admission 202, saturation 503 over HTTP, retry
  same-secret, shutdown drain + goroutine leak with the measured counts).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset — irrelevant for this mode (in_process is process-local and datastore-free; there is no
durable store to exercise). The real mail/notifier + leak/race proof this task owed is
delivered hermetically over real HTTP + the real console-class mailer under `-race`.

Premise adaptations:

- Knob validation lives in the DELIVERY package (typed errors + `Validate` methods, where the
  defaults and the cross-field capacity invariant live) and is surfaced by `auth.NewService`;
  the auth Config carries the knobs and the matrix asserts `errors.Is` against the delivery
  sentinels (auth already re-exports delivery types, e.g. `DeliveryStatus`). `NewInProcessQueue`
  keeps its no-error signature (auth validates before calling it); `NewInProcessRuntime`
  self-validates. This avoided churning ~20 delivery-package test call sites.
- The deliverychar harness drives the real `InProcessRuntime.process` SYNCHRONOUSLY (not the
  concurrent Run pool) with backoff forced to zero: the suite's manual-clock, drain-to-quiescence
  model is designed for a store-polling runtime, while the in_process pool is a real-time
  worker pool that CLOSES its queue on shutdown (so it cannot be Run/stopped repeatedly across
  a case's many Drain calls). Running the same `process` loop is the faithful mapping of the
  bounded delivery policy; the pool lifecycle/concurrency is proven by the AV3D-4.1 tests.
- Three deliverychar cases are SKIPPED with justification rather than mapped: two are the
  durable-only cases the AV3D-4.3 log named (crash-replay, purge-retention); the third
  (provider-timeout) models a reschedule-to-durable-pending semantic the ephemeral in-worker
  retry does not implement, though it bounds the send identically. Each skip names the
  dedicated runtime test proving the property the bounded mode CAN honor.
- The real-host proof reuses the host's own `buildAuthConfig` flipped to in_process rather
  than adding a permanent in_process host; run() still ships jobs mode (the recommended
  production posture), matching the AV3D-3.5 "temporary in_process wiring driven over real
  HTTP" allowance.
- The retry real-host test runs on the package DEFAULT backoff (~5s, not a Config knob — a
  backoff func is not env-tunable), so it is patient (~5s) rather than instant; the fast
  timing/cancellation proof stays at the delivery layer.
