# Auth v3 delivery-runtime follow-up

Status: **DRAFT — architecture recommended; owner ratification required before execution.**
Working name: `authv3-delivery-refactor`; task prefix: `AV3D`.
Insertion point: after `AV3-9.6` and before the existing `AV3-9.7` reviewer wave.

## Outcome

Remove authentication's private durable job domain and make outbound delivery use
one of two explicit host-owned execution modes:

1. `jobs`: encrypted authentication delivery commands run on the generic jobs
   feature, with durable retry, replacement, status, stale-claim fencing, and
   terminal cleanup; or
2. `in_process`: the same delivery processor runs behind a bounded queue and
   fixed worker pool, with finite retry/status retention and explicitly ephemeral
   crash guarantees.

The refactor must preserve the auth-v3 security properties already implemented:

- forgot-password and passwordless starts perform no account lookup, challenge
  issuance, rendering, or provider call on the request path;
- persisted durable payloads are encrypted and contain no plaintext destination
  or secret;
- a retry resends the exact same rendered secret rather than minting a new one;
- duplicate starts are idempotent and an explicit resend supersedes older active
  work under the same PII-free logical key;
- provider calls, attempts, retry delay, queue size, shutdown, and retained status
  are bounded;
- terminally undeliverable challenges are discarded best-effort;
- status reveals only lifecycle data to the existing session-gated receipt flow;
  and
- production wiring fails closed instead of silently selecting an ephemeral mode
  or accepting a jobs mode whose runtime is never started.

## Decision

### Jobs execute work; events observe it

Authentication submits delivery work directly to generic jobs in durable mode.
The sdk events bus is not a queue: its asynchronous mode may drop events and its
synchronous mode is only as durable as its handlers. Putting an event in front of
the job enqueue adds a second failure boundary without improving durability.

An optional, narrow delivery observer may publish secret-free accepted,
initialized, skipped, delivered, retried, dead-lettered, superseded, and purged
events in either mode. Those events are operational/domain effects only. Their
failure never changes the already-recorded job state and they are never required
to make delivery happen.

A future auth-domain transactional outbox remains valid for state changes that
must commit with an event. It is separate from this transport refactor and must
not be simulated with emit-after-commit.

### No automatic production fallback

`DeliveryMode` is explicit. Construction never selects a mode merely because a
collaborator happens to be non-nil:

| mode | accepted work survives restart | cross-instance coordination | retry/status | required host lifecycle |
|---|---|---|---|---|
| `jobs` | yes, when backed by a durable jobs store | yes | durable and retained by jobs | host runs generic jobs runtime |
| `in_process` | no | no | process-local and retention-bounded | host runs auth delivery runtime |
| `off` | n/a | n/a | none | allowed only when no configured auth capability can send |

Development may deliberately select `in_process`. Production requires an
explicit ephemeral-delivery acknowledgment for that mode. The recommended
production posture is `jobs`.

### Why generic jobs must be hardened first

The current jobs queue is close, but it cannot safely replace `DeliveryJobs` as
written:

1. `Complete` and `Fail` are not fenced by the worker that owns the claim, so a
   stale worker can mutate a job after another worker reclaims it.
2. Job ID is the only idempotency key. Authentication needs a stable logical key
   separate from unique execution IDs, plus atomic enqueue-once and replace.
3. There is no atomic claimed-payload checkpoint. Opaque auth work must persist
   its newly rendered encrypted secret before the provider call so every retry
   sends the same secret.
4. Failure requeues immediately. Auth delivery needs bounded backoff, permanent
   failure, and a post-dead-letter cleanup hook.
5. There is no latest-by-logical-key status, cancellation/supersession state, or
   bounded terminal purge.

These become general jobs capabilities with shared memory/pgx/turso conformance.
Authentication must not recreate them in a renamed feature-local repository.

## Delivery processing model

Every delivery uses one versioned, encrypted envelope and one reusable processor:

```text
request/service
    -> dispatcher.Submit or dispatcher.Replace
        -> jobs queue (durable) OR bounded queue (ephemeral)
            -> processor opens envelope
                -> opaque? resolve + issue + render
                -> checkpoint rendered encrypted envelope
                -> bounded provider send
                -> complete / retry / dead-letter + discard
```

For opaque enumeration-safe starts, admission stores only normalized resolution
input inside the encrypted envelope. Initialization runs after admission. The
rendered checkpoint is claim-fenced and durable before send in jobs mode. A crash
after provider acceptance but before completion may resend the same message; this
is intentionally at-least-once, not exactly-once. Redemption remains single-use.

Replacement prevents future processing of superseded work and fences a stale
worker's checkpoint/completion. It cannot retract a provider call already in
flight. The freshly issued challenge invalidates/replaces the old proof where the
auth flow supports replacement; documentation and tests must state this race
honestly.

## Public-boundary direction

- Remove `Repositories.DeliveryJobs` and the public
  `domain/deliveryjob.Repository`.
- Add explicit delivery configuration and narrow stdlib-typed queue/processor
  seams; authentication still imports no sibling feature.
- The generic jobs service exposes the primitive methods necessary for a
  consuming feature to match structurally without importing jobs domain types.
- A composition adapter may import both features, but neither feature core
  imports the other.
- Replace `RunDeliveryWorker` with a host-owned runtime only for `in_process`.
  In `jobs` mode the host runs `jobs.Runtime`; auth exposes the registered job
  kind/handler seam.
- Preserve `DeliveryStatus` at the auth boundary, mapping generic job lifecycle
  to the existing secret-free projection.

Exact names are frozen in phase 0 after checking repository compatibility; these
points are architectural constraints, not a mandate for one premature Go shape.

## Phase queue

| Phase | File | Depends on | Gate produced |
|---|---|---|---|
| 0 | `01-contracts-and-jobs.md` | auth v3 through AV3-9.6 | mode, delivery, and generic-jobs contracts frozen |
| 1 | `01-contracts-and-jobs.md` | phase 0 | generic jobs has fenced/keyed/checkpointed retry primitives |
| 2 | `02-auth-processor.md` | phases 0-1 contracts | one transport-neutral auth delivery processor |
| 3 | `03-durable-jobs-mode.md` | phases 1-2 | durable generic-jobs mode preserves all auth semantics |
| 4 | `04-bounded-mode.md` | phase 2 | bounded in-process mode with honest weaker guarantees |
| 5 | `05-migration-and-closeout.md` | phases 3-4 | bespoke queue removed, hosts/docs/proof migrated |

Phases 3 and 4 may be implemented in either order after phase 2, but use one
implementer at a time to avoid colliding on the shared processor and construction
matrix.

## Execution protocol

- Execute one `AV3D-x.y` task at a time and append its evidence to the owning
  phase file.
- Preserve the current dirty auth-v3 worktree. Do not reset, squash, or rewrite
  unrelated changes.
- Behavioral characterization lands before deleting the bespoke implementation.
- Generic jobs contracts and conformance land before the auth adapter consumes
  them.
- Both jobs and authentication migration trees maintain pgx/turso filename
  parity.
- Run narrow module gates plus `make guard` per code task; run `make check` and
  `make guard` at phase gates.
- Live pgx/turso, restart, stale-claim, resend, and real-provider checks cannot be
  closed by hermetic tests alone.
- After two failed repairs for the same root cause, stop instead of weakening a
  guarantee.

## Standing invariants

- No request-path provider call or account lookup for unauthenticated starts.
- No goroutine per request, unbounded channel, unbounded retry loop, or unbounded
  status map.
- No plaintext secret/destination in durable generic job columns, logs, events,
  errors, metrics labels, or status responses.
- A checkpoint, completion, failure, or retry from a stale/superseded claim fails
  with conflict and cannot clobber the current execution.
- Backoff is context-cancellable and provider timeout sits safely inside the
  claim lease.
- Events are never the only record that accepted delivery work.
- `Register` starts no goroutines. Hosts explicitly run the selected runtime.
- `jobs` production mode validates durable store and runtime acknowledgment;
  `in_process` production mode validates explicit crash-loss acknowledgment.
- The authentication core, jobs core, and sdk preserve their module boundaries.

## Preflight and insertion into auth-v3 closeout

Before `AV3D-0.1`:

1. Confirm `AV3-9.1` through `AV3-9.6` remain green and `AV3-9.7/9.8` are open.
2. Record `git status --short` and preserve all current changes.
3. Run jobs, authentication, sdk/workers, `make check`, and `make guard` baselines.
4. Record live pgx/turso availability for both jobs and authentication.
5. Ratify the two-mode decision and the production acknowledgment posture.

The existing `AV3-9.7` reviewer wave is intentionally delayed until phase 5 is
complete. That reviewer audits auth v3 plus this refactor as one final system;
`AV3-9.8` applies accepted findings and performs the PR-ready reverification.
Authorization v3 execution remains paused until that combined closeout, so its
effects work can consume the settled jobs/events pattern.

## Global stop conditions

Stop for owner direction if implementation would require:

- synchronous lookup or provider delivery for forgot-password/passwordless;
- treating best-effort event emission as accepted durable work;
- plaintext durable auth payloads;
- weakening single-use challenge redemption;
- silently allowing ephemeral production delivery;
- maintaining a second auth-specific durable queue after cutover; or
- a jobs API change that forces unrelated consumer features to import auth.

## Execution log

Append only. Phase files own task-level entries; this overview receives the final
handoff entry before `AV3-9.7` begins.

### 2026-07-13 — MILESTONE EVIDENCE / AV3-9.7 HANDOFF (AV3D-5.5)

The final implementation-complete gate. Every AV3D phase claim (0.1–5.4) was
re-certified from a fresh run of the full gate (`-count=1` where caching could
mask), the adversarial searches were re-run against the clean tree, the three
stale closeout comments the AV3D-5.4 log flagged were retired (comment-only), and
the milestone is handed to the existing `AV3-9.7` reviewer wave. No contract was
weakened; no live database was available (the standing open owner gate).

**FULL GATE (fresh, command by command):**

| # | Command | Result |
|---|---|---|
| 1 | `cd sdk && go test -race -count=1 ./foundation/workers/...` | PASS (2.5s) |
| 2 | `cd features/jobs && go build ./... && go test -race -count=1 ./... && go vet ./...` | PASS (storetest 11.4s incl. real ~3.1s lease-expiry sleeps) |
| 3 | `cd features/authentication && go build ./... && go test -race -count=1 ./... && go vet ./...` | PASS (25 pkgs ok, incl. the two touched: authsvc, invitationsvc) |
| 4 | `cd features/authentication/stores/pgx && go test -count=1 ./...` | PASS (live `TestConformance_Postgres` **SKIP** — "POSTGRES_TEST_DSN not set — postgres conformance NOT verified") |
| 5 | `cd features/authentication/stores/turso && go test -count=1 ./... && go vet -tags=integration ./...` | PASS (live `TestConformance_Turso` **SKIP** — "TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified"); integration vet clean |
| 6 | `cd features/jobs/stores/pgx && go test -count=1 ./...` | PASS (live `TestConformance_{Queue,FencedQueue,Schedules}` **SKIP** — "POSTGRES_TEST_DSN not set — postgres conformance NOT verified") |
| 7 | `cd features/jobs/stores/turso && go test -count=1 ./... && go vet -tags=integration ./...` | PASS (live `TestConformance_{Queue,FencedQueue,Schedules}` **SKIP** — "TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified"); integration vet clean |
| 8 | `cd examples/auth-cms && go build ./... && go test -race -count=1 ./...` | PASS (cmd/server 29.9s + 6 internal pkgs) |
| 9 | `cd examples/jobs-minimal && go test -count=1 ./...` | PASS (cmd/server: no test files; compiles) |
| 10 | livedelivery pgx: `go test -tags=livedelivery -count=1 -run TestLiveJobsDeliveryPGX ./cmd/server` | **LOUD SKIP** verbatim: "POSTGRES_TEST_DSN not set — LIVE postgres jobs-mode delivery proof NOT verified (KnownUnknownOpaqueAdmissionParity, ProviderTimeoutAndRetryOffRequestPath, RestartAfterOpaqueAdmission, RestartAfterCheckpointResendsSameSecret, RestartAfterProviderAcceptanceResendsSameSecret, ResendConvergesToLatestGeneration, StatusAndEventsContainNoSecrets, TerminalCleanupAndPurge)" |
| 11 | livedelivery turso: `go test -tags='livedelivery integration' -count=1 -run TestLiveJobsDeliveryTurso ./cmd/server` | **LOUD SKIP** verbatim: "TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — LIVE turso jobs-mode delivery proof NOT verified (…same eight proofs…)" |
| 12 | `go vet -tags='livedelivery integration' ./cmd/server` | PASS (both dialect live harnesses COMPILE) |
| 13 | `make guard` | PASS (fifteen guards, incl. G14 auth-no-delivery-repo + G15 auth-no-request-time-provider) |
| 14 | `make check` | PASS ("all checks passed": templ no-drift + warm-scaffold-cache + per-module vet/build/test + integration-tag turso vet + all guards) |

**PROTOCOL COVERAGE (which auth-cms test carries each — all PASS under `-race -count=1`):**

- **restart:** `TestRestartAfterOpaqueAdmissionInitializesSafely`,
  `TestRestartAfterCheckpointResendsSameSecret`,
  `TestRestartAfterProviderAcceptanceResendsSameSecret`.
- **stale-claim / supersession fencing:** `TestAdversarialReplaceWhile{Pending,Initializing,Checkpointed,Sending}`
  (a superseded worker cannot checkpoint/complete/fail; fresh generation proceeds);
  store-level live fence proven by jobs `RunFencedQueue` (ReplaceFencesRunningClaim /
  ClaimFencingOnStaleLease — the live leg of which is the open owner gate).
- **resend:** `TestReplaceCreatesFreshGenerationStatusSelectsLatest`,
  `TestSubmitOnceCoalescesOntoOneActiveExecution`, the `RestartAfter*ResendsSameSecret`
  pair, `TestInProcessHostRetryReusesSecretOverPool`.
- **saturation:** `TestDeliveryHealthBacklogUnderSaturation`,
  `TestInProcessHostSaturationReturns503OverHTTP` (honest 503 over real HTTP, never a
  202-after-drop).
- **shutdown:** `TestInProcessHostShutdownDrainAndNoLeak`, `TestDeliveryRuntimeStartStop`.
- **real-provider / HTTP drive:** `TestJobsModeDeliveryEndToEnd` (jobs-mode fenced
  runtime end-to-end), `TestInProcessHostDeliversRegistrationOverPool`,
  `TestInProcessHostForgotPasswordAdmitsOverHTTP` (register/verify/forgot/passwordless
  driven over httptest HTTP with console/recording-mailer observation, delivery OFF the
  request path). The AV3D-3.5 log additionally records a manual `go run ./cmd/server`
  drive over live HTTP in jobs mode (register→verify→forgot→passwordless, console
  mailer, enumeration-safe pre-verification skip); this gate re-certifies the equivalent
  via the httptest harnesses above.

**MIGRATION PARITY (both features, both trees — `diff` output empty):**

- AUTH: `diff <(ls features/authentication/stores/pgx/migrations) <(ls .../turso/migrations)`
  → IDENTICAL — `0001_users … 0013_authentication_grants` (thirteen files; NO delivery table).
- JOBS: `diff <(ls features/jobs/stores/pgx/migrations) <(ls .../turso/migrations)`
  → IDENTICAL — `0001_job_queue, 0002_job_schedules, 0003_fenced_job_queue`.

**UPGRADE REHEARSAL (dry — no live DB; RELEASING.md "Auth delivery-runtime upgrade
runbook" walked against the actual schema/table names):**

- Option B `INSERT INTO fenced_job_queue (job_id, kind, payload, status, logical_key,
  scheduled_for, created_at, updated_at) SELECT …` — every named column EXISTS in the
  canonical `0003_fenced_job_queue.sql` (both dialects); every omitted column is nullable
  or DEFAULTed (priority/retry_count/max_attempts/payload have defaults; lease_id,
  leased_until, worker_name, failure_reason, claimed_at, completed_at, terminal_at are
  nullable) so the INSERT is valid. The `NOT EXISTS … status IN ('pending','running')`
  active-key guard matches the `uq_fenced_job_queue_active_key` partial unique index.
  pgx `gen_random_uuid()::text` / turso `lower(hex(randomblob(16)))` mint fresh execution
  ids. Source columns `dj.{kind,payload,idempotency_key,state}` match the historical
  bespoke `delivery_jobs` DDL (RELEASING.md §v2→v3, retained-as-history).
- Step 4 verify `SELECT count(*) … FROM delivery_jobs WHERE state = 'pending'` and Step 5
  `DROP TABLE IF EXISTS delivery_jobs` are consistent with that historical DDL.
- Every symbol the runbook names exists in code: `Config.DeliveryMode`/`DeliveryDispatcher`/
  `DeliveryEncrypter`/`DeliveryJobsAcknowledged`/`ErrDeliveryJobsUnacknowledged`/
  `DeliveryEphemeralAcknowledged`/`ErrDeliveryEphemeralUnacknowledged`,
  `Service.RunDelivery`/`DeliveryJobRuntime`, `jobs.Runtime`/`jobs.FencedRuntime`,
  `jobsstore.ExportMigrations` (both dialects). Minor prose imprecision (NOT a blocker):
  Step 6 jobs-mode says "`jobs.Runtime`" generically; the fenced delivery surface the host
  actually wires is `jobs.FencedRuntime` — the runbook's own §Step-3/fenced references are
  correct; flagged for the reviewer as a wording tidy. No live DB available to execute the
  runbook (recorded).

**ADVERSARIAL-SEARCH INVENTORY (commands + results, all CLEAN):**

1. *Plaintext secrets in durable paths.* jobs pgx/turso `fenced.go` store `payload` as
   raw opaque `[]byte`/`json.RawMessage` into a BYTEA/BLOB column via `payloadBytes()`;
   no `secret`/`destination`/`body`/`code` field name exists anywhere under
   `features/jobs/stores/`. `delivery.Service.Enqueue`/`Replace` call `s.seal(cmd)`
   (AES-GCM `deliverycmd.Seal`) BEFORE `dispatcher.Submit(…payload)`, so only sealed
   ciphertext crosses the stdlib-typed `[]byte` dispatcher seam. → no plaintext secret
   in any durable column.
2. *Direct provider sends outside the delivery package.* G15 coarse grep
   (`\.(Deliver|Send|Notify)\(` in `features/authentication/internal/logic`, minus
   `/delivery/`, minus tests) → ZERO hits. AST companion
   `TestNoProducerBypassesDispatcherSeam` present and green. The three legitimate send
   sites are all INSIDE `delivery/` (`command/engine.go`, `jobsprocessor.go:238`
   `router.Deliver` = the off-request job handler, `router.go`).
3. *Bespoke auth job persistence.* G14 both tripwires clean: `grep delivery_jobs
   features/authentication` → NONE; `grep -E 'domain/deliveryjob|package deliveryjob'
   features/authentication examples/auth-cms` → NONE. No `CREATE TABLE … (deliver|outbox|
   queue)` in any auth migration → auth ships no delivery/outbox/queue table.
4. *Unbounded goroutines.* Every production `go` statement is pool-scoped or
   lifecycle-bounded: `sdk/foundation/workers/pool.go:176` (fixed pool worker loop),
   `delivery/inprocess.go:802` (`for i<r.workers` fixed pool, WaitGroup-tracked,
   ctx-derived cancel), `inprocess.go:990` (single `waitBounded` shutdown helper per
   RunDelivery lifecycle), `jobs/internal/logic/runtime/runtime.go:95` (one scheduler
   goroutine, WaitGroup-joined, ctx-bounded). `storetest` `go func` hits are bounded
   test-harness fan-out (fixed loop counts), not runtime paths.
5. *Unbounded channels/maps.* `delivery/inprocess.go:306 make(chan inProcessItem,
   capacity)` = the FINITE bounded queue; jobs `queuesvc:40 make(chan struct{}, 1)` and
   sdk `pool.go:145 make(chan error, workerCount)` are bounded; remaining `make(chan
   struct{})` are unbuffered lifecycle signals. The in-process `keys
   map[string]*keyRecord` retention map has FINITE max-entry eviction (oldest terminal
   entry) + terminal TTL eviction, validated at construction
   (`ErrInProcessStatusRetentionTooSmall`, maxEntries ≥ capacity) — proven never to grow
   (AV3D-4.2: 200 keys → ≤8).
6. *Event-driven dispatch.* The ONLY `Emit` in auth delivery paths is
   `delivery/observer.go:80` inside `EventObserver.Observe` — best-effort (WARN-on-error,
   contained by `command.SafeObserve`), strictly observer-side, AFTER the transport has
   recorded state. No enqueue/checkpoint/dispatch path emits an event; no event emission
   is load-bearing for delivery.

**CLOSEOUT DEBRIS FIXED (comment-only, per AV3D-5.4's three flagged stale comments):**

- `authsvc/service.go:257` and `invitationsvc/service.go:202` — the `Queue` field docs no
  longer reference the removed `DeliveryJobs`/"durable delivery outbox"; they now describe
  the delivery-dispatch seam wired from `Config.DeliveryDispatcher` / the in_process queue.
- `jobs/domain/job/fenced.go:20` — dropped the reference to authentication's removed
  private `deliveryjob.Repository`; now states authentication runs its encrypted delivery
  work on this generic-jobs surface in jobs mode.
- Re-ran the two touched modules after the edits: authentication and jobs
  `go build ./... && go test -race -count=1 ./... && go vet ./...` — PASS (rows 2–3 above).

**OPEN OWNER GATES (swept across all phase logs — carried into `AV3-9.7`):**

1. **Live pgx/turso fenced/queue conformance** (AV3D-1.5) — `POSTGRES_TEST_DSN` +
   `TURSO_*` unset; both dialect suites loud-SKIP; stores compile/vet under
   `-tags=integration`. Close with `make test-stores` (env set).
2. **Live pgx/turso auth store conformance** — same env gate; loud-SKIP.
3. **Live jobs-mode delivery run-and-look harness** (AV3D-3.5) — the eight-proof
   `livedelivery` pgx/turso harnesses loud-SKIP (env unset) and COMPILE; close with the
   `go test -tags=livedelivery[ integration]` invocations recorded in the AV3D-3.5 log.
4. **auth-cms `go.mod` tidy decision** (AV3D-3.5) — `make tidy` was deliberately NOT run:
   the tagged `livedelivery` harness is workspace-resolved so the default in-memory host
   build stays driver-free; a tidy would resolve all build tags and pull the pgx/libsql
   driver graph into the in-memory host's go.mod. **YOUR CALL:** keep the harness
   workspace-resolved (recommended) or relocate the live harness to a module that legitimately
   requires the drivers.
5. **`DeliveryStatus.Attempt` now reads 0** (AV3D-2.3) — the transport-neutral status seam
   is lifecycle-only, so the JSON `attempt` field no longer carries the executor's retry
   count. One observable behavior change; for reviewer sign-off.
6. **Unused `authsvc.Service.mailer` field** (AV3D-2.4) — `email.Sender` stored in
   `NewService` (service.go:371/489) but read on no code path after the outbox refactor
   (only `invitationsvc` reads its mailer for the capability check). Dead field; reviewer
   cleanup.
7. **In-process backpressure error kind** (AV3D-4.1) — `ErrDeliveryCapacity`/
   `ErrDeliveryClosed` wrap `sdk.ErrConflict` because the kernel exposes no dedicated
   unavailable/backpressure kind; reviewer to adopt a dedicated kind if the kernel grows one.
8. **Adjacent stale "durable worker (phase 4)" comments** (found this task, OUT of the
   named-three scope, not fixed) — the `Deliver *delivery.Router` field docs at
   `authsvc/service.go:252` and `invitationsvc/service.go:197` still say "the durable
   worker (phase 4) consumes it"; the durable worker is removed. Trivial comment tidy for
   the reviewer wave; left untouched to keep this task's diff to the three named comments.

**FORMAL STATEMENT:** AV3D-0.1 through AV3D-5.5 are COMPLETE. The bespoke authentication
durable delivery queue is fully removed; durable delivery runs on the generic hardened
**jobs** fenced queue via a host-wired dispatcher, and `in_process` is the bounded
ephemeral pool — both modes carry their claimed evidence. Hosts, docs, and migrations are
current; every guard and the full `make check` are green; the adversarial searches are
clean; the only unclosed items are the live-store/real-DB gates (env unset) and the
reviewer-cleanup flags above. **The existing `AV3-9.7` reviewer wave is UNBLOCKED** — it
audits auth-v3 plus this refactor as one system; `AV3-9.8` owns accepted remediation and
the PR-ready reverification.
