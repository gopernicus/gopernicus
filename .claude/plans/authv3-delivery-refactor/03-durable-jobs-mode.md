# Phase 3 — durable generic-jobs mode

Depends on phases 1 and 2.

## Outcome

Run authentication delivery as a registered generic job kind, preserving all
durable auth-v3 semantics while authentication and jobs remain independently
importable feature modules.

### AV3D-3.1 — composition adapter and host wiring

Create the narrow composition adapter that maps auth dispatcher operations to
generic jobs submit-once/replace/latest and maps the generic job handler to the
auth processor. The adapter may import both modules; neither core may import the
other.

Resolve construction order explicitly and prove no handler can run before the
fully built auth service is attached. `Register` starts nothing; the host runs the
already-built `jobs.Runtime`.

### AV3D-3.2 — encrypted admission and checkpointed initialization

In jobs mode, every persisted payload is sealed with `DeliveryEncrypter`, including
the opaque resolution input. The handler checkpoints the rendered sealed payload
under the current worker fence before any provider call.

Prove:

- database inspection finds no destination, normalized identifier, code, token,
  subject, or rendered body in plaintext;
- restart after opaque admission initializes safely;
- restart after checkpoint sends the same secret; and
- restart after provider acceptance may resend only that same secret.

### AV3D-3.3 — duplicate, resend, and stale-worker behavior

Map auth receipt keys to generic logical keys, never execution IDs. Submit-once is
idempotent while active; replace creates a fresh generation and status selects the
latest.

Run adversarial replacement while old work is pending, initializing, checkpointed,
and sending. A stale handler cannot checkpoint or record success/failure after
supersession. Record the unavoidable already-in-flight provider race in docs.

### AV3D-3.4 — retry, terminal cleanup, lifecycle, and retention

Map transient provider errors to capped exponential retry-at, permanent errors to
immediate dead-letter, and parent cancellation to reclaimable work. Provider
timeout must be safely shorter than the claim lease.

After a successful dead-letter transition, invoke the idempotent challenge discard
hook. Map generic status to auth status and emit optional observer events. Configure
bounded generic terminal retention/purge without auth-specific SQL.

### AV3D-3.5 — production, live-store, restart, and real-interaction proof

Add construction negatives for missing encrypter, incomplete jobs capabilities,
ephemeral jobs store in production, missing runtime acknowledgment, invalid
timeout/lease/backoff, and development-only transports.

Required run-and-look proof on pgx and turso:

- known/unknown opaque starts have matching admission behavior;
- provider timeout and retry occur off request path;
- process restart at each checkpoint boundary behaves as documented;
- resend and stale-claim races converge to the latest generation;
- status and events contain no secrets; and
- terminal cleanup and purge occur.

### Phase 3 gate

Run jobs/auth/integration suites under `-race`, both live dialects, restart harness,
real interaction, migration parity, `make check`, and `make guard`.

## Execution log

Append task evidence, including exact crash points and live database results.

### 2026-07-13 — AV3D-3.1 (composition adapter and host wiring)

Wired authentication's encrypted delivery onto the generic jobs feature end-to-end,
additively, with the composition adapter living in the HOST. Both feature cores are
still independently importable and neither imports the other (guards green). The
codec swap the AV3D-2.3 log promised for phase 3 lands here so the jobs-mode handler
(the transport-neutral `command.Engine` processor) opens exactly what admission
sealed. Deep encryption/checkpoint/stale/retry/retention PROOFS remain AV3D-3.2..3.4;
this task is the wiring skeleton + construction-order proof + host run.

ADAPTER PLACEMENT RATIONALE (the call the task left open): the adapter lives in the
HOST at `examples/auth-cms/internal/authjobs/`, NOT in an integration and NOT as a
new inter-feature module. Reasoning from the Makefile guards + ARCHITECTURE:

- `guard-integration-no-inward` (G13) forbids integrations importing features, so the
  composing `integrations/notify/mailer` exemplar cannot be the model — an adapter
  importing BOTH features cannot be an integration.
- `guard-feature-no-cross-feature` (rule 6) forbids a feature core importing another
  feature, and `guard-store-no-foreign-feature` forbids store modules doing so — so
  the adapter cannot live in either feature or a store.
- The repo has no sanctioned `compositions/` module between features today; the ONE
  place that imports two features is a host (`examples/auth-cms`). So the adapter is
  host-local composition code, exactly where the overview's "only hosts import both
  features today" and "a composition adapter may import both features" point. The
  feature seams it bridges are stdlib-typed on both sides
  (`auth.DeliveryDispatcher`/`auth.DeliveryClaim`/`auth.DeliveryJobRuntime` and
  `jobs` `EnqueueOnce/Replace/LatestStatusByKey`/`FencedClaim`/`FencedHandlerFunc`),
  so the adapter is thin and the cores stay decoupled.

JOBS public surface (additive; `features/jobs`): extended the public Service over the
already-implemented (AV3D-1.x) fenced queue so a consumer can submit-once/replace/
read-latest and register a fenced handler kind + runtime — the "generic jobs service
exposes the primitive methods" clause.

- `Repositories.FencedQueue job.FencedQueueRepository` (optional); `NewService` now
  requires `Queue OR FencedQueue` (a fenced-only delivery host needs no cron/unfenced
  queue). Existing consumers wiring `Queue` are byte-for-byte unaffected; the both-nil
  case still returns `ErrQueueRequired` (message widened). Unfenced `Enqueue`/
  `EnqueueJob`/`NewRuntime` now guard a nil queue.
- `Service.EnqueueOnce`/`Replace`/`LatestStatusByKey`/`Checkpoint` delegate to the
  fenced queue, satisfying the frozen `KeyedEnqueuer`/`KeyStatusReader`/`Checkpointer`
  seams (compile assertions added in `fenced.go`).
- `FencedRuntime`/`NewFencedRuntime(svc, cfg)` with stdlib-typed `FencedClaim`/
  `FencedHandlerFunc` + per-kind `DeadLetterFunc` hooks. It is built on the AV3D-1.4
  `workers.FencedRunner` (reusing its lease fence, retry-at reschedule, process
  timeout, and post-Fail dead-letter hook — the primitives.go seam intent) driven by a
  `workers.Pool`. The checkpoint closure binds to the claimed job's `JobID`+`LeaseID`
  (the claimed `job.Job` already carries `LeaseID`), so NO sdk kernel change was needed
  to thread the checkpoint (the AV3D-1.3 "no runner checkpoint seam yet" YAGNI holds).
  `NewFencedRuntime` starts nothing; the host runs `Run`.

AUTH public surface (additive; `features/authentication`): exposes BOTH directions of
the seam the adapter needs, per 00-overview's "auth exposes the registered job
kind/handler seam" and "accepts a Dispatcher implementation via Config":

- `Config.DeliveryDispatcher DeliveryDispatcher` (stdlib-typed Submit/Replace/
  LatestStatus) — the jobs-mode delivery transport, the alternative to the bespoke
  `Repositories.DeliveryJobs` outbox. In jobs mode `NewService` builds the
  `delivery.Service` over it with the versioned command codec and does NOT build the
  bespoke worker.
- `DeliveryJobKind` const; `DeliveryClaim` + `DeliveryJobRuntime` stdlib-typed types;
  `Service.DeliveryJobRuntime() (DeliveryJobRuntime, bool)` returns the job kind + a
  stdlib handler func (the wrapped `command.Engine`) + the discard hook, ok only when
  the jobs-mode processor is wired.
- Construction matrix updated: jobs-mode queue capability = `DeliveryJobs` OR
  `DeliveryDispatcher`; the encrypter is required whenever either is wired; `off`
  rejects either; `validatePasswordless` treats either as the wired outbox. The
  DeliveryJobs durability/ack negatives are unchanged (they still guard the bespoke
  path).
- `internal/logic/delivery`: `Service.CommandCodec` seals the versioned
  `command.Envelope` (new `commandcodec.go` converts producer `Command` →
  `command.Envelope`); new `jobsprocessor.go` wraps `command.Engine` with
  `command.Initializer`/`command.Deliverer` adapters over the bespoke Router/auth
  Initializer, exposing stdlib `Handle`/`Discard`.

CONSTRUCTION ORDER / no-half-built proof: the delivery processor is built INSIDE
`NewService` AFTER `authService` (its account resolver) is attached, so the handler
closure can only capture a fully-built engine. `DeliveryJobRuntime()` returns
`ok=false` until then, and the host's `if !ok { return error }` fails LOUDLY if a host
tried to build the runtime before wiring completes. The host builds `jobs.Service`
from the fenced queue, the dispatcher from that Service, the auth Service from the
dispatcher, and only THEN reads `DeliveryJobRuntime()` and constructs the
`jobs.FencedRuntime` — which it runs explicitly. `Register` starts nothing (proven).

HOST wiring (`examples/auth-cms`): `run()` builds an in-memory `jobsmem.NewFencedQueue`
→ `jobs.NewService` → `authjobs.NewDispatcher`, nils `authRepos.DeliveryJobs`, sets
`authCfg.DeliveryDispatcher`, builds `authSvc`, reads `DeliveryJobRuntime()`, builds
`jobs.NewFencedRuntime(deliveryJobs, authjobs.FencedRuntimeConfig(rt))`, and runs it
in place of the bespoke `RunDeliveryWorker` goroutine (same shutdown-after-HTTP order).
`buildAuthConfig` and its existing negative/worker tests are UNCHANGED (they still
exercise the bespoke `DeliveryJobs` path via `authmem`), so no existing test churned.

Files changed:

- `features/jobs/jobs.go` — `Repositories.FencedQueue`; `Service.fencedQueue`;
  `NewService` requires Queue-or-FencedQueue and builds queuesvc/scheduler only with
  Queue; nil-queue guards on `Enqueue`/`EnqueueJob`/`NewRuntime`; `ErrQueueRequired`
  reword + new `ErrFencedQueueRequired`.
- `features/jobs/fenced.go` — NEW: `EnqueueOnce`/`Replace`/`LatestStatusByKey`/
  `Checkpoint` primitive methods + seam compile assertions; `FencedClaim`/
  `FencedHandlerFunc`/`FencedRuntimeConfig`/`FencedRuntime`/`NewFencedRuntime` over
  `workers.FencedRunner` + `workers.Pool`.
- `features/jobs/fenced_test.go` — NEW: primitives idempotency/replace/latest;
  runtime claims→checkpoint→completes (and starts nothing before Run); always-fail →
  dead-letter with the hook observing the recorded terminal state; fenced-queue-required.
- `features/authentication/authentication.go` — `DeliveryDispatcher` port;
  `DeliveryJobKind`/`DeliveryClaim`/`DeliveryJobRuntime`; `Config.DeliveryDispatcher`;
  `Service.jobsProcessor` + `DeliveryJobRuntime()`; NewService jobs-mode branch +
  matrix updates.
- `features/authentication/internal/logic/delivery/service.go` — `ServiceDeps.CommandCodec`
  / `Service.commandCodec`; `seal` branches to the command codec.
- `features/authentication/internal/logic/delivery/commandcodec.go` — NEW: producer
  Command ↔ versioned command.Envelope conversions + `sealCommand`.
- `features/authentication/internal/logic/delivery/jobsprocessor.go` — NEW:
  `JobsProcessor` (`command.Engine` wrapper) + Router/Initializer command-envelope adapters.
- `features/authentication/delivery_jobs_test.go` — NEW: DeliveryJobRuntime exposed
  iff dispatcher wired; Register/NewService start no transport work; producer submits a
  versioned opaque command envelope with no secret.
- `examples/auth-cms/internal/authjobs/authjobs.go` — NEW: the composition adapter
  (`Dispatcher` + `FencedRuntimeConfig`).
- `examples/auth-cms/internal/authjobs/authjobs_test.go` — NEW: single-kind mapping;
  claim/checkpoint/dead-letter bridging.
- `examples/auth-cms/cmd/server/main.go` — jobs-mode delivery wiring (fenced queue,
  jobs.Service, dispatcher, fenced runtime replacing the delivery worker).
- `examples/auth-cms/cmd/server/jobs_delivery_test.go` — NEW: real register → jobs
  FencedRuntime → processor → send end-to-end; nothing delivered before the host
  starts the runtime.
- `examples/auth-cms/go.mod` — require+replace `features/jobs`.

Commands run (all PASS):

- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...`
- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
  (14 ok packages, 0 FAIL under -race)
- `cd examples/auth-cms && go build ./... && go test ./...` (incl. the new
  `TestJobsModeDeliveryEndToEnd` real register→runtime→send cycle)
- `make guard` — all thirteen guards green (rule-6 "no feature core imports a different
  feature" proves neither core imports the other).
- `make check` — "all checks passed" (templ drift + per-module vet/build/test +
  integration-tag compile vet + all guards).
- `make tidy` — no drift.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. Per the task, the in-memory jobs fenced-queue stand-in
(`jobsmem.NewFencedQueue`) backs the host and the end-to-end test; live pgx/turso
delivery, restart, and real-interaction proof are AV3D-3.5.

Premise adaptations:

- The task's "maps the generic job handler to the auth processor engine" requires the
  jobs-mode admission to seal the versioned `command.Envelope` so the `command.Engine`
  can open it. That codec swap + the `command.Initializer`/`command.Deliverer` adapters
  over the bespoke Router/auth Initializer therefore land HERE (behind the same
  Dispatcher seam, a drop-in implementation change), even though the encryption/
  checkpoint PROOFS (db inspection, restart at each boundary) are AV3D-3.2. The bespoke
  `delivery.Envelope` codec + worker are left intact (characterization-before-deletion;
  they retire at phase 5) — the codec is selected per-mode by `Service.CommandCodec`.
- Retry precision: the fenced runtime maps the processor's explicit Result onto
  `workers.FencedRunner`'s error model (Completed/Skipped → complete; Retry/Permanent →
  a secret-free error → the runner's attempt-capped retry-at/dead-letter policy). The
  processor's own explicit retry-at/permanent-now classification is REFINED onto the
  runtime in AV3D-3.4 ("retry, terminal cleanup, lifecycle, and retention"); the seam
  (stdlib handler error + per-kind dead-letter hook) is stable now.
- No sdk kernel change was needed: the claimed `job.Job` already carries `LeaseID`, so
  the checkpoint closure fences without a new `FencedRunner` checkpoint seam (the
  AV3D-1.3 YAGNI holds).
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here.

### 2026-07-13 — AV3D-3.2 (encrypted admission and checkpointed initialization)

PROOF-ONLY task: drove the AV3D-3.1 jobs-mode composition end to end and proved the four
AV3D-3.2 security properties on the host's real stack (auth.Service → authjobs.Dispatcher
→ generic jobs fenced queue → jobs.FencedRuntime → auth delivery processor). NO gap was
found — the mechanism 3.1 wired already seals every persisted payload (including the opaque
resolution input) and checkpoints the rendered payload under the claim fence before any
send — so NO production/feature code changed. The only new file is a host test. This is
consistent with the task ("much of the mechanism exists from 3.1 — this task PROVES it and
closes any gaps found"); there were no gaps to close.

PLACEMENT: all proofs live in the HOST (`examples/auth-cms/cmd/server`), which owns the full
composition. The feature-internal `command` package (the codec/engine) cannot be imported by
a host, so the test decrypts persisted payloads through the PUBLIC `cryptids.Encrypter`
(`cfg.DeliveryEncrypter`) into a host-local mirror of the `command.Envelope` JSON shape — no
internal import, byte-exact read of what was sealed.

RESTART MODEL: "crash + restart" is modeled by keeping the DURABLE state alive (one fenced
queue instance + one authmem `Repositories`) while dropping and rebuilding the jobs Service,
auth Service, and FencedRuntime over it — a process restart with the DB intact. The delivery
encrypter, challenge pepper, and identifier key are pinned via `t.Setenv` to stable values
(a real multi-instance/restart deployment MUST share them, per the demo.go WARNs), so a
payload sealed before the restart is openable after it. An inspecting `job.FencedQueueRepository`
wrapper over `jobsmem.FencedQueue` records every payload byte-for-byte as it is persisted
(enqueue-once/replace/checkpoint), signals when a checkpoint lands, and can drop the first
`Complete` for a target execution (the crash-after-acceptance simulation).

EXACT CRASH POINTS exercised (the phase file demands them):

1. AFTER OPAQUE ADMISSION, before any initialization — `TestRestartAfterOpaqueAdmissionInitializesSafely`:
   forgot-password admits an opaque job with NO runtime running; the stored job is asserted
   `StatusPending` (no request-path init). A fresh runtime over the surviving store then
   initializes it exactly once (exactly ONE delivered message, no duplicate) and completes it.
2. AFTER THE RENDERED CHECKPOINT, before a successful send — `TestRestartAfterCheckpointResendsSameSecret`:
   a failing-provider boot resolves→renders→CHECKPOINTS, then the send fails; the runtime is
   stopped the instant the checkpoint signal fires (asserted to be the forgot-password job id).
   The checkpointed secret is read by decrypting the stored payload. A healthy-provider restart
   reads the checkpointed rendered payload, skips re-init, and the resent message CARRIES the
   checkpointed secret; the stored payload's secret is UNCHANGED (no re-mint).
3. AFTER PROVIDER ACCEPTANCE, before completion — `TestRestartAfterProviderAcceptanceResendsSameSecret`:
   a healthy-provider boot delivers message #1, then the first `Complete` for the job is DROPPED
   (completion write lost); the runtime is stopped on the drop signal. A restart reclaims the
   lapsed-lease job and resends — message #2 is BYTE-IDENTICAL to #1 (same To/Subject/Text/HTML
   ⇒ same secret, never newly minted; at-least-once), and the job then completes.

BYTE-INSPECTION (`TestJobsModeSealsEveryPersistedPayload`): drives registration verification
(RENDERED, via `RegisterUser`), forgot-password (OPAQUE, via `ForgotPassword`), and passwordless
(OPAQUE, via the mounted `POST /auth/passwordless/start` — the only reachable entry point), runs
the runtime so the opaque starts initialize + checkpoint + deliver, then scans EVERY persisted
byte. It recovers the ACTUAL plaintext each payload carried (by decrypting it) plus the
externally observed address/subject/code the capturing mailer received, and asserts none of
those canaries appears in any durable payload ciphertext, nor (for long canaries) in the
plaintext `Kind`/`LogicalKey`/`FailureReason` columns. A positive control asserts the decrypted
plaintext really did carry the address and a secret, so the absence result is non-vacuous. The
opaque resolution input (the normalized email) is proven sealed. Short numeric secrets are only
checked against the payload ciphertext (a 6-digit run can coincidentally appear in a hex
`LogicalKey` without being a real leak — flagged in a comment).

Files changed:

- `examples/auth-cms/cmd/server/jobs_delivery_proof_test.go` — NEW: the four AV3D-3.2 proofs +
  the inspecting fenced-queue wrapper, the `bootDelivery` restart harness, the shared
  admit-verified-forgot-password helper, and the sealed-envelope host mirror decoder.

Commands run (all PASS):

- `cd examples/auth-cms && go build ./... && go test -race ./...` — all packages ok.
- `cd examples/auth-cms && go vet ./...` — clean.
- `cd examples/auth-cms && go test -race -count=5 -run 'TestJobsModeSealsEveryPersistedPayload|TestRestartAfter' ./cmd/server/`
  — deterministic across 5 iterations under `-race` (no flake, no data race).
- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` — ok
  (unchanged; cached green — no feature code touched).
- `make guard` — all thirteen guards green.
- `gofmt -l` on the new file — clean.
- `features/jobs` NOT run: not touched (no jobs code changed). `make check` NOT required: a
  single module (the host) was touched, and only with a test file.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset. Per the task, the inspectable in-memory fenced queue backs every proof; live pgx/turso
run-and-look restart proof at each boundary remains AV3D-3.5.

Premise adaptations:

- The task lists three flows (registration verification, forgot-password, passwordless). The four
  restart proofs use forgot-password (OPAQUE — the only flow that admits opaque, initializes,
  and checkpoints, so it exercises all four crash points) plus registration verification as the
  drained rendered admission; the byte-inspection additionally drives passwordless through the
  mounted HTTP start. Passwordless shares the identical opaque admission → off-path init →
  checkpoint path as forgot-password, so the restart proofs cover it by equivalence.
- "Restart" rebuilds the Services/runtime over surviving stores; the DURABLE stand-in is the
  in-memory fenced queue + authmem repos (env DSNs unset). Key material is pinned via `t.Setenv`
  so the sealed payload survives the rebuild — the honest analog of a shared-key restart.
- "Same secret, never newly minted" is proven two ways: the checkpoint proof shows the resent
  message carries the checkpointed secret AND the stored payload's secret is unchanged; the
  provider-acceptance proof shows the resend is byte-identical to the first delivery.
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here.

### 2026-07-13 — AV3D-3.3 (duplicate, resend, and stale-worker behavior)

PROOF-plus-doc task: proved the AV3D-3.3 duplicate/resend/stale-worker properties end to end
on the host's real jobs-mode composition (auth.Service → authjobs.Dispatcher → generic jobs
fenced queue → jobs.FencedRuntime → auth delivery processor), REUSING and extending the
AV3D-3.2 inspecting-fenced-queue harness rather than duplicating it. The only production change
is one doc comment on the auth public surface stating the unavoidable in-flight provider race
(the mechanism itself — logical-key admission, atomic supersession, lease-fenced
checkpoint/complete/fail — was already wired by AV3D-3.1/3.2 and hardened at the store level by
AV3D-1.2; this task proves those semantics END TO END through the auth jobs-mode composition and
adversarially). No jobs code and no auth logic changed.

RECEIPT KEY → PII-FREE LOGICAL KEY (never an execution id): `TestReceiptKeyMapsToPIIFree...`
asserts the auth receipt key equals the generic job's `LogicalKey`, differs from the execution
id (`JobID`), and contains no plaintext identifier fragment (raw/normalized address or local
part) — it is the keyed identifier digest suffixed with the purpose (authsvc.idempotencyKey →
identifierDigest, a keyed HMAC in production / PII-free SHA-256 dev fallback). Status resolution
is proven latest-by-key: `DeliveryStatus(receiptKey)` resolves (pending before delivery,
succeeded after) while `DeliveryStatus(executionID)` returns sdk.ErrNotFound (status keys on the
logical key, never the execution id).

SUBMIT-ONCE COALESCING: `TestSubmitOnceCoalescesOntoOneActiveExecution` fires two
forgot-password starts for the same identifier while active and asserts exactly ONE non-terminal
generation holds the key end to end (EnqueueOnce returns the existing active job for the second
submit) and running the runtime delivers exactly two messages (verification + the ONE coalesced
reset), never a third.

REPLACE → FRESH GENERATION, STATUS = LATEST: `TestReplaceCreatesFreshGenerationStatusSelectsLatest`
drives the exact composition Replace path (authjobs.Dispatcher.Replace → jobs.Service.Replace →
fenced-queue supersession) and asserts the prior generation ends StatusSuperseded (never
delivers), the fresh generation delivers + completes with a distinct execution id, and the
receipt key reports the LATEST (succeeded), not the superseded generation (which would be
canceled).

FOUR ADVERSARIAL REPLACEMENT STAGES (extended the inspecting queue with two claim-scoped
checkpoint gates — enter/done — plus a mid-provider-call gating mailer; each gate targets the
stale execution id and is one-shot so the fresh generation is never gated; the widened 5s lease
keeps a paused handler from lapsing its lease during the test's orchestration window):

1. PENDING (pre-claim) — `TestAdversarialReplaceWhilePending`: Replace lands before any claim;
   the stale generation is StatusSuperseded (terminal) at once and never claimed; only the fresh
   generation delivers (exactly one message); status = latest.
2. INITIALIZING (claimed, pre-checkpoint) — `TestAdversarialReplaceWhileInitializing`: the stale
   handler resolves/renders and is paused ENTERING its checkpoint; Replace supersedes; the stale
   checkpoint then FAILS with sdk.ErrConflict (asserted through the done-gate) so the handler
   never sends (exactly one delivery, the fresh generation); stale generation stays Superseded.
3. CHECKPOINTED (pre-send) — `TestAdversarialReplaceWhileCheckpointed`: the stale handler commits
   its checkpoint, is paused before send, Replace supersedes, then it proceeds to send (the
   honest in-flight race — it cannot re-check supersession between a committed checkpoint and its
   send) but CANNOT record success: Complete is lease-fenced → conflict → generation stays
   Superseded. Two messages delivered (the stale old proof + the fresh proof); the two rendered
   messages DIFFER (the reissued challenge — IssueChallenge is a challenge-store Replace —
   supersedes the old proof); status = latest.
4. SENDING (mid-provider-call) — `TestAdversarialReplaceWhileSending`: the gating mailer pauses
   the stale handler's provider call in flight; Replace supersedes; the in-flight send cannot be
   retracted (delivers the old proof) but Complete is fenced → conflict → generation stays
   Superseded. Two messages, different secrets; status = latest.

IN-FLIGHT PROVIDER RACE recorded honestly on the auth public surface: extended the
`DeliveryDispatcher` doc comment (features/authentication/authentication.go) to state that
Replace fences a stale worker's checkpoint/completion (both lease-fenced → conflict) but CANNOT
retract a provider call already in flight — delivery is at-least-once, not exactly-once — and
that the freshly issued challenge REPLACES the old proof so the stale delivery is harmless and
status always reflects the latest generation. (00-overview already states the race generally;
this puts it at the frozen public seam where Replace is declared.)

Files changed:

- `examples/auth-cms/cmd/server/jobs_delivery_replace_test.go` — NEW: the receipt-key /
  submit-once / replace-latest proofs and the four adversarial-replacement stage proofs, plus the
  gating mailer and the small key/status/active-generation helpers.
- `examples/auth-cms/cmd/server/jobs_delivery_proof_test.go` — extended the shared harness:
  `inspectingQueue` gains claim-scoped checkpoint gates (gateCheckpointEnter/gateCheckpointDone +
  setGates) wired into Checkpoint; `booted` carries the jobs.Service; `bootDelivery` takes
  variadic FencedRuntimeConfig tuning (existing callers unaffected). No behavior change to the
  AV3D-3.2 proofs.
- `features/authentication/authentication.go` — doc-comment-only: the in-flight provider race on
  `DeliveryDispatcher`/Replace. No code change.

Commands run (all PASS):

- `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...` — all packages ok.
- `cd examples/auth-cms && go test -race -count=5 -run 'TestReceiptKeyMapsToPIIFreeLogicalKeyNotExecutionID|TestSubmitOnceCoalescesOntoOneActiveExecution|TestReplaceCreatesFreshGenerationStatusSelectsLatest|TestAdversarialReplace' ./cmd/server/`
  — deterministic across 5 iterations under `-race` (no flake, no data race).
- `cd features/authentication && go build ./... && go test -race -count=1 . && go vet ./...` — ok
  (doc-comment change only).
- `make guard` — all thirteen guards green (rule 6: neither feature core imports the other).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset. Per the task, the inspectable in-memory fenced queue backs every proof; live pgx/turso
resend/stale-claim run-and-look proof remains AV3D-3.5.

Premise adaptations:

- The adversarial replacement is triggered through the REAL composition Replace seam
  (authjobs.Dispatcher.Replace → jobs.Service.Replace), admitting the prior generation's own
  opaque admission bytes as the fresh generation — a faithful re-admission of the same
  enumeration-safe start — because no natural second auth flow issues Replace against the
  forgot-password logical key (forgot-password itself is submit-once/Enqueue). The fresh
  generation therefore initializes to a fresh challenge exactly as a real resend would.
- "Restart"/pause at each stage is modeled with claim-scoped gates on the inspecting fenced
  queue's Checkpoint (stages b/c) and a mid-provider-call gating mailer (stage d); stage a needs
  no gate (Replace precedes the first claim). The widened lease is a test-orchestration
  accommodation only — it never changes the fencing outcome.
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here (sdk.ErrConflict,
  sdk.ErrNotFound).

### 2026-07-13 — AV3D-3.4 (retry, terminal cleanup, lifecycle, and retention)

Refined the AV3D-3.1 jobs-mode transport so the auth delivery Engine's explicit
retry/permanent verdict now drives the fenced runner's reschedule/fail path (the
"REFINED onto the runtime in AV3D-3.4" the 3.1 log deferred), wired the optional
lifecycle observer into the jobs-mode transport, enforced the provider-timeout-inside
-lease invariant at construction, and exposed host-driven bounded terminal purge. Every
mapping is proven end to end on the real host composition (auth.Service → authjobs
adapter → generic jobs fenced queue → jobs.FencedRuntime → auth delivery processor)
against the inspectable in-memory fenced queue; live pgx/turso proof remains AV3D-3.5.

ENGINE-RESULT → RUNNER RETRY/FAIL (the core refinement). Before 3.4 the jobs-mode
handler collapsed OutcomeRetry AND OutcomePermanent into one bare error, so the runtime's
attempt-only policy re-derived the verdict and — the real defect — retried a
structurally-permanent command (unopenable payload, no initializer, seal failure) up to
the cap instead of dead-lettering it at once. 3.4 makes the Engine the classification
authority and the runner honor it:

- KERNEL (sdk/foundation/workers/fenced.go): new `FencedRetryDecider func(err error,
  attempt int) (delay, retry bool)` + `WithFencedRetryDecider`, an ERROR-AWARE retry
  policy that SUPERSEDES the attempt-only `WithFencedRetry` when set. `handleFailure`
  consults it with the process error, so a consumer routes "permanent now" vs "transient
  retry-at" onto Reschedule/Fail. Additive and backward-compatible: `WithFencedRetry` and
  the AV3D-1.1 first-failure default are unchanged (all existing sdk/workers tests green).
- JOBS (features/jobs/fenced.go): `Permanent(reason)` returns a `dispositionError` a
  FencedHandlerFunc returns to dead-letter IMMEDIATELY; the FencedRuntime wires
  `WithFencedRetryDecider` = "permanent → (0,false); else capped-exponential retry-at
  bounded by MaxAttempts". A transient/plain error keeps the capped-exponential retry-at.
- AUTH (internal/logic/delivery/jobsprocessor.go + command): `JobsProcessor.Handle` now
  returns a `jobFailure{permanent}` (permanent for OutcomePermanent, transient otherwise);
  exported `delivery.HandleErrorPermanent` classifies it; the public `authentication`
  package re-exports it as `auth.DeliveryErrorPermanent`. `command.Result` gained the
  bounded, secret-free `Kind`/`Purpose` (Engine.tag) so a transition can be observed
  without re-opening the sealed payload.
- ADAPTER (examples/auth-cms/internal/authjobs): the handler maps auth's verdict onto the
  generic policy — `auth.DeliveryErrorPermanent(err)` → `jobs.Permanent(err.Error())`
  (immediate dead-letter + rt.Discard); any other error → the plain retryable error
  (capped-exponential retry-at). This cross-module classification lives in the ONE place
  that imports both features; neither core imports the other (rule 6, guards green).

PARENT CANCELLATION → RECLAIMABLE is the runner's AV3D-1.1 property (it leaves the
in-flight job reclaimable on ctx cancel, never Complete/Fail); 3.4 proves it end to end
(`TestJobsModeParentCancellationLeavesReclaimable`): cancelling the runtime mid-send
leaves the job non-terminal and a restart reclaims and delivers it.

PROVIDER TIMEOUT INSIDE THE LEASE. `jobs.NewFencedRuntime` now validates
`ProcessTimeout < LeaseFor` and returns the new `ErrProcessTimeoutExceedsLease` when
inverted (jobs-mode config validation fails loudly). The host (main.go) sets
ProcessTimeout=20s inside the 30s default lease. Proof
(`TestJobsModeProviderTimeoutBoundedInsideLease`): a stuck (ctx-respecting) provider with
ProcessTimeout=40ms/lease=3s dead-letters in ≪ the lease (the per-attempt timeout cut
each send well inside the lease), and construction with timeout≥lease fails with
ErrProcessTimeoutExceedsLease.

DISCARD AFTER DEAD-LETTER. The kernel already fires the per-kind hook only after Fail
records the terminal transition; 3.4 proves it end to end
(`TestJobsModeDiscardRunsAfterDeadLetterIdempotent`: the hook reads status==dead_letter
when it runs and running the real discard twice both succeed) and that a hook FAILURE
never resurrects the job (`TestJobsModeDiscardFailureDoesNotResurrect`: status stays
dead_letter and no further send occurs after a hook that returns an error).

LIFECYCLE STATUS + OBSERVER. Status mapping (normalizeStatus, AV3D-2.3) is UNCHANGED and
verified through the receipt flow: a rescheduled (retrying) generic state → auth pending,
a dead_letter → auth failed (`TestJobsModeTransientRetriesBoundedThenDeadLetters` polls
DeliveryStatus pending→failed). The optional `EventObserver` is now WIRED into the
jobs-mode transport: auth.Config gained `DeliveryEventsEmitter` (sdk-typed), NewService
builds the EventObserver from it and passes it to the JobsProcessor, which emits
delivered/skipped/retried in Handle and dead_lettered from Discard (AFTER the recorded
terminal), all via `SafeObserve`. The purged batch event is emitted by the host-driven
purge (DeliveryClaim gained ExecutionID; the Discard seam gained executionID; the runtime
seam gained `Purged`). Proofs: `TestJobsModeObserverEmitsRetryDeadLetterPurge` (retried +
dead_lettered + purged observed) and `TestJobsModeObserverFailureChangesNothing` (an
emitter that fails EVERY Emit changes no delivery outcome — the message still delivers and
the receipt reads succeeded).

BOUNDED TERMINAL RETENTION/PURGE WITHOUT AUTH-SPECIFIC SQL. `jobs.Service.PurgeTerminal`
exposes the store's bounded purge; the host-owned `authjobs.PurgeTerminal(ctx, jobs, rt,
before, limit)` drives it with the caller's retention window/batch and emits the purged
observation. Proof (`TestJobsModePurgeRemovesTerminalStatusSane`): purge removes an old
terminal (completed) generation while a non-terminal (pending) generation survives, and a
status read for the purged key returns sdk.ErrNotFound (sane — never a crash or false
success).

PERMANENT proof (`TestJobsModePermanentDeadLettersImmediately`): a garbage (unopenable)
payload enqueued directly under DeliveryJobKind dead-letters on the FIRST attempt
(Retries==1) under a MaxAttempts=10 cap, with ZERO provider sends — the permanent verdict
short-circuits both retry and send.

Files changed:

- `sdk/foundation/workers/fenced.go` — `FencedRetryDecider` type + `WithFencedRetryDecider`
  option + `retryDecider` field; `handleFailure` prefers the error-aware decider; doc.
- `sdk/foundation/workers/fenced_test.go` — `TestFencedRunner_RetryDeciderDeadLettersPermanentImmediately`
  (permanent error dead-letters at attempt 1; transient reschedules).
- `features/jobs/jobs.go` — `ErrProcessTimeoutExceedsLease`.
- `features/jobs/fenced.go` — `Permanent`/`dispositionError`/`isPermanent`;
  `Service.PurgeTerminal`; NewFencedRuntime timeout<lease validation; the runtime wires
  `WithFencedRetryDecider` (permanent → dead-letter now; else capped-exponential retry-at).
- `features/jobs/fenced_test.go` — permanent-immediate-dead-letter, timeout-inside-lease
  construction validation, and Service.PurgeTerminal tests.
- `features/authentication/internal/logic/delivery/command/processor.go` — `Result.Kind`/`Purpose`.
- `features/authentication/internal/logic/delivery/command/engine.go` — `tag` stamps Kind/Purpose.
- `features/authentication/internal/logic/delivery/jobsprocessor.go` — Observer field +
  `jobFailure`/`HandleErrorPermanent`; Handle emits transitions + returns the disposition;
  Discard takes executionID + emits dead_lettered post-transition; `ObservePurge`; `meta`.
- `features/authentication/authentication.go` — `Config.DeliveryEventsEmitter`;
  `DeliveryClaim.ExecutionID`; `DeliveryJobRuntime.Discard(executionID,...)` + `Purged`;
  `DeliveryErrorPermanent`; NewService builds the EventObserver; DeliveryJobRuntime()
  threads executionID/Purged.
- `examples/auth-cms/internal/authjobs/authjobs.go` — handler translates the verdict
  (permanent → jobs.Permanent) + threads ExecutionID; DeadLetters uses j.JobID; new
  `Purger` + `PurgeTerminal` host-driven purge+observe helper.
- `examples/auth-cms/internal/authjobs/authjobs_test.go` — Discard signature update.
- `examples/auth-cms/cmd/server/main.go` — `authCfg.DeliveryEventsEmitter = bus`;
  runtime `ProcessTimeout = 20s` inside the 30s lease.
- `examples/auth-cms/cmd/server/jobs_delivery_proof_test.go` — `bootDeliveryEmit`
  (emitter variant) + `booted.rt`; bootDelivery delegates (existing callers unchanged).
- `examples/auth-cms/cmd/server/jobs_delivery_retry_test.go` — NEW: the eight AV3D-3.4
  end-to-end proofs + the ctx-respecting gating sender and capturing emitter.

Commands run (all PASS):

- `cd sdk && go build ./... && go test -race ./foundation/workers/... && go vet ./foundation/workers/...`
- `cd features/jobs && go build ./... && go test -race ./... && go vet ./...`
- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
- `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...`
- `cd examples/auth-cms && go test -race -count=3 -run 'TestJobsMode(Transient|Permanent|ParentCancellation|ProviderTimeout|Discard|Observer|Purge)' ./cmd/server/`
  — deterministic across 3 iterations under `-race` (no flake, no data race).
- `make guard` — all guards green. `make check` — "all checks passed". `make tidy` — no drift.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset. Per the task, the inspectable in-memory fenced queue backs every proof; live
pgx/turso retry/timeout/purge/real-interaction proof remains AV3D-3.5.

Premise adaptations:

- "Permanent errors → immediate dead-letter": the auth Engine classifies OutcomePermanent
  for structurally-dead commands (unopenable/no-initializer/seal-failure) and for an
  exhausted budget; there is no provider-permanent signal in the notify seam, so a
  provider send stays transient (retried, capped-exponential, bounded) and the permanent
  path is proven via an unopenable payload. No new provider-permanent detection was added
  (out of scope; no seam exists for it) — the refinement makes the runtime HONOR the
  Engine's existing OutcomePermanent immediately, which was previously retried to the cap.
- The retry TIMING (capped exponential retry-at) is owned by the jobs FencedRuntime
  (already capped-exponential, tunable) and bounded by MaxAttempts; the Engine owns the
  permanent-vs-transient CLASSIFICATION. This keeps the engine the verdict authority while
  the durable retry-at lives where the store reschedule does — the strongest form of
  context-cancellable backoff (no in-runner sleep), consistent with AV3D-1.4.
- The provider-timeout-inside-lease invariant is enforced at the jobs-mode boundary as
  `ProcessTimeout < LeaseFor` (the outer per-attempt bound); the Engine's own inner
  provider timeout (default 15s) remains a tighter bound appropriate to the 30s default
  lease. No auth engine retry/timeout config surface was exposed (unnecessary — the
  runtime knobs the tests already tune drive every proof).
- `sdk/errs` in the standing prompt maps to root `sdk` error kinds here (sdk.ErrNotFound).

### 2026-07-13 — AV3D-3.5 (production, live-store, restart, and real-interaction proof)

Closed the phase-3 proof surface: extended the jobs-mode PRODUCTION construction negatives
(hermetic, run now), built the per-dialect LIVE run-and-look delivery harness (pgx + turso,
build-tag gated, loud-skips now = the open owner gate), and ran the phase-3 gate INCLUDING a
real forgot-password/passwordless drive over live HTTP against the running auth-cms app. No
contract was weakened; production still fails closed on every negative.

CONSTRUCTION NEGATIVES (hermetic — all RUN + PASS):

- `features/authentication/delivery_mode_test.go` gained five jobs-mode cases + two stubs
  (`stubDispatcher` satisfying `DeliveryDispatcher`; `devOnlyMailer` declaring
  `email.Capabilities{DevelopmentOnly:true}`): the AV3D-3.1 DISPATCHER path is now covered by
  the same matrix as the bespoke queue — missing encrypter → `ErrDeliveryEncrypterRequired`;
  dispatcher+encrypter constructs (dev); dispatcher in production without ack →
  `ErrDeliveryJobsUnacknowledged`; dispatcher (no durability metadata) + ack constructs; and a
  development-only transport under an otherwise-complete production jobs wiring →
  `ErrInsecureDeliveryTransport` (the "console/test transport rejected in production" negative,
  reusing the AV3-era `validateDeliveryTransports`). The existing "jobs requires the queue
  capability" case is the BOTH-absent (bespoke queue AND dispatcher nil) incomplete-capability
  negative; the durable-store-in-production and missing-ack negatives were already present
  (AV3D-0.1) and still pass. The dev "jobs with queue and encrypter constructs" case is the
  positive control.
- Invalid timeout/lease/backoff: the store-level `jobs.ErrProcessTimeoutExceedsLease` was
  already unit-covered (`features/jobs/fenced_test.go::TestNewFencedRuntime_ProcessTimeoutInsideLease`,
  AV3D-3.4). AV3D-3.5 adds the COMPOSED jobs-mode negative
  (`examples/auth-cms/internal/authjobs/authjobs_test.go::TestFencedRuntimeConfigRejectsTimeoutExceedingLease`):
  the host wiring `authjobs.FencedRuntimeConfig(rt, …)` → `jobs.NewFencedRuntime` fails closed
  with `ErrProcessTimeoutExceedsLease` when `ProcessTimeout >= LeaseFor` (== and >), and
  constructs when the timeout is safely inside the lease. That is the runtime this host runs
  (main.go sets ProcessTimeout=20s inside the 30s lease).

LIVE RUN-AND-LOOK HARNESS (build now; runs when env exists — env is UNSET here, so the two
dialect tests LOUD-SKIP, which IS the open owner gate, the AV3D-1.5 precedent):

- `examples/auth-cms/cmd/server/jobs_delivery_live_test.go` (NEW, `//go:build livedelivery`) —
  the reusable harness: `liveInspectingQueue` wraps ANY `job.FencedQueueRepository` (embeds the
  INTERFACE, unlike the hermetic `inspectingQueue` which embeds the concrete memstore) so it
  composes over a live-backed store, capturing every persisted payload byte-for-byte, signalling
  checkpoints, and dropping the first Complete for a target execution (the provider-acceptance
  crash). `runLiveDeliveryProofs(t, dialect, open)` runs eight subtests covering the phase-3 live
  list against the real jobs-mode composition (auth.Service → authjobs.Dispatcher → live fenced
  queue → jobs.FencedRuntime → auth delivery processor), reusing the hermetic untagged helpers
  (`bootDelivery`/`bootDeliveryEmit`/`captureSender`/`failingSender`/`runRuntime`/`openSealed`/…):
  KnownUnknownOpaqueAdmissionParity, ProviderTimeoutAndRetryOffRequestPath (a ctx-respecting
  `hangingSender` + ProcessTimeout inside lease → repeated timeout+reschedule off the request
  path, then a healthy restart delivers), RestartAfterOpaqueAdmission, RestartAfterCheckpoint-
  ResendsSameSecret, RestartAfterProviderAcceptanceResendsSameSecret, ResendConvergesToLatest-
  Generation (Replace → gen-1 superseded, gen-2 latest+completed, exactly one delivery),
  StatusAndEventsContainNoSecrets (durable-payload/metadata canary scan + `DeliveryStatus` by
  the PII-free logical key + a `secretScanningEmitter` scanning every emitted lifecycle event),
  and TerminalCleanupAndPurge (garbage payload → permanent dead-letter + discard hook fires;
  host-driven `authjobs.PurgeTerminal` removes the terminal generation, a non-terminal survives).
- `examples/auth-cms/cmd/server/jobs_delivery_live_pgx_test.go` (NEW, `//go:build livedelivery`)
  — `TestLiveJobsDeliveryPGX` opens+migrates a live pgx `NewFencedQueueStore` via
  `POSTGRES_TEST_DSN`, else loud-skips naming all eight proofs.
- `examples/auth-cms/cmd/server/jobs_delivery_live_turso_test.go` (NEW,
  `//go:build livedelivery && integration`) — `TestLiveJobsDeliveryTurso` opens+migrates a live
  turso `NewFencedQueueStore` via `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`, else loud-skips.

PLACEMENT / DEPENDENCY DECISION (the call the task left open): the live harness lives in the
HOST (`examples/auth-cms/cmd/server`) because it must import the host-internal `authjobs`
adapter, `authmem`, and `buildAuthConfig` — the full composition only exists here. It imports
the live jobs stores (`features/jobs/stores/{pgx,turso}`) and datastore connectors
(`pgxdb`/`tursodb`), which resolve through `go.work` in workspace mode (verified: `go build
-tags=livedelivery ./cmd/server` succeeds with NO go.mod edit). The files are BUILD-TAG GATED
(`livedelivery`, turso also `integration`) so the DEFAULT hermetic `make check` / `go build` /
`go test` never compile them and the in-memory host's default build stays byte-for-byte free of
any datastore driver — `make check` stays hermetic-green. `make tidy` was deliberately NOT run:
tidy resolves all build tags and would pull the pgx/libsql driver graph into the in-memory
host's go.mod, contradicting auth-cms's "all in-memory" role; the tagged harness is
workspace-resolved instead. (Flagged as a follow-up owner call under Notes.)

PHASE 3 GATE (results):

- jobs `-race`: `cd features/jobs && go build ./... && go test -race ./... && go vet ./...` — PASS.
- authentication `-race`: `cd features/authentication && go build ./... && go test -race ./...
  && go vet ./...` — PASS (25 packages ok, 0 FAIL).
- auth-cms `-race`: `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...`
  — PASS; `go test -race -count=1 -run 'TestRestartAfter|TestJobsMode|TestReceiptKey|TestSubmitOnce|TestReplace|TestAdversarial' ./cmd/server/`
  — PASS (14.7s; the restart/adversarial/retry harness runs, not cached).
- construction matrix: `go test -run TestNewServiceDeliveryModeMatrix -v .` — 21 subtests PASS
  (incl. the five new dispatcher/dev-transport negatives); `go test -run TestFencedRuntimeConfig
  ./internal/authjobs/` — PASS (composed timeout/lease negative).
- both live dialects (LOUD SKIPS — the open owner gate; env unset):
  - `go test -tags=livedelivery -run TestLiveJobsDeliveryPGX ./cmd/server` — SKIP
    "POSTGRES_TEST_DSN not set — LIVE postgres jobs-mode delivery proof NOT verified (…eight proofs…)".
  - `go test -tags='livedelivery integration' -run TestLiveJobsDeliveryTurso ./cmd/server` — SKIP
    "TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — LIVE turso jobs-mode delivery proof NOT verified (…eight proofs…)".
  - `go vet -tags='livedelivery integration' ./cmd/server` — clean (both dialect harnesses COMPILE).
- REAL INTERACTION (drove the actual running auth-cms app over HTTP, jobs-mode delivery):
  `PORT=8137 go run ./cmd/server` with the stable dev key env; the host logged
  `worker pool starting pool=fenced-delivery` (the generic-jobs FencedRuntime, not a bespoke
  worker). Drove, over live HTTP:
    1. `POST /auth/register` → 201; the fenced-delivery runtime delivered the verification email
       OFF the request path — console line `email (console sender) … subject="Verify your email"
       … code is: 102188`.
    2. `POST /auth/verify {code:102188}` → 200 (account verified).
    3. `POST /auth/password/forgot` → 202 (returns immediately, no request-path provider call);
       the runtime later delivered the reset OFF the request path — `email (console sender) …
       subject="Reset your password"`.
    4. `POST /auth/passwordless/start` → 202; the runtime later delivered the sign-in link OFF
       the request path — `email (console sender) … subject="Your sign-in link"`.
  ENUMERATION-SAFE control observed: forgot/passwordless driven BEFORE verification delivered
  NOTHING (the unverified account has no verified recovery/login identifier → the opaque job
  resolves to a skip), and both starts returned the same 202 shape as the verified case — the
  known/unknown parity holding on the live app. Each 202 returned in ≤ ~0.1ms while delivery
  landed seconds later, the off-request-path proof over real HTTP. Server stopped cleanly
  (graceful shutdown: HTTP drain → delivery runtime stop → bus close).
- migration parity: `diff <(ls features/jobs/stores/pgx/migrations) <(ls .../turso/migrations)`
  and the same for `features/authentication/stores/{pgx,turso}/migrations` — BOTH trees identical
  filename sets (jobs: 0001_job_queue, 0002_job_schedules, 0003_fenced_job_queue; auth: 0001..0014).
- `make guard` — all thirteen guards green.
- `make check` — "all checks passed" (templ no-drift + warm-scaffold-cache + per-module
  vet/build/test + integration-tag turso vet + all guards); the livedelivery-tagged files are
  excluded from the default build, so the hermetic surface is unchanged.

Files changed:

- `features/authentication/delivery_mode_test.go` — `stubDispatcher` + `devOnlyMailer` stubs;
  five jobs-mode construction cases (dispatcher-path encrypter/ack + dev-only-transport-in-prod);
  `email` import.
- `examples/auth-cms/internal/authjobs/authjobs_test.go` — `TestFencedRuntimeConfigRejectsTimeoutExceedingLease`
  (composed ProcessTimeout≥LeaseFor negative + inside-lease positive); `errors`/`time`/`jobsmem` imports.
- `examples/auth-cms/cmd/server/jobs_delivery_live_test.go` — NEW: the `//go:build livedelivery`
  live harness (`liveInspectingQueue`, `hangingSender`, `secretScanningEmitter`,
  `runLiveDeliveryProofs` + eight proof bodies).
- `examples/auth-cms/cmd/server/jobs_delivery_live_pgx_test.go` — NEW: live pgx dialect entry.
- `examples/auth-cms/cmd/server/jobs_delivery_live_turso_test.go` — NEW: live turso dialect entry
  (`//go:build livedelivery && integration`).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`
unset. LOUD-SKIP INVENTORY (the open owner gate): `TestLiveJobsDeliveryPGX` (all eight pgx
proofs) and `TestLiveJobsDeliveryTurso` (all eight turso proofs) — neither ran; both COMPILE
under their tags and are ready to run the moment the env is provided. To close the gate:
`POSTGRES_TEST_DSN=… go test -tags=livedelivery -run TestLiveJobsDeliveryPGX ./cmd/server` and
`TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -tags='livedelivery integration' -run
TestLiveJobsDeliveryTurso ./cmd/server` from `examples/auth-cms`.

Premise adaptations:

- The live proof durability under test is the LIVE jobs fenced queue (the dialect-specific
  component); the auth repositories stay in-memory (authmem) across the modeled restart, exactly
  as the hermetic proofs do — the auth stores' own live conformance is proven separately by
  `features/authentication/stores/{pgx,turso}` storetest. A "restart" rebuilds the jobs/auth
  Services + FencedRuntime over the surviving live store, keying material pinned via
  `stableDeliveryEnv` so a pre-restart sealed payload opens after it.
- stale-claim FENCING (a superseded/reclaimed worker cannot checkpoint/complete/fail) is proven
  live at the store level by `features/jobs/stores/{pgx,turso}` `RunFencedQueue`
  (ReplaceFencesRunningClaim / ClaimFencingOnStaleLease, AV3D-1.5). The live DELIVERY harness
  asserts the observable composition outcome — resend converges to the latest generation — since
  the store-level fence is already the live conformance gate.
- No `make tidy` (documented above): the livedelivery harness is workspace-resolved and
  tag-gated; tidy would pull the pgx/libsql driver graph into the in-memory host's go.mod.
- `sdk/errs` in the standing prompt maps to root `sdk`/`jobs` error kinds here
  (`jobs.ErrProcessTimeoutExceedsLease`).
