# sdk-parity — port the remaining general-purpose surface from gopernicus-original

Status: PLANNED (scope RATIFIED by jrazmi 2026-07-06; this plan designs how it lands)
Milestone dir: `.claude/plans/sdk-parity/`
Source repo: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original` (single module —
reference-only salvage; read the old code, re-type fresh, port no import paths)
Target repo: this repo (18 modules today; 21 after this milestone)

> **Convention note.** Sibling milestones use `00-overview.md` + one file per
> phase (`01-<slug>.md`, …). This milestone was dispatched as a single
> consolidated plan file; phases below are written phase-shaped (own DoD, own
> verify gate, own task block) so the loop protocol can cut them into numbered
> phase files verbatim if it prefers. Task numbering is continuous across
> phases.

## Context

The 2026-07-02 roadmap loop closed datastore-portability, auth-v1,
trio-relayout, and jobs-v1, deliberately deferring the original repo's
general-purpose surface (validation, async, conversion, the JSON-API web kit,
events/oauth/tracing/crypto ports, fop pagination). A two-pass gap analysis has
been completed and jrazmi has **ratified** the un-deferral scope verbatim.
Inclusions are settled; this plan designs how each item lands in the
multi-module layout, phases low-risk pure additions before the two invasive
changes (the `sdk/repository → sdk/crud` breaking rename and the `sdk/web`
JSON-API merge), and enumerates the full blast radius of the rename.

Two prior rulings are **deliberately superseded** by this ratification (newer
jrazmi decision wins; do not re-litigate, do log):

- jobs-v1 J6/J7 ("no in-process retry", "no tracer hooks" in `sdk/workers`) —
  items 9 and 11 below restore both, reconciled so the store-owned durable
  retry model stays intact.
- events-feature-design §9's "no Redis integration in v1" — the ratified scope
  ships `integrations/events/redis-streams` now. `Mount.Events`,
  `features/events` (outbox + SSE gateway), and the durable rail remain
  deferred to events-v1. This milestone also pulls events-v1's phase 1 (SSE
  primitives) and phase 2 (`sdk/events` + `eventstest`) forward; the design doc
  gets a status-header amendment in the docs phase so events-v1 resumes at
  phase 3 without double-building.

## Goal

Every ratified item lands in its architecturally correct home — sdk staying
zero-require, three new single-library integration modules, the
repository→crud rename executed across all 30 importing files — with
`make check` green after every phase.

## Definition of Done

- `sdk/go.mod` still has **no `require` block**; `make guard` (all four) green.
- New sdk packages: `validation`, `async`, `conversion`, `tracing`, `cryptids`,
  `oauth`, `events` (+ `events/eventstest`); extended: `config`, `logging`,
  `slug`, `workers`, `web`, `email`; renamed: `repository` → `crud`.
- Three new modules in `go.work` + Makefile `MODULES`:
  `integrations/events/redis-streams`, `integrations/oauth/google`,
  `integrations/oauth/github` — each with exactly one external library (github:
  zero, under the taxonomy amendment task-13 lands).
- `grep -rn 'sdk/repository' --include='*.go' .` and the docs sweep return
  nothing; all 30 enumerated importers build against `gopernicus/sdk/crud`.
- Full `make check` green (21 modules); `examples/cms` run-and-look SSR
  regression check passed after the web merge.
- Docs synced: ARCHITECTURE (module count + taxonomy amendment), sdk/README
  package table, RELEASING, Makefile header, events-design status header,
  NOTES.md milestone entry.

## Out of scope (state as non-goals; ratified OUT list)

- Redis cache/limiter adapters; sqlite limiter; throttler; sendgrid; gcs/s3
  filestorage adapters — fast-follow integrations after this milestone.
- otel/stdouttrace tracing exporters (`integrations/tracing/otel` remains the
  ratified fast-follow; this milestone ships port + Noop only).
- The `infrastructure/database/crud` SQL-generation DSL (Spec/Dialect/render/
  scan) — stays dead; providers keep hand-writing SQL.
- `httpc` (dead in the original); `golang-jwt` integration (JWTSigner is
  port-only this milestone); authorization/ReBAC + invitations (auth v2+);
  the events-v1 durable feature (outbox schema, poller, SSE gateway,
  `Mount.Events`); the pg outbox helper.
- Old `sdk/web/server.go` (`WebServer` + functional options) — the new
  `ServerConfig` + `web.Run` loop supersedes it. Old `capture.go`
  (`ResponseCapture`) — the new `statusWriter` + `http.ResponseController`
  cover its use; not ported unless SSE porting proves otherwise.
- Old `cryptids.GenerateID/GenerateCustomID/GenerateUUID` — `sdk/id` owns IDs.
- Old `events.EventRegistry` (prefix routing), `WithPriority`, package-level
  `var GenerateID` — trimmed per the ratified events design §2.
- Old `WithCORS`/`WithDefaultHeaders` `HandlerOption`s — `Use()` is the one
  composition idiom; only the plain `Middleware` constructors come along.
- Wiring `ParseEnvTags` into the example hosts' `main` — mechanism + tests this
  milestone; host adoption is each host's own call.

## Schema / datastore impact

None. No migrations, no SQL changes, no EAV spine impact. Feature store
modules are touched **only** by the phase-5 import-path rename; the hermetic
storetest reference suites cover behavior. A live `make test-stores` run after
phase 5 is recommended-if-creds-available, not gating (mechanical rename; the
suites' semantics are unchanged).

## Module / API impact

- **+3 modules (18 → 21):** `integrations/events/redis-streams` (redis/go-redis
  v9), `integrations/oauth/google` (coreos/go-oidc v3),
  `integrations/oauth/github` (zero external requires — see task-13). Each:
  own `go.mod` following `integrations/cryptids/bcrypt`'s pattern (`replace`
  of `gopernicus/sdk` by relative path), added to `go.work` and Makefile
  `MODULES`.
- **Breaking sdk API change:** package `gopernicus/sdk/repository` is deleted;
  `gopernicus/sdk/crud` replaces it with a redesigned contract (below). No
  tags have ever been cut (RELEASING.md), so there is no version-bump
  obligation — but the RELEASING module narrative must not mention
  `repository` after phase 5.
- **Additive sdk API:** everything else. `web` grows the JSON kit/SSE/static/
  OpenAPI surface; `workers` grows two options; `logging` grows two context
  setters; `slug.Make` changes behavior (documented below); `email` grows a
  template layer and multipart sending.
- The naming discrepancy `integrations/events/redis` (events design §1,
  capability-map) vs `integrations/events/redis-streams` (ratified scope,
  verbatim) resolves to **redis-streams** — the ratified wording is newer and
  the name is honest about which Redis primitive it wraps (streams consumer
  groups + pub/sub broadcast, not RESP-generic). Docs phase amends the design
  table.

## Generated-artifact impact

None. No `.templ` sources change. `make check`'s templ-drift gate still runs
every phase; never hand-edit `*_templ.go`.

## Design decisions (the five open points, resolved)

**D-1 — github oauth provider placement: `integrations/oauth/github`.**
The "stdlib-only impl of an sdk port ships in sdk as a default" rule is a rule
about *defaults* — vendor-neutral implementations that make an app boot with
zero external infrastructure (`Memory`, `Disk`, `Console`, `SMTP`). A GitHub
connector is not a default in any sense: it cannot operate without a vendor
account, and its API surface churns on GitHub's release schedule, not sdk's —
putting it in the kernel makes sdk releases hostage to a SaaS vendor.
`sdk/oauth` therefore ships **port + PKCE helpers only, no default impl**
(precedent: `sdk/tracing` port + Noop is the same shape; oauth doesn't even
need a Noop since a host that doesn't wire oauth simply has no provider). Both
providers are integrations. Cost, stated honestly: a zero-require integration
module contradicts the letter of the earn-your-module test as applied in R3
(the `stores/memory` refusal). Resolution: task-13 amends the ARCHITECTURE
taxonomy — an integration isolates one external dependency, **a third-party
library OR an external vendor's live API contract**; the `stores/memory` case
isolated neither and stays forbidden. The amendment is a precondition task,
not an implicit reading. (flagged by lead-backend-engineer; jrazmi ratifies at
review.)

**D-2 — conversion package shape: ONE `sdk/conversion` package.** The six old
files (~296 lines total) share one theme — converting representations — and
have zero interdependencies except the acronym table; splitting into six
micro-packages pollutes the sdk namespace and none would pass the admission
bar alone. Two deliberate trims: (a) `ToURLSlug` is **not** ported — its
accent-fold table upgrades `sdk/slug.Make` instead, keeping one canonical
slugger (D-5); (b) package-mutable `AddAcronym` is **not** ported (same
reasoning that killed `events.GenerateID`: package-level mutable state, and it
was a data race in the original) — the seeded acronym table (ID, URL, API,
HTTP, JSON, SQL, UUID) ships as an unexported fixed map; a host needing custom
acronyms is the future signal to add a `Caser` type, not restore a global.

**D-3 — cryptids naming: `sdk/cryptids`.** `integrations/cryptids/bcrypt`
already established the category name in this repo (auth-v1); a differently
named sdk package (`crypto` shadows the stdlib import; `secrets` misdescribes
signing) would split the vocabulary. Port names follow the -er rule
(`Encrypter`, `JWTSigner`); the default is named for the technology and lives
next to its port per the facility convention: `cryptids.AESGCM`
(cacher.Memory-style, in-package, not an `aesgcm` subpackage).

**D-4 — CORS/DefaultHeaders: constructors yes, HandlerOptions no.**
`CORSMiddleware(origins []string)` and `DefaultHeadersMiddleware(headers)` come
along as plain `web.Middleware` constructors — generic transport policy that
the JSON kit + SPA static server make immediately relevant. The old
`WithCORS`/`WithDefaultHeaders` `HandlerOption`s are not ported: `Use()` is the
one composition idiom and the v0.1 port already dropped them deliberately
(NOTES.md).

**D-5 — slug accent folding is a BEHAVIOR CHANGE, not a free upgrade.**
`slug.Make("Café Résumé")` today → `caf-r-sum`; after → `cafe-resume`. Call
sites audited: entry/term/menu slugs are computed at **write time** (create +
rename) and persisted — existing rows keep their slugs, no stored URL moves;
but `features/cms/logic/content/schema.go` derives content-type route segments
from registered type config **at runtime** (`slug.Make(c.Plural)`) — a
recompute path. Shipped seed types (Article/Page) are ASCII, so no shipped
route changes; a host that registered an accented `Plural` would see its route
segment change on upgrade. Mixed-corpus caveat (old rows slugged under the old
algorithm coexisting with new) recorded in NOTES at milestone close.

**D-6 — the crud contract redesign** (jrazmi verbatim: "I think we should
rename that crud. Also we may need to make it generic as all of those list
functions will need very specific filter parameters"):

```go
package crud // sdk/crud — renamed from sdk/repository

const ( ASC = "ASC"; DESC = "DESC" )        // from old fop/order.go
type OrderField struct { Column string; CastLower bool }
type Order struct { Field, Direction string }
func NewOrder(field, direction string) Order
func ParseOrder(fields map[string]OrderField, orderBy string, defaultOrder Order) (Order, error)

// ListRequest stays NON-generic: page params are uniform; the filter is what
// varies per aggregate, so it rides the interface type parameter instead.
type ListRequest struct {
    Limit  int
    Cursor string
    Order  Order // zero value = the store's default order
}
func (r ListRequest) NormalizedLimit() int   // KEPT — clamping survives at the store edge

// Strict transport-edge parsing (old fop.ParsePageStringCursor semantics):
// empty limitStr → DefaultLimit; non-numeric / ≤0 / >maxLimit → error, never clamp.
func ParseListRequest(limitStr, cursor string, maxLimit int) (ListRequest, error)

// The interfaces go generic over the domain filter type F (prior art:
// old infrastructure/database/crud.Store[T,F,C,U] — the DSL stays dead,
// only the contract shape is salvaged).
type Reader[T, F any] interface {
    Get(ctx context.Context, id string) (T, error)
    List(ctx context.Context, filter F, req ListRequest) (Page[T], error)
}
type Writer[T, C, U any] interface { /* unchanged Create/Update/Delete */ }
type CRUD[T, F, C, U any] interface { Reader[T, F]; Writer[T, C, U] }

type Page[T any] struct {                    // json tags NEW (future JSON APIs)
    Items          []T    `json:"items"`
    NextCursor     string `json:"next_cursor,omitempty"`
    HasMore        bool   `json:"has_more,omitempty"`
    HasPrev        bool   `json:"has_prev,omitempty"`        // restored from fop
    PreviousCursor string `json:"previous_cursor,omitempty"` // restored from fop
}
func TrimPage[T any](records []T, limit int, encode func(T) (string, error)) (Page[T], error)
func MarkPrevPage[T any](p *Page[T], prevRecords []T, limit int, encode func(T) (string, error)) error
// cursor.go (EncodeCursor/DecodeCursor/Cursor) moves unchanged.
```

Rationale for the split: verified fact — **no current code implements
`repository.Reader/Writer/CRUD`** (features declare their own ports; the 30
importers touch only `Page`/`ListRequest`/`NormalizedLimit`/`ErrNotFound`/the
cursor codec). Making only the interfaces generic means the entire blast
radius is the mechanical import-path + identifier rename; nothing needs an `F`
argument today. Two limit-validation semantics coexist deliberately, and the
package doc pins the rule: **JSON transports parse user input with
`ParseListRequest` (bad input → 400 via `ErrValidation`); SSR handlers and
stores keep `NormalizedLimit` clamping (store-side safety; a programmatic
limit of 0 means "default", not an error).** Existing SSR consumers keep
clamping in the rename phase — zero user-visible behavior change. The generic
interfaces stay optional vocabulary: no task may assume feature ports adopt
`crud.Reader[T,F]`; they embed, narrow, or ignore it (ARCHITECTURE's "CRUD is
a convenience, never a tax").

**D-7 — workers in-process retry, reconciled with the store lease
(supersedes J6, keeps its invariant).** The old `processWithRetry` ran retries
*inside one claim*; jobs-v1 moved durable retry into the store's `Fail`
(requeue-below-max / dead-letter-at-max) — both now coexist as two named axes:

- `WithMaxAttempts(n)` — UNCHANGED: the durable axis, passed to `store.Fail`;
  the store owns retry-count/dead-letter bookkeeping across claims.
- `WithRetryWithinClaim(attempts int, initialDelay, maxElapsed time.Duration)`
  — NEW, opt-in, default OFF (jobs-v1 live-verified behavior unchanged).
  Exponential backoff (1s→2s→4s… from `initialDelay`), ctx-cancellable
  `select`. **`maxElapsed` is mandatory (>0) and caps cumulative in-claim time
  including backoff sleeps** — the guard against the lease-overrun landmine:
  a backoff window that outlives the store's claim lease lets a second worker
  re-claim the job mid-retry (memstore reclaims at `claimed_at + lease`;
  SQL stores likewise). The doc comment states: set `maxElapsed` well below
  the store's lease; retries within a claim block that pool worker
  (head-of-line); total process calls are up to durable-attempts ×
  within-claim-attempts. On exhaustion, `Fail` is called exactly once.

**D-8 — validation ↔ web.FieldErrors composition: documentation, not
coupling.** Validators return plain `error`s with the field name in the
message; `web.FieldErrors.AddErr(field, err)` already bridges them (the minor
field-name duplication in messages is accepted parity). `validation.Errors`
(the slice accumulator) serves non-HTTP contexts. No cross-import in either
direction; the composition recipe goes in both package docs. `eventstest`-style
conformance doesn't apply — validation is pure functions, table tests suffice.

**D-9 — sdk/events must match the RATIFIED design §2 exactly** (`roadmap/
events-feature-design.md`) so events-v1 resumes cleanly at its phase 3:
`Event`/`Metadata`/`BaseEvent`/`Handler`/`Subscription`/`Emitter`/`Bus`/
`Broadcaster`/`TypedHandler[T]`/`Record`+`NewRecord`, `Memory` (async default,
`WithSync()`, satisfies `Broadcaster`, `Close` drains), `Noop`, `WakeChannel`,
correlation IDs from `sdk/id`. The ratified sdk-parity scope adds two salvage
items the design references but doesn't spell: `EventEncoder` (custom
serialization seam; `EncodeEvent` falls back to `json.Marshal` — this is what
`NewRecord` uses for `Record.Payload`) and `RemoteEvent` +
`DecodeRemoteMetadata` (the rehydrated-event shape implementing
Event+Metadata+Unmarshaler — `TypedHandler`'s slow path; the redis broadcast
loop needs it). Both are stdlib and consistent with §2's shapes. Trims per the
design: no `WithPriority`, no `EventRegistry`, no `var GenerateID`. The
`Memory` default lives in-package (`events.Memory`, cacher.Memory-style), not
a `memorybus` subpackage.

## Risks

1. **In-process retry vs store lease → double execution** (highest). Mitigated
   by D-7's mandatory `maxElapsed` ceiling + doc contract; the runner test
   suite must include a lease-overrun regression case (task-9).
2. **Rename blast: one missed importer or doc string.** Mitigated by the
   enumerated 30-file list, the two-step (add crud → migrate+delete) so every
   task boundary builds, and a hard grep gate in task-18's verify.
3. **slug.Make behavior change on a recompute path** (cms content-type route
   segments). Shipped types are ASCII; caveat recorded; golden tests updated
   deliberately (task-5).
4. **SSE streams dying at WriteTimeout (15s default)** if the
   `http.ResponseController` per-write deadline extension is dropped in the
   port. Task-21 names it as an acceptance criterion with a
   longer-than-WriteTimeout stream test.
5. **`sdk/events` ships ahead of any in-repo emitter** — admission holds
   (plurality: Memory + redis-streams; conformance: eventstest), but it's
   proven only by suites until events-v1. Accepted by ratification; noted.

---

## Phase 1 — pure stdlib additions (config tags, validation, async, conversion, slug, cryptids)

**DoD:** six sdk concerns added/extended; sdk go.mod untouched (zero requires);
`make check` green. Everything here is verified stdlib-only in the source
(explored 2026-07-06: validation/async/conversion/environment/tags hand-roll
everything incl. the accent table — no x/text anywhere).

### task-1: sdk/config — restore ParseEnvTags

- **depends_on:** []
- **model:** opus
- **files:** [sdk/config/tags.go, sdk/config/tags_test.go, sdk/web/server_env_test.go, sdk/logging/logging_env_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Port `ParseEnvTags(namespace string, cfg any) error` from
  old `sdk/environment/tags.go` (reflection populator honoring `env:"KEY"`,
  `default:`, `separator:`, `required:` tags; precedence env var > existing
  non-zero field > default tag; supports string/int/int64+Duration/bool/
  float/[]string; errors on non-pointer-struct and unsupported kinds; keys
  namespaced via the existing `GetNamespaceEnvKey`). Port its tests. Add
  round-trip tests proving the dormant tags are live: `sdk/web/server_env_test.go`
  populates `web.ServerConfig` via `ParseEnvTags` (env-set, default-fallback,
  and existing-non-zero-field-wins cases) and `sdk/logging/logging_env_test.go`
  does the same for `logging.Options` (test-only imports of sdk/config; no
  production import edge). Precedence statement goes in the func doc — hosts
  building configs via struct literal are unaffected until they opt in.

### task-2: sdk/validation — port the validator library

- **depends_on:** []
- **model:** opus
- **files:** [sdk/validation/validate.go, sdk/validation/errors.go, sdk/validation/custom.go, sdk/validation/validate_test.go, sdk/validation/errors_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Port the full library from old `sdk/validation`: string
  validators (Required/MinLength/MaxLength/OneOf/Email/UUID/URL/Matches + Ptr
  variants), numeric (Min/Max/Range/Positive + Ptr), generic collections
  (NotEmpty/MinItems/MaxItems), common (Slug/SlugPtr, PasswordStrength,
  PasswordsMatch), `IfSet[T]`, the `Errors` slice accumulator
  (Add/Err/HasErrors/All), and custom.go's doc-only custom-validator seam.
  Keep the design invariants in the package doc: reflection-free, field-name-
  first args, nil/empty passes everything except Required. Per D-8, document
  the `web.FieldErrors.AddErr(field, validation.MinLength(...))` composition
  recipe in this package doc (and cross-reference from web's) — no import
  edge either direction. Table tests ported; no conformance suite (pure funcs).

### task-3: sdk/async — bounded fire-and-forget pool

- **depends_on:** []
- **model:** opus
- **files:** [sdk/async/pool.go, sdk/async/pool_test.go]
- **verify:** `cd sdk && go build ./... && go test -race ./async/ && go vet ./...` then `make guard`
- **description:** Port old `sdk/async/pool.go`: `Pool`, `NewPool`,
  `Go`/`GoContext`/`Wait`/`Close(ctx)`/`Stats`, options `WithMaxConcurrency`
  (default 100)/`WithDropOnFull`/`WithShutdownTimeout` (30s)/`WithLogger`,
  presets `IOPreset`/`CPUPreset`, panic recovery + atomic counters. The
  package doc MUST state the boundary with `sdk/workers`: async is a bounded
  fire-and-forget goroutine pool for request-scoped side work (no polling, no
  jobs, no claims); workers is the adaptive-polling worker pool + job Runner.
  Run the suite with -race (it's all concurrency).

### task-4: sdk/conversion — one package, two trims

- **depends_on:** []
- **model:** opus
- **files:** [sdk/conversion/ptr.go, sdk/conversion/cases.go, sdk/conversion/datetime.go, sdk/conversion/json.go, sdk/conversion/slices.go, sdk/conversion/ptr_test.go, sdk/conversion/cases_test.go, sdk/conversion/datetime_test.go, sdk/conversion/json_test.go, sdk/conversion/slices_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Port per D-2: `Ptr`/`Deref`/`DerefOr`; acronym-aware
  `ToPascalCase`/`ToCamelCase`/`ToSnakeCase`/`ToKebabCase`/`ToLowerSpaced`
  with the seeded acronym table as an unexported fixed map (**`AddAcronym` is
  NOT ported** — package-mutable state, D-2 records the trim and the future
  `Caser` seam); `ParseDateTime`/`ParseFlexibleDate` (keep the
  ambiguity warning docs); `JSONOrEmptyObject`/`JSONOrEmpty`;
  `Overlap[T comparable]`. **`urls.go`/`ToURLSlug` is NOT ported** — task-5
  owns the accent table in sdk/slug. Port the five files' tests.

### task-5: sdk/slug — accent folding (a documented behavior change)

- **depends_on:** []
- **model:** opus
- **files:** [sdk/slug/slug.go, sdk/slug/slug_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make check`
- **description:** Fold accents in `Make` using the old `removeAccents`
  hand-rolled rune table (à-å→a, è-ë→e, ì-ï→i, ò-ö→o, ù-ü→u, ý/ÿ→y, ñ→n, ç→c,
  ß→ss/s — stdlib only, no x/text) before the existing keep-[a-z0-9] pass, so
  `Make("Café Résumé") == "cafe-resume"`. This is a behavior change, not an
  upgrade (D-5): update golden vectors deliberately; add the Café Résumé case;
  doc comment records that pre-change persisted slugs (cms entries/terms/
  menus are slugged at write time) are untouched while the
  `content/schema.go` route-segment recompute path shifts for non-ASCII
  `Plural` registrations (shipped seed types are ASCII). Run full `make check`
  — cms feature tests exercise slug call sites.

### task-6: sdk/cryptids — Encrypter port + AESGCM default + SHA256 hasher + JWTSigner port

- **depends_on:** []
- **model:** opus
- **files:** [sdk/cryptids/cryptids.go, sdk/cryptids/aesgcm.go, sdk/cryptids/hasher.go, sdk/cryptids/jwt.go, sdk/cryptids/aesgcm_test.go, sdk/cryptids/hasher_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** New facility package named per D-3. Ports:
  `Encrypter interface { Encrypt(plaintext string) (string, error); Decrypt(ciphertext string) (string, error) }`
  (from old `infrastructure/cryptids/encrypter.go`); `JWTSigner interface {
  Sign(claims map[string]any, expiresAt time.Time) (string, error);
  Verify(token string) (map[string]any, error) }` — **port only**, doc noting
  the golang-jwt integration is deliberately not built (jrazmi has not
  committed to the library); `SHA256Hasher` (deterministic hex fast-hash for
  API keys; doc says explicitly NOT for passwords — auth's `PasswordHasher`
  port + bcrypt integration own that). Default next to the port:
  `cryptids.AESGCM` (from old `cryptids/aesgcm`: 32-byte-key constructor
  error, random nonce, `base64.RawURLEncoding(nonce||ciphertext)`), asserted
  `var _ Encrypter = (*AESGCM)(nil)`; port the compliance/round-trip/tamper
  tests. Old GenerateID/UUID funcs are NOT ported (sdk/id owns IDs — in
  non-goals).

## Phase 2 — tracing spine (sdk/tracing + logging + workers hooks + within-claim retry)

**DoD:** `sdk/tracing` exists (port + Noop, stdlib); `logging` carries
trace_id/span_id again; `workers` has tracer hooks and opt-in within-claim
retry with the lease guard; jobs-v1 default behavior unchanged (all existing
workers/jobs tests pass unmodified except additions); `make check` green.
Exporters stay deferred (`integrations/tracing/otel` is the ratified
fast-follow).

### task-7: sdk/tracing — Tracer port + Noop default

- **depends_on:** []
- **model:** opus
- **files:** [sdk/tracing/tracing.go, sdk/tracing/tracing_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** New package modeled on old `sdk/workers/tracer.go` (the
  stdlib decoupling boundary — old `telemetry/` is otel-aliased and stays
  behind the deferred integration): `Tracer interface { StartSpan(ctx
  context.Context, operationName string) (context.Context, SpanFinisher) }`;
  `SpanFinisher interface { SetAttributes(...Attribute); RecordError(error);
  Finish() }`; `Attribute{Key, Value string}` + `StringAttribute(k, v)`;
  exported `Noop` Tracer (facility default named for what it does, like
  `email.Console`) returning a no-op finisher. Doc comment maps the port to
  the future otel integration (span kind/attribute richness live there; this
  port is deliberately minimal — capability-map ruling #4, ratified).

### task-8: sdk/logging — restore trace/span context + TracingHandler injection

- **depends_on:** [task-7]
- **model:** opus
- **files:** [sdk/logging/context.go, sdk/logging/context_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make check`
- **description:** Restore from old `sdk/logger/tracing.go`:
  `WithTraceID(ctx, id)` / `WithSpanID(ctx, id)` context setters (unexported
  keys beside the existing `requestIDKey`) and extend `TracingHandler.Handle`
  to inject `trace_id`/`span_id` attrs when present (request_id behavior
  unchanged). Purely additive; existing `WithTracing()` option now earns its
  name. `make check` guards the consumers (web middleware imports logging).

### task-9: sdk/workers — WithTracer hooks + WithRetryWithinClaim (supersedes J6/J7)

- **depends_on:** [task-7]
- **model:** opus
- **files:** [sdk/workers/runner.go, sdk/workers/middleware.go, sdk/workers/runner_test.go, sdk/workers/middleware_test.go]
- **verify:** `cd sdk && go build ./... && go test -race ./workers/ && go vet ./...` then `make check`
- **description:** Two additions per D-7, both opt-in, defaults preserving
  jobs-v1 live-verified behavior. (1) `WithTracer(tracing.Tracer)` RunnerOption
  wrapping claim→process in spans (job.process span per attempt, RecordError
  on failure — old runner's shape against the NEW sdk/tracing port) +
  `TracingMiddleware(tracing.Tracer)` pool middleware (old middleware.go).
  (2) `WithRetryWithinClaim(attempts int, initialDelay, maxElapsed
  time.Duration)`: exponential ctx-cancellable backoff inside one claim;
  constructor/option errors (or panics at wire-time — match the package's
  option idiom) on maxElapsed <= 0; cumulative elapsed (processing + backoff)
  never exceeds maxElapsed; on exhaustion `store.Fail` is called exactly once
  with the UNCHANGED durable `maxAttempts`. Doc comment: the two axes
  (durable `WithMaxAttempts` × within-claim attempts multiply), the
  head-of-line worker-blocking cost, and the lease contract (`maxElapsed`
  must sit well below the store's claim lease — cite features/jobs memstore
  reclaim semantics). Tests: retry-then-succeed within claim; exhaustion →
  single Fail; ctx cancel mid-backoff; a lease-overrun regression case
  (fake store with a short lease asserting no double-claim while within-claim
  retry is active under a compliant maxElapsed); race-clean. Log the J6/J7
  supersession in the task commit note for NOTES (docs phase collects it).

## Phase 3 — sdk/events + eventstest + integrations/events/redis-streams

**DoD:** `sdk/events` matches the ratified design §2 (D-9) and passes its own
`eventstest`; the redis-streams module builds hermetically with a loud skip
and passes eventstest live when `REDIS_TEST_ADDR` is set; go.work/Makefile
updated; `make check` green (19 modules). `Mount.Events` and `features/events`
are NOT touched.

### task-10: sdk/events — port + Memory + Noop + WakeChannel + Record

- **depends_on:** []
- **model:** opus
- **files:** [sdk/events/events.go, sdk/events/memory.go, sdk/events/noop.go, sdk/events/wake.go, sdk/events/record.go, sdk/events/events_test.go, sdk/events/memory_test.go, sdk/events/wake_test.go]
- **verify:** `cd sdk && go build ./... && go test -race ./events/ && go vet ./...` then `make guard`
- **description:** Implement design §2 verbatim (D-9): `Event`, `Metadata`
  (the design's rename of old `EventWithMetadata`), `BaseEvent` (+
  `WithTenant`/`WithAggregate` builders, correlation IDs from `sdk/id` —
  **no `var GenerateID`**), `Handler` (doc: implementations must be
  idempotent), `Subscription`, `Emitter` (emit-only narrow port),
  `Bus`, `Broadcaster`, `TypedHandler[T]` (assert fast path / `Unmarshaler`
  slow path), `Unmarshaler`, `EventEncoder` + `EncodeEvent` (json fallback),
  `RemoteEvent` + `DecodeRemoteMetadata`, `Record` + `NewRecord` (EventID from
  sdk/id; payload via EncodeEvent), `EmitOption` + `WithSync` (**no
  WithPriority**), `WakeChannel(bus, topic)` (cap-1 coalesced, lost wakes
  tolerated — pairs with `workers.WithWakeChannel`). `Memory`: async default
  dispatch (bounded queue, drop-on-full warn-logged, `context.WithoutCancel`
  values, panic-recovered workers), `WithSync()` for deterministic delivery,
  trivially satisfies Broadcaster, `Close(ctx)` drains; `Noop`. Salvage the
  old `events.go`/`memorybus` designs; re-type fresh. Race-run the suite.

### task-11: sdk/events/eventstest — the conformance suite

- **depends_on:** [task-10]
- **model:** opus
- **files:** [sdk/events/eventstest/eventstest.go, sdk/events/memory_conformance_test.go]
- **verify:** `cd sdk && go build ./... && go test ./events/... && go vet ./...` then `make guard`
- **description:** `eventstest.Run(t *testing.T, newBus func(t *testing.T)
  events.Bus)` following the cachertest factory pattern, scoped exactly as the
  design §2 rules: assert only the common observable contract —
  subscribe-then-emit delivers; `"*"` wildcard matches; unsubscribe stops
  delivery; no delivery after Close; Close idempotent; `WithSync` completes
  handlers before returning; `TypedHandler` handles both assertion and
  Unmarshaler paths. Delivery-count guarantees are documented per backend,
  never asserted centrally. `Memory` runs it via
  `sdk/events/memory_conformance_test.go`.

### task-12: integrations/events/redis-streams — the Redis Streams bus module

- **depends_on:** [task-11]
- **model:** opus
- **files:** [integrations/events/redis-streams/go.mod, integrations/events/redis-streams/bus.go, integrations/events/redis-streams/broadcast.go, integrations/events/redis-streams/bus_test.go, integrations/events/redis-streams/conformance_test.go, integrations/events/redis-streams/README.md, go.work, Makefile]
- **verify:** `cd integrations/events/redis-streams && go build ./... && go test ./... && go vet ./...` then `make check` (hermetic: conformance skips LOUDLY without REDIS_TEST_ADDR); live leg: `docker run --rm -d -p 6379:6379 redis:7 && REDIS_TEST_ADDR=localhost:6379 go test ./...`
- **description:** New module (19th) wrapping **`github.com/redis/go-redis/v9`
  directly** — one library; the old `goredisdb` indirection is not recreated
  (`New(rdb *redis.Client, log *slog.Logger, opts Options)` takes the caller's
  client, exactly as the old goredisbus did). Port old
  `infrastructure/events/goredisbus`: streams consumer-groups `Bus`
  (lazy XGROUPCREATE MKSTREAM, XReadGroup workers, XACK-always poison-pill
  policy, at-least-once competing-consumer semantics — document them) and
  pub/sub broadcast `Broadcaster` (envelope → `events.RemoteEvent` via
  `DecodeRemoteMetadata`; fan-out, no durability), against the NEW sdk/events
  port (`Record`/`EncodeEvent` naming; old `OutboxEvent` shape does not
  return). Runs `eventstest.Run` env-gated on `REDIS_TEST_ADDR` with a loud
  skip (the POSTGRES_TEST_DSN pattern) so `make check` stays hermetic.
  go.mod follows the bcrypt integration's replace-of-sdk pattern; add to
  go.work and Makefile `MODULES`. README documents delivery guarantees per
  path (streams vs broadcast) and the events-design compatibility note (this
  is the design §1 table's integration row, built early per the 2026-07-06
  ratification). MUST NOT import features/*; run `make guard`.

## Phase 4 — sdk/oauth + provider integrations (google, github)

**DoD:** `sdk/oauth` port + PKCE in sdk (stdlib); two provider modules build
and pass httptest-driven unit tests hermetically; taxonomy amendment landed
BEFORE the github module; go.work/Makefile updated; `make check` green
(21 modules).

### task-13: ARCHITECTURE taxonomy amendment — what an integration isolates

- **depends_on:** []
- **model:** fable
- **files:** [ARCHITECTURE.md, features/README.md]
- **verify:** `make guard` (docs-only; guards prove nothing broke)
- **description:** Precondition for task-16 (D-1, flagged by
  lead-backend-engineer). Amend ARCHITECTURE.md's taxonomy row + "earns its
  existence" sentence: an integration isolates exactly one external
  dependency — **a third-party library OR an external vendor's live API
  contract** (vendor-specific connectors are never sdk defaults even when
  stdlib-implementable, because sdk defaults must be vendor-neutral and a
  vendor connector's surface churns on the vendor's schedule). Explicitly
  distinguish the R3 `stores/memory` refusal (isolated nothing external —
  still forbidden). Add one sentence to features/README.md §2's R3 paragraph
  cross-referencing the distinction. Keep the diff surgical — these are two
  load-bearing documents.

### task-14: sdk/oauth — Provider port + PKCE

- **depends_on:** []
- **model:** opus
- **files:** [sdk/oauth/oauth.go, sdk/oauth/pkce.go, sdk/oauth/pkce_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Port old `infrastructure/oauth/oauth.go` + `pkce.go`
  (both stdlib): `Provider` interface with the full EIGHT methods — `Name`,
  `SupportsOIDC`, **`TrustEmailVerification`** (present in the old port beyond
  the seven ratified-scope-listed methods; keep it — it's the account-linking
  policy bit), `GetAuthorizationURL(state, codeVerifier, nonce, redirectURI)`,
  `ExchangeCode`, `GetUserInfo`, `ValidateIDToken`, `RefreshToken`; shared
  types `TokenResponse`/`UserInfo`/`IDTokenClaims`/`ProviderConfig`;
  `GenerateCodeChallenge(verifier)` (S256). No default impl ships (D-1 —
  port-only facility; doc comment says providers live in
  `integrations/oauth/*`). Never authz/authn in any identifier or comment —
  authentication/authorization spelled out.

### task-15: integrations/oauth/google — go-oidc provider module

- **depends_on:** [task-14]
- **model:** opus
- **files:** [integrations/oauth/google/go.mod, integrations/oauth/google/google.go, integrations/oauth/google/google_test.go, integrations/oauth/google/README.md, go.work, Makefile]
- **verify:** `cd integrations/oauth/google && go build ./... && go test ./... && go vet ./...` then `make check`
- **description:** New module (20th) wrapping **`github.com/coreos/go-oidc/v3`**
  (verified: the old provider hand-rolls its HTTP flows on net/http — go-oidc
  is the single external library; no x/oauth2). Port old `googleoauth`:
  OIDC discovery at construction (fail-fast network call — document it),
  PKCE S256 authorization URL (`access_type=offline`, `prompt=consent`,
  nonce), code exchange/refresh, userinfo, `ValidateIDToken` via the go-oidc
  verifier (JWKS), 1MB response caps. Package `google`, type named for the
  vendor. Port/adapt the old tests to httptest fakes where the old suite had
  them (discovery + token endpoints faked; no live Google in CI). go.work +
  Makefile MODULES + bcrypt-pattern go.mod.

### task-16: integrations/oauth/github — stdlib-only provider module

- **depends_on:** [task-13, task-14]
- **model:** opus
- **files:** [integrations/oauth/github/go.mod, integrations/oauth/github/github.go, integrations/oauth/github/github_test.go, integrations/oauth/github/README.md, go.work, Makefile]
- **verify:** `cd integrations/oauth/github && go build ./... && go test ./... && go vet ./...` then `make check`
- **description:** New module (21st), **zero external requires** (sdk +
  stdlib), placed per D-1 under the task-13 amendment — the README's first
  paragraph states the placement rationale (vendor API contract is the
  isolated dependency). Port old `githuboauth`: no construction-time network,
  `SupportsOIDC()=false` / `TrustEmailVerification()=false`,
  `ValidateIDToken` returns the documented "OIDC not supported" error,
  GitHub-Apps-aware `RefreshToken`, hand-rolled exchange/userinfo on net/http
  with httptest-faked tests. go.work + Makefile MODULES + bcrypt-pattern
  go.mod.

## Phase 5 — sdk/repository → sdk/crud (the breaking rename + fop restore)

**DoD:** `sdk/crud` carries the D-6 contract; `sdk/repository` no longer
exists; all 30 importers migrated; `grep -rn 'sdk/repository'` across code AND
docs returns nothing; full `make check` green; zero behavior change in SSR
consumers (clamping semantics kept at every existing call site).

**Ordering note:** this phase MUST land fully green before phase 6 — the web
merge's OpenAPI pagination schema is authored against `crud.Page`'s json tags,
and both phases touch `examples/*` (no interleaving).

### task-17: create sdk/crud (new package, old one still standing)

- **depends_on:** []
- **model:** opus
- **files:** [sdk/crud/crud.go, sdk/crud/order.go, sdk/crud/pagination.go, sdk/crud/cursor.go, sdk/crud/crud_test.go, sdk/crud/order_test.go, sdk/crud/pagination_test.go, sdk/crud/cursor_test.go]
- **verify:** `cd sdk && go build ./... && go test ./crud/ ./repository/ && go vet ./...` then `make guard`
- **description:** Implement D-6 exactly: `crud.go` (DefaultLimit/MaxLimit/
  ErrNotFound alias, `Reader[T, F]` with `List(ctx, filter F, req
  ListRequest) (Page[T], error)`, `Writer[T, C, U]` unchanged,
  `CRUD[T, F, C, U]`, non-generic `ListRequest{Limit, Cursor, Order}` with
  `NormalizedLimit()` kept, `Page[T]` with json tags + `HasPrev`/
  `PreviousCursor`); `order.go` (ASC/DESC, Order, OrderField, NewOrder,
  ParseOrder — old fop semantics: error on unknown field/direction, default
  on empty); `pagination.go` (`TrimPage` in the NEW-repo shape returning
  `Page[T]`, restored `MarkPrevPage` adapted to set `Page.HasPrev`/
  `PreviousCursor` — full prev window ⇒ PreviousCursor from prevRecords[0],
  any prev records ⇒ HasPrev; strict `ParseListRequest(limitStr, cursor
  string, maxLimit int) (ListRequest, error)` — empty→DefaultLimit,
  non-numeric/≤0/>maxLimit→error, NEVER clamps); `cursor.go` moved verbatim
  from sdk/repository. Package doc pins the two-semantics rule (D-6: strict
  parse at JSON transport edges, clamp at store edges) and the
  convenience-never-a-tax stance (nothing is required to implement
  `Reader[T,F]`). `sdk/repository` is NOT deleted in this task — both build,
  so the task boundary is git-reset-safe.

### task-18: migrate all importers, delete sdk/repository, sweep the string

- **depends_on:** [task-17]
- **model:** opus
- **files:** [features/cms/logic/content/entry_repository.go, features/cms/internal/logic/entrysvc/service.go, features/cms/internal/logic/entrysvc/service_test.go, features/cms/internal/inbound/http/entries.go, features/cms/internal/inbound/http/public.go, features/cms/internal/inbound/http/public_views_test.go, features/cms/internal/inbound/http/helpers_test.go, features/cms/storetest/storetest.go, features/cms/storetest/reference_test.go, features/cms/stores/postgres/entries.go, features/cms/stores/postgres/menus.go, features/cms/stores/postgres/terms.go, features/cms/stores/turso/entries.go, features/cms/stores/turso/menus.go, features/cms/stores/turso/terms.go, features/jobs/logic/job/job.go, features/jobs/logic/schedule/schedule.go, features/jobs/jobs_test.go, features/jobs/memstore/queue.go, features/jobs/memstore/schedules.go, features/jobs/storetest/storetest.go, features/jobs/internal/logic/queuesvc/service_test.go, features/jobs/internal/logic/runtime/runtime_test.go, features/jobs/internal/logic/schedulesvc/service_test.go, features/jobs/stores/postgres/queue.go, features/jobs/stores/postgres/schedules.go, features/jobs/stores/turso/queue.go, features/jobs/stores/turso/schedules.go, examples/auth-cms/internal/memstore/memstore.go, examples/minimal/internal/memstore/memstore.go, ARCHITECTURE.md, sdk/README.md]
- **verify:** `make check` && `! grep -rn 'sdk/repository' --include='*.go' .` && `! grep -rn 'sdk/repository' ARCHITECTURE.md sdk/README.md README.md RELEASING.md features/*/README.md 2>/dev/null` ; then delete sdk/repository/ and re-run `make check`; optional-if-creds: `make test-stores`
- **description:** Mechanical migration of the 30 enumerated Go files
  (import path `gopernicus/sdk/repository` → `gopernicus/sdk/crud`, qualifier
  `repository.` → `crud.`; features/auth imports nothing — verified), then
  delete `sdk/repository/` entirely, then the grep gates. NO semantic changes:
  every existing `NormalizedLimit`/clamping call site keeps clamping (D-6);
  nobody adopts `Reader[T,F]`, `Order`, `ParseListRequest`, or the prev-page
  fields in this task. Docs touched here (not the docs phase) so the grep
  gate closes atomically: ARCHITECTURE.md line ~78 ("the generic `repository`
  CRUD shape" → `crud`), sdk/README.md package-table row. `.claude/plans/`
  historical mentions are left as written (history, not live docs).

## Phase 6 — sdk/web JSON-API surface merge (additive)

**DoD:** the old JSON kit, route groups/verb sugar, SSE, static server, and
OpenAPI generator live in `sdk/web` alongside the untouched SSR kernel
(render.go, cache.go, run.go, existing middleware unmodified); all old web
tests ported and green; run-and-look SSR regression check on examples/cms
passed; `make check` green.

**Standing constraints for every task in this phase** (from consultation):
verb sugar and `Group` stay on the concrete `*WebHandler`/`*RouteGroup` only —
**`feature.RouteRegistrar` is never widened** (hosts' minimal routers and
`PrefixRegistrar` depend on its exact one-method shape); `openapi.go` stays an
app-driven spec builder (callers pass `[]RouteSpec`), never a route
introspector — sdk/web must not learn app routes; genuine collisions found
beyond those documented here get surfaced, not silently merged.

### task-19: JSON request/response kit + error sentinels

- **depends_on:** []
- **model:** opus
- **files:** [sdk/web/request.go, sdk/web/response.go, sdk/web/errors.go, sdk/web/request_test.go, sdk/web/response_test.go, sdk/web/errors_test.go]
- **verify:** `cd sdk && go build ./... && go test ./web/ && go vet ./...` then `make check`
- **description:** Port from old sdk/web: `request.go` (`Param`/`QueryParam`,
  `Decode[T]`/`DecodeJSON[T]` with the unexported `validator{ Validate()
  error }` auto-validate — composes with the existing `FieldErrors.Err()`);
  into the existing `response.go`: `RespondJSON`/`RespondJSONOK`/
  `RespondJSONCreated`/`RespondJSONAccepted`/`RespondJSONError`/
  `RespondJSONDomainError` (≥500 → the existing `RecordError` seam with the
  original error) /`RespondStream` (old exact names — the ratified scope's
  "JSONOK/Created/Accepted" shorthand refers to these); into the existing
  `errors.go`: `ErrValidation` (precedence: `*http.MaxBytesError` → 413,
  `FieldErrors` → 400 with per-field detail, else 400 bad_request) and the
  sentinels `ErrPayloadTooLarge` (413 payload_too_large),
  `ErrTooManyRequests` (429 rate_limit_exceeded), `ErrUnavailable`
  (503 unavailable). `Error`/`FieldError`/`FieldErrors`/`ErrFromDomain` are
  byte-identical between repos (verified) — do not touch them. Port the three
  old test files' relevant cases, including the MaxBytesError→413 case.

### task-20: RouteGroup + verb sugar

- **depends_on:** [task-19]
- **model:** opus
- **files:** [sdk/web/groups.go, sdk/web/methods.go, sdk/web/groups_test.go]
- **verify:** `cd sdk && go build ./... && go test ./web/ && go vet ./...` then `make check`
- **description:** Port `groups.go` (`RouteGroup`, `WebHandler.Group(prefix,
  mw...)`, nested `Group` accumulating prefix+middleware, trailing-slash
  trim) and `methods.go` (GET/POST/PUT/DELETE/PATCH on both `*WebHandler` and
  `*RouteGroup`, each delegating to `Handle("VERB", ...)`). Reconciliation
  with the new optional-method `Handle` is nil — the old signature is
  identical and the sugar always passes a non-empty method; `RouteGroup.Handle`
  passes an empty method through unchanged (document that groups support the
  `/{$}` empty-method pattern too). Per the standing constraint:
  concrete types only; `feature.RouteRegistrar` untouched (add a test
  asserting `*WebHandler` still satisfies it).

### task-21: SSE primitives (events-v1 phase 1, pulled forward)

- **depends_on:** []
- **model:** opus
- **files:** [sdk/web/sse.go, sdk/web/stream.go, sdk/web/sse_test.go, sdk/web/sse_heartbeat_test.go, sdk/web/stream_test.go]
- **verify:** `cd sdk && go build ./... && go test -race ./web/ && go vet ./...` then `make check`
- **description:** Port `SSEEvent`, `SSEStream` (channel-fed, `WithHeartbeat`
  comment frames, ctx-done/channel-close loop), `StreamWriter`
  (`Send`/`SendJSON`/`SendData`, lazy header write — the respond-or-upgrade
  writer; ratified scope includes it, overriding the events design's
  "unless free" note), `AcceptsStream`. **Acceptance criterion, named:** the
  `http.ResponseController` per-write `SetWriteDeadline` extension (window =
  heartbeat×4, 2m default) must survive the port and be proven by a test that
  streams longer than a short `WriteTimeout` on an `httptest.Server` — the
  new `ServerConfig` defaults WriteTimeout to 15s and a stream without the
  extension dies there (events design §1 finding; risk 4). The new
  `statusWriter.Unwrap` already lets ResponseController reach Flush through
  the Logger middleware — add a test through the middleware stack.

### task-22: StaticFileServer (SPA fallback + immutable caching)

- **depends_on:** [task-20]
- **model:** opus
- **files:** [sdk/web/static.go, sdk/web/static_test.go]
- **verify:** `cd sdk && go build ./... && go test ./web/ && go vet ./...` then `make check`
- **description:** Port `StaticFileServer` over `fs.FS`: `WithAssetPrefix`
  (default "assets/" → `Cache-Control: public, max-age=31536000, immutable`),
  `WithSPAMode` (missing/dir/root → index.html with no-store headers),
  `AddRoutes(handler, basePath, mw...)` registering `GET {basePath}/{path...}`
  + the trailing-slash redirect, range support via ServeContent. Depends on
  task-20 only for `AddRoutes`' use of the handler surface. Old `capture.go`
  is not needed by this port (verified) — if the implementer finds otherwise,
  surface it rather than porting silently.

### task-23: OpenAPI 3.1 generator

- **depends_on:** [task-19]
- **model:** opus
- **files:** [sdk/web/spec.go, sdk/web/openapi.go, sdk/web/openapi_test.go]
- **verify:** `cd sdk && go build ./... && go test ./web/ && go vet ./...` then `make check`
- **description:** Port `RouteSpec`/`SpecQueryParam`/`ParamSchema` (spec.go)
  and `BuildOpenAPISpec`/`WebHandler.ServeOpenAPI` (openapi.go: deterministic
  path→method sort, struct-reflection component schemas with embedded
  flattening/json tags/pointer-optional/time→date-time, bearerAuth scheme,
  401s for Authenticated routes, Paginated wrapper + query params). Two
  adaptations: (1) path params are ServeMux-native `{id}` — port
  `colonToOpenAPIPath` as a compatibility shim but document `{id}` as the
  canonical form; (2) the hand-written `Pagination` component schema is
  authored to match `crud.Page`'s json tags exactly (items, next_cursor,
  has_more, has_prev, previous_cursor) — kept as a literal map, NO
  web→crud import (looser coupling; the phase-5-before-6 ordering keeps the
  shapes honest). Builder-only, per the standing constraint.

### task-24: CORS + DefaultHeaders middleware + SSR regression gate

- **depends_on:** [task-19]
- **model:** opus
- **files:** [sdk/web/middleware.go, sdk/web/middleware_test.go]
- **verify:** `cd sdk && go build ./... && go test ./web/ && go vet ./...` then full `make check`; then run-and-look: `make run` and drive examples/cms in a browser — admin list/create/edit an entry, public home + /blog + one entry page, confirm X-Cache HIT on a second public load; green tests alone do not close this phase
- **description:** Port `CORSMiddleware(origins []string)` (allowlist/`*`
  echo semantics, credentials only for non-wildcard, OPTIONS 204
  short-circuit) and `DefaultHeadersMiddleware(headers map[string]string)`
  into the existing middleware.go alongside Panics/Logger/RequestID —
  constructors only, no HandlerOptions (D-4). Then execute this phase's
  run-and-look: the merge is additive and hosts register nothing new, so the
  check is an SSR no-regression drive of examples/cms as described in verify.

## Phase 7 — sdk/email template layer (lowest priority; explicit fast-follow allowed)

**DoD:** template rendering (registry/layouts/branding) available as an
optional layer over `email.Sender`; SMTP actually sends HTML when present;
existing Console/SMTP behavior otherwise unchanged; `make check` green. If the
milestone needs to cut scope, THIS phase detaches cleanly as a fast-follow —
nothing later depends on it.

### task-25: template registry + renderer over email.Sender

- **depends_on:** []
- **model:** opus
- **files:** [sdk/email/templates.go, sdk/email/renderer.go, sdk/email/emailer.go, sdk/email/templates/layouts/transactional.html, sdk/email/templates/layouts/transactional.txt, sdk/email/templates/layouts/marketing.html, sdk/email/templates/layouts/marketing.txt, sdk/email/templates/layouts/minimal.html, sdk/email/templates/layouts/minimal.txt, sdk/email/templates_test.go, sdk/email/emailer_test.go]
- **verify:** `cd sdk && go build ./... && go test ./email/ && go vet ./...` then `make guard`
- **description:** Port old `infrastructure/communications/emailer` core
  (stdlib-verified: html/template + embed.FS + sync): `TemplateRegistry`
  (layered Infra/Core/App resolution, namespaced `"ns:name"` content
  templates, `RegisterTemplates`/`RegisterLayouts`, `RenderWithLayout` with
  the strip-HTML text fallback), `Renderer`/`SendRequest`/`RenderConfig`/
  `WithLayout`/layout consts, `Branding`/`SocialLink`, embedded default
  layouts, and an `Emailer` adapted to wrap the NEW `email.Sender` +
  `email.Message` (old `Client`/`Email` do not return; `RenderAndSend`
  produces a Message with both HTML and Text set, applies default From,
  validates via Message.Validate). sendgrid/stdout subpackages are NOT ported
  (OUT list). Options: `WithLogger`/`WithContentTemplates`/`WithLayouts`/
  `WithBranding` as in the original.

### task-26: SMTP multipart/alternative

- **depends_on:** [task-25]
- **model:** opus
- **files:** [sdk/email/smtp.go, sdk/email/smtp_test.go]
- **verify:** `cd sdk && go build ./... && go test ./email/ && go vet ./...` then `make check`
- **description:** Today `Message.HTML` is silently unsent (`buildMessage`
  emits text/plain only) — the template layer would be dishonest without
  this. Extend `buildMessage`: when HTML is empty, byte-identical current
  behavior (assert with the existing tests); when HTML is set, emit
  `multipart/alternative` (text part first, HTML second, stdlib
  mime/multipart, proper headers). Console unchanged (it logs, never
  renders).

## Phase 8 — docs sync + milestone close

### task-27: docs sync (ARCHITECTURE, sdk/README, RELEASING, Makefile header, events design, NOTES)

- **depends_on:** [task-12, task-16, task-18, task-24]
- **model:** fable
- **files:** [ARCHITECTURE.md, sdk/README.md, RELEASING.md, Makefile, .claude/plans/roadmap/events-feature-design.md, NOTES.md]
- **verify:** `make check` (full, 21 modules) then a fresh-eyes grep pass: `grep -rn 'eighteen\|18 modules\|8 modules' ARCHITECTURE.md README.md RELEASING.md Makefile sdk/README.md` returns only intentional text
- **description:** (1) ARCHITECTURE.md: module map + "Eighteen modules today"
  → twenty-one, add the three integration rows (the taxonomy amendment
  itself landed in task-13). (2) sdk/README package table: add rows for
  validation, async, conversion, tracing, cryptids, oauth, events
  (+eventstest); the repository row became crud in task-18 — confirm; note
  web's grown surface and email's template layer in their rows. (3)
  RELEASING.md: module enumeration 18→21. (4) Makefile: the stale header
  comment ("8 modules") — correct it in the same pass (MODULES itself was
  updated in tasks 12/15/16). (5) events-feature-design.md status header:
  amend — SSE primitives (its phase 1) and `sdk/events`+`eventstest` (its
  phase 2) landed in sdk-parity, `integrations/events/redis-streams` built
  early superseding §9's deferral (name reconciled from `redis`); events-v1
  resumes at its phase 3 (Mount.Events). (6) NOTES.md dated milestone entry:
  what shipped, the J6/J7 supersession and lease-guard design, the slug
  mixed-corpus caveat (D-5), the crud rename artifact (30 files, grep-clean),
  the D-1 taxonomy amendment, open flags for jrazmi (below).

## Sequencing

Phases run in order 1→8. Hard edges: task-7 before tasks 8/9 (tracing port
first); task-10→11→12 (port → suite → integration); task-13 & task-14 before
task-16 (amendment + port before the github module); **phase 5 fully green
before phase 6 starts** (OpenAPI pagination schema authored against
crud.Page; both phases touch examples/*); task-17 before task-18 (both
packages coexist so every task boundary is `git reset`-reversible in spirit —
the repo isn't git-managed, so "reversible" means each task ends buildable);
task-19 before 20/23/24; task-25 before 26. Phases 2, 3, 4 are mutually
independent (any could reorder if a blocker appears) but default sequential.
Phase 7 may detach as a fast-follow without affecting phase 8 (its doc rows
would move with it).

## Consultation notes

`lead-backend-engineer` reviewed the full sketch (single hop). Verdict:
ship-with-edits. Findings incorporated: (a) the in-process-retry/lease
double-execution landmine → D-7's mandatory `maxElapsed` ceiling, the
`WithRetryWithinClaim` name (de-overloading "attempts"), the N×M and
head-of-line docs, and task-9's lease-overrun regression test; (c) the
zero-require github module contradicting R3's earn-your-module wording → the
explicit taxonomy-amendment precondition (task-13) rather than an implicit
reading; slug reframed from upgrade to behavior change with the
`content/schema.go` recompute-path finding (D-5, task-5); web-merge
guardrails written as standing constraints (never widen `RouteRegistrar`;
SSE's ResponseController deadline extension as a named acceptance criterion;
openapi stays a builder); phase 5-before-6 confirmed as required, not
preference; `redis` vs `redis-streams` naming reconciled explicitly; the
stale Makefile "8 modules" header added to the docs sweep. The lead confirmed
the "nothing implements Reader/Writer/CRUD" claim independently (the rename
is genuinely the whole blast radius) and endorsed the non-generic-ListRequest
/ generic-interfaces split.

## Open questions

_None blocking._ Three items are flagged for jrazmi at review, with defaults
already chosen so no task waits: (1) D-1 github placement + taxonomy
amendment — the one genuinely rule-amending call in the plan; (2) the D-2
`AddAcronym` trim (restorable in an hour if a host needs it); (3) whether a
live `make test-stores` run should gate phase 5 (default: recommended, not
gating — the rename is import-path-mechanical).

## Recommended reviews

- **product-manager** — scope discipline: phase 7's fast-follow option, the
  events-integration-before-emitter call, and whether 8 phases is the right
  release grain.
- **architecture-steward** — task-13's taxonomy amendment (D-1), the crud
  contract shape (D-6), `sdk/events` fidelity to the ratified design (D-9),
  and the phase-6 standing constraints.
- **lead-backend-engineer** — re-review of D-7's lease guard and task-9's
  test list (pre-write consult already done; this is the post-write pass).
- **platform-sre** — three new modules' go.mod/replace hygiene, the env-gated
  redis conformance pattern, RELEASING implications of the pre-tag breaking
  rename, degraded-mode docs for the redis bus.
- **data-integration-reviewer** — task-18's 30-file sweep across both dialect
  store trees + storetest suites, and the no-behavior-change claim.

## Notes

- Salvage sources (read, re-type fresh; never copy import paths):
  old `sdk/environment/tags.go`, `sdk/validation/*`, `sdk/async/pool.go`,
  `sdk/conversion/*`, `sdk/web/*` (13 files), `sdk/fop/*`,
  `sdk/workers/{runner,middleware,tracer}.go`, `sdk/logger/tracing.go`,
  `infrastructure/cryptids/{encrypter,hasher,jwt}.go` + `aesgcm/`,
  `infrastructure/oauth/*` + both providers, `infrastructure/events/*` +
  `memorybus/` + `goredisbus/` + `eventstest/`,
  `infrastructure/communications/emailer/` (core three files + layouts).
  Old `telemetry/` is otel-aliased end to end — model shapes only, port
  nothing from it directly.
- Auth naming rule holds everywhere: authentication/authorization
  (authenticator/authorizer), never the abbreviated forms.
- The repo is not a git repo: "reversible task" means every task boundary
  leaves all 21 modules building; the two-step rename (task-17/18) exists for
  exactly this reason.
