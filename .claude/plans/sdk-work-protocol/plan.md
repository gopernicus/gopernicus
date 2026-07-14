# sdk-work-protocol — promote the keyed-work submission protocol into sdk

Status: **EXECUTED 2026-07-13 — all six tasks (SWP-1..SWP-6) complete and green; see
the execution log. Tree left uncommitted for the AV3-9.7 wave. One pre-existing
auth-v3 HTML passwordless-start defect found during run-and-look and FIXED
same-session post-milestone by owner direction (see the SWP-6 log entry). Prior
Codex/steward/backend revisions were folded before execution; the owner's /loop
instruction served as ratification.**
Working name: `sdk-work-protocol`; task prefix: `SWP`.
Insertion point: **before the `AV3-9.7` reviewer wave**, on top of the already-uncommitted
auth-v3 + AV3D tree. **No commits, no pushes, no tags** — the 9.7 wave audits the final
shape once.

## Context

AV3D closed with the generic keyed-work submission shape duplicated on exactly one
bridge: `features/jobs` exports the primitive-typed seams (`primitives.go`:
`KeyedEnqueuer`/`KeyStatusReader`/`Checkpointer` — currently **zero** code importers)
and `features/authentication` mirrors the seven lifecycle strings as bare constants
(`internal/logic/delivery/dispatcher.go` `genState*`) solely to avoid importing jobs
(constitution rule 6). The shape is frozen (AV3D-0.3), conformance-proven
(`storetest.RunFencedQueue`, three implementations), and consumed structurally through
one host adapter (`examples/auth-cms/internal/authjobs`). This milestone promotes the
**consumer-facing** half into sdk as a platform protocol and ratifies the graduation
doctrine that governs when a shape may do that again.

## Outcome

`sdk` owns the keyed-work submission **protocol** — submit-once by logical key,
replace/supersede, latest-status by logical key, opaque `[]byte` payload, a typed
lifecycle `Status` — with a conformance suite; `features/jobs` is the **implementation
of record** and passes it; authentication drops its mirrored bare-string lifecycle
constants for the sdk typed vocabulary with **zero observable change** to its
DeliveryStatus projection; and the "sdk defines protocols; features are implementations
of record or build on them" doctrine is reconciled across ARCHITECTURE.md, sdk/README.md,
and features/README.md §5.

## Decision record (base direction ratified 2026-07-13; Codex revisions await owner ratification)

### D1 — Doctrine: sdk defines protocols; features implement or build on them

sdk owns the **interoperability grammar** (vocabulary + narrow contracts + conformance
semantics); features either implement that grammar or build domain behavior on it while
retaining their own aggregates, schema, lifecycle, and routes. The relationship is not
uniform: `features/authentication` implements `sdk/foundation/identity.Resolver` while
keeping users/credentials feature-owned; `features/events` CONSUMES an
`sdk/capabilities/events.Bus` to build the durable-outbox/SSE domain and is not the Bus
implementation; `features/jobs` implements the new keyed-work protocol while keeping
its durable aggregate/runtime feature-owned. Jobs is the missing canonical protocol,
not evidence that every feature must implement the sdk contract it builds on.

### D2 — Promote NOW (the entrenchment argument)

The shape is frozen (AV3D-0.3), its repository semantics are conformance-proven across
memstore/pgx/turso, and it is duplicated on exactly one bridge. The sdk-level protocol
suite added here separately proves its consumer contract through a test-only inspector;
the existing store suite remains the executor-side proof. Today this is a naming
decision; after a second consumer copies the mirror, it becomes a migration. Waiting
buys nothing.

### D3 — Protocol v1 is CONSUMER-FACING ONLY

In: `EnqueueOnce` (idempotent-while-active by PII-free logical key), `Replace`
(supersede all active generations + fresh execution ID), `LatestStatusByKey`
(deterministic latest-by-key lifecycle read), opaque `[]byte` payload, typed `Status`.

**Explicitly OUT:** claim/checkpoint/lease/fencing (executor-side — the mechanism
already lives in `sdk/foundation/workers.FencedRunner`/`FencedStore`; the domain
contract stays `features/jobs/domain/job.FencedQueueRepository`), `DeadLetterFunc`-style
hooks, scheduling, retry policy, purge/retention. `Job`/`User`/`Event` aggregates never
move. `authentication.DeliveryDispatcher` STAYS auth-owned (it expresses auth-specific
intent — kind/purpose params; the host adapter does legitimate semantic translation).
`examples/auth-cms/internal/authjobs` stays host-owned.

### D4 — Canonical Status strings are the FROZEN set, verbatim

Checked against `features/jobs/domain/job/job.go` and auth's mirror: the frozen
vocabulary is **`pending` / `running` / `completed` / `failed` / `dead_letter` /
`canceled` / `superseded`** — NOT the brief's shorthand spellings
("succeeded"/"dead_lettered"). The protocol adopts the frozen strings verbatim, as the
FULL seven-value set (not a coarser projection): the auth bridge already transports all
seven verbatim and folds them itself (`normalizeStatus`), a coarser sdk projection would
force a second mapping layer at every future bridge, and renaming any string is a
persisted-status-column + mirror migration for zero semantic gain. Semantics carried in
doc + predicate: `failed` is **non-terminal** (retryable, rescheduled); `dead_letter` is
the permanent terminal failure; `Terminal()` is true for
`completed|dead_letter|canceled|superseded`.

### D5 — Placement: `sdk/capabilities/work` (**REVISED by Codex review, 2026-07-13**)

Capability, not foundation. The live layering law classifies `foundation/*` as pure
mechanism/vocabulary with **zero service semantics**, and `capabilities/*` as behavior
ports whose observable contract is pinned by conformance tests. Keyed admission,
idempotency-while-active, atomic replace/supersede, and latest-generation status are
service semantics, even though the implementation of record lives in a feature and no
honest process-local default exists. `sdk/capabilities/oauth` is the existing proof that
a capability need not ship a default.

Placement is determined by what the sdk package MEANS and may depend on, not by where a
current implementation happens to live: pure mechanism/vocabulary → foundation;
behavioral port + observable policy → capability. This keeps the tier predictive if an
implementation later moves or another implementation appears. The package is named
`work`, not `jobs`, so hosts can distinguish the canonical protocol from
`features/jobs` without aliases unless they want them.

`work` imports `context` only; its `worktest` subpackage imports stdlib + the sdk root +
`work`. G1 and G12(c) cover the new directory automatically; no guard edit is required.
SWP-1 reconciles the existing layering/taxonomy prose in BOTH ARCHITECTURE.md and
sdk/README.md so this decision lands without a contradictory "all capabilities have a
default" claim.

### D6 — `features/jobs/primitives.go`: RETIRE

Zero code importers (verified: every hit outside `features/jobs` is a doc comment;
auth's `command` package declares its own local `Checkpointer`). The sdk protocol
supersedes the two consumer seams (`KeyedEnqueuer`, `KeyStatusReader`); `Checkpointer`
is executor-side (out of protocol, D3) with zero importers — a future consumer
redeclares it structurally, exactly as auth's processor already does. `DeadLetterFunc`
has real consumers (`jobs.FencedRuntimeConfig`, `authjobs`) and is jobs' own
host-hook surface, not a cross-feature seam — it folds into `features/jobs/fenced.go`
together with its `workers.FencedDeadLetterFunc[job.Job]` compile-time seam. Then
`primitives.go` is deleted.

### D7 — Doctrine text lands consistently in THREE docs

- **ARCHITECTURE.md**: a short "Protocols and feature relationships" subsection with
  the table: identity → authentication IMPLEMENTS Resolver; events → events feature
  BUILDS ON Bus; work → jobs IMPLEMENTS the protocol; authorization → **deferred**.
  Reconcile the existing layering diagram, sdk-facility taxonomy row, litmus prose,
  "contract lives with the consumer" sentence, and sdk-vs-logic test so none claims
  every capability must have a default or every shared contract must stay local.
- **sdk/README.md**: add `work` to capabilities, explicitly state that capabilities
  own behavioral ports + observable policy and MAY have a stdlib default, an integration
  implementation, or a feature implementation of record; retain foundation's
  pure-mechanism/vocabulary and flat-import fence.
- **features/README.md §5 (C2 amendment)**: consumer-declared ports remain the
  **DEFAULT**; add the "ratified platform protocol" graduation path with five criteria:
  (1) a real producer and a real consumer in separate modules, (2) semantics meant to be
  canonical across gopernicus, (3) no feature aggregate or persistence model in the
  contract, (4) narrow enough for independent implementations, (5) a conformance suite
  can describe observable behavior. **The five criteria are CONJUNCTIVE WITH — never a
  substitute for — sdk/README.md's admission policy and ARCHITECTURE.md's five-point
  sdk-vs-logic test: a graduation must pass all three gates** (steward-required
  precision, so the path can never become an admission back door). Record that
  authorization's check/decision vocabulary explicitly **FAILS criterion 2 today** and
  stays consumer-declared (deferred; trigger: authorizationv3 settles its semantics).

### D8 — The promoted surface (exact, so the implementer has zero latitude)

```go
// sdk/capabilities/work — package work: the keyed-work submission protocol.
// Vocabulary + contract only; NO default implementation (the oauth precedent).
// The implementation of record is features/jobs.

type Status string

const (
    StatusPending    Status = "pending"
    StatusRunning    Status = "running"
    StatusCompleted  Status = "completed"
    StatusFailed     Status = "failed"      // retryable, NON-terminal: rescheduled
    StatusDeadLetter Status = "dead_letter" // permanent terminal failure
    StatusCanceled   Status = "canceled"
    StatusSuperseded Status = "superseded"
)

func (s Status) Terminal() bool // completed | dead_letter | canceled | superseded
func (s Status) Known() bool    // membership in the canonical seven — the totality helper

// Enqueuer is the producer half: idempotent keyed admission.
// payload is opaque bytes the queue never interprets ([]byte, deliberately NOT
// json.RawMessage — auth's payloads are ciphertext, and the protocol must not
// imply JSON).
type Enqueuer interface {
    EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (executionID string, err error)
}

// Replacer is the optional atomic replace/supersede capability. It is segregated
// because an implementation may honestly support keyed admission without replacement.
type Replacer interface {
    Replace(ctx context.Context, kind, logicalKey string, payload []byte) (executionID string, err error)
}

// StatusReader is the status half: the deterministic latest-by-key lifecycle
// projection. Unknown key → the sdk not-found error class. Lifecycle-only:
// never payload, destination, attempt count, or secret.
type StatusReader interface {
    LatestStatusByKey(ctx context.Context, logicalKey string) (Status, error)
}
```

`sdk/capabilities/work/worktest` ships the conformance suite in the repo convention
(`cachertest`/`eventstest`/`filestoragetest` shape):

```go
// Queue is the core submit/status protocol. It does not require optional replacement.
type Queue interface { work.Enqueuer; work.StatusReader }

// ReplaceQueue adds the optional atomic replacement capability.
type ReplaceQueue interface { Queue; work.Replacer }

// Execution is TEST-ONLY inspection vocabulary, not part of the production protocol.
type Execution struct {
    ExecutionID string
    Status      work.Status
    Payload     []byte
}

// Inspector lets the conformance suite prove hidden queue effects without adding a
// production payload/list API. Implementations provide an adapter in their tests.
type Inspector interface {
    ExecutionsByKey(ctx context.Context, logicalKey string) ([]Execution, error)
}

func Run(t *testing.T, newHarness func(t *testing.T) (Queue, Inspector))
func RunReplace(t *testing.T, newHarness func(t *testing.T) (ReplaceQueue, Inspector))
```

`Run` pins the core protocol semantics: enqueue-once idempotency while active (second
`EnqueueOnce` under the same key returns the SAME execution ID AND inspection shows
exactly one execution); latest-by-key determinism; status projection totality (every
returned `Status` satisfies `Known()`; unknown key →
`errors.Is(err, sdk.ErrNotFound)`); payload opacity and byte preservation (arbitrary
non-UTF8/non-JSON bytes are accepted and the test-only inspector sees a deep-copied
payload unchanged). `RunReplace` pins the optional extension: fresh/distinct execution
ID, old execution `StatusSuperseded`, exactly one fresh `StatusPending` generation, and
latest status reflecting the fresh generation across repeated replace sequences. The
Inspector exists ONLY in `worktest`; it must never be promoted into `work` or used by
production consumers. Executor behavior under claim/lease races remains in
`features/jobs/storetest.RunFencedQueue`.

**Adoption consequence (deliberate, pre-tag, on the uncommitted tree):**
`features/jobs.Service`'s three seam methods change signature to satisfy the protocol
by compile-time assertion — `EnqueueOnce`/`Replace` take `payload []byte` (was
`json.RawMessage`; callers passing `json.RawMessage` remain assignable), and
`LatestStatusByKey` returns `(work.Status, error)` (was `(string, error)`). The domain
aggregate (`job.Job`, `job.Enqueue`) and every executor-side signature
(`Checkpoint`, `FencedClaim`, `FencedQueueRepository`) are untouched.

`features/jobs/domain/job.Status` becomes a SOURCE-COMPATIBLE ALIAS of `work.Status`,
and its seven `StatusX` constants alias the sdk constants. The `Job` aggregate remains
feature-owned; only its lifecycle vocabulary takes the canonical sdk type. This removes
the duplicate definition rather than retaining a second source guarded only by tests.

## Definition of Done

- `sdk/capabilities/work` + `worktest` exist, stdlib-only and guard-green.
- `var _ work.Enqueuer = (*jobs.Service)(nil)`, `var _ work.Replacer =
  (*jobs.Service)(nil)`, and `var _ work.StatusReader = (*jobs.Service)(nil)` compile;
  the jobs Service over the memstore fenced queue passes `worktest.Run` and
  `worktest.RunReplace` with a test-only inspector proving execution count,
  supersession, and opaque bytes.
- `job.Status` and all seven `job.StatusX` names alias the sdk vocabulary; there is one
  type/constant source of truth, not two definitions plus a drift detector.
- `dispatcher.go`'s `genState*` mirror is deleted; auth imports `work`; the
  DeliveryStatus projection table test passes byte-identically (same four public
  states, same fold, unknown → pending).
- `features/jobs/primitives.go` is deleted; `DeadLetterFunc` + its compile seam live in
  `fenced.go`; no dangling doc references to the retired seam names.
- Doctrine/taxonomy text is consistent across all three docs, including the
  authorization deferral and the implement-vs-build-on distinction.
- `make check` and `make guard` green across the workspace; a jobs-mode run-and-look on
  `examples/auth-cms` recorded in the execution log.

## Out of scope

- Any executor-side protocol (claim/lease/checkpoint/fence), scheduling, retry policy,
  dead-letter hooks, purge/retention — stays in `sdk/foundation/workers` +
  `features/jobs`.
- Moving `authentication.DeliveryDispatcher`, `DeliveryClaim`, `DeliveryJobRuntime`, or
  the `authjobs` host adapter.
- Any authorization-vocabulary graduation (recorded as deferred, D7).
- Migration/schema changes of any kind; the jobs status column keeps its frozen strings
  by construction (D4).
- Tagging/releasing any module; closing AV3D's open owner gates (live-store runs etc.).
- A `Mount.Jobs` registrar field (C3 candidate list — untriggered).

## Schema / datastore impact

None. No migration files change; no store adapter SQL changes. `job.Status` aliases the
canonical sdk type and the seven constant literals remain byte-identical to the
persisted strings (D4), locked in the sdk package's literal test. `examples/*/workshop/
migrations` and both jobs dialect trees are untouched.

## Module / API impact

- **New sdk packages** (no new module): `sdk/capabilities/work`,
  `sdk/capabilities/work/worktest`. sdk remains require-free.
- **`features/jobs` public API change (pre-tag, uncommitted tree — no RELEASING.md tag
  action):** `Service.EnqueueOnce`/`Replace` payload `json.RawMessage` → `[]byte`;
  `Service.LatestStatusByKey` `(string, error)` → `(work.Status, error)`; `job.Status`
  becomes an alias of `work.Status`; `KeyedEnqueuer`/`KeyStatusReader`/`Checkpointer`
  interfaces are deleted; `DeadLetterFunc` is relocated in-module (same name, same
  package `jobs`).
- **`examples/auth-cms`**: `internal/authjobs` narrow interface re-derived from the sdk
  protocol; `cmd/server` jobs-delivery proof/retry/live tests updated to the new
  signatures.
- **`features/authentication`**: internal-only typed-status adoption across dispatcher,
  in-process queue, tests, and comment tidies; **no exported-symbol change**.

## Generated-artifact impact

None. No `.templ` sources touched; no `make generate`.

## Risks

1. **Signature ripple wider than mapped** — the `json.RawMessage`→`[]byte` and
   `string`→`work.Status` changes touch jobs tests (~21 call sites), authjobs, and the
   auth-cms proof/live tests. Mitigation: everything is on the uncommitted pre-tag
   tree; SWP-5's `make check` sweeps all 36 modules.
2. **Conformance suite under- or over-pinning** — return values alone cannot prove no
   hidden duplicate or that replacement tombstoned the prior generation, while
   executor/lease policy would overreach the consumer protocol. Mitigation: `worktest`
   uses a TEST-ONLY Inspector to prove execution count, status, latest-generation, and
   bytes without adding production read APIs; claim/lease behavior remains exclusively
   in `storetest.RunFencedQueue`.
3. **Doctrine scope creep** — the §5 amendment must not weaken C2's default.
   Mitigation: the amendment's first sentence re-states consumer-declared ports as the
   DEFAULT; graduation is the exception with five conjunctive criteria.

## Standing invariants (hold at every task boundary)

- sdk imports stdlib only (G1); its `go.mod` gains no `require`.
- Capability layering holds: `work` imports stdlib only; `worktest` imports root +
  `work`, never another capability or `sdk/feature` (G12c).
- No feature imports another feature (G7); auth imports sdk only (FS1 — feature→sdk is
  always legal).
- Auth's public surface is observably unchanged: same exported symbols, same four
  DeliveryStatus states, same fold, `Attempt` still reads 0 through the seam.
- Migration trees untouched; `job.Status` persisted strings unchanged.
- No commits, pushes, or tags — the tree stays uncommitted for the AV3-9.7 wave.
- `authentication.DeliveryDispatcher`, `authjobs`, and all feature aggregates stay put.

## Stop conditions (halt, surface to owner + steward — do not plan around)

- The protocol cannot stay sdk-only or capability-layer legal (anything beyond stdlib
  + sdk root/foundation turns out to be required).
- The conformance suite run against the jobs memstore-backed Service reveals the frozen
  semantics are wrong (e.g. enqueue-once not idempotent under the suite's sequences) —
  that is an AV3D defect, not a spec to bend.
- Any step would force a feature aggregate or persistence model into sdk, or would
  change auth's observable DeliveryStatus projection.
- `make guard` requires weakening any existing guard regex to pass.

## Tasks

### SWP-1: Reconcile the protocol doctrine across ARCHITECTURE.md, sdk/README.md, and features/README.md §5

- **depends_on:** []
- **model:** fable
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/ARCHITECTURE.md`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/README.md`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/README.md`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard` (docs-only sanity; no guard may change)
- **description:** Add the ARCHITECTURE.md subsection "Protocols and feature
  relationships (sdk-work-protocol, 2026-07-13)" after the sdk-layering-law section:
  the doctrine sentence (D1), the four-row table (identity → authentication IMPLEMENTS;
  events → events feature BUILDS ON; work → jobs IMPLEMENTS, capability/no default;
  authorization → deferred), and D5's semantic placement rule. Reconcile the existing
  layering diagram (`foundation` = pure mechanism/vocabulary; `capabilities` = behavioral
  ports/policy, defaults optional), sdk-facility taxonomy/default prose, litmus tests,
  broad "contract lives with consumer" sentence, and sdk-vs-logic test. Make the same
  layering/default correction in sdk/README.md and add the `work` package row. Amend
  features/README.md §5's graduation corollary: consumer-declared ports remain the
  DEFAULT; a shape may graduate only as a **ratified platform protocol** meeting all
  five criteria (D7, quoted verbatim); record the authorization deferral. Cite the
  frozen status vocabulary (D4).
- **Acceptance:** all three docs agree on foundation vs capability, optional defaults,
  and implement-vs-build-on; the table and five criteria are present; C2's default is
  restated, not weakened; authorization deferral + trigger recorded; no stale sentence
  still claims every capability has a default or every shared contract lives locally.

### SWP-2: Create `sdk/capabilities/work` + the `worktest` conformance suite

- **depends_on:** [SWP-1]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/capabilities/work/work.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/capabilities/work/work_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/capabilities/work/worktest/worktest.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test -race -count=1 ./... && go vet ./... && cd .. && make guard`
- **description:** Implement D8 exactly: `Status` + the seven frozen constants,
  `Terminal()`, `Known()`, segregated `Enqueuer`, `Replacer`, and `StatusReader`, with a
  package doc stating the doctrine (protocol only, NO default implementation — the oauth
  precedent; the implementation of record is features/jobs; `failed` is non-terminal;
  unknown key is the sdk not-found class; lifecycle-only production status, never
  payload/destination/attempts). `worktest.Run(t, newHarness)` pins the core
  Enqueuer+StatusReader semantics and `worktest.RunReplace(t, newHarness)` pins the
  optional Replacer extension, each with named subtests. Their TEST-ONLY `Inspector`
  verifies execution count, old/new statuses, latest generation, and deep-copied,
  byte-exact opaque payload; the package doc explicitly forbids promoting Inspector
  into the production protocol and leaves claim/lease/executor behavior to storetest.
  `work` imports `context` only; `worktest` imports stdlib (`bytes`, `context`, `errors`,
  `testing`) + sdk root + `work`.
- **Acceptance:** package doc carries the no-default rationale; unit tests cover
  `Terminal`/`Known` over all seven values + an unknown string; **`work_test.go` locks
  the seven constant LITERALS to their frozen strings (`StatusCompleted == "completed"`,
  …) — the `identity_test.go:TestConstantValues` precedent, so the sdk package
  self-guards its wire vocabulary independent of `features/jobs`**. `job.Status` aliases
  these constants in SWP-3, so no independent cross-package literal definition remains;
  Inspector is absent from the production `work` package; guard G12 is green with zero
  guard edits.

### SWP-3: `features/jobs` adopts the protocol; retire `primitives.go`

- **depends_on:** [SWP-2]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/fenced.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/primitives.go` (deleted)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/fenced_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/jobs_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/memstore/protocol_conformance_test.go` (new)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/domain/job/job.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/domain/job/fenced.go` (comment-only)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/README.md`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs && go build ./... && go test -race -count=1 ./... && go vet ./... && cd ../.. && make guard`
- **description:** Change `Service.EnqueueOnce`/`Replace` to `payload []byte`
  (converting internally with `json.RawMessage(payload)`) and `LatestStatusByKey` to
  return `(work.Status, error)` directly from the now-aliased `j.JobStatus`. Replace the
  fenced.go seam assertions with `var _ work.Enqueuer = (*Service)(nil)`,
  `var _ work.Replacer = (*Service)(nil)`, and `var _ work.StatusReader =
  (*Service)(nil)`. Make `job.Status` a type alias of `work.Status` and each
  `job.StatusX` a constant alias of the canonical sdk constant; update `Job.Terminal`
  to delegate to `j.JobStatus.Terminal()` and retain focused parity coverage. Delete
  `KeyedEnqueuer`/`KeyStatusReader`/`Checkpointer`; move `DeadLetterFunc` + its
  `workers.FencedDeadLetterFunc[job.Job]` compile seam into `fenced.go`; delete
  `primitives.go` (D6). Add the conformance test INSIDE package `memstore`, where a
  test-only Inspector can safely snapshot the private queue map under its mutex. It
  builds a `jobs.Service` over that queue, returns the Service + Inspector to both
  `worktest.Run` and `worktest.RunReplace`, and never adds an inspection method to
  production memstore or work. The Inspector clones payload bytes while holding the
  queue mutex so its snapshots stay race-safe.
  Update the `domain/job/fenced.go` comment and README to point at
  `sdk/capabilities/work`.
- **Acceptance:** three compile-time assertions in place; `job.Status`/constants are
  aliases, not duplicate literals; conformance tests prove exact execution count,
  supersession, latest generation, and bytes under `-race`; zero remaining references
  to the three retired interface names anywhere in `features/jobs` production code or
  docs.

### SWP-4: Authentication adopts the typed Status; drop the mirrored constants

- **depends_on:** [SWP-3]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/dispatcher.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/dispatcher_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/inprocess.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/inprocess_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/inprocess_retry_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/service_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication/internal/logic/delivery/command/processor.go` (comment-only)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/authentication && go build ./... && go test -race -count=1 ./... && go vet ./...`
- **description:** Delete the `genState*` bare-string constants; import
  `sdk/capabilities/work`; rewrite `normalizeStatus(state string)` to switch on
  `work.Status(state)` with the IDENTICAL fold (pending/running/failed → auth pending;
  completed → succeeded; dead_letter → failed; canceled/superseded → canceled;
  default/unknown → pending) — totality and the safe-unknown branch preserved
  byte-for-byte in behavior. The four public auth `Status*` constants, the
  `Dispatcher` interface (string-typed `LatestStatus` — auth-owned, stays), `JobKind`,
  and the `Status` struct are untouched. Update the dispatcher.go comments that call
  the constants a "mirror" of jobs (they now cite the sdk protocol) and the stale
  `processor.go:84` "frozen jobs Checkpointer" comment. Change the in-process queue's
  `keyRecord.state` to `work.Status`, use `work.StatusX` for every transition, and
  convert to `string` only at its unchanged string-typed `LatestStatus` boundary.
  Replace every test reference across the four affected test files with the sdk
  constants (converting only where a string-typed fake requires it). Extend the existing
  `normalizeStatus` table test to source inputs from `work.Status*` so drift cannot
  silently reopen.
- **Acceptance:** `grep -n "genState" features/authentication -r` → zero hits; the
  projection table test passes with unchanged expected outputs; no exported auth
  symbol added, removed, or re-typed.

### SWP-5: Sweep the host bridge and workspace (`authjobs`, auth-cms tests, full check)

- **depends_on:** [SWP-4]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/internal/authjobs/authjobs.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/internal/authjobs/authjobs_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/jobs_delivery_proof_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/jobs_delivery_retry_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/jobs_delivery_live_test.go`
  - Must-compile set (route through the string-typed `Dispatcher`; likely zero source
    edits but MUST be confirmed against the new `*jobs.Service` interface satisfaction —
    lead-backend-required completion of the sweep):
    `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms/cmd/server/main.go`,
    `.../cmd/server/main_test.go`, `.../cmd/server/jobs_delivery_test.go`,
    `.../cmd/server/jobs_delivery_replace_test.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/auth-cms && go build ./... && go test -race -count=1 ./... && go vet ./... && go vet -tags="livedelivery integration" ./cmd/server && cd ../.. && make check`
- **description:** Re-derive `authjobs.Enqueuer` from the protocol — replace the
  hand-written three-method interface with
  `interface { work.Enqueuer; work.Replacer; work.StatusReader }` (the bridge explicitly
  requires all three segregated capabilities while other consumers may require less);
  `Dispatcher.Submit`/`Replace` pass `payload []byte` straight through (drop the
  `json.RawMessage` conversions); `LatestStatus` returns `string(st)`. **Keep
  `encoding/json` in `authjobs.go`** — still required for the
  `jobs.FencedClaim.Checkpoint` bridge closure (executor-side, `json.RawMessage`-typed,
  out of protocol and unchanged by this plan). Update the auth-cms delivery
  proof/retry/live tests to the new jobs.Service signatures (the tagged live test must
  still COMPILE under its build tags — the verify line's tagged vet covers it).
  `make check` sweeps every module for stragglers (jobs-minimal, minimal, cms).
- **Acceptance:** `authjobs` contains no re-declared copy of the protocol method set;
  the full must-compile set builds; `make check` fully green; tagged harnesses compile.

### SWP-6: Final gate — guards, workspace check, jobs-mode run-and-look

- **depends_on:** [SWP-5]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.claude/plans/sdk-work-protocol/plan.md` (execution-log append only)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make check && make guard`
- **description:** Run the full workspace check and all fifteen guards. Then the
  real-interaction check (green tests alone do not close this): run `examples/auth-cms`
  per its README with the default `DELIVERY_MODE=jobs`, drive a forgot-password or
  passwordless start in the browser, poll the receipt/status flow, and confirm the
  observable lifecycle is unchanged — it reaches succeeded after the console/demo
  delivery lands, and no raw work lifecycle word leaks beyond the four auth states.
  Observing the transient pending state is NOT required in this unblocked manual run;
  deterministic pending/in-flight coverage remains in the automated bridge tests.
  Append a dated execution-log entry recording commands, results, and the run-and-look
  evidence. No commits.
- **Acceptance:** `make check` + `make guard` green; run-and-look recorded in the
  execution log; tree left uncommitted for AV3-9.7.

## Sequencing

Strictly linear: SWP-1 (doctrine — the decision text reviewers gate on) → SWP-2 (the
protocol + suite, pure addition) → SWP-3 (implementation of record adopts; the only
API-changing task) → SWP-4 (auth drops the mirror) → SWP-5 (the one bridge + workspace
sweep) → SWP-6 (gate). Dependency truth (lead-backend correction): **SWP-4 depends only
on SWP-2** — auth imports `sdk/capabilities/work`, never `features/jobs`; its
unchanged-projection table test runs entirely inside `features/authentication`. The
auth-projection-over-real-jobs-status end-to-end cross-check happens in SWP-5's auth-cms
harnesses. The linear order is retained for review clarity. **Known and intended:
`examples/auth-cms` does not compile between SWP-3 and SWP-5** (the jobs signature
change lands before the bridge sweep); per-task verifies are module-scoped, so a red
`make check` mid-flight is expected, not a defect. Placement is settled by this
revision as `sdk/capabilities/work` (D5); owner ratification supersedes the earlier
steward placement call.

## Consultation notes

The prior architecture-steward and lead-backend reviews remain recorded below. Codex's
2026-07-13 review found an incomplete auth edit set, an underpowered conformance suite,
and a contradiction between the proposed foundation placement and the live layering
law. This revision resolves those findings directly; because it changes the steward's
D5 placement ruling, owner ratification is required before execution.

## Open questions

None — the two earlier questions and the later Codex findings are resolved in this
revision (2026-07-13):

1. **Placement (D5): REVISED — `sdk/capabilities/work`**. Behavioral ports and
   conformance semantics follow the live capability tier; defaults are optional
   (`sdk/capabilities/oauth` precedent). Implementation location does not determine
   sdk tier.
2. **FencedStore in the doctrine table: RULED — plan default ratified**: one clause
   noting executor-side stays workers/jobs; NO table row (a row would invite exactly
   the tier conflation D3 fences off).

## Review record (2026-07-13)

- **architecture-steward: ratify-with-edits (historical pre-Codex pass).** Placement
  `sdk/foundation/work` was ratified
  (corrected discriminator). Required edits (BOTH APPLIED to this plan): (1) D5/SWP-1
  doctrine text rewritten to the implementation-of-record rule — never "capability ⇒
  default" (oauth counterexample); (2) §5 five-criterion path made explicitly
  conjunctive with sdk/README admission + the ARCHITECTURE five-point test. Verified
  independently: G12b regex admits `worktest → work`, excludes `workers`; admission
  passes on its own terms; primitives.go retirement clean. Advisory (not required): a
  `genState` resurrection tripwire guard; a future `job.Status = work.Status` alias
  pass. D5/D8 now supersede the placement and adopt the alias in this plan.
- **lead-backend-engineer: ratify-with-edits (historical pre-Codex pass).** Endorsed:
  the then-two-interface grain,
  worktest dependency direction (jobs→worktest, never sdk→jobs),
  `json.RawMessage`→`[]byte` assignability, drift tripwire, guard mechanics,
  primitives retirement, byte-identical normalizeStatus fold. Required edits (ALL
  APPLIED): (1) D8 payload-opacity semantic reworded to acceptance-only + no-payload-
  read guardrail; (2) SWP-2 gains the in-package constant-literal lock test
  (TestConstantValues precedent); (3) SWP-5 must-compile set completed (main.go,
  main_test.go, jobs_delivery_test.go, jobs_delivery_replace_test.go) + keep
  `encoding/json` in authjobs.go for the Checkpoint bridge; (4) sequencing rationale
  corrected (SWP-4 depends only on SWP-2; auth-cms intentionally red between SWP-3 and
  SWP-5). Cleared as non-issues: failed-non-terminal consistency, tagged livedelivery
  harness compile, migration parity. D8 now further segregates `Replacer` and splits
  core vs replacement conformance entry points.
- **product-manager / platform-sre:** not run this pass — scope is six tasks in one
  pre-9.7 milestone with no tagging/migration/CI surface movement; the AV3-9.7 wave
  provides the broader audit. Escalate to them only if scope grows.
- **Codex review: ratify-with-required-edits.** Six findings, ALL APPLIED by this
  revision: (1) SWP-4 now covers all six `genState`-using files; (2) `worktest` gains a
  test-only Inspector that can prove hidden execution effects; (3) placement moves to
  `sdk/capabilities/work` and all three architecture docs are reconciled; (4) Replace
  is segregated as `work.Replacer`; (5) `job.Status` becomes an alias of canonical
  `work.Status`; (6) the manual pending observation is no longer timing-dependent.
  The earlier steward placement and future-alias advisories above are retained as
  historical review record but superseded by D5/D8.

## Notes

- The AV3-9.7 reviewer wave audits auth-v3 + AV3D + this milestone as one system;
  AV3-9.8 owns accepted remediation. Nothing here closes AV3D's open owner gates
  (live-store conformance, the auth-cms tidy decision, etc.).
- The brief's status spellings ("succeeded"/"dead_lettered") were checked against the
  frozen vocabulary and corrected (D4); this is a plan-level finding, not a code change.

## Execution log

(append-only; empty at ratification)

### 2026-07-13 — SWP-1 complete (doctrine reconciliation)

- ARCHITECTURE.md: added "Protocols and feature relationships (sdk-work-protocol,
  2026-07-13)" after the layering-law section — D1 doctrine sentence, four-row table
  (identity IMPLEMENTS / events BUILDS ON / work IMPLEMENTS / authorization deferred),
  D5 semantic placement rule, executor-side stays-put clause (open-question-2 ruling),
  frozen seven-string vocabulary, three-gate conjunction. Reconciled: top-of-file module
  map ("defaults optional"), layering diagram capabilities tier, sdk-facility taxonomy
  row (+`work` example, defaults OPTIONAL), "default implementation of each facility
  port" → "where a vendor-neutral stdlib implementation honestly exists" + oauth/work
  exceptions, "contract lives with the consumer" → DEFAULT + ratified-platform-protocol
  exception, sdk-vs-logic test → noted as one of three conjunctive gates.
- sdk/README.md: capabilities layering bullet rewritten (behavioral ports + observable
  policy; MAY have stdlib default / integration impl / feature implementation of
  record); `work` row added to the packages table (frozen vocabulary, segregated ports,
  no-default posture, worktest). Foundation pure-mechanism + flat fence retained
  untouched.
- features/README.md §5: "Amended 2026-07-13 (sdk-work-protocol)" block — consumer-
  declared ports restated as DEFAULT, five graduation criteria verbatim, CONJUNCTIVE
  with sdk admission + five-point test (all three gates), work cited as first
  graduation with frozen vocabulary (D4), authorization deferral (fails criterion 2;
  trigger: authorizationv3).
- Verify: `make guard` → all 15 guards green, zero guard edits.

### 2026-07-13 — SWP-2 complete (sdk/capabilities/work + worktest)

- Created `sdk/capabilities/work/work.go` (Status + seven frozen constants,
  `Terminal()`/`Known()`, segregated `Enqueuer`/`Replacer`/`StatusReader`, full D8
  package doc: no-default/oauth precedent, implementation of record = features/jobs,
  failed non-terminal, unknown key → sdk not-found, lifecycle-only, []byte opacity);
  imports `context` only.
- Created `work_test.go`: `TestConstantValues` literal lock (identity_test precedent)
  + `TestTerminal`/`TestKnown` over all seven values and an unknown string.
- Created `worktest/worktest.go`: `Queue`/`ReplaceQueue`, TEST-ONLY `Execution` +
  `Inspector`, `Run` (idempotency-while-active, latest-by-key determinism, totality,
  unknown-key not-found, payload opacity + deep-copy byte preservation) and
  `RunReplace` (fresh distinct ID, supersession, exactly one fresh pending, repeated
  sequences); package doc forbids Inspector promotion; imports stdlib + sdk root +
  work only.
- Executed by the implementer agent; one post-pass lint fix (range-over-int).
- Verify: sdk `go build` / `go test -race -count=1` / `go vet` PASS; `make guard`
  all 15 green, zero guard edits. Deviation from D8: none.

### 2026-07-13 — SWP-3 complete (features/jobs adopts the protocol; primitives.go retired)

- `fenced.go`: three seam assertions now `var _ work.Enqueuer/Replacer/StatusReader =
  (*Service)(nil)`; `EnqueueOnce`/`Replace` take `payload []byte`;
  `LatestStatusByKey` returns `(work.Status, error)`; `DeadLetterFunc` + its
  `workers.FencedDeadLetterFunc[job.Job]` compile seam relocated in from the deleted
  `primitives.go`; Checkpoint doc marked executor-side/out-of-protocol.
- `domain/job/job.go`: `Status` is a type alias of `work.Status`; all seven `StatusX`
  are constant aliases; `Job.Terminal` delegates to `JobStatus.Terminal()`. New
  `domain/job/job_test.go` parity coverage.
- `primitives.go` DELETED; `KeyedEnqueuer`/`KeyStatusReader`/`Checkpointer` gone —
  grep across features/jobs: zero hits. README + `domain/job/fenced.go` comment now
  cite `sdk/capabilities/work`.
- New `memstore/protocol_conformance_test.go` (in-package): test-only inspector
  snapshots the private queue map under its mutex (payloads cloned while locked);
  jobs Service over the memstore fenced queue passes `worktest.Run` AND
  `worktest.RunReplace` under `-race`.
- **Judgment call (flag for owner/9.7):** the conformance suite's payload-isolation
  case exposed that memstore stored the payload slice by reference; fixed with a
  single `bytes.Clone` in `Service.EnqueueOnce`/`Replace` (the protocol boundary, in
  scope) rather than editing memstore — payload isolation now holds for every backing
  store at the work.Enqueuer/Replacer seam.
- `fenced_test.go`/`jobs_test.go` needed zero edits (RawMessage assignable to []byte;
  untyped string comparisons compile against work.Status).
- Verify: jobs `go build` / `go test -race -count=1` / `go vet` PASS; `make guard`
  all 15 green, zero guard edits. Expected mid-flight red: `examples/auth-cms` does
  not compile until SWP-5 (plan-sequencing intent).

### 2026-07-13 — SWP-4 complete (auth adopts the typed Status; mirror dropped)

- `dispatcher.go`: seven `genState*` constants DELETED; imports
  `sdk/capabilities/work`; `normalizeStatus` switches on `work.Status(state)` with the
  identical fold (pending/running/failed → pending; completed → succeeded;
  dead_letter → failed; canceled/superseded → canceled; default/unknown → pending);
  "mirror of jobs" comments now cite the sdk protocol. Four public `Status*`
  constants, `Dispatcher` (string-typed `LatestStatus`), `JobKind`, `Status` struct
  untouched.
- `inprocess.go`: `keyRecord.state` is `work.Status`; all transitions use `work.StatusX`;
  string conversion only at the unchanged `LatestStatus` boundary.
- `command/processor.go`: comment-only — local structural Checkpointer, seam retired,
  executor-side/out-of-protocol.
- Four test files moved to `work.Status*` (string conversion only at string-typed
  fakes); `TestNormalizeStatus` table now sources inputs from `work.Status*` with
  UNCHANGED expected outputs — drift cannot silently reopen.
- Verify (agent + independent re-run): auth `go build` / `go test -race -count=1` /
  `go vet` PASS; `grep -rn genState features/authentication` → zero hits. No exported
  auth symbol added/removed/re-typed. Auth imports sdk only (never features/jobs).

### 2026-07-13 — SWP-5 complete (host bridge + workspace sweep)

- `authjobs.go`: hand-written three-method interface replaced with the protocol
  composition `interface { work.Enqueuer; work.Replacer; work.StatusReader }`;
  `Submit`/`Replace` pass `[]byte` straight through (RawMessage conversions dropped);
  `LatestStatus` returns `string(st)`; `encoding/json` KEPT for the executor-side
  `jobs.FencedClaim.Checkpoint` bridge closure.
- `authjobs_test.go`: fakes moved to the new signatures (`[]byte`, `work.Status`).
- Must-compile set (main.go, main_test.go, jobs_delivery_test.go,
  jobs_delivery_replace_test.go) AND the proof/retry/live tests needed ZERO source
  edits — `json.RawMessage`→`[]byte` assignability + the `job.Status`/`work.Status`
  alias absorbed the signature change. Tagged live harness compiles under
  `livedelivery integration`.
- Verify: auth-cms `go build` / `go test -race -count=1` / `go vet` /
  `go vet -tags="livedelivery integration" ./cmd/server` PASS; `make check` from root
  fully green (all 36 modules, 15 guards, integration-tag vet, templ drift). No
  commits.

### 2026-07-13 — SWP-6 complete (final gate + jobs-mode run-and-look)

- **Gate:** `make check && make guard` from the repo root → exit 0 (all modules
  build/test/vet, integration-tag vet, templ drift check, all 15 guards).
- **Run-and-look (real interaction, DELIVERY_MODE=jobs default):** booted
  `examples/auth-cms` per its README (`AUTH_JWT_SECRET=… AUTH_DEBUG=1 go run
  ./cmd/server`); fenced-delivery worker pool (2 workers) confirmed in the log.
  - Register → verification email delivered through the jobs queue (console
    sender) → verify → login: PASS.
  - `POST /auth/passwordless/start` (JSON, method=code) → 202; sign-in code
    delivered via the queue; `POST /auth/passwordless/verify` → 200 + session:
    PASS — the full producer→queue→worker→transport→redeem lifecycle over the new
    sdk protocol seams.
  - Receipt/status poll: identifier-add (`POST /auth/identifiers/email`, bearer) →
    `{"status":"sent","receipt":…}`; `GET /auth/delivery/status?receipt=…` polled:
    `pending` (×2, transient state observed though not required) → `succeeded`,
    `attempt:0` throughout. Only auth's public states appeared; zero occurrences of
    raw work lifecycle words (`completed`/`running`/`dead_letter`/`superseded`) in
    any response or the server log: PASS.
  - Browser drive (Playwright): login page → "forgot password" link → form submit
    for admin@example.com → PRG to `?sent=1` with the enumeration-safe generic
    confirmation; "Reset your password" email subsequently delivered through the
    jobs queue: PASS.
- **Pre-existing defect found (NOT this milestone — flag for AV3-9.7/owner):** the
  bundled HTML passwordless-start page posts `identifier`+`method` but no `kind`
  field, while `passwordlessStartForm` (forms.go:288) requires `form.Get("kind")` —
  every browser passwordless start 400s with the generic failure copy. The JSON arm
  (which takes `identifier_kind`) works. Left unfixed here (out of SWP scope; no SWP
  task touched forms/views).
  **FIXED same session, post-milestone (owner-directed, 2026-07-13):** root cause
  was the handlers never populating `PasswordlessStartPage.Kinds` (the template
  already rendered a kind select for >1 kinds / hidden input for exactly 1). Fix:
  `authsvc.Service.PasswordlessKinds()` (sorted, deterministic) + interface method
  in `sessions.go` + `Kinds:` at both render sites (`html.go` GET page, `forms.go`
  failure re-render). No template change, no exported-symbol change beyond the
  internal handlers' consumer-declared interface. Verified live in the browser via
  Playwright: start form (kind select renders email/phone) → PRG to
  `/auth/passwordless/check?kind=email` → queue-delivered sign-in code → OTP entry
  page → session cookies (`session`, `auth_csrf`) set, landed on `/`. Module tests
  + views/templ module green; full `make check && make guard` re-run green.
- Tree left uncommitted for the AV3-9.7 wave, per plan. **Milestone complete: all
  Definition-of-Done items green.**
