# Phase 4 — durable outbound delivery

Status: READY after phase 2; phase 3 may complete first but is not required for
AV3-4.1/4.2.
Depends on: phases 0–2. AV3-4.3 additionally depends on phase 3.
Design: §§4.1 enumeration contract, 6, 8 V14–V15.

## Goal

Unify all authentication/invitation outbound content and move
unauthenticated starts onto a durable worker path so account resolution and
provider latency cannot leak identifier existence.

## Task AV3-4.1 — shared delivery renderer/router

Touch: new `features/authentication/internal/logic/delivery`, authsvc and
invitationsvc dependency construction, sdk template usage, tests.

Implement:

- one constructor-injected kind router consumed by authsvc and invitationsvc;
- email through `email.TemplateRegistry`/Emailer and email-kind bridge policy;
- body-only SMS templates; no HTML email layout in SMS;
- core templates for registration, reset, pending OAuth link, magic link,
  sensitive codes, identifier-change proof/notices, invitation, and member
  added;
- `LayerCore` defaults with `LayerApp` override support;
- notifier context deadline/cancellation contract and stable kind-tagged errors;
- no global registry or service-to-service imports.

At this task, renderer/router returns an encrypted-job-ready message envelope;
it does not synchronously send from request handlers.

Verify:

```sh
cd features/authentication && go test ./internal/logic/delivery/... ./internal/logic/authsvc/... ./internal/logic/invitationsvc/...
make guard
```

## Task AV3-4.2 — delivery queue service and worker

Depends on: AV3-4.1, AV3-0.6, phase-2 stores.
Touch: delivery service/worker, feature public driving methods as needed,
composition wiring, tests.

Implement:

- enqueue with encrypted payload, keyed idempotency/replacement, and no plaintext
  destination/message columns;
- worker claim loop callable by a host-owned process/lifecycle hook without
  importing the jobs feature;
- first-attempt account resolution and challenge issue/render as one logical job
  initialization; persist the encrypted rendered payload so retries resend the
  same valid secret;
- bounded provider timeout, retry classification/backoff, lease recovery,
  terminal failure, challenge cancellation on terminal failure, success, and
  purge;
- host-visible health/metrics/log fields containing job IDs/kinds only, never
  destination or secret by default;
- graceful shutdown and no goroutine leak when a notifier honors cancellation;
- unit/race tests for contention, crash-after-send replay, retry, replacement,
  terminal cleanup, encryption failure, provider timeout, and cancellation.

Document at-least-once delivery: a provider may receive a duplicate message,
but redemption remains single-use.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/delivery/... ./storetest
make check
```

## Task AV3-4.3 — migrate all existing outbound sites

Depends on: AV3-4.2 and phase 3.
Touch: auth registration/forgot/reset/OAuth services, invitations service,
handlers/tests.

Delete direct message construction and direct request-time sends. Migrate:

- registration verification;
- forgot/reset and reset-complete notice;
- pending OAuth link;
- invitations/member-added; and
- any auth-v2 security notice currently sent directly.

Unauthenticated start paths normalize/rate-limit/enqueue an opaque command and
return an indistinguishable accepted response without resolving the account.
Known and unknown tests must traverse the same repository/enqueue calls and
have bounded comparable handler timing under a blocking fake provider.

Session-gated starts return a job receipt and expose a live-session-gated status
method/handler; provider failure is observable there without blocking the start
request. Ensure a failed enqueue rolls back/cancels any pending flow state
created in the request.

Verify:

```sh
cd features/authentication && go test ./internal/logic/authsvc/... ./internal/logic/invitationsvc/... ./internal/inbound/authentication/...
rg -n 'sendVerificationEmail|sendResetEmail|sendPendingLinkEmail|email\.Message\{' features/authentication
make check
```

The grep must have no obsolete direct-send sites.

## Task AV3-4.4 — production transport and worker wiring validation

Depends on: AV3-4.2.
Touch: feature constructor/config validation and proof-host composition tests
only; full proof-host runtime is phase 8.

Test that production mode rejects:

- console email sender or console notifier;
- transport lacking security metadata;
- missing delivery encrypter;
- non-durable/in-process-only delivery repository where metadata can identify
  it; and
- absent worker/lifecycle acknowledgment while an outbound-enabled subsystem is
  configured.

Development mode permits console with one startup warning and still requires
encrypted job payloads. Custom production test transports explicitly declare
metadata.

## Task AV3-4.5 — worker real-interaction check

Depends on: AV3-4.3, AV3-4.4.

Run a disposable host/worker against each fresh dialect store with console
delivery in development. Observe enqueue → claim → console delivery → success,
retry after injected failure, replacement, terminal failure cleanup, lease
recovery, and purge. Inspect logs for absence of pepper, OTP/token outside the
explicit development console message, and plaintext destination in worker
diagnostic logs. Record transcripts with secrets redacted.

## Phase acceptance

```sh
make check
make guard
```

Plus worker real-interaction evidence. A blocking provider does not delay an
unauthenticated start response.

## Stop conditions

- The only available worker integration requires importing another feature:
  stop and expose a host-driven lifecycle method instead.
- Encryption would prevent idempotent retry without storing plaintext: stop and
  correct the envelope design before migrating handlers.
- Provider cancellation cannot be honored: mark that adapter unsupported for
  production rather than spawning an unbounded goroutine.

## Execution log

Append dated entries per completed task.

### 2026-07-12 — AV3-4.1 shared delivery renderer/router

Dependencies verified complete: phases 0–3 all checked off in TASKS.md and
gate-green; phase 4 depends on phases 0 and 2 per the overview (both closed).
Preserved the pre-existing worktree changes; touched only this task's files.

Implemented the constructor-injected kind renderer/router in
`features/authentication/internal/logic/delivery`:

- `router.go` — `Router` with `NewRouter(Deps)`, `Render(ctx, Request) (Envelope,
  error)`, and `Deliver(ctx, kind, Envelope) error`. Nine delivery-purpose
  constants (registration verification, password reset, pending OAuth link, magic
  link, sensitive code, identifier-change proof, identifier-change notice,
  invitation, member-added). Email renders through `email.Emailer`/`TemplateRegistry`
  (LayerCore defaults + `Deps.AppTemplates` LayerApp overrides) into HTML+text;
  non-email kinds render body-only SMS templates (short in-core text/template, no
  HTML layout). `Deliver` owns the email-kind bridge policy (wired email-kind
  notifier wins, else the Mailer) and deny-by-absence for other kinds; it honors
  ctx cancellation and returns stable kind-tagged errors (`ErrMailerRequired`,
  `ErrUnknownPurpose`, `ErrKindUnsupported` — each wrapping `sdk.ErrInvalidInput`
  — plus `DeliveryError{Kind, Err}` tagging transport failures while unwrapping to
  the cause). No global registry; no service-to-service import (delivery imports
  sdk ports + the feature's own `domain/deliveryjob` only). Render returns the
  encrypted-job-ready `Envelope`; it never sends from request handlers.
- `templates/*.html` — nine LayerCore email content templates (embedded).
- `envelope.go` — added an `HTML` field to `Envelope` so the email rail persists
  both HTML and text for idempotent retry resend (SMS leaves it empty). The
  AV3-0.6 round-trip/opaque/nil tests still pass (`HTML` defaults empty).
- `testoverride/` — a test-support subpackage embedding one LayerApp override
  template (a second `templates/` dir cannot live in the delivery package itself).
- `router_test.go` — render (email/SMS), subject interpolation, LayerApp override,
  unsupported/unknown purpose, deliver via Mailer, email-kind bridge preference,
  SMS-via-notifier, deny-by-absence, kind-tagged transport error, and two
  cancellation-contract cases.

Dependency construction wired the router into both services without migrating any
send site (that is AV3-4.3): `authsvc.Deps.Deliver`/`Service.deliver`,
`invitationsvc.Deps.Deliver`/`Service.deliverer` (field renamed to avoid the
existing `deliver` method), and `authentication.go` builds one
`delivery.NewRouter` after transport validation and injects it into both Deps.
The public `Config` surface is unchanged.

Verification (exact task commands):

- `cd features/authentication && go test ./internal/logic/delivery/...
  ./internal/logic/authsvc/... ./internal/logic/invitationsvc/...` → **ok** (all
  three packages pass; `testoverride` has no tests).
- `make guard` → **exit 0** (all thirteen guards pass).

Additional sanity (not required by the task): whole-feature `go build ./... && go
test ./...` → pass; `go vet ./...` → exit 0; `examples/auth-cms` `go build ./...`
→ exit 0.

Premise adaptations: none. One design note for the next task — the `Envelope`
gained an `HTML` field (design §6.2's HTML+text email rail needs it persisted);
`Seal`/`Open` are unchanged (whole-envelope JSON). No product/architecture
premise conflicts encountered.

### 2026-07-12 — AV3-4.2 delivery queue service and worker

Dependencies verified: phases 0–3 all checked off and gate-green; AV3-4.1
complete and checked off (the renderer/router is injected as
`authsvc.Service.deliver` / `invitationsvc.Service.deliverer`); AV3-0.6
`deliveryjob.Repository` frozen and live-proven on both dialects; phase-2 stores
present. Preserved all pre-existing worktree changes; touched only this task's
files.

Implemented the durable delivery queue service and at-least-once worker in
`features/authentication/internal/logic/delivery` (imports sdk ports +
`domain/deliveryjob` only — no service-to-service import, no registry):

- `service.go` — `Service` (`NewService`, `Enqueue`, `Replace`) seals a `Command`
  envelope through the required `DeliveryEncrypter` and enqueues an at-least-once
  outbox job keyed by the caller-supplied PII-free `IdempotencyKey`; idempotent
  enqueue and superseding `Replace` ride the frozen repository. `Receipt` returns
  the stable idempotency key (not the job ID) as the session-gated status handle.
  Loud construction (`ErrRepositoryRequired`, `ErrEncrypterRequired`) and command
  validation (`ErrCommandIncomplete`), each wrapping `sdk.ErrInvalidInput`. No
  account resolution, no provider call — the enqueue path is bounded and identical
  for known/unknown identifiers.
- `worker.go` — `Worker` (`NewWorker`, `Run`, `RunOnce`, `Purge`). `Run(ctx)` is
  the host-owned lifecycle hook: drain-then-poll claim loop with a purge ticker,
  graceful return on `ctx` cancel, no jobs-feature import. Per job: claim (leased,
  single-claimant) → `Open` payload → an OPAQUE start job (empty rendered body) is
  resolved+rendered ONCE through the injected `Initializer` and its encrypted
  rendered payload is persisted via `Replace` under the same key (the next claim
  delivers); a rendered job is sent through the router under a bounded
  `ProviderTimeout`. Success → `Succeed`; transient failure → `Retry` with capped
  exponential backoff; attempt-cap → terminal `Fail` + `Initializer.Discard`
  (challenge cancellation); "nothing to deliver" (unknown identifier) → silent
  `Succeed` with no send; undecryptable payload → terminal `Fail`; parent-context
  cancel mid-send → left leased for reclaim (no burned attempt, no goroutine
  leak). `last_error` and all logs/`Observer` events carry only job ID / kind /
  purpose / outcome / attempt — never a destination or secret (the transport
  cause is reduced to its `DeliveryError.Kind`). At-least-once delivery is
  documented on the `Worker` type: a provider may receive a duplicate, but the
  persisted secret is identical on replay and redemption stays single-use because
  the challenge repository consumes atomically.
- `service_test.go` / `worker_test.go` — a concurrent-safe in-test
  `deliveryjob.Repository` (+ a `flakyRepo` that fails `Succeed` once to model a
  crash-after-send), fakes for `Initializer`/`Observer`/notifier/encrypter, and a
  manual clock. Coverage: construction guards, seal-opacity (real AES-GCM proves
  ciphertext-only payload), idempotent enqueue, command validation, enqueue-time
  encryption failure, replacement, opaque init→persist→deliver (single init,
  same secret), skip-unknown, deny-by-absence (no initializer), retry-then-succeed
  with unchanged secret, terminal-failure challenge cancellation with
  secret/destination-free `last_error`, crash-after-send replay of the identical
  secret + lease recovery, provider-timeout bounding, graceful shutdown / no leak,
  50-job × 8-worker contention (each delivered exactly once, no double-succeed),
  and retention-respecting purge.

Composition wiring: added the enable-time `ErrDeliveryEncrypterRequired`
validation in `authentication.go` (`repos.DeliveryJobs != nil && cfg.DeliveryEncrypter
== nil` → loud error), completing the handoff item. The error var already existed
in `security.go`. Full worker construction + `Initializer` host wiring is deferred
to AV3-4.3 (it needs the resolver/challenge seams migrated there); building an
unused worker in the composition root now would be dead code.

Verification (exact task commands):

- `cd features/authentication && go test -race ./internal/logic/delivery/...
  ./storetest` → **ok** (both packages pass under `-race`).
- `make check` → **all checks passed**.
- `make guard` → **exit 0** (all thirteen guards pass).

Premise adaptation logged (reconciling §4.1/§6.1.1 with the AV3-0.6 frozen
repository): the design pins account resolution + challenge issuance + rendering
in the worker, off the request path, while the frozen `deliveryjob.Repository`
can write `Payload` only at `Enqueue`/`Replace` (no payload-update op). The worker
therefore persists the worker-rendered payload with `Replace` keyed by the
idempotency key — a two-phase init (opaque enqueue → worker resolve/render/persist
→ deliver on the next claim). This keeps the request path opaque, keeps the
rendered secret durable before the first send (so crash-after-send replays the
identical secret), and uses only the frozen repository ops. Cost: one extra
claim tick of latency per opaque job and a canceled tombstone from `Replace`
(purged under retention). No repository-shape change; no product/architecture
premise conflict.

Handoff to AV3-4.3 (migrate all outbound sites):

- The delivery seam is `delivery.Service` (Enqueue/Replace) on the request path
  and `delivery.Worker` (Run/RunOnce) on the host lifecycle. AV3-4.3 must build
  and wire a concrete `delivery.Initializer` (resolve identifier → issue/render
  challenge → `Router.Render`; `Discard` cancels the challenge) in the composition
  root, closing over `authsvc`/passwordless resolution, and expose the worker to
  the host process. `delivery` must NOT import a service — the Initializer is
  injected from `authentication.go`.
- Unauthenticated starts enqueue an OPAQUE command: `Envelope{ResolutionInput:
  normalized}` with empty `Body`/`HTML` (the worker discriminates opaque vs
  rendered on `Body=="" && HTML==""`). Session-gated starts return the `Receipt`;
  the receipt is the idempotency key (stable across the worker's init-time
  `Replace`), NOT the job ID.
- GAP for AV3-4.3's session-gated status endpoint: the frozen
  `deliveryjob.Repository` has no read/get-by-key method, so a status handler
  cannot read job state through the current port. Resolve by either adding a
  read-only lookup (reopens AV3-0.6 / phase-2 stores + storetest) or tracking
  status out-of-band. Flag before implementing the status route.
- The IdentifierKeyer-derived idempotency key is caller-supplied to
  `Command.IdempotencyKey` (delivery stays sdk/domain-only and does not import the
  keyer).

### 2026-07-13 — AV3-4.3 migrate all existing outbound sites

Dependencies verified complete: AV3-4.2 and all of phase 3 checked off in
TASKS.md and gate-green; AV3-4.1/4.2 delivery renderer/router + queue/worker
present. Preserved all pre-existing worktree changes; touched only this task's
files.

Premise adaptation logged (the AV3-4.2 GAP, per the orchestrator directive):
the frozen `deliveryjob.Repository` had no read op, so a session-gated
delivery-status handler could not read job state. Resolved by ADDING a narrow,
read-only `GetLatestByIdempotencyKey(ctx, key) (Job, error)` to the port — keyed
on the idempotency key (the Receipt the owner holds), returning the
most-recently-created row for that key (the live row after a resend/init-time
Replace leaves canceled tombstones), `sdk.ErrNotFound` for an unknown key. It
never leases, mutates, or resolves an account, so it is not part of the
concurrency surface — purely additive and read-only; no atomicity or
state-machine change. This deliberately reopens the AV3-0.6 / phase-2 port shape.
Implemented in all four impls (storetest reference `refDeliveryJobs`, examples/
auth-cms `authmem.deliveryJobRepo`, pgx `DeliveryJobStore` — `ORDER BY created_at
DESC, id DESC LIMIT 1`, turso twin) plus a `GetLatestByIdempotencyKey` storetest
conformance case under the existing skip-loud `DeliveryJobs` guard (asserts the
latest row wins over a canceled tombstone, the read never leases, and a terminal
state is observable).

Migrated every outbound site off request-time direct send onto the durable
outbox:

- `authsvc`: new `internal/logic/authsvc/delivery.go` — a `deliveryQueue`
  seam (Enqueue/Replace/Status; `delivery.Service` satisfies it),
  `identifierKeyer` seam, a PII-free `idempotencyKey` helper (host keyer, else a
  SHA-256 fallback), `enqueueRendered` (render-on-request → enqueue for the
  account-resolved flows), and the concrete `delivery.Initializer`
  (`Initialize`/`Discard`) dispatching on job purpose. Register now renders the
  verification code and enqueues a rendered job (account already created — not
  enumeration-sensitive). ForgotPassword is now the enumeration-safe opaque
  start: it normalizes and enqueues `Envelope{ResolutionInput: normalized}` with
  no account resolution or provider call; the worker's `initPasswordReset`
  resolves the active verified recovery identifier off the request path, issues
  the token, renders, and delivers (unknown/unverified → deliver=false, no
  challenge, no mail). `Discard` voids the reset token via `RedeemToken` on
  terminal failure (uses existing challenge ports — the challenge port is NOT
  reopened). OAuth pending-link renders + enqueues, and rolls back the
  pending-link oauthstate (Consume) on a failed enqueue. Deleted
  `sendVerificationEmail`/`sendResetEmail`/`sendPendingLinkEmail`. Added public
  `DeliveryStatus`.
- `invitationsvc`: deleted the `deliver` email/notify fork and the direct
  `email.Message` build; `sendInviteSent`/`sendMemberAdded` now render through the
  shared router and enqueue (Enqueue for a fresh invite, Replace for a resend —
  supersedes the prior pending job), keyed on the invitation ID. The accept token
  rides a rendered `inviteLink` query param.
- Composition root (`authentication.go`): builds the `delivery.Service` queue
  when `DeliveryJobs` is wired, injects it (and `IdentifierKeyer`) into both
  services as genuine-nil-when-off interfaces, builds the `delivery.Worker` with
  the auth service as `Initializer`, and exposes `RunDeliveryWorker(ctx)`
  (host-owned lifecycle) + `DeliveryStatus` (alias `DeliveryStatus = delivery.Status`).
- Transport: `GET /auth/delivery/status?receipt=` gated by `RequireLiveSession`
  (the live-session-gated status handler); DTO + `DeliveryStatus` on the inbound
  `authService` interface.
- Host (`examples/auth-cms`): wired `DeliveryEncrypter` (bundled AES-GCM, distinct
  `AUTH_DELIVERY_ENCRYPTER_KEY` or ephemeral-dev key) and the bundled
  `IdentifierKeyer` (`AUTH_IDENTIFIER_KEY`), and run the worker from the host
  process on its own Background-derived context with graceful stop after HTTP
  drains (mirrors the events poller order). This resolves the AV3-4.2-left
  runtime gap (DeliveryJobs wired without an encrypter would have been
  `ErrDeliveryEncrypterRequired` at boot).

Test rework: the authsvc/invitationsvc test harnesses now wire the REAL
delivery.Service + Worker over an in-mem `deliveryjob.Repository` behind a
synchronous `drainingQueue` (white-box field injection, same package), so the
existing mail-assertion tests read the worker-delivered message within the call.
Added authsvc tests for the enumeration same-path (known/unknown both enqueue one
opaque job, neither claimed on the request path), a blocking-provider bounded-timing
start, and the status projection. Inbound tests use a `stubQueue` + real router.

Verification (exact task commands):

- `cd features/authentication && go test ./internal/logic/authsvc/...
  ./internal/logic/invitationsvc/... ./internal/inbound/authentication/...` →
  **ok** (all three pass).
- `rg -n 'sendVerificationEmail|sendResetEmail|sendPendingLinkEmail|email\.Message\{'
  features/authentication` → the only hit is
  `internal/logic/delivery/router.go:294` (the ONE canonical transport send inside
  `Router.Deliver`); no obsolete direct-send sites remain.
- `make check` → **all checks passed**; `make guard` → all thirteen guards pass.

Run-and-look (host, not required by the task but recorded): booted
`examples/auth-cms`, `POST /auth/register` returned 201 immediately and ~3s later
the worker delivered the verification code via the console sender
(`outcome=delivered purpose=registration_verification`); the delivery-job log line
carried only job_id/kind/purpose/outcome/attempt — no destination or secret.
Graceful shutdown stopped the worker cleanly.

Handoff to AV3-4.4 (production transport and worker wiring validation):

- The outbox is now REQUIRED to send: with `DeliveryJobs` wired, a nil
  `DeliveryEncrypter` is already `ErrDeliveryEncrypterRequired`; with the queue
  off (`DeliveryJobs` nil) the send sites fail closed with `ErrDeliveryDisabled`
  (wraps `sdk.ErrForbidden`) rather than sending synchronously. AV3-4.4 should add
  the construction-time negatives (console transport rejection in production, the
  worker/lifecycle-acknowledgment check, non-durable repo metadata) — the
  transport-security validation seam (`validateDeliveryTransports`) already exists.
- `RunDeliveryWorker(ctx)` is the host lifecycle hook (no-op when the outbox is
  off) and is wired in `examples/auth-cms`; AV3-4.5's real-interaction check drives
  it per dialect.
- Scope note (flagged, not silently fixed): the phase's "reset-complete notice"
  has no existing send site or template — ResetPassword sends nothing today — so
  there was nothing to migrate; it is left for the credential/notice work rather
  than inventing a new template here. The now-vestigial `mailer`/`mailFrom` fields
  on the authsvc/invitationsvc services (superseded by the router) were left in
  place to keep the diff surgical (removing them ripples through every test
  builder's Deps).

### 2026-07-13 — AV3-4.4 production transport and worker wiring validation

Dependencies verified complete: AV3-4.2 (this task's stated dependency) plus
AV3-4.1/4.3 all checked off in TASKS.md and gate-green; the outbox is the only
send path and `validateDeliveryTransports`/`ErrDeliveryEncrypterRequired` already
existed from AV3-0.1/4.2/4.3. Preserved all pre-existing worktree changes; touched
only this task's files (feature constructor/config validation + the deliveryjob
port's optional durability metadata + the construction-matrix tests). Full
proof-host runtime remains phase 8; examples/auth-cms was NOT modified (it runs
development mode, so the new production-only gates never fire there).

Added the two production-negative construction checks the phase file names that
did not yet exist, alongside the already-covered console/metadata-less transport
and missing-encrypter rejections:

- Non-durable/in-process-only delivery repository — new optional
  `deliveryjob.Durability{InProcessOnly}` + `deliveryjob.DurabilityReporter` on the
  domain port (mirrors the email/notify transport capability posture, inverted for
  the "where metadata can identify it" rule). `security.go` gains
  `validateDeliveryDurability(mode, repo)` and `ErrNonDurableDeliveryRepository`:
  production rejects a repository that POSITIVELY declares `InProcessOnly:true`; a
  repository declaring no metadata (pgx/turso implement no reporter) is tolerated —
  a durable store is never asked to prove a negative; development permits either.
- Worker/lifecycle acknowledgment — new `Config.DeliveryWorkerAcknowledged bool`
  (a wiring assertion, no env tag) + `ErrDeliveryWorkerUnacknowledged`. Because the
  outbox is the ONLY send path (AV3-4.3) and the feature cannot observe the host
  process lifecycle, production with `DeliveryJobs` wired REQUIRES the host to
  affirm it runs `RunDeliveryWorker`; an unacknowledged outbox would silently
  swallow every message. Development tolerates the zero value.

Both new checks live inside the existing `repos.DeliveryJobs != nil` block in
`NewService`, ordered AFTER `ErrDeliveryEncrypterRequired` (encrypted payloads are
required in every mode — the development-permits-console-but-still-requires-
encryption rule) and BEFORE the transport-security validation. Development mode
enforces neither durability nor the acknowledgment but still requires the
encrypter.

`security_test.go` (the established construction-matrix home) gains a
`cryptids.Encrypter` stub, a metadata-less `stubDeliveryJobs` reference plus
`durableDeliveryJobs`/`inProcessDeliveryJobs` variants, and seven cases: production
rejects a non-durable repo, accepts a durable repo, tolerates a metadata-less repo,
requires the worker acknowledgment, reports the missing encrypter before durability
(ordering), development tolerates a non-durable unacknowledged outbox, and
development still requires the encrypter. The pre-existing console/metadata-less
transport rejections (AV3-4.2/0.1) already covered the first two bullets and were
left intact.

Verification (standing per-task gate — AV3-4.4 names no ```sh``` block, so the
narrowest module gate + guards per the overview's standing verification):

- `cd features/authentication && go build ./... && go test ./... && go vet ./...`
  → **ok** (every package passes; deliveryjob has no test files); vet clean.
- `go test -run 'Delivery|Durab|Worker|Production' -v .` → all seven new cases plus
  the four pre-existing production-transport cases **PASS**.
- `make guard` → **exit 0** (all thirteen guards pass).
- `examples/auth-cms` `go build ./...` → **BUILD OK** (the additive port method and
  new Config field are non-breaking; authmem implements no DurabilityReporter and is
  tolerated — it only runs development anyway).

Premise adaptations: none — the design (§8, V15) names both negatives; the exact
field/error names are implementer latitude (the phase file names behavior, not
identifiers). One scope note (flagged, not silently fixed): `examples/auth-cms`'s
in-process `authmem.deliveryJobRepo` could declare `Durability{InProcessOnly:true}`
for defense-in-depth if it were ever run in production, but the host runs
development mode and the touch scope is "feature constructor/config validation and
proof-host composition tests only" — so the runtime host was left untouched. Wire
that declaration (and `DeliveryWorkerAcknowledged: true`) in the phase-8 production
proof-host composition, not here.

Handoff to AV3-4.5 (worker real-interaction check): the production gates added here
are construction-only and dormant in development, so they do NOT affect the
development-mode disposable-host run. AV3-4.5 drives `RunDeliveryWorker` against each
fresh dialect store (pgx/turso) with console delivery in development; neither store
implements `DurabilityReporter`, so no durability gate fires, and development skips
the worker-acknowledgment gate — construction is unaffected. Live DBs available:
authv3-pg (`POSTGRES_TEST_DSN`, C-collation `authv3_cconf`) and authv3-libsql
(`http://127.0.0.1:8080`, `TURSO_AUTH_TOKEN=local-dev`).

### 2026-07-13 — AV3-4.5 worker real-interaction check (phase 4 close)

Dependencies verified complete: AV3-4.3 and AV3-4.4 (this task's stated deps) plus
AV3-4.1/4.2 all checked off in TASKS.md and gate-green. This is a run-and-look
task — NOT marked complete from hermetic green; it is proven by real interaction
against both live SQL dialects plus a live host run, transcripts recorded below
(secrets redacted). Preserved all pre-existing worktree changes throughout; the
only files touched were two DISPOSABLE run-and-look driver tests, both DELETED
after capturing evidence (no committed code change — the tree is unchanged from
AV3-4.4).

Premise adaptation logged (per the orchestrator handoff, matching the AV3-4.3
precedent): `examples/auth-cms` is a memory-only host (authmem) with no DSN-backed
mode, so "a disposable host/worker against each fresh dialect store" was satisfied
by driving the REAL `delivery.Worker` + `delivery.Service` (`internal/logic/delivery`,
imported directly by a same-feature store-module test — allowed by the Go internal
rule since `features/authentication/stores/{pgx,turso}` is rooted at
`features/authentication/`) over each live `DeliveryJobStore`, with a real
AES-256-GCM `cryptids.NewAESGCM` envelope encrypter and a real `email.NewConsole`
transport through the shared `delivery.NewRouter`. The host leg (auth-cms, memory
dialect) then re-confirmed the full authsvc path with a REAL challenge pepper in
play. No product/architecture change; the disposable drivers were removed so `make
guard`/`make check` and the committed tree are unaffected.

Exercised each phase-named behavior against BOTH live dialects (fresh/truncated
`delivery_jobs`), driving the actual worker claim/complete/retry/purge SQL, the
real seal/open crypto, and the real console send:

- Leg A enqueue → claim → console delivery → success: observer `outcome=delivered
  attempt=1`, `Status=succeeded`; the explicit console message carried the code and
  destination, the worker diagnostic line did not.
- Leg B transient failure → retry → success: attempt 1 `outcome=retried` (status
  pending), clock advanced past the 1s backoff, attempt 2 `outcome=delivered`,
  `Status=succeeded` — the persisted secret was resent unchanged.
- Leg C replacement: `Replace` superseded the prior pending job (exactly 1
  `canceled` tombstone in the row), and only the live job delivered (observer count
  == 1); the superseded job never delivered.
- Leg D terminal failure cleanup: always-failing transport, `MaxAttempts=2` → attempt
  1 retried, attempt 2 `outcome=failed`; the injected `Initializer.Discard` fired
  exactly once (challenge cancellation), `Status=failed`, and the persisted
  `last_error="deliver failed via email"` carried no secret or destination.
- Leg E lease recovery: worker A claimed then "crashed" (no completion); after the
  lease expired worker B reclaimed the SAME row (attempt 2); worker A's stale
  `Succeed(lease-A)` was rejected `sdk.ErrConflict`; worker B delivered and
  succeeded — at-least-once reclaim with the late completer losing.
- Leg F opaque start (two-phase init): an opaque `Envelope{ResolutionInput}` with
  empty Body/HTML → worker `Initialize` rendered once and persisted via `Replace`
  (`outcome=initialized`), the next claim delivered (`outcome=delivered`),
  `Status=succeeded` — single initialize, secret persisted once.
- Leg G purge: after advancing past the 24h retention, `Worker.Purge` removed all 8
  terminal rows (0 remaining), pending rows untouched.

Secret hygiene (both dialects): the worker's own diagnostic log (a separate slog
sink from the console transport) was asserted to contain NONE of the six OTP
secrets, the six destinations, or the opaque resolution input — every "delivery
job" line carried only `job_id/kind/purpose/outcome/attempt`. The console sender
line (the explicit development message the phase file exempts) carried the code and
`to=` as expected; recorded redacted.

Host run-and-look (auth-cms, memory dialect, REAL authsvc Initializer + REAL
challenge pepper `AUTH_CHALLENGE_PEPPER` set): `POST /auth/register` →
`HTTP=201 time_total=0.057s` (the worker did NOT block the request path); the
host-run `delivery.Worker` then delivered asynchronously —
`msg="delivery job" ... purpose=registration_verification outcome=delivered
attempt=1` with no destination or code on that line; the console sender logged the
verification code (explicit dev message). The configured pepper value appeared
`0` times anywhere in the host log; no "delivery job" worker line contained the
destination or a 6-digit code. Graceful shutdown on SIGTERM stopped HTTP → delivery
worker → bus in order and the process exited cleanly (no worker goroutine leak).

Exact commands / observed results:

- pgx: `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable'
  go test -run TestAV345_WorkerRealInteraction_Postgres -v ./...` (in
  `features/authentication/stores/pgx`, disposable driver) → **PASS**, all 7 legs.
- turso: `TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev'
  go test -tags=integration -run TestAV345_WorkerRealInteraction_Turso -v ./...` (in
  `features/authentication/stores/turso`, disposable driver) → **PASS**, all 7 legs.
- host: `PORT=8087 LOG_LEVEL=DEBUG AUTH_CHALLENGE_PEPPER=… ./authcms` + `curl -X POST
  /auth/register` → 201 in 57ms, async worker delivery, pepper absent (grep count 0),
  clean graceful shutdown.

Phase-4 close gate (phase acceptance):

- `make check` → **all checks passed** (every module builds/tests/vets, templ no
  drift, integration-tag vet compiles, all guards).
- `make guard` → **exit 0** (all thirteen guards pass).
- Worker real-interaction evidence recorded above; a blocking provider did NOT delay
  the unauthenticated/register start response (57ms 201, delivery async) — phase
  acceptance's "a blocking provider does not delay an unauthenticated start response"
  is met (Leg B/D drove a failing/blocking transport off the request path, and the
  host register returned before any send).

Handoff to phase 5 (AV3-5.1, `06-service-rekey.md`): nothing in phase 4 is left
open. The delivery seam is stable and live-proven on both dialects — `delivery.Service`
(Enqueue/Replace/Status) on the request path and `delivery.Worker` (Run/RunOnce/Purge)
on the host lifecycle, with the concrete authsvc `Initializer` wired in
`authentication.go` and exposed as `RunDeliveryWorker`. Phase 5's registration/login/
token/recovery/resolver re-key onto identifiers should keep enqueueing opaque starts
through this seam (ForgotPassword is already the opaque-start precedent) and continue
deriving PII-free idempotency keys via the host `IdentifierKeyer`; no delivery-side
change is required. The `GetLatestByIdempotencyKey` read-only status projection ties
on identical `created_at` (the id-DESC tiebreak surfaces the latest), which is a
non-issue under real wall-clock ordering but worth remembering if any re-key test
uses an injected clock without advancing between enqueue and resend.
