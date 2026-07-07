# telemetry-closeout — telemetry closeout + hygiene sweep + demand-gated ledger

Status: **RATIFIED 2026-07-07 (jrazmi)** — all five ratification items at
their defaults, TC1–TC5 (NOTES.md 2026-07-07 "planning wave RATIFIED" entry).
Review gate run 2026-07-07 (architecture-steward / platform-sre /
product-manager), all ratify-with-amendments; amendments applied below.

**R10's standalone telemetry milestone is demoted to this closeout — RATIFIED
FACT (TC1, 2026-07-07).** The roadmap (`.claude/plans/roadmap/00-intersections.md`
R10; `loop-handoff.md`'s "Telemetry (sdk/tracing) is AFTER all of the above")
sequenced telemetry as its own milestone. Nearly all of that surface has since
shipped inside sdk-parity and fast-follows (2026-07-06): `sdk/tracing` port +
`Noop`, `integrations/tracing/otel` (stdout/OTLP/provider exporters),
trace/span IDs in `sdk/logging` (`TracingHandler`, `WithTraceID`/`WithSpanID`),
`sdk/workers.WithTracer`, goredis `LoggingHook`/`TracingHook`. What remains is
one middleware and one end-to-end proof — not a milestone. The 2026-07-06
planning-wave NOTES.md entry recorded the demotion as PROPOSED,
ratify-at-plan-review; the 2026-07-07 ratification entry confirms it.

## Context

Three light workstreams: (1) close the telemetry capability — the
request-scoped span middleware the capability map ratified into `sdk/web`
("bundled with the tracing decision, not built this phase"), plus a
real-interaction observability proof on one example host; (2) a hygiene sweep
of small flagged debts from the 2026-07-02/2026-07-06 NOTES.md entries; (3) a
demand-gated deferral ledger so every deliberately-deferred item carries an
explicit wake-up TRIGGER instead of evaporating. Salvage reference for the
middleware: `gopernicus-original/bridge/transit/httpmid/telemetry.go`.

## Goal

Telemetry is closable as a capability (middleware shipped + real spans observed
end-to-end on a real host), the flagged hygiene debts are fixed or explicitly
closed, and every open deferral has a recorded trigger in NOTES.md.

## Definition of Done

- `web.Tracing(tracing.Tracer) Middleware` lives in `sdk/web`, tested, with all
  four guards green (`make guard`).
- Real spans observed for a real request flow on `examples/cms` via
  `integrations/tracing/otel` (stdout exporter), with `trace_id`/`span_id`
  appearing on the request log lines — exact commands + span excerpts recorded
  in this plan's execution log. **Green tests alone never close workstream 1.**
  If playground Turso creds don't materialize, the drive runs on the named
  local fallback (task-2: `examples/cms` on a `file:///` libsql DSN) — the
  drive is datastore-independent; only the DSN class of the evidence changes.
- All flag dispositions in the table below are executed (code landed) or closed
  (ruling recorded), per the five ratification items ruled 2026-07-07 at their
  defaults (TC1–TC5).
- The demand-gated ledger entry (drafted verbatim in this plan) is appended to
  NOTES.md.
- Fresh `make check` green across all 26 modules; module count and go.work /
  Makefile MODULES unchanged.

## Out of scope

- **otel W3C trace-context propagation helpers** — ruled wait-until-needed
  2026-07-06. Do not reopen; it rides the ledger. Consequence accepted
  consciously: every request starts a fresh root trace (inbound `traceparent`
  ignored, no cross-service stitching).
- Metrics — no original capability exists (capability map, Telemetry section);
  any metrics work is new scope needing its own plan.
- The C1 non-root-prefix link **fix** — assess-only here (task-6);
  `features/README.md` §4 already concluded future-milestone scope, so no fix
  executes in this milestone. C1 also gets a ledger row (trigger-gated).
- **Tracked in their own design docs, not this ledger:** events-v1 (resumes at
  its phase 3 per the amended events design) and auth-v2 scope including the
  flagged auth-v1 product debts (login-not-gated-on-verification, unrouted
  ChangePassword, session-token hashing — owned by the auth-v2 design doc per
  the 2026-07-06 authorization ruling). **Demand-gated in this ledger:** jobs
  v2 / `Mount.Jobs`, tenancy, and the other ledger rows below — deferred with
  explicit triggers, not built.
- `examples/minimal` stays otel-free / Noop-only: its charter (doc comment in
  `examples/minimal/cmd/server/main.go`) is a module graph of only
  `features/cms` (+ theme deps) + `sdk`. No guard enforces this — it is a named
  constraint of this plan, like the libsql-free proof it already carries.

## Schema / datastore impact

None. No SQL, no migrations, no EAV spine, no store adapters touched.

## Module / API impact

- `sdk/web`: new exported `Tracing(t tracing.Tracer) Middleware` (additive;
  intra-sdk edge `sdk/web` → `sdk/tracing` passes `guard-sdk-stdlib`, which
  excludes the sdk's own module path).
- `sdk/tracing`: gains the NAMED optional interface `SpanIdentity`
  (`TraceID() string; SpanID() string`) — steward ruling at the review gate
  flipped this from structural-in-web; **ruled TC5, stands — no flip-back**
  (see Consultation notes; ratification item #5). Additive; implementations
  without stable span identity simply omit it. This does not breach
  port-minimality: trace/span-ID identity is already sdk kernel vocabulary
  (`sdk/logging.WithTraceID`/`WithSpanID`).
- `integrations/tracing/otel`: the unexported `spanFinisher` gains
  `TraceID() string` / `SpanID() string` methods plus the compile assertion
  `var _ tracing.SpanIdentity = (*spanFinisher)(nil)` (additive).
- `examples/cms/go.mod`: + `require`/`replace` for
  `github.com/gopernicus/gopernicus/integrations/tracing/otel` (example hosts
  are never tagged per RELEASING.md — no tagging implication).
- `integrations/filestorage/gcs`: NO change — the credentials swap is
  **DEFERRED, ruled TC4 (2026-07-07)**; task-4's swap-now branch is not taken.
  No `cloud.google.com/go/auth` indirect→direct promotion.
- `features/jobs`: `Config` gains an optional `Logger *slog.Logger` field
  (additive, non-breaking).
- Repo is untagged (first tags belong to the repo-hardening milestone), so the
  additive API changes carry no release action now.

## Generated-artifact impact

None. No `.templ` sources touched — C1 is assess-only and its fix is
future-milestone scope by prior conclusion (`features/README.md` §4); the
eventual fix edits `.templ` sources + `make generate`, never `*_templ.go`.

## Flag dispositions (workstream 2 checklist)

Each item cites its flag origin; dispositions marked "record" are executed by
task-7 after ratification.

- [ ] **Stale `sdk/ratelimiter` docs** — origin: NOTES.md 2026-07-06 throttler
  entry ("Stale-doc note"). Three stale spots in
  `sdk/ratelimiter/ratelimiter.go`: package comment says implementations live
  in a `memorylimiter/` subpackage (line 2); `Limiter` doc says "Memory, Redis,
  and SQLite backends satisfy it" (SQLite was dropped; redis is
  `goredis.Limiter`); usage example calls `memorylimiter.New()`. → **task-3**.
- [ ] **Stale `sdk/tracing` package doc** — origin: found in this planning
  pass. `sdk/tracing/tracing.go` still says the richer vocabulary "lives in
  the **future** integrations/tracing/otel module" and "Exporters are the
  **deferred** … fast-follow" — otel shipped in fast-follows. → **task-1**
  (same-module touch).
- [ ] **gcs deprecated `option.WithCredentialsJSON`** — origin: NOTES.md
  2026-07-06 throttler entry ("Also pre-existing"). **SRE critical correction
  at the review gate:** the swap this plan first specified
  (`option.WithAuthCredentials` over `credentials.DetectDefault`) pre-builds
  credentials with NO OAuth scopes — the storage client short-circuits on
  pre-supplied creds and silently drops the `devstorage.full_control`/
  `cloud-platform` scopes it normally injects → 403 on every object op in the
  first real host, invisible to hermetic tests (the emulator path uses
  `WithoutAuthentication`). NEVER ship that form. Default disposition:
  **DEFER** — close as WONT-DO-until-a-live-GCS-run, recording the verified
  correct form for the future fix:
  `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`
  (sets `DialSettings.AuthCredentialsJSON` so scope injection is preserved; no
  new direct dependency; verified present in the vendored
  google.golang.org/api v0.271.0). **RULED TC4 (2026-07-07): DEFERRED** —
  task-4's swap-now branch is not taken; the closure recording lands in
  **task-7**.
- [ ] **Runtime-logger Config knob** — origin: NOTES.md 2026-07-02 jobs-v1
  phase 8 ("Ergonomics flag: no Config knob for the runtime pools' logger
  (slog.Default)") + jobs-v1 close entry. → **task-5**.
- [ ] **Job backing-field rename (JobID/JobStatus/Retries)** — origin: NOTES.md
  2026-07-02 jobs-v1 close ("open to a pre-v1 rename if jrazmi prefers").
  → close **WONT-DO**: v1 shipped and is consumed (memstore, two dialect
  stores, storetest, examples/jobs-minimal); the rename is now a breaking
  change with zero behavior payoff. Record in task-7.
- [ ] **`AddAcronym`/`Caser` seam** — origin: NOTES.md 2026-07-06 sdk-parity
  entry, open flag #1. **Correction to this plan's own framing:** this flag is
  NOT parked — fast-follows task-0 already shipped the seam
  (`sdk/conversion/caser.go`: immutable `NewCaser(WithAcronyms(...))`, package
  funcs delegating to an immutable default). → close **ALREADY-SHIPPED**.
  Record in task-7.
- [ ] **`ß` → `s` vs `ss`** — origin: NOTES.md 2026-07-06 sdk-parity entry,
  open flag #3. **RULED TC2 (2026-07-07): keep `s`** — flag closes; no sdk/slug
  follow-on task (the `ss` branch is not taken). Record the closure in task-7.
- [ ] **C1: cms non-root-prefix link limitation** — origin: NOTES.md 2026-07-02
  ROADMAP LOOP FINAL SUMMARY open flags; documented limitation in
  `features/README.md` §4 and `restructure/00-overview.md` C1 row (views
  hardcode absolute links; prefixed routes serve 200 but in-page navigation
  404s). → **task-6, ASSESS-ONLY**: produce the forward-plan shape (inventory
  + seam recommendation + size); no fix in this milestone — future-milestone
  scope was already concluded by `features/README.md` §4. Also ledgered with a
  trigger (ledger entry below).
- [ ] **turso-vs-libsql naming** — origin: NOTES.md 2026-07-06
  kvstore-consolidation entry (R-KV2/R-KV3: "open flag if `libsql` preferred")
  + kvstore plan's open question. **RULED TC3 (2026-07-07): KEEP `turso`** —
  flag closes. Record the closure in task-7.

## Risks

1. **OTLP shutdown flush drop is invisible on the stdout path.** By the time
   `web.Run` returns, the signal-derived ctx is cancelled; `tracer.Shutdown`
   on that ctx makes the OTLP batch exporter bail on its final flush, while
   stdout (synchronous `WithSyncer`) hides the bug. Task-2 pins a fresh
   `context.WithTimeout(context.Background(), …)` for Shutdown and carries an
   optional docker OTLP leg to prove it.
2. **Playground Turso creds may not materialize.** Preferred drive is the
   playground DB (precedent: sdk-parity phase 6); the named local fallback
   (task-2: `examples/cms` on a `file:///` libsql DSN — everything under proof
   is datastore-independent) keeps the workstream closable, at the honest cost
   that the evidence's DSN class is local-file, not remote Turso. Never
   substitute a green-tests close or a Noop-only drive.
3. **Scope inflation via C1.** The link fix means base-path threading through
   every cms view — real scope. Task-6 is capped at producing the forward-plan
   shape; the fix stays future-milestone scope regardless of how small the
   assessment finds it.

## Tasks

Executor model policy (standing jrazmi rule): implementation tasks `model:
opus`; docs/judgment tasks `model: fable`; **never sonnet**.

### task-1: sdk/web request-scoped span middleware

- **depends_on:** []
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/web/middleware.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/web/middleware_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/tracing/tracing.go` (`SpanIdentity` interface + doc staleness fix)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/tracing/tracing_test.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test ./... && go vet ./... && cd .. && make guard`
- **description:** Add `func Tracing(t tracing.Tracer) Middleware` to `sdk/web`,
  mirroring the Logger/Panics shape. Nil tracer → `tracing.Noop{}` (the
  `workers.WithTracer` precedent). Design points, all load-bearing:
  - **Span name from `r.Pattern` alone** — the pattern already embeds the
    method (`"GET /foo/{id}"`); prefixing the method double-prints it. This
    works because `WebHandler.Handle` wraps middleware per-route *inside* the
    mux match, so `r.Pattern` is populated when middleware runs (unlike the
    original's outside-the-mux wrapping). Empty pattern (`HandleRaw` bypasses
    middleware anyway; defensive) → static name `"http.request"`, never
    `r.URL.Path` (cardinality).
  - Attributes via `tracing.StringAttribute`: `http.method`, `http.host`,
    `http.route` (when pattern non-empty), `user_agent`, peer host from
    `RemoteAddr`; after `next` returns, `http.status_code` from the package's
    own `statusWriter`.
  - **5xx → `RecordError` with a synthesized error** (e.g.
    `fmt.Errorf("server error: %d", status)`). Do NOT read the wrapper's
    `.err`: `web.RecordError` type-asserts the writer directly with no Unwrap
    walk, and Tracing sits outer of Logger (task-2 order), so the handler's
    recorded error lands on Logger's writer, not this one.
  - **Trace/span-ID → log linkage (option (a); integration-side stashing
    rejected; NAMED interface per the steward ruling at the review gate — see
    Consultation notes and ratification item #5):** declare
    `tracing.SpanIdentity` in `sdk/tracing/tracing.go` —
    `interface{ TraceID() string; SpanID() string }` — with the doc line
    "optional; implementations without stable span identity simply omit it."
    The middleware type-asserts it on the returned `SpanFinisher` and, when
    satisfied with non-empty IDs, stashes via
    `logging.WithTraceID`/`WithSpanID` before `r.WithContext(ctx)`. `sdk/web`
    already imports `sdk/logging` (RequestID does).
  - **Godoc ordering constraint is an explicit deliverable** (steward + PM):
    `web.Tracing`'s godoc MUST state "place outer of `web.Logger`" with both
    consequences — (i) the traced ctx propagates into the access log line
    (trace_id/span_id), (ii) `web.RecordError`'s direct statusWriter
    type-assert keeps landing on Logger's writer so the `error` log field
    doesn't silently regress. Precedent: `RequestID` carries the same class of
    doc-only ordering constraint — doc-only is acceptable, but it must live ON
    the reusable surface, not just in example wiring.
  - **Noop cost — accepted and documented (planner's call):** the middleware
    pays a per-request attribute-slice allocation + `RemoteAddr` parse even
    when wired with `tracing.Noop{}` — parity with `Logger`'s per-request attr
    build. No Noop fast-path; one godoc sentence states the cost so a host
    that cares simply omits the middleware.
  - Fix the stale `sdk/tracing` package doc while here ("future"/"deferred"
    integrations/tracing/otel wording — the module shipped) and point
    implementers at the `SpanIdentity` linkage convention.
  - Tests: span started/finished per request; name from pattern vs static
    fallback; status attribute; 5xx RecordError; Noop path inert (Noop does
    NOT satisfy `SpanIdentity`); a stub finisher implementing
    `tracing.SpanIdentity` proves the context carries trace/span IDs (assert
    via `logging.TracingHandler` output or context reads).

### task-2: otel finisher IDs + examples/cms wiring + observability proof

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/integrations/tracing/otel/tracer.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/integrations/tracing/otel/otel_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/cms/cmd/server/main.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/cms/go.mod` (+ go.sum)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/cms/.env.example` (CREATE — does not exist yet; only `.env` does)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make check` — **plus the run-and-look drive below, which is what closes workstream 1.**
- **description:** Three pieces:
  1. `otel.spanFinisher` gains `TraceID()`/`SpanID()` (from
     `span.SpanContext()`; return `""` when invalid) plus the compile
     assertion `var _ tracing.SpanIdentity = (*spanFinisher)(nil)` alongside
     the existing assertions. Test via the existing tracetest SpanRecorder
     path.
  2. Wire `examples/cms`: gate the **tracer choice, not the middleware** — when
     `TRACING_ENABLED=true`, build `otel.Open` with Config populated from the
     existing `TRACING_*` env tags (`environment.ParseEnvTags`); otherwise
     `tracing.Noop{}`. Register **`router.Use(web.RequestID(), web.Tracing(tracer),
     web.Logger(log), web.Panics(log))`** — Tracing MUST sit outer of Logger:
     (i) Logger emits its access line with `r.Context()`, so the traced ctx
     must already be on `r` for `trace_id`/`span_id` to appear; (ii)
     `web.RecordError`'s direct type-assert must keep landing on Logger's
     writer so the existing `error` log field doesn't silently regress. Defer
     `tracer.Shutdown` with a **fresh** `context.WithTimeout(context.Background(),
     shutdownTimeout)` — never the run-scoped ctx, which is already cancelled
     when `web.Run` returns (mirrors `web.Run`'s own `srv.Shutdown` pattern).
     Intentional output change: cms request logs gain `trace_id`/`span_id`
     (the dormant `logging.WithTracing()` wiring goes live). Create
     `examples/cms/.env.example` documenting the `TRACING_ENABLED` gate and
     each `TRACING_*` knob with a one-line comment (all non-secret), including
     the note that with tracing enabled, client IP + user-agent leave the
     process to the configured trace backend.
  3. **Real-interaction check (mandatory, closes the workstream):**
     `TRACING_ENABLED=true make run` against the playground Turso DB; drive a
     real flow — `curl -s http://localhost:8080/` plus a browser admin leg
     (login → edit an entry → save → view the public page). OBSERVE: stdout
     exporter JSON spans named by route pattern with status attributes, and
     `trace_id`/`span_id` on the corresponding request log lines. Record exact
     commands + span/log excerpts in this plan's Execution log.
     **Local fallback (named per SRE — the drive is never hostage to
     playground creds):** same host, local DSN — set
     `TURSO_DATABASE_URL="file://$PWD/local-dev.db"` (absolute path required;
     empty `TURSO_AUTH_TOKEN`). VERIFIED against the vendored driver, not assumed:
     libsql-client-go sql.go lists `file://` among its supported schemes, the
     turso module requires `modernc.org/sqlite` directly for exactly this
     path, `turso.Open` passes the DSN through untouched, and the migration
     runner reads the same `TURSO_DATABASE_URL` — so `make run`'s pre-boot
     migrate leg works against the local file too. `examples/auth-cms`
     (memstore-backed, credential-free) was considered and REJECTED as the
     fallback host: it carries none of the tracing wiring this task delivers,
     so driving it would mean wiring a second host — scope for no extra proof.
     Record which DSN class the evidence used. Optional-if-docker OTLP leg
     (proves the shutdown flush): run an OTLP collector (e.g. `docker run
     --rm -p 4317:4317 jaegertracing/all-in-one`), rerun with
     `TRACING_EXPORTER=otlpgrpc TRACING_OTLP_ENDPOINT=localhost:4317
     TRACING_OTLP_INSECURE=true`, SIGTERM the server, confirm the final
     requests' spans arrived. If docker is unavailable, record a loud skip.

### task-3: sdk/ratelimiter doc staleness

- **depends_on:** []
- **model:** fable
- **files:** `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/ratelimiter/ratelimiter.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go vet ./...`
- **description:** Fix the three stale doc spots (comment-only, zero behavior):
  package comment's `memorylimiter/` subpackage claim → `Memory` is in-package
  and `Acquire` is the blocking helper; `Limiter` doc's "Memory, Redis, and
  SQLite backends" → Memory in-package, `goredis.Limiter` as the external
  backend (SQLite dropped by ruling); the usage example's
  `memorylimiter.New()` → `ratelimiter.NewMemory()` (match the real
  constructor name in the file).

### task-4: gcs deprecated credentials option swap (RESOLVED: NOT TAKEN — TC4 ruled DEFER, 2026-07-07)

- **depends_on:** [] — **this task does not execute.** TC4 ruled the swap
  DEFERRED (wont-do until a live GCS run); task-7 records the closure. The
  task body below stays as the record of the corrected form for the future
  fix — whoever executes it at the trigger uses ONLY this form.
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/integrations/filestorage/gcs/gcs.go`
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/integrations/filestorage/gcs && go build ./... && go test ./... && go vet ./...` — **plus a mandatory live GCS conformance leg (`GCS_TEST_BUCKET` + creds) before close; hermetic green does not close a credential-path change.**
- **description:** ONLY the SRE-verified form:
  `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`
  (sets `DialSettings.AuthCredentialsJSON`, preserving the storage client's
  scope injection; no new direct dependency; present in the vendored
  google.golang.org/api v0.271.0 at option.go:201). **NEVER the
  `option.WithAuthCredentials(credentials.DetectDefault(...))` form this
  plan's first draft specified** — it pre-builds scope-less credentials that
  403 on every object op in a real host while staying invisible to hermetic
  tests (the emulator path uses `WithoutAuthentication`). `Config` surface
  unchanged. Rationale for the DEFER ruling: deprecated ≠ removed, the cost
  of waiting is nil, and an unverified credential-path change doesn't belong
  in a telemetry release.

### task-5: jobs runtime-logger Config knob

- **depends_on:** []
- **model:** opus
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/jobs.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs/jobs_test.go`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/jobs-minimal/` (one-line Config wire in its main)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/jobs && go build ./... && go test ./... && go vet ./... && cd ../../examples/jobs-minimal && go build ./... && go test ./... && go vet ./...`
- **description:** Add optional `Logger *slog.Logger` to `jobs.Config`
  (additive; nil keeps today's `slog.Default()` fallback) and thread it to the
  two internal seams that already have the field: `schedulesvc.Deps.Logger`
  (jobs.go NewService) and `runtime.Deps.Logger` (jobs.go NewRuntime — carry it
  on `Service`/`resolvedConfig`). `queuesvc.NewService`'s third param is a
  clock, not a logger — leave it. The field's godoc MUST include one sentence
  distinguishing it from `feature.Mount.Logger`: `Config.Logger` is the
  runtime pools' operational logger; `Mount.Logger` is registration-time
  logging — so no future reader "unifies" them by threading Mount into
  NewService. Test: a Config with a
  distinguishable handler-backed logger produces pool log lines through it
  (and nil still defaults). Wire `Logger: log` in examples/jobs-minimal's
  Config literal — the flag's origin host — as the one-line consumer proof.

### task-6: C1 non-root-prefix assessment (ASSESS-ONLY)

- **depends_on:** []
- **model:** fable
- **files:** `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.claude/plans/telemetry-closeout/c1-assessment.md`
- **verify:** deliverable file exists; no code changed by this task.
- **description:** Produce the **forward-plan shape** for the cms
  non-root-prefix link limitation (`features/README.md` §4;
  `restructure/00-overview.md` C1 row) — §4 already concluded
  future-milestone scope, so the deliverable is NOT a trivial-vs-own-plan
  decision but the plan a future milestone cuts from: (1) inventory of the
  hardcoded absolute links across cms views (`.templ` sources) and handlers;
  (2) recommended seam — base path via Mount/PrefixRegistrar surface vs a
  view-context value vs relative links, with the trade-offs; (3) size
  estimate. Do not fix anything in this task.

### task-7: flag closures + demand-gated ledger + doc sync

- **depends_on:** [task-1, task-2, task-3, task-5, task-6] — task-4 is not
  taken (TC4 ruled DEFER); task-7 records the gcs closure itself
- **model:** fable
- **files:**
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/NOTES.md`
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/README.md` (web row: tracing middleware mention **including the "place outer of web.Logger" ordering constraint** — steward/PM amendment: the constraint must be visible on the reusable surface's docs, not only in example wiring)
  - `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.claude/plans/restructure/capability-map.md` (execution note: Telemetry rows BUILT; transit-middleware row → ledger pointer)
- **verify:** docs-only; `make guard` (cheap confirmation nothing moved).
- **description:** Append TWO NOTES.md entries: (1) a short telemetry-closeout
  milestone entry recording what shipped, the drive evidence pointer (incl.
  which DSN class), and the flag closures — JobID/JobStatus/Retries WONT-DO;
  Caser ALREADY-SHIPPED (correcting the "parked" framing); the gcs swap
  disposition per TC4 (ruled DEFER 2026-07-07: closed
  WONT-DO-until-a-live-GCS-run, recording the verified correct form
  `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`
  and the scope-drop warning against the `WithAuthCredentials`/`DetectDefault`
  form); **ß closed as KEEP `s` (TC2, ruled 2026-07-07)** and
  **turso naming closed as KEEP `turso` (TC3, ruled 2026-07-07)** — cite the
  NOTES.md 2026-07-07 planning-wave ratification entry for all three rulings;
  C1 disposition pointing at task-6's forward-plan shape — each citing its
  origin entry; (2) the demand-gated deferral ledger, **verbatim from the
  "Ledger entry" section below** (fill the date). Sync the two doc surfaces
  listed.

### task-8: final gate

- **depends_on:** [task-7]
- **model:** opus
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && go clean -testcache && make check`
- **description:** Fresh full gate: all 26 modules build/vet/test + four guards
  green; confirm go.work ↔ Makefile MODULES agreement unchanged; confirm the
  workstream-1 drive evidence is recorded in the Execution log (the gate does
  NOT substitute for it). Record pass/fail per module.

## Ledger entry (task-7 appends verbatim; fill the date)

```markdown
## 2026-07-XX — demand-gated deferral ledger (telemetry-closeout; every deferral gets a wake-up TRIGGER)

Deferrals without triggers evaporate. Every deliberately-deferred item now
carries the observable condition that reopens it. Nothing below is scheduled;
each waits for its trigger, then gets its own plan.

- **jobs v2 — `Mount.Jobs` + jobs admin surface** (R8/J3 designed-deferred;
  jobs-v1 close). TRIGGER: a real scheduled-publishing consumer (e.g. cms
  scheduled publish) OR an operator need for a jobs admin surface. Note: once
  events-v1 ships its SSE gateway, an admin surface gets live job status
  nearly free — if both triggers fire, build the surface after events-v1.
- **Tenancy** (capability-map ratified call #3: an auth v2+ subdomain, never a
  standalone feature). TRIGGER: a real multi-tenant host exists.
- **otel W3C trace-context propagation helpers** (fast-follows open flag #3;
  ruled wait-until-needed 2026-07-06). TRIGGER: the first host needing
  cross-service propagation — calling a downstream traced service or sitting
  behind a traced edge — reopens it as a small addition to
  integrations/tracing/otel. Until then every request is a fresh root trace,
  accepted consciously at the telemetry closeout.
- **s3 manager-backed streaming multipart** for the plain upload path
  (fast-follows open flag #4). TRIGGER: a host uploads objects large enough
  that whole-object buffering hurts. Needs a network fetch of
  feature/s3/manager.
- **goredis smooth token-bucket Acquire variant** (throttler ruling entry: the
  old NewTokenBucket salvage). TRIGGER: a consumer needs even pacing instead
  of burst-then-wait — one more file in integrations/kvstores/goredis
  (R-KV1).
- **sdk/web transit-middleware residue** — trust-proxy IP resolution,
  client-info extraction, idempotency-key dedupe, max-body-size limiter
  (original `bridge/transit/httpmid/{trust_proxies,client_info,unique_to_id,
  body_limit}.go`; capability-map "Bridge transit middleware" row, sdk/web
  backlog). TRIGGER: first host need — a deployment behind a reverse proxy
  (trust-proxy + client-info) or a public write API (idempotency-key +
  body-limit).
- **Generic HTTP rate-limit middleware** (`RateLimit` over `sdk/ratelimiter`;
  original `bridge/transit/httpmid/rate_limit.go`; capability-map Rate
  limiting section, ratified home sdk/web, backlog). TRIGGER: first host
  exposing an endpoint that needs HTTP-surface rate limiting. Both backends
  already exist (`ratelimiter.Memory`, `goredis.Limiter`) — this is
  middleware-shape work only.
- **C1 — cms non-root-prefix link fix** (documented limitation,
  features/README §4; forward-plan shape produced at the telemetry closeout,
  `.claude/plans/telemetry-closeout/c1-assessment.md`). TRIGGER: a host needs
  cms mounted under a non-root prefix, or a multi-feature mount forces
  non-root prefixes.
- **Span vocabulary — server/client span kinds** (conscious loss at the
  telemetry closeout): the string-attribute-only `sdk/tracing` port carries no
  span kind, so HTTP request spans render as INTERNAL in trace viewers, vs the
  original's `SpanKindServer`. TRIGGER: a host needs server/client span
  differentiation in its trace backend — reopens as a port-vocabulary
  question (capability-map ruling: richer vocabulary belongs to the otel
  integration side), not a silent middleware patch.
- **ReBAC** — pointer, not a deferral: the 2026-07-02 "defer entirely" ruling
  is SUPERSEDED by the 2026-07-06 authorization ruling (auth-v2 ships
  authorization as a port-shaped capability; first-party ReBAC authorizer is
  the flagship implementation, never required). Owned by the auth-v2 design
  doc, not this ledger.
```

## Sequencing

task-1 → task-2 (workstream 1, strictly ordered). Tasks 3, 5, 6 are
independent of each other and of workstream 1; default sequential after
task-2, any order. task-4 is NOT TAKEN (TC4 ruled DEFER 2026-07-07) — task-7
records the closure. task-7 needs tasks 1–3, 5, 6. task-8 last.
Per-workstream gate: `make check` green before moving on (task-2 and task-8
run it in full; single-module tasks verify locally in between).

Execution-ready order: task-1 → task-2 → {task-3, task-5, task-6 in any
order} → task-7 → task-8.

## Consultation notes

`lead-backend-engineer` reviewed the workstream-1 sketch ("ship-with-edits").
Adopted wholesale: (1) middleware order is load-bearing — Tracing outer of
Logger, for ctx propagation into the access line AND because `web.RecordError`
type-asserts the writer with no Unwrap walk (inner placement would silently
eat Logger's `error` field); (2) `tracer.Shutdown` must get a fresh
timeout context — the run ctx is already cancelled when `web.Run` returns, and
the stdout exporter's synchronous path hides the OTLP flush drop; (3) span
name from `r.Pattern` alone (it embeds the method; middleware runs inside the
mux match so the pattern is populated — the original's outside-the-mux version
could never see it); static fallback, never `URL.Path`; (4) linkage option
(b) (otel integration stashing log IDs on every span) REJECTED — an invisible
global side effect coupling the integration to sdk/logging unconditionally;
(5) gate the tracer choice, not the middleware presence. The planner's initial
call on the one governance question the lead surfaced (identity interface
structural in `sdk/web`) was **overturned by the architecture-steward at the
2026-07-07 review gate** — steward ruling, now this plan's default: a NAMED
optional interface `tracing.SpanIdentity` in `sdk/tracing`. Reasoning cited:
structural satisfaction is the repo's tool for avoiding forbidden import
edges, and there is no edge here (same module); the implementer is a
DIFFERENT module, so structural-in-web leaves a cross-module method-set
contract with no compile-checked home — and the re-derivers are already
visible (`sdk/workers` tracing, `goredis.TracingHook`). Port-minimality is
not breached: trace/span-ID identity is already sdk kernel vocabulary
(`sdk/logging.WithTraceID`/`WithSpanID`). The same review gate contributed
the task-4 GCS credential correction (SRE, verified in vendored source), the
godoc ordering-constraint deliverable (steward + PM), the local drive
fallback (SRE), and the three added ledger rows (PM/steward).

## Ratification items — RULED 2026-07-07 (jrazmi), all five at defaults

Rulings TC1–TC5 per the NOTES.md 2026-07-07 "planning wave RATIFIED" entry.

1. **TC1 — RULED: R10 demotion CONFIRMED.** Ratifying this plan ratified the
   demotion of R10's standalone telemetry milestone to this closeout.
2. **TC2 — RULED: keep `ß`→`s`** (sdk-parity flag #3 closes). The default
   stood: it matches the old table exactly, is already live, and switching
   would deepen the D-5 mixed-slug-corpus caveat (renames re-slug). The `ss`
   branch (a small sdk/slug task) is not taken.
3. **TC3 — RULED: KEEP `turso`** (R-KV2 open flag closes). The default stood:
   Turso is the provider name, the module carries the vendor's live-service
   assumptions (kvstore plan's reasoning), and the rename would touch two
   module paths, host imports, and docs for zero behavior.
4. **TC4 — RULED: gcs swap DEFERRED** (wont-do until a live GCS run). The
   verified correct form stays recorded in the flag and task-4 for the future
   fix: `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`.
   The swap-now branch (task-4 + mandatory live GCS conformance leg) is not
   taken. Deprecated ≠ removed; the cost of waiting is nil; an unverified
   credential-path change doesn't belong in a telemetry release.
5. **TC5 — RULED: `tracing.SpanIdentity` named in `sdk/tracing` STANDS**
   (steward default; no flip-back). The three structural-option mitigations
   (godoc method-set contract of record; otel anonymous-interface compile
   assertion; sdk/tracing doc pointer) are moot as acceptance criteria — the
   named interface subsumes them.

## Recommended reviews

Review gate RUN 2026-07-07: **architecture-steward**, **platform-sre**, and
**product-manager** all returned ratify-with-amendments; every amendment is
folded in above (steward: SpanIdentity placement + godoc ordering constraint;
SRE: gcs credential correction + local drive fallback + .env.example +
shutdown-flush proof; PM: ledger rows + out-of-scope precision + task-6
reframe). jrazmi ruled the five ratification items at their defaults and
ratified 2026-07-07 (TC1–TC5) — no further review pending.

## Execution log

_Task-2's drive evidence (exact commands, span + log excerpts) lands here;
task-8 confirms it exists before closing._

### task-1 — 2026-07-07 (sdk/web Tracing middleware) — PASS

**Landed:**
- `sdk/tracing/tracing.go`: added the NAMED optional interface
  `SpanIdentity interface { TraceID() string; SpanID() string }` (TC5) with the
  doc line "optional; implementations without stable span identity simply omit
  it"; fixed the stale package doc (dropped "future"/"deferred" otel wording —
  the module shipped) and added the SpanIdentity linkage-convention pointer for
  implementers.
- `sdk/web/middleware.go`: added `func Tracing(t tracing.Tracer) Middleware`
  mirroring the Logger/Panics shape; nil tracer → `tracing.Noop{}`. Span name
  from `r.Pattern` alone, static `"http.request"` fallback (never `URL.Path`).
  Attributes via `tracing.StringAttribute`: `http.method`, `http.host`,
  `user_agent`, `net.peer.ip` (peer host from `RemoteAddr` via a new `peerHost`
  helper), `http.route` (only when pattern non-empty), and `http.status_code`
  from the package's own `statusWriter` after `next`. 5xx → `RecordError` with a
  synthesized `fmt.Errorf("server error: %d", status)` — the wrapper's recorded
  error is never read. Type-asserts `tracing.SpanIdentity` on the returned
  finisher and, on non-empty IDs, stashes via `logging.WithTraceID`/`WithSpanID`
  before `r.WithContext(ctx)`. Godoc states "place outer of `web.Logger`" with
  both consequences (traced ctx into the access line; RecordError landing on
  Logger's writer) plus the accepted Noop cost (per-request attr slice +
  RemoteAddr parse, no fast path).
- `sdk/tracing/tracing_test.go`: `TestNoopFinisherDoesNotSatisfySpanIdentity`,
  `TestSpanIdentityExposesIDs` (+ compile assertion `var _ SpanIdentity`).
- `sdk/web/middleware_test.go`: recording + identity tracer stubs;
  `TestTracing_StartsAndFinishesSpanPerRequest`,
  `TestTracing_StaticNameFallbackWhenNoPattern`,
  `TestTracing_StatusCodeAttribute`, `TestTracing_RecordsServerError`,
  `TestTracing_ClientErrorDoesNotRecord`,
  `TestTracing_SpanIdentityStashesTraceAndSpanIDs` (asserts trace_id/span_id via
  `logging.TracingHandler` JSON output), `TestTracing_NoopPathCarriesNoIDs`.

**Verify (all PASS):**
- `cd sdk && go build ./...` — PASS.
- `cd sdk && go test ./...` — PASS (all packages ok; new Tracing/SpanIdentity
  tests green).
- `cd sdk && go vet ./...` — PASS (clean).
- `make guard` — PASS (all four guards green; the intra-sdk `sdk/web` →
  `sdk/tracing` edge passes guard-sdk-stdlib as expected).
- Standing per-leg check: root `make check` — PASS (all 26 modules
  build/vet/test + four guards, "all checks passed"). `examples/minimal` booted
  on :8081 — `GET /` → 200, `GET /products/widget-3000` → 200; killed by pid,
  port confirmed free.

**Divergences:** none affecting design points. Peer-host attribute key uses
`net.peer.ip` (salvage-reference precedent from
`gopernicus-original/.../httpmid/telemetry.go`); the plan named that attribute
descriptively ("peer host from RemoteAddr") without pinning a key. `user_agent`
key follows the plan text verbatim (not the salvage's `http.user_agent`).

### task-2 — 2026-07-07 (otel IDs + cms wiring + observability drive) — PASS

**Landed:**
- `integrations/tracing/otel/tracer.go`: `*spanFinisher` gained `TraceID()` /
  `SpanID()` off `span.SpanContext()` (return `""` when
  `!HasTraceID()`/`!HasSpanID()`), plus the compile assertion
  `var _ tracing.SpanIdentity = (*spanFinisher)(nil)` alongside the existing
  Tracer/SpanFinisher assertions.
- `integrations/tracing/otel/otel_test.go`: `TestSpanFinisherExposesIDs` drives
  the SpanIdentity path through the existing tracetest SpanRecorder — asserts the
  finisher's IDs are 32-/16-hex and equal the recorded span's own SpanContext.
- `examples/cms/cmd/server/main.go`: `buildTracer(ctx)` gates the TRACER CHOICE
  on `TRACING_ENABLED` — true → `otel.Open` with a `Config` filled by
  `environment.ParseEnvTags("", &cfg)` from the `TRACING_*` env tags; else
  `tracing.Noop{}`. Middleware order is now
  `router.Use(web.RequestID(), web.Tracing(tracer), web.Logger(log), web.Panics(log))`
  (Tracing outer of Logger). `tracer.Shutdown` is deferred with a FRESH
  `context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT)` — never the
  run-scoped ctx.
- `examples/cms/go.mod` (+ go.sum): added require/replace for
  `integrations/tracing/otel`; `go mod tidy` pulled the otel indirect deps into
  go.sum (and bumped the templ-tool indirects golang.org/x/{tools,mod,sync} to
  their tidy-resolved minimums — natural tidy churn, no source effect).
- `examples/cms/.env.example` (CREATED): documents the `TRACING_ENABLED` gate and
  every `TRACING_*` knob (all non-secret), including the note that enabling
  tracing sends client IP + user-agent to the trace backend.

**Verify (all PASS):**
- `cd integrations/tracing/otel && go build ./... && go test ./... && go vet ./...` — PASS.
- root `make check` — PASS (26 modules build/vet/test + integration-tag vet +
  four guards; "all checks passed"), run both before and after the drive.
- Standing per-leg boot: `examples/minimal` on :8081 → `GET /` 200,
  `GET /products/widget-3000` 200; killed by pid; port 8081 free. (minimal stays
  Noop-only — not wired.)

**Real-interaction drive — stdout leg (closes workstream 1):**
- DSN class: **remote playground Turso** (`libsql://gopernicus-cms-playground-…turso.io`,
  the authorized DB). Pre-boot migrate ran against it (`make run` log:
  `msg="running migrations" dir=primary` → `msg="migrations complete"`).
- Command: `TRACING_ENABLED=true TRACING_SERVICE_NAME=cms-playground make run`
  (stdout→spans file, stderr→logs file).
- Admin auth: **the `examples/cms` host mounts CMS admin routes UNGATED — there
  is no login** (Register passes no adminMW; router.go Mount leaves admin routes
  ungated when adminMW is nil). Drive flow was therefore direct: `GET /` →
  `GET /articles` (admin list) → `GET /articles/new` (form; fields
  title/excerpt/body/author/status) → `POST /articles` (create, status=published,
  303) → `GET /articles/{slug}` (public page reflects the new entry) →
  `GET /articles/{id}/edit` → `POST /articles/{id}` (edit body, 303) →
  admin edit form shows the edited body (uncached) → `GET /articles/{slug}?v=edited`
  (cache-busted public page reflects the EDITED body).
- Spans (stdout exporter) are named by route PATTERN with `http.status_code` +
  `http.route`; each matches its request log line's `trace_id`/`span_id`:

  ```
  span GET /{$}              status=200  trace_id=1a4f7b11de0fca32e1cc3cfdbbed9be9 span_id=043e9f42d9504f15
  log  {"msg":"request","method":"GET","path":"/","status":200,"trace_id":"1a4f7b11de0fca32e1cc3cfdbbed9be9","span_id":"043e9f42d9504f15"}

  span POST /articles        status=303  trace_id=f05d48ab23ff383be5492f74b3b262a4 span_id=dba52597bc808e51
  log  {"msg":"request","method":"POST","path":"/articles","status":303,"trace_id":"f05d48ab23ff383be5492f74b3b262a4","span_id":"dba52597bc808e51"}

  span GET /articles/{slug}  status=200  trace_id=4a106ae116f46836efdc8c07fd440835 span_id=edfbfc6facc5a0bd
  log  {"msg":"request","method":"GET","path":"/articles/telemetry-drive-…","status":200,"trace_id":"4a106ae116f46836efdc8c07fd440835","span_id":"edfbfc6facc5a0bd"}
  ```
  Other observed span names: `GET /articles`, `GET /articles/new`,
  `GET /articles/{id}/edit`, `POST /articles/{id}` — all carry status + route +
  `user_agent=curl/8.14.1` + `net.peer.ip=127.0.0.1`. Server killed by pid;
  port 8080 free.

**OTLP shutdown-flush leg (Risk 1 proof) — RAN, PASS:**
- Docker 27.5.1 available. Host 4317/16686 were already held by an unrelated
  project's Jaeger, so a fresh `jaegertracing/all-in-one` (COLLECTOR_OTLP_ENABLED)
  was run on alternate host ports `-p 14317:4317 -p 26686:16686`.
- Reran `TRACING_ENABLED=true TRACING_EXPORTER=otlpgrpc
  TRACING_OTLP_ENDPOINT=localhost:14317 TRACING_OTLP_INSECURE=true
  TRACING_SERVICE_NAME=cms-otlp-drive make run`; drove 4 requests
  (`GET /`,`GET /articles`,`GET /articles/{slug}`,`GET /contact`), then `kill -TERM`
  the server binary WITHIN the batch window (spans still buffered, not yet
  batch-flushed).
- After graceful exit (code 0), Jaeger query
  `http://localhost:26686/api/traces?service=cms-otlp-drive` returned all 5 of the
  final requests' spans (route-pattern operationNames + `http.status_code`); every
  captured request trace_id (`ab0f9476…`, `463e01ad…`, `b6359ded…`, `fd063b6b…`,
  `01cf0a71…`) = ARRIVED. This proves the deferred fresh-`context.Background()`
  `tracer.Shutdown` flushes the OTLP batch exporter on SIGTERM — the drop the
  run-scoped (already-cancelled) ctx would have caused is avoided. Container
  stopped; ports 14317/8080 free; the unrelated project's Jaeger left untouched.

**Divergences:** (1) OTLP Jaeger used alternate host ports 14317/26686 because a
foreign container already held 4317/16686 — the server's OTLP endpoint was
pointed at 14317 accordingly; no code impact. (2) `go mod tidy` bumped the
templ-tool indirect deps (golang.org/x/{tools,mod,sync}) in examples/cms — normal
tidy resolution, non-source. No design-point divergences.

## Notes

- **Execution readiness (context from the same 2026-07-07 ratification):**
  repo-hardening was ratified same day with repo =
  `github.com/gopernicus/gopernicus` PUBLIC; D8 collapses to a verification
  pass, so NO quiet window exists — telemetry-closeout has no
  rename-collision constraint and can execute whenever scheduled. The
  ratified execution order (NOTES.md 2026-07-07 entry) runs repo-hardening
  phases 1–3 first (everything into git before more code lands); events-v1
  and telemetry-closeout then execute per their plans.
- Module count stays 26; no go.work/Makefile edits anywhere in this plan.
- Original repo salvage reference:
  `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original/bridge/transit/httpmid/telemetry.go`
  (shape only — its W3C propagation extract and otel-direct coupling do not
  carry over).
- Plan-file convention precedent: `.claude/past/kvstore-consolidation/plan.md`
  (single-file small milestone; closed-milestone plan dirs moved from
  `.claude/plans/` to `.claude/past/` on 2026-07-07 — flag-origin citations in
  this plan reference NOTES.md entries, which did not move).

### task-3 — 2026-07-07 (sdk/ratelimiter doc staleness) — PASS

Comment-only; zero behavior change. Every rewritten claim cross-checked against
the code (`memory.go`, `acquire.go`, `resolver.go`,
`integrations/kvstores/goredis/limiter.go`) before writing.

**The three spots (`sdk/ratelimiter/ratelimiter.go`):**
1. Package comment: "Limiter implementations are in the memorylimiter/
   subpackage" → "Memory is the in-package default Limiter; Acquire is the
   blocking helper that waits for quota instead of rejecting." (`Memory` is
   in-package at memory.go:24; `Acquire(ctx, limiter, key, limit) error` at
   acquire.go:23.)
2. `Limiter` doc: "Memory, Redis, and SQLite backends satisfy it" → "Memory
   satisfies it in-package; goredis.Limiter (integrations/kvstores/goredis) is
   the Redis-backed implementation." (SQLite dropped by ruling; `goredis.Limiter`
   verified at limiter.go:97 with `var _ ratelimiter.Limiter = (*Limiter)(nil)`.)
3. Usage example: `memorylimiter.New()` → `ratelimiter.NewMemory()` (the real
   constructor, memory.go:38). Same example block: `ratelimiter.New(store,
   resolver, log)` → `ratelimiter.New(store, resolver,
   ratelimiter.WithLogger(log))` — the real signature is
   `New(Limiter, LimitResolver, ...Option)`, so the positional `log` was itself
   drift inside the stale example; leaving it would have kept a non-compiling
   snippet.

**Verify (all PASS):**
- `cd sdk && go build ./... && go vet ./...` — PASS.
- Standing per-leg check: root `make check` — PASS ("all checks passed", all
  modules + integration-tag vet + four guards). `examples/minimal` booted on
  :8081 → `GET /` 200, `GET /products/widget-3000` 200; killed by port
  (pid 34836); port 8081 confirmed free.

**Divergences:** the `WithLogger` fix in spot 3's example (above) is one line
beyond the task's literal constructor swap, inside the same usage-example spot —
included because the task's charter is removing doc/reality drift.

**task-2 addendum — 2026-07-07, main-session browser leg (run-and-look):**
after the implementer's curl drive, a real-browser pass (playwright/chromium)
against `TRACING_ENABLED=true make run` (remote playground DSN) loaded `/`
("Home · ACME") and the drive's article page
(`/articles/telemetry-drive-1783459281`), which rendered the EDITED body —
save-then-view confirmed visually; screenshots in the session scratchpad.
Server stdout for the browser session emitted pattern-named spans
(`GET /{$}` ×2, `GET /articles/{slug}`, `GET /{slug}`) and the matching
request log line carried `trace_id=5fbcf925…`/`span_id=411622bf…` — the
task-1 linkage observed from a real browser client. Server killed; port
8080 free. Note of record: `examples/cms` mounts admin routes ungated (no
login exists in that host) — the plan's "login →" step had nothing to
drive; flagged for jrazmi's awareness (auth-gating an example host is
auth-v2/examples scope, not telemetry scope).

### task-5 — 2026-07-07 (jobs runtime-logger Config knob) — PASS

**Landed:**
- `features/jobs/jobs.go`: added optional `Logger *slog.Logger` to `Config`
  (additive; nil keeps the seams' existing `slog.Default()` fallback), with godoc
  distinguishing it from `feature.Mount.Logger` ("Config.Logger is the runtime
  pools' operational logger, while Mount.Logger is registration-time logging — do
  not unify them by threading Mount into NewService"). Carried on `resolvedConfig`
  (`logger`), threaded to `schedulesvc.Deps.Logger` in `NewService` (= `cfg.Logger`)
  and `runtime.Deps.Logger` in `NewRuntime` (= `svc.cfg.logger`). `queuesvc.NewService`'s
  third param is a clock — left untouched. Added the `log/slog` import.
- `features/jobs/jobs_test.go`: added a distinguishable `captureHandler`
  (slog.Handler recording record messages) + `runOneJob` helper, and
  `TestConfigLogger_RuntimePoolsLogThroughIt` with two subtests: "wired logger
  receives pool lines" (Config.Logger = capture → asserts the runner's
  `"processing job"` line) and "nil logger falls back to slog.Default"
  (Config.Logger nil + `slog.SetDefault(capture)` save/restore → same assertion).
- `examples/jobs-minimal/cmd/server/main.go`: wired `Logger: log` in the Config
  literal (the flag's origin host) and removed the now-obsolete pre-knob workaround
  (the `slog.SetDefault(log)` block whose comment claimed "jobs.NewRuntime leaves
  the runtime logger unset" — false once the knob is wired); the knob is now the
  mechanism routing pool lines to the host logger.

**Verify (plan) — all PASS:**
- `cd features/jobs && go build ./... && go test ./... && go vet ./...` — PASS
  (`TestConfigLogger_RuntimePoolsLogThroughIt` + both subtests green; all jobs
  packages ok).
- `cd examples/jobs-minimal && go build ./... && go test ./... && go vet ./...` — PASS.

**Standing per-leg check:**
- root `make check` — PASS ("all checks passed": 26 modules build/vet/test +
  integration-tag vet + four guards).
- `examples/minimal` on :8081 — `GET /` 200, `GET /products/widget-3000` 200;
  killed by port; port 8081 free.

**Live jobs-minimal proof (knob live, unambiguous):** ran
`PORT=8083 LOG_FORMAT=json go run ./cmd/server`, POST `/enqueue`
`{"kind":"demo.print","payload":{"source":"task5-live-proof"}}` →
`{"job_id":"job_3d6fc8429a59dc453ad705be134e7ba0"}`. With `slog.SetDefault`
removed, the ONLY path from the pools to the host logger is `Config.Logger`, so
the pools emitting **JSON** (the host logger's `LOG_FORMAT=json`, not the stdlib
default text handler) proves the knob:
```
{"level":"INFO","msg":"worker pool starting","pool":"jobs-queue",...}
{"level":"INFO","msg":"processing job","worker_id":"jobs-queue-worker-3","job_id":"job_3d6fc8429a59dc453ad705be134e7ba0"}
{"level":"INFO","msg":"demo.print","job_id":"job_3d6fc8429a59dc453ad705be134e7ba0","payload":"{\"source\":\"task5-live-proof\"}"}
{"level":"INFO","msg":"job completed","worker_id":"jobs-queue-worker-3","job_id":"job_3d6fc8429a59dc453ad705be134e7ba0","duration":38250}
```
Server SIGTERM'd; port 8083 free.

**Divergence:** the plan named the host change a "one-line Config wire", but the
origin host also carried the pre-knob `slog.SetDefault(log)` workaround whose
comment becomes false once the knob is wired. Removed that 5-line workaround
block so the host demonstrates the knob AS the mechanism (and the live proof is
unambiguous — no default-logger path to confound it). Behavior-neutral: no other
`slog.Default()` consumer remains in that host. Nothing else diverged.

### task-6 — 2026-07-07 (C1 assessment, assess-only) — PASS

**Deliverable:** `.claude/plans/telemetry-closeout/c1-assessment.md` — the
forward-plan shape for the cms non-root-prefix link limitation. No code
changed (assess-only per Risk 3; the fix stays future-milestone scope).

**Inventory totals:** 36 path-valued link sites across 10 of 11 cms `.templ`
files (12 static literals, 14 built inside templ expressions, 10 fed by
Go-computed model values or stored data — 2 of those are menu-item DATA
URLs); 14 handler redirects (all admin: entries 5, menus 4, terms 3, media 2)
+ 11 Go-side link-construction sites feeding views (entries 5, terms 4,
public 2) = 25 Go-side sites. Admin/public split (templ): 27 admin, 8 public,
1 shared chrome (`layout.templ:14`). Plus two data-level classes a code fix
cannot reach: seeded menu-item URLs (`examples/{minimal,auth-cms}` seed
`"/"`/`"/about"`) and markdown entry bodies. Grep commands recorded in the
deliverable for reproducibility.

**Recommendation (one line):** option (a) — base path discovered from the
registrar via a named optional `BasePath() string` interface in `sdk/feature`
(satisfied by `PrefixRegistrar`, composing), threaded explicitly
(handler-held URL builder → model fields / template base params); (b)
view-context rejected as an invisible dependency, (c) relative links rejected
as per-route-depth arithmetic. Size: ~360–510 hand-written lines across ~24
files + 11 regenerated `*_templ.go`; three-phase split (seam+Go side → views
→ data policy + real-interaction proof).

**Verify (all PASS):** deliverable file exists; `git status` shows only the
new .md + this plan-file entry (no code touched). Standing per-leg check:
root `make check` — PASS ("all checks passed"); `examples/minimal` booted on
:8081 → `GET /` 200, `GET /products/widget-3000` 200; killed by port; port
8081 confirmed free.

**Divergences:** none.

### task-7 — 2026-07-07 (flag closures + ledger + doc sync) — PASS

Docs-only; no Go code touched.

**Landed — NOTES.md (two appended entries, in order):**
1. `## 2026-07-07 — telemetry-closeout EXECUTED: web.Tracing shipped,
   real-drive proof on remote playground Turso, hygiene flags dispositioned`
   — records what shipped (task-1 `web.Tracing` + `tracing.SpanIdentity` TC5;
   task-2 otel finisher IDs + cms wiring; task-3/task-5 hygiene fixes), the
   drive evidence pointer (this plan's Execution log; DSN class = **remote
   playground Turso**; OTLP shutdown-flush leg PASSED via Jaeger; main-session
   browser leg observed spans + linkage), and the flag closures each citing
   its origin entry: JobID/JobStatus/Retries → WONT-DO; Caser/AddAcronym →
   ALREADY-SHIPPED (corrects the "parked" framing); gcs credential swap → per
   TC4 DEFER, closed WONT-DO-until-a-live-GCS-run (verified form
   `option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON))`
   recorded + the scope-drop warning against the
   `WithAuthCredentials`/`DetectDefault` form); ß→`s` KEPT (TC2); turso naming
   KEPT (TC3) — all three citing the 2026-07-07 "planning wave RATIFIED"
   entry; C1 assess-only done (forward-plan shape at
   `.claude/plans/telemetry-closeout/c1-assessment.md`). Plus the two
   execution facts: `examples/cms` admin routes ungated (auth-v2/examples
   scope), and jobs-minimal's `slog.SetDefault` workaround removed when the
   `Config.Logger` knob landed.
2. `## 2026-07-07 — demand-gated deferral ledger (telemetry-closeout; every
   deferral gets a wake-up TRIGGER)` — appended VERBATIM from this plan's
   "Ledger entry" section, heading date filled 2026-07-07; rows unedited
   (jobs v2, tenancy, otel W3C propagation, s3 streaming multipart, goredis
   token-bucket, sdk/web transit-middleware residue, generic HTTP rate-limit
   middleware, C1, span-kind vocabulary, ReBAC pointer).

**Landed — sdk/README.md (web row extended):** the `web` package row's
middleware list now reads "request-id, **tracing**, logger, panic recovery,
CORS, default headers — **place `Tracing` outer of `Logger`** so the traced
context reaches the access log line and `RecordError` keeps landing on
Logger's writer" (steward/PM amendment: the ordering constraint lives on the
reusable surface's docs, not only in example wiring).

**Landed — capability-map.md (execution note):** appended
`**Execution note (2026-07-07, telemetry-closeout):**` to the file's
top-of-file execution-note block — Telemetry section rows BUILT (`sdk/tracing`
+ `Noop`, `integrations/tracing/otel`, trace-aware `sdk/logging`,
`sdk/web.Tracing`), real-drive-verified on `examples/cms` (remote playground
Turso); Metrics stay N/A; the Bridge transit-middleware residue rows + generic
HTTP rate-limit middleware remain backlog, now trigger-gated in the NOTES.md
2026-07-07 demand-gated deferral ledger.

**Verify (all PASS):**
- `git status` docs-only — PASS: only `NOTES.md`, `sdk/README.md`,
  `.claude/plans/restructure/capability-map.md`, and this plan file modified.
- `make guard` — PASS (all four guards ran green; cheap confirmation nothing
  moved).
- Standing per-leg check: root `make check` — PASS ("all checks passed", 26
  modules build/vet/test + integration-tag vet + four guards).
  `examples/minimal` booted on :8081 → `GET /` 200,
  `GET /products/widget-3000` 200; killed by port; port 8081 confirmed free.

**Divergences:** none.

### task-8 — 2026-07-07 (final gate) — PASS

Fresh, uncached full gate: `go clean -testcache && make check`. Ended
`all checks passed`.

**Per-module results:** all 26 modules green (vet + build + test), no
failures. The `== module ==` blocks, in gate order: `sdk`,
`integrations/cryptids/bcrypt`, `integrations/cryptids/golang-jwt`,
`integrations/datastores/pgxdb`, `integrations/datastores/turso`,
`integrations/email/sendgrid`, `integrations/filestorage/gcs`,
`integrations/filestorage/s3`, `integrations/kvstores/goredis`,
`integrations/oauth/github`, `integrations/oauth/google`,
`integrations/scheduling/robfig-cron`, `integrations/tracing/otel`,
`features/auth`, `features/auth/stores/pgx`, `features/auth/stores/turso`,
`features/cms`, `features/cms/stores/pgx`, `features/cms/stores/turso`,
`features/jobs`, `features/jobs/stores/pgx`, `features/jobs/stores/turso`,
`examples/auth-cms`, `examples/cms`, `examples/jobs-minimal`,
`examples/minimal` — 26/26 PASS. Templ generation was a no-op (no
`*_templ.go` git-diff drift). Integration-tag vet (compile-only, no DB) clean
for the three turso store modules. All four guards ran clean
(guard-sdk-stdlib, guard-feature-isolation, guard-sdk-no-outward,
guard-no-legacy-path).

**Module-count agreement:** go.work `use` block = 26 entries; Makefile
`MODULES` = 26 entries; 26 = 26, unchanged this milestone (no go.work/Makefile
edits landed anywhere in this plan).

**Drive-evidence confirmation (the gate does NOT substitute for it):** the
workstream-1 real-interaction evidence IS recorded above in this Execution
log — the task-2 entry carries the remote-playground DSN class ("DSN class:
**remote playground Turso** (`libsql://gopernicus-cms-playground-…turso.io`,
the authorized DB)"), the stdout span↔log excerpts with matching
`trace_id`/`span_id`, and the OTLP shutdown-flush leg ("**OTLP
shutdown-flush leg (Risk 1 proof) — RAN, PASS:**", spans confirmed arrived at
Jaeger after SIGTERM); the task-2 browser addendum records the real-browser
leg ("a real-browser pass (playwright/chromium) against
`TRACING_ENABLED=true make run` (remote playground DSN)"). Both present.

**Standing per-leg check:** `examples/minimal` booted on :8081 → `GET /` 200,
`GET /products/widget-3000` 200; killed by port (pid 97329); port 8081
confirmed free.

**Divergences:** none.
