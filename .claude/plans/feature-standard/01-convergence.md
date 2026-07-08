# feature-standard — convergence: bring sdk + three features up to the standard

Status: **RATIFIED 2026-07-07 (jrazmi)** alongside `00-charter.md`.
Signature ruling (A2/E1): method form — `func (s *Service) Register(m
feature.Mount) error`; auth's promoted user-registration use-case is
`RegisterUser`. Sequencing gate 1 is SATISFIED at ratification: auth-v2
CLOSED 2026-07-07 (NOTES.md; plans in `.claude/past/auth-v2/`), so Phase A
executes against main's post-A10 state.
Origin: structure review session 2026-07-07.

## Goal

Make the ratified extension model true in code: auth gains its public driving
port and loses its hand-rolled responders; cms sheds its third-party deps from
the core (content pipeline now, views next); the sdk gains the small pieces the
standard leans on; the six store modules get a helper-duplication audit. jobs
takes exactly one amendment — its `Register` rebuilds and discards a Service
(`jobs.go:287-288`), the same seam FS2 retires in auth (Phase E) — and is
otherwise the conformance proof for the W3 guards (passes all of them with
zero change).

## Sequencing — read first

1. **After auth-v2 closes.** The auth-v2 milestone (`.claude/plans/auth-v2/`,
   A2–A6 on main, 07a/07b/09/10 in flight in a parallel session) touches
   `auth.go`, `internal/inbound/http`, and both stores. Phase A below rewrites
   the same files' public shape. Land auth-v2 first; rebase this plan's Phase A
   over its final state. The lone exception an executor may pull forward:
   task A3 (responder cleanup) if auth-v2's 01-debts wants it — coordinate in
   the other session, don't do it twice.
2. **Before repo-hardening's first tags.** Phase A changes auth's public API,
   Phase E changes jobs's, and Phase B changes cms's module graph.
   repo-hardening tasks 8–10 cut the first version tags (double-gated on
   events-v1 close + LICENSE), and `features/cms` is in the task-9 slice. All
   breaking shape changes in this plan MUST land before any module they touch
   is tagged — after tags, the same change costs a major-version story. The
   MUST gets an enforcement hook at ratification: record "feature-standard
   B1+B2 landed (or consciously waived by jrazmi)" as a named precondition in
   repo-hardening task-9's depends-on, and append a sync note to task-10's
   now-stale "features/cms@v0.1.0 drags templ/goldmark/bluemonday in as real
   requires" sentence.
3. **Before events-v1 starts building.** events must be born conforming (FS
   guards green on day one), so Phase C (sdk pieces) and the charter precede
   it; Phases A/B may run in parallel with events planning but not after its
   code starts copying auth's current shape.

## Module / API impact

- `features/auth`: public API grows (use-case methods on `Service`);
  `Register` signature changes (breaking, pre-tag so cheap — FS2).
- `features/cms`: core go.mod loses goldmark + bluemonday (B1), then templ
  including the `tool` directive (B2); new sibling module
  `features/cms/views/templ` (B2) — added to `go.work` and the Makefile
  `MODULES` list, and the Makefile `generate` target + header comment
  (`Makefile:3,18-19`) retarget from `features/cms` to the new module. Core
  exported surface otherwise unchanged in B1.
- `features/jobs`: `Register` signature changes to consume a built Service
  (Phase E; breaking, pre-tag so cheap — FS2).
- `sdk/web`: one additive file (html/template Renderer adapter).
- `sdk/feature`: additive types (`Route`, `Group`).
- No schema/datastore impact anywhere in this plan. No generated files beyond
  templ regeneration in B1/B2.

## Tasks

### Phase A — auth: the driving surface (FS2, FS9)

### task-A1: promote use-cases onto the public Service

Thin delegation from `auth.Service` to the sealed `authsvc.Service`, in the
same style as the existing `RequireUser`/`CurrentUser` promotions
(`features/auth/auth.go:376-414`). Promote by subsystem, mirroring the
deny-by-absence groups so docs read one way:

- session lifecycle: `Register` (rename on Service to avoid clashing with the
  mount func — see A2), `Login`, `Logout`, `Verify`
- passwords: `ChangePassword`, `ForgotPassword`, `ResetPassword`
- oauth: `StartOAuth`, `StartLink`, `OAuthCallback`, `VerifyLink`,
  `ListLinked`, `Unlink`
- machine identity: `CreateServiceAccount`, `ListServiceAccounts`,
  `MintAPIKey`, `ListAPIKeys`, `RevokeAPIKey`
- token: `IssueToken`
- invitations (enumerated 2026-07-07 from the internal `InvitationService`
  interface, `internal/inbound/http/invitation.go`): `Create`,
  `ListByResource`, `Mine`, `Accept`, `Decline`, `Cancel`, `Resend` — seven
  methods, no more. Their `invitationsvc.CreateInput`-style input/result
  types become public aliases per the existing `Principal = authsvc.Principal`
  precedent (`auth.go:120`).

Subsystem-off calls return the same domain errors the internal service already
yields (no new error vocabulary). Doc comment on `Service` states FS2: the
shipped HTTP layer is an optional adapter over exactly this surface.

### task-A2: Register consumes a built Service

Replace the internal rebuild (`auth.Register` calling `NewService` —
`features/auth/auth.go:423-441`) with the FS2 shape: host builds once, mounts
once (`jobs.NewRuntime(svc)` is the consuming-artifact precedent; jobs's own
`Register` gets the same fix in Phase E). Signature decision for the executor
to surface at plan-ratification:

- (a) method: `func (s *Service) Register(m feature.Mount) error` — reads as
  "mount this service"; clashes with the promoted user-registration use-case
  name from A1 (resolve by naming the use-case `RegisterUser` — recommended)
- (b) package func: `func Register(m feature.Mount, s *Service) error` — keeps
  the `feature.Register`-style call shape hosts already know

Update `examples/auth-cms/cmd/server/main.go:121-132` (the built-twice site —
`NewService` at :126, `Register` at :130) and every doc that quotes the old
signature (`features/README.md`, auth-v2 09/10 artifacts).

### task-A3: responder cleanup

Three sites (the complete set as of 2026-07-07 — steward-verified):

1. `writeJSON`/`writeError`
   (`features/auth/internal/inbound/http/http.go:322-335`): replace call
   sites with `web.RespondJSON`/`web.RespondJSONOK`/`web.RespondJSONCreated`/
   `web.RespondJSONAccepted`/`web.RespondJSONError`; the
   `writeError(w, web.ErrFromDomain(err))` sites become
   `web.RespondJSONDomainError(w, err)`; delete both helpers.
2. + 3. `writeUnauthorized`/`writeTooManyRequests`
   (`features/auth/internal/logic/authsvc/service.go:757,766`) — middleware
   writing hand-rolled JSON from inside the hexagon; replace with
   `web.RespondJSONError(w, web.ErrUnauthorized(…))` /
   `web.NewError(http.StatusTooManyRequests, …)` equivalents.

Byte-compare a sample of response bodies before/after (status, content-type,
JSON shape) — the sdk responders must be a drop-in or the diff is wrong.
Note: auth-v2's `01-debts.md` contains no responder scope as of 2026-07-07,
so this task has no standing collision — if the parallel session adds one,
coordinate so it's done exactly once.

Verify (each A task): `go build ./... && go vet ./...` at repo root via
go.work; `go test ./features/auth/...`; `make check`.

Real-interaction check (Phase A): run `examples/auth-cms`, drive
register → verify → login → whoami → logout against the running server (curl
or browser), plus one custom-handler proof: a scratch host handler calling
`svc.Login` directly, demonstrating FS2's bypass tier actually works.

### Phase B — cms: shed the core deps (FS1, FS10, FS3)

### task-B1: pull goldmark + bluemonday; content is plain text

`RenderMarkdown` (`features/cms/internal/inbound/http/views/markdown.go`, sole
caller `content_seeds.templ`) becomes an escape-to-HTML plain-text renderer:
stdlib `html.EscapeString`, paragraphs on blank lines, no other markup. Rename
to say what it now does (`RenderPlainText`). Regenerate templ; drop both
requires + transitive tail from `features/cms/go.mod`; `go mod tidy`.

Real-interaction check: run `examples/cms`, create an entry with blank-line
paragraphs plus a `<script>alert(1)</script>` body, view the public page —
paragraphs render, script shows as escaped text (this check replaces
bluemonday's job; it MUST pass before the dep leaves).

### task-B2: extract `views/templ`; Views port in core

New sibling module `features/cms/views/templ` holding the templ
implementations + `theme` defaults; core defines the Views port(s) and mounts
HTML routes only when non-nil (FS3 uniform nil → off). `theme.PublicViews`
migrates here and its coverage gap (admin views are hardcoded internal, not
behind the port) is closed as part of the move. Update `examples/cms`,
`examples/minimal`, `examples/auth-cms` wiring (+ one import line each).

Tooling moves that ride B2 (steward finding 7 — each is a silent-breakage
path if forgotten):

- the `tool github.com/a-h/templ/cmd/templ` directive moves from
  `features/cms/go.mod` to the new module's go.mod;
- the Makefile `generate` target and header comment (`Makefile:3,18-19` —
  `cd features/cms && go tool templ generate`, "features/cms/go.mod pins
  templ") retarget to `features/cms/views/templ`;
- `features/cms/views/templ` joins `go.work` and the Makefile `MODULES` list;
- repo-hardening task-5's CI pins templ via that exact go.mod path — append
  the sync note to that plan (see Sequencing rule 2's enforcement hook).

This is the big cms task — the executor should split it into its own
sub-plan when reached; B1 does not wait for it.

### task-B3 (deferred, demand-driven): cms public Service

FS2 applied to cms, per-domain as a host need appears. Recorded here so the
charter's "cms converges staged" sentence has an anchor; no work scheduled.

### Phase C — sdk: the pieces the standard leans on

### task-C1: html/template Renderer adapter in sdk/web

Stdlib-only (belongs in sdk by the kernel's own rule): wrap a
`*template.Template` + name + data as a `web.Renderer`. One file + tests,
following `render.go`'s doc style. This is what makes "stdlib templates
instead of templ" a three-line host choice (FS3's promise).

### task-C2: `feature.Route` as data + `feature.Group` wrapper

Additive in `sdk/feature`: the `Route` struct (Name, Method, Path, Handler,
Middleware) and a `Group{Prefix, Middleware, Next}` registrar wrapper
(composition, `PrefixRegistrar` style — the contract stays one method).
Features adopt route-tables-as-data opportunistically (auth during Phase A is
natural); the public `Config.Routes` hook stays unshipped (FS7).

Verify (Phase C): sdk go.mod still has no require block; `go test ./sdk/...`.

### Phase D — stores: rich connector, thin adapter (FS5)

### task-D1: helper-duplication audit

Diff the helper surface across the six store modules
(`{auth,cms,jobs}/stores/{pgx,turso}` — `helpers.go`, `pagination.go`,
null-mapping, tx plumbing). Report: what is genuinely driver-generic →
promote into `integrations/datastores/pgxdb` / `datastores/turso`; what is
feature-specific → stays. Output is a short findings doc + a follow-on task
list appended to this plan; promotion itself executes only after the audit is
reviewed (it touches six modules and the conformance suites — storetest stays
green per feature throughout).

**D1 EXECUTED 2026-07-07 (data-integration-reviewer). Findings summary:**

- **Clean (a)-class promotions:** `ExportMigrations` (byte-identical ×6, pure
  io/fs — do first, zero risk); the turso timestamp/bool bundle (`tsLayout`,
  `formatTS`, `parseTime`, `boolToInt` — identical where extracted, but cms
  turso open-codes `.UTC().Format(tsLayout)` at ~10 sites); keyset
  `ListPage[T]` (auth has the generic form; cms `listWhere` and jobs'
  two `List`s open-code it — ~8 sites, the largest structural dedup; pulls
  `sdk/crud` into the connectors, a conscious FS5 scope expansion to ratify
  at D5).
- **(c)-class divergences (the reason the audit precedes promotion —
  per-feature storetest proves auth-pgx ≡ auth-turso, never auth ≡ cms):**
  (1) `nullableTS` — same name, two contracts: auth turso takes
  `time.Time`, zero → NULL; cms turso takes `*time.Time`, nil → NULL. Both
  are legitimate domain absent-models; promotion must ship BOTH
  (`NullTime`/`NullTimePtr`), never collapse to one. (2) The same
  value-vs-pointer split on the pgx side (auth `nullableTime`/
  `fromNullableTime` round-trip pair vs cms `publishedAt` with no read-back
  twin). (3) rows-affected → ErrNotFound implemented three structural ways
  (inline, `affectedOne`, `execAffecting`); promote only the
  `(int64, error)` normalization, keep zero→ErrNotFound adapter-side (port
  semantic). No dropped-error bug found (all 15 turso RowsAffected sites
  checked).
- **Stays feature-side (b):** `MigrationsFS` embeds (compile-local),
  `newID`/`payloadValue` (jobs domain choices), `orderField` (per-port
  pagination key, coincidental value). jobs turso's `retryBusy` is
  driver-generic but single-consumer — promote when a second write-heavy
  turso store appears; note auth/cms turso have NO busy-retry (latent
  parity gap, behavior not helpers, out of D1 scope).
- Minor: `crud.ErrNotFound` vs `errs.ErrNotFound` is the same sentinel
  (crud aliases errs) — spelling inconsistency only; `pgxdb` exports
  `Querier`, turso has no equivalent — mirror it if D5 lands.

### Follow-on tasks (cut from the D1 audit; execute after review, each gated
on per-feature storetest green)

### task-D2: promote `ExportMigrations` + `Scanner` to the connectors
Add `ExportMigrations(fs.FS, dir, dst)` and `Scanner` to both connectors;
replace the 6 byte-identical bodies and 6 `scanner` interfaces with
delegates/aliases; add connector-level ExportMigrations tests (export
embedded FS → temp dir, assert file set + bytes, dst auto-create). Blast: 6
store modules + 2 connectors. Risk: none.

### task-D3: promote the turso timestamp/bool bundle to `tursodb`
`TimeLayout`, `FormatTime`, `ParseTime`, `ParseNullTime`, `BoolToInt`, plus
the reconciled pair `NullTime(time.Time)` / `NullTimePtr(*time.Time)`
(divergence 1: auth adopts NullTime, cms adopts NullTimePtr — do NOT
collapse). Converge cms turso's ~10 inline format sites. Connector test:
fixed-width round-trip identity, RFC3339 tolerance, both null encodings both
directions — the test that would have caught divergence 1. Blast: 3 turso
stores + connector.

### task-D4: reconcile the pgx nullable-time helpers
`pgxdb.NullTime`/`FromNullTime` + `NullTimePtr`; migrate auth pgx and cms
pgx (including cms's open-coded read side), preserving each feature's
absent-model. Blast: 2 pgx stores + connector.

### task-D5: promote keyset `ListPage[T]` to both connectors
Dialect-specific (pgxdb binds `time.Time`; tursodb binds `FormatTime(cv)`).
Rewire auth (drop `pagination.go`), cms `listWhere` (call + hydrate), jobs'
two `List`s. RATIFY at execution: connectors gain a `sdk/crud` import (no
new external dep). Mirror `Querier` into turso for symmetry. Gate: storetest
pagination cases (confirm they cover empty page, exact-limit, over-fetch
trim, cursor round-trip at the created_at tie-break before moving). Blast: 6
stores + 2 connectors.

### task-D6 (optional): normalize rows-affected
Connector `ExecAffecting(ctx, q, args...) (int64, error)`; collapse the
three shapes onto it; zero→ErrNotFound stays adapter-side; jobs turso keeps
`retryBusy` wrapping. Cosmetic once D3/D4 land — lowest priority.

### Phase E — jobs: Register consumes the built Service (FS2)

### task-E1: retire the rebuild in jobs.Register

`jobs.Register` (`features/jobs/jobs.go:287-288`) calls `NewService(repos,
cfg)` internally and discards the result — the exact seam FS2 retires.
Amend to the same shape chosen in A2 (method vs package func — one decision,
applied to both features identically). Update `examples/jobs-minimal` wiring
and any doc quoting the old signature. This is jobs's only change in the
plan; it is FS2 conformance, not guard conformance — jobs passes all W3
guards untouched.

Verify: `go build ./... && go vet ./...`; `go test ./features/jobs/...`.
Real-interaction check: run `examples/jobs-minimal`, enqueue a job, watch it
execute.

## Risks

- **Parallel-session collision (auth-v2).** Mitigation: sequencing rule 1;
  Phase A rebases over auth-v2's final state; A3 ownership is decided in one
  place.
- **A1 freezes a wide public API.** Pre-tag this is cheap to adjust; post-tag
  it is compatibility surface. Mitigation: sequencing rule 2, and A1's
  by-subsystem grouping keeps the promotion reviewable.
- **B1 silently changes rendered content for existing entries.** Plain text
  where markdown rendered before. Accepted by FS10 ("cross that bridge at cms
  specifics"); the real-interaction check pins the new behavior including the
  escaping property.
- **Guard ordering (W3 of 00-charter vs B1/B2).** The FS1 guard fails on cms
  until B2 lands. Either carve out cms with a dated TODO or land guard+B1
  together and extend the carve-out only for templ. Executor notes the choice
  in both execution logs.

## Acceptance

- `make check` green including both W3 guards, cms carve-out gone after B2.
- auth: public Service exposes the six use-case groups; `Register` consumes a
  built Service; zero `json.NewEncoder` under `features/auth/internal/inbound`.
- cms core go.mod: sdk-only after B2 (goldmark/bluemonday already gone after
  B1), `tool` directive included.
- jobs: exactly one change (task-E1's Register shape); all W3 guards pass on
  jobs with zero change.
- All four examples build, run, and pass their real-interaction checks
  (auth-cms, cms, minimal, jobs-minimal).

## Execution log

### 2026-07-07 — Phases A, E, B1, C, D1 executed (parallel implementer
agents, same session as ratification)

- **Phase A (auth)** — A1: 34 use-cases promoted by thin delegation
  (session, passwords, oauth, machine, token, the seven invitation methods
  with public aliases `OAuthResult`/`CreateInput`/`CreateResult`/
  `AcceptInput`/`AcceptResult`, plus the cookie/transport seam); new
  `ErrInvitationsDisabled` (wraps errs.ErrNotFound → 404, mirroring the
  transport's deny-by-absence). A2: `Register` is a method; auth README
  wiring `main.go` re-verified compiling in a scratch module;
  examples/auth-cms rewired. A3: responders converted (5 files —
  http/oauth/machine/invitation + the two authsvc middleware writers);
  applied AFTER the byte-compare gate fired and the wire delta was
  explicitly approved. **Wire delta of record:** Content-Type gains
  `; charset=utf-8` on every auth JSON response; success bodies
  (json.Marshal) lose json.Encoder's trailing `\n`; error bodies keep it;
  status codes and JSON fields byte-identical. Leg-0 protocol re-run
  post-A3: identical 401→201→403→200→200→200→200→401. FS2 bypass proven
  live (host handler calling svc.Login, host-authored JSON, then
  reverted). **Divergence of record:** the Register method no longer
  defaults Config.Logger from Mount.Logger — build-once captures the
  logger at NewService (nil → slog.Default()); both examples pass Logger
  explicitly; documented on the method.
- **Phase E (jobs)** — `Register` method form; validation seam moved to
  NewService with tests relocated (not dropped); jobs-minimal rewired;
  real-interaction: enqueue → handler executed → clean drain, exit 0.
- **Phase B1 (cms)** — `RenderMarkdown` → `RenderPlainText` (escape +
  blank-line paragraphs + `<br>` for single newlines); templ regenerated;
  goldmark/bluemonday + douceur/gorilla-css requires gone (templ stays for
  B2; goldmark lingers only as an x/tools transitive of templ's tooling).
  Real-interaction: seeded `<script>alert(1)</script>` page renders the
  tag as escaped text, paragraphs as separate `<p>` blocks (actual
  response bytes captured); temporary seed reverted.
- **Phase C (sdk)** — `web.Template` adapter (+tests); `feature.Route` +
  `RegisterRoutes` + `feature.Group` (reuses joinPrefix; fresh-slice
  middleware combine to avoid aliasing, pinned by test). sdk go.mod still
  has zero requires. Scratch host proved both live (stdlib template
  rendered + escaped; group prefix + middleware applied; bare path 404).
  FS7 hook deliberately not shipped.
- **Phase D1 (audit)** — findings + cuttable D2–D6 appended above.
- **Verification** — per-module build/vet/test across all 26 modules PASS;
  templ generation idempotent (shasum-stable across two runs); all six
  guards PASS (G6 shown failing on the three pre-A3 sites first —
  prove-can-fail). `make check`'s drift stanza requires a committed tree
  (git diff vs HEAD), so the full one-command check goes green at commit.
- **Remaining in this plan:** B2 (views/templ extraction — own sub-plan),
  B3 (cms public Service, demand-driven), D2–D6 (store promotions, after
  review of the D1 findings).
