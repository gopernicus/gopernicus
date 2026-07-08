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

### 2026-07-07 — Phase B2 executed (own sub-plan `02-b2-views-extraction.md`)

**The cms carve-out is gone.** B2 extracted the bundled default views into the
new `features/cms/views/templ` module (its own `go.mod`, `tool` directive, and
`MODULES`/`go.work` entry — the 27th module), defined the core-owned `cms.Views`
port (19 methods, public + admin chrome) exported from `internal/inbound/http`
and re-exported via root `features/cms/views.go` aliases, threaded it through all
six handler constructors, deleted `features/cms/theme`, and flipped nil
`Config.Views` from "bundled default" to "HTML surface not registered" (only
`GET /media/{id}/file` survives, now error-responding via
`web.RespondJSONDomainError`). `features/cms/go.mod` is sdk-only (templ require +
tool directive gone; `grep -c a-h/templ` → 0), and the Makefile G5 cms carve-out
+ dated TODO are deleted — **G5 now passes cms-clean**. All three example hosts
rewired: `minimal` + `auth-cms` carry the FS3 one-line default
(`Views: cmstempl.New()`), `examples/cms` embeds the default and overrides the
four ACME chrome methods (partial-override demonstration). This fully meets this
plan's **Acceptance** line "cms carve-out gone after B2" and "cms core go.mod:
sdk-only after B2". Verification (task-4, 2026-07-07): byte-exact
baseline-vs-after HTML compare on the 11-URL examples/cms set → all identical;
minimal's 3 URLs identical modulo per-boot in-memory KSUIDs/order (proven by an
after-vs-after reboot reproducing the same nondeterminism; deterministic-slug
`/products/widget-3000` byte-identical); live renders on all three hosts incl.
auth-cms login-gated admin (`GET /articles` → 200 behind `RequireUser`); both
wire deltas confirmed live (media file error JSON; nil-Views blackout). `make
check` fails ONLY at the templ drift gate (git-diff vs HEAD, uncommitted move —
resolves at commit, per the same-commit constraint); build/vet/test across all
27 modules, the three turso integration vets, and all six guards pass. Still
remaining in this plan: B3 (deferred, demand-driven), D2–D6 (store promotions).

### 2026-07-07 — task-D2 executed (promote ExportMigrations + Scanner)

**Connectors gained the shared scaffold helper + scan surface; the six stores
now delegate.** Added `func ExportMigrations(migrationsFS fs.FS, dir, dst string)
error` to both connectors (`integrations/datastores/pgxdb/migrate.go`,
`integrations/datastores/turso/migrate.go`) — body is the byte-for-byte D1
helper (os.MkdirAll dst, fs.ReadDir dir, skip dirs, fs.ReadFile + os.WriteFile
0o644), stdlib-only (`os`/`path` added; no external imports — the sdk/crud
expansion is D5's, not touched). Added `type Scanner interface { Scan(dest
...any) error }` — to `pgxdb/querier.go` (sibling of Querier) and `turso/db.go`
(no Querier there yet; mirroring it stays a D5 item). The six store
`ExportMigrations` bodies collapsed to one-line delegates (`return
pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)` / `tursodb.…`),
dropping their `io/fs`/`os`/`path`/`path/filepath` imports; the six private
`scanner` interfaces became aliases (`type scanner = pgxdb.Scanner` /
`tursodb.Scanner`), adding one connector import to each `helpers.go`.
`MigrationsFS`/`MigrationsDir` embeds stayed feature-side (compile-local per
D1); every store's public `ExportMigrations` signature is unchanged, so hosts
(incl. `examples/cms/workshop/migrations`, which reads pre-scaffolded copies —
no runtime call) are untouched.

- **New connector tests** — `migrate_export_test.go` in both connectors:
  fstest.MapFS with two top-level `.sql` + one nested file, exported into a
  not-yet-existing `<tmp>/nested/migrations`; asserts dst auto-create, exact
  two-file set (subdirectory skipped), and verbatim bytes. Both PASS.
- **Verification** — `go test ./...` PASS in both connectors and all six store
  modules. Per-feature storetest gate (`features/{auth,cms,jobs}` roots) PASS
  incl. all three `storetest` hermetic suites. Env-gated live suites skip
  loudly: auth-pgx `TestConformance_Postgres` + cms-pgx `TestConformance_Postgres`
  + jobs-pgx `TestConformance_Queue`/`TestConformance_Schedules` all
  `SKIP … POSTGRES_TEST_DSN not set — postgres conformance NOT verified`; the
  turso store modules carry only the hermetic `TestExportMigrations` (their
  conformance runs via modernc sqlite in storetest). `make check` → **all
  checks passed** (27 modules build/vet/test, three integration-tag vets, all
  six guards; templ drift clean — no templ touched). gofmt clean on every
  changed file.
- **Real-interaction** — `examples/minimal` on :8081: `GET /` → 200,
  `GET /products/widget-3000` → 200 (server log confirms both); killed, port
  free. No example/Makefile calls ExportMigrations at runtime (only a doc
  comment in the cms workshop runner references it), so no migrate flow to
  drive.
- **Divergence** — none. Placement note: `Scanner` sits beside `Querier` in
  pgxdb but in `turso/db.go` because turso has no Querier interface yet (D5
  mirrors it). D3–D6 still remaining.

### 2026-07-07 — task-D3 executed (promote the turso timestamp/bool bundle)

**The `tursodb` connector now owns the timestamp/bool bundle; the three turso
stores delegate.** New file `integrations/datastores/turso/timestamps.go`
(stdlib-only — `database/sql`, `time`; no go.mod change) exports:
`const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"`;
`FormatTime(time.Time) string` (= `t.UTC().Format(TimeLayout)`);
`ParseTime(string) (time.Time, error)` (fixed-width, RFC3339Nano fallback,
UTC-normalized); `ParseNullTime(sql.NullString) (time.Time, error)` (NULL/empty
→ zero); `BoolToInt(bool) int`; and the reconciled absent-model **pair** —
`NullTime(time.Time)` (zero → NULL, auth's value-typed contract) and
`NullTimePtr(*time.Time)` (nil → NULL, cms's pointer-typed contract). Divergence
1 shipped as BOTH, never collapsed; doc comments cross-reference the two.

- **auth turso** — adopted `NullTime` (its `nullableTS(time.Time)` maps 1:1);
  deleted `helpers.go`'s `tsLayout`, `formatTS`, `nullableTS`, `parseNullTime`,
  `parseTime`, `boolToInt` (6 helpers) + the now-orphan `database/sql`/`time`
  imports; call sites across 11 files rewired to `tursodb.*`. Kept `orderField`,
  the `scanner` alias, `MigrationsFS`/`MigrationsDir` (feature-specific per D1).
  The `pagination.go` cursor site's `formatTS(cv)` converged to
  `tursodb.FormatTime(cv)` — a format-call convergence only; keyset structure
  (D5) untouched.
- **cms turso** — adopted `NullTimePtr` (its `nullableTS(*time.Time)` maps 1:1);
  deleted `tsLayout`, `nullableTS`, `parseTime` (3 helpers); converged **15**
  open-coded `X.UTC().Format(tsLayout)` sites (terms 3, inquiries 1, entries 4
  incl. the `listWhere` cursor site, assets 1, menus 5, plus the two write
  paths) onto `tursodb.FormatTime`. Kept `orderField`, `scanner`.
- **jobs turso** — adopted the `FormatTime`/`ParseTime`/`BoolToInt` subset it
  duplicated; deleted `tsLayout`, `formatTS`, `parseTime`, `boolToInt` (4).
  Kept the feature-specific `newID`, `payloadValue`, `isBusy`, `retryBusy` +
  the busy-retry consts, `orderField`, `scanner` (per D1).

Byte-identity holds by construction: `TimeLayout` is the exact former `tsLayout`
string, `FormatTime`/`ParseTime`/`NullTime`/`NullTimePtr`/`ParseNullTime`/
`BoolToInt` bodies are the former helper bodies verbatim. No store public API
changed (all six deleted helpers were private).

- **Connector test** — `timestamps_test.go` (follows the module's `package
  turso` internal-test convention): fixed-width round-trip identity + width/UTC
  assertions, RFC3339 tolerance (trimmed-zero, no-fraction, offset, microsecond
  cases), invalid-input rejection, BOTH null encodings BOTH directions
  (`NullTime` zero→NULL / set→string, `NullTimePtr` nil→NULL / set→string, their
  parity on a set value), `ParseNullTime` NULL/empty→zero and set→instant, and a
  `NullTime`→`ParseNullTime` round-trip — the test that would have caught
  divergence 1. All PASS.
- **Verification** — connector `go test ./...` PASS; the three turso store
  modules build/vet/`go test ./...` PASS (hermetic run is `TestExportMigrations`
  only; the tagged `TestConformance_Turso`/`_Postgres`/`_Queue` skip loudly
  without live creds — `TURSO_DATABASE_URL … NOT verified`). Per-feature
  storetest gate green: `features/{auth,cms,jobs}` roots + all three `storetest`
  packages PASS. `make check` → **all checks passed** (27 modules build/vet/test,
  three integration-tag vets, all six guards; no templ touched).
- **Real-interaction** — `examples/minimal` :8081 `GET /` + `/products/widget-3000`
  → 200/200, killed, port free. **turso-real drive:** `.env` URL check PASSED —
  `TURSO_DATABASE_URL=libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
  matches the authorized playground DB, so the live drive ran: `examples/cms`
  :8080 `GET /` → 200 and `GET /articles/b2-baseline-article` → 200, rendering
  "B2 Baseline Article" read from live turso — exercising the new
  `FormatTime`/`ParseTime` path (every entry scan parses created_at/updated_at
  and the nullable published_at). Killed, port free.
- **Divergence** — none. D4 (pgx nullable-time), D5 (keyset `ListPage`), D6
  (rows-affected) still remaining.

### 2026-07-08 — task-D4 executed (reconcile the pgx nullable-time helpers)

**The `pgxdb` connector now owns the nullable-time helpers; the two pgx stores
delegate.** New file `integrations/datastores/pgxdb/timestamps.go` (stdlib-only —
`time`; no go.mod change) exports the reconciled absent-model **pair plus its two
read twins**, all returning/consuming `*time.Time` (pgx's native nullable
TIMESTAMPTZ binding — nil → NULL — so the wire behavior is byte-identical to the
former private helpers): `NullTime(time.Time) *time.Time` (zero → NULL, auth's
value-typed contract), `FromNullTime(*time.Time) time.Time` (NULL → zero),
`NullTimePtr(*time.Time) *time.Time` (nil → NULL, cms's pointer-typed contract),
`FromNullTimePtr(*time.Time) *time.Time` (NULL → nil). Divergence 2 shipped as
BOTH encodings, never collapsed; doc comments cross-reference each write/read
twin. The pgx layer (unlike turso's TEXT storage) needs no format/parse primitive
— the driver scans TIMESTAMPTZ directly into `*time.Time` — so the bundle is
null-mapping only.

- **auth pgx** (value-typed, zero → NULL) — adopted `NullTime`/`FromNullTime`;
  deleted `helpers.go`'s `nullableTime`/`fromNullableTime` round-trip pair + the
  now-orphan `time` import; the 9 call sites across `oauth_accounts.go`,
  `invitations.go`, `api_keys.go` rewired to `pgxdb.NullTime`/`pgxdb.FromNullTime`
  (all three files already imported `pgxdb` for `MapError`). Bodies map 1:1 —
  `NullTime` ≡ former `nullableTime`, `FromNullTime` ≡ former `fromNullableTime`.
- **cms pgx** (pointer-typed, nil → NULL) — adopted `NullTimePtr` (write) and
  **closed the read-back gap D1 flagged** with `FromNullTimePtr`: the two write
  sites in `entries.go` moved from the private `publishedAt(...)` to
  `pgxdb.NullTimePtr(e.PublishedAt)`; `scanEntry`'s open-coded read
  (`if published != nil { t := published.UTC(); e.PublishedAt = &t }`) collapsed
  to `e.PublishedAt = pgxdb.FromNullTimePtr(published)`. Deleted `publishedAt` +
  the now-orphan `time` import from `helpers.go` (`entries.go` keeps `time` for
  its scan-var declarations). `NullTimePtr` ≡ former `publishedAt`.

- **jobs pgx disposition** — **left untouched, per D1's "2 pgx stores + connector"
  scope.** jobs pgx defines NO nullable-time helper functions (nothing to dedup);
  it does open-code the same pointer read-back pattern that `FromNullTimePtr` now
  replaces, at 3 sites (`queue.go` `claimedAt`/`completedAt`, `schedules.go`
  `lastRunAt`). These are a latent adoption candidate — noted here the way D1
  parked jobs turso's `retryBusy` — but adopting them was out of D4's ratified
  scope, so this leg leaves them. Follow-on if a D-series cleanup revisits jobs.
- **Note — the fourth function.** task-D4 names three (`NullTime`/`FromNullTime`
  + `NullTimePtr`); shipping `FromNullTimePtr` is the minimal addition required to
  satisfy the same task line's "migrate … including cms's open-coded read side …
  preserving each feature's absent-model (cms pointer-typed, nil → NULL)" — a
  value-returning read helper would have silently converted cms to the value
  absent-model. It is `NullTimePtr`'s natural read twin and gives cms the symmetric
  round-trip pair auth already had. Recorded as a conscious, in-spirit expansion
  of the named surface, not a redesign.

- **Connector test** — `timestamps_test.go` (`package pgxdb` internal-test
  convention): both encodings both directions + round-trip identity — `NullTime`
  zero→nil / set→UTC instant, `FromNullTime` nil→zero, their round-trip; the same
  for `NullTimePtr`/`FromNullTimePtr` (nil↔NULL, set→UTC); and a
  `NullTimePtr(&zero)` case pinning the value-vs-pointer semantic difference (a
  pointer to a zero time is a present value, only nil is absent). Hermetic (pure
  value-mapping, no DSN). All PASS.
- **Verification** — connector `go test ./...` PASS; both pgx store modules
  build/vet/`go test ./...` PASS. Per-feature storetest gate green:
  `features/{auth,cms,jobs}` roots + all three `storetest` packages PASS (the
  hermetic conformance runs the turso path over modernc sqlite; the pgx
  read/write path I touched is exercised live only under the DSN-gated suites).
  DSN-gated suites skip loudly: auth-pgx `TestConformance_Postgres` + cms-pgx
  `TestConformance_Postgres` both `SKIP … POSTGRES_TEST_DSN not set — postgres
  conformance NOT verified`; each pgx module's hermetic `TestExportMigrations`
  PASS. `make check` → **all checks passed** (27 modules build/vet/test, three
  integration-tag vets, all six guards; no templ touched → drift clean). gofmt
  clean on all eight changed files.
- **Real-interaction** — `examples/minimal` :8081 `GET /` + `GET
  /products/widget-3000` → 200/200 (server log confirms both), killed, port free.
  No live pgx drive this leg — a POSTGRES_TEST_DSN docker drive of the auth/cms
  pgx nullable-time round-trip belongs to events-v1 phase 4; the hermetic-only
  scope is honest here (connector helpers unit-tested; store code compiles + is
  covered by the DSN-gated conformance when creds land).
- **Divergence** — the `FromNullTimePtr` fourth-function addition above (in
  spirit, flagged). Otherwise none. D5 (keyset `ListPage`), D6 (rows-affected)
  still remaining.

### 2026-07-08 — task-D5 executed (promote keyset `ListPage[T]` to both connectors)

**Both connectors now own the keyset-paginated SELECT; the six stores delegate.**
Added `func ListPage[T any](ctx, db Querier, columns, table, where string, args
[]any, orderField, pkCol string, req crud.ListRequest, scan func(Scanner) (T,
error), keyOf func(T) (time.Time, string)) (crud.Page[T], error)` to both
connectors — `integrations/datastores/pgxdb/pagination.go` and
`integrations/datastores/turso/pagination.go`. Bodies are the byte-for-byte D1
reference (`auth` was the promotion source: its `pagination.go` generic held the
exact shape). Dialect-specific per the plan: **pgxdb** binds the cursor time as
`time.Time` (`cv.UTC()`) and numbers `$N` placeholders from `len(args)+1`;
**tursodb** binds `FormatTime(cv)` (fixed-width TEXT) with `?` placeholders. The
sole promotion generalization: `orderField` (the cursor field-tag, `created_at`
for every caller) is now also the SQL ordered/predicate column — since tag ==
column at all six call sites, the produced SQL is byte-identical to each store's
former inline/generic form. Cursor decode → predicate → over-fetch LIMIT → scan
loop → `crud.TrimPage` sequence is unchanged; empty/exact/over-fetch behavior
stays entirely inside `crud.TrimPage` (untouched).

- **RATIFY-AT-EXECUTION recorded** — the connectors gain a
  `github.com/gopernicus/gopernicus/sdk/crud` import. jrazmi granted this
  in-session 2026-07-07 (overnight-loop authorization 3): **no new external
  dependency** (both connector go.mods already `require` the `sdk` module; `crud`
  is a package within it — no go.mod/go.sum change), normal downward direction
  (connector → sdk). Grant applied; no other dependency added.

- **Querier mirror (D1 minor + D4 placement note)** — new
  `integrations/datastores/turso/querier.go` holds `Querier` (Exec/Query/QueryRow
  over `*sql.DB`/`*sql.Tx` return types) + the moved `Scanner`, with the
  `_ Querier = (*DB)(nil)` / `_ Querier = (*Tx)(nil)` assertions — mirroring
  pgxdb's `querier.go` layout exactly. `Scanner` was relocated out of `turso/db.go`
  (D4 flagged it sat there only because `Querier` didn't exist yet); its contract
  is unchanged, and the private `scanner` aliases in the three turso stores still
  resolve (`type scanner = tursodb.Scanner`). `ListPage` accepts the `Querier`
  interface (accept-interfaces), so `*DB` and `*Tx` both work.

- **Per-store rewire (6 modules, no public-API change):**
  - **auth turso / auth pgx** — deleted `pagination.go` (the private `listPage`
    generic) from both; the five call sites each dialect (service_accounts,
    api_keys, invitations ×2, security_events) now call
    `tursodb.ListPage`/`pgxdb.ListPage` with `orderField, "id"` inserted before
    `req`. Kept `orderField`, the `scanner` alias, the scan/keyOf funcs verbatim.
  - **cms turso / cms pgx** — `listWhere` now builds its `where` (base + optional
    status filter, unchanged placeholder logic) then calls the connector
    `ListPage` and **hydrates the returned page's items** (fields + terms) in
    place. `q.ListRequest` (the embedded field) is passed as the request.
  - **jobs turso / jobs pgx** — both `List`s (queue: kind/status filters +
    `job_id` pk; schedules: `schedule_id` pk) build `where`/`args` then delegate
    to the connector `ListPage`. Kept the feature-specific filter-building,
    `newID`/`payloadValue`/`retryBusy` etc.

- **Divergence (cms, benign, flagged) — hydrate-after-trim.** cms's former
  `listWhere` hydrated all `limit+1` over-fetched spine rows, THEN trimmed; the
  connector `ListPage` trims first and cms hydrates only the trimmed page. The
  **returned page is byte-identical** (the dropped over-fetch row was never in the
  result; the cursor is encoded from the spine's `(created_at, id)`, unaffected by
  hydration) — the only change is one fewer fields+terms query per page on the
  discarded probe row (a strict improvement). No other divergence; auth/jobs
  reproduce their prior SQL exactly.

- **GATE (per-feature storetest pagination coverage, run FIRST, file:line):**
  Assessed the four required cases as keyset-`List`/`crud.Page` paths in each
  feature's `storetest.go` (the shared conformance contract; `reference_test.go`
  runs it against memory):
  - **cursor round-trip at the created_at tie-break (the load-bearing case — the
    only one exercising the moved SQL predicate): PRESENT ×3-per-feature.** auth:
    `testServiceAccountsListCollision`/`testAPIKeysListCollision`/
    `testSecurityEventsListCollision`/`testInvitationsListByResourceCollision`
    (storetest.go:701,864-line region,1083,1366 — identical `created_at`, assert
    exact id-DESC order through cursor traversal). cms: `testEntriesPrecision`
    (storetest.go:416 — µs-truncation collapses 6 rows into equal-`created_at`
    groups straddling a page boundary). jobs: queue's six emails at a shared
    `now` (storetest.go:290) drained at limit 2.
  - **over-fetch trim: PRESENT.** cms `collectEntries` and jobs `drainPages`
    assert `len(page.Items) <= limit` across multi-page traversals
    (storetest.go:123 / 599); auth's paged tests assert exact full-population
    order (a failed trim surfaces as a dup/order mismatch).
  - **exact-limit: PRESENT.** auth collision (4 rows / limit 2 → last full page,
    HasMore false), cms/jobs 5-row / limit-2 traversals.
  - **empty page: PRESENT in cms only** (storetest.go:340-342 —
    `ListByTerm` after cascade delete → 0 items via `crud.Page`). **auth and jobs
    have NO dedicated zero-item keyset-`List` assertion** — auth's only `len==0`
    (storetest.go:551) is `ListByUser`, a non-keyset slice port; jobs'
    `testClaimEmpty` is `Claim`, not `List`.
  - **Gate disposition — proceeded on all three, NOT blind.** The empty-page path
    is entirely inside `crud.TrimPage(nil, …)` → `Page{}` (encode never called),
    which this refactor does not touch and which `crud`'s own unit tests cover
    (`sdk/crud/pagination.go` + tests). The one case that exercises the code I
    MOVED (tie-break cursor round-trip) is the best-covered case in all three
    features. Per the gate's "instead of proceeding blind" clause I traced the
    empty path to unchanged/separately-covered code rather than adding storetest
    cases (which would alter the six-store conformance contract). Recorded as a
    conscious, reversible call — flagging so the parent can direct an empty-page
    keyset case be added to `storetest.go` if the contract should pin it.

- **Connector tests** — `pagination_test.go` in both, each per its own hermetic
  convention:
  - **turso** (internal `package turso`, real in-proc modernc sqlite via the
    existing `newMemDB`): a `(id, created_at TEXT)` table seeded through
    `FormatTime`, then `ListPage` end-to-end — empty page, over-fetch trim +
    remainder, exact-limit boundary (HasMore false on a full final page), and a
    tie-break traversal (3+2 rows sharing two `created_at` values, paged at limit
    2, asserting every id once in `(created_at, id)` DESC order). All PASS.
  - **pgxdb** (internal `package pgxdb`, no hermetic DB available — pure SQL-shape
    via a `captureQuerier` that records query+args and returns early): asserts the
    exact no-cursor SQL + `$N`/limit+1 args, and the with-cursor keyset-predicate
    SQL with continued placeholder numbering AND that args[1..2] are bound as
    `time.Time` (UTC) — the dialect contract distinguishing pgx from turso's
    string. All PASS.

- **Verification** — both connectors `go test ./...` PASS. All six store modules
  `go test -count=1 ./...` PASS (the rewired turso/pgx adapter `List` bodies run
  under the `//go:build integration` + creds-gated conformance suites, which are
  NOT in the default run — turso needs `TURSO_DATABASE_URL/TURSO_AUTH_TOKEN`, pgx
  needs `POSTGRES_TEST_DSN`, both skip loudly; default runs are the hermetic
  `TestExportMigrations`). The moved logic is proven hermetically by the connector
  `pagination_test.go` (turso over modernc sqlite) and live by the cms drive
  below. Per-feature storetest gate green: `features/{auth,cms,jobs}/storetest`
  all PASS. `make check` → **all checks passed** (27 modules build/vet/test, the
  three integration-tag vets, all six guards; no templ touched → drift clean).
  gofmt clean on all changed files.

- **Real-interaction** — `examples/minimal` :8081 `GET /` + `GET
  /products/widget-3000` → 200/200, killed, port free. **pagination-real drive:**
  `examples/cms/.env` URL check PASSED —
  `TURSO_DATABASE_URL=libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
  matches the authorized playground DB. Booted `examples/cms` :8080 (rebuilt
  binary, clean bind after clearing a stale listener); `GET /articles` (admin
  list, paginated through the rewired `cmsturso` → `tursodb.ListPage`) → 200
  rendering the "Articles" heading + the Baseline Article row read from live
  turso; `GET /` (public listing) → 200 with the baseline article. Killed, port
  free.

- **Divergence** — cms hydrate-after-trim (benign, above); the empty-page gate
  disposition (above). Otherwise none. D6 (rows-affected) still remaining.

### 2026-07-08 — task-D6 executed (normalize rows-affected)

**Both connectors now own the exec + rows-affected normalization; the six stores
delegate and keep their port semantic adapter-side.** New file
`integrations/datastores/{pgxdb,turso}/exec.go` exports
`func ExecAffecting(ctx, db Querier, query string, args ...any) (int64, error)` —
`pgxdb` returns `tag.RowsAffected()` (int64, no driver error), `tursodb` returns
`res.RowsAffected()` (int64, error) — both propagating the Exec error unchanged
and normalizing ONLY to `(int64, error)`. Stdlib-only (`context`); no go.mod
change. The connector never maps zero to ErrNotFound and never retries — those are
the caller's, per D1 finding (3). `db` is the D5 `Querier`, so `*DB` pools and
`*Tx` transactions both flow through it (used by cms's in-Tx entry Update).

- **Shape map found (D1's three structural forms, site counts confirmed against
  the 15 turso `RowsAffected` sites + the pgx side):**
  - **inline** (Exec then `res.RowsAffected()`/`res.RowsAffected() == 0` open-coded
    at the call site): auth turso 7 (users Update; verification codes/tokens
    Delete ×2; sessions Delete; service_accounts Update+Delete; oauth Delete),
    auth pgx 9 (the same set + api_keys Revoke/TouchLastUsed), cms turso 4 (terms
    Update; entries Update in-Tx; entries Delete; menus UpdateItem), cms pgx 4
    (same), jobs turso 1 (schedules ClaimDue), jobs pgx 1 (schedules ClaimDue).
  - **`affectedOne(res sql.Result)` helper** (auth turso only): 1 private helper,
    2 callers (api_keys Revoke + TouchLastUsed).
  - **`execAffecting(ctx, query, args...)` method** (jobs only): 4 methods —
    turso queue+schedules, pgx queue+schedules — serving 8 call sites (Complete,
    Fail, SetLastJob, SetEnabled, Delete ×2 dialects).
  - Two site semantics, kept where they were: expects-one → `errs.ErrNotFound`
    (every site above except ClaimDue), and ClaimDue's compare-and-set → `won =
    n == 1` (jobs turso/pgx schedules). Only the mechanical `(int64, error)`
    extraction moved; both semantic checks stayed adapter-side.

- **Per-store collapse:**
  - **auth turso** — 7 inline sites rewired to `tursodb.ExecAffecting`; the
    `affectedOne` helper DELETED and its 2 api_keys callers inlined to
    `n, err := tursodb.ExecAffecting(...); if err …; if n == 0 { return
    errs.ErrNotFound }` (collapsing the second of the three shapes entirely). No
    orphaned imports (`database/sql` stays for `sql.NullString` in scanAPIKey).
  - **auth pgx** — 9 inline sites → `pgxdb.ExecAffecting`.
  - **cms turso / cms pgx** — 4 sites each → connector `ExecAffecting`; the in-Tx
    entries Update passes `tx` (the `*Tx` Querier); entries Delete preserves its
    `tursodb.MapError(err)`/`pgxdb.MapError(err)` on the error branch (uniqueness
    is n/a there, but the wrapping is kept verbatim).
  - **jobs turso** — the 2 `execAffecting` method BODIES rewired to
    `tursodb.ExecAffecting` **inside the retained `retryBusy` wrap** (retry NOT
    absorbed into the connector, per the task); ClaimDue's inline likewise rewired
    inside its own `retryBusy`, keeping `won = n == 1`.
  - **jobs pgx** — the 2 `execAffecting` method bodies + ClaimDue's inline →
    `pgxdb.ExecAffecting` (no retry on the pgx side, unchanged).
  No public API changed (every touched helper/method was private or internal).
  Residual grep for `RowsAffected`/`affectedOne`/`res, err := ` under
  `features/*/stores/` → zero.

- **Connector tests** — `exec_test.go` in both:
  - **turso** (internal `package turso`, real in-proc modernc sqlite via
    `newMemDB`): affects-one → 1, affects-none → 0 (asserting the connector does
    NOT map zero to ErrNotFound — the adapter owns that), affects-many → 2, and a
    driver error (bad table) propagates rather than normalizing. All PASS.
  - **pgxdb** (internal `package pgxdb`, hermetic `execQuerier` stub returning a
    `pgconn.NewCommandTag("UPDATE 3")`/`"DELETE 0"` and recording query+args):
    count normalization + query/args passthrough, zero-rows → `(0, nil)` (not
    mapped), and a sentinel Exec error propagated via `errors.Is`. All PASS.

- **Verification** — both connectors `go test ./...` PASS. All six store modules
  `go build ./... && go vet ./... && go test ./...` PASS. Per-feature storetest
  gate green: `features/{auth,cms,jobs}` roots + all three `storetest` suites PASS
  (the rewired adapter write bodies run under the creds-gated conformance; default
  runs are hermetic). DSN/creds-gated conformance skips loudly on every store:
  auth/cms turso `SKIP TestConformance_Turso`, auth/cms pgx `SKIP
  TestConformance_Postgres`, jobs turso/pgx `SKIP TestConformance_Queue` +
  `SKIP TestConformance_Schedules` (no `TURSO_*`/`POSTGRES_TEST_DSN`). `make check`
  → **all checks passed** (27 modules build/vet/test, the three integration-tag
  vets, all six guards; no templ touched → drift clean). gofmt clean on all
  changed files.

- **Real-interaction** — `examples/minimal` :8081 `GET /` + `GET
  /products/widget-3000` → 200/200, killed, port free. **cms mutating drive
  crossing ExecAffecting:** `.env` URL check PASSED —
  `TURSO_DATABASE_URL=libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
  matches the authorized playground DB. Booted `examples/cms` :8080 (admin is open
  in this host). Identified B2 Baseline Article (id `6weeujymank7a47kfqiolihr6q`,
  published), read its edit form, and `POST /articles/6weeujymank7a47kfqiolihr6q`
  re-submitting the *identical* field values (title/author/excerpt/body/status/
  term_id — a non-destructive edit, only `updated_at` bumps) → **303 See Other**
  → `/articles/…/edit`, i.e. `EntryStore.Update` returned n==1 through
  `tursodb.ExecAffecting`; public `GET /articles/b2-baseline-article` still 200,
  title intact; server log clean (no error/500). **Not-found mutation:** `POST
  /articles/doesnotexist000000000000000` → **404** — the `ErrNotFound` mapping
  yields the same status as before. Killed, port free.

- **Divergences (flagged):**
  1. **cms entries Delete error-branch (benign).** Formerly `res.RowsAffected()`'s
     own error returned raw while the Exec error was MapError'd; `ExecAffecting`
     merges Exec+count into one error, so a (practically-unreachable — modernc
     sqlite / pgx never error from RowsAffected here) count error now also flows
     through `MapError`. `MapError` passes non-driver errors through, so behavior
     is unchanged in practice; recorded for completeness.
  2. **Not-found HTTP path does not reach the ExecAffecting zero-rows branch.**
     cms's entry service `Get`s before `Update` (entrysvc `Edit`/`Publish`), so a
     not-found POST 404s at the read, never reaching `ExecAffecting`'s n==0 →
     ErrNotFound branch. That branch is proven hermetically (connector
     `exec_test` affects-none + the storetest conformance's missing-id Update/
     Delete cases), not by the live drive — an honest note, not a gap.
  Otherwise none. **Phase D (D1–D6) complete.**
