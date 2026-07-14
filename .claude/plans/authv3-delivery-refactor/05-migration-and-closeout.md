# Phase 5 — migration, removal, proof hosts, and closeout handoff

Depends on phases 3 and 4.

## Outcome

Delete the auth-specific durable queue only after both replacement modes satisfy
the shared characterization suite, migrate hosts and docs, and return the complete
auth-v3 system to its existing reviewer/remediation wave.

### AV3D-5.1 — remove bespoke delivery persistence and worker

Delete:

- `features/authentication/domain/deliveryjob`;
- auth pgx/turso delivery-job stores and reference/storetest implementations;
- auth delivery-job migrations once the upgrade path is documented;
- the private claim/poll/purge worker and durability reporter; and
- `Repositories.DeliveryJobs`, `DeliveryWorkerAcknowledged`, and obsolete errors.

Retain and reshape the renderer/router, encrypted envelope, initializer, processor,
observer, and public status behavior. Add guards that fail if auth-specific delivery
tables/repositories or request-time provider calls return.

### AV3D-5.2 — canonical migrations and adopter upgrade

Keep pgx/turso filename sets identical in both jobs and authentication modules.
Write an explicit upgrade procedure for deployments with pending auth delivery
rows:

1. stop old auth delivery workers and quiesce admission;
2. drain or export/re-enqueue pending encrypted commands without decrypting them
   in tooling;
3. apply generic jobs schema changes and new host wiring;
4. verify no active auth delivery rows remain;
5. remove the obsolete auth table in the allowed migration strategy; and
6. start the generic jobs runtime or selected bounded runtime.

If existing released tags require append-only migrations, stop before rewriting a
canonical migration.

### AV3D-5.3 — proof-host variants and operational health

Make auth-cms demonstrate the recommended durable jobs composition. Provide a
small/development bounded-mode variant without hiding its ephemeral posture.

Expose bounded, secret-free health/status sufficient to distinguish:

- runtime not started;
- queue saturated/backlogged;
- provider retry/dead-letter activity; and
- observer failure.

No health endpoint may expose recipient identifiers or payloads.

### AV3D-5.4 — public docs and compatibility inventory

Update authentication, jobs, events, example, migration, and release docs with:

- mode selection and guarantees;
- composition snippets and lifecycle ownership;
- events-as-observation decision;
- encryption/key rotation expectations;
- at-least-once duplicate semantics and resend race;
- bounded-mode crash/multi-instance limitations;
- status retention and purge; and
- public removals/renames plus adopter steps.

Search the repository for every removed symbol/table and record the zero-result
inventory or intentional historical-plan references.

### AV3D-5.5 — final implementation-complete gate and auth-v3 handoff

Run:

- sdk/workers, jobs, authentication, integration, and proof-host tests under
  `-race` where supported;
- live pgx/turso jobs + auth suites on fresh/reset databases;
- restart, stale-claim, resend, saturation, shutdown, and real-provider protocols;
- migration parity and upgrade rehearsal;
- `make check` and `make guard`; and
- adversarial searches for plaintext secrets, direct provider sends, bespoke auth
  job persistence, unbounded goroutines/queues/maps, and event-driven dispatch.

Append the milestone evidence to `00-overview.md`, then begin the existing
`AV3-9.7` reviewer wave. That review covers the whole auth-v3 implementation plus
this follow-up. `AV3-9.8` owns accepted remediation and final reverification; do
not create a competing reviewer track here.

### Phase 5 gate

All AV3D tasks are logged, no bespoke auth queue remains, both modes have their
claimed evidence, hosts/docs/migrations are current, and `AV3-9.7` is unblocked.

## Execution log

Append dated task evidence, the compatibility inventory, upgrade rehearsal, and
the final handoff to `AV3-9.7`.

### 2026-07-13 — AV3D-5.1 (remove bespoke delivery persistence and worker)

Removed authentication's private durable delivery queue in full. Durable delivery
is now exclusively the generic **jobs** feature reached through a host-wired
`Config.DeliveryDispatcher`; `in_process` is the bounded ephemeral pool. No second
auth durable queue remains. Public `DeliveryStatus` behavior, the encrypted
`command` envelope, the renderer/router, the initializer seam, the processor
engine, the observer, and the `deliverychar` suite are retained unchanged.
`DeliveryWorkerAcknowledged`→`DeliveryJobsAcknowledged` was already renamed in
AV3D-0.1 (verified: no `DeliveryWorkerAcknowledged` symbol remains).

DELETED:

- `features/authentication/internal/logic/delivery/worker.go` (private
  claim/poll/purge worker) + `worker_test.go`, `characterization_test.go`,
  `envelope_test.go` (worker-based harness deleted with the worker);
- the bespoke `deliveryjob` domain package (already absent from HEAD; confirmed no
  `domain/deliveryjob` import or `package deliveryjob` anywhere);
- auth pgx/turso delivery-job stores (`stores/{pgx,turso}/delivery_jobs.go`) and
  the `0014_delivery_jobs.sql` migration in BOTH dialect trees;
- the durability reporter, `Repositories.DeliveryJobs`, `Service.RunDeliveryWorker`,
  the old `repoDispatcher`/`newRepoDispatcher`/`bespokeToGeneric` bridge, and the
  obsolete errors (`ErrNonDurableDeliveryRepository`,
  `ErrInProcessDurableDeliveryRepository`, durability funcs).

RESHAPED CONSUMERS: `delivery.Service` drops the repo-backed default — the
`Dispatcher` is now required (`ErrDispatcherRequired`); `jobsprocessor.go`/
`dispatcher.go`/`service.go`/`envelope.go` shed the deliveryjob import; the
construction matrix in `authentication.go`/`security.go` treats a wired
`DeliveryDispatcher` as the jobs-mode queue capability (`ErrDeliveryQueueRequired`
when jobs mode lacks it); `examples/auth-cms` removed all residual `DeliveryJobs`
wiring (`main.go`, `authmem`, `ports_v3.go`, jobs/in-process proof tests, override
test flipped to in_process, `main_test.go`/`production_test.go` set
`DeliveryDispatcher` via a `jobsDispatcher(t)` helper over an in-memory fenced
queue); storetest suite dropped its DeliveryJobs section; the pgx/turso conformance
truncation lists dropped `delivery_jobs`; README (feature + auth-cms) rewritten to
the two-mode model.

ADDED GUARDS (Makefile, following the existing G1–G13 style; `make guard` now runs
fifteen): `guard-auth-no-delivery-repo` (G14, AV3D-5.1) fails if `delivery_jobs`
(case-sensitive snake_case table — never matches the legitimate jobs-mode names
DeliveryJobKind/DeliveryJobRuntime/DeliveryJobsAcknowledged) OR a `deliveryjob`
domain package/import returns to `features/authentication`/`examples/auth-cms`;
`guard-auth-no-request-time-provider` (G15, AV3D-2.4) fails if a producer package
(a sibling of `internal/logic/delivery`) calls a provider-send verb
(`.Deliver`/`.Send`/`.Notify`) directly on the request path. Both were proven
meaningful (each fails on an injected sentinel, restored) and pass on the clean
tree. The AST-precise companion `TestNoProducerBypassesDispatcherSeam`
(producer_seam_test.go) is retained.

MIGRATION PARITY: after removing `0014_delivery_jobs.sql` from both trees,
`diff` of the pgx and turso migration filename sets is IDENTICAL (0001–0013);
README migration section updated to "0001–0013 / thirteen".

VERIFICATION (exact commands, all PASS):

- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` — PASS
- `cd features/authentication/stores/pgx && go build ./... && go test -race ./... && go vet ./...` — PASS
- `cd features/authentication/stores/turso && go build ./... && go test -race ./... && go vet ./... && go vet -tags=integration ./...` — PASS
- `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...` — PASS
- migration parity: `diff <(ls .../pgx/migrations) <(ls .../turso/migrations)` — IDENTICAL
- `make guard-auth-no-delivery-repo guard-auth-no-request-time-provider` — PASS
- `make guard` — PASS (fifteen guards)
- `make check` — PASS ("all checks passed": templ no-drift, warm-scaffold-cache,
  per-module vet/build/test across all modules, integration-tag vet, all guards)

LIVE-STORE AVAILABILITY: `POSTGRES_TEST_DSN`, `TURSO_DATABASE_URL`, and
`TURSO_AUTH_TOKEN` are all UNSET — the pgx/turso live conformance suites loud-SKIP
and remain the standing open owner gate (unchanged by this task).

PREMISE ADAPTATIONS: the phase-5 spec's "durability reporter" and
`ErrInProcessDurableDeliveryRepository` were already partly folded by earlier AV3D
phases; the removal here is of whatever residue remained. The worker-based
characterization harness was deleted with the worker per the task; the surviving
characterization coverage runs against the processor + in_process harnesses (jobs-
mode host proofs in `examples/auth-cms/cmd/server` still pass). No plaintext
secrets introduced; no contract weakened.

### 2026-07-13 — AV3D-5.2 (canonical migrations and adopter upgrade)

Doc-only task. Wrote the host-side adopter upgrade procedure for deployments that
scaffolded the bespoke auth delivery-outbox table under an earlier v3 cut and are
now crossing the AV3D delivery refactor (durable delivery is the generic **jobs**
`fenced_job_queue`; `in_process` is ephemeral). Per the greenfield-migrations rule
this is host-side runbook prose + host-owned migration guidance — NO new canonical
migration files, no upgrade/evolution files in either canonical set.

TAG CHECK (append-only guard, phase-file "if released tags require append-only
migrations, stop"): `git tag -l` — **EMPTY** (zero tags of any kind). No module
tag has shipped the old auth delivery migration, so the greenfield rewrite that
removed it from the canonical set carries no append-only constraint. Recorded and
proceeded.

MIGRATION PARITY (both features, both trees — identical filename sets):

- AUTH: `diff <(ls features/authentication/stores/pgx/migrations) <(ls
  .../turso/migrations)` → IDENTICAL (0001–0013, thirteen files).
- JOBS: `diff <(ls features/jobs/stores/pgx/migrations) <(ls .../turso/migrations)`
  → IDENTICAL (0001_job_queue, 0002_job_schedules, 0003_fenced_job_queue).

FILES CHANGED:

- `RELEASING.md` — added the **Auth delivery-runtime upgrade runbook (bespoke auth
  outbox → generic jobs / in_process)** as a sibling to the existing v2→v3 host
  upgrade runbook: six-step procedure — (1) stop old delivery workers + quiesce
  admission; (2) drain [Option A, recommended] OR export/re-enqueue the opaque
  encrypted commands WITHOUT decrypting [Option B, with explicit pgx + SQLite/libSQL
  INSERT…SELECT that carries `payload`/`kind`/`idempotency_key`→`logical_key` opaquely
  and a `NOT EXISTS` active-key guard], each with tradeoffs; (3) apply generic jobs
  schema (0001–0003) + jobs-mode/in_process host wiring; (4) verify no active rows
  (exact `SELECT count(*) … WHERE state = 'pending'` == 0); (5) host-owned
  `DROP TABLE IF EXISTS` (both dialects); (6) start the jobs runtime or bounded
  `RunDelivery`. Added the no-decrypt / no-blind-copy / same-encrypter-key / no
  re-enqueue-into-ephemeral warnings and the empty-`git tag -l` evidence. Also added
  a forward-pointer callout in the v2→v3 runbook's Step 5 flagging the retained
  `delivery_jobs` CREATE as obsolete at/after the refactor (validation record
  preserved, DDL not rewritten).
- `features/authentication/README.md` — cross-linked the new runbook from the
  "Migrations are host-owned (0001–0013)" section and from the v2→v3 UPGRADE NOTE
  (where the AV3-9.2 runbook is referenced). Deliberately avoided the literal
  `delivery_jobs` snake_case token (G14 forbids it under features/authentication);
  referred to it as "the bespoke delivery-outbox table" — the exact table name lives
  only in `RELEASING.md`, out of guard scope.

VERIFICATION (exact commands, all PASS):

- `git tag -l` — EMPTY (recorded; no append-only constraint).
- AUTH parity `diff` — IDENTICAL (0001–0013).
- JOBS parity `diff` — IDENTICAL (0001–0003).
- `make guard` — PASS (fifteen guards, incl. G14 guard-auth-no-delivery-repo after
  the README wording avoided the forbidden token).
- `make check` — PASS ("all checks passed": templ no-drift, warm-scaffold-cache,
  per-module vet/build/test across all modules, integration-tag vet, all guards).

LIVE-STORE AVAILABILITY: unchanged from AV3D-5.1 — `POSTGRES_TEST_DSN`,
`TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN` all UNSET; live conformance loud-SKIPs;
this task is doc-only and required no live run.

PREMISE ADAPTATIONS: the phase spec's step 5 says "remove the obsolete auth table
in the allowed migration strategy" — realized as a host-owned `DROP TABLE IF EXISTS`
(both dialects; SQLite's DROP TABLE also drops the table's indexes, so no separate
index-drop step). The runbook is written for a host coming off an EARLIER v3 cut
that had the bespoke outbox (the only population source for pending auth delivery
rows); a host that never scaffolded it, or has an empty table, short-circuits to
Step 5/6. Tooling never decrypts: every export/re-enqueue/count/drop treats the
sealed `command.Envelope` payload as opaque bytes. No contract weakened.

### 2026-07-13 — AV3D-5.3 (proof-host variants and operational health)

Made auth-cms demonstrate BOTH delivery postures and exposed a host-composed,
secret-free delivery health surface. The recommended DURABLE generic-jobs
composition was already the auth-cms default (AV3D-3.1); this task verified it,
made the README state it plainly + exemplary, ADDED the bounded ephemeral variant
without hiding its posture, and ADDED operational health.

VARIANT SHAPE DECISION (recorded): the smallest honest shape per repo convention is
an **env switch in the existing single `main`**, not a separate `cmd`. auth-cms's
`main` already selects runtime behavior from `environment.GetEnvOrDefault` (the
`EVENTS_OUTBOX`, `TRUSTED_PROXY_COUNT`, `AUTH_*` precedent), so `DELIVERY_MODE`
(`deliveryMode()`) joins that family: default/unset/`jobs` → the DURABLE jobs
composition; `in_process` → the bounded EPHEMERAL pool the host drives via
`authSvc.RunDelivery`; any other value FAILS SAFE to jobs (never a silent ephemeral
selection). A separate cmd would duplicate the whole composition root (auth +
authorization + cms + events wiring) for one field — rejected as larger and less
honest. The ephemeral posture is announced LOUDLY: `DELIVERY_MODE=in_process` logs a
startup WARN stating accepted in-flight work is LOST on crash/restart, there is NO
cross-instance coordination, and multiple instances de-duplicate on NEITHER (a user
may receive duplicate messages). The `RunDelivery` lifecycle is wired (goroutine on
its own Background-derived context, stopped after HTTP drains, mirroring the jobs
runtime order).

HEALTH-SEAM DESIGN DECISION (recorded): health is **host-COMPOSED** over existing +
one small additive read seam — NO new feature route (the architecture's preferred
posture). A host-local `examples/auth-cms/internal/deliveryhealth` package aggregates
three secret-free observation points and serves `GET /healthz/delivery`:
- **runtime not-started vs running** — the host owns the delivery goroutine, so
  `MarkStarted`/`MarkStopped` bracket it (both modes);
- **provider retry + dead-letter activity, and observer failure** — `Emitter` wraps
  `Config.DeliveryEventsEmitter`, classifies each bounded `authentication.delivery.<transition>`
  topic (only `ev.Type()` is read — never ExecutionID/Kind/Purpose) into a monotonic
  counter, forwards to the real bus, and increments `observer_failures` when the
  forward errors (both modes);
- **backlog/saturation** — jobs mode: `Dispatcher` wraps `Config.DeliveryDispatcher`
  to count admissions, `in_flight = admitted − (delivered+skipped+dead_lettered)`
  clamped ≥0; in_process mode: a small ADDITIVE read seam `auth.Service.InProcessQueueDepth()
  (queued, capacity int, ok bool)` (backed by a new `delivery.InProcessQueue.Depth()`)
  gives live `queued`/`capacity`/`saturated`. The two modes' backlog provenance
  differs because their queue substrates differ (durable jobs store vs in-memory
  channel); the snapshot labels both honestly. The event vocabulary does not emit
  `accepted`/`initialized` (only delivered/skipped/retried/dead_lettered/purged reach
  the observer today), so an events-only cross-mode in-flight gauge is not derivable —
  hence the mode-specific backlog sources rather than a feature-core change to emit
  admission events (rejected as out of scope + more invasive).

OUTPUT is counters/gauges/enums only: `{mode, runtime, admitted, in_flight, queued,
capacity, saturated, delivered, skipped, retried, dead_lettered, superseded, purged,
observer_failures}` — NO recipient identifier, destination, logical key, or payload.
A no-leak test serializes the endpoint output after a real delivery with a canary
recipient + captures the delivered secret and asserts neither appears.

FILES CHANGED:
- `features/authentication/internal/logic/delivery/inprocess.go` — added
  `InProcessQueue.Depth() (queued, capacity int)` (bounded, secret-free channel read).
- `features/authentication/authentication.go` — retained `inProcessQueue` on `Service`;
  added `Service.InProcessQueueDepth() (queued, capacity int, ok bool)` (ok only in
  in_process mode; jobs-mode backlog lives in the jobs store, not process-local).
- `examples/auth-cms/internal/deliveryhealth/deliveryhealth.go` (NEW) + `_test.go` —
  host-composed health: `Health`, `Emitter`/`Dispatcher` wrappers, `SetDepthSource`,
  `MarkStarted`/`MarkStopped`, `Snapshot`, `Handler`.
- `examples/auth-cms/cmd/server/main.go` — `deliveryMode()` env switch; branched the
  delivery composition (jobs queue/dispatcher/FencedRuntime built only in jobs mode;
  in_process flips config + loud WARN + `RunDelivery`); wired health (emitter +
  jobs-mode dispatcher wrap + in_process depth source + lifecycle markers); mounted
  `GET /healthz/delivery`.
- `examples/auth-cms/cmd/server/delivery_health_test.go` (NEW) — real-interaction
  proof: not-started vs running, backlog under saturation (503 over HTTP + endpoint),
  dead-letter increments after a permanent failure (MaxAttempts=1, always-failing
  sender), observer-failure visible (failing emitter), and no-leak.
- `examples/auth-cms/README.md`, `examples/auth-cms/.env.example` — documented both
  variants (jobs recommended/durable; in_process small/ephemeral, posture stated) +
  the health endpoint + `DELIVERY_MODE`.

VERIFICATION (exact commands, all PASS):
- `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...` — PASS
- `cd features/authentication && go build ./... && go test -race ./... && go vet ./...` — PASS
- `cd examples/auth-cms && go test -race -run TestDeliveryHealth ./cmd/server/` — PASS
  (all five health real-interaction tests)
- `make guard` — PASS (fifteen guards; G14 clean — the new code uses camelCase
  `DeliveryMode…`/`InProcessQueueDepth`, never the forbidden snake_case table token)
- `make check` — PASS ("all checks passed")
features/jobs was NOT touched (no jobs-feature change was needed — the health
surface reads jobs-mode backlog via the host-wrapped dispatcher, not a jobs seam), so
its narrow gate was covered by `make check`'s per-module run rather than a separate
invocation.

LIVE-STORE AVAILABILITY: unchanged — `POSTGRES_TEST_DSN`, `TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN` all UNSET; this task is hermetic (in-memory host + httptest) and
required no live store; the live conformance suites remain the standing open owner
gate.

PREMISE ADAPTATIONS: the task text says "auth-cms's main already has RuntimeMode env
handling" — precisely, `main` hardcodes `RuntimeMode=development` but does drive
runtime behavior from env throughout (`EVENTS_OUTBOX`, `TRUSTED_PROXY_COUNT`, the
`AUTH_*` builders); `DELIVERY_MODE` follows that established `GetEnvOrDefault` pattern.
No contract weakened; `Register` starts no goroutine; no feature→feature import (the
health package is host code and may import auth; it imports no other feature).

### 2026-07-13 — AV3D-5.4 (public docs and compatibility inventory)

Doc-only task. Brought authentication, jobs, events, example, migration, and
release docs to the post-AV3D two-mode delivery model, recorded the compatibility
inventory, and resolved the RELEASING.md historical-DDL scope gap inherited from
AV3D-5.2. No code changed; `make check` + `make guard` PASS.

FILES CHANGED:

- `features/authentication/README.md` — rewrote every stale delivery spot to the
  two-mode model: intro ("delivery outbox + worker" → host-owned delivery runtime,
  jobs/in_process); domain tree (dropped `deliveryjob/`, noted delivery owns no
  domain); `internal/logic/delivery/` layout line (shared processor + in_process
  runtime); store migration count (0001–0014 → 0001–0013); `GET /auth/delivery/status`
  (durable outbox → dispatcher); removed the `DeliveryJobs deliveryjob.Repository`
  struct field + its nil-semantics row + the "delivery rails" header wording. In the
  "Delivery execution modes" section ADDED: at-least-once/never-exactly-once + the
  resend/in-flight replacement race; encryption + single-key rotation honesty
  (in-flight sealed payload sealed under a removed key dead-letters); terminal +
  status retention (both modes) + `DeliveryStatus.Attempt` now 0; events-observe-
  never-queue (`DeliveryEventsEmitter`); a "Composition and lifecycle ownership"
  subsection with a jobs-mode wiring snippet (composition-adapter pattern +
  `jobs.FencedRuntime`) and the in_process `RunDelivery` note; multi-instance /
  crash-loss honesty on the `in_process` bullet. ADDED an "UPGRADE NOTE — the AV3D
  delivery-runtime refactor" (public removals/renames/additions, DeliveryStatus.Attempt=0,
  adopter steps + runbook pointer). Annotated the v2→v3 note's "delivery-job flow
  tables" as removed-by-AV3D. **G14 catch:** an initial edit wrote the literal
  `0014_delivery_jobs.sql` token under features/authentication (forbidden by G14);
  reworded to "the `0014` delivery-outbox migration" and re-verified the token is
  absent under features/authentication.
- `features/jobs/README.md` — documented the additive fenced delivery surface:
  layout (`primitives.go`, `fenced.go`, `job.FencedQueueRepository`); the
  `Repositories.Queue`/`FencedQueue` nil-semantics (at-least-one-required,
  `ErrFencedQueueRequired`); a "fenced delivery surface (AV3D)" section (lease
  fencing, logical-key admission/supersession, claimed-payload checkpoint, bounded
  retry + `Permanent` + `DeadLetterFunc` + `PurgeTerminal`, the frozen stdlib
  primitives seam, `NewFencedRuntime` + `ErrProcessTimeoutExceedsLease`); and the
  three-file migration set incl. `0003_fenced_job_queue`.
- `features/events/README.md` — ADDED an "Events observe work; they never queue it"
  subsection recording the auth-delivery decision (bus is not a queue; durable side
  effects ride Repositories; observer failure changes nothing; a future
  transactional outbox stays valid and separate).
- `examples/auth-cms/README.md` — intro ("durable delivery worker" → two-mode
  runtime); authmem port list (dropped `delivery-job`, noted delivery owns no auth
  port); Leg 11 header/body ("delivery worker" → delivery runtime, "outbox is the
  ONLY send path" → dispatcher, dropped the removed `ErrNonDurableDeliveryRepository`
  from the production-negative list to match `production_test.go`).
- `RELEASING.md` — auth-v3 identity BREAKING note: dropped `delivery_jobs` from the
  flow-tables list + delivery from the Repositories-ports list, ADDED an AV3D
  delivery-refactor delta paragraph (removals/renames/additions, canonical 0001–0013,
  DeliveryStatus.Attempt=0). Tag table: `0001…0014` → `0001…0013` (+ no delivery
  port), ADDED a `features/jobs` minor-floor row for the fenced surface. Production
  checklist: rewrote the actively-wrong `RunDeliveryWorker` bullet to the two-mode
  host-owned lifecycle (jobs recommended, in_process ephemeral, at-least-once not
  exactly-once), and added delivery-key single-key-rotation honesty. Step-6 verify
  text: named the runtime-specific drain instead of a bare `delivery_jobs`.
- `NOTES.md` — appended a dated "AV3D delivery-runtime refactor: release/change
  delta (AV3D-5.4)" entry amending the AV3-9.4 inventory in its own format
  (supersedes the old `RunDeliveryWorker`/`0001…0014` bullets; removals/renames/
  additions; guarantees-never-overstated; adopter runbook pointer; the RELEASING.md
  historical-DDL resolution; the grep inventory result).

RELEASING.md HISTORICAL-DDL RESOLUTION (the scope gap AV3D-5.2 flagged to me): the
v2→v3 host upgrade runbook's `delivery_jobs` CREATE DDL (both dialects) is a
validated AV3-9.2 record. Chosen form: **preserve, do not delete** — the DDL stays
in place, already annotated by the AV3D-5.2 Step-5 callout as obsolete-at/after-the-
refactor with a pointer to the sibling Auth delivery-runtime upgrade runbook; I
additionally corrected the actively-wrong forward-looking prose (the production
checklist `RunDeliveryWorker` posture, the tag table `0001…0014`/delivery-port, the
delivery-key rotation posture) and softened Step-6's `delivery_jobs` verify sentence
to name the runtime the host actually wired. Deleting validated history was rejected;
annotate-in-place is the cleanest honest form. The exact `delivery_jobs` snake_case
token lives only in `RELEASING.md` (+ the Makefile guard), outside G14's
features/authentication scope.

COMPATIBILITY INVENTORY (grep, 2026-07-13; scope = repo minus `.claude/plans/**`,
which is intentional plan history). Removed symbol/table → hits in shipping code/docs:

| removed symbol / table | active .go code | shipping docs | verdict |
|---|---|---|---|
| `Repositories.DeliveryJobs` (field) | 0 (2 STALE COMMENTS only: `authsvc/service.go:257`, `invitationsvc/service.go:202`) | only new removal-doc refs (README/RELEASING/NOTES) | CLEAN — comments flagged as follow-up |
| `domain/deliveryjob` / `package deliveryjob` | 0 (1 cross-feature doc COMMENT: `features/jobs/domain/job/fenced.go:20`, out of G14 scope) | new removal-doc refs only | CLEAN — comment flagged |
| `delivery_jobs` (table) | 0 | RELEASING.md (historical runbook + new AV3D runbook, intentional), Makefile (guard), NOTES.md (inventory); NONE under features/authentication (G14) | CLEAN |
| `DeliveryWorkerAcknowledged` | 0 | only rename-doc refs (README/RELEASING/NOTES) | CLEAN |
| `RunDeliveryWorker` | 0 | RELEASING.md (new AV3D runbook naming the OLD binary's method to stop, intentional) + removal-doc refs | CLEAN |
| `ErrNonDurableDeliveryRepository` | 0 | removal-doc refs only (was stale in auth-cms README Leg 11 → fixed) | CLEAN |
| `ErrDeliveryWorkerUnacknowledged` | 0 | removal-doc refs only | CLEAN |
| `ErrInProcessDurableDeliveryRepository` | 0 | removal-doc refs only | CLEAN |
| `repoDispatcher` / `newRepoDispatcher` / `bespokeToGeneric` | 0 | 0 (only `.claude/plans/**` history) | CLEAN |

New symbols confirmed present: `Config.DeliveryMode`/`DeliveryModeJobs`/`InProcess`/`Off`,
`DeliveryDispatcher`, `InProcessDelivery`, `DeliveryEventsEmitter`, `DeliveryEphemeralAcknowledged`,
`Service.RunDelivery`/`DeliveryJobRuntime`/`InProcessQueueDepth`, `DeliveryJobsAcknowledged`;
jobs `Repositories.FencedQueue`, `jobs.FencedRuntime`, `Permanent`, `Service.PurgeTerminal`,
migration `0003_fenced_job_queue`.

RESIDUAL (flagged, NOT fixed — out of docs-task scope, trips no guard): three stale
CODE COMMENTS reference removed symbols — `authsvc/service.go:257` and
`invitationsvc/service.go:202` ("Wired whenever DeliveryJobs is"), and
`features/jobs/domain/job/fenced.go:20` ("authentication's private
deliveryjob.Repository"). All are descriptive comments, not active references; a
follow-up code-comment sweep can retire them.

VERIFICATION (exact commands):

- grep inventory (precise, removed tokens, shipping scope) — commands + results
  above; every removed symbol/table has ZERO active-code hits (only flagged
  comments) and only intentional-history / new-removal-doc references.
- G14 token check: `grep -rn 'delivery_jobs' features/authentication` → CLEAN;
  `grep -rnE 'domain/deliveryjob|package deliveryjob' features/authentication
  examples/auth-cms` → CLEAN.
- `make guard` — PASS (fifteen guards; G14 `guard-auth-no-delivery-repo` + G15
  `guard-auth-no-request-time-provider` green).
- `make check` — PASS ("all checks passed": templ no-drift, warm-scaffold-cache,
  per-module vet/build/test across all modules, integration-tag vet, all guards).
- Module spot-builds: not separately re-run — `make check` compiles/tests every
  module (auth, jobs, auth-cms all `ok`); the README snippets are illustrative
  composition-root prose, not shipped code, and name only verified exported symbols.

LIVE-STORE AVAILABILITY: unchanged — `POSTGRES_TEST_DSN`, `TURSO_DATABASE_URL`,
`TURSO_AUTH_TOKEN` all UNSET; this task is doc-only and required no live run; the
live conformance suites remain the standing open owner gate.

PREMISE ADAPTATIONS: (1) The AV3-9.4 release/change inventory lives in `NOTES.md`
(the dated `## 2026-07-13 — auth-v3 release/change inventory (AV3-9.4)` entry), so
the delivery-refactor deltas were appended there in that entry's format (RELEASING.md
carries the keyed per-module tag notes it cross-references). (2) ARCHITECTURE.md was
NOT edited — its only delivery-adjacent content is the generic module list
(`features/events` one-liner) and the sdk-facility table, neither of which the
refactor invalidates; no surgical change was warranted. (3) The auth-cms README was
"mostly done at 5.3" per the task — verified and finished the residual Leg 11 /
intro / authmem stale spots. (4) Docs never overstate: bounded `in_process` never
claims durability or cross-instance coordination; jobs mode is at-least-once, never
exactly-once; no secret-leaking example was added.

### 2026-07-13 — AV3D-5.5 (final implementation-complete gate and AV3-9.7 handoff) — CLOSES PHASE 5

Ran the full implementation-complete gate FRESH (`-count=1` where caching could mask),
re-ran the adversarial searches against the clean tree, retired the three stale closeout
comments AV3D-5.4 flagged (comment-only), and appended the milestone evidence + AV3-9.7
handoff to `00-overview.md`'s execution log. No contract weakened; no live DB available.

GATE (command by command — full table with verbatim loud-skips lives in `00-overview.md`
under "2026-07-13 — MILESTONE EVIDENCE / AV3-9.7 HANDOFF (AV3D-5.5)"):

- sdk/workers `-race -count=1` — PASS; jobs `build`+`-race -count=1`+`vet` — PASS;
  authentication `build`+`-race -count=1`+`vet` — PASS (incl. the two touched packages
  authsvc + invitationsvc); auth/jobs pgx+turso store modules `-count=1` (+ turso
  integration vet) — PASS with every live conformance suite LOUD-SKIPPING (env unset);
  auth-cms `build`+`-race -count=1` — PASS; jobs-minimal `-count=1` — PASS.
- Protocols named to their carrying auth-cms tests (all PASS `-race -count=1`): restart
  (`TestRestartAfter{OpaqueAdmissionInitializesSafely,CheckpointResendsSameSecret,
  ProviderAcceptanceResendsSameSecret}`); stale-claim/supersession
  (`TestAdversarialReplaceWhile{Pending,Initializing,Checkpointed,Sending}`); resend
  (`TestReplaceCreatesFreshGenerationStatusSelectsLatest`,
  `TestSubmitOnceCoalescesOntoOneActiveExecution`,
  `TestInProcessHostRetryReusesSecretOverPool`); saturation
  (`TestDeliveryHealthBacklogUnderSaturation`, `TestInProcessHostSaturationReturns503OverHTTP`);
  shutdown (`TestInProcessHostShutdownDrainAndNoLeak`, `TestDeliveryRuntimeStartStop`);
  real-provider/HTTP drive (`TestJobsModeDeliveryEndToEnd`,
  `TestInProcessHostDeliversRegistrationOverPool`, `TestInProcessHostForgotPasswordAdmitsOverHTTP`).
- Live legs: pgx+turso jobs/auth conformance and the `livedelivery` pgx/turso delivery
  harnesses all LOUD-SKIP verbatim (env unset) and COMPILE (`go vet -tags='livedelivery
  integration' ./cmd/server` clean) — the standing open owner gate.
- Migration parity: AUTH `diff` IDENTICAL (0001–0013, no delivery table); JOBS `diff`
  IDENTICAL (0001–0003). Upgrade rehearsal: the RELEASING.md delivery runbook's Option-B
  `INSERT INTO fenced_job_queue …` columns all exist in `0003_fenced_job_queue.sql`
  (omitted columns nullable/DEFAULTed), the `NOT EXISTS` active-key guard matches
  `uq_fenced_job_queue_active_key`, Step-4/5 read the historical `delivery_jobs` DDL
  columns, and every named symbol exists in code — dry only, no live DB (one minor prose
  imprecision noted: Step-6 jobs-mode says `jobs.Runtime` where the host wires
  `jobs.FencedRuntime`).
- `make guard` — PASS (fifteen guards); `make check` — PASS ("all checks passed").

ADVERSARIAL SEARCHES (all CLEAN; commands + results recorded in `00-overview.md`):
plaintext-secrets-in-durable-paths (jobs payload = opaque `[]byte`→BYTEA/BLOB; delivery
seals before dispatch); direct-provider-sends (G15 grep ZERO + AST test green + send verbs
only inside `delivery/`); bespoke-auth-job-persistence (G14 both tripwires clean, no auth
delivery table); unbounded-goroutines (every production `go` pool-scoped/lifecycle-bounded);
unbounded-channels/maps (bounded queue + capacity-bounded/lifecycle channels + finite
max-entry+TTL-evicted retention map); event-driven-dispatch (sole `Emit` is the
best-effort observer, strictly after the recorded state, never load-bearing).

CLOSEOUT DEBRIS FIXED (comment-only): `authsvc/service.go` + `invitationsvc/service.go`
`Queue`-field docs (dropped removed `DeliveryJobs`/"durable outbox" → delivery-dispatch
seam) and `jobs/domain/job/fenced.go` doc (dropped removed `deliveryjob.Repository` → the
generic-jobs surface auth runs on). Both touched modules re-ran green under
`-race -count=1`. The adjacent `Deliver`-field "durable worker (phase 4)" comments
(authsvc:252, invitationsvc:197) are stale too but OUT of the named-three scope — left for
the reviewer wave (recorded as an open flag).

PHASE 5 GATE (met): all AV3D tasks are logged; no bespoke auth durable queue remains
(G14 clean, no auth delivery table/domain); both modes have their claimed evidence (jobs
mode end-to-end + restart/resend/stale-claim; in_process saturation/shutdown/retry over
HTTP); hosts/docs/migrations are current (parity IDENTICAL both features, RELEASING runbook
consistent); `make check` + `make guard` green. `AV3-9.7` is UNBLOCKED.

LIVE-STORE AVAILABILITY: `POSTGRES_TEST_DSN`, `TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN` all
UNSET — the live conformance + livedelivery legs loud-SKIP and remain the standing open
owner gate; `AV3-9.7` was NOT started from this task (handoff only, per scope).
