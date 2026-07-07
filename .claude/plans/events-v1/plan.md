# events-v1 — Mount.Events, features/events (transactional outbox + SSE gateway), dual stores, proof host

Status: **DRAFT — tier-review gate PASSED 2026-07-06 (ship-with-edits, both
reviewers; edits applied below), awaiting jrazmi ratification.**
The gate (design §11 plan-cut requirement 1) ran with exactly the prompt
*"is any piece in the wrong tier, and is the host wiring tour acceptable?"*:
`architecture-steward` and `lead-backend-engineer` both confirmed tier
placement **clean throughout**, and the SSE-gateway-routes-in-
`features/events` placement (R9, design §6) was **consciously confirmed —
no reopening**. Their consolidated edits are applied and logged in "Gate
review amendments" below. jrazmi's independent post-gate review
(2026-07-07) returned findings P1–P5 — verified against live code and
applied, logged in "Post-gate review amendments" below.

Design of record: `.claude/plans/roadmap/events-feature-design.md` —
**RATIFIED 2026-07-02, O1–O8 all to their proposed defaults (R9)**, amended
2026-07-06 (sdk-parity early-landing of its phases 1–2; kvstore-consolidation
R-KV1; straddle-review plan-cut requirements). Nothing in that design is
re-decided here; this plan phases and operationalizes it. Milestone dir:
`.claude/plans/events-v1/`.

Executor model policy (jrazmi, standing since jobs-v1): implementation tasks
on `model: opus`; design/doc-judgment tasks on `model: fable`. Never sonnet.

## Gate review amendments (2026-07-06)

Both reviewers returned SHIP-WITH-EDITS; the eight consolidated edits below
are applied throughout (marked "gate edit N" where they land):

1. **EventID → SSE `id:` was unwireable as drafted** — `events.Event`/
   `Metadata` carry no EventID and `sdkevents.RemoteEvent` has none either;
   CorrelationID is not unique per event (same-request events share it), so
   it is the wrong de-dupe key for the durable rail. Fix: the poller emits a
   **feature-local** event type exposing `EventID() string` (and satisfying
   `Unmarshaler`); the hub sources `id:` by type-asserting the optional
   `interface{ EventID() string }`; best-effort events fall back to
   CorrelationID with **no de-dupe guarantee**, documented. `sdk/events`
   stays frozen. (tasks 4, 5, 14)
2. **Durable-demo promptness was false as drafted** — `WakeChannel(bus,
   topic)` fires only on bus emits; the demo append never emits, so pickup
   would wait out the idle interval. Fix: the canonical **append-then-
   signal** pattern — a host-owned cap-1 wake channel, non-blocking send
   after `Append`, passed via `workers.WithWakeChannel`. (task-12; protocol
   step 5)
3. **Compile fix** — `sdkevents.WakeChannel` returns
   `(<-chan struct{}, Subscription, error)`; corrected wherever shown, with
   `Unsubscribe` slotted into the shutdown order. (tasks 4, 12, 14)
4. **Wiring-page verify made satisfiable** — the page's complete `main.go`
   IS the outboxmem twin; the store-module swap is an explicit snippet; stop
   4 verifies as a port-equivalent substitution. (task-14)
5. **storetest relocated to phase 2** (it needs only task-3's port), making
   "phase 5 does not depend on phase 4" literally true. Renumbering:
   storetest = task-6; cms emitter / host wiring = tasks 7/8; G5 guard /
   README / docs sync = tasks 13/14/15.
6. **`poller.go` vs `events.go` reconciled** — `poller.go` stands, logged
   as a refinement of design §12 item 4 (same root package; file split
   only, host-facing surface unchanged). (phase-2 preamble, task-4)
7. **task-1 also updates `sdk/feature`'s package doc** — the "carries only
   stdlib types plus sdk/web" sentence goes stale when the sdk/events
   import lands.
8. **New G5 guard** (rule 6: no feature-core → feature-core imports — G2
   does not catch it, and this is the first milestone built around
   cross-feature flow) lands as task-13, prove-can-fail, with rule-6 greps
   added to the boundary-creating tasks' verifies (5, 7). **Additive scope
   beyond the design — jrazmi may strike it at ratification**; if struck,
   the DoD names feature→feature isolation as manually-checked-not-guarded,
   alongside the appender seam.

## Post-gate review amendments (jrazmi, 2026-07-07)

jrazmi's independent post-gate review returned four findings plus one
decided open question — each verified against live code before landing.
Applied throughout, marked "post-gate edit PN" at landing sites. Status
stays DRAFT awaiting ratification.

- **P1 (HIGH) — the poller could falsely mark rows published.** Verified:
  `Memory.Emit`'s async default returns nil even when the bounded queue
  DROPS the event (memory.go: non-blocking select → warnDropped("queue
  full") → nil), and goredis's async Emit is fire-and-forget XADD on a
  tracked goroutine — so async publish-then-mark was NOT at-least-once as
  drafted: a dropped/failed emit still got marked published, silently
  losing durable events. Fix: the poller emits with `sdkevents.WithSync()`
  and never marks on emit error; new stub-bus test required. (task-4)
- **P2 (MEDIUM) — the cache-invalidation run-and-look was racy.** cms
  emits stay async (O3, ratified — emitter latency must not be hostage to
  subscribers), so asserting "X-Cache MISS right after the edit" claimed a
  synchronous guarantee the semantics don't provide. Fix: bounded-poll
  wording (retry ≤ ~2s). Sync cms emits were the rejected alternative —
  that would re-decide O3 and contradict §3's re-fetch-trigger semantics.
  (task-8)
- **P3 (MEDIUM) — shutdown drained with a canceled context.** Verified:
  `web.Run(ctx, …)` blocks until ctx cancellation and drains HTTP on its
  OWN fresh Background+ShutdownTimeout context (run.go:30–34) — so after
  it returns, the parent ctx is already canceled, and `bus.Close(ctx)` on
  it drains nothing (`Memory.Close` drains "up to the context deadline";
  canceled ctx = zero drain). Fix: fresh bounded contexts for pool stop
  and `bus.Close`, and the poller pool on its own context canceled only
  AFTER HTTP shutdown completes. (tasks 8, 12; task-14's listing)
- **P4 (MEDIUM) — resource-scoped streams had no delivery-filter
  contract.** As drafted, a "scoped" stream could legally deliver
  everything. Fix: a resource-scoped connection delivers only events whose
  `Metadata` matches the path's (resource_type, resource_id); events
  carrying no `Metadata` are suppressed on scoped streams —
  deny-by-default, consistent with the metadata-only projection posture.
  Tests added. (task-5)
- **P5 — `MaxConnAge` semantics decided (design micro-amendment, jrazmi
  confirms at ratification).** Design §6 says both "0 → 15m" and (O7)
  "hosts can set 0 explicitly" — contradictory on a plain `time.Duration`.
  Decision: **no-disable in v1** — zero value → 15m, unlimited NOT
  offered; effectively-unlimited = an explicitly large value (e.g. 8760h);
  a negative sentinel is the documented future seam if a real unlimited
  need appears. Rationale: O7's own argument is that bounded conn age IS
  the security posture (cheap forced reconnects by construction); offering
  a disable undercuts it, and the repo has no feature-Config sentinel
  precedent (`sse.go`'s "0 disables" heartbeat is the primitive tier, not
  the feature Config tier). O7's "set 0 explicitly" sentence is superseded
  — recorded in the design-header amendment. (task-5; task-15; open
  question 4)

## Context

Ratified capability-map call #4 deferred the event bus until a second real
multi-feature consumer existed; the events design is that consumer's design
(cms = first emitter, the SSE gateway = the multi-feature consumer, a
host-side cache-invalidation subscriber = the concrete third). The design's
phases 1–2 (sdk/web SSE primitives; `sdk/events` + `eventstest`) **landed
early in sdk-parity**, and `integrations/kvstores/goredis` carries the Redis
Streams Bus already (built early, R-KV1) — so events-v1 **resumes at
design-phase 3** (`Mount.Events`) and delivers design-phases 3–8: the Mount
field, the `features/events` core (outbox domain + host-driven poller + SSE
gateway), the cms best-effort emitter, both store modules, the proof host,
and docs including the mandated wiring-tour page.

## Verified current state (2026-07-06 — read before trusting this plan)

Everything below was checked in code while cutting this plan:

- **`sdk/events` is complete to design §2** (sdk-parity D-9): `Event`,
  `Metadata`, `BaseEvent`, `Handler`, `Subscription`, `Emitter`, `Bus`,
  `Broadcaster`, `TypedHandler[T]`, `Unmarshaler`, `EmitOption`/`WithSync`/
  `ApplyOptions`, `EncodeEvent`, `Record`/`NewRecord`, `RemoteEvent`,
  `DecodeRemoteMetadata` (`sdk/events/events.go`, `record.go`),
  `WakeChannel(bus, topic) (<-chan struct{}, Subscription, error)`
  (`wake.go`), `Memory` (async + `WithSync`, satisfies `Broadcaster`),
  `Noop`, and `eventstest.Run(t, newBus)` with a Memory conformance run.
- **`sdk/web` SSE primitives are complete to design §1's finding**:
  `SSEEvent`, `SSEStream`/`NewSSEStream`, `WithHeartbeat`, the
  `http.ResponseController` per-write `SetWriteDeadline` extension
  (`sse.go:79`), `StreamWriter`, `AcceptsStream`, heartbeat + long-stream
  tests.
- **`sdk/workers` ships everything §5 requires**: `ErrNoWork`
  (`errors.go`), `WithWakeChannel` (`pool.go:90`), `NewPool`, panic
  recovery in pool and runner, graceful context-bounded stop.
- **`integrations/kvstores/goredis`** asserts `_ events.Bus` and
  `_ events.Broadcaster` on its `Bus` (`bus.go:35–36`) — the multi-instance
  backend exists; v1 hosts still wire `events.Memory` (design §9 non-goal
  unchanged: no multi-instance host yet).
- **`integrations/datastores/pgxdb` exists** (module `pgxdb`, package `pgx`,
  imported by stores under the alias `pgxdb`): `DB`, `Tx`, `InTx`, `Open`,
  `StatusCheck`, `migrate.go`. `features/jobs/stores/pgx` is the connector-
  and-convention template (`Repositories(db *pgxdb.DB, ...)`,
  `ExportMigrations(dst)`, README, env-gated conformance).
- **`features/auth` provides the exact §6 shapes**: `Service.RequireUser`
  (web middleware) and `Service.CurrentUser(ctx) (userID string, ok bool)`
  (`auth.go:151, :162`) — structurally satisfies the gateway's
  consumer-declared `CurrentUser` port with zero imports.
- **`feature.Mount` is `{Router RouteRegistrar, Logger *slog.Logger}`** —
  see supersession S3 below (the design's §4 snippet shows a `Migrations`
  field that does not exist in code).
- **cms**: `entrysvc.NewService(entries, registry, clock)` built inside
  `cms.Register`; write methods `Create/Edit/Publish/Unpublish/Delete/
  SetTerms`; public-page caching is `web.CachePages` with key prefix
  **`page:`** (`sdk/web/cache.go:24`), and `cacher.Storer` carries
  `DeletePattern` on the port.
- **`examples/auth-cms`** already mounts auth + cms on in-memory stores
  (`internal/authmem`, `internal/memstore`), wires `cacher.NewMemory()`
  into `cms.Config.Cache`, gates admin via `authSvc.RequireUser`, has zero
  datastore drivers in its graph (go.mod: sdk + both features + bcrypt
  only), serves on :8082 with `WriteTimeout: 15s` (the SSE write-deadline
  extension is load-bearing).
- **Makefile `MODULES` = 26 and matches `go.work` exactly**;
  `STORE_MODULES` = 6; guard G2's regex is generic over `features/*`
  (A4-generalized — new features are covered with **no guard edit**). The
  repo is **not** a git repo: `make check` uses the checksum fallback for
  templ drift, and "reversible task" means every task boundary leaves all
  modules building.
- Trio layout confirmed live in `features/auth` and `features/jobs`:
  `logic/<domain>` (public rim), `internal/logic/<svc>`,
  `internal/inbound/http`, `storetest/` (+ `reference_test.go` running the
  suite hermetically), `stores/{turso,pgx}` with `migrations/` dirs.

## Supersessions and design deltas (newer ratification wins — logged, not relitigated)

- **S1 — store naming (R-KV2/R-KV3, 2026-07-06, NOTES.md):** the design's
  `features/events/stores/postgres` is **`features/events/stores/pgx`**
  (package `pgx`, connector alias `pgxdb`), consuming
  `integrations/datastores/pgxdb` — per the `features/jobs/stores/pgx`
  conventions. `stores/turso` keeps its name. The design's precondition
  "`integrations/datastores/postgres` must exist" is satisfied by `pgxdb`.
- **S2 — redis integration (R-KV1, already amended in the design header):**
  `integrations/events/redis` → built early as `integrations/kvstores/
  goredis`. Nothing in this milestone touches it; §9's memory-bus-only v1
  deployment shape stands.
- **S3 — Mount snippet drift (verified in code):** design §4's snippet
  shows `Mount{Router, Migrations, Logger, Events}`; the live `Mount` is
  `{Router, Logger}` — migration registration moved to scaffold-and-own
  (`ExportMigrations` + host-owned pre-boot application, kvstore-
  consolidation correction). `Mount.Events` is therefore the **third**
  field on a two-field struct; the capability and its emit-only/nil
  semantics are exactly as ratified. No `MigrationRegistrar` is added.
- **S4 — cache-key prefix (verified in code):** the design's illustrative
  `cache.DeletePattern("public:*")` is `cache.DeletePattern(ctx, "page:*")`
  in this repo (`web.CachePages` keys pages as `"page:" + RequestURI`).
- **S5 — O6 corollary spelled out:** §7's "subscriber on `content.*`" is
  implemented as `Subscribe("*")` + a `strings.HasPrefix(e.Type(),
  "content.")` filter inside the handler — topic matching is exact + `"*"`
  only (O6, ratified). No prefix routing gets built.
- **S6 — in-memory outbox placement confirmed by R3:** "simple features
  (cms, auth, **events-outbox**) keep the test-scoped reference +
  example-local memstores" — so the hermetic reference lives in
  `features/events/storetest/reference_test.go` and the runnable in-memory
  store is example-local (`examples/auth-cms/internal/outboxmem`). No
  `stores/memory` module, no in-core public memstore.

## Phase map (this plan ↔ design §11)

| plan phase | design §11 | what | size | depends on | modules after |
|---|---|---|---|---|---|
| — | 1 | sdk/web SSE primitives | — | **DONE** (sdk-parity, verified above) | — |
| — | 2 | `sdk/events` + `eventstest` | — | **DONE** (sdk-parity, verified above) | — |
| 1 | 3 | `Mount.Events` + sdk/feature tests + charter C3 cash-in | S | — | 26 |
| 2 | 4 | `features/events` core: `logic/outbox`, exported poller, gateway hub + HTTP, `Register`/`Repositories`/`Config`, `storetest` + hermetic reference (relocated here — gate edit 5) | L | 1 | **27** |
| 3 | 5 | cms emitter (best-effort, nil-guarded) + host bus + cache-invalidation subscriber in `examples/auth-cms` | S–M | 1 (hard), 2 (ordering) | 27 |
| 4 | 6 | `stores/turso` + `stores/pgx` (S1; the suite they execute is cut in phase 2 — gate edit 5) | L | 2 | **29** |
| 5 | 7 | proof host: extend `examples/auth-cms` — gateway mount, SSE end-to-end, in-memory-outbox second variant, the real-interaction check | M | 2, 3 | 29 |
| 6 | 8 | G5 rule-6 guard (gate edit 8) + docs sync: feature README + **wiring-tour page** (plan-cut requirement 2), counts, design-header amendment, NOTES entry | S | all | 29 |

Cut-time refinements (logged as refinements of the explicitly-rough §11,
not design changes): the proof host is **`examples/auth-cms` extended**, not
a new example — it already has auth (the gateway's identity + middleware),
cms (the emitter), a live `cacher.Memory` behind `cms.Config.Cache` (the
invalidation consumer is real, not staged), and zero drivers (charter §3's
zero-infra proof), and it avoids a 30th module. `storetest` is cut in
**phase 2** (gate edit 5 — it needs only task-3's port; design §11 grouped
it with the stores) and lives in the `features/events` module (no
module-count effect). Design sequencing "5 needs 3+4" is operationalized as:
phase 3 hard-depends only on phase 1 (`Mount.Events` is the emitter's whole
surface); it runs after phase 2 by default but may swap forward if phase 2
blocks. Phase 5 deliberately does **not** depend on phase 4: the proof host
runs memory bus + example-local outbox (§8's zero-infra proof) and never
imports a store module — with storetest in phase 2, that claim is now exact
(gate edit 5).

## Goal

A host can mount `features/events` next to auth + cms, watch a cms edit
arrive as a `content.updated` SSE frame on an authenticated `/events`
stream, and (in the second variant) watch a durable outbox record ride
append → poll → emit → SSE — with both dialect stores conformant and no
feature importing any other feature.

## Definition of Done

- `feature.Mount` carries `Events events.Emitter` (emit-only, nil = silent
  no-op) with the §3 guarantee language in its doc; charter §6 C3's
  event-bus candidate is marked cashed.
- `features/events` (module 27) compiles standalone with `go.mod` = sdk
  only; `Register` errors on nil `Bus`/`Identity`; routes claim `/events/*`;
  hub is internal, poller is exported and host-driven.
- cms emits `content.published`/`content.updated`/`content.deleted` via
  `mount.Events` behind a nil guard — best-effort path only; **no cms store
  or port contract changes** (O2: no v1 feature wires the outbox).
- `features/events/storetest` green hermetically in `make check` (reference
  implementation) and against both stores' live legs (turso playground /
  dockered postgres) with dated NOTES.md artifacts; `AppendTx` tested
  per-store against its own integration; boot-time table probe present.
- The phase-5 real-interaction protocol passed and recorded: `curl -N` on
  `/events` (authenticated) receives the `content.updated` frame while a
  cms entry is edited in another session; the second variant proves
  outbox → poller (workers pool + wake channel) → bus → SSE; shutdown order
  HTTP → poller → `bus.Close` observed clean. Green tests alone close
  nothing here.
- Docs synced (module count 29 everywhere; `features/events/README.md` with
  the per-capability **wiring tour**: one diagram + one complete `main.go`
  spanning bus → Mount.Events → gateway+poller → store module → workers
  pool); full `make check` green (29 modules, all guards — five with
  task-13's G5).
- G5 (no feature-core → feature-core imports) added to `make guard` and
  proven able to fail (task-13, gate edit 8) — **or**, if jrazmi strikes it
  at ratification, feature→feature isolation is explicitly named in the
  NOTES entry as manually-checked-not-guarded, alongside the appender seam.

## Out of scope (design §9 + O2, restated as cut lines)

- Wiring any v1 host onto the goredis bus (memory bus only; single-process
  SSE; the hub's single-instance warning path is the proven seam).
- cms outbox mode: no `OutboxAppender` port in any cms store, no
  `[]events.Record` on cms write inputs — that contract change lands with
  the first durable emitter (auth v2 per §7), possibly never for cms.
- ReBAC / fine-grained stream authorization; tenancy behavior (vocabulary
  fields only); prefix topic routing / `EventRegistry`; `WithPriority`;
  durable subscriptions, replay, event-sourcing; webhooks; multi-poller
  claiming (`FOR UPDATE SKIP LOCKED`); the `sdk/repository`→`sdk/crud`
  Transactor seam (owned by `roadmap/datastore-portability.md`, revisit
  trigger = third durable emitter).

## Schema / datastore impact

- **New table, new migration source `"events"`** (unique vs
  `"cms"`/`"auth"`/`"jobs"`): the original's `0004_event_outbox.sql` shape —
  `event_id` PK (de-dupe key), `event_type`, `occurred_at`,
  `correlation_id`, `payload`, `aggregate_type`/`aggregate_id`/`tenant_id`
  (nullable), `created_at`, `published_at` (nullable; nil = unpublished),
  partial index on unpublished. Turso: `TEXT`/`INTEGER`; pgx:
  `TIMESTAMPTZ`, payload as **JSON not JSONB by default** (the jobs-v1
  precedent: byte-exact round-trip for an opaque column; deviation from the
  design's illustrative JSONB — implementer logs it if kept, flags if
  reversed). Identical migration version sets across both store trees
  (kvstore-consolidation vocabulary rule).
- **No changes to cms/auth/jobs schemas or the EAV spine.** Store parity:
  turso + pgx + the test-scoped reference + example-local `outboxmem` all
  run one `storetest` suite.
- Cross-source ordering hazard (§5, risk 2): the ledger expresses no
  ordering between sources — mitigated by the boot-time probe in each
  store's constructor plus the documented prerequisite in store READMEs.
- Hosts apply migrations pre-boot via scaffold-and-own (`ExportMigrations`);
  the framework never migrates at startup (D4 — unchanged).

## Module / API impact

- **+3 modules, 26 → 29**: `features/events` (phase 2),
  `features/events/stores/turso` + `features/events/stores/pgx` (phase 4).
  Each: own `go.mod` on the sibling-replace pattern, registered in `go.work`
  + Makefile `MODULES` in its phase; the store modules also join
  `STORE_MODULES` (6 → 8) and gain `test-stores` legs (pgx plain, turso
  `-tags=integration`).
- **`sdk/feature` grows one field** (`Events events.Emitter`) — compatible
  pre-v1 (named-field construction, charter §6); the new import edge
  `sdk/feature → sdk/events` is sdk-internal, same class as the existing
  `sdk/web` edge. G1/G3 unaffected.
- cms's `entrysvc.NewService` signature changes — **internal package**, not
  public API (charter B3); `cms.Register`'s signature is untouched.
- `examples/auth-cms/go.mod` gains `features/events` (+ replace).
- No tags have ever been cut (RELEASING.md), so no version-bump obligation;
  RELEASING's module enumeration updates in phase 6.
- Package-name collision (O5, ratified keep-and-alias): `features/events`
  is package `events`, colliding with `sdk/events` — feature files and
  hosts alias `sdkevents "github.com/gopernicus/gopernicus/sdk/events"`;
  the feature README documents it.

## Generated-artifact impact

None. The v1 surface is JSON/SSE — no `.templ` sources are touched anywhere
in this milestone. `make check`'s templ-drift gate (checksum fallback; not a
git repo) still runs every phase; never hand-edit `*_templ.go`.

## Verification norms (standing, every phase)

- Phase gate: `make check` (all modules incl. this milestone's additions:
  27 from phase 2, 29 from phase 4) + all guards (four today; five once
  task-13's G5 lands). `go.work` ↔ Makefile `MODULES` agreement is part of
  each module-adding task's verify.
- Store suites: hermetic always (`storetest` reference runs inside
  `make check`; store modules' live conformance skips LOUDLY without env).
  Live legs: turso `-tags=integration` against the authorized playground DB
  only (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
  — verify the env URL before any run); pgx env-gated on
  `POSTGRES_TEST_DSN` (docker postgres:17). Milestone close requires one
  recorded live run per store as dated NOTES.md artifacts.
- User-facing phases (3, 5) end with run-and-look checks; phase 5's
  protocol is mandatory and recorded verbatim (commands, ports, observed
  frames).

## Risks (ordered)

1. **The two-emit-paths asymmetry** (design §3, its risk 1): a feature
   author assuming `Mount.Events.Emit` is transactional ships a silent
   durability bug. Mitigation: the guarantee language lives in the
   `Mount.Events` field doc (task-1), the `Emitter` doc already carries it,
   the charter update names both paths (task-2), and the feature README
   reprints the §3 table (task-14).
2. **Cross-source migration ordering** (design §5, its risk 2): an
   appender-wired host that scaffolds `"cms"` but not `"events"` fails at
   runtime. Mitigation pair, both required: boot-time table probe in each
   store constructor (tasks 9/10) + documented prerequisite in both store
   READMEs. Residual: hosts that skip both — v1 exposure is near zero since
   no feature wires the appender.
3. **Unguarded appender seam** (design §5 cost 1, O8): `AppendTx` ships as
   per-store glue no `make guard` target covers. Contained in v1 (zero
   emitters wired); the Transactor revisit trigger (third durable emitter)
   is already lodged with the portability plan (R5). Phase 6 notes the
   unguarded seam in the feature README + NOTES.
4. **SSE through the auth middleware stack**: `RequireUser` + Logger/Panics
   wrap a long-lived streaming response — the sdk-parity port proved
   `statusWriter.Unwrap` keeps `ResponseController` flush/deadline access
   through middleware, and auth-cms's 15s `WriteTimeout` makes the
   per-write deadline extension load-bearing. Task-5's tests must exercise
   the full middleware stack on an `httptest.Server`, and revocation
   latency stays bounded by `MaxConnAge` = 15m (O7).

---

## Phase 1 — `Mount.Events` (design-phase 3) — S

**DoD:** `feature.Mount` carries the emit-only port with ratified nil
semantics; charter C3's candidate is cashed; `make check` + guards green;
no host or feature behavior changes (zero-value field).

### task-1: add `Events events.Emitter` to `feature.Mount`

- **depends_on:** []
- **model:** opus
- **files:** [sdk/feature/feature.go, sdk/feature/feature_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`
- **description:** Add the one field per design §4 (as adjusted by S3 — the
  live Mount is `{Router, Logger}`): `Events events.Emitter` with a doc
  comment that states, verbatim in intent: emit-only; **best-effort
  at-most-once — never transactional, lost on crash between commit and
  emit**; the durable path rides `Repositories`, never this field; nil →
  the feature emits nothing (features nil-guard or wrap `events.Noop`,
  behavior identical). Tests: zero-value Mount keeps working (existing
  construction sites compile unchanged — `make check` proves all five
  hosts/features); a Mount carrying `events.NewMemory()` delivers an emit
  to a subscriber via `WithSync`. Also update the package doc (gate edit
  7): `feature.go`'s "It carries only stdlib types plus sdk/web (itself
  stdlib-only)" sentence must name sdk/events too. The
  `sdk/feature → sdk/events` import is sdk-internal; G1/G3 must stay green.

### task-2: charter C3 cash-in

- **depends_on:** [task-1]
- **model:** fable
- **files:** [features/README.md]
- **verify:** `make guard` (docs-only) and a read-back that §6 no longer lists the event bus as a candidate
- **description:** Update `features/README.md` §6 (C3): move the "event bus
  port" from candidate to built — `Mount.Events`, emit-only, added
  events-v1 the day cms's first emit call landed (C3's sanctioned process),
  with one sentence naming the two emit paths and their guarantees (§3) and
  pointing durable delivery at feature `Repositories`. Update the §1/§5
  `feature.Mount{Router, Logger}` wording to include `Events`. Surgical
  diff — this is a load-bearing document.

## Phase 2 — `features/events` core (design-phase 4) — L — module 27

**DoD:** the feature module compiles standalone (`go.mod` = sdk only,
charter item 2); `logic/outbox` public; poller exported and host-driven
(returns `workers.ErrNoWork` when idle); gateway hub internal with the §6
defaults; `Register` errors on nil `Bus`/`Identity`; routes `/events` and
`/events/{resource_type}/{resource_id}` (the latter only when `Authorize`
is set); the `storetest` suite (R4) green hermetically via its test-scoped
reference (gate edit 5 — relocated from the design's phase-6 grouping);
in-module tests green; `make check` green at **27 modules**.

Trio layout (plan-cut requirement 2 of the milestone brief, mirroring
`features/auth`): public port layer at `logic/outbox/`; hub internals under
`internal/logic/`; HTTP under `internal/inbound/http/`; host-facing surface
in the root package — `Register`/`Repositories`/`Config` in `events.go`,
the poller in `poller.go` (gate edit 6: a file split within the root
package, logged as a refinement of design §12 item 4's "host-facing
constructors live in `<name>.go`" — same public surface).

### task-3: module skeleton + `logic/outbox` + registration

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/events/go.mod, features/events/logic/outbox/outbox.go, features/events/logic/outbox/outbox_test.go, go.work, Makefile]
- **verify:** `cd features/events && go build ./... && go test ./... && go vet ./...` then `make check` (27 modules) and `make guard`
- **description:** Create module
  `github.com/gopernicus/gopernicus/features/events` (go 1.26.1; requires
  sdk only, sibling replace — `features/jobs/go.mod` is the template).
  `logic/outbox`: `Entry` (embeds `events.Record` + `CreatedAt`,
  `PublishedAt *time.Time`; nil = unpublished) and `EntryRepository`
  exactly per design §5 (`Append` non-transactional convenience,
  `ListUnpublished` ordered by CreatedAt ascending, `MarkPublished`,
  `PurgePublished`); port doc comments are the spec the storetest suite
  will execute — duplicate `Append` of an existing EventID returns
  `errs.ErrAlreadyExists`; `MarkPublished` is idempotent. Register the
  module in `go.work` and Makefile `MODULES` (alphabetical: after
  `features/cms/stores/turso`, before `features/jobs`). Alias
  `sdkevents` for `sdk/events` everywhere in this module (O5).

### task-4: the poller — exported, host-driven

- **depends_on:** [task-3]
- **model:** opus
- **files:** [features/events/poller.go, features/events/poller_test.go]
- **verify:** `cd features/events && go build ./... && go test -race ./... && go vet ./...` then `make guard`
- **description:** Port design §5's poller: `NewPoller(repo
  outbox.EntryRepository, bus sdkevents.Bus)` (design's stated signature;
  batch-size option with a sane default), `Poll(ctx) error` — read a batch
  of unpublished entries, `Emit` each as a **feature-local rehydrated event
  type** (gate edit 1: `sdkevents.RemoteEvent` carries no EventID, and
  CorrelationID is not unique per event — sdk/events stays frozen): a
  root-package type wrapping the `Record` that implements `sdkevents.Event`,
  satisfies `sdkevents.Unmarshaler` by carrying the payload (the
  `TypedHandler` slow path), and exposes `EventID() string` — the durable
  rail's de-dupe key, and what the hub reads for SSE `id:`.
  **Emit discipline (post-gate edit P1):** the poller MUST emit with
  `sdkevents.WithSync()` and, on emit error, return WITHOUT calling
  `MarkPublished` (the entry stays unpublished; the next poll retries) —
  verified: `Memory`'s async default returns nil even when the bounded
  queue drops the event, and goredis's async Emit is fire-and-forget XADD,
  so async publish-then-mark would silently lose durable events. Sync
  semantics to cite in the doc: `Memory` + `WithSync` returns the first
  handler error — a failing subscriber therefore also leaves the entry
  unpublished → redelivery, consistent with the idempotent-handler
  contract (document this); goredis + `WithSync` returns the XADD error
  properly. Closed-bus edge, stated explicitly: BOTH buses return nil
  (with a "dropped" warning) on `WithSync` against a CLOSED bus — safe
  only because the documented shutdown order stops the poller before
  `bus.Close`; the doc says so. Only after a successful emit:
  `MarkPublished` — **publish-then-mark = at-least-once** (duplicates
  possible on retry/poller crash; consumers de-dupe on `EventID()` — say
  so in the doc). Return `workers.ErrNoWork` when the batch is empty (the
  pool's idle trigger; hosts wake it via a cap-1 append-then-signal
  channel, or via `sdkevents.WakeChannel(bus, topic)` — which returns
  `(<-chan struct{}, Subscription, error)`; destructure, check the error,
  `Unsubscribe` at shutdown — gate edit 3). The poller owns NO goroutines
  and no lifecycle — the host drives it on an `sdk/workers` pool (D4
  philosophy; single-poller-per-outbox is the documented v1 assumption).
  Tests: fake repository + `Memory` bus — publish-then-mark ordering
  (sync emits per P1), **a stub bus whose Emit returns an error → the
  entry is NOT marked published (P1, required)**, mark failure leaves
  entry unpublished (duplicate emit on next poll, documented), ErrNoWork
  on empty, a subscriber sees `EventID()` and `TypedHandler` rehydrates
  via the Unmarshaler path, race-run.

### task-5: SSE gateway hub (internal) + HTTP + the feature socket

- **depends_on:** [task-3]
- **model:** opus
- **files:** [features/events/events.go, features/events/events_test.go, features/events/internal/logic/hub/hub.go, features/events/internal/logic/hub/hub_test.go, features/events/internal/inbound/http/routes.go, features/events/internal/inbound/http/routes_test.go]
- **verify:** `cd features/events && go build ./... && go test -race ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment (gate edit 8): `! grep -rn '"github.com/gopernicus/gopernicus/features/\(auth\|cms\|jobs\)' features/events/`
- **description:** Implement design §6 whole. Hub
  (`internal/logic/hub`): one per process; subscribes at `Register` —
  `SubscribeBroadcast` when the bus satisfies `Broadcaster`, else
  `Subscribe("*")` with a logged single-instance warning; per-connection
  buffered channels (default 64), **drop-on-full** with a sampled warning
  counter; per-subject connection cap (default 10); metadata-only
  projection `{type, occurred_at, aggregate_type, aggregate_id, tenant_id}`
  — raw payloads NEVER forwarded unless `Config.Projector` opts in; SSE
  `id:` sourced by type-asserting the optional
  `interface{ EventID() string }` (gate edit 1 — satisfied by the poller's
  rehydrated events: the durable rail's de-dupe key), **falling back to
  `CorrelationID` for best-effort events, documented explicitly as carrying
  no per-event de-dupe guarantee** (harmless — that path is a wake-up
  channel). HTTP (`internal/inbound/http`):
  `GET /events` (subject stream; `?types=a,b` exact-match allow-list — O6:
  no prefix patterns) and `GET /events/{resource_type}/{resource_id}`
  (registered ONLY when `Config.Authorize` is non-nil — deny by absence).
  **Resource-scoped delivery-filter contract (post-gate edit P4):** a
  scoped connection delivers ONLY events whose `Metadata` matches the
  path's (resource_type, resource_id) — `AggregateType`/`AggregateID`
  equality; events carrying no `Metadata` are SUPPRESSED on scoped streams
  (deny-by-default, consistent with the metadata-only projection posture).
  Handlers call `cfg.Identity.CurrentUser(ctx)`, 401 when absent; streams
  ride `web.NewSSEStream` with `Heartbeat` (default 25s) and `MaxConnAge`.
  **`MaxConnAge` semantics (post-gate edit P5, design micro-amendment):**
  plain `time.Duration`; zero value → **15m** (O7's posture, deliberately
  inverting the original); **unlimited NOT offered in v1** — a host
  wanting effectively-unlimited sets an explicitly large value (e.g.
  8760h); a negative sentinel is the documented future seam. O7's "hosts
  can set 0 explicitly" is superseded (P5). Socket
  (`events.go`): `CurrentUser` port, `AuthorizeStream` func type,
  `Repositories{Outbox outbox.EntryRepository}` (nil → direct-emit mode
  documented), `Config` exactly per §6 (`Bus`, `Identity`,
  `StreamMiddleware`, `Authorize`, `Projector`, `Heartbeat`, `BufferSize`,
  `MaxConnAge`, `MaxConnsPerSubject`), `Register` hard-erroring on nil
  `Bus`/`Identity` (auth's `ErrHasherRequired` precedent — exported error
  vars). Tests: register on a recording router; httptest end-to-end —
  emit on the bus → frame on the response (id + metadata-only body);
  types filter; per-subject cap (11th connection rejected); Projector
  override; nil-Authorize ⇒ resource route absent; nil Bus/Identity ⇒
  errors; **resource-scoped (P4): matching event delivered, non-matching
  suppressed, no-Metadata event suppressed, Authorize-denied connection
  rejected**. No `init()`, no package-level state (checklist item 8).

### task-6: `features/events/storetest` + hermetic reference (relocated — gate edit 5)

- **depends_on:** [task-3]
- **model:** opus
- **files:** [features/events/storetest/storetest.go, features/events/storetest/reference_test.go]
- **verify:** `cd features/events && go build ./... && go test ./... && go vet ./...` then `make check`
- **description:** `storetest.Run(t, newRepo func(t *testing.T)
  outbox.EntryRepository)` (one port set — follow the cms/jobs suite shape)
  asserting design §8's contract: append + list-order (CreatedAt asc);
  unpublished-only listing; mark-published idempotence; purge-published
  retention; **EventID uniqueness** (duplicate `Append` →
  `errs.ErrAlreadyExists`). `reference_test.go` carries a test-scoped
  in-memory `EntryRepository` (R3/S6 — memstore-honest: it enforces
  uniqueness and the suite proves it, the phase-2-W7 lesson) and runs the
  suite hermetically on every `make check`. The dialect-typed `AppendTx`
  is deliberately NOT in the suite (it takes a dialect Tx; each store
  tests its own — design §8). Relocated from the design's phase-6 grouping:
  it needs only task-3's port, and landing it here makes phase 5
  independent of phase 4.

## Phase 3 — cms emitter + host consumers (design-phase 5) — S–M

**DoD:** `entrysvc` emits `content.*` post-write via the mount's emitter
behind a nil guard — zero port/store changes (best-effort only, §3/O2);
`examples/auth-cms` carries the shared bus and invalidates its public page
cache on content events; run-and-look passed.

### task-7: cms core — nil-guarded emits from `entrysvc`

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/cms/internal/logic/entrysvc/events.go, features/cms/internal/logic/entrysvc/service.go, features/cms/internal/logic/entrysvc/service_test.go, features/cms/cms.go]
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment (gate edit 8): `! grep -rn '"github.com/gopernicus/gopernicus/features/\(auth\|events\|jobs\)' features/cms/ --exclude-dir=stores`
- **description:** Define cms-internal typed events (no shared struct
  crosses the feature boundary — §4 rule-6 note) embedding
  `sdkevents.BaseEvent` with aggregate metadata (`aggregate_type:
  "entry"`, `aggregate_id`: the entry ID). Extend `entrysvc` with an
  `sdkevents.Emitter` collaborator (internal package — signature change is
  free; nil ⇒ no emits, guard or `Noop`-wrap): emit **after the domain
  write returns** (best-effort path, §3 — never inside/around a repository
  call): `Publish` → `content.published`; `Create`/`Edit`/`Unpublish`/
  `SetTerms` → `content.updated`; `Delete` → `content.deleted` (the three
  ratified type names only). `cms.Register` passes `m.Events` into
  `entrysvc.NewService`. Tests: `Memory` bus with `WithSync` asserting
  each write path's event type + aggregate metadata; nil-emitter paths run
  unchanged (existing tests must pass unmodified); an emit error is logged
  at most, never returned to the caller (best-effort means the write
  already succeeded).

### task-8: host wiring — shared bus + cache-invalidation subscriber

- **depends_on:** [task-7]
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/main.go, examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check`; run-and-look (bounded-poll wording — post-gate edit P2): `go run ./cmd/server` (:8082), register+login, edit a seeded article via the admin UI, then reload its public page expecting fresh content + X-Cache MISS **within a short window (retry up to ~2s)** — cms emits are async (O3), so the admin response may return before the invalidation handler runs; in practice the in-process handler is near-instant, but the check must not assert a synchronous guarantee the ratified semantics don't provide. Then a second load → HIT. Previously the page was TTL-bound.
- **description:** Wiring only (rule 8): build one `bus :=
  sdkevents.NewMemory(...)`; hold the existing `cacher.NewMemory()` in a
  variable (it currently goes straight into `cms.Config.Cache`); set
  `Mount{Router, Logger, Events: bus}`; subscribe the host's
  cache-invalidation handler — `bus.Subscribe("*")` filtering
  `strings.HasPrefix(e.Type(), "content.")` in the handler (S5/O6), calling
  `cache.DeletePattern(ctx, "page:*")` (S4 — the real `web.CachePages`
  prefix). Rejected alternative, logged (P2): making cms emits synchronous
  would give a deterministic invalidation check but re-decides ratified O3
  and contradicts §3's re-fetch-trigger semantics — async stays. Shutdown
  (post-gate edit P3): `web.Run` returns only after the parent ctx is
  already canceled (it drains HTTP internally on its own fresh
  Background+ShutdownTimeout context — run.go), so closing the bus on the
  parent ctx would drain NOTHING. Use a fresh bounded context, explicitly
  this idiom: `closeCtx, cancel := context.WithTimeout(context.Background(),
  5*time.Second); defer cancel(); bus.Close(closeCtx)` — the §7 ordering
  comment goes in `main.go` now and gains the poller in phase 5. README:
  one paragraph on the bus + invalidation wiring.

## Phase 4 — both stores (design-phase 6, S1 naming) — L — modules 28–29

**DoD:** the phase-2 `storetest` suite (R4) executed by `stores/turso`
(live leg `-tags=integration`, playground DB) and by `stores/pgx` (live leg
`POSTGRES_TEST_DSN`); canonical migrations source `"events"` with identical
version sets across both trees; `AppendTx` per-store tested against its own
integration; boot-time probe in both constructors; `make check` green at
**29 modules**; live runs recorded as dated NOTES.md artifacts at milestone
close.

### task-9: `features/events/stores/turso` (module 28)

- **depends_on:** [task-6]
- **model:** opus
- **files:** [features/events/stores/turso/go.mod, features/events/stores/turso/turso.go, features/events/stores/turso/outbox.go, features/events/stores/turso/migrations/, features/events/stores/turso/conformance_test.go, features/events/stores/turso/appender_test.go, features/events/stores/turso/README.md, go.work, Makefile]
- **verify:** `cd features/events/stores/turso && go build ./... && go test ./... && go vet ./...` (hermetic: loud skip without TURSO_*) then `make check` (29 after task-10; go.work↔Makefile agreement) and `make guard`; live leg: verify the env URL is the authorized playground DB, then `TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -tags=integration ./...`
- **description:** Follow `features/jobs/stores/turso` conventions verbatim
  (module layout, `Repositories`/`New(db)` constructor shape,
  `ExportMigrations(dst)`, README). Canonical migrations (source
  `"events"`, schema per this plan's Schema section, turso dialect);
  `EntryRepository` implementation; `AppendTx(ctx context.Context, tx
  *tursodb.Tx, recs ...sdkevents.Record) error` — the dialect-typed
  transactional appender (§5; satisfied structurally by future emitting
  stores' consumer-declared ports; nothing consumes it in v1);
  **boot-time probe**: the constructor verifies the outbox table exists
  and errors before the host serves traffic (§5 mitigation b). Conformance:
  `storetest.Run` env-gated `-tags=integration` (loud skip); appender test
  inside the live leg (`InTx` → `AppendTx` → visible after commit, rolled
  back on error). README states the prerequisite loudly: wiring an
  appender requires the `events` source applied. Register module 28 in
  `go.work`, Makefile `MODULES`, `STORE_MODULES`, and a `test-stores`
  turso leg.

### task-10: `features/events/stores/pgx` (module 29)

- **depends_on:** [task-6]
- **model:** opus
- **files:** [features/events/stores/pgx/go.mod, features/events/stores/pgx/postgres.go, features/events/stores/pgx/outbox.go, features/events/stores/pgx/migrations/, features/events/stores/pgx/conformance_test.go, features/events/stores/pgx/appender_test.go, features/events/stores/pgx/README.md, go.work, Makefile]
- **verify:** `cd features/events/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic: loud skip without POSTGRES_TEST_DSN) then `make check` (29 modules) and `make guard`; live leg: `docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable' go test ./...`
- **description:** The pgx pair (S1): package `pgx`, connector
  `integrations/datastores/pgxdb` under the `pgxdb` alias —
  `features/jobs/stores/pgx` is the template. Same surface as task-9 over
  `*pgxdb.Tx`; migration **filenames/versions identical to the turso
  tree's**; `TIMESTAMPTZ` time columns, payload JSON-by-default (Schema
  section; log the JSONB deviation either way); partial index `WHERE
  published_at IS NULL`; boot probe; env-gated conformance + appender
  tests. Register module 29 in `go.work`, Makefile `MODULES`,
  `STORE_MODULES`, and a `test-stores` pgx leg. With task-9, both store
  trees pass ONE suite — record both live runs for the milestone-close
  NOTES artifacts.

## Phase 5 — proof host (design-phase 7) — M

**DoD:** `examples/auth-cms` mounts the gateway (default: `Outbox: nil`,
best-effort — O2) and a flag-selected second variant proving the durable
rail on the example-local in-memory outbox; the real-interaction protocol
below passed and recorded verbatim. **Green tests alone do not close this
phase.**

**Phase gate — the real-interaction protocol (design §11 phase 7, verbatim
in intent):**
1. `go run ./cmd/server` (:8082, default variant). Register + login with a
   cookie jar: `curl -c /tmp/jar -b /tmp/jar` through the auth-cms README
   flow.
2. `curl -N -b /tmp/jar http://localhost:8082/events` — stream opens;
   heartbeat comment frames arrive (~25s cadence).
3. In another session, log in and **edit a seeded cms entry** via the admin
   UI → the **`content.updated` frame arrives on the open stream** (SSE
   `id:` present — CorrelationID on this best-effort path, gate edit 1 —
   metadata-only body). Reload the public page: fresh content (phase-3
   invalidation).
4. Unauthenticated `curl -N http://localhost:8082/events` → 401/redirect
   per `RequireUser`.
5. Restart with `EVENTS_OUTBOX=memory`; `curl -N` the stream; POST the
   host's demo append → the handler appends then signals the host-owned
   wake channel (gate edit 2) → the frame arrives **via the poller
   promptly** (sub-second — observably NOT the idle interval), with `id:` =
   the record's EventID.
6. Ctrl-C → logs show the documented shutdown order (HTTP server → poller
   pool → `bus.Close`), exit clean, port free.
Record exact commands, ports, and observed frames in the execution log.

### task-11: mount the gateway (default variant, best-effort)

- **depends_on:** [task-5, task-8]
- **model:** opus
- **files:** [examples/auth-cms/cmd/server/main.go, examples/auth-cms/go.mod, examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check`; run-and-look: protocol steps 1–4
- **description:** Add `features/events` to go.mod (+ sibling replace;
  the graph must stay driver-free — re-assert `go list -m all | grep -i
  libsql` empty, the host's own doc-comment claim). Wire (wiring only,
  rule 8): `eventsfeature.Register(mount, eventsfeature.Repositories{},
  eventsfeature.Config{Bus: bus, Identity: authSvc, StreamMiddleware:
  []web.Middleware{authSvc.RequireUser}})` — one bus instance flows to
  both `Mount.Events` and `Config.Bus` (§6's wiring note); `Outbox` nil =
  direct-emit mode; `Authorize` left nil (resource-scoped routes absent —
  deny by absence, documented in the README). README gains the stream
  section (route surface, auth requirement, curl examples).

### task-12: `outboxmem` + poller variant + shutdown ordering

- **depends_on:** [task-11, task-6]
- **model:** opus
- **files:** [examples/auth-cms/internal/outboxmem/outboxmem.go, examples/auth-cms/internal/outboxmem/outboxmem_test.go, examples/auth-cms/cmd/server/main.go, examples/auth-cms/README.md]
- **verify:** `cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...` then `make check`; run-and-look: protocol steps 5–6
- **description:** Example-local in-memory `outbox.EntryRepository`
  (R3/S6; mutex-backed, EventID-uniqueness-honest) whose tests run
  `features/events/storetest` (the auth-cms memstore precedent). Second
  variant behind `EVENTS_OUTBOX=memory`: wire `Repositories{Outbox:
  outboxmem}`, construct `events.NewPoller(outbox, bus)`, drive it on an
  `sdk/workers` pool woken by the **canonical append-then-signal pattern
  (gate edit 2)**: the host owns a dedicated `wake := make(chan struct{},
  1)`; the demo handler does a non-blocking send after `Append`; the pool
  takes `workers.WithWakeChannel(wake)`. (`WithIdleInterval(1s)` was the
  rejected simpler alternative — it would make the tour's fifth stop, wake
  wiring, a lie. The bus-fed `sdkevents.WakeChannel(bus, "*")` is NOT used
  here — it fires only on bus emits, so the demo append would never wake
  it, and the poller's own emits would self-wake it; where that variant IS
  shown (task-4 doc, task-14 listing) it uses the corrected three-return
  form with `Unsubscribe` in the shutdown order — gate edit 3.) The demo
  trigger is a small host-owned example-local `POST /outbox-demo` handler
  appending a `sdkevents.NewRecord`-built record (the jobs-minimal
  `/enqueue` precedent) — cms itself never touches the outbox (O2).
  Implement and document the §7 shutdown order in `main.go` with the
  corrected context idiom (post-gate edit P3): the poller pool runs on its
  OWN context, canceled only AFTER HTTP shutdown completes — never the
  same ctx that stops the server (`web.Run` returning means the parent ctx
  is already canceled and HTTP already drained on `run.go`'s own fresh
  context). Order: stop HTTP (closes SSE via request contexts) → cancel
  the pool's context + bounded pool stop (finish in-flight batch) →
  unsubscribe any bus subscriptions → `bus.Close` on a fresh bounded
  context (`closeCtx, cancel := context.WithTimeout(context.Background(),
  5*time.Second); defer cancel(); bus.Close(closeCtx)` — a canceled parent
  ctx would make `Memory.Close` drain nothing). Zero-infra proof stands:
  memory bus + in-memory outbox + poller + SSE over `go run`, no driver in
  the graph (charter §3).

## Phase 6 — G5 guard + docs sync + milestone close (design-phase 8) — S

**DoD:** G5 in `make guard` and proven able to fail (or struck by jrazmi —
see task-13's note); feature README with the wiring tour; module count 29
consistent everywhere; design status header amended; NOTES.md milestone
entry with the live-run artifacts; fresh full `make check` green.

### task-13: G5 — the rule-6 feature-isolation guard (gate edit 8)

- **depends_on:** [task-3]
- **model:** opus
- **files:** [Makefile]
- **verify:** `make guard` green on a clean tree; prove-can-fail (A4 practice): add a temporary `features/auth` import to a `features/events` file, observe G5 fail with its error message, remove it, re-run `make guard` green; then full `make check`
- **description:** Both gate reviewers pushed to close this gap now: G2
  catches feature-core → integrations/examples/stores but NOT feature-core
  → feature-core (rule 6), and events-v1 is the first milestone built
  entirely around cross-feature flow. Add `guard-feature-no-cross-feature`
  (G5) to the Makefile and the `guard` aggregate: for each `features/<x>`
  core (excluding its `stores/` subtree), grep for imports of
  `github.com/gopernicus/gopernicus/features/<y>` where y ≠ x; print
  nothing and exit 0 on a clean tree, loud error otherwise (match the
  existing guard targets' shape). **Note for ratification: this is
  additive scope beyond the design — jrazmi may strike it.** If struck,
  task-15's NOTES entry and the milestone DoD must name feature→feature
  isolation as manually-checked-not-guarded (the tasks 5/7 greps),
  alongside the appender seam.

### task-14: `features/events/README.md` + the wiring-tour page

- **depends_on:** [task-11, task-12]
- **model:** fable
- **files:** [features/events/README.md]
- **verify:** `make guard`; then the fresh-eyes pass (gate edit 4): stops 1–3 and 5 of the wiring tour verified **line-for-line** against `examples/auth-cms/cmd/server/main.go`; stop 4 verified as a port-equivalent substitution (outboxmem implements the same `outbox.EntryRepository`), with the store-module + migration step read against `features/events/stores/turso/README.md`; optionally paste the swap-variant listing into a scratch module and `go build` it once so "compiling" is verified, not asserted
- **description:** The feature README (auth/jobs READMEs are the shape):
  layout (trio), route surface `/events/*` (C1 claim; prefixable — JSON/SSE
  carries no HTML links), `Config` table with per-field nil semantics
  (checklist item 12; nil `Bus`/`Identity` = hard error), the **two-emit-
  paths guarantee table (§3) reprinted**, delivery guarantees per rail
  (memory at-most-once / outbox at-least-once — de-dupe on `EventID()` via
  the poller's rehydrated event type, durable rail only; best-effort frames
  carry CorrelationID as `id:` with no de-dupe guarantee — gate edit 1),
  single-poller assumption, `MaxConnAge` revocation posture, the `"events"`
  migration-source prerequisite + boot probe, the O5 aliasing note, and the
  unguarded-appender-seam note (risk 3). **Plus the mandated per-capability
  wiring page** (design §11 plan-cut requirement 2) as a top-level README
  section "Wiring: live updates end-to-end": ONE diagram (ascii) of the
  five stops — `sdk/events` bus → `Mount.Events` → `features/events`
  gateway + poller → a store module → `sdk/workers` pool — and ONE
  complete, compiling `main.go` listing. Per gate edit 4: **the listing IS
  the outboxmem twin** (the only variant `make check` actually compiles);
  the `stores/turso` swap (constructor + scaffold-and-own migration step)
  is shown as an explicit labeled snippet/diff, and the diagram labels
  stop 4 as the substitution point. The listing includes the shutdown
  order **with P3's corrected context idiom** (post-gate edit P3: the
  pool on its own post-HTTP context; `bus.Close` on a fresh bounded
  `context.WithTimeout(context.Background(), …)` — never the canceled
  parent ctx; it claims "complete, compiling", so it must show the real
  idiom) and, where it shows the bus-fed `WakeChannel` variant, the
  corrected three-return form (gate edit 3). `examples/auth-cms` is named
  as the executable twin (its second variant runs the same tour).

### task-15: repo docs sync + records

- **depends_on:** [task-13, task-14]
- **model:** fable
- **files:** [ARCHITECTURE.md, README.md, RELEASING.md, Makefile, sdk/README.md, features/README.md, .claude/plans/roadmap/events-feature-design.md, NOTES.md]
- **verify:** full `make check` (29 modules, all guards) then `grep -rn 'Twenty-six\|26 modules' ARCHITECTURE.md README.md RELEASING.md Makefile sdk/README.md` returns nothing unintentional
- **description:** (1) ARCHITECTURE.md: module tree + "Twenty-six modules
  today" → twenty-nine; add the events rows (feature + two stores); add
  G5 to the guard enumeration (or record the strike). (2) README.md +
  RELEASING.md enumerations → 29. (3) Makefile header comment count.
  (4) sdk/README: cross-reference `features/events` from the events row
  (the consumer now exists — closes the straddle entry's perception
  artifact). (5) features/README.md: checklist-trace/app-mapping
  touch-ups for events (design §12 is the source; C3 itself was task-2).
  (6) Design status header: phases 3–8 executed via
  `.claude/plans/events-v1/plan.md`, with this plan's supersession log
  (S1–S6), gate amendments, and the **P5 micro-amendment (post-gate edit
  P5): O7's "hosts can set 0 explicitly" sentence is superseded —
  `MaxConnAge` is no-disable in v1** (zero → 15m, unlimited not offered,
  negative sentinel = the documented future seam). (7) NOTES.md dated
  milestone entry: what shipped, the S1–S6 deltas + the eight gate edits +
  the P1–P5 post-gate amendments, both live-store artifacts
  (suite/dialect/DSN-class/result), the phase-5 protocol results verbatim,
  G5's landing (or strike + the manually-checked note), open flags for
  jrazmi. G2 itself needs no edit (generic regex — verified); G5 is
  task-13's.

## Sequencing

Phases run 1→6. Hard edges: task-1 before everything (the Mount field is
the milestone's keystone); task-3 before tasks 4/5/6/13; task-6 before
tasks 9/10/12; task-7 before task-8; task-5 + task-8 before task-11;
task-11 + task-6 before task-12; task-13 before task-15's final gate;
task-14 before task-15. Phase 3 hard-depends only on phase 1 and may swap
ahead of phase 2 if phase 2 blocks. Phase 4 (stores) and phase 5 (proof
host) are independent of each other — the proof host never imports a store
module (§8 zero-infra proof; storetest relocated to phase 2 per gate
edit 5) — but default order keeps store conformance ahead of the demo.
Phase 6 last. Not a git repo: every task boundary must leave all modules
building.

## Consultation notes

No lead consulted while cutting the original draft — deliberately: the
design carries a pre- and post-write `lead-backend-engineer` review
already, and the mandated tier-review gate on THIS document is exactly the
architecture-steward + lead-backend-engineer pass a pre-write consult
would duplicate. **The gate ran 2026-07-06** (ship-with-edits, both
reviewers); its findings are folded in as the eight gate edits (see "Gate
review amendments") — the load-bearing catches were the EventID/`id:`
wiring gap, the wake-channel promptness bug in the durable demo, and the
missing rule-6 guard. jrazmi's independent post-gate review (2026-07-07)
added P1–P5 (see "Post-gate review amendments") — the load-bearing catch
there was P1: async emit's nil-on-drop would have let the poller falsely
mark rows published, breaking the durable rail's at-least-once claim.

## Open questions

1. **Wiring-page placement** — default: a section of
   `features/events/README.md` (per-feature READMEs are the only visible
   docs convention; no `docs/` dir exists). If jrazmi prefers a standalone
   page (e.g. repo-root `docs/`), task-14 moves, content unchanged.
2. **pgx payload column** — default JSON (jobs-v1 precedent for opaque
   columns) vs the design's illustrative JSONB; task-10 logs whichever
   lands. Flag only if JSONB is wanted for future in-database querying.
3. **G5 guard (task-13)** — additive scope beyond the design, recommended
   by both gate reviewers; jrazmi confirms or strikes at ratification
   (gate edit 8 records the strike consequence).
4. **P5 `MaxConnAge` no-disable** — decided in the post-gate review as a
   design micro-amendment (zero → 15m, unlimited not offered in v1,
   negative sentinel = the future seam; O7's "set 0 explicitly" sentence
   superseded); jrazmi confirms at ratification.

## Recommended reviews

- **architecture-steward + lead-backend-engineer** — the tier-review gate
  pair: **ran 2026-07-06, ship-with-edits, all eight edits applied** (see
  "Gate review amendments"). Re-engage only if jrazmi's ratification
  changes scope (e.g. strikes task-13's G5).
- **product-manager** — scope: O2's ship-stores-wire-nothing value line;
  whether the auth-cms extension (vs a dedicated example) keeps the demo
  legible; six phases as the release grain.
- **data-integration-reviewer** — outbox SQL across both trees, storetest
  coverage vs the port docs, boot probe, reference/outboxmem honesty,
  migration version-set parity.
- **platform-sre** — shutdown ordering, migration phasing + the
  cross-source hazard, single-poller assumption, MaxConnAge posture,
  module registration hygiene (go.work/Makefile/STORE_MODULES/test-stores).
- **lead-frontend-engineer** — not needed in v1 (JSON/SSE surface only);
  re-engage if a later phase grows an admin live-update view.

## Notes

- Salvage sources (reference-only; re-type fresh, never copy import paths):
  `gopernicus-original/bridge/events/ssebridge/{bridge,hub}.go` (gateway),
  `infrastructure/events/poller` (poller),
  `workshop/migrations/primary/0004_event_outbox.sql` +
  `core/repositories/events/eventoutbox/` (outbox SQL/repo shapes).
  The sdk-side salvage is DONE (sdk-parity).
- Auth naming rule holds in every identifier and comment:
  authentication/authorization (authenticator/authorizer) — never
  abbreviated.
- Rule-6 spot check at milestone close:
  `grep -rn "features/\(auth\|cms\|jobs\)" features/events/` empty, and the
  reverse for `features/events` (cms's emitter uses only sdk types).
