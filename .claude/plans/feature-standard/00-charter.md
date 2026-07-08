# feature-standard — the extension model, ratified and written down

Status: **RATIFIED 2026-07-07 (jrazmi)** — FS1–FS10 at Claude's
recommendations ("take your recommendations, ratify them"); NOTES.md entry
same date. Open calls resolved at ratification: Register = **method form**
(`svc.Register(mount)`; auth's user-registration use-case is named
`RegisterUser`); FS1 guard lands **with a cms carve-out scoped to templ
only** (dated TODO, removed at convergence B2).
Origin: structure review session 2026-07-07 (auth as the worked example; all
findings verified against source that day; steward-reviewed, 14 findings
folded).

## Goal

Ratify one extension model for every feature module — how a host configures,
replaces, injects into, and extends past a feature — and write it into the
charter (`features/README.md`) and `ARCHITECTURE.md`, with machine guards where
the rule is structural. The companion plan (`01-convergence.md`) brings the
three existing features up to the standard; this plan is the decisions and the
documents only.

Design goals, in priority order (stated 2026-07-07): correctness, ease of use,
extensibility, ease of mental model. The stack is a **starter stack**: features
ship with our defaults; they never force them.

## Context an executor needs (verified 2026-07-07)

The repo already contains every data point the standard generalizes from:

- **jobs is the template for the driving surface.** `features/jobs/jobs.go:152-287`:
  public `Service` with real use-cases (`Enqueue`, `EnqueueJob`,
  `EnsureSchedule`), and the secondary artifact consumes a built service
  (`NewRuntime(svc)`).
- **auth is the template for deny-by-absence and dependency hygiene.**
  `features/auth/go.mod` requires exactly one thing: sdk. Subsystems switch
  off by absence (no `Config.Providers` → no OAuth routes; no `Granter` → no
  invitation routes; no `TokenSigner` → no JWT parsing). But its use-cases are
  sealed in `internal/logic/authsvc` — the public `Service` exposes only the
  identity slice — and `Register` rebuilds the service a host may already have
  built (`examples/auth-cms/cmd/server/main.go:121-132` — `NewService` at
  :126, `Register` at :130 — the documented "built twice" seam). jobs's
  `Register` has the same rebuild (`jobs.go:287-288` calls `NewService` and
  discards it), so FS2 amends jobs too — see `01-convergence.md` Phase E.
- **cms is the template for component replacement — and the dependency
  outlier.** `cms.Config` (`features/cms/cms.go:60`) demonstrates
  interface-valued fields with bundled defaults (`Views theme.PublicViews`,
  nil → `theme.Default()`), registries (`Types`, `Templates`), and middleware
  injection (`AdminMiddleware`). But `features/cms/go.mod` requires templ,
  bluemonday, and goldmark — an API-only cms host pulls all three (plus the
  transitive tail) into its module graph. `theme.PublicViews` predates the sdk
  port: treat it as the reference implementation of the *shape*, not canon.
- **The view seam is already tech-neutral by design.** `sdk/web/render.go:15`
  defines `Renderer` with stdlib types only; templ satisfies it implicitly and
  an `html/template` wrapper satisfies it in three lines. templ is our
  default, never the contract.
- **The sdk responder family exists and auth doesn't use it.**
  `sdk/web/response.go:34-63` (`RespondJSON`, `RespondJSONOK/Created/Accepted`,
  `RespondJSONError`, `RespondJSONDomainError`);
  `features/auth/internal/inbound/http/http.go:322-335` hand-rolls `writeJSON`/
  `writeError` duplicating them, and two more hand-rolled response writers sit
  inside the hexagon itself (`internal/logic/authsvc/service.go:757,766` —
  middleware writing HTTP from `internal/logic`). Those three are the only
  `json.NewEncoder`/`http.Error(` hits in any feature core today (verified by
  steward review 2026-07-07). cms uses `web.Respond*`/`web.Render` correctly.
- **Hosts already have route groups.** `web.RouteGroup.Handle`
  (`sdk/web/groups.go:34`) has the identical signature to
  `feature.RouteRegistrar.Handle`, so a host can pass a group (prefix + shared
  middleware) as `Mount.Router` today. `feature.PrefixRegistrar` proves the
  composition pattern for registrar wrappers.

## YOUR CALLS — PROPOSED, awaiting ratification

- **FS1 — feature core modules import sdk only.** Structural, like sdk's empty
  go.mod, and machine-checked (W3). Anything carrying a third-party dependency
  ships as a sibling module of the feature. Auth and jobs pass today; cms
  converges in `01-convergence.md`. **Supersedes D7**
  (`restructure/00-overview.md:70`, RATIFIED 2026-07-02 "accept for now" —
  cms core carries view deps), invoking D7's own revisit clause: the headless
  host has materialized. `features/README.md` checklist item 2's "+ any
  view-rendering deps … per D7's cms precedent" parenthetical is deleted by
  W1. Guard mechanics: the dev-only relative `replace` of sdk is permitted
  pre-tag; a `tool` directive counts as a require (this is what correctly
  keeps cms red until convergence B2).
- **FS2 — the public `Service` is the feature's driving surface.** Use-cases
  live on the feature's public `Service` (thin delegation to the sealed
  interior, exactly as `auth.Service.RequireUser` does today). Not "port" — a
  concrete struct is not a port; a host or consuming feature declares its own
  narrow port and the Service satisfies it structurally, exactly the ratified
  C2 pattern and `jobs.Enqueue`'s hard-contract comment (`jobs.go:207-210`).
  The shipped transport is an **optional convenience adapter** over that
  surface: a host may mount it, mount part of it (subsystem deny-by-absence),
  or skip it and write its own handlers. Any artifact that consumes the built
  feature — a transport mount, a runtime — takes the BUILT `Service`, never
  `(repos, cfg)` a second time (`jobs.NewRuntime(svc)`, `jobs.go:244`, is the
  in-repo precedent); the "built twice" seam is retired. `jobs.Register`'s
  rebuild-and-discard shape (`jobs.go:287-288`) is amended in
  `01-convergence.md` Phase E — "jobs requires zero change" cannot stand
  alongside an unscoped FS2, so it doesn't: jobs takes exactly that one
  amendment. **Supersedes the §1 Register contract** (`features/README.md` §1
  "exactly two things … a single `Register(mount, repos, cfg) error`", quoted
  in ARCHITECTURE.md:158-161); W1 rewrites §1's "definition and the dial"
  accordingly.
- **FS3 — presentation is a port; defaults are sibling modules.** A feature
  with HTML surface defines a `Views` interface in its core (domain-typed
  params, `web.Renderer` returns). Bundled default implementations ship as
  `views/<pkg>` sibling modules (named for the package they're built on, per
  R-KV2 — `views/templ`), mirroring `stores/<pkg>`. **Nil Views → the HTML
  surface is not registered** — uniform across features, cms included; the
  examples show the one-line default wiring. An API-only host does nothing and
  carries zero view tech in its graph. **Amends R6's four-kind taxonomy**
  (`roadmap/00-intersections.md` §1) with the `views/<pkg>` row, and rewrites
  the ratified degraded-mode matrix row "cms `Config.Views` nil → bundled
  site chrome" (§2, line ~77) to "nil → HTML surface not registered". A
  `views/<pkg>` module requires its view tech directly — no `integrations/`
  connector exists or is needed, because `sdk/web.Renderer` is already the
  structural seam. It is not an integration: it carries feature-specific
  template content (the templ isolation is incidental) and it imports the
  feature core, which an integration never may.
- **FS4 — sibling modules are per-concern, never mandatory.** A feature has a
  `views/` only if it has views, `stores/` only if it persists. jobs (no HTTP,
  no views) is a fully conforming feature.
- **FS5 — store adapters stay feature-owned; connectors absorb shared
  helpers.** The `stores/<pkg>`-under-integrations alternative was examined
  and rejected 2026-07-07. Primary reason: a store adapter must import its
  feature's core, so housing it under `integrations/` puts feature-aware code
  below the features layer — inverting the ratified dependency direction
  (`sdk/README.md:10-11`: adapters depend downward, never the reverse; an
  integration isolates one external dependency and knows nothing above it).
  Corollaries: adapters change on the feature's release train
  (versioning/tagging locality — RELEASING.md's directory-prefixed tags),
  an adapter isolates no new dependency (fails the integration litmus), and
  third-party feature authors cannot add code to our integrations tree —
  feature-owned placement is the only one that generalizes outside the
  monorepo. The streamlining instinct is honored the
  other way: **rich connector, thin adapter** — promote driver-generic
  machinery (null mapping, cursor pagination, tx helpers) from feature stores
  into the `pgxdb`/`turso` connector modules (audit in `01-convergence.md`
  Phase D).
- **FS6 — Config structs remain the carrier; no functional options.** The
  `With*` idiom would be a second configuration style delivering nothing
  `Config{Field: v}` doesn't. Nil semantics stay documented per field;
  deny-by-absence stays expressed by absent fields.
- **FS7 — route tables become data; the public hook waits for demand.**
  Features build their route tables as `[]feature.Route` (Name, Method, Path,
  Handler, Middleware — e.g. `"auth.login"`) internally. The
  `Config.Routes func([]feature.Route) []feature.Route` override hook is
  designed but NOT shipped until a real host hits the gap single-route
  override fills; FS2's bypass tier plus subsystem deny-by-absence cover the
  known cases. `feature.RouteRegistrar` stays one method; group ergonomics come
  from composition (a `feature.Group` wrapper), never from widening the
  contract.
- **FS8 — behavior hooks defer to the events feature.** Notify-style extension
  (on-register, on-login) rides the events rail when `events-v1` lands. Sync
  hooks in Config earn a place only with veto/mutate semantics, argued
  case-by-case.
- **FS9 — feature transports use sdk/web.** Responders (`web.Respond*`),
  rendering (`web.Render`), errors (`web.Err*`, `ErrFromDomain`). A
  feature-local write helper is a red flag in review and a guard failure (W3).
  When the sdk is missing a capability a feature needs, the fix lands in the
  sdk **if it passes sdk/README.md's admission policy** (plurality,
  narrow+stable, shared policy); otherwise the feature keeps one named local
  helper with a comment citing the failed admission test — never a silent
  fork of an existing sdk responder.
- **FS10 — cms content is plain text for now.** goldmark + bluemonday leave the
  cms core (`01-convergence.md` B1); markdown-vs-plain returns as its own
  decision when cms specifics come up. Any future rich-text pipeline enters
  through a port with the FS1 placement rules.

## Work items

### W1 — features/README.md: the extension-model section

Add a charter section (after the anatomy table) naming the four extension
tiers, with the current best in-repo example of each:

1. **Configure** — nil-safe Config fields with safe defaults; deny-by-absence
   subsystems (auth).
2. **Replace a component** — interface-valued Config fields with bundled
   defaults (Views), registries (cms `Types`, jobs `Handlers`).
3. **Inject at the seams** — middleware fields (cms `AdminMiddleware` taking
   auth's `RequireUser`, zero imports either way).
4. **Extend past the feature** — the public `Service` driving surface (FS2);
   shipped transport optional.

Plus: the FS1 dependency rule and sibling-module placement table
(`stores/<pkg>`, `views/<pkg>`), FS3 nil semantics, FS9 sdk-usage rule, and an
authoring-checklist update (core go.mod is sdk-only; transport uses sdk/web;
route table as data; Views port if HTML).

Two named rewrites the enumeration must not miss (steward finding 2):

- §1 "The definition, and the dial" — the "exactly two things … a single
  `Register(mount, repos, cfg) error`" sentence is rewritten per FS2
  (build once, mount the built Service).
- The anatomy table's `theme/` row — replaced per FS3 (`views/<pkg>` sibling;
  `theme.PublicViews` reframed as the reference implementation that proved
  the shape).
- Checklist item 2's "+ any view-rendering deps … per D7" parenthetical —
  deleted per FS1's D7 supersession.

### W2 — ARCHITECTURE.md sync

- Feature anatomy paragraph gains the FS1 sentence and `views/<pkg>` alongside
  `stores/<pkg>` in the layout tree and the module taxonomy table (swap unit:
  a module import + one Config field).
- The features section states FS2 (Service is the driving surface; Register
  consumes a built Service) once the convergence plan lands it.

### W3 — Makefile guards

- **sdk-only cores:** fail if any `features/*/go.mod` outside `stores/`,
  `views/`, `storetest/`(if module) requires anything beyond
  `github.com/gopernicus/gopernicus/sdk`. (cms fails until convergence B1+B2 —
  wire the guard with cms carved out + a TODO, or land the guard in the same
  change that fixes cms; executor's choice, note it in the log.)
- **sdk/web usage:** fail on `json.NewEncoder(` / `http.Error(` inside
  `features/*/internal/**` — the whole sealed interior, not just inbound:
  two of today's three violations live in `internal/logic`
  (`authsvc/service.go:757,766`), not the transport. DTO *decoding*
  (`json.NewDecoder`) stays legal — the guard targets response writing only.
  Prove-can-fail set at introduction: `http.go:334`,
  `authsvc/service.go:757`, `:766` (all removed by convergence A3). Waiver
  convention for future legitimate hits (e.g. encoding into a buffer or an
  SSE stream — events-v1's gateway will live under `features/events/
  internal/`): a named per-line exception in the guard citing FS9, never a
  regex weakening (the repo-hardening secrets-sweep convention).
- **core never imports its own `views/<pkg>`:** extend Makefile G2
  (`Makefile:88-90`) to mirror stores exactly — forbidden regex
  `features/[a-z0-9]+/(stores|views)`, with `--exclude-dir=stores
  --exclude-dir=views` so the sibling modules' own intra-module imports
  don't false-positive.

### W4 — fresh-eyes cross-check

Re-read the amended charter + ARCHITECTURE.md against `features/jobs`
(conforms after `01-convergence.md`'s one Register amendment, Phase E; passes
both W3 guards with zero change) and against the events plans — **both**
`roadmap/events-feature-design.md` and the operational
`.claude/plans/events-v1/plan.md`, whose line ~743 uses the old
`Register(mount, Repositories{}, …)` shape and needs the FS2 fold-in before
execution starts. The next feature must be born conforming.

## Acceptance

- FS1–FS10 carry RATIFIED (or amended) markers with date + initials.
- `features/README.md` + `ARCHITECTURE.md` updated; no *live* doc contradicts
  them — grep for `theme.PublicViews` scoped to ARCHITECTURE.md,
  features/README.md, sdk/README.md, and feature READMEs (historical plan
  files and execution logs stay as written, per repo convention).
- Superseded decisions carry markers at their source: D7
  (`restructure/00-overview.md`), the R6 matrix rows
  (`roadmap/00-intersections.md`), and §1's Register sentence
  (`features/README.md`) each gain a "superseded by feature-standard FS_"
  line.
- `make check` includes all three W3 guards; guard failure messages name the
  rule (FS1/FS9/FS3) so a future author learns the standard from the failure.
- jobs passes all guards with zero change (its Phase E amendment is FS2
  conformance, not guard conformance).

## Out of scope

- All code convergence (auth Service promotion, cms dep-pull/views extraction,
  sdk adapter) — `01-convergence.md`.
- The route-override hook implementation (FS7 holds it).
- Any markdown/rich-text decision for cms (FS10 defers it).

## Execution log

### 2026-07-07 — ratified + W1–W4 executed (same session)

- **Ratified** (jrazmi, "take your recommendations, ratify them"): FS1–FS10
  at recommendations; Register = method form (`svc.Register(mount)`, auth's
  use-case named `RegisterUser`); FS1 guard with cms templ-only carve-out.
  NOTES.md entry same date. Cross-gates placed: repo-hardening task-9
  depends-on + task-10 sync note; events-v1 task-11 FS2 sync note.
- **W1** — `features/README.md`: §1 rewritten to the FS2 trio; anatomy
  table (`<name>.go` socket, sdk-web-only handlers, `theme/` row →
  `views/<pkg>` sibling row); four-tier extension model written out
  (with FS6/FS7/FS8 dispositions inline); FS1 + FS9 rules added to §3;
  checklist items 2 + 4 rewritten; §4's PrefixRegistrar example updated
  with the honest cms-until-B3 note.
- **W2** — ARCHITECTURE.md: taxonomy now five kinds (views-module row,
  amendment note on the intro), feature-anatomy paragraph carries
  FS1/FS2/FS3, tree comment updated. Top-level README.md synced (sdk-only
  core bullet, FS2 wiring bullet with cms exception, "six layering
  guards"). Supersession markers placed at D7
  (`restructure/00-overview.md`) and the R6 taxonomy + degraded-mode rows
  (`roadmap/00-intersections.md`).
- **W3** — Makefile: G2 extended to `(stores|views)` with
  `--exclude-dir=views`; new G5 `guard-feature-core-sdk-only` (direct
  requires + tool directives; cms templ carve-out with dated B2 TODO) and
  G6 `guard-feature-transport-sdk-web`
  (`json.NewEncoder(`/`http.Error(` in `features/*/internal/**`,
  non-test). **Prove-can-fail captured live:** pre-A3, G6 failed on
  exactly the three steward-verified sites (http.go:334,
  authsvc/service.go:757,766); G5's parser shown detecting cms's templ
  require + tool directive (suppressed only by the ratified carve-out).
  Post-convergence: all six guards pass.
- **W4** — fresh-eyes: jobs conforms after its one Phase E amendment and
  passes all guards untouched ✓; `events-v1/plan.md` line ~743 carries the
  FS2 fold-in sync note ✓; live-docs grep for `theme.PublicViews` finds
  only the sanctioned reference-implementation framing
  (features/README.md anatomy row) ✓.
