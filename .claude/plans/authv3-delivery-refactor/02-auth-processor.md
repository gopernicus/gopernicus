# Phase 2 — transport-neutral authentication delivery processor

Depends on phase 0 and the frozen phase-1 jobs contract. It may begin while jobs
adapters are being implemented only if it touches no shared jobs files and uses
the frozen ports exactly.

## Outcome

Separate authentication delivery policy from queue ownership. One processor must
open, initialize, checkpoint, deliver, classify failure, discard, and observe a
command whether the executor is generic jobs or the bounded in-process runtime.

### AV3D-2.1 — versioned command and processor contract

Define one versioned encrypted command envelope covering:

- kind, purpose, logical receipt key, and opaque/rendered stage;
- normalized resolution input for opaque work;
- rendered destination/content/secret after initialization; and
- enough stable metadata for retry, status, and safe observation.

Parsing rejects unknown versions, malformed stage combinations, missing purpose,
and unsealed durable payloads. Errors and logs never include payload bytes.

The processor receives narrow checkpoint and delivery collaborators and returns
an explicit result: completed, skipped, retry-at, or permanent failure. It does
not claim jobs or own a polling loop.

### AV3D-2.2 — move initialization and delivery policy into the processor

Move the current opaque initialization, renderer/router selection, bounded
provider call, retry classification, and best-effort challenge discard behind the
processor contract.

The sequence is load-bearing:

1. open envelope;
2. if opaque, resolve/issue/render off request path;
3. seal and successfully checkpoint rendered envelope;
4. call provider under timeout;
5. return completion/retry/permanent result; and
6. discard only after a recorded terminal transition callback.

Use the characterization suite to prove identical-secret retry and no send before
checkpoint.

### AV3D-2.3 — dispatcher and secret-free status seams

Replace the internal repository-shaped queue dependency with a transport-neutral
dispatcher supporting submit-once, replace, and latest status. Keep cross-feature
surfaces stdlib-typed or define them at an integration boundary so authentication
imports no jobs package.

Preserve receipt possession plus live-session authorization. Normalize generic
job states into stable auth states without exposing worker name, failure text,
destination, secret, or raw logical key.

### AV3D-2.4 — migrate every producer to the dispatcher

Inventory and migrate all outbound sites, including:

- registration verification;
- forgot/reset password;
- passwordless magic link and OTP;
- step-up/sensitive codes;
- set/change/remove identifier proof and notices;
- OAuth pending-link/unlink paths; and
- invitations/member-added notifications.

For each site record whether it is rendered or opaque, submit-once or replace,
and whether the caller may observe a dispatch error. No site may call a provider
directly.

### AV3D-2.5 — optional lifecycle observer/events adapter

Retain a narrow secret-free observer. Provide an optional host adapter that emits
generic events for operational/domain observation. Event IDs are stable enough for
subscriber de-duplication; payloads contain bounded enums and opaque execution IDs,
not recipient identifiers.

Prove that no observer, missing subscriber, dropped async event, or observer error
can lose, retry, duplicate, or fail accepted delivery work.

### Phase 2 gate

Run the transport-neutral characterization suite, all authentication tests under
`-race`, producer inventory/guards, `make check`, and `make guard`. Both concrete
execution modes may still be test adapters at this point.

## Execution log

Append dated task evidence and the final producer inventory.

### 2026-07-13 — AV3D-2.1 (versioned command and processor contract)

Contract + codec + parsing/sealing tests only. Defined ONE versioned, encrypted
delivery command envelope and the transport-neutral processor contract, in a NEW
sdk-only subpackage. NO initialization/delivery POLICY was moved (that is AV3D-2.2):
the concrete `NewProcessor`/`Process` sequence does not exist yet — only the ports,
value types, Result vocabulary, and the codec it will run against. The existing
bespoke `delivery.Envelope`/`Command`/`Service`/`Worker` and `domain/deliveryjob`
are UNTOUCHED (characterization-before-deletion; they retire in phase 5).

Placement (NEW package `features/authentication/internal/logic/delivery/command`,
per repo convention — mirrors the delivery package's sdk-only shape and the
`deliverychar` subpackage split):

- Rationale: this is delivery POLICY + its encrypted wire format, not a driving/driven
  adapter, so it belongs under `internal/logic/delivery`, sdk-only, importing no
  inbound/outbound/integration/sibling-feature. A focused SUBPACKAGE (not new symbols
  in `delivery`) was chosen because `delivery.Envelope` and `delivery.Command` are
  the bespoke-worker names this supersedes — the versioned envelope and processor
  needed their own non-colliding names (`command.Envelope`, `command.Processor`).
- Reachability (00-overview "Public-boundary direction"): `internal` is correct and
  the composition adapter story is settled in phase 3 — a composition adapter never
  reaches this internal package directly; authentication exposes a PUBLIC delivery
  seam (AV3D-2.3 / phase 3) wrapping the processor, so both the jobs-mode adapter and
  the bounded runtime consume it through that exported boundary while the feature core
  still imports no sibling feature. The package doc records this rationale in-tree.

Envelope (`command.Envelope`, `command/command.go`): `Version` (typed; `Version1`
the only recognized value), `Kind`, `Purpose`, `Key` (PII-free logical receipt key),
`Stage` (`StageOpaque`/`StageRendered`), `ResolutionInput` (opaque work),
`Destination`/`Subject`/`Body`/`HTML`/`Secret` (rendered). Kind/Purpose/Key/Stage are
the stable secret-free metadata safe for retry classification, status, and
observation; the rendered fields never are. `NewOpaque`/`NewRendered` constructors set
the current version (a raw literal omitting `Version` decodes as unknown → fails
closed). `Validate` enforces: recognized version; non-empty kind/purpose/key;
recognized stage; and stage/content consistency (opaque rejects any rendered content
or a missing resolution input; rendered rejects a missing destination or empty
content).

Codec (`command/codec.go`): `Seal` VALIDATES then encrypts (an invalid command never
becomes a durable payload); `Open` decrypts → decodes → re-validates and is the sole
reader of a durable payload. Parsing rejects, each mapped to a STATIC sentinel wrapping
`sdk.ErrInvalidInput`: unknown version (`ErrUnknownVersion` — also catches the zero
value of an unsealed/foreign payload), missing purpose (`ErrMissingPurpose`), malformed
stage combination (`ErrStageMismatch`)/unknown stage (`ErrInvalidStage`), and an
unsealed durable payload (`ErrUnsealedPayload` — non-ciphertext fails the decrypt).
Load-bearing no-leak decision: `Open` returns STATIC sentinels and deliberately does
NOT wrap the `enc.Decrypt`/`json.Unmarshal` errors — a wrapped `json.Unmarshal` error
would echo the DECRYPTED PLAINTEXT (which carries the secret) into the error string.
No branch interpolates any envelope field into an error. Tests prove no secret/
destination bytes appear in the unsealed, malformed, unknown-version, or
stage-mismatch error strings (canary substrings asserted absent).

Processor contract (`command/processor.go`): `Processor` is an INTERFACE
(`Process(ctx, Claim) Result`) a transport ACCEPTS — the concrete struct + policy is
AV3D-2.2's `return structs`. `Result`+`Outcome` give the explicit disposition
(`Completed`/`Skipped`/`Retry`(retry-at)/`Permanent`) with constructors and a
secret-free `Reason`; the transport owns the queue transition. Narrow collaborators:
`Checkpointer` (claim-scoped, fenced payload checkpoint — stdlib `(ctx, sealed []byte)`
so a jobs-mode adapter satisfies it by closing over the frozen jobs `Checkpointer`
WITHOUT importing jobs), `Initializer` (opaque resolve→rendered + best-effort
`Discard`), `Deliverer` (one bounded send). `Claim` carries the sealed payload, the
attempt count, and the claim-scoped `Checkpointer` — the processor NEVER claims work
or owns a polling loop; the durable/bounded runtimes do. The load-bearing sequence
(open → resolve/render → checkpoint → send-under-deadline → result → discard-after-
terminal) is documented on `Processor` as AV3D-2.2's deliverable, not implemented here.

Non-negotiables held: no plaintext destination/identifier/code/token/secret in any
durable payload (Seal encrypts the whole validated envelope; `TestSealPayloadIsOpaque`),
log, error (static sentinels; four no-leak tests), or status (Result/Outcome carry only
lifecycle + coarse reason); authentication imports no jobs package (the seam is
stdlib-typed and structural); `Register` starts no goroutines (unchanged — this task
adds no runtime, only a contract + codec).

Files changed (all NEW; nothing existing modified):

- `features/authentication/internal/logic/delivery/command/command.go` — `Version`,
  `Stage`, `Envelope`, `NewOpaque`/`NewRendered`, `Validate`, the static error
  sentinels, and the package doc + placement rationale.
- `features/authentication/internal/logic/delivery/command/codec.go` — `Seal`/`Open`.
- `features/authentication/internal/logic/delivery/command/processor.go` — `Outcome`/
  `Result` (+ constructors), `Checkpointer`/`Initializer`/`Deliverer`, `Claim`,
  `Processor` interface.
- `features/authentication/internal/logic/delivery/command/command_test.go` — table-
  driven `Validate` + constructor tests.
- `features/authentication/internal/logic/delivery/command/codec_test.go` — seal/open
  round-trip (opaque + rendered), opaque-payload, nil-encrypter, seal-validates, and
  the four no-leak reject tests (unsealed / malformed / unknown-version / stage-
  mismatch) + sdk-kind mapping.
- `features/authentication/internal/logic/delivery/command/processor_test.go` — Result
  constructors, `Outcome.String`, and a stdlib-only fake proving the contract shape is
  implementable without a jobs/domain import.

Premise adaptation: AV3D-0.1's log kept the existing `Repositories.DeliveryJobs`
structural port as the `jobs`-mode queue capability until phase 3; this task likewise
adds the new command/processor contract ADDITIVELY (no deletion, no rewire of the live
worker/service), consistent with the "characterization/contracts land before deletion"
protocol. The processor is defined as an INTERFACE (contract) rather than a concrete
struct because AV3D-2.1 is explicitly "contract + codec + tests" and AV3D-2.2 moves the
policy behind it — shipping a half-implemented `Process` would either pre-empt 2.2 or
leave untested stub bodies. `sdk/errs` in the standing prompt maps to root `sdk` error
kinds here (`sdk.ErrInvalidInput`).

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
- `cd features/authentication && go test -race ./internal/logic/delivery/command/...`
- `make guard` — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. Hermetic contract/codec task run against a real AES-GCM
encrypter; no live-store proof required (deferred to phase 3's AV3D-3.5).

### 2026-07-13 — AV3D-2.2 (move initialization and delivery policy into the processor)

Implemented the concrete processor behind the AV3D-2.1 contract — the `return
structs` the frozen `Processor` interface was waiting for. The current opaque
initialization, renderer/router selection, bounded provider call, retry
classification, and best-effort challenge discard now live in one transport-neutral
`command.Engine`, reimplemented against the same collaborators the bespoke
`delivery.Worker` uses (the worker stays green — characterization-before-deletion;
it retires in phase 5). Nothing existing was modified except two additive lines
(the new `ErrDelivererRequired` sentinel in `command.go`).

Concrete processor (`command/engine.go`): `NewProcessor(ProcessorDeps) (*Engine,
error)` returns `*Engine`, which satisfies both the frozen `Processor` (`Process`)
and a NEW `Discarder` seam. `Process` runs the load-bearing sequence: open the
sealed envelope (`command.Open`); if opaque, resolve/issue/render OFF the request
path via `Initializer`; `Seal` the rendered envelope and SUCCESSFULLY `Checkpoint`
it (claim-fenced) BEFORE any send; perform ONE `Deliverer` send under a bounded
`context.WithTimeout` that sits inside the claim lease; return
`Completed`/`Skipped`/`Retry`(bounded backoff)/`Permanent`. Retry classification is
attempt-budget based and mirrors the worker (`attempt >= MaxAttempts` → permanent;
otherwise `Retry(now+Backoff(attempt))`); a parent-context cancel is a graceful
`Retry(now, interrupted)` left for reclaim rather than a burned attempt; an
unopenable payload, a nil initializer on opaque work, and a seal failure are
immediate `Permanent`. Every `Result.Reason` is a STATIC coarse token (phase name
only) — never the deliverer's raw error, a destination, or a secret.

Discard placement (load-bearing step 6, "discard only after a recorded terminal
transition"): the FROZEN `OutcomePermanent` doc says the TRANSPORT dead-letters and
"the per-kind terminal hook then discards." So `Process` does NOT discard — it
returns `Permanent`, the transport records the dead-letter, and only THEN invokes
`Engine.Discard(ctx, Claim)` (the `Discarder` seam), which opens the checkpointed
payload and voids the challenge (best-effort, idempotent; nil initializer is a
no-op). This preserves the crash-safety ordering: a crash between the terminal
record and the discard leaves the challenge un-discarded (best-effort) rather than
voiding a challenge for still-reclaimable work. Adding `Discarder` is additive (it
does not weaken the frozen `Processor`/`Claim`/collaborator contract) and is exactly
the "move best-effort challenge discard behind the processor contract" this task
was scoped to.

Second characterization harness (`processor_char_test.go`, package `delivery`): a
MINIMAL test executor drives `command.Engine` over an in-memory `pstore` that
simulates claim/checkpoint/retry — NOT a real queue (phases 3/4 supply the durable
and bounded runtimes; the phase-2 gate permits test adapters). The executor claims
the oldest due job, hands the `Engine` a `Claim` bound to a lease-fenced
`checkpointer`, applies the returned `Result` (recording a dead-letter BEFORE
calling `Discard`), and models a crash-after-send by leaving a completed claim
leased so it is reclaimed and the identical secret replayed. It reuses the existing
`fakeClock`/`fakeEncrypter`/`newRouter`/neutral `Provider`/`Initializer` seams and
runs the SAME `deliverychar.Run` suite the worker passes — proving identical-secret
retry, no-send-before-checkpoint, enumeration/known-unknown parity, and
lifecycle-only status on the processor path. The `command` package import is aliased
`cmd` because the inherited worker harness already binds the identifier `command` (a
`Command`-building helper); the worker harness was left untouched. A focused
`command/engine_test.go` also unit-tests the Engine directly (construction, checkpoint-
before-send, skip, permanent/no-initializer/unopenable, deliver classify + timeout
bound, discard).

Non-negotiables held: no account resolution or provider send on the admission
(request) path — `Submit` only enqueues the sealed opaque envelope, and
`OpaqueInitializationOffRequestPath` proves `Inits()==0`/sends==0 at admission; no
plaintext secret/destination in durable payloads (whole envelope sealed), logs, or
`Result`/status (static reasons; secret-free `Observation`); checkpoint precedes
send and retry/replay resends the byte-identical secret
(`CheckpointPrecedesSend`/`CrashAfterSendReplaysSameSecret`); provider calls,
retries, and timeouts are bounded; the processor claims no work and owns no polling
loop (the executor claims); authentication imports no jobs package (the `Checkpointer`
seam is stdlib `(ctx, []byte)`). `command` remains sdk-only (context/time + cryptids).

Files changed:

- `features/authentication/internal/logic/delivery/command/engine.go` — NEW: `Config`,
  `ProcessorDeps`, `Engine`, `NewProcessor`, `Process`, `Discard`, the `Discarder`
  seam, defaults, and `classify`/`defaultBackoff`.
- `features/authentication/internal/logic/delivery/command/command.go` — additive
  `ErrDelivererRequired` sentinel (wraps `sdk.ErrInvalidInput`).
- `features/authentication/internal/logic/delivery/command/engine_test.go` — NEW:
  direct Engine unit tests (stdlib fakes only).
- `features/authentication/internal/logic/delivery/processor_char_test.go` — NEW:
  minimal executor (`pstore` + `processorHarness`) + `TestProcessorCharacterization`
  running `deliverychar.Run` against the Engine.

Premise adaptation: the frozen `Claim` (Payload/Attempt/Checkpoint) carries no
terminal-record callback, so — consistent with the frozen `OutcomePermanent`
contract — the discard is a transport-invoked `Discarder` hook rather than an
in-`Process` step. Both concrete execution modes remain test adapters at this point,
as the phase-2 gate explicitly allows. `sdk/errs` in the standing prompt maps to
root `sdk` error kinds (`sdk.ErrInvalidInput`).

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
  — both characterization harnesses (`TestCharacterization` worker +
  `TestProcessorCharacterization`) green under `-race`.
- `cd features/authentication && go test -race -run 'TestProcessorCharacterization|TestCharacterization' ./internal/logic/delivery/`
- `make guard` — exit 0, all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. Hermetic processor/characterization task run against the
in-memory executor and a reversible test encrypter; no live-store proof required
(durable/live proof is phase 3's AV3D-3.5).

### 2026-07-13 — AV3D-2.3 (dispatcher and secret-free status seams)

Replaced the delivery service's internal repository-shaped queue dependency with a
transport-neutral `Dispatcher` seam and added the secret-free status normalization.
`delivery.Service` now depends on `Dispatcher` (submit-once / replace / latest
status) instead of `deliveryjob.Repository` directly; the bespoke repository becomes
ONE dispatcher implementation (an internal `repoDispatcher` bridge) so current
behavior keeps working. The durable jobs-mode and bounded-mode dispatchers land in
phases 3/4. The bespoke `delivery.Worker` and `domain/deliveryjob` are UNTOUCHED
(they retire in phase 5).

Dispatcher shape decision (the codec-neutral seam) — `Submit`/`Replace(ctx, kind,
purpose, logicalKey string, payload []byte) (executionID string, err error)` +
`LatestStatus(ctx, logicalKey string) (state string, err error)`:

- All surfaces are STDLIB-TYPED, so the authentication core imports no jobs (or any
  sibling) feature and a phase-3 composition adapter OUTSIDE both features can
  implement it and bridge each call to the frozen jobs `KeyedEnqueuer`/
  `KeyStatusReader` (dropping the `kind`/`purpose` params — a generic-jobs payload
  carries the rail and purpose INSIDE its encrypted `command.Envelope` — and mapping
  the lifecycle string). The extra `purpose` param (vs `KeyedEnqueuer`'s three) is
  the deliberate codec-neutral union: the bespoke store keeps purpose as a COLUMN
  (needs it as a param to route the untouched worker), while a generic-jobs store
  keeps it inside the sealed payload; carrying it as a param lets ONE seam serve both
  storage models without the seam knowing the codec. It is bridgeable (a 4→3 param
  drop), which is all the phase-3 constraint requires.
- The seam moves OPAQUE sealed bytes: in AV3D-2.3 those bytes are the existing
  `delivery.Envelope`-sealed payload (so the untouched bespoke worker's
  `delivery.Open` + purpose column route exactly as before, and every existing
  `service_test`/characterization guarantee holds byte-for-byte). Phase 3 swaps the
  codec to the AV3D-2.1/2.2 `command.Envelope` + `command.Engine` runtime behind the
  SAME dispatcher seam; the dispatcher is codec-agnostic, so this is a
  drop-in-implementation change, not a seam change.

Placement rationale (the port lives in `internal/logic/delivery`, exported as
`delivery.Dispatcher`): the composition adapter satisfies the port STRUCTURALLY with
stdlib types — it never imports the interface — so nothing custom needs exporting
outside `internal` for AV3D-2.3 (this is exactly why stdlib-typed was chosen; "the
port and its parameter types must be exported where structurally necessary"
resolves to "nothing"). Defining it in `delivery` (not in each consumer) keeps ONE
transport-neutral contract next to the service that consumes it and the status
normalization that pairs with it; `delivery.Service` (the logic-layer service the
producers already call) is the "authentication service layer" wired to consume it.
Producers (`authsvc`/`invitationsvc`) and their `delivery.Command`-shaped seams are
UNCHANGED — the per-site rendered/opaque + submit-once/replace inventory is AV3D-2.4.

Secret-free status (`normalizeStatus`, `bespokeToGeneric`): the generic job lifecycle
(`pending`/`running`/`completed`/`failed`/`dead_letter`/`canceled`/`superseded`,
mirrored as plain-string constants so authentication imports no jobs) folds into the
EXISTING stable auth `DeliveryStatus` words (`pending`/`succeeded`/`failed`/
`canceled`) — the observable receipt contract does not move as the transport does.
The map is TOTAL and an UNRECOGNIZED state maps SAFELY to a non-terminal `pending`
(never a false success/failure, never an echo of the raw string a caller could
enumerate on). `pending`/`running`/`failed`(retryable) → pending; `completed` →
succeeded; `dead_letter` → failed; `canceled`/`superseded` → canceled. The bespoke
TERMINAL `failed` maps to the generic `dead_letter` (not the generic retryable
`failed`) so a bespoke terminal failure normalizes to `StatusFailed`. Receipt
possession + live-session authorization are preserved exactly: `DeliveryStatus`
(handler) still gates on `RequireLiveSession` + receipt-key possession and the
service `Status` read still never leases/mutates/resolves.

No-leak: the status projection carries only `State` (a stable lifecycle word),
`Pending`, and `Failed` — never worker name, failure text, destination, secret, or
the raw logical key. `TestDeliveryStatusNoLeak` dumps the projection for a pending
AND a terminally-failed job (whose `LastError` deliberately embeds the destination)
and asserts neither the secret, the destination, nor the raw key appears.

Premise adaptation (attempt count): the transport-neutral status seam is
lifecycle-only (the frozen jobs `KeyStatusReader` returns a lifecycle string and
nothing more), so `DeliveryStatus.Attempt` is NOT carried across it and reads 0 —
the attempt counter is executor-internal retry bookkeeping, not a stable lifecycle
signal. The `DeliveryStatus` TYPE is preserved unchanged (field retained) so the
public projection and the frozen `deliverychar.Observation` mapping stand; the
characterization suite asserts `Pending`/`Failed`/`Delivered()` (never the attempt
VALUE), so it stays green. This is the one observable behavior change (the JSON
status response's `attempt` now reads 0); flagged for the AV3-9.7 reviewer wave.

Non-negotiables held: no plaintext secret/destination/identifier in any status,
log, or error (the projection is coarse lifecycle-only; sealing happens in
`delivery.Service` via the existing `delivery.Seal` which propagates its encrypt
error unchanged — `TestServiceEnqueueEncryptionFailure` still matches `errBoom`);
authentication imports no jobs package (the seam is stdlib-typed and structural);
`Register` starts no goroutines (this task adds no runtime — only a seam, a bridge,
and normalization); no goroutine per request (submit is a bounded enqueue).

Files changed:

- `features/authentication/internal/logic/delivery/dispatcher.go` — NEW: `Dispatcher`
  port; stable auth status constants (`StatusPending`/`StatusSucceeded`/`StatusFailed`/
  `StatusCanceled`); generic-state constants (jobs vocabulary mirrored as strings);
  `JobKind`; `normalizeStatus`; `repoDispatcher` bespoke bridge + `bespokeToGeneric`.
- `features/authentication/internal/logic/delivery/service.go` — `Service` now holds
  a `Dispatcher` (was `deliveryjob.Repository`); `ServiceDeps` gains optional
  `Dispatcher` (nil → build `repoDispatcher` from `Repo`); `Enqueue`/`Replace` seal +
  dispatch; `Status` normalizes `LatestStatus`; `build`/`receipt` replaced by `seal`;
  `Status` struct doc updated (Attempt not carried by the seam).
- `features/authentication/internal/logic/delivery/dispatcher_test.go` — NEW:
  `TestDispatcherContract` (submit-once / replace / latest-status, run against BOTH
  the `repoDispatcher` and an in-memory `memDispatcher` double); `TestNormalizeStatus`
  (total map + unknown-safe + no-raw-echo); `TestBespokeToGeneric`;
  `TestServiceConsumesDispatcher` (service seals payload + routes secret-free metadata
  + normalizes every generic state, via a recording `fakeDispatcher` + real AES-GCM);
  `TestDeliveryStatusNoLeak`.

Premise adaptation (scope): the phase-file wiring bullet says "the bespoke queue
becomes one dispatcher implementation so current behavior keeps working" — realized
by adapting `deliveryjob.Repository` behind `repoDispatcher` and routing
`delivery.Service` through the `Dispatcher`, NOT by rewiring the individual producer
call sites (registration/forgot/passwordless/invitations) to the raw seam — that
exhaustive rendered-vs-opaque + submit-once-vs-replace inventory is AV3D-2.4. The
seam stays codec-agnostic on `delivery.Envelope` bytes for this task; the switch to
`command.Envelope` + `command.Engine` is phase 3, per "characterization/contracts
land before deletion". `sdk/errs` in the standing prompt maps to root `sdk` error
kinds (`sdk.ErrNotFound`, `sdk.ErrInvalidInput`).

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
  — both characterization harnesses (`TestCharacterization` worker +
  `TestProcessorCharacterization`) still green under `-race`; new dispatcher/
  normalization/no-leak suites green.
- `make guard` — all guards green.
- `make check` — "all checks passed" (run as a full-workspace safety gate; no public
  surface type or other module changed — the change is isolated to
  `internal/logic/delivery`).

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. Hermetic seam/normalization task run against the in-memory
`memRepo`/`memDispatcher` and a real AES-GCM encrypter; no live-store proof required
(durable/live proof is phase 3's AV3D-3.5).

### 2026-07-13 — AV3D-2.4 (migrate every producer to the dispatcher)

Inventoried every outbound producer site and verified each already admits through the
transport-neutral dispatcher seam — no migration of a call site was required, and a
structural guard now fails if a future producer bypasses it. The producer packages are
exactly `authsvc` and `invitationsvc` (the only non-`delivery` packages under
`internal/logic`); `internal/inbound` handlers are thin delegations that neither send
nor enqueue. Every send site routes through the `deliveryQueue` seam
(`Enqueue`/`Replace`/`Status`), which `delivery.Service` backs with the AV3D-2.3
`Dispatcher`. The provider-send surface (`delivery.Router.Deliver`, `email.Sender.Send`,
`notify.Notifier.Notify`) is invoked ONLY inside the `delivery` package
(`worker.go`/`router.go`/`command/engine.go`) — never a producer. Producers call
`delivery.Router.Render` (envelope production, no send) and hold no
`deliveryjob.Repository`; `deliveryjob.Job`/`Envelope` appear in `authsvc` only as the
`Initialize`/`Discard` worker-callback parameter types, not on a producer path. So the
"migrate any that render/send directly or reach the repository-shaped queue" clause
resolved to zero migrations — the AV3-4.3/AV3D-2.3 wiring already put every producer on
the dispatcher-backed path transitively; this task PROVES it and pins it.

Structural guard (`internal/logic/delivery/producer_seam_test.go`, `package
delivery_test`, stdlib `go/parser`+`go/ast` only — no third-party dep, FS1 held): a Go
test beside the seam it guards (a Makefile grep would be disproportionate for a single
feature's internal layering, which the phase file explicitly permits). It enumerates
every sibling directory of `delivery` under `internal/logic` — so a NEWLY ADDED producer
package is covered automatically, by directory, not a hand-maintained list — parses each
non-test `.go` file, and fails on any call to a provider-send verb (`Deliver`/`Send`/
`Notify`). It asserts it actually scanned `authsvc` and `invitationsvc` (a guard that
silently scans nothing is a false green). Negative-verified: a throwaway
`internal/logic/zzprobe` package with one `s.Send()` call tripped it with a file:line
diagnostic; removed after confirming, leaving the guard green on the real tree. `Render`
is deliberately NOT banned — it produces the sealed Envelope and never sends, so it is
the sanctioned request-path step.

Enumeration-safety semantics were left byte-for-byte as characterized: `ForgotPassword`
and `StartPasswordless` still enqueue an OPAQUE command carrying only the normalized
`ResolutionInput` (no account resolution, no challenge, no provider call on the request
path), still return `nil` on a malformed identifier (uniform accepted), and the worker's
`Initialize` still resolves-or-not off-path. The dispatch error they DO surface is a
PII-free transport/enqueue error identical for known and unknown identifiers (it precedes
any account lookup), so observing it leaks no existence signal.

FINAL PRODUCER INVENTORY (11 sites; all admit through the `deliveryQueue` seam →
`delivery.Service` → `Dispatcher`; zero call a provider directly):

| # | Flow | Call site (file:func) | Rendered / Opaque | Submit-once / Replace | Caller observes dispatch error? |
|---|---|---|---|---|---|
| 1 | Registration verification | `authsvc/service.go:Register` (via `enqueueRendered`) | Rendered (account just created; not enumeration-sensitive) | Submit-once (`Enqueue`) | Observes — returned with the created account; user can resend later |
| 2 | Forgot / reset password | `authsvc/service.go:ForgotPassword` | Opaque (`ResolutionInput` only; worker resolves+issues+renders) | Submit-once (`Enqueue`) | Observes the enqueue error, but PII-free & pre-lookup (identical known/unknown); malformed → `nil` |
| 3 | Passwordless magic link / OTP | `authsvc/passwordless.go:StartPasswordless` | Opaque (`ResolutionInput` only) | Submit-once (`Enqueue`) | Observes the enqueue error (pre-lookup, PII-free); malformed → `nil` |
| 4 | Step-up / sensitive code | `authsvc/stepup.go:BeginStepUp` (via `enqueueRendered`) | Rendered (session-authenticated; dest already owned+verified) | Submit-once (`Enqueue`) | Observes — returned to caller |
| 5 | Remove-password sensitive code | `authsvc/password.go:StartRemovePassword` (via `enqueueRendered`) | Rendered (authenticated; recovery identifier) | Submit-once (`Enqueue`) | Observes — returned to caller |
| 6 | Set/change identifier proof | `authsvc/identifier_management.go:StartIdentifierChange` (via `enqueueRenderedReplace`) | Rendered (proof code to proposed NEW address) | Replace (caller-driven resend supersedes stale code) | Observes — returned to caller |
| 7 | Identifier change notice | `authsvc/identifier_management.go:enqueueIdentifierChangeNotices` (via `enqueueRendered`, per recipient) | Rendered (no secret; security notice to prior channels) | Submit-once (`Enqueue`) | Swallowed — best-effort; mutation already committed; coarse WARN by error kind |
| 8 | OAuth unlink sensitive code | `authsvc/oauth.go:StartUnlinkOAuth` (via `enqueueRendered`) | Rendered (authenticated; recovery identifier) | Submit-once (`Enqueue`) | Observes — returned to caller |
| 9 | OAuth pending-link | `authsvc/oauth.go:startPendingLink` (via `enqueueRendered`) | Rendered (matched existing identifier; not enumeration-sensitive) | Submit-once (`Enqueue`) | Observes — and rolls back the just-created `oauthstate` on error so no orphaned secret |
| 10 | Invitation | `invitationsvc/service.go:sendInviteSent` | Rendered (kind-aware; accept token in link) | Replace when `resend`, else Submit-once (`Enqueue`) | Observes — returned to caller |
| 11 | Member-added notice | `invitationsvc/service.go:sendMemberAdded` | Rendered (no secret; you-were-added notice) | Submit-once (`Enqueue`) | Swallowed — best-effort; grant already committed; coarse WARN by error kind |

Non-negotiables held: no account resolution or provider call on the unauthenticated
request paths (sites 2/3 stay opaque — proven by the untouched characterization suite);
no plaintext secret/destination in durable payloads/logs/errors/status (`Seal` encrypts
the whole envelope; the two best-effort WARN lines log `error_kind` only); no goroutine
per request (every site is a bounded enqueue); authentication imports no jobs package
(the seam is the stdlib-typed `deliveryQueue`); `Register` starts no goroutines (this
task adds only a test). No contract was weakened.

Premise adaptation: the phase-file bullet "migrate all outbound sites … no site may call
a provider directly" resolved — after full inventory — to a VERIFY-AND-PIN task rather
than a rewire, because AV3-4.3 (request-time send → durable outbox) plus AV3D-2.3
(service → Dispatcher) had already routed all eleven sites through the seam; the
remaining deliverable is the enumeration inventory (above) and the structural tripwire
that keeps it true. Unused-field note (NOT fixed — out of scope, surgical-diff): after
the outbox refactor `authsvc.Service.mailer` (`email.Sender`) is stored in `NewService`
but never read on any code path (only `invitationsvc` still reads its `mailer`/
`notifiers` for the `kindSupported` deny-by-absence capability check, not to send);
flagged for the AV3-9.7 reviewer wave / phase-5 cleanup.

Files changed:

- `features/authentication/internal/logic/delivery/producer_seam_test.go` — NEW:
  `TestNoProducerBypassesDispatcherSeam` — AST guard over every producer package under
  `internal/logic` asserting no direct `Deliver`/`Send`/`Notify` call (stdlib only).

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...`
  — all suites green under `-race`, including the two characterization harnesses and the
  new seam guard.
- `cd features/authentication && go test -run TestNoProducerBypassesDispatcherSeam ./internal/logic/delivery/`
  — guard green; negative-verified against a throwaway violating package (then removed).
- `make guard` (repo root) — all thirteen guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. This task is a static inventory + structural guard over source;
no store or provider is exercised, so no live-store proof applies (durable/live delivery
proof remains phase 3's AV3D-3.5).

### 2026-07-13 — AV3D-2.5 (optional lifecycle observer/events adapter) — CLOSES PHASE 2

Defined the narrow, secret-free lifecycle observer seam in the transport-neutral
`command` package and shipped an OPTIONAL events adapter that publishes generic
`sdkevents.Emitter` (Mount.Events rail) events for operational/domain observation.
Proved — under `-race` — that no observer, a missing subscriber, a dropped async event,
an erroring observer, or a panicking observer can lose, retry, duplicate, or fail
accepted delivery work. Nothing existing was modified except additive test wiring in the
processor characterization harness; the bespoke `delivery.Worker.Observer` seam is
untouched (it retires with the worker in phase 5).

Observer seam (`command/observer.go`, sdk-only — imports `context` only): `Transition`
is a BOUNDED enum (`accepted`/`initialized`/`skipped`/`delivered`/`retried`/
`dead_lettered`/`superseded`/`purged`) with a stable `String()`; `LifecycleEvent` carries
ONLY `ExecutionID` (opaque unit-of-work ID), `Kind`/`Purpose` (bounded envelope enums),
`Transition`, `Attempt`, and a purge `Count` — no destination, secret, resolution input,
or raw logical key has a field to travel through. `Observer.Observe(ctx, LifecycleEvent)
error` is OPTIONAL and observation-only. `SafeObserve(ctx, obs, ev)` is the containment
boundary the transport calls: nil observer → zero-cost no-op; a panic → recovered; a
returned error → swallowed; returns NOTHING so the transport can never branch on
observation. The processor never emits — the TRANSPORT (durable jobs runtime / bounded
pool / the phase-2 test executor) owns the queue transition and is the sole observer
caller, mapping each `Result` and each admission/supersession/purge into one transition.

Placement rationale: the seam lives in `command` (not `delivery`) because it is the
transport-neutral lifecycle VOCABULARY both concrete runtimes (phases 3/4) consume
alongside `Outcome`/`Result`/`Claim` — the transport already imports `command` and holds
the `Engine`, so it holds the `Observer` and calls `SafeObserve` from the same import.
Keeping it sdk-only (context only) means the processor package gains no events/bus
dependency. The EVENTS adapter is the driven side and lives one layer out, in the
`delivery` package, so the events-bus import stays out of the sdk-only processor core.

Events adapter (`delivery/observer.go`, imports `command` + `sdk/capabilities/events`):
`EventObserver` satisfies `command.Observer` and maps a `LifecycleEvent` onto a
`DeliveryLifecycle` generic event, one bounded type per transition
(`authentication.delivery.<transition>`), then `Emit`s best-effort. The de-duplication
`ID` is STABLE — `ExecutionID + ":" + transition` (a batch purge, which has no execution
ID, uses the transition token alone) — never random, so a redelivered/at-least-once event
collapses on a subscriber. Per-execution events carry the opaque execution ID as the
aggregate ID for SSE routing (never a recipient or the logical key). `Observe` logs an
emit failure by stable transition and returns nil (best-effort, cms `emit` convention); a
nil emitter defaults to `sdkevents.Noop`, a nil logger to `slog.Default`.

Independence proof (all under `-race`): (1) `SafeObserve` unit tests — nil is a no-op, a
returned error is contained, a panic is recovered (the test would itself panic if the
recover were missing). (2) The processor characterization harness now wires the observer
at every transition (Accepted on new admission, Superseded+Accepted on replace,
Initialized on a successful checkpoint, Delivered/Skipped/Retried on the matching
outcome, DeadLettered AFTER the dead-letter is recorded, Purged on a non-empty sweep) and
runs the FULL `deliverychar.Run` suite THREE more times: with an observer that errors on
every transition, with one that panics on every transition, and with the real
`EventObserver` over a recording emitter. All pass IDENTICALLY to the nil-observer
`TestProcessorCharacterization` — the suite IS the outcome spec, so identical passes prove
observer variants change no delivery outcome. (3) Positive tests prove the observer is
actually exercised (rendered → Accepted,Delivered; opaque → Accepted,Initialized,
Delivered; permanent failure → Accepted,DeadLettered with status still `Failed`), so
parity is not vacuous. (4) The events adapter tests pin per-transition mapping, stable
(non-random) dedup IDs, the batch-purge shape, best-effort error swallowing, the Noop
default, and a secret-free encoded payload (canary substrings absent).

Non-negotiables held: events are NEVER required to make accepted delivery happen (the
transport records state; the observer is a strictly-after, best-effort side effect);
observer failure never changes recorded state (`SafeObserve` swallows error + recovers
panic; the DeadLettered observation runs AFTER the terminal record); no plaintext
secret/destination/identifier/raw-key in any event payload (`LifecycleEvent` has no such
field; the encoded-payload no-leak test confirms); `Register` starts no goroutines (this
task adds a synchronous seam + adapter, no runtime); authentication imports no jobs
package (the seam is stdlib-typed; `command` imports only `context`; `delivery/observer.go`
imports only `command` + `sdk/capabilities/events`). No contract was weakened.

Premise adaptation: the phase file says "retain a narrow secret-free observer" — the
retained seam is REDEFINED in the transport-neutral `command` package (not the bespoke
`delivery.Worker.Observer`, which is worker-shaped and dies in phase 5) so both phase-3/4
runtimes consume one seam; the bespoke worker observer is left green and untouched per
characterization-before-deletion. The "initialized" transition is emitted by the
transport when a checkpoint SUCCEEDS (checkpoint happens only on opaque initialization),
since the `Engine` is pure and never emits; the remaining transitions map from `Result`
and admission/purge. Both concrete execution modes remain test adapters at this point, as
the phase-2 gate explicitly allows. `sdk/errs` in the standing prompt maps to root `sdk`
error kinds (`sdk.ErrInvalidInput`).

Files changed (all NEW except the additive harness wiring):

- `features/authentication/internal/logic/delivery/command/observer.go` — NEW:
  `Transition` enum + `String`, `LifecycleEvent`, `Observer`, `SafeObserve`.
- `features/authentication/internal/logic/delivery/command/observer_test.go` — NEW:
  `Transition.String`, `SafeObserve` nil/forward/error/panic containment.
- `features/authentication/internal/logic/delivery/observer.go` — NEW: `DeliveryLifecycle`
  event, `EventObserver` (satisfies `command.Observer`), `NewEventObserver`,
  `newDeliveryLifecycle` (stable dedup ID).
- `features/authentication/internal/logic/delivery/observer_test.go` — NEW: per-transition
  mapping, stable ID, purge-batch shape, secret-free payload, error-swallow, Noop default.
- `features/authentication/internal/logic/delivery/processor_char_test.go` — MODIFIED
  (additive): observer field + `observe` helper + transition emission in
  Submit/Replace/Drain/Purge/checkpointer; `enqueue`/`replace` now report
  inserted/superseded; erroring/panicking/healthy full-suite parity tests + three positive
  observation tests.

Commands run (all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` —
  ALL-PASS (14 ok packages, 0 FAIL); the four characterization harness runs
  (`TestProcessorCharacterization` nil + erroring + panicking + healthy) all green under
  `-race`.
- `cd features/authentication && go test -race -run 'TestNoProducerBypassesDispatcherSeam|TestProcessorCharacterization|TestEventObserver|TestSafeObserve|TestProcessorObserver|TestTransitionString' ./internal/logic/delivery/...`
  — green (AV3D-2.4 tripwire still green with the new package files present).
- `gofmt -l` on all five files — clean.

PHASE 2 GATE (AV3D-2.5 closes phase 2) — all PASS:

- Transport-neutral characterization suite, BOTH harnesses: `TestCharacterization`
  (bespoke worker) and `TestProcessorCharacterization` (command `Engine` executor) green
  under `-race`, plus the three additional observer-variant processor runs.
- All authentication tests under `-race`: `go test -race ./...` — 14 ok, 0 FAIL.
- Producer inventory/guards (AV3D-2.4 tripwire): `TestNoProducerBypassesDispatcherSeam`
  green (the new `delivery/observer.go` calls `Emit`, not a banned `Deliver`/`Send`/
  `Notify`, and is inside the `delivery` package the guard does not scan as a producer).
- `make check` (repo root) — "all checks passed" (templ drift + per-module vet/build/test +
  integration-tag compile vet + all guards).
- `make guard` (repo root) — all guards green.

Live-store availability: `POSTGRES_TEST_DSN` unset; `TURSO_DATABASE_URL`/
`TURSO_AUTH_TOKEN` unset. Hermetic observer/adapter/parity task run against the in-memory
executor, a recording emitter, and a reversible test encrypter; no live-store proof
required (durable/live delivery + real-bus proof remains phase 3's AV3D-3.5).
