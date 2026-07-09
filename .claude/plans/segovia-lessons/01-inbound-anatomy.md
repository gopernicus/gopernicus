# Phase 01 — inbound anatomy (flag #1): ratify the app-local inbound anatomy; features mirror the file axis

Status: **RATIFIED 2026-07-08 (jrazmi, in-conversation) — EXECUTED 2026-07-08 (tasks 1–5; see Execution log)**
Milestone: `segovia-lessons` (see `00-overview.md` — the flag ledger, the
provenance discipline, the inherited law)
Executor model: **fable** for the doc tasks (1, 5 — design/doc judgment),
**opus** for the re-slice tasks (2, 3, 4)
Depends on: — (first phase). Decision **D1: RATIFIED (d) 2026-07-08** —
tasks 2–5 unblocked; the phase's own ratification and a clean tree are
the remaining execution gates. Task-1 was always D1-independent.
Size: M

## The flag (input of record — Segovia's flags doc, ratified there 2026-07-08, adopted verbatim)

> **App-local inbound anatomy is underspecified.** ARCHITECTURE.md defines
> `internal/inbound/` as "driving adapters (HTTP, CLI, cron)" but no
> per-domain convention. Propose ratifying the full anatomy Segovia adopted
> (2026-07-08; placement amended same day):
> `internal/inbound/domains/<domain>/` with **routes.go** (route table) /
> **api.go** (JSON) / **html.go** (HTML pages; fragments.go when htmx lands)
> / **views.go** (the render **port** — methods return `web.Renderer`, FS3
> doctrine scaled to app-local: templ is the default, never the contract) /
> **templates/** (the bundled default implementation, **co-located with its
> domain** — matching how features bundle theirs, e.g. cms `views/templ`;
> implements the port structurally, never imports the transport);
> `internal/inbound/http/` for transport plumbing (middleware);
> `internal/inbound/views/` as the **global** presentation tree — shared
> `Shell`/layouts and the future UI kit (the theme root), consumed by every
> domain's templates. **This is the UI-kit/theming seam**: a themed kit is a
> new implementation of the ports + one cmd wiring change. **Partial
> override via embedding**: because the default is a concrete exported
> struct, a host (or a single binary — `cmd/<binary>/views/`) embeds it and
> overrides individual port methods; method promotion supplies the rest.
> Override granularity = port method (page), deliberately — reuse comes from
> exported building blocks (Shell, kit primitives), never exported page
> internals. **Growth rule (multi-resource domains, ruled 2026-07-08):** the
> file axis flips from transport to RESOURCE — `routes.go` stays singular
> (one domain = one readable route table, never split); transport-named
> files (`api.go`/`html.go`) are the single-resource degenerate form; at
> resource #2, per-resource files (`grants.go` holding that resource's
> api+html; `grants_api.go`/`grants_html.go`/`grants_fragments.go` only when
> one grows heavy). NEVER `/api` `/html` `/htmx` subdirectories (v1
> bridge-think — scatters one resource across trees). A subdirectory means a
> new CONTRACT (own schema/vocabulary, e.g. `adreport/`) or a swappable
> implementation behind a port (`templates/`), never mere file count; a
> domain wanting its own package tree is two domains. Same rule mirrors in
> `logic/domains/<domain>/` and `templates/{resource}.templ`. Should shape
> the future `gopernicus new domain` scaffold (workshop-v2 brief).

Segovia v2's live tree is the proof of shape (the reference implementation —
no host in THIS repo has app-local domains, so the app side lands as
documentation only):

```
v2/internal/inbound/domains/dashboards/{routes.go, api.go, html.go, views.go,
    templates/{views.templ, views.go, adreport.templ, adreport_model.go, adreport_support.go, ...}}
v2/internal/inbound/http/middleware/
v2/internal/inbound/views/{layout.templ, ...}
```

## Context

ARCHITECTURE.md's app pattern (§"The app pattern (hexagonal)", table row at
~line 224) currently gives `internal/inbound/` one line — "driving adapters:
HTTP (admin + public), CLI, cron, queue consumers" — and nothing below the
package level. Segovia v2 lived in that gap and ratified an anatomy;
this phase carries it upstream (owner direction 2026-07-08): ratify the app
side into ARCHITECTURE.md, define the feature-side **partial** mirror in
`features/README.md`, and re-slice the three feature inbound packages
(`features/{authentication,cms,events}/internal/inbound/http/`) to the
ratified file anatomy. The re-slice is renames/moves only — zero behavior
change, zero route change, zero exported-symbol change.

## Goal

The inbound anatomy is ratified and documented (app side full, feature side
partial-with-stated-reasons), D1 is decided, and all three feature inbound
packages conform to the ratified file anatomy with rename-only diffs.

**The adoption path, stated (PM fold):** until the workshop-v2 scaffold
emits this shape, a host developer adopts it by hand — read the
ARCHITECTURE.md subsection and apply it, with Segovia v2 as the living
reference. Task-1 is the phase's primary user-facing deliverable and is
D1-independent: a D1 stall never blocks the actual product value.

## Definition of Done

- ARCHITECTURE.md's app-pattern section carries the full inbound-anatomy
  subsection: per-domain package anatomy, the views-port doctrine
  (`web.Renderer` returns; templ default, never contract), the global views
  tree + theming seam (**app-only** — consult fold, landmine 2),
  override-via-embedding, the growth rule (file axis flips to RESOURCE at
  #2; never `/api` `/html` `/htmx` subdirs; subdirectory = new contract or
  port-backed swappable impl), with Segovia flag #1 provenance cited.
- `features/README.md` §2 documents the feature-side mirror: file anatomy
  adopted (routes.go route table; per-resource files at resource #2;
  transport-named files as the single-resource degenerate form), the
  FS1/FS3 reasons the mirror is partial (templates never co-locate; the
  seam is the `views/<pkg>` sibling module + the exported port), and the
  feature exception to "never split" (a `Mount` dispatcher in routes.go +
  per-resource `mountX` helpers in their resource files — the
  authentication deny-by-absence shape).
- D1 ratified and recorded; the three feature inbound packages renamed to
  `internal/inbound/<feature>/` (task-2) and conforming: cms `router.go`
  → `routes.go`; authentication's route table, the `handlers` struct, and
  transport plumbing extracted to `routes.go` and its session/account
  handlers to `sessions.go`; events' file set confirmed conforming under
  the maximal-flatten clause (its only diff is the task-2 rename).
- Every re-slice diff is rename/move-only (`git diff --stat` shows moves;
  no body changes beyond package docs and import lines), all touched
  modules green per-module, `make check` green at 30 modules, and the
  run-and-look legs pass (examples/cms pages render; the auth-cms
  register→login flow returns the same codes as before).
- NOTES.md carries a dated entry: flag #1 adopted, D1 outcome, the
  deliberate feature-side deltas.

## Decision D1 — RATIFIED (d), 2026-07-08 (jrazmi): the feature inbound package is `internal/inbound/<feature>/`

The feature interior is single-domain by definition (a feature wanting two
domains is two features — owner's own note; the escape hatch of a real
`domains/` split inside a feature is acknowledged and expected never to
fire). So Segovia's `internal/inbound/domains/<domain>/` axis has no `<x>`
to name in a feature. Four options, honestly ((d) added 2026-07-08 from
owner direction, superseding the post-cut (b) recommendation):

- **(a) `internal/inbound/domain/` (singular)** — the owner's initial
  lean; **withdrawn by owner 2026-07-08** ("don't necessarily need
  inbound/domain").
  *For:* maximal word-level mirror of the app anatomy; the package name
  says "the one domain's inbound surface." *Against:* (i) the public rim
  was renamed `features/*/domain/` THIS WEEK (commit 88239a5) — one module
  would spend the same word on two different things (`features/cms/domain/`
  = entities+ports rim; `features/cms/internal/inbound/domain/` = HTTP
  handlers), a standing grep/doc/onboarding hazard; (ii) the mirror is
  imperfect anyway — Segovia's form is **plural `domains/<x>/`**, so
  singular `domain/` with no child is already a degenerate adaptation, not
  a true mirror (consult fold, landmine 4); (iii) collateral churn: the
  `internalhttp` import aliases in four root files
  (`features/cms/cms.go:16`, `features/cms/views.go:3`,
  `features/authentication/authentication.go:35`,
  `features/events/events.go:34`) plus in-comment path references
  (`features/authentication/internal/logic/authsvc/context.go:29`) all
  rename; and `package domain` importing `features/cms/domain/content`
  reads wrong; (iv) (a) is a **charter amendment, not a rename** —
  `internal/inbound/http/` is codified in the ratified trio-layout rows
  (`features/README.md` §2 lines 52/66, ARCHITECTURE.md line 26), so (a)
  amends ratified charter text in two documents on top of the churn
  (steward fold). Note there is **no import-path collision** — the rim
  `domain/` is a directory of packages, not itself imported — the cost is
  vocabulary, charter churn, and file churn, not compilation.
- **(b) keep `internal/inbound/http/`; adopt the FILE anatomy inside it**
  (the post-cut review recommendation — **declined by owner
  2026-07-08**). *For:* the growth rule's own logic seemed to close this — a
  transport-named form is the single-X degenerate form, and degenerate
  forms flatten; a feature is definitionally single-domain AND (today)
  single-transport, so both axes flatten and the package name falls back
  to transport. `features/events/internal/inbound/http/routes.go` already
  demonstrates the conforming degenerate shape with zero changes. No
  vocabulary collision, no churn, and the day a feature grows a second
  transport it adds a sibling `internal/inbound/<transport>/` — still
  consistent. *Against:* the package name diverges from Segovia's
  `domains/<x>` at the word level; and Segovia reserves app-side
  `inbound/http/` for plumbing-only middleware, while a feature's `http/`
  holds handlers — a documented, bounded meaning-shift across the
  app/feature line (the feature has no separate plumbing tree to conflict
  with). *Why declined:* the owner's objection exposes what the reviews
  underweighted — domain-shaped content (handlers, the render port, DTOs,
  view models) organized under a transport name is the disorganization
  being fixed, and Segovia names its handler packages for the DOMAIN
  (`domains/dashboards`), never the transport; a handler-stuffed feature
  `http/` contradicts the plumbing-only doctrine ratified one table row
  up in the same document. The "flatten to transport name" argument
  assumed a naming convention Segovia's own tree doesn't use.
- **(c) another name** (`internal/inbound/` flat as `package inbound`;
  `web/`) — surveyed and rejected: flat `inbound` loses the transport
  slot for no gain; `web/` collides with `sdk/web` vocabulary. (The
  feature's-own-name form, dismissed here at cut time as stutter,
  graduated to option (d) on owner direction.)
- **(d) `internal/inbound/<feature>/` — `inbound/cms`,
  `inbound/authentication`, `inbound/events` — RECOMMENDED
  (owner-proposed 2026-07-08).** *For:* this is the FAITHFUL mirror —
  Segovia's handler package is named for its domain
  (`inbound/domains/dashboards/`), so a feature's form is that same
  domain-named package flattened out of `domains/` (a feature is its one
  domain); `http/` keeps a single meaning on both sides of the
  app/feature line (transport plumbing, if a feature ever grows any); and
  the import path reads role-then-domain (`internal/inbound/cms`) exactly
  like the app form. *Against, honestly:* path stutter
  (`features/cms/internal/inbound/cms`) and the package name duplicates
  the feature's root package, making root-socket aliases mandatory — but
  they are already the standing practice (`internalhttp` today; rename
  the alias to `inbound`, which reads as `inbound.Mount(...)`); churn is
  identical to (a) — three `git mv`s, package clauses, four root-socket
  alias lines, one doc comment — plus the same charter-amendment edits
  ((a)'s Against (iv): `features/README.md` §2 rows, ARCHITECTURE.md
  line 26); and the Go-idiom nit that a package named `cms` describes
  the domain, not the contents — answered by Segovia's own `dashboards`
  precedent and by the `inbound/` path segment carrying the role.

**RATIFIED: (d)** — owner, 2026-07-08, same day as the direction. The
ruling, in the owner's words: mirror `inbound/domains/dashboards` as
close as possible — `routes.go` (registering through the feature's
`feature.RouteRegistrar` contract, which `web.WebHandler` satisfies),
`views.go`, `{entity}.go` per resource; "the http directory is wrong,
that's meant for higher-level middlewares, generics" — i.e. `http/` is
plumbing-only on both sides of the app/feature line, and a feature has
no `http/` at all until real plumbing appears. The post-cut (b)
recommendation is superseded (rationale preserved under (b) — it was
wrong about Segovia's naming axis, not about the flatten). Tasks 2–5
are unblocked; the remaining execution gates are the phase's own
ratification and the clean-tree precondition.

## The feature-side mirror is necessarily partial (FS1/FS3 — locked, not relitigated)

- A feature core's `go.mod` requires exactly `sdk` (FS1), and templ is a
  third-party dependency — so **templates can NEVER co-locate in feature
  inbound**. The render port stays in the feature core (cms: the `Views`
  interface in `internal/inbound/http/views.go`, aliased at
  `features/cms/views.go:11`) and the bundled default stays the
  `views/<pkg>` sibling module (`features/cms/views/templ`, FS3/FS4).
- Therefore the **feature theming seam is the sibling module + exported
  port** (embed `templ.Views{}`, override methods — `examples/cms/internal/
  theme/theme.go` is the live host-side override), **never** an
  `internal/inbound/views/` tree. The app-side global-views-tree doctrine
  is app-only and the docs must say so (consult fold, landmine 2; there is
  no `features/cms/theme/` — do not cite one).
- What features DO adopt is the **file anatomy**: `routes.go` as the
  readable route table (with the feature exception: per-resource
  deny-by-absence `mountX` helpers live in their resource file — landmine
  1), per-resource files at resource #2 (cms's
  `entries.go`/`media.go`/`menus.go`/`terms.go`/`contact.go`/`public.go`
  already conform), transport-named `api.go`/`html.go` only as the
  single-resource degenerate form, and never `/api` `/html` `/htmx`
  subdirectories.
- **The maximal flatten (landmine 5 — lead-backend fold; a gopernicus-side
  clarification of the verbatim flag, owner ratifies it with this phase):**
  a single-resource, single-transport surface with only a small handler set
  may keep its handlers in `routes.go` itself — the degenerate ladder runs
  one rung past `api.go`. `features/events/internal/inbound/events/routes.go`
  (post-task-2 path; 172 lines: `Config`, `gateway`, `Mount`, and the SSE
  handlers) is the blessed in-repo example. The "route table never split" rule constrains
  splitting the *table*, not the co-residence of a few handlers; at
  resource #2 (or real heft) the per-resource files appear as above.
  Without this clause events is non-conforming the day task-1 lands;
  with it, events' only phase diff is the task-2 rename (its file set is
  untouched). This clause extends the flag text —
  a candidate to flow back to Segovia's doc (owner's channel).

## Current state survey (2026-07-08 — real files, verified)

- **`features/events/internal/inbound/http/`** — `routes.go` +
  `routes_test.go` only. **Already conforming under the maximal-flatten
  clause** (handlers co-resident in `routes.go` — the rung past `api.go`;
  see the mirror section). File-set zero diff — only the task-2 rename
  touches it; recorded in task-5.
- **`features/cms/internal/inbound/http/`** — already resource-sliced:
  `entries.go`, `media.go`, `menus.go`, `terms.go`, `contact.go`,
  `public.go` (public chrome), `models.go` (port view-model vocabulary,
  aliased at `features/cms/views.go`), `views.go` (the Views port —
  load-bearing across the module boundary via the root alias),
  `router.go` (`Mount` at :61, `BuildRouter` test-convenience at :150,
  `RouterOption`/`routerConfig`) + tests. **Diff: `router.go` →
  `routes.go`**, nothing else moves.
- **`features/authentication/internal/inbound/http/`** — `http.go`
  (358 lines: package doc, `authService` port, request/response DTOs,
  `Mount` at :143, session/account handlers — register/login/token/verify/
  forgotPassword/resetPassword/changePassword/logout — `decode`,
  `clientInfoRegistrar`/`clientInfoMiddleware`/`clientIP`), plus
  already-per-resource `invitation.go` (`mountInvitations` :90),
  `machine.go` (`mountMachine` :117), `oauth.go` (`mountOAuth` :43), and
  tests (`http_test.go` 466 lines, `token_test.go` with no `token.go` —
  the token handler lives in http.go). **Diff: split http.go** →
  `routes.go` (Mount + the clientInfo plumbing) + `sessions.go` (port,
  DTOs, handlers, decode). The `mountX` helpers STAY in their resource
  files (landmine 1). `internal/redirect/` is a shared interior package
  consumed by `authsvc`/`invitationsvc`/the root socket — **not an inbound
  package, out of this re-slice**.

## Out of scope

- Editing Segovia's flags doc or code (owner flips flag #1 there).
- The `gopernicus new domain` scaffold (workshop-v2 brief — the anatomy
  should shape it; recorded, not planned).
- Flags #2 and #3 (queued in `00-overview.md`).
- Any behavior, route, exported-symbol, or `Config`/`Mount` change; any
  FS-rule change; any change to `features/cms/views/templ`,
  `examples/*`, or store modules.
- Relocating the cms Views port or view models out of the inbound package
  (consult guardrail — the root alias depends on them; over-tidying here
  is a regression, not a cleanup).

## Schema / datastore impact

None. No SQL, no migrations, no store adapters touched.

## Module / API impact

None public. All moves are inside `internal/` (import paths private to each
feature module); the root sockets' `internalhttp` aliases absorb the
D1(d) rename (alias → `inbound`) with import-line-only edits. No go.mod/go.work changes; no tagging
implications (zero tags exist). Guard posture verified at cut: no Makefile
guard greps an `internal/inbound/http` path (G6 scans `features/*/internal/`
path-agnostically; G2/G5/G7 are import/go.mod-shaped) — no guard edits
under any D1 outcome.

## Generated-artifact impact

None. No `.templ` sources touched; `features/cms/views/templ` is out of
scope. `make check`'s templ-drift gate runs regardless — the re-slice tasks
verify it stays a no-op (consult fold: confirm the sibling module isn't
accidentally in a `make generate` diff).

## Risks

1. **The auth test re-home duplicates or drops fixtures** — `http_test.go`
   hosts the shared fixtures (`do`, `newTestHandler`, the `mem*` stores,
   `fakeHasher`, `nopMailer`) the four sibling test files consume; all
   five are `package http`, so fixture location can never break
   compilation — the real hazards are duplicate declarations and silent
   omissions (landmine 3, reframed by the lead-backend review).
   Mitigation: task-4's fixture-survey substep + its abort line.
2. **Doc doctrine overreach onto features** — writing "never split" or the
   global-views-tree seam without the feature carve-outs makes
   authentication (mountX helpers) and every feature (no
   `inbound/views/`) non-conforming on day one (landmines 1, 2).
   Mitigation: the carve-outs are named in tasks 1 and 5, not left to the
   implementer.
3. **Dirty working tree** — several inbound files carry mid-flight
   modifications at cut time. Mitigation: preconditions require a clean
   tree; renames on dirty files corrupt blame and diffs.

## Preconditions

- D1 ratified — **satisfied 2026-07-08**: (d) `internal/inbound/<feature>/`
  (this file's §D1).
- Clean working tree at execution (`git status` — the cut-time tree was
  dirty; do not execute over it).
- `make check` green on the current tree (30 modules, all guards).
- Read: ARCHITECTURE.md §"The app pattern (hexagonal)" (~lines 200–266),
  `features/README.md` §2, and the three inbound packages in full.

## Tasks

### task-1: ARCHITECTURE.md — the app-local inbound-anatomy subsection

- **depends_on:** []
- **model:** fable
- **files:** [ARCHITECTURE.md]
- **verify:** `make guard` (no Go code touched); proofread the subsection
  against the verbatim flag quote above — every rule present, none
  paraphrased into drift
- **description:** Add an "Inbound anatomy (per-domain)" subsection to
  §"The app pattern (hexagonal)", directly after the dir table (~line 224's
  row stays as the summary line). Content, from the flag verbatim in
  intent: `internal/inbound/domains/<domain>/` with `routes.go` (the ONE
  readable route table — never split within a domain) / `api.go` (JSON) /
  `html.go` (HTML pages; `fragments.go` when htmx lands) / `views.go` (the
  render port — methods return `web.Renderer`; FS3 scaled to app-local:
  templ is the default, never the contract) / `templates/` (the bundled
  default, co-located with its domain, implements the port structurally,
  never imports the transport); `internal/inbound/http/` = transport
  plumbing (middleware) only; `internal/inbound/views/` = the global
  presentation tree (shared Shell/layouts, the future UI kit — the theme
  root) consumed by every domain's templates. The theming seam: a themed
  kit is a new implementation of the ports + one cmd wiring change;
  partial override via embedding the concrete exported default (method
  promotion supplies the rest); override granularity = port method (page),
  deliberately — reuse via exported building blocks, never exported page
  internals. The growth rule: file axis flips transport→RESOURCE at
  resource #2 (`grants.go`; `grants_api.go`/`grants_html.go`/
  `grants_fragments.go` only when one grows heavy); transport-named files
  are the single-resource degenerate form; NEVER `/api` `/html` `/htmx`
  subdirectories; a subdirectory means a new contract (own
  schema/vocabulary) or a swappable implementation behind a port
  (`templates/`), never mere file count; a domain wanting its own package
  tree is two domains; the same rule mirrors in `logic/domains/<domain>/`
  and `templates/{resource}.templ`. State the **maximal flatten**
  explicitly (landmine 5): a single-resource, single-transport surface with
  a small handler set may keep its handlers in `routes.go` itself —
  `features/events/internal/inbound/events/routes.go` is the in-repo
  example (cite the post-task-2 path; task-5 cross-checks it) — marked
  in the text as a gopernicus-side clarification of the Segovia flag. Add one **view-tech scoping sentence (steward fold)**:
  view-tech dependencies (templ) ride the app module's go.mod and touch
  only `internal/inbound` — `internal/logic` stays sdk-only; `templates/`
  is the app pattern's first explicit blessing of a third-party dependency
  inside an inbound subtree, and the sentence stops a reader inferring
  more. **Two scoping sentences (consult fold,
  landmines 1+2):** the global `internal/inbound/views/` tree and its
  theming seam are **app-only** — a feature's seam is its `views/<pkg>`
  sibling module + exported port (FS1/FS3; cross-reference
  `features/README.md` §2); and the feature-side route-table form is a
  `Mount` dispatcher in `routes.go` with per-resource deny-by-absence
  `mountX` helpers in their resource files. Provenance line: adopted from
  Segovia v2 (flag #1, 2026-07-08), which is the living reference
  implementation — no host in this repo has app-local domains yet, and the
  future `gopernicus new domain` scaffold (workshop-v2) should emit this
  shape. Do NOT cite a `features/cms/theme/` (doesn't exist; the live
  override example is `examples/cms/internal/theme/`).

### task-2: rename the feature inbound packages to `internal/inbound/<feature>/` (D1 = (d))

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authentication/internal/inbound/http/ →
  features/authentication/internal/inbound/authentication/,
  features/cms/internal/inbound/http/ →
  features/cms/internal/inbound/cms/,
  features/events/internal/inbound/http/ →
  features/events/internal/inbound/events/, features/cms/cms.go,
  features/cms/views.go, features/authentication/authentication.go,
  features/events/events.go,
  features/authentication/internal/logic/authsvc/context.go]
- **verify:** `make check` (cross-module: three feature modules + drift
  gate) and `make guard`; `git diff --stat` shows directory renames +
  import/package lines only
- **description:** Executes under the D1 = (d) ratification. `git mv`
  each feature's `internal/inbound/http/` to
  `internal/inbound/<feature>/`; update the three package clauses
  (`package authentication` / `package cms` / `package events`) and the
  `internalhttp` import aliases in the four root files — rename the alias
  to **`inbound`** (reads `inbound.Mount(...)`; never leave a lying
  alias; consult landmine 4); update the in-comment path reference at
  `features/authentication/internal/logic/authsvc/context.go:29`. The
  inbound package name now equals the feature's root package name — the
  alias is what keeps the root-socket files readable; goimports will not
  invent it, write it. No body changes.

### task-3: cms re-slice — `router.go` → `routes.go`

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/cms/internal/inbound/cms/router.go →
  features/cms/internal/inbound/cms/routes.go — post-task-2 paths]
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...`
  then `make check` and `make guard`; `git diff --stat` shows one rename,
  zero body-line changes; run-and-look: `make run` (examples/cms, :8080) —
  `GET /` renders 200, one admin page from the `Mount` route table renders
  200, `GET /media/{id}/file` behavior unchanged; kill, port free
- **description:** `git mv router.go routes.go` — `Mount`, `BuildRouter`
  (the test-convenience constructor), `RouterOption`, and `routerConfig`
  move together, unmodified (consult-verified: the resource axis already
  conforms, and the `views/templ` sibling module + root alias import
  `features/cms`, never `internal/`, so nothing else feels this). Do NOT
  touch `views.go`/`models.go` (the port + vocabulary are load-bearing
  across the module boundary via `features/cms/views.go` — the out-of-scope
  guardrail). Pure move; green tests alone do not close it — drive the
  pages.

### task-4: authentication re-slice — split `http.go` into `routes.go` + `sessions.go`

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/authentication/internal/inbound/authentication/http.go →
  features/authentication/internal/inbound/authentication/routes.go +
  features/authentication/internal/inbound/authentication/sessions.go,
  http_test.go → re-homed test files per the fixture survey — post-task-2
  paths]
- **verify:** `cd features/authentication && go build ./... && go test ./... && go vet ./...`
  then `make check` and `make guard`; `git diff --stat` shows moves with
  zero net body changes; run-and-look: boot `examples/auth-cms`
  (`cd examples/auth-cms && go run ./cmd/server`), drive the cookie-jar
  flow — register → verify (code from the console-mailer log) → login →
  `POST /auth/logout` — and report exact codes (must match pre-slice
  behavior); kill, port free
- **description:** **Substep 1 (consult landmine 3, before any move):**
  survey `http_test.go` (466 lines) and the sibling `_test.go` files for
  shared unexported fixtures (`do`, `newTestHandler`, the `mem*` stores,
  `fakeHasher`, `nopMailer`); give them ONE home — `routes_test.go` or a
  new `helpers_test.go`. All five test files are `package http`, so
  fixture location can never strand compilation (lead-backend fold) — the
  real hazards are a **duplicate declaration** (re-adding a fixture the
  survey missed) and an **omission** (a fixture dropped in the move);
  survey against those two failure modes, then confirm the sibling test
  files still compile. **Substep
  2:** split `http.go` → `routes.go` (the package doc, `Mount` :143, the
  **`handlers` struct (http.go:72–77)** — it is the package-wide handler
  set: it couples `authService` with `InvitationService`, receives methods
  from every resource file, and `Mount` constructs it; it lives with the
  dispatcher, never inside one resource's file (steward fold; the
  lead-backend alternative of `sessions.go` was considered and declined —
  the struct spans resources) — and the transport plumbing:
  `clientInfoRegistrar`, `clientInfoMiddleware`,
  `clientIP`) + `sessions.go` (the `authService` port, the
  request/response DTOs, `newUserResponse`, `decode`, and the
  session/account handlers: register, login, token, verify,
  forgotPassword, resetPassword, changePassword, logout — `token` stays
  here per the growth rule's only-split-when-heavy clause; `token_test.go`
  already pairs with it, and post-split there is STILL no `token.go` —
  the pre-existing filename pairing persists; recorded here so it is not
  later read as a miss). Import blocks shift with the moves
  (`encoding/json` leaves the Mount file, joins `sessions.go`); goimports
  is the sanctioned cleanup and those lines are the only permitted
  non-move diff. The port+DTO+`decode` move strands nothing:
  `oauth.go`/`machine.go`/`invitation.go` reference package-level
  identifiers, not imports. The per-resource `mountOAuth` (oauth.go:43) /
  `mountMachine` (machine.go:117) / `mountInvitations` (invitation.go:90)
  helpers STAY in their resource files — that IS the ratified feature form
  (landmine 1), not a violation to inline. Re-home `http_test.go` content
  as `sessions_test.go` (+ whatever the fixture survey put in
  `routes_test.go`/`helpers_test.go`). If the port/DTO placement in
  `sessions.go` proves awkward mid-move, a small shared file is
  acceptable — log the call; never a `/api` or `/html` subdirectory.
  Zero behavior change. **Abort line (PM fold):** if the fixture survey
  shows the split forces non-trivial body churn, do not push it through a
  rename-only phase — defer the auth split to its own micro-phase and
  record authentication as known-nonconformant in NOTES.md; the phase's
  value (ratified convention + cms rename + events' conformance) survives
  without it.

### task-5: docs — `features/README.md` §2, the feature-side record, NOTES.md

- **depends_on:** [task-3, task-4]
- **model:** fable
- **files:** [features/README.md, ARCHITECTURE.md, NOTES.md]
- **verify:** `make guard`; cross-read task-1's subsection and this text —
  the app-only vs feature-side scoping must not contradict; then
  `make check` once (final phase gate, 30 modules)
- **description:** `features/README.md` §2: rewrite the anatomy-table row
  for the feature inbound package (~line 52; the path per D1's outcome) to
  name the ratified file anatomy — `routes.go` as the route table (a
  `Mount` dispatcher; per-resource deny-by-absence `mountX` helpers live
  in their resource files — the authentication shape), per-resource files
  at resource #2, transport-named `api.go`/`html.go` only as the
  single-resource degenerate form, never `/api` `/html` `/htmx`
  subdirectories — and update the app↔feature mapping-table row for
  `internal/inbound` (~line 66) to point at the app anatomy
  (`internal/inbound/domains/<domain>/`) with the partial-mirror deltas
  stated: templates never co-locate (FS1 — templ is third-party), the
  render port lives in the core with the bundled default as the
  `views/<pkg>` sibling module (FS3), and the app-side global
  `internal/inbound/views/` theme tree has no feature equivalent — the
  feature theming seam is embed-the-sibling-default (landmine 2). Cite the
  provenance (Segovia flag #1, 2026-07-08). ARCHITECTURE.md: update the
  repo-tree feature line (~line 26) only if D1 changed the path; otherwise
  untouched. Record events' zero-diff conformance
  (`features/events/internal/inbound/http/routes.go` — the maximal
  flatten, the blessed example of that rung) wherever the anatomy row
  cites examples. **Fix the two stale `features/README.md` paths the
  re-slice makes doubly wrong (steward fold):** `internal/http` at
  ~lines 170 and 204 → `internal/inbound/http`, and the §4 C1 route-table
  pointer → `features/cms/internal/inbound/http/routes.go`'s `Mount`.
  NOTES.md: dated entry — flag #1 adopted verbatim (+ the maximal-flatten
  clarification, flagged as the one gopernicus-side extension), D1
  outcome + rationale, the deliberate feature-side deltas, tasks 3/4
  diffs summarized, pointer to the workshop-v2 scaffold implication,
  **and a guard-or-decline record (steward fold)** for the one
  mechanically-guardable rule — "NEVER `/api` `/html` `/htmx`
  subdirectories" (a find-over-dirs guard is possible): guard it or
  record why not; never silence.

## Sequencing

task-1 (D1-independent, can land immediately) → D1 ratification gates the
rest → task-2 (the rename) → tasks 3 and 4 (independent
of each other; either order) → task-5 documents reality last. Every task
boundary leaves all 30 modules building; each lands as a CI-green commit.

## Acceptance

```sh
make check     # 30 modules, templ drift no-op, all guards
make guard
git log --stat -3   # re-slice commits show renames/moves, no body churn
```

Run-and-look (both legs, per tasks 3/4): examples/cms pages render;
auth-cms register→verify→login→logout codes unchanged. Green tests alone
close nothing here — the diffs are behavior-neutral by claim, and the claim
is proven by driving, not asserting.

## Consultation notes

`lead-frontend-engineer` consulted pre-cut (2026-07-08, paragraph sketch,
single hop): **ship-with-edits** — all four findings folded in place:

1. **"Never split the route table" is feature-violated on day one** —
   authentication's `Mount` dispatches to `mountOAuth`/`mountMachine`/
   `mountInvitations` in their resource files (deny-by-absence). Folded as
   the explicit feature exception (tasks 1, 4, 5): dispatcher in
   `routes.go`, `mountX` helpers stay put.
2. **The global `internal/inbound/views/` theme-tree doctrine is
   app-only** — the feature seam is the `views/<pkg>` sibling module +
   exported port (`features/cms/views.go` alias; live override at
   `examples/cms/internal/theme/`); no `features/cms/theme/` exists and
   none may be cited. Folded into tasks 1 and 5 as scoping sentences.
3. **The auth test split fights over shared fixtures** in `http_test.go`
   that the four sibling test files likely compile against. Folded as
   task-4's mandatory fixture-survey substep.
4. **D1(a) has no import collision** (the rim `domain/` is a directory of
   packages) **but takes alias + doc-comment churn, and Segovia's form is
   plural `domains/<x>`** — singular `domain/` is itself a degenerate
   adaptation, strengthening (b). Folded into the D1 section; events'
   already-conforming `http/routes.go` recorded as a further (b) data
   point.

Also adopted: the don't-relocate-the-Views-port guardrail (Out of scope +
task-3) and the `git diff --stat` renames-only + templ-drift-no-op checks
on every re-slice verify. Verified-safe findings recorded: the cms
re-slice is genuinely one rename; the doc doctrine (Renderer returns,
override-via-embedding, page-level granularity) matches `cms.Views` +
`templ.Views{}` exactly.

**Post-cut reviews (2026-07-08, three run in parallel; all verdicts
ship-with-edits; every must-fix folded in place):**

- `product-manager` — folds: the adoption-path statement (Goal), task-4's
  abort line, task-1 named the shippable core, and (in `00-overview.md`)
  the milestone health condition + the ledger's raised-on column. Its open
  question on task-3's run-and-look leg is answered: the leg stays — the
  house false-green history outweighs the cost of driving a pure rename.
- `architecture-steward` — folds: the `handlers` struct given an explicit
  home (task-4), the stale `features/README.md` `internal/http` citations
  fixed in task-5, D1(a)'s Against (iv) charter-amendment cost, task-1's
  view-tech scoping sentence, and task-5's guard-or-decline record.
  Independently verified: the Makefile guard-posture claim, all cited
  line numbers, and that co-locating templ under app-side inbound breaks
  no locked rule (the one rule constrains `internal/logic` and `sdk`,
  never inbound).
- `lead-backend-engineer` — folds: **landmine 5** (the maximal-flatten
  clause — without it events is non-conforming the day task-1 lands,
  contradicting the phase's own zero-diff claim), the fixture-risk
  reframe (same-package scope → duplication/omission, never stranding),
  the token_test.go pairing note, and the goimports import-line note.
  Independently verified: the split strands nothing (package-level
  identifiers, not imports); the root sockets are untouched under D1(b).
- **Divergence resolved:** steward placed `handlers` in `routes.go`,
  lead-backend in `sessions.go`. Ruled `routes.go` — the struct couples
  services across resources and is constructed by `Mount`; parking it in
  one resource's file is the placement smell the anatomy exists to
  prevent.

**Owner direction (2026-07-08, post-reviews):** D1 ruled toward **(d)
`internal/inbound/<feature>/`** — the owner declined (b) (domain-shaped
content organized under a transport name) and withdrew (a). The post-cut
(b) recommendation is superseded; the reviews' factual verifications
(blast radius, guard posture, root-socket aliases) carry over unchanged,
since (d)'s churn is identical to (a)'s already-inventoried set. Task-2
became unconditional; **(d) was ratified the same day** — the owner's
follow-up names the `dashboards` mirror explicitly and rules `http/` as
plumbing-only ("higher-level middlewares, generics").

## Open questions

None. D1 RATIFIED (d) 2026-07-08 (section above); the remaining gates
are the phase's own ratification and the clean-tree precondition.

## Recommended reviews

All run post-cut 2026-07-08 and folded — see §Consultation notes.
(`lead-backend-engineer` ran in place of the recommended post-hoc
frontend pass: the Go split mechanics were the open surface, and the
frontend doctrine was already consulted pre-cut.)

- `product-manager` — scope discipline (docs + rename-only re-slice; flag
  #3 deliberately not folded in). **Done, folded.**
- `architecture-steward` — the ARCHITECTURE.md / `features/README.md`
  amendments and the D1 framing against the trio-relayout amendment.
  **Done, folded.**
- `lead-backend-engineer` — the split mechanics, symbol partition, and
  verify coverage. **Done, folded.**

## Execution log

- 2026-07-08 — phase ratified in-conversation (jrazmi: "ratified, can we
  update our features/"). Precondition wait: an unrelated session held the
  tree dirty (121 files, overlapping the auth inbound package); executed
  only after it committed (`621ef38 crud work`) and `git status` came back
  clean. Baseline `make check` green before task-1.
- 2026-07-08 — **task-1 done** (`212b473`): ARCHITECTURE.md §"Inbound
  anatomy — inside `internal/inbound/`" added after the app-pattern dir
  table; proofread against the verbatim flag — all rules present, plus
  the marked maximal-flatten clarification and the view-tech scoping
  sentence. `make guard` green.
- 2026-07-08 — **task-2 done** (implementer agent; committed next SHA):
  three dir renames to `internal/inbound/{authentication,cms,events}`,
  package clauses, root-socket aliases `internalhttp` → `inbound`,
  `authsvc/context.go` comment path. 32 files, 46+/46−, renames + line
  edits only. Per-module build/test/vet + `make check` + `make guard`
  green.
- 2026-07-08 — **task-3 done**: cms `router.go` → `routes.go`, pure
  0-line rename (no `router_test.go` existed). Run-and-look on
  examples/cms (live Turso): `GET /` 200 rendered HTML, `/terms` 200,
  `/menus` 200, bogus `/media/{id}/file` 404, clean shutdown. Note: this
  example mounts admin routes without auth middleware, so 200 (not a
  redirect) is the correct pre-existing behavior.
- 2026-07-08 — **task-4 done**: fixture survey found the shared set
  (`do`, `newTestHandler`, `mem*` stores, `fakeHasher`, `nopMailer`,
  `sessionCookie`, `denyLimiter`, the `authService` seam pin) → ONE home
  `helpers_test.go`; abort line not triggered. Split landed as specified:
  `routes.go` (package doc, `Mount`, `handlers` struct, clientInfo
  plumbing) + `sessions.go` (git-tracked rename of `http.go`: port, DTOs,
  `decode`, session/account handlers); `http_test.go` →
  `sessions_test.go`. Also fixed the three stale `// Package http` doc
  comments the task-2 rename orphaned (flagged by the executor, folded
  into the task-4 commit). Run-and-look on auth-cms (:8123): register
  201 → verify 200 → login 200 → logout 200 → stale-cookie 401.
  Line-citation drift from the interleaved `crud work` commit was
  re-surveyed as instructed; symbol inventory held.
- 2026-07-08 — **task-5 done**: `features/README.md` §2 anatomy row +
  app↔feature mapping row rewritten; §3/§4 stale `internal/http` paths
  fixed; ARCHITECTURE.md repo-tree line untouched (its `internal/inbound`
  mention was path-generic — the "only if D1 changed the path" condition
  did not fire); NOTES.md dated entry appended, including the
  **guard-or-decline record: DECLINED for now** (zero such subdirs exist;
  G1–G7 are import/module-shaped; named trigger = first in-repo app-local
  domain or the workshop-v2 scaffold). Final `make check` green.
