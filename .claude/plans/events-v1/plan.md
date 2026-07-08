# events-v1 — Mount.Events, features/events (transactional outbox + SSE gateway), dual stores, proof host

Status: **RATIFIED 2026-07-07 (jrazmi) — at defaults.** Open questions 1–4
resolved at their recommended/decided defaults (wiring page in the feature
README; pgx payload JSON; G5 guard STANDS; P5 MaxConnAge no-disable
confirmed); amendments A-I1 + A-R1 ratified the same day (see below).
Pre-execution pre-flight still owed: the FS2 fold-in (task-11 sync note /
feature-standard W4) — first loop leg on this plan.
Tier-review gate PASSED 2026-07-06 (ship-with-edits, both
reviewers; edits applied below).
The gate (design §11 plan-cut requirement 1) ran with exactly the prompt
*"is any piece in the wrong tier, and is the host wiring tour acceptable?"*:
`architecture-steward` and `lead-backend-engineer` both confirmed tier
placement **clean throughout**, and the SSE-gateway-routes-in-
`features/events` placement (R9, design §6) was **consciously confirmed —
no reopening**. Their consolidated edits are applied and logged in "Gate
review amendments" below. jrazmi's independent post-gate review
(2026-07-07) returned findings P1–P5 — verified against live code and
applied, logged in "Post-gate review amendments" below.
Amendment A-I1 (`sdk/identity` graduation) **RATIFIED 2026-07-07
(jrazmi)** — edits E1–E8 applied through the body same day (marked
"A-I1 EN" at landing sites). Amendment A-R1 (rename `features/auth` →
`features/authentication`) **RATIFIED 2026-07-07 (jrazmi)** — lands as
task-0, phase 0; forward-looking paths and greps in this plan read
`features/authentication` accordingly (pre-rename verified-state prose
stays as written).

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

## Amendment A-I1 — `sdk/identity` graduation (RATIFIED 2026-07-07, jrazmi)

Origin: taxonomy discussion 2026-07-07 (jrazmi + Claude). The "foundational
features" question resolved to: a feature is foundational exactly to the
degree its **vocabulary** has graduated to sdk. events and jobs graduated
theirs on day one (`sdk/events`, `sdk/workers`); auth's identity-in-context
vocabulary is the one still sealed behind a private context key
(`features/auth/internal/logic/authsvc/context.go:5-10`, whose doc comment
says "It lives here (not sdk) by design" — that design note is what this
amendment supersedes, via the graduation process the charter §5 corollary
explicitly sanctions: "an identity-in-context convention" is its named
example of the one thing allowed to move from a feature into sdk).

### A-I1.1 The decision

Add **`sdk/identity`** — a vocabulary-only package (the `sdk/oauth`/`sdk/errs`
shape: no default implementation because there is nothing to implement — it
is shared vocabulary, not a facility with swappable backends):

- `Principal{Type, ID string}` — the effective caller, exactly the shape AV5
  pinned (today `authsvc.Principal`, re-exported as `auth.Principal`).
- `User = "user"` and `ServiceAccount = "service_account"` — the two
  well-known subject-type constants (auth's resolution vocabulary today,
  `authsvc/machine.go`; a future authorizer reads them unadapted — they
  already deliberately match the ReBAC Subject vocabulary).
- `WithPrincipal(ctx, p) context.Context` and
  `FromContext(ctx) (Principal, bool)` — the identity-in-context convention:
  unexported key, zero-valued (empty-ID) principal reports `false`.

Nothing else graduates. `RequireUser`/`RequirePrincipal` middleware stays in
`features/auth` (it needs the session/API-key stores — behavior, not
vocabulary). `PasswordHasher`, session semantics, and client-attribution
context (`clientInfo` — audit plumbing, not identity) all stay put. **No
authorization vocabulary** — auth's `Granter` is write-shaped and this plan's
`AuthorizeStream` is check-shaped; no convergence exists to graduate, and the
standing revisit trigger (shape convergence / third consumer) is unchanged.

Admission-policy trace (`sdk/README.md`): **plurality** — two real consumers
(cms admin gating consumes the middleware side today via
`Config.AdminMiddleware`; the events gateway reads the value on every stream
connect); **narrow + stable** — one two-field struct, two functions, two
constants, shape ratified AV5; **shared policy** — "identity rides the
request context; absent identity fails closed" is platform semantics, not
any feature's domain shape.

### A-I1.2 What it buys (why decide now, not post-v1)

Today a consuming feature needs a matched PAIR from one provider: the
middleware that stashes identity AND a port back to that provider's Service
to read it — because the context key is private. That pairing is exactly why
design §6 carries both `StreamMiddleware` and `Config.Identity`. With the
convention in sdk, the pair dissolves: middleware stashes
`identity.Principal`; any feature reads `identity.FromContext(ctx)` — no
`Identity` port, no per-consumer `CurrentUser` declarations, no
same-provider pairing constraint. Deciding before execution avoids building
`Config.Identity` in task-5 and deleting it a milestone later; nothing is
tagged, so the alias-based auth conformance below is compatibility-free.

### A-I1.3 New tasks (letter-suffixed — tasks 2–15 keep their numbers)

### task-1b: `sdk/identity` package

- **depends_on:** []
- **model:** opus
- **files:** [sdk/identity/identity.go, sdk/identity/identity_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard` (G1/G3: stdlib-only, zero-require go.mod unchanged)
- **description:** The package exactly as A-I1.1 scopes it, stdlib-only.
  Package doc carries: the AV5 lineage (one Principal shape, string subject
  pairs, no registry table); the fails-closed convention (a reader treating
  absent identity as anonymous-allowed is a bug — absence means deny/401);
  and the scope fence, verbatim in intent: *vocabulary only — middleware
  and credential resolution live with the credential owners
  (features/auth); authorization vocabulary is deliberately absent.* Tests:
  With/From round-trip; zero-value Principal reports false; absent value
  reports false; the constants' literal values (they are a wire-adjacent
  convention, locked by test).

### task-1c: auth conformance — one identity carrier, public API unchanged

- **depends_on:** [task-0, task-1b]
- **model:** opus
- **files:** [features/authentication/internal/logic/authsvc/context.go, features/authentication/internal/logic/authsvc/machine.go, features/authentication/internal/logic/authsvc/service.go, features/authentication/authentication.go, features/authentication/internal/logic/authsvc tests as touched]
- **verify:** `cd features/authentication && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`; run-and-look (real-interaction rule): `cd examples/auth-cms && go run ./cmd/server` — register/login via the README flow, confirm the cms admin gate still admits the session and an unauthenticated hit still redirects/401s (RequireUser path exercised end-to-end)
- **description:** `authsvc.Principal` becomes `= identity.Principal` (type
  alias) and the subject-type constants alias `identity.User`/
  `identity.ServiceAccount` — the public chain `auth.Principal =
  authsvc.Principal` is untouched, so hosts see zero API change.
  `RequireUser` stashes `identity.WithPrincipal(ctx,
  identity.Principal{Type: identity.User, ID: userID})`;
  `RequirePrincipal`/`RequireServiceAccount` stash their resolved Principal
  the same way; the private `userIDKey`/`principalKey` pair collapses into
  the one sdk carrier (`CurrentUser` reads `FromContext` and filters
  `Type == identity.User`). `CurrentUser`/`CurrentPrincipal` signatures and
  observable behavior are UNCHANGED — existing auth tests must pass
  unmodified (that is the conformance proof). `clientInfo` stays
  feature-private. Rewrite context.go's "It lives here (not sdk) by design"
  comment to cite this amendment as the superseding decision. NOTE: the
  feature-standard convergence pass (01-convergence A3) touches the same
  middleware region (the two hand-rolled response writers at
  service.go:757/766) — land A3 first or rebase this task over it;
  executor logs the order taken.

### A-I1.4 Edits applied at ratification (mechanical, enumerated)

- **E1 — task-5:** delete the consumer-declared `CurrentUser` port and
  `Config.Identity`. Handlers read `identity.FromContext(ctx)`; absent →
  401. `Register` hard-errors on nil `Bus` only. Stream subject key becomes
  the composite `Type + ":" + ID` (micro-decision: prevents user/machine ID
  collisions under the per-subject connection cap; with the shipped
  `StreamMiddleware: RequireUser` wiring only user principals appear, so
  behavior is unchanged in the proof host). **Posture change, amends the
  ratified degraded-mode matrix row "events `Config.Identity` nil → hard
  error"**: a host that wires no identity-stashing middleware now gets
  uniform 401s — the misconfiguration fails CLOSED at request time instead
  of loudly at construction (consistent with the deny-by-default stream
  posture; the README documents the StreamMiddleware requirement). Tests:
  the nil-Identity constructor-error test is replaced by a
  no-middleware → all-streams-401 test. task-5 gains `depends_on` task-1b.
- **E2 — task-11:** the wiring snippet drops `Identity: authSvc,`.
- **E3 — DoD (milestone + phase 2):** "`Register` errors on nil
  `Bus`/`Identity`" → "errors on nil `Bus`; absent identity fails closed
  (401) per A-I1 E1".
- **E4 — "Verified current state" auth bullet:** gains a supersession note —
  the §6 consumer-declared-port shape it verifies is retired by A-I1; what
  the gateway now needs from auth is only that its middleware stashes
  `identity.Principal` (task-1c).
- **E5 — task-14 (README):** the Config table loses the `Identity` row and
  gains the identity-in-context section: the sdk convention, the
  StreamMiddleware requirement, fails-closed semantics.
- **E6 — task-15 (docs sync) additions:** `sdk/README.md` gains the
  `identity` entry with the A-I1.1 admission trace; ARCHITECTURE.md's sdk
  package enumeration gains `identity`; `features/README.md` §5's corollary
  is marked CASHED for identity-in-context (the illustrative `CurrentUser`
  port stays as the general C2 pattern for domain-shaped needs); the
  degraded-mode matrix row (roadmap/00-intersections.md §2, events
  `Config.Identity`) and the §3 identity seam row gain dated AMENDED
  markers; the NOTES.md milestone entry records A-I1.
- **E7 — Module/API impact:** sdk gains one package (no module-count
  change); `features/auth`'s public API is unchanged (alias conformance);
  no new import edges outside sdk-internal ones.
- **E8 — Sequencing:** task-1b joins task-1 at the front (independent of
  it); task-1c after task-1b, before phase 5's proof-host work; task-5's
  new edge per E1.

### A-I1.5 Relation to the authentication/authorization split (jrazmi question, 2026-07-07)

Already split, by v2 design: `features/auth` contains zero authorization
decisions — AV4 deliberately kept the grant seam consumer-declared
(`Granter`, host-implemented) and ReBAC-decoupled, and the feature's other
surfaces (sessions, OAuth, machine identity, invitations, audit) are all
authentication or attribution. A future authorization implementation lands
as its OWN module — a feature if it owns tables (role/tuple stores), an
integration if it wraps a vendor's live API — never as growth inside
`features/auth`; `identity.Principal` is the vocabulary the two sides will
exchange (its subject pairs already match the ReBAC Subject shape). The
rename question this raised — `features/auth` → `features/authentication`
for symmetry with a future `features/authorization` — was DECIDED
2026-07-07 same session: see Amendment A-R1 immediately below.

## Amendment A-R1 — rename `features/auth` → `features/authentication` (RATIFIED 2026-07-07, jrazmi)

Companion to A-I1, same discussion: with authorization confirmed as a
future sibling module (A-I1.5), the feature pair must be unambiguous —
`features/authentication` today, `features/authorization` whenever a real
implementation is wanted — and the repo naming rule (authentication /
authorization, spelled out, never ambiguous) finally applies to the module
itself. Zero tags are cut (RELEASING.md), so path churn is free now and
dearer forever after. Lands as **task-0, phase 0** — before every other
task, so events-v1 is born against the new path.

Scope, verified by grep 2026-07-07 before ratification:

- **Module paths (three) + replaces:** `…/features/auth`,
  `…/features/auth/stores/turso`, `…/features/auth/stores/pgx` →
  `…/features/authentication{,/stores/turso,/stores/pgx}`; the two store
  modules' sibling `replace` directives; `examples/auth-cms/go.mod`'s
  require + replace lines.
- **External importers: `examples/auth-cms` ONLY** (six .go files across
  `cmd/server` and `internal/authmem`) — no other module imports the
  feature.
- **Root package `auth` → `authentication`** (Go dir=package convention),
  and the root file `auth.go` → `authentication.go` (charter: `<name>.go`
  is the host-facing surface). `examples/auth-cms` imports under the alias
  `auth "github.com/gopernicus/gopernicus/features/authentication"` so
  every `auth.` call site is unchanged (the O5 aliasing precedent).
  Interior names stay: `authsvc`, `authmem`, `authSvc` variables, the
  `examples/auth-cms` directory — the naming rule bans the ambiguous
  forms, not the stem, and interior churn buys nothing.
- **Registration surfaces:** go.work (three lines); Makefile `MODULES`,
  `STORE_MODULES`, the two `test-stores` legs (Makefile:49–56), and the
  FS1 guard's module list (Makefile:114 `for f in features/auth …`).
  G2/G5-style guards use regexes generic over `features/*` — no edit.
- **Migrations: nothing to do.** No `"auth"` source string exists in store
  code (verified — the ledger source is host-side vocabulary), no host
  ledger anywhere holds auth rows (`examples/auth-cms` is in-memory;
  `examples/cms`'s workshop tree is cms-only — verified), and migration
  filenames 0001–0011 are unchanged. Store READMEs' ledger-source wording
  says "authentication" going forward.
- **Docs sweep (live docs only; historical plans/NOTES stay as written):**
  ARCHITECTURE.md (tree, taxonomy, features section), features/README.md,
  sdk/README.md cross-references, README.md, RELEASING.md enumerations,
  the feature's own README + both store READMEs. In-flight plan files get
  dated sync notes rather than rewrites: `feature-standard/
  01-convergence.md`'s auth-path references; THIS plan's fold-in is
  already done (task file lists and verify greps read
  `features/authentication`; pre-rename verified-state prose stays).

### task-0: the rename (phase 0 — before every other task)

- **depends_on:** []
- **model:** opus
- **files:** [features/auth/** → features/authentication/** (root file → authentication.go), go.work, Makefile, examples/auth-cms/go.mod, examples/auth-cms/cmd/server/main.go, examples/auth-cms/cmd/server/demo.go, examples/auth-cms/cmd/server/membership.go, examples/auth-cms/internal/authmem/authmem.go, examples/auth-cms/internal/authmem/authmem_test.go, examples/auth-cms/internal/authmem/ports_v2.go, ARCHITECTURE.md, README.md, RELEASING.md, features/README.md, sdk/README.md, .claude/plans/feature-standard/01-convergence.md]
- **verify:** full `make check` (module count unchanged) and `make guard`; then `grep -rn 'features/auth' --include='*.go' --include='go.mod' . go.work Makefile | grep -v 'features/authentication'` returns nothing; docs grep the same way over ARCHITECTURE.md README.md RELEASING.md features/README.md sdk/README.md; run-and-look (real-interaction rule): `cd examples/auth-cms && go run ./cmd/server` — register/login via the README flow, admin gate admits the session, unauthenticated hit 401s/redirects
- **description:** Execute the scope list above verbatim: move the
  directory, rewrite the three module lines + replaces + importer import
  paths, rename the root package/file, alias the example back to `auth`,
  update the registration surfaces and live docs, drop the dated sync
  note into `01-convergence.md`. Pure mechanical churn — zero behavior
  change; existing tests pass unmodified everywhere.
  **COORDINATION (in-flight work, 2026-07-07):** feature-standard
  convergence execution opened this session and is touching
  `features/auth` files (NOTES.md entry same date; uncommitted working
  tree). Land or park that work FIRST — this rename is pure path churn
  and rebases trivially over content edits, not vice versa. Executor logs
  the order taken.

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
  [SUPERSEDED by A-I1 (RATIFIED 2026-07-07): the consumer-declared-port
  shape this bullet verifies is retired — what the gateway now needs from
  the authentication feature is only that its middleware stashes
  `identity.Principal` (task-1c). Path reads `features/authentication`
  post task-0 (A-R1).]
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
| 2 | 4 | `features/events` core: `logic/outbox`, exported poller, gateway hub + HTTP, `NewService`/`Service.Register`/`Repositories`/`Config` (FS2 fold-in), `storetest` + hermetic reference (relocated here — gate edit 5) | L | 1 | **27** |
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
  only; `NewService` errors on nil `Bus` (FS2 fold-in 2026-07-08:
  validation at construction, the jobs Phase-E precedent; the built
  `Service` mounts via `svc.Register(mount)`); absent identity fails closed —
  every stream 401s when no middleware stashed an `identity.Principal`
  (A-I1 E3); routes claim `/events/*`; hub is internal, poller is exported
  and host-driven.
- A-R1 landed first: `features/authentication` everywhere (task-0's greps
  clean); `sdk/identity` shipped with authentication-feature conformance
  proven by unmodified passing tests + the login run-and-look (tasks
  1b/1c).
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
- **sdk gains one package, `sdk/identity`** (A-I1 E7 — vocabulary only,
  stdlib-only, no module-count change); the authentication feature's
  public API is unchanged by conformance (type aliases, task-1c). A-R1
  changes three module PATHS (`features/authentication{,/stores/turso,
  /stores/pgx}`) and the root package name — one example updates its
  import lines under an `auth` alias; no API surface changes.
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
defaults; `NewService` errors on nil `Bus` (FS2 fold-in: construction-time
validation; `svc.Register(mount)` mounts), and absent identity fails closed
(401 on every stream — A-I1 E3); routes `/events` and
`/events/{resource_type}/{resource_id}` (the latter only when `Authorize`
is set); the `storetest` suite (R4) green hermetically via its test-scoped
reference (gate edit 5 — relocated from the design's phase-6 grouping);
in-module tests green; `make check` green at **27 modules**.

Trio layout (plan-cut requirement 2 of the milestone brief, mirroring
`features/authentication` — path per A-R1): public port layer at
`logic/outbox/`; hub internals under
`internal/logic/`; HTTP under `internal/inbound/http/`; host-facing surface
in the root package — `NewService`/`Service` (mounting via the FS2 method
form `svc.Register(mount)`)/`Repositories`/`Config` in `events.go`,
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

- **depends_on:** [task-3, task-1b]
- **model:** opus
- **files:** [features/events/events.go, features/events/events_test.go, features/events/internal/logic/hub/hub.go, features/events/internal/logic/hub/hub_test.go, features/events/internal/inbound/http/routes.go, features/events/internal/inbound/http/routes_test.go]
- **verify:** `cd features/events && go build ./... && go test -race ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment (gate edit 8; path per A-R1): `! grep -rn '"github.com/gopernicus/gopernicus/features/\(authentication\|cms\|jobs\)' features/events/`
- **description:** Implement design §6 whole. Hub
  (`internal/logic/hub`): one per process; subscribes at `NewService`
  (FS2 fold-in: the hub exists once the service is built; `Register`
  only mounts routes — build-once means behavior starts at construction) —
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
  Handlers read `identity.FromContext(ctx)` (sdk/identity — A-I1 E1), 401
  when absent; per-subject keys are the composite `Type + ":" + ID` (A-I1
  E1 micro-decision: prevents user/machine ID collisions under the
  per-subject cap; with the shipped `StreamMiddleware: RequireUser` wiring
  only user principals appear, so proof-host behavior is unchanged);
  streams ride `web.NewSSEStream` with `Heartbeat` (default 25s) and
  `MaxConnAge`.
  **`MaxConnAge` semantics (post-gate edit P5, design micro-amendment):**
  plain `time.Duration`; zero value → **15m** (O7's posture, deliberately
  inverting the original); **unlimited NOT offered in v1** — a host
  wanting effectively-unlimited sets an explicitly large value (e.g.
  8760h); a negative sentinel is the documented future seam. O7's "hosts
  can set 0 explicitly" is superseded (P5). Socket
  (`events.go`): `AuthorizeStream` func type,
  `Repositories{Outbox outbox.EntryRepository}` (nil → direct-emit mode
  documented), `Config` per §6 as amended by A-I1 E1 (`Bus`,
  `StreamMiddleware`, `Authorize`, `Projector`, `Heartbeat`, `BufferSize`,
  `MaxConnAge`, `MaxConnsPerSubject` — NO `Identity` field; the
  consumer-declared `CurrentUser` port is retired), `NewService`
  hard-erroring on nil `Bus` (FS2 fold-in: construction-time validation,
  the `ErrHasherRequired` precedent — exported error var; the built
  `Service` mounts via `svc.Register(mount)`). **Misconfiguration posture (A-I1 E1, amends the ratified
  degraded-mode matrix row "events `Config.Identity` nil → hard error"):**
  a host that wires no identity-stashing middleware gets uniform 401s —
  fails CLOSED at request time, consistent with the deny-by-default stream
  posture; the README documents the StreamMiddleware requirement (task-14).
  Tests: register on a recording router; httptest end-to-end —
  emit on the bus → frame on the response (id + metadata-only body);
  types filter; per-subject cap (11th connection rejected); Projector
  override; nil-Authorize ⇒ resource route absent; nil Bus ⇒ error; **no
  identity-stashing middleware ⇒ every stream 401s (fails closed — A-I1
  E1)**; **resource-scoped (P4): matching event delivered, non-matching
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
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard`, plus the rule-6 grep at this boundary-creating moment (gate edit 8; path per A-R1): `! grep -rn '"github.com/gopernicus/gopernicus/features/\(authentication\|events\|jobs\)' features/cms/ --exclude-dir=stores`
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
  rule 8, FS2 method form):
  `eventsSvc, err := eventsfeature.NewService(eventsfeature.Repositories{},
  eventsfeature.Config{Bus: bus, StreamMiddleware:
  []web.Middleware{authSvc.RequireUser}})` then
  `eventsSvc.Register(mount)` (A-I1 E2: no `Identity` field —
  `RequireUser` stashes the `identity.Principal` the gateway reads) — one
  bus instance flows to both `Mount.Events` and `Config.Bus` (§6's wiring
  note); `Outbox` nil = direct-emit mode; `Authorize` left nil
  (resource-scoped routes absent — deny by absence, documented in the
  README). README gains the stream
  section (route surface, auth requirement, curl examples).
  [SYNC NOTE 2026-07-07, feature-standard ratification (FS2) — **FOLDED
  2026-07-08 (pre-execution leg)**: the method form is now spelled at every
  operative site — this wiring snippet, the milestone DoD, the phase-2 DoD
  + trio layout + phase table, task-5's hub-subscribe seam and nil-`Bus`
  validation (both moved to `NewService`, the jobs Phase-E precedent).
  The A-I1.4 ratification records were left as written (append-only);
  read their `Register` mentions as `NewService`/`svc.Register(mount)`
  per this fold-in.]

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
- **verify:** `make guard` green on a clean tree; prove-can-fail (A4 practice): add a temporary `features/authentication` import (path per A-R1) to a `features/events` file, observe G5 fail with its error message, remove it, re-run `make guard` green; then full `make check`
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
  (checklist item 12; nil `Bus` = hard error; identity rides
  `sdk/identity` — absent principal ⇒ streams fail closed with 401, and
  the StreamMiddleware requirement is documented loudly — A-I1 E5), the **two-emit-
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
- **files:** [ARCHITECTURE.md, README.md, RELEASING.md, Makefile, sdk/README.md, features/README.md, .claude/plans/roadmap/events-feature-design.md, .claude/plans/roadmap/00-intersections.md, NOTES.md]
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
  the P1–P5 post-gate amendments + A-I1/A-R1 execution, both live-store
  artifacts (suite/dialect/DSN-class/result), the phase-5 protocol results
  verbatim, G5's landing (or strike + the manually-checked note), open
  flags for jrazmi. G2 itself needs no edit (generic regex — verified);
  G5 is task-13's. (8) A-I1/A-R1 doc landings (A-I1 E6): sdk/README.md
  gains the `identity` entry with the A-I1.1 admission trace;
  ARCHITECTURE.md's sdk package enumeration gains `identity` (the A-R1
  path/name updates land in task-0, not here — this task only verifies no
  stale `features/auth` reference survives in the live docs it touches);
  features/README.md §5's corollary is marked CASHED for
  identity-in-context (the illustrative `CurrentUser` port stays as the
  general C2 pattern for domain-shaped needs);
  roadmap/00-intersections.md §2's events-`Config.Identity` row and §3's
  identity seam row gain dated AMENDED markers citing A-I1.

## Sequencing

Phases run 0→6 (A-I1 E8 + A-R1). Hard edges: task-0 before everything
(the A-R1 rename — every later task reads the new path); then task-1 and
task-1b front the milestone, independent of each other; task-1c after
task-0 + task-1b and before phase 5 (the proof host logs in through the
conformed middleware); task-3 before tasks 4/5/6/13; task-5 also needs
task-1b (A-I1 E1 — the gateway reads `identity.FromContext`); task-6
before tasks 9/10/12; task-7 before task-8; task-5 + task-8 before
task-11; task-11 + task-6 before task-12; task-13 before task-15's final
gate; task-14 before task-15. Phase 3 hard-depends only on phase 1 and
may swap ahead of phase 2 if phase 2 blocks. Phase 4 (stores) and phase 5 (proof
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

[ALL RESOLVED at ratification 2026-07-07 (jrazmi): items 1–4 at their
recommended/decided defaults — README placement (1), JSON payload (2), G5
stands (3), P5 confirmed (4); item 5's amendments were ratified the same
day. Kept as written for the decision trail.]

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
5. **A-I1 `sdk/identity` graduation — RATIFIED 2026-07-07 (jrazmi)**,
   same day as proposed; edits E1–E8 applied through the body (adds
   tasks 1b/1c, drops `Config.Identity`, fails-closed posture change to
   the ratified degraded-mode matrix row). A-R1 (the
   `features/authentication` rename, task-0) ratified alongside it. No
   longer open — listed here so the ratification trail stays in one
   place.

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
- Rule-6 spot check at milestone close (path per A-R1):
  `grep -rn "features/\(authentication\|cms\|jobs\)" features/events/`
  empty, and the reverse for `features/events` (cms's emitter uses only
  sdk types).

## Execution log

### task-0 (A-R1 rename) — done 2026-07-08, jrazmi

Renamed `features/auth` → `features/authentication` (+ both store modules),
pure path churn, zero behavior change; existing tests pass unmodified.

- **Coordination order taken:** feature-standard convergence landed FIRST
  (its working-tree edits were committed to HEAD before this leg; tree clean
  at start), then this rename rebased over it — trivially, exactly as the
  task predicted (path churn rebases over content edits, not vice versa).
- **What moved:** `git mv features/auth features/authentication`; root file
  `auth.go` → `authentication.go`; root package decl + package-doc first
  line `auth` → `authentication`. Interior unchanged (`authsvc`, `authmem`,
  `authSvc` vars, the `examples/auth-cms` directory). Three module paths
  (`features/authentication{,/stores/turso,/stores/pgx}`) + both store
  modules' sibling `replace` directives + `examples/auth-cms/go.mod`
  require/replace all rewritten. All internal self-imports (storetest,
  stores, in-package tests) and the six external importer files in
  `examples/auth-cms` rewritten to the new path; every bare-root importer
  (11 files: 6 internal + 5 example) aliased `auth "…/features/authentication"`
  so all `auth.` call sites stay unchanged (O5 precedent).
- **Importer re-verification:** fresh grep (post feature-standard/D-legs)
  still shows exactly the six `examples/auth-cms` files the 2026-07-07
  ratification count named — no drift.
- **Registration surfaces:** go.work (3 use lines), Makefile `MODULES`,
  `STORE_MODULES`, both `test-stores` legs, and the FS1 guard's module list
  (`for f in features/authentication …`). G2/G5 `features/*` generic guards
  needed no edit (verified). Migrations: nothing to do (confirmed).
- **Docs swept (live only):** ARCHITECTURE.md, README.md, RELEASING.md,
  features/README.md, sdk/README.md, the feature's own README (incl. its
  host-usage snippet, now aliased), the pgx store README, and
  examples/auth-cms/README.md. (No turso store README exists.) Historical
  plans and NOTES.md left as written.
- **DIVERGENCE from task file-list item 7 (pre-approved):** the listed sync
  note into `.claude/plans/feature-standard/01-convergence.md` was SKIPPED —
  that plan CLOSED 2026-07-08 and moved to `.claude/past/feature-standard/`
  (historical, append-only). Per the blanket path-note convention in
  `.claude/past/README.md`, past-plan citations of `features/auth` read
  through the A-R1 rename; no edit made.
- **Verify:** `make check` → "all checks passed" (27 modules); `make guard`
  green. Negative grep `grep -rn 'features/auth' --include='*.go'
  --include='go.mod' . go.work Makefile | grep -v 'features/authentication'`
  → empty; same over ARCHITECTURE.md README.md RELEASING.md
  features/README.md sdk/README.md → empty. Run-and-look
  (`examples/auth-cms`, server on :8080 — its build default; README's :8082
  is env-driven): no-session `/articles` 401 → register 201 → pre-verify
  login 403 → verify 200 → login 200 → gated `/articles` 200 → logout 200 →
  gated `/articles` 401; server killed, port free.
- **Pre-existing (not fixed, out of scope):** `examples/auth-cms/cmd/server/
  demo.go` has a gofmt import-order quirk (`sdk/crud` vs `sdk/cryptids`)
  present at HEAD before this leg; `make check` does not gate gofmt, so it
  stays untouched per surgical-diff rule.

### task-1 (`Mount.Events`) — done 2026-07-08

Added the emit-only rail to `feature.Mount` and named `sdk/events` in the
package doc.

- **Public surface:** new `Events events.Emitter` field on
  `feature.Mount{Router, Logger, Events}`. Doc comment carries the ratified
  semantics verbatim in intent: emit-only; best-effort at-most-once — never
  transactional, lost on a crash between commit and emit; the durable path
  rides feature `Repositories`, never this field; nil → the feature emits
  nothing (nil-guard or wrap `events.Noop`, behavior identical).
- **Package doc (gate edit 7):** `feature.go`'s "carries only stdlib types
  plus sdk/web (itself stdlib-only)" now reads "plus sdk/web and sdk/events
  (both stdlib-only)".
- **Import edge:** `sdk/feature → sdk/events`, sdk-internal — G1/G3 stay
  green, `sdk/go.mod` untouched (zero require block confirmed).
- **Tests:** `TestMount_ZeroValueFieldsAreNilable` extended to assert nil
  `Events` on a zero-value construction; new
  `TestMount_EventsDeliversToSubscriber` wires `events.NewMemory()` into a
  Mount, subscribes on `"*"`, and asserts a `WithSync` emit reaches the
  subscriber. Existing `TestMount_RegisterHitsRouter` unchanged.
- **Verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`
  green; `make check` → "all checks passed" (27 modules, every host/feature
  construction site compiles unchanged — the zero-value proof); `make guard`
  green. Run-and-look: `examples/minimal` on :8081, `GET /` and
  `GET /products/widget-3000` → 200/200 (zero-value `Mount.Events` changes
  nothing in a real host), server killed, port free.

### task-1b (`sdk/identity`) — done 2026-07-08

Added the vocabulary-only `sdk/identity` package per A-I1.1 (stdlib-only,
`sdk/oauth`/`sdk/errs` shape — no default implementation, no middleware).

- **Public surface:** `Principal{Type, ID string}` (AV5 shape); constants
  `User = "user"`, `ServiceAccount = "service_account"`; functions
  `WithPrincipal(ctx, p) context.Context` and
  `FromContext(ctx) (Principal, bool)` (unexported key; zero-valued
  empty-ID principal reports false).
- **Package doc:** AV5 lineage (one Principal shape, string subject pairs,
  no registry table); the fails-closed convention (absent identity means
  deny/401, a reader treating it as anonymous-allowed is a bug); and the
  scope fence verbatim in intent: vocabulary only — middleware and
  credential resolution live with the credential owners
  (features/authentication); authorization vocabulary deliberately absent.
- **Tests:** With/From round-trip; explicitly-stashed zero-value Principal
  reports false; absent value reports false; constants' literal values
  locked.
- **Verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`
  green; `make check` → "all checks passed" (27 modules); `make guard`
  green; `sdk/go.mod` untouched (zero require block — G1/G3). No consumer
  wiring yet (task-1c aliases `authsvc.Principal` onto it later); this leg
  adds vocabulary only.

### task-1c (auth conformance — one identity carrier) — done 2026-07-08

Collapsed auth's two private identity context keys onto the single
`sdk/identity` carrier; public API provably unchanged (existing tests pass
unmodified — the conformance proof).

- **Alias/carrier changes (file:line):**
  - `authsvc/machine.go` — `type Principal = identity.Principal` (was a
    local struct); `PrincipalUser = identity.User` /
    `PrincipalServiceAccount = identity.ServiceAccount` (were bare string
    literals); `CurrentPrincipal` reads `identity.FromContext` (was
    `principalFromContext`); `RequireServiceAccount` and `RequirePrincipal`
    stash via `identity.WithPrincipal` (were `withPrincipal`).
  - `authsvc/service.go` — `RequireUser` stashes
    `identity.WithPrincipal(ctx, identity.Principal{Type: identity.User, ID:
    userID})` (was `withUserID`); `CurrentUser` reads `identity.FromContext`
    and filters `Type == identity.User` (was `userIDFromContext`); logout's
    best-effort attribution reads `s.CurrentUser(ctx)` (was
    `userIDFromContext`). Added `sdk/identity` import.
  - `authsvc/context.go` — deleted the `userIDKey`/`principalKey` pair and
    their four helpers (`withUserID`, `userIDFromContext`, `withPrincipal`,
    `principalFromContext`); the `contextKey` const now carries only
    `clientInfoKey`. Rewrote the "It lives here (not sdk) by design" doc note
    to cite A-I1 as the superseding decision — identity graduated to
    `sdk/identity`; only `clientInfo` (audit plumbing, behavior not identity)
    stays feature-private.
  - `authentication.go` — UNTOUCHED; `auth.Principal = authsvc.Principal`
    now chains through the alias to `identity.Principal`, so hosts see zero
    API change.
- **Conformance proof:** NO existing test file was modified
  (`git status --porcelain features/authentication | grep _test.go` → empty).
  All authsvc/http/invitationsvc suites pass with `-count=1`. The
  key-collapse widens two never-asserted cross-reads (CurrentUser now
  readable after a RequirePrincipal-session; CurrentPrincipal after
  RequireUser) — the plan mandated the collapse and no existing test asserts
  those negatives, so behavior in every tested path is unchanged.
- **Coordination note (A3 ordering):** SATISFIED implicitly — feature-standard
  A3 (the two hand-rolled response writers at the middleware region) landed
  2026-07-07 and the milestone closed 2026-07-08 (folded into task-0's
  rebase); this leg rebased over it, no service.go writer conflict.
- **Verify:** `cd features/authentication && go build ./... && go test
  -count=1 ./... && go vet ./...` green (no test file modified); `make check`
  → "all checks passed"; `make guard` green. Run-and-look
  (`examples/auth-cms`, :8082, AUTH_JWT_SECRET + AUTH_DEBUG=1): no-session
  `/articles` 401 → register 201 → pre-verify login 403 → verify 200 → login
  200 + session cookie → gated `/articles` 200 (RequireUser end-to-end
  through the NEW identity carrier) → logout 200 → gated `/articles` 401 →
  clean-jar unauth `/articles` 401; server killed, port free.

### 2026-07-08 — task-2 (charter C3 cash-in) executed

- **What:** `features/README.md` §6 — the event bus port moved from the
  candidates list to a new "Built from this list" entry: `Mount.Events`,
  emit-only, best-effort at-most-once (lost on crash between commit and
  emit), durable delivery pointed at feature `Repositories`, nil → emits
  nothing; added per C3's sanctioned process at events-v1. The candidates
  list retains only the jobs registrar (closing sentence re-singularized).
  §1's mount bullet now reads `feature.Mount{Router, Logger, Events}`.
  §6's pre-v1 compatible-change paragraph kept its
  `Mount{Router: r, Logger: log}` construction example (it illustrates
  named-field construction, not the field inventory).
- **Verify:** `make guard` green (docs-only change); read-back confirms §6
  no longer lists the event bus as a candidate (grep: the only remaining
  "candidate" mentions are the unrelated views-scope note and the
  jobs-registrar section).

### 2026-07-08 — task-3 (module skeleton + `logic/outbox` + registration) executed

- **Module created:** `github.com/gopernicus/gopernicus/features/events`
  (go 1.26.1; `require` + sibling `replace` = sdk only, `features/jobs/go.mod`
  template verbatim). Born FS1/G5-conforming — go.mod has no dependency
  beyond sdk.
- **`logic/outbox/outbox.go`:** `Entry` embeds `sdkevents.Record` +
  `CreatedAt time.Time` + `PublishedAt *time.Time` (nil = unpublished), per
  design §5. `EntryRepository` with the four §5 methods —
  `Append(ctx, ...Record)` (non-transactional convenience; duplicate EventID
  → `errs.ErrAlreadyExists`, zero records → no-op nil), `ListUnpublished(ctx,
  limit) ([]Entry, error)` (CreatedAt ascending), `MarkPublished(ctx,
  eventID)` (idempotent — already-published and unknown-id both return nil),
  `PurgePublished(ctx, before) (int, error)` (published-only, strictly-before
  retention). Doc comments written AS the spec the task-6 storetest suite
  executes; the suite itself is NOT built here.
- **O5 alias applied:** `sdkevents "…/sdk/events"` everywhere in the module.
- **`logic/outbox/outbox_test.go`:** port-level only — a `stubRepo`
  compile-check pinning the exact `EntryRepository` signatures, plus
  `Entry` zero-value semantics (nil `PublishedAt` = unpublished, zero
  `CreatedAt`) and embedded-`Record` field promotion (`EventID`/`Type`).
- **Registration:** `go.work` `use` line and Makefile `MODULES` both gained
  `features/events` in strict alphabetical order — placed **after
  `features/cms/views/templ`, before `features/jobs`**. DIVERGENCE (logged,
  pre-flagged in the task): the plan text said "after
  `features/cms/stores/turso`" but `features/cms/views/templ` landed after
  this plan was written and sorts between them, so events lands after
  views/templ; strict alphabetical order preserved. Makefile header comment
  bumped 27 → 28 modules.
- **Module-count observation:** `make check` now iterates **28 modules** —
  the Phase 2 DoD/table predicted 27 (written before `features/cms/views/templ`
  existed). Same off-by-one as the go.work placement adjustment above; no
  scope change.
- **Verify:** `cd features/events && go build ./... && go test ./... &&
  go vet ./...` green (`logic/outbox` ok); `gofmt -l` clean. `make check`
  → "all checks passed" (28 modules); `make guard` green (all six). Note:
  the FS1 guard's explicit `for f in features/authentication features/cms
  features/jobs` loop does NOT yet include `features/events` — adding it is
  a phase-8 concern (design §12 item 3, G2 module-list flagged for phase 8);
  the module's go.mod is sdk-only regardless, so it is born-conforming.
  Run-and-look: `examples/minimal` on :8081, `GET /` → 200 and
  `GET /products/widget-3000` → 200 (the new module is not yet wired into any
  host, so behavior is unchanged); server killed, port 8081 free.

### 2026-07-08 — task-4 (the poller — exported, host-driven) executed

- **`features/events/poller.go`** (root package `events`, gate edit 6 file
  split): `Poller` + `NewPoller(repo outbox.EntryRepository, bus
  sdkevents.Bus, opts ...PollerOption) *Poller` with functional-option
  `WithBatchSize(n int)` (default 100) — option style follows the Memory
  bus's `MemoryOption` in the same sdk module (design's stated two-arg
  signature preserved; batch size is the variadic tail). `Poll(ctx) error`
  matches `workers.WorkFunc` exactly, so a host feeds it straight to
  `workers.NewPool`. Owns no goroutines and no lifecycle (design §5; D4
  host-drives-execution).
- **Emit discipline (P1), implementation points:** `Poll` reads
  `ListUnpublished(ctx, batchSize)`, returns `workers.ErrNoWork` on an empty
  batch (`poller.go` — `if len(entries) == 0`), else loops emitting each with
  `sdkevents.WithSync()`. On emit error it `return`s immediately **without**
  `MarkPublished` (the entry stays unpublished → next-poll retry); only after
  a successful emit does it call `MarkPublished`. A mark error also returns
  (entry stays unpublished → documented duplicate emit next poll; consumers
  de-dupe on `EventID()`). The doc comment on `Poll` cites all three
  semantics verbatim in intent: Memory + `WithSync` returns the first handler
  error (failing subscriber ⇒ unpublished ⇒ redelivery, idempotent-handler
  contract); goredis + `WithSync` returns the XADD error properly; the
  closed-bus edge where BOTH buses return nil (dropped warning) and `Poll`
  would mark-anyway — safe only because the documented shutdown order stops
  the poller before `bus.Close`.
- **Feature-local rehydrated event (gate edit 1):** unexported `outboxEvent`
  embeds `sdkevents.RemoteEvent` (reusing sdk's frozen envelope
  `Event`/`Metadata`/`Unmarshaler`/`EventEncoder` decoding) and adds
  `EventID() string`. `newOutboxEvent(rec)` maps a `Record` onto it. Static
  asserts pin `Event`, `Metadata`, `Unmarshaler`, and `interface{ EventID()
  string }`. `RemoteEvent` carries no EventID (why the wrapper exists);
  sdk/events stays frozen. The hub (task-5) will read `id:` via the
  `interface{ EventID() string }` assertion — the concrete type stays
  unexported.
- **`features/events/poller_test.go`** (white-box `package events`, hermetic):
  `fakeRepo` (in-memory `EntryRepository`, ascending-CreatedAt
  `ListUnpublished`, MarkPublished call counter, injectable list/mark errors),
  `stubBus` (records `Sync` flag + emitted EventIDs, injectable emit error,
  delivers to no subscribers), `sampleEvent` (`BaseEvent` + `Name`). Tests:
  `TestPoll_EmptyBatch_ReturnsErrNoWork`;
  `TestPoll_DrainsInCreatedAtOrder_MarksEachOnce` (seeded out of order,
  Memory bus + `*` subscriber, delivery order = CreatedAt ascending, each
  marked once, second poll ⇒ ErrNoWork); `TestPoll_EmitsWithSync` (stubBus
  `sawSync`); `TestPoll_EmitError_DoesNotMark_RetriedNextPoll` (P1: mark
  count 0 after emit error, then 1 after the bus recovers);
  `TestPoll_MarkError_LeavesUnpublished_DuplicateEmitNextPoll` (emit count
  1 → 2 across two polls); `TestPoll_EventIDSurfacesOnRehydratedEvent`;
  `TestPoll_TypedHandlerRehydratesViaUnmarshaler` (`TypedHandler[sampleEvent]`
  slow path rehydrates `Name`/`Type`).
- **Verify:** `cd features/events && go build ./... && go test -race ./... &&
  go vet ./...` → all green (2 packages ok, race clean); `gofmt -l .` clean.
  `make guard` green (all six). `make check` → "all checks passed" (28
  modules). Run-and-look: `examples/minimal` on :8081, `GET /` → 200 and
  `GET /products/widget-3000` → 200 (events module still unwired into any
  host — behavior unchanged); server killed, port 8081 free.
- **DIVERGENCE / note:** the DoD says "27 modules"; `make check` iterates 28
  (the same `features/cms/views/templ` off-by-one task-3 logged — not a
  scope change). No other divergences; `NewPoller`'s variadic option tail is
  an additive refinement of the design's two-arg signature, not a change.

### 2026-07-08 — task-5 (SSE gateway hub + HTTP + the feature socket) executed

- **`features/events/events.go`** (root package `events`, host-facing
  socket, O5 `sdkevents` alias). Exported surface:
  - `AuthorizeStream func(ctx context.Context, principal identity.Principal,
    resourceType, resourceID string) (bool, error)` — the consumer-declared
    coarse ownership check. **DIVERGENCE (logged):** design §6 typed it
    `userID string`; post-A-I1 the handler holds an `identity.Principal`, and
    sdk/identity's stated lineage is "a host's authorizer reads the Principal
    unadapted", so the check receives the full Principal (Type + ID), not a
    lossy user-id string. E1 did not re-spec this signature; the Principal
    shape is the faithful post-amendment form. With the shipped
    `StreamMiddleware: RequireUser` wiring only user principals appear, so
    proof-host behavior is unchanged.
  - `Projector func(sdkevents.Event) any` — audited opt-in to a richer SSE
    body; nil → metadata-only.
  - `Repositories{ Outbox outbox.EntryRepository }` — nil Outbox documented as
    direct-emit mode (no durable rail; the host wires+drives a Poller
    separately via `NewPoller`; the gateway never owns the poller). Carried on
    the Service; the gateway itself is a bus consumer and does not read it.
  - `Config{ Bus, StreamMiddleware, Authorize, Projector, Heartbeat,
    BufferSize, MaxConnAge, MaxConnsPerSubject }` — **NO `Identity` field**
    (A-I1 E1: the consumer-declared `CurrentUser` port is retired). No
    `Logger` field either — the plan's enumerated Config set omits it, so the
    hub falls back to `slog.Default()` (noted; jobs' `Config.Logger` precedent
    was NOT adopted to keep the enumerated set exact).
  - `var ErrBusRequired = errors.New("events: Config.Bus is required")` — the
    lone construction-time hard error (the `ErrHasherRequired` precedent).
  - `NewService(repos Repositories, cfg Config) (*Service, error)` — validates
    nil `Bus`, builds the hub (**subscribes to the bus here** — FS2
    build-once: fan-out starts at construction), defaults `Heartbeat` 25s /
    `MaxConnAge` 15m. `(*Service) Register(m feature.Mount) error` — mounts
    routes ONLY; the resource-scoped route is registered only when
    `Config.Authorize` was set (deny-by-absence); logs a registration line
    when `m.Logger != nil`.
  - **`MaxConnAge` (P5):** zero → 15m default, CANNOT be disabled;
    effectively-unlimited is an explicitly large value. Documented on the
    Config field.
- **`features/events/internal/logic/hub/hub.go`** (logic tier — imports sdk
  only, no sdk/web; produces a transport-neutral `Frame`). Behavior:
  - **Subscription-mode selection at `New`:** `SubscribeBroadcast("*")` when
    the bus satisfies `sdkevents.Broadcaster`, else `Subscribe("*")` with a
    logged single-instance warning (the v1 memory-bus path — Memory satisfies
    Broadcaster, so the warning fires only for a non-broadcaster backend).
  - **Per-connection buffered channels** (default 64); **drop-on-full**
    non-blocking send with a sampled warning counter (`atomic.Uint64`, logs
    the 1st drop then every 100th).
  - **Per-subject connection cap** (default 10); `Connect` returns
    `ErrTooManyConnections` past the cap; a different subject is unaffected;
    releasing a slot readmits.
  - **Projection:** metadata-only `metaView{type, occurred_at,
    aggregate_type, aggregate_id, tenant_id}` by default; raw payloads never
    forwarded unless a `Projector` opts in.
  - **SSE `id:`** sourced by asserting `interface{ EventID() string }` (the
    poller's rehydrated events — gate edit 1), falling back to
    `CorrelationID` for best-effort events (documented: no per-event de-dupe
    guarantee).
  - **Resource-scoped delivery filter (P4):** a scoped connection
    (`ResourceType != ""`) delivers only events whose `Metadata` matches both
    `AggregateType`/`AggregateID`; events with no/nil aggregate metadata are
    suppressed (deny-by-default). Subject streams apply only the `?types`
    allow-list.
  - The Frame channel is never closed by the hub — the reader stops on its own
    context and unregisters, so a late fan-out send lands in the buffer or is
    dropped, never on a closed channel (no send-on-closed panic).
- **`features/events/internal/inbound/http/routes.go`** (inbound tier).
  Routes: `GET /events` (subject stream, `?types=a,b` exact-match allow-list —
  O6) always; `GET /events/{resource_type}/{resource_id}` only when
  `Config.Authorize != nil`. Handlers read `identity.FromContext(ctx)` and
  **fail closed with 401 when absent** (A-I1 E1); resource stream 403s on
  deny, 500s on authorize error. Per-subject key is the composite
  `principal.Type + ":" + principal.ID` (A-I1 E1). Streams ride
  `web.NewSSEStream` with `WithHeartbeat`; `MaxConnAge` bounds each stream via
  a `context.WithTimeout` on the request context. Cap breach → 429. FS9: all
  error responses go through `web.RespondJSONError`/`web.Err*`.
- **Tests** (all `-race` clean):
  - `hub_test.go` — broadcast-vs-subscribe mode selection (recording fake
    buses); `Close` unsubscribes; metadata-only projection fields + id =
    CorrelationID; `EventID()` sources id:; Projector override; types filter;
    resource-scoped P4 (non-matching suppressed, no-metadata suppressed,
    matching delivered); per-subject cap (+ cross-subject independence + slot
    release); drop-on-full counter; unregister stops delivery.
  - `events_test.go` (`package events_test`) — nil Bus ⇒ ErrBusRequired;
    builds with a bus; **register on a recording router** proving resource
    route deny-by-absence (absent without Authorize, present with it);
    **httptest end-to-end** over `web.NewWebHandler` + an identity-stashing
    middleware: emit on the Memory bus → SSE frame arrives with correct
    `id:`/`event:`/metadata-only `data:` body; **no-middleware ⇒ 401**
    (fails closed — A-I1 E1's replacement for the retired nil-Identity
    constructor-error test).
  - `routes_test.go` (`package http`) — subject + resource streams 401 without
    identity; resource stream 403 on deny, 500 on authorize error; authorize
    receives the correct Principal + path values; per-subject cap ⇒ 429.
    (P4 delivery correctness lives in hub_test; the HTTP layer covers the
    authorize-denied rejection.)
- **Verify:** `cd features/events && go build ./... && go test -race ./... &&
  go vet ./...` → all four packages ok, race clean. Rule-6 grep (gate edit 8)
  `grep -rn '"…/features/\(authentication\|cms\|jobs\)' features/events/` →
  empty (exit 1). `make guard` green (all six). `make check` → "all checks
  passed". Run-and-look: `examples/minimal` :8081 `GET /` → 200 and
  `GET /products/widget-3000` → 200 (events still unwired into any host —
  task-11), server killed, port 8081 free.
- **NOTE:** the http layer trusts `Heartbeat`/`MaxConnAge` are pre-defaulted
  by `NewService` (documented on the internal `Config`); the direct
  `routes_test.go` supplies them itself since it bypasses `NewService`. A real
  SSE drive (curl -N, observed frames) happens at the proof host
  (task-11/protocol), as the plan directs.

### 2026-07-08 — task-6 (`features/events/storetest` + hermetic reference) executed

- **`features/events/storetest/storetest.go`** (package `storetest`, a separate
  package inside the events module — matching `features/jobs/storetest` and
  `features/cms/storetest` exactly: same-module sibling package, not a separate
  module). Exports `Run(t *testing.T, newRepo func(t *testing.T)
  outbox.EntryRepository)` — one port set (the events feature has a single
  outbound port), following the cms `Run(t, newImpl)` shape rather than jobs'
  two-runner split. Imports stdlib + sdk (`sdkevents`, `errs`) + the events
  feature's own `logic/outbox` only (no driver — guard G2). Five leaf cases,
  each on a clean repo from `newRepo(t)`:
  - **AppendAndListOrder** — empty `Append()` is a nil no-op; a clean repo lists
    nothing; three records appended oldest-first (3ms real sleep between so
    CreatedAt is strictly increasing even against a microsecond-truncating
    store) come back CreatedAt-ascending; every returned entry is unpublished
    (PublishedAt nil); the durable envelope round-trips (Type/OccurredAt/Payload
    on the oldest); a positive `limit` caps the page to the oldest N. Records
    share `suiteBase` as OccurredAt so ordering is proven against the
    store-assigned CreatedAt, not the event's own timestamp.
  - **UnpublishedOnly** — publishing a middle entry drops it from
    ListUnpublished while the rest stay in append order; publishing all leaves
    nothing unpublished.
  - **MarkPublishedIdempotence** — re-marking an already-published entry returns
    nil; marking an unknown eventID returns nil (row may have been purged) — the
    poller can retry a mark without a hard failure.
  - **PurgePublishedRetention** — reads the store-assigned CreatedAts, then a
    cutoff equal to new-pub's CreatedAt purges only old-pub (strictly-before
    semantics: new-pub is retained), returns count 1; a far-future cutoff purges
    the remaining published row; the unpublished `keep-unpub` is never purged
    regardless of age across both purges.
  - **EventIDUniqueness** — a second `Append` of an existing EventID returns
    `errs.ErrAlreadyExists` and leaves exactly the one original row.
  - The dialect-typed `AppendTx` is deliberately absent (it takes a store Tx;
    each store tests its own — design §8).
- **`features/events/storetest/reference_test.go`** (`package storetest`, a
  `_test.go` file so the reference is test-scoped and never ships in the module's
  non-test build surface — matching jobs/cms). `TestReference` runs `Run`
  against `newReference()`, a fresh instance per call (clean-isolation contract).
  - **Reference honesty (R3/S6, phase-2-W7 lesson):** `reference` is a
    mutex-guarded `map[string]*outbox.Entry` keyed by EventID. `Append`
    pre-checks the whole batch against existing rows AND a within-batch `seen`
    set before writing any row, returning `errs.ErrAlreadyExists` on a
    collision — so a batch carrying a duplicate commits nothing (the atomicity a
    SQL primary key gives for free). `ListUnpublished` filters PublishedAt==nil
    and sorts CreatedAt-ascending (EventID breaking ties). `MarkPublished` is a
    nil no-op for already-published/unknown. `PurgePublished` deletes published
    rows strictly before the cutoff. A compile-time `var _
    outbox.EntryRepository = (*reference)(nil)` pins conformance.
  - **Uniqueness honesty proof:** the EventID map + pre-check genuinely enforce
    uniqueness. If the reference instead blindly overwrote (`entries[id] = …`
    with no pre-check) or appended a duplicate, its `Append` would return nil on
    the second call and **testEventIDUniqueness would fail** at
    `!errors.Is(err, errs.ErrAlreadyExists)` — the suite proves the contract,
    it does not pass vacuously.
- **Verify:** `cd features/events && go build ./... && go test -race ./... &&
  go vet ./...` → all five packages ok, race clean (storetest 1.2s under -race).
  `make check` → "all checks passed". `make guard` → green (all six). Run-and-
  look: `examples/minimal` :8081 `GET /` → 200 and `GET /products/widget-3000`
  → 200, server killed, port 8081 free.
- **Convention check:** `features/jobs/storetest` and `features/cms/storetest`
  are both separate packages within their feature module (import path
  `…/features/<name>/storetest`, `package storetest`, reference in a
  `_test.go`). `features/events/storetest` matches this verbatim.

### 2026-07-08 — Phase 2 (tasks 3–6, design-phase 4) complete — DoD holds

Every phase-2 DoD clause with its verifying artifact:

- **Module standalone / go.mod sdk-only** — `features/events/go.mod` has a lone
  `require github.com/gopernicus/gopernicus/sdk v0.0.0`; guard FS1 ("feature core
  go.mod requires sdk only") green in `make check`/`make guard`.
- **`logic/outbox` public** — `features/events/logic/outbox/outbox.go` is a
  non-`internal` package exporting `Entry` + `EntryRepository`; imported by
  `storetest` and by the future store adapters (tasks 9–10).
- **Poller exported, host-driven, returns `workers.ErrNoWork`** —
  `features/events/poller.go` `NewPoller`/`(*Poller)` at the module root; the
  drain-empty path returns `workers.ErrNoWork` (task-4 log; poller_test.go).
- **Hub internal** — `features/events/internal/logic/hub/hub.go` under
  `internal/`; guard G2 keeps it unreachable from outside the module.
- **`NewService` errors on nil Bus** — `var ErrBusRequired`; `events_test.go`
  asserts `NewService` with nil `Config.Bus` returns it (task-5 log).
- **Absent identity fails closed** — handlers read `identity.FromContext` and
  401 when absent; `routes_test.go` + `events_test.go` no-middleware-⇒-401
  cases (task-5 log).
- **Routes `/events` always, scoped only when `Authorize` set** —
  `internal/inbound/http/routes.go` registers `GET /events` unconditionally and
  `GET /events/{resource_type}/{resource_id}` only when `Config.Authorize != nil`
  (deny-by-absence); `events_test.go` register-on-recording-router case.
- **storetest green hermetically** — `TestReference` runs `Run` against the
  honest in-memory reference on every `features/events` `go test` and every
  `make check`; verified this leg (race-clean, no driver in the graph — guard G2).

### 2026-07-08 — task-7 (cms core — nil-guarded emits from `entrysvc`) executed

- **cms-internal typed events** — new `features/cms/internal/logic/entrysvc/events.go`:
  three structs embedding `sdkevents.BaseEvent` — `ContentPublished`,
  `ContentUpdated`, `ContentDeleted` — with private constructors that stamp the
  ratified type name + aggregate metadata via `NewBaseEvent(type).WithAggregate("entry", entryID)`.
  Type-name consts: `content.published` / `content.updated` / `content.deleted`
  only; aggregate_type const `"entry"`. No shared struct crosses the feature
  boundary (§4 rule-6 note) — consumers subscribe by topic, project metadata only.
- **Emitter collaborator** — `Service` gained an `events sdkevents.Emitter`
  field. `NewService` signature extended with a **trailing optional**
  `emitter ...sdkevents.Emitter` (variadic, not a new required positional) so
  the ~11 existing 3-arg `NewService(...)` call sites — including the whole
  existing `service_test.go` suite — compile and pass **unmodified**; omitted or
  nil ⇒ `sdkevents.Noop{}`, so emit call sites stay unconditional. `cms.Register`
  now passes `m.Events` as that trailing arg (`entrysvc.NewService(repos.Entries,
  registry, nil, m.Events)`). cms is still package-func `Register` (B3 deferred) —
  it receives the `feature.Mount` and threads `m.Events`.
- **Emit points (all AFTER the domain write returns, never inside/around a repo
  call — best-effort §3), by method:line in service.go:**
  - `Create` emit L98 → `content.updated` (aggregate_id = created entry ID)
  - `Edit` emit L129 → `content.updated`
  - `Publish` emit L166 → `content.published`
  - `Unpublish` emit L181 → `content.updated`
  - `Delete` emit L190 → `content.deleted` (aggregate_id = the id arg)
  - `SetTerms` emit L199 → `content.updated` (aggregate_id = entryID)
  Each write method now captures the repo result, returns early on error
  (no emit on a failed write), then calls the private `emit(ctx, evt)` helper.
- **Emit is swallowed, never returned** — `emit` calls `s.events.Emit(ctx, evt)`
  and on error logs via `slog.Default().ErrorContext` at most; the write already
  succeeded so the caller never sees an emit error (§3 best-effort).
- **Tests (service_test.go, additive only — existing functions byte-identical):**
  `TestService_Emits` drives Create/Edit/Publish/Unpublish/SetTerms/Delete
  through a `recordingBus` (wraps `sdkevents.NewMemory()`, forces `WithSync()` so
  delivery is deterministic) subscribed on `"*"`, asserting each path's exact
  event type + aggregate metadata (aggregate_type "entry", aggregate_id = entry
  ID). `TestService_EmitErrorNotReturned` uses a `failingEmitter` (always errors)
  and asserts Create returns nil error + a created entry. `TestService_NilEmitterNoPanic`
  proves the nil-emitter default path. The pre-existing suite ran **unmodified**
  and green — no behavior drift.
- **Rule-6 grep clean** — `grep -rn '"…/features/\(authentication\|events\|jobs\)'
  features/cms/ --exclude-dir=stores` → empty (exit 1). Independent
  `grep -rn 'features/events' features/cms/` → empty: cms rides the emitter on
  `sdk/events` only, never imports `features/events`.
- **Verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...`
  all green (entrysvc suite ok). `make check` → "all checks passed"; `make guard`
  green (all six guards). Real-interaction (nil-emitter host, proves unchanged
  behavior): booted `examples/minimal` on :8081 → `GET /` 200, `GET
  /products/widget-3000` 200; listener killed, port confirmed free.
- **DIVERGENCE (pre-reasoned, not silent):** the plan's "signature change is
  free" and "existing tests pass UNMODIFIED" are only jointly satisfiable if the
  new arg does not break the existing 3-arg call sites — hence the trailing
  **variadic** emitter rather than a required 4th positional param. Behavior is
  identical (single emitter, nil→Noop); the variadic is purely a
  backward-compatibility affordance for the unmodified-tests requirement.

### 2026-07-08 — task-8 (host wiring — shared bus + cache-invalidation subscriber) executed

Wiring only (rule 8) — two files: `examples/auth-cms/cmd/server/main.go`,
`examples/auth-cms/README.md`. No feature/port/store changes.

- **Shared bus** — one `bus := sdkevents.NewMemory(sdkevents.WithLogger(log))`
  built in `run()` before the mount (import aliased `sdkevents` per O5). Set as
  `feature.Mount{Router: router, Logger: log, Events: bus}` — cms (the emitter,
  task-7) now publishes `content.*` through `m.Events`.
- **Page cache held in a variable** — `pageCache := cacher.NewMemory()` (was the
  inline `cacher.NewMemory()` flowing straight into `cms.Config.Cache`); the
  cms `Cache:` field now reads `pageCache` so the host subscriber can drop it.
- **Cache-invalidation subscriber (S5/O6)** — `bus.Subscribe("*", …)` with the
  handler filtering `strings.HasPrefix(e.Type(), "content.")` and calling
  `pageCache.DeletePattern(ctx, "page:*")` (S4 — verified the real key prefix in
  `sdk/web/cache.go:24`: `"page:" + r.URL.RequestURI()`; `Memory.DeletePattern`
  trims the `*` to a prefix delete). Plain `"*"` fan-out, filter-in-handler — no
  prefix routing built (O6).
- **Shutdown (§7 / P3 idiom)** — `web.Run` blocks until the parent ctx is
  canceled and drains HTTP on its OWN fresh Background+ShutdownTimeout context
  (`run.go:32`), so by return the parent ctx is already canceled. The bus is
  therefore closed on a FRESH bounded context:
  `closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel(); bus.Close(closeCtx)`.
  A §7 ordering comment in `main.go` states HTTP → bus.Close today and marks
  where phase 5 inserts the outbox poller (stopped after HTTP, before bus.Close).
- **Rejected alternative, logged (P2):** making cms emits synchronous would give
  a deterministic invalidation check but re-decides ratified O3 (emitter latency
  must not be hostage to subscribers) and contradicts §3's re-fetch-trigger
  semantics — async stays; the run-and-look uses bounded-poll wording instead.
- **README** — added an "event bus + cache invalidation" bullet to the Wiring
  section: one shared `sdkevents.NewMemory` as `Mount.Events`, cms emits
  `content.*` post-write, host subscribes `"*"`/filters `content.*`/clears
  `page:*`, async (re-fetch trigger not transactional write), pre-change page was
  TTL-bound, bus closed on a fresh bounded context at shutdown.

- **Verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet
  ./...` all green (memstore + authmem suites ok; cmd/server no test files).
  `make check` → "all checks passed" (27 modules; six guards green).
- **Run-and-look (bounded-poll per P2), :8082, `AUTH_JWT_SECRET` set:**
  register `admin@example.com` (201) → verify with mailer-logged code (200) →
  login cookie jar (200) → authed `GET /articles` (200). Article
  `saf7a5i6krivhyepxfnzyv7t7m` = "Two features, one host", public
  `/articles/two-features-one-host`.
  - prime `GET` → **X-Cache: MISS** (renders original, caches).
  - second `GET` → **X-Cache: HIT** (served from cache).
  - `POST /articles/saf7a5i6krivhyepxfnzyv7t7m` (title unchanged so slug stable,
    status=published, body carrying `EDITED-1783530326`) → **303** — cms emits
    `content.updated` async; host handler clears `page:*`.
  - poll public page (retry ≤ ~2s): **MISS observed on poll #1 after 34ms**, and
    the re-rendered page contains the `EDITED-1783530326` marker (fresh content).
  - next `GET` → **X-Cache: HIT** again (re-cached), marker still present.
  - **Pre-change contrast:** load 2 was a HIT only 34ms before the invalidated
    MISS — well inside the 60s `publicPageTTL`. Without this subscriber the cache
    is purely TTL-bound: that HIT would have kept serving the pre-edit bytes (no
    marker) for up to 60s. The 34ms MISS with fresh content is exactly the
    non-TTL-bound behavior the wiring adds.
  - `SIGTERM` → "server shutting down" → "server stopped" (clean; `bus.Close` on
    the fresh context logged no close-timeout warning); port 8082 freed. Did NOT
    commit.

### 2026-07-08 — Phase 3 (tasks 7–8, design-phase 5) complete — DoD holds

Phase 3 DoD (plan ~line 877) satisfied and evidenced:

- `entrysvc` emits `content.*` post-write via the mount's emitter behind a
  nil→Noop guard, zero port/store changes, best-effort (§3/O2) — task-7's
  events.go + service.go emit points, existing suite unmodified/green.
- `examples/auth-cms` carries the shared bus (`Mount.Events`) and invalidates its
  public-page cache on `content.*` — task-8's subscriber + fresh-context
  shutdown.
- Run-and-look passed and recorded (above): edit → MISS-within-window (34ms) with
  fresh content → HIT; TTL-bound pre-change contrast noted.
- Artifacts: `make check` green at 27 modules; the :8082 register→verify→login→
  edit→invalidate transcript with observed X-Cache transitions and timing.

### 2026-07-08 — task-9 (`features/events/stores/turso`, module 28) executed

Module surface (files per plan + one convention-match test): `go.mod`,
`turso.go` (package doc, `New(db) (*Store, error)` + boot probe,
`ExportMigrations`, `MigrationsFS`/`MigrationsDir`), `outbox.go` (the
`outbox.EntryRepository` impl + `AppendTx`), `migrations/0001_event_outbox.sql`,
`conformance_test.go` + `appender_test.go` (both `//go:build integration`),
`turso_test.go` (hermetic `ExportMigrations`), `README.md`.

- **Migration** — `migrations/0001_event_outbox.sql`, source `"events"` (turso
  dialect: `event_id` PK/de-dupe key, `event_type`, `occurred_at`,
  `correlation_id`, `payload` TEXT JSON, nullable `aggregate_type`/`aggregate_id`/
  `tenant_id`, `created_at`, nullable `published_at`; partial index
  `idx_event_outbox_unpublished ON (created_at) WHERE published_at IS NULL`). The
  connector applied it under its internal `default` source name (the host's own
  runner is what stamps it `"events"` post-scaffold — RunMigrations hardcodes
  `defaultMigrationSource`).
- **`EntryRepository`** over the tursodb connector — helpers reused: `Querier`
  (shared `insertRecords` for `Append`/`AppendTx`), `FormatTime`/`ParseTime`,
  `ExecAffecting` (PurgePublished count), `MapError` (rows.Err + scan;
  UNIQUE→`ErrAlreadyExists` on duplicate `event_id`), `ExportMigrations`,
  `Scanner` alias. `MarkPublished` idempotent via `WHERE published_at IS NULL`
  zero-row no-op (unknown/already-published → nil); `ListUnpublished` is
  `created_at ASC, event_id` with `LIMIT -1` for non-positive.
- **`AppendTx(ctx, tx *tursodb.Tx, recs ...sdkevents.Record) error`** — the
  dialect-typed transactional appender (§5); shares `insertRecords` with the
  own-tx `Append`. Nothing consumes it in v1; future emitting stores
  consumer-declare a matching one-method port satisfied structurally.
- **Boot-time probe (§5 mitigation b)** — no sibling store probes; introduced
  fresh here. `New` runs `SELECT name FROM sqlite_master WHERE type='table' AND
  name='event_outbox'`; `sql.ErrNoRows` → wrapped `errs.ErrNotFound` naming the
  unapplied `"events"` source, before the host serves traffic. README states the
  prerequisite loudly.
- **Registration** — `go.work` (`./features/events/stores/turso` after
  `./features/events`); Makefile `MODULES` (after `features/events`),
  `STORE_MODULES` (7th turso entry), and a `test-stores` turso leg (`cd
  features/events/stores/turso && go test -tags=integration ./...`). go.work ↔
  Makefile `MODULES` agree at **29** entries.

- **Verify (hermetic):** `cd features/events/stores/turso && go build ./... && go
  test ./... && go vet ./...` — green (`ok … 0.207s`, `TestExportMigrations`
  runs). Loud skip recorded on the tagged path without env:
  `go test -tags=integration ./...` → `--- SKIP: TestConformance` /
  `--- SKIP: TestAppendTx` — `TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso
  conformance NOT verified`, suite still `PASS`.
- **Verify (gates):** `make guard` — six guards green. `make check` — "all checks
  passed" at 29 modules (incl. `vet -tags=integration features/events/stores/turso`).
- **LIVE LEG (authorized, milestone-close artifact):** `.env`
  `TURSO_DATABASE_URL` asserted equal to
  `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` (MATCH,
  recorded; token never echoed). Suite **`features/events/stores/turso`** (turso,
  `-tags=integration -count=1`) against the **playground DB**, dated
  **2026-07-08**:
  - `TestAppendTx` — **PASS (2.56s)**: `AppendTx` inside `InTx` visible after
    commit; rolled back on a sentinel error leaves no row.
  - `TestConformance` — **PASS (9.87s)**, 5/5 subtests: `AppendAndListOrder`,
    `UnpublishedOnly`, `MarkPublishedIdempotence`, `PurgePublishedRetention`,
    `EventIDUniqueness`.
  - `TestExportMigrations` — **PASS**. Total `ok … 12.660s`. Migration
    `0001_event_outbox.sql` applied once (checksum `c4ac8338`), reused across
    subtests. **6 top-level PASS / 0 FAIL**, single executor.
- **Standing real-interaction check:** `examples/minimal` booted on `:8081` —
  `GET /` → **200**, `GET /products/widget-3000` → **200** (server log confirms
  both), process killed, port 8081 freed. Did NOT commit.

### 2026-07-08 — task-10 (`features/events/stores/pgx`, module 30) executed

Module surface (files per plan + one convention-match hermetic test): `go.mod`,
`postgres.go` (package doc, `New(db) (*Store, error)` + boot probe,
`ExportMigrations`, `MigrationsFS`/`MigrationsDir`, `scanner` alias), `outbox.go`
(the `outbox.EntryRepository` impl + `AppendTx`), `migrations/0001_event_outbox.sql`,
`conformance_test.go` + `appender_test.go` (both **plain env-gated, no build tag**
— the pgx-sibling convention, distinct from task-9's tagged turso tests),
`postgres_test.go` (hermetic `ExportMigrations`), `README.md`.

- **Sibling-gating divergence (resolved):** the events **turso** sibling uses
  `//go:build integration`; the **pgx** siblings (`features/{jobs,cms,
  authentication}/stores/pgx`) use plain `POSTGRES_TEST_DSN` env-gating with a
  loud `t.Skip`. Task-10's brief said "mirror the sibling pgx stores exactly" —
  so this store is plain env-gated (no build tag), matching jobs pgx verbatim.
  Consequence for the DoD's per-tree symmetry: both trees still run ONE
  `storetest.Run` suite; only the gating mechanism differs by dialect, as it
  already does across the other feature store pairs.
- **Migration** — `migrations/0001_event_outbox.sql`, **identical filename/version
  to the turso tree**, source `"events"`, **postgres dialect**: `event_id` PK,
  `event_type`, `occurred_at TIMESTAMPTZ`, `correlation_id`, `payload`,
  nullable `aggregate_type`/`aggregate_id`/`tenant_id`, `created_at TIMESTAMPTZ`,
  nullable `published_at TIMESTAMPTZ`; partial index `idx_event_outbox_unpublished
  ON (created_at) WHERE published_at IS NULL`.
- **JSON vs JSONB decision (logged per Schema section + open-question 2):**
  `payload` is **`JSON`, not `JSONB`** — KEPT the plan's ratified default (pgx
  payload JSON), a deliberate deviation from the design's illustrative JSONB.
  Rationale: the payload is opaque to this store (no jsonb operators/indexes),
  `JSON` preserves the caller's exact bytes while `JSONB` re-canonicalizes
  whitespace/key order, and the shared `storetest` suite asserts a **byte-exact
  payload round-trip** (`string(first.Payload) == {"eid":"evt-1"}`, storetest.go:90)
  — which only `JSON` satisfies. Same decision + rationale as
  `features/jobs/stores/pgx` (jobs-v1 precedent). Logged in the migration file,
  `postgres.go`-adjacent README, and here.
- **`EntryRepository`** over the pgxdb connector — helpers reused (D2–D6):
  `Querier` (shared `insertRecords` for `Append`/`AppendTx`), `FromNullTimePtr`
  (`published_at` NULL → nil = unpublished, UTC-normalized on read),
  `ExecAffecting` (PurgePublished count), `MapError` (SQLSTATE-based; scan +
  `rows.Err`; unique-violation → `errs.ErrAlreadyExists` on duplicate `event_id`),
  `ExportMigrations`, `Scanner` alias. Postgres scans timestamps natively into
  `time.Time`/`*time.Time` and nullable text into `*string` (NULL → nil) — no
  TEXT-parsing layer (the turso `FormatTime`/`ParseTime` twin is absent by
  dialect). `MarkPublished` idempotent via `WHERE published_at IS NULL` zero-row
  no-op; `ListUnpublished` is `created_at ASC, event_id` with `LIMIT ALL` for a
  non-positive limit (postgres has no `LIMIT -1`; binds `$1` only when positive).
  Payload arg goes through `$5` as JSON text; nullable `*string` metadata binds
  directly (pgx: nil → NULL) — no `nullStr` helper needed.
- **`AppendTx(ctx, tx *pgxdb.Tx, recs ...sdkevents.Record) error`** — the
  dialect-typed transactional appender (§5); shares `insertRecords` with the
  own-tx `Append`. Nothing consumes it in v1; future emitting stores
  consumer-declare a matching one-method port satisfied structurally.
- **Boot-time probe (§5 mitigation b), postgres mechanics:** `New` runs
  `SELECT to_regclass('event_outbox')::text` and scans into a `*string`;
  `to_regclass` returns the qualified relation text when the table is visible on
  the search_path, or **NULL** (→ nil pointer) when absent → wrapped
  `errs.ErrNotFound` naming the unapplied `"events"` source, before the host
  serves traffic. (turso's twin is the `sqlite_master` lookup.) README states the
  prerequisite loudly.
- **Registration** — `go.work` (`./features/events/stores/pgx` inserted before
  `./features/events/stores/turso`); Makefile `MODULES` (`features/events/stores/pgx`
  before `features/events/stores/turso`), `STORE_MODULES` (now 8: pgx before turso
  events legs), and a `test-stores` pgx leg (`cd features/events/stores/pgx && go
  test ./...`, plain — no tag). Makefile header comment count `28 → 30`. go.work ↔
  Makefile `MODULES` agree at **30** entries (the plan's "29" predates
  `features/cms/views/templ`; adjustment logged here).

- **Verify (hermetic):** `cd features/events/stores/pgx && go build ./... && go
  test ./... && go vet ./...` — green (`ok … 0.221s`, `TestExportMigrations`
  runs). Loud skip recorded without env: `go test -v` →
  `--- SKIP: TestConformance` / `--- SKIP: TestAppendTx` —
  `POSTGRES_TEST_DSN not set — postgres conformance NOT verified`, suite still
  `PASS`. (No `-tags=integration` vet leg — this tree uses no build tag.)
- **Verify (gates):** `make guard` — six guards green. `make check` — "all checks
  passed" at **30 modules** (physically 30 — the plan's "29" predates
  views/templ, adjustment logged).
- **LIVE LEG (docker, milestone-close artifact), dated 2026-07-08:**
  `docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=postgres --name
  events-pgx-test postgres:17` (container `9c2356983a3d`), `pg_isready` green after
  2s. Suite **`features/events/stores/pgx`** with
  `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable'
  go test -v -count=1 ./...`:
  - `TestAppendTx` — **PASS (0.06s)**: `AppendTx` inside `InTx` visible after
    commit; rolled back on a sentinel error leaves no row.
  - `TestConformance` — **PASS (0.27s)**, 5/5 subtests: `AppendAndListOrder`
    (0.05s), `UnpublishedOnly` (0.06s), `MarkPublishedIdempotence` (0.05s),
    `PurgePublishedRetention` (0.07s), `EventIDUniqueness` (0.04s).
  - `TestExportMigrations` — **PASS**. Total `ok … 0.511s`. Migration
    `0001_event_outbox.sql` applied once (checksum `0867fbb9`), reused across
    subtests. **3 top-level PASS / 0 FAIL**, single executor.
  - **Docker lifecycle:** `docker stop events-pgx-test` → container removed
    (`--rm`), confirmed gone; port `55432` confirmed free.
- **Standing real-interaction check:** `examples/minimal` booted on `:8081` —
  `GET /` → **200**, `GET /products/widget-3000` → **200**, process killed, port
  8081 freed. Did NOT commit.

### 2026-07-08 — Phase 4 (tasks 9–10, design-phase 6) complete — DoD holds

Both store trees landed; the phase gate is green. Restating the Phase 4 DoD
lines against the artifacts:

- **"the phase-2 `storetest` suite (R4) executed by `stores/turso` (live leg
  `-tags=integration`, playground DB) and by `stores/pgx` (live leg
  `POSTGRES_TEST_DSN`)"** — ✅ both live runs recorded above (task-9: playground
  turso, 6 top-level PASS; task-10: dockered postgres:17, 3 top-level PASS). Both
  trees pass **ONE** `storetest.Run` conformance suite.
- **"canonical migrations source `"events"` with identical version sets across
  both trees"** — ✅ both carry exactly `migrations/0001_event_outbox.sql`, source
  `"events"`, per-dialect content (turso TEXT/ISO-8601; pgx TIMESTAMPTZ + JSON).
- **"`AppendTx` per-store tested against its own integration"** — ✅ `TestAppendTx`
  green on each (turso live 2.56s; pgx live 0.06s): commit-visible / rollback-clean.
- **"boot-time probe in both constructors"** — ✅ turso `sqlite_master` lookup; pgx
  `to_regclass`; each maps absence → wrapped `errs.ErrNotFound` naming `"events"`.
- **"`make check` green at 29 modules"** — ✅ green; **count adjusted to 30**
  physically (the plan's "29" predates `features/cms/views/templ`; logged).
- **"live runs recorded as dated NOTES.md artifacts at milestone close"** — the two
  dated live-run records are captured here in the execution log; the NOTES.md
  milestone entry is task-15's docs-sync deliverable (phase 6).

Registration parity: `go.work` ↔ Makefile `MODULES` agree at 30; `STORE_MODULES`
= 8 (both events legs); `test-stores` runs both events legs (pgx plain, turso
`-tags=integration`).

### 2026-07-08 — task-11 (mount the gateway — default variant, best-effort) executed

Wiring only (rule 8, FS2 method form) — two files:
`examples/auth-cms/cmd/server/main.go`, `examples/auth-cms/README.md`, plus
`examples/auth-cms/go.mod`. No feature/port/store changes.

- **go.mod** — added `require github.com/gopernicus/gopernicus/features/events
  v0.0.0` + the sibling `replace … => ../../features/events`.
- **Driver-free graph re-asserted (host doc-comment claim)** —
  `GOWORK=off go list -m all | grep -i libsql` → **empty (exit 1)** from
  `examples/auth-cms`. The feature and its stores never pull a driver into the
  host graph. (Under the workspace `go list -m all` sees the turso store module's
  libsql via `go.work`, which is why the host's own doc comment pins the check to
  `GOWORK=off` — the module-graph form.)
- **Wire** — import aliased `eventsfeature` (O5: package is `events`, `sdkevents`
  already taken by the sdk bus). After `authSvc.Register`/`cms.Register` and the
  cache-invalidation subscriber, before the host demo/debug routes:
  `eventsSvc, err := eventsfeature.NewService(eventsfeature.Repositories{},
  eventsfeature.Config{Bus: bus, StreamMiddleware:
  []web.Middleware{authSvc.RequireUser}})` then `eventsSvc.Register(mount)`. The
  **same** `bus` instance flows to both `Mount.Events` (cms emitter) and
  `Config.Bus` (gateway consumer) — one fan-out. `Repositories.Outbox` nil ⇒
  direct-emit; `Authorize` nil ⇒ resource route not registered; no `Identity`
  field (A-I1 E2 — `RequireUser` stashes the `identity.Principal` the gateway
  reads). Boot log confirmed `registered events feature resource_streams=false
  durable_outbox=false`.
- **Route surface / prefix decision** — the host mounts every feature at the
  root router (no prefix; same as cms/auth), so the subject stream lands at
  **`GET /events`** — exactly the protocol's curl URL. The resource-scoped
  `/events/{resource_type}/{resource_id}` is absent (nil `Authorize`).
- **README** — added "Leg 6 — the events SSE stream" (route `/events`, 401 auth
  requirement, direct-emit/best-effort, metadata-only body + `id:` correlation,
  `: ping` heartbeat cadence, the open-stream + admin-edit + reload-public curls)
  and an events bullet in the Route surface section.

- **Verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet
  ./...` all green (memstore + authmem suites ok; cmd/server no test files).
  `make check` → "all checks passed" (30 modules; six guards green; templ no
  drift).

- **Run-and-look — the real-interaction protocol steps 1–4, verbatim.** Server
  `AUTH_JWT_SECRET=… go run ./cmd/server` on `localhost:8082` (default variant,
  `EVENTS_OUTBOX` unset). Commands + observed frames/statuses:

  **Step 1 — register + login with a cookie jar** (`/tmp/jar`):
  ```
  curl -s -o /dev/null -w "%{http_code}" -c jar -b jar http://localhost:8082/articles
    -> 401                          # no session, admin gate closed
  curl -sX POST http://localhost:8082/auth/register -H 'Content-Type: application/json' \
    -d '{"email":"admin@example.com","password":"correct horse battery staple","display_name":"Admin"}'
    -> 201
  # mailer log (console sender): text="Your verification code is: uxzdjpmbencedxkdacr4goc4f4"
  curl -sX POST http://localhost:8082/auth/verify -H 'Content-Type: application/json' \
    -d '{"code":"uxzdjpmbencedxkdacr4goc4f4"}'
    -> 200
  curl -s -D - -c jar -b jar -X POST http://localhost:8082/auth/login -H 'Content-Type: application/json' \
    -d '{"email":"admin@example.com","password":"correct horse battery staple"}'
    -> HTTP/1.1 200 OK
       Set-Cookie: session=oehqpq7ayikypiv2sjythc2glu; Path=/; HttpOnly; SameSite=Lax
  curl -s -o /dev/null -w "%{http_code}" -c jar -b jar http://localhost:8082/articles
    -> 200                          # authed admin GET
  ```

  **Step 2 — open the stream; heartbeat comment frames arrive (~25s cadence):**
  ```
  curl -N -b jar http://localhost:8082/events      # stream opens, stays open
  ```
  After ~26s the open capture accumulated the heartbeat comments (raw bytes):
  ```
  : ping

  : ping

  : ping
  ```

  **Step 3 — in another session, edit a seeded cms entry → `content.updated`
  frame on the open stream:**
  ```
  ID=$(curl -s -b jar http://localhost:8082/articles | grep -o '/articles/[a-z0-9]*/edit' \
        | head -1 | sed 's#/articles/##;s#/edit##')   # -> odgfhfpmvn3il6iynr7yc4slvm ("Bring your own stores")
  curl -s -o /dev/null -w "%{http_code}" -b jar -X POST "http://localhost:8082/articles/$ID" \
    --data-urlencode 'title=Bring your own stores (edited)' \
    --data-urlencode 'excerpt=Both features run on in-memory stores; no libsql in the graph.' \
    --data-urlencode 'body=Swap datastores without forking a feature.' \
    --data-urlencode 'author=demo' --data-urlencode 'status=published'
    -> 303                          # admin update; cms emits content.updated async
  ```
  Raw SSE bytes observed on the open `/events` stream (metadata-only body, `id:`
  = correlation id — gate edit 1 best-effort path):
  ```
  event: content.updated
  id: vtr6y7dq64iqqadda76ebyr7oq
  data: {"type":"content.updated","occurred_at":"2026-07-08T17:41:07.115485Z","aggregate_type":"entry","aggregate_id":"odgfhfpmvn3il6iynr7yc4slvm"}

  event: content.updated
  id: 4zyvr66gyulwq5m6rlmhswlt6y
  data: {"type":"content.updated","occurred_at":"2026-07-08T17:41:07.115494Z","aggregate_type":"entry","aggregate_id":"odgfhfpmvn3il6iynr7yc4slvm"}
  ```
  (Two frames per admin edit: cms `entrysvc` emits `content.updated` on both the
  `Edit` and the trailing `SetTerms` write — observation, not a task defect.)
  Public reload after the edit — `curl -s http://localhost:8082/` contained
  `Bring your own stores (edited)`: fresh content, phase-3 cache invalidation
  holds.

  **Step 4 — unauthenticated stream → 401 (`RequireUser` fails closed):**
  ```
  curl -s -N -w "status=%{http_code}" http://localhost:8082/events
    -> status=401
       {"message":"authentication required","code":"unauthenticated"}
  ```

  `SIGTERM` → processes stopped clean; `lsof -iTCP:8082 -sTCP:LISTEN` → port 8082
  free. Did NOT commit. (Steps 5–6 — the `EVENTS_OUTBOX=memory` poller variant and
  shutdown-order log — are task-12's gate.)

### 2026-07-08 — task-12 (`outboxmem` + poller variant + shutdown ordering) executed

Four files: `examples/auth-cms/internal/outboxmem/{outboxmem.go,outboxmem_test.go}`,
`examples/auth-cms/cmd/server/main.go`, `examples/auth-cms/README.md`. No
feature/port/store changes; zero driver added to the host graph.

- **`internal/outboxmem`** — the example-local, runnable in-memory
  `outbox.EntryRepository` (R3/S6: no `stores/memory` module; the runnable
  memstore is host-local, the hermetic reference stays test-scoped in
  `features/events/storetest`). `Store` is a mutex-backed
  `map[string]*outbox.Entry` keyed by EventID; it HAND-ENFORCES EventID
  uniqueness exactly like the storetest reference — `Append` pre-checks the whole
  batch against existing rows AND a within-batch `seen` set before writing any
  row (a colliding batch commits nothing → `errs.ErrAlreadyExists`), so the
  honesty proof is real, not vacuous. `ListUnpublished` CreatedAt-ascending
  (EventID tie-break), `MarkPublished` idempotent, `PurgePublished`
  strictly-before. `var _ outbox.EntryRepository = (*Store)(nil)`.
- **Honesty proof** — `outboxmem_test.go`'s `TestConformance` runs the FULL
  shared `features/events/storetest.Run` suite against `outboxmem.New()` (the
  auth-cms memstore precedent). The suite's `EventIDUniqueness` case passes only
  because `Append` genuinely enforces the primary-key invariant; a store that
  overwrote/blindly-appended a duplicate would fail it. Race-clean (`0.2–1.3s`
  under `-race`).
- **Variant selection** — `durableOutbox()` reads `EVENTS_OUTBOX=memory`. When
  set, the host builds `outboxmem.New()` and wires
  `eventsfeature.Repositories{Outbox: outboxStore}` into `NewService` (boot log
  `durable_outbox=true`); default keeps `Repositories{}` (direct-emit,
  `durable_outbox=false`). The gateway is unchanged either way — only the emit
  path in front of the shared bus differs.
- **Poller drive (gate edit 2)** — `eventsfeature.NewPoller(outboxStore, bus)` on
  a `workers.NewPool(poller.Poll, WithName("outbox-poller"), WithWakeChannel(wake),
  WithLogger(log))`. The pool runs on its OWN `context.WithCancel(context.Background())`
  context in a goroutine (`poolDone` closed on exit). A host-owned
  `POST /outbox-demo` handler builds `sdkevents.NewRecord(NewBaseEvent("demo.outbox")
  .WithAggregate("demo","outbox-demo"))`, `Append`s it, then does a non-blocking
  cap-1 send on `wake` — the canonical append-then-signal pattern. `WakeChannel(bus,…)`
  was NOT used (it fires on bus emits, not the append; the poller's own emits
  would self-wake it — plan rationale). cms never touches the outbox (O2).
- **Shutdown order (§7, P3 idiom)** — documented in `main.go`'s run-tail comment
  and observed live: `web.Run` returns (parent ctx already canceled, HTTP drained
  on run.go's own fresh ctx) → cancel the pool's Background-derived context +
  bounded 5s wait on `poolDone` → `bus.Close` on a fresh
  `context.WithTimeout(context.Background(), 5s)`. Order: **HTTP server → poller
  pool → bus.Close**. The poller-on-its-own-context is why the closed-bus edge
  never fires (poller stops before Close).
- **main.go package doc** — extended to name features/events alongside cms +
  authentication (task-11 flag), with a paragraph on the two rails
  (direct-emit default vs `EVENTS_OUTBOX=memory` durable) and the shutdown order.
- **README** — added "Leg 6b — the durable outbox variant (`EVENTS_OUTBOX=memory`)"
  (outbox→poll→emit→SSE, the `POST /outbox-demo` curl, `id:` = durable EventID vs
  the direct rail's CorrelationID, the HTTP→poller→bus.Close shutdown order) and a
  `/outbox-demo` note in the Route surface.

- **Verify:** `cd examples/auth-cms && go build ./... && go test -race ./... &&
  go vet ./...` → all green (outboxmem conformance ok; memstore + authmem ok;
  cmd/server no tests). `make check` → **"all checks passed"** (30 modules, six
  guards green, templ no drift, integration-tag vet incl. events/stores/turso).

- **Run-and-look — protocol steps 5–6, verbatim.** Server
  `AUTH_JWT_SECRET=… EVENTS_OUTBOX=memory go run ./cmd/server` on `localhost:8082`.
  Boot log confirmed `registered events feature resource_streams=false
  durable_outbox=true` and `events durable outbox variant ENABLED
  (EVENTS_OUTBOX=memory) outbox="in-memory (internal/outboxmem)"
  trigger="POST /outbox-demo"`, plus `worker pool starting pool=outbox-poller`.

  **Step 5 — durable rail via outbox → poller → bus → SSE.** Register
  `admin@example.com` (201) → verify with mailer-logged code
  `pzjy3rt5isuwuzphuc4vchbndq` (200) → login cookie jar (200,
  `session=tu2cknfl2jrjwy6xffu2hfwyti`) → authed `GET /articles` (200). Opened
  `curl -sN -b jar http://localhost:8082/events`, then in a second session:
  ```
  curl -sX POST -b jar http://localhost:8082/outbox-demo
    -> 202  {"event_id":"glfp4qmtyvfvsj7atfa4xfstiu"}
  ```
  Frame observed on the open stream **37ms** after the POST (sub-second —
  observably NOT the 30s idle interval), raw bytes:
  ```
  event: demo.outbox
  id: glfp4qmtyvfvsj7atfa4xfstiu
  data: {"type":"demo.outbox","occurred_at":"2026-07-08T17:52:21.363829Z","aggregate_type":"demo","aggregate_id":"outbox-demo"}
  ```
  **Provenance check:** the SSE `id:` = `glfp4qmtyvfvsj7atfa4xfstiu` is EXACTLY the
  POST response `event_id` — the durable outbox EventID the poller's rehydrated
  `outboxEvent.EventID()` surfaces, NOT a CorrelationID. This differs in
  provenance from the direct-emit variant (task-11 step 3, where `id:` was a
  per-request correlation id `vtr6y7dq64iqqadda76ebyr7oq`): the durable rail's id
  is the row primary key / de-dupe key.

  **Step 6 — SIGTERM → documented shutdown order.** `kill -TERM <pid 13079>`.
  Server log tail, verbatim (order HTTP server → poller pool → bus.Close):
  ```
  msg="server shutting down"
  msg="server stopped"
  msg="stopping outbox poller pool"
  msg="worker stopped" pool=outbox-poller worker_id=outbox-poller-worker-1
  msg="worker pool stopped" pool=outbox-poller runtime=1m11.378635667s stats="{ActiveWorkers:0 Iterations:4 Errors:0 Panics:0}"
  msg="outbox poller pool stopped"
  msg="closing event bus"
  ```
  Exit clean (Errors:0 Panics:0); `lsof -iTCP:8082 -sTCP:LISTEN` → **port 8082
  free**. Did NOT commit.

### 2026-07-08 — Phase 5 (tasks 11–12, design-phase 7) complete — DoD holds

The proof host mounts the gateway in BOTH variants and the mandatory
real-interaction protocol passed and is recorded verbatim across the two task
entries. Green tests alone did not close it. Phase 5 DoD lines against artifacts:

- **"`examples/auth-cms` mounts the gateway (default: `Outbox: nil`, best-effort
  — O2)"** — ✅ task-11: `NewService(Repositories{}, Config{Bus, StreamMiddleware:
  RequireUser})`, `durable_outbox=false`; direct-emit `content.*` frames observed
  (steps 1–4), `id:` = CorrelationID.
- **"a flag-selected second variant proving the durable rail on the example-local
  in-memory outbox"** — ✅ task-12: `EVENTS_OUTBOX=memory` wires
  `Repositories{Outbox: outboxmem}` + `NewPoller` on an `sdk/workers` pool with
  the append-then-signal wake; `POST /outbox-demo` → frame via outbox→poller→bus
  in 37ms, `id:` = durable outbox EventID (distinct provenance from the direct
  rail — verified against task-11's correlation-id frame).
- **"the real-interaction protocol below passed and recorded verbatim (commands,
  ports, observed frames)"** — ✅ steps 1–4 in the task-11 entry, steps 5–6 in the
  task-12 entry: exact curls, ports (8082), raw SSE bytes, and the verbatim
  shutdown-order log lines.
- **"shutdown order HTTP → poller → `bus.Close` observed clean"** — ✅ step 6 log
  tail above; port freed, Errors:0/Panics:0.
- **Zero-infra proof (charter §3)** — memory bus + in-memory outbox + poller + SSE
  over `go run`, no driver in the graph (`GOWORK=off go list -m all | grep -i
  libsql` empty, re-asserted task-11).

Artifacts: `make check` green at 30 modules (six guards); the two dated protocol
transcripts (direct-emit + durable) with observed frames, latencies (34ms
invalidation / 37ms durable pickup), and shutdown log lines.

### 2026-07-08 — task-13 (rule-6 feature-isolation guard) executed

One file: `Makefile` (plus two temporary prove-can-fail edits, reverted). The
tree at HEAD carried task-12; the untracked `features/events/README.md` is
task-14's in-flight work, left untouched.

- **Label adjustment (logged per the leg's instruction):** the plan calls this
  guard "G5", but the Makefile's numbering moved on since the plan was cut — the
  FS1 guard (feature-standard 2026-07-07) already took the G5 slot, and there
  were six guards (G1–G6) on the clean tree. This guard therefore lands as **G7,
  `guard-feature-no-cross-feature`**, the next free number/name consistent with
  the existing `guard-*` targets. It is the same rule-6 semantics the plan
  specified for "G5"; only the label changed. task-15's docs sync should record
  it as G7 (not G5) in the guard enumeration.
- **Guard shape (matches G2/G5/G6):** a `.PHONY` entry, an `== guard: ... ==`
  echo line, and membership in the `guard:` aggregate (comment updated "all six"
  → "all seven"). Matching logic: for each `features/<x>/` it greps the whole
  subtree for `"github.com/gopernicus/gopernicus/features/[a-z0-9]+` imports and
  drops self-imports (`features/<x>/…`) with a boundary-anchored `grep -v`
  (`features/<x>(["/])`); what survives is an x-file reaching into some
  `features/<y>`, y ≠ x → loud error naming rule 6, else nothing + exit 0.
- **Exclusions rationale:** `stores/` is excluded via `--exclude-dir=stores`
  (separate adapter modules, per the task spec, mirroring G2's stores
  exclusion). `views/` is deliberately **NOT** excluded — the concern was that
  `features/cms/views/templ` legitimately imports its OWN core, but that is a
  self-import (y == x) dropped by the `grep -v` filter, so it never
  false-positives; leaving views scanned still catches a views adapter reaching
  a FOREIGN feature. Verified green with the current tree (cms/views/templ →
  cms/… present and correctly ignored).
- **FS1 list addition (tracked from task-3's log):** the FS1 guard's hardcoded
  module list `for f in features/authentication features/cms features/jobs`
  gained `features/events` → `… features/authentication features/cms
  features/events features/jobs`. The events core is sdk-only and is now held to
  it by the guard, not just by grep.
- **Prove-can-fail #1 (G7, A4):** temporarily added `_
  "github.com/gopernicus/gopernicus/features/authentication"` (path per A-R1) to
  `features/events/events.go`; `make guard-feature-no-cross-feature` failed with
  exit 2:

  ```
  == guard: no feature core imports a different feature (rule 6) ==
  ERROR (rule 6): events reaches into a different feature core — declare a port and let the host wire the peer:
  features/events/events.go:34:	_ "github.com/gopernicus/gopernicus/features/authentication"
  make: *** [guard-feature-no-cross-feature] Error 1
  ```

  Reverted; `make guard` green.
- **Prove-can-fail #2 (FS1 scans events, A4):** temporarily added `require
  example.com/fake v1.0.0` to `features/events/go.mod`; `make
  guard-feature-core-sdk-only` failed with exit 2, proving events is now in the
  FS1 scan:

  ```
  == guard: feature core go.mod requires sdk only (FS1) ==
  ERROR (FS1): features/events/go.mod requires more than sdk:
  example.com/fake
  make: *** [guard-feature-core-sdk-only] Error 1
  ```

  Reverted; `make guard` green.
- **Guard count:** **seven** on the clean tree (G1–G7). The task's "8 presumably"
  was off by one — the G5 slot was already spent on FS1, so this leg adds exactly
  one guard (six → seven), not two.
- **Verify:** `make guard` green (7 `== guard:` lines); full `make check` green
  (exit 0) at 30 modules, all seven guards. Only `Makefile` changed;
  `features/events/{events.go,go.mod}` diff-clean after reverts.

### 2026-07-08 — task-14 (feature README + wiring-tour page) executed

- **What:** `features/events/README.md` written (auth/jobs README shape):
  trio layout; `/events/*` route table with the prefixability note (C1);
  Config nil-semantics table (nil Bus = ErrBusRequired; the LOUD
  StreamMiddleware requirement — absent principal ⇒ every stream 401s,
  fails closed, A-I1 E5); the §3 two-emit-paths guarantee table reprinted
  verbatim; per-rail delivery + SSE id: provenance (best-effort =
  CorrelationID no de-dupe; durable = outbox EventID, consumers de-dupe —
  gate edit 1); single-poller assumption (+ the non-Broadcaster
  single-instance warning); MaxConnAge revocation posture (P5 no-disable);
  the `events` migration-source prerequisite + boot probe; O5 aliasing
  note; the unguarded-appender-seam note (risk 3). Top-level "Wiring: live
  updates end-to-end" section: one ascii diagram of the five stops with
  stop 4 starred as the substitution point; ONE listing assembled verbatim
  from examples/auth-cms/cmd/server/main.go (the executable twin, named as
  such — elisions explicitly marked); the stores/turso swap as a labeled
  snippet (constructor + scaffold-and-own migration step, read against the
  store README). The bus-fed WakeChannel variant is not shown (the twin
  uses the plain cap-1 wake channel; nothing to correct per gate edit 3).
- **Verify (gate edit 4 fresh-eyes):** `make guard` green (incl. the new
  G7). Stops 1–3 + 5 line-for-line: six key lines grep-verified VERBATIM
  in the twin (bus construction, Mount literal, NewService call, pool
  construction, bounded close context, DeletePattern call). Stop 4
  port-equivalence: outboxmem passed the full storetest suite under -race
  (task-12's conformance run). The swap-variant listing pasted into a
  scratch module (GOWORK=off, path replaces) and built once —
  "SWAP SNIPPET COMPILES": compiling verified, not asserted.

### 2026-07-08 — task-15 (repo docs sync + records) executed — phase 6 + milestone COMPLETE

- ARCHITECTURE.md: tree gains cms views/templ + the three events rows;
  "Twenty-six modules today" → thirty; sdk services enumeration gains
  `identity` (A-I1 E6). README.md: listing + heading → thirty (views/templ
  + events rows added); "six layering guards" → seven (both mentions).
  RELEASING.md: twenty-seven → thirty, events added to the enumeration.
  Makefile counts were already current (tasks 9/10/13).
- sdk/README.md: events row cross-references features/events (closes the
  straddle perception artifact); `identity` row added with the A-I1.1
  admission trace (E6).
- features/README.md §5 corollary marked CASHED for identity-in-context
  (sdk/identity; the illustrative CurrentUser port stays as the general C2
  pattern).
- roadmap/00-intersections.md: §2 events Config.Identity row + §3 identity
  seam row carry dated AMENDED markers citing A-I1; §2's prose adjusted.
  G2 verified generic (no edit needed); G7 was task-13's.
- Design doc header: dated status amendment — phases 3–8 EXECUTED via this
  plan; S1–S6/gate-edits/P1–P5/A-I1/A-R1 pointed at the records; the P5
  micro-amendment recorded (O7 "hosts can set 0 explicitly" superseded).
- NOTES.md: the dated events-v1 CLOSED milestone entry (shipped scope,
  both live-store artifacts, the phase-5 protocol summary, deltas of
  record, open flags for jrazmi).
- Verify: stale-count grep over the five docs returns nothing; full
  `make check` green (30 modules, seven guards).
