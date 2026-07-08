# feature-standard B2 — extract `features/cms/views/templ`; Views port in core

Status: **CUT 2026-07-07 under ratified 01-convergence.md task-B2 — execution
mechanics, no ratification gate (overnight-loop authorization).**
Parent: `.claude/plans/feature-standard/01-convergence.md` (task-B2, RATIFIED
2026-07-07) under `.claude/plans/feature-standard/00-charter.md` (FS1, FS3,
FS4, FS9; W3 guard mechanics). This sub-plan operationalizes ratified
decisions; it does not reopen them.

## Context

FS3 (00-charter, RATIFIED): a feature with HTML surface defines a Views port in
its core (domain-typed params, `web.Renderer` returns); the bundled default
ships as sibling module `views/<pkg>` named for the package it's built on
(R-KV2 → `features/cms/views/templ`); **nil Views → the HTML surface is not
registered** — the `theme.Default()` fallback dies, uniformly. FS1: after B2,
`features/cms/go.mod` requires ONLY sdk — the templ require and the
`tool github.com/a-h/templ/cmd/templ` directive move to the new module, and the
Makefile G5 carve-out (dated TODO, `Makefile:110-111`) is removed. The
admin-views coverage gap closes in the same move: admin pages (entries, terms,
menus, media, contact/inquiries, error) currently render by handlers calling
`internal/inbound/http/views` directly; they go behind the port too.

## Goal

`features/cms` core is sdk-only (G5 green without carve-out), all HTML —
public chrome AND admin — renders through a core-defined `cms.Views` port whose
bundled default lives in the new `features/cms/views/templ` module, all three
example hosts render identically to before, and nil Views provably registers no
HTML routes.

## Definition of Done

- `features/cms/go.mod`: `require` block contains exactly
  `github.com/gopernicus/gopernicus/sdk`; no `tool` directive; G5 passes with
  the cms carve-out deleted from the Makefile.
- `features/cms/views/templ` exists as its own module (in `go.work` and
  Makefile `MODULES`), owns the `.templ` sources + tool directive, and `make
  generate` targets it; generation is idempotent (two runs, no diff).
- Every handler render call goes through the port; `features/cms/theme` is
  deleted; nil `Config.Views` registers only `GET /media/{id}/file`.
- All three examples wired: `minimal` and `auth-cms` carry the one-line default
  (`Views: cmstempl.New()` + module require); `examples/cms` provides the full
  port by embedding the default and overriding the four chrome methods.
- `make check` fully green (drift gate, 27 modules, all six guards).
- Real-interaction checks pass: baseline-vs-after HTML byte-compare on the
  sample page set (examples/cms), public + admin pages render on all three
  hosts, and the nil-Views blackout is demonstrated live.

## Out of scope

- Handler-logic redesign of any kind — render calls change from `views.X(...)`
  to `h.views.X(...)` and nothing else moves. Error mapping
  (`web.ErrFromDomain`, `RecordError`, status selection) stays in handlers.
- cms public `Service` (B3 — deferred, demand-driven).
- Store modules, migrations, SQL — untouched.
- The contact-success non-PRG flow (`contact.go:57` renders `ContactThanks`
  inline on POST). Pre-existing; noted by the frontend lead; do NOT "fix" it
  in passing.
- Markdown/rich-text (FS10 holds it; B1 already landed `RenderPlainText`).
- The FS7 route-override hook.

## Schema / datastore impact

None. No SQL, no migrations, no store adapters, no EAV spine change. The
Registry/EAV render rail (`content.Registry.Render` over registered
`TemplateFunc`s) is preserved as the per-entry mechanism — only WHO supplies
the seed bindings changes (see port shape, task-2).

## Module / API impact

- **New module** `github.com/gopernicus/gopernicus/features/cms/views/templ`
  (package `templ` — dir-named per stores convention; verified this session:
  a package named `templ` whose generated code imports `github.com/a-h/templ`
  generates, builds, and vets cleanly). Joins `go.work` and Makefile `MODULES`.
  Requires: `github.com/a-h/templ v0.3.1020`, `features/cms`, `sdk`; dev
  `replace` for both locals; carries the `tool github.com/a-h/templ/cmd/templ`
  directive. Per RELEASING.md its future tag prefix is
  `features/cms/views/templ/v*` (pre-tag today — no tags exist yet).
- **`features/cms` breaking changes (pre-tag, so cheap — sequencing rule 2 of
  01-convergence):** `theme` package deleted (`PublicViews`, `ListItem`,
  `Default()` gone); `Config.Views` retyped `theme.PublicViews` → `cms.Views`;
  nil Views semantics flip from "bundled default" to "HTML surface not
  registered"; `cms.TemplateBinding` becomes an alias of the new
  `content.TemplateBinding` (host call sites compile unchanged); new public
  aliases (A1 precedent, `auth.Principal = authsvc.Principal` style):
  `cms.Views`, `cms.ListItem`, `cms.EntryListItem`, `cms.EntryFormModel`,
  `cms.FieldInput`, `cms.SelectOption`, `cms.TermChoice`, `cms.TermFormModel`,
  `cms.ContactModel`.
- **`features/cms/go.mod`** loses templ (require + tool directive) and its
  transitive tail; `go mod tidy` shrinks go.sum.
- **Example go.mods**: all three gain
  `require github.com/gopernicus/gopernicus/features/cms/views/templ v0.0.0`
  + relative replace. `examples/cms/go.mod` also drops its vestigial
  `tool github.com/a-h/templ/cmd/templ` directive (no `.templ` sources exist
  under `examples/` — verified; leaving it would keep a second, unmanaged
  templ pin after B2's whole point is consolidating the pin into views/templ).
- **Landing B2 satisfies the "feature-standard B1+B2 landed" precondition on
  repo-hardening task-9** — record it there (task-4 sync touches).

## Generated-artifact impact

The 11 `.templ` sources (and their `*_templ.go`) move from
`features/cms/internal/inbound/http/views/` to `features/cms/views/templ/`.
Their `package views` declarations are edited to `package templ` and their
model references retargeted to the `cms.` aliases **in the `.templ` sources
only**, then regenerated via the retargeted `make generate`. Never hand-edit
`*_templ.go` — move via `git mv`, edit the `.templ`, regenerate.
**Same-commit constraint (lead finding, drift-gate interplay):** the `git mv`,
the package renames, and the Makefile `generate` retarget must land in one
commit — a commit with `.templ` files in the new location but `generate` still
pointing at `features/cms` leaves `make check`'s drift gate comparing against
a directory with no sources (stale or missing `*_templ.go`), and the gate
itself needs a committed tree (`git diff --exit-code` vs HEAD), so check goes
green only at commit, as recorded in 01-convergence's execution log.

## Design decisions (binding for the implementer)

**D-1: Port location — exported from the internal transport package, re-exported
via root aliases.** The interface and all view-model structs are defined
EXPORTED in `features/cms/internal/inbound/http` (`views.go` for the interface,
`models.go` moved up from the departing views subpackage, plus `ListItem`
extracted from `public.templ` and `ContactModel` from `contact.templ`); the
root `cms` package re-exports them as public type aliases in a new
`features/cms/views.go`. This is the sanctioned A1 precedent. Guard fit:
(a) no core file imports anything matching G2's
`features/[a-z0-9]+/(stores|views)` regex — the port never lives under a
`views/` path; (b) port files sit in `internal/inbound/http`, which G2 scans
(not excluded), while the sibling module's own files are correctly skipped by
`--exclude-dir=views` exactly as stores are; (c) no import cycle — root `cms`
already imports `internal/inbound/http`, internal never imports root. **No G2
regex change is needed.** A public `features/cms/views` port package was
rejected: core imports of it would trip G2, and `--exclude-dir=views` would
exempt the port's own files from scanning.

**D-2: Port shape — one interface, `Views`, 19 methods, doc-grouped by
surface.** All methods return `web.Renderer`; params are domain types
(`menus.Menu[Item]`, `taxonomy.Term`, `media.Asset`, `messaging.Inquiry`,
`content.Entry` via bindings) and the port's own view models:

- *Public chrome* (former `theme.PublicViews`, signatures unchanged):
  `Home(nav []menus.MenuItem, items []ListItem)`,
  `Archive(heading string, nav []menus.MenuItem, items []ListItem, nextCursor, baseHref string)`,
  `Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer)`,
  `Error(status int, message string)`.
- *Public forms/fragments*: `ContactForm(m ContactModel)`, `ContactThanks()`,
  `MenuNav(m menus.Menu, items []menus.MenuItem)`.
- *Admin*: `EntriesList(heading, newHref, editPrefix string, items []EntryListItem, nextCursor string)`,
  `EntryForm(m EntryFormModel)`, `TermsList(categories, tags []taxonomy.Term)`,
  `TermForm(m TermFormModel)`, `MenusList(ms []menus.Menu)`,
  `MenuNew(formError string)`, `MenuDetail(m menus.Menu, items []menus.MenuItem)`,
  `MenuItemForm(it menus.MenuItem)`,
  `MediaLibrary(assets []media.Asset, formError string)`,
  `InquiriesList(items []messaging.Inquiry)`,
  `AdminError(status int, message string)` — renamed from `views.ErrorPage`
  to avoid clashing with the chrome `Error` (they are distinct templates:
  `error.templ` vs `public.templ`'s `PublicError`).
- *Seed templates*: `SeedTemplates() []content.TemplateBinding` — see D-3.

The port doc comment states FS3 and the blessed partial-override path: embed
the concrete default from `features/cms/views/templ` and override individual
methods; implementing all 19 from scratch (e.g. pure `web.Template`) is
possible but not the sold path. Go ordering per repo convention: consts/vars
top of file, then the interface.

**D-3: Seed templates come off the Views value, but ride the Registry rail.**
The frontend lead objected to `Article(e)`/`Page(e)` render methods on the
port (conflates the chrome seam with the ratified per-type seam,
`Config.Templates`). Resolution — middle path, planner's call: a new struct
`content.TemplateBinding{Type, Template string; Fn TemplateFunc}` lands in
`features/cms/logic/content` (registry-adjacent, where `TemplateFunc` lives);
root `cms.TemplateBinding` becomes its alias so host call sites compile
unchanged. The port carries `SeedTemplates() []content.TemplateBinding`; the
default implementation returns the article/page "default" bindings (wrapping
the moved `ArticleContent`/`PageContent` components). `cms.Register` replaces
`registerSeedTemplates` with: if `cfg.Views != nil`, register
`cfg.Views.SeedTemplates()` on the Registry at the same point in the sequence
(after seed types + `cfg.Types`, before `cfg.Templates`).
`Registry.RegisterTemplate` is last-write-wins (verified,
`logic/content/registry.go:72`), so host `Config.Templates` still overrides
seed bindings — behavior identical to today. Rendering stays on the Registry
rail; the Views value only supplies default bindings. This avoids the lead's
alternative's silent trap (host wires `Views` but forgets a separate
`Templates: templ.SeedTemplates()` → seed singles 404) and keeps FS3's
one-line default wiring true. With nil Views nothing registers bindings — and
nothing needs them, since the single routes aren't mounted.

**D-4: Nil-Views route disposition — everything off except
`GET /media/{id}/file`.** `internalhttp.Mount` gets the branch (one place;
`BuildRouter` inherits it): when `views == nil`, register ONLY the media serve
route, then return. Justification: it is the sole non-HTML endpoint in the
inventory — byte serving with content-type, which an API-only or
custom-frontend host still needs for stored assets, and FS3's ratified wording
turns off "the HTML surface", which this is not. Explicit off-list under nil:
public home, all registry-driven public singles, `/category/{slug}`,
`/tag/{slug}`, `/menu/{slug}` (HTML nav fragment), `GET+POST /contact`
(renders form/thanks HTML), and the entire admin surface (entries CRUD per
type, terms, menus + menu-items, media library/upload/delete, `/inquiries`).
The nil→default fallbacks at `router.go:60-62` and `public.go:36-40` are
deleted. Accepted lead correction riding this: **`media.Serve`'s error path
switches unconditionally to `web.RespondJSONDomainError`** (`media.go:88-97`'s
`renderError` currently emits an HTML `ErrorPage` on a byte endpoint — a
latent bug independent of Views, and FS9-clean to fix this way). This is an
intentional wire delta; record it in the execution log. All other handlers'
error paths keep rendering `AdminError`/`Error` through the port — they only
exist when Views is non-nil.

**D-5: New-module surface.** `features/cms/views/templ` exports a concrete
embeddable struct + constructor (accept interfaces, return structs):
`type Views struct{}` and `func New() Views`, with 19 wrapper methods over the
package's templ components, `var _ cms.Views = New()` as the compile-time
conformance pin. `helpers.go` (`menuOrderStr`, `statusText`) and `markdown.go`
(`RenderPlainText`, B1's plain-text renderer — callers are all `.templ` files)
move with the templates. `.templ` sources import the root `cms` package for
model types (`cms.EntryFormModel`, …); the local `ListItem`/`ContactModel`
declarations in `public.templ`/`contact.templ` are deleted in favor of the
core-owned types.

## Risks

1. **Silent render regressions from the port widening** — ~30 direct
   `views.X(...)` call sites across six handler files re-thread through the
   port; a wrong model mapping or swapped argument renders plausible-looking
   wrong HTML behind green tests. Mitigation: task-1 captures baseline HTML
   bytes BEFORE any change; task-4 re-captures the identical URL set and
   byte-compares (expected: identical; any diff halts the leg). Handler edits
   are strictly mechanical — only the final render call changes.
2. **templ drift-gate interplay while files move** — see the same-commit
   constraint under Generated-artifact impact. Also: `make check` iterates
   `MODULES`; between task-2 and task-3 the examples don't compile, so full
   `make check` is only demanded at task-3's close (see Sequencing).
3. **Nil-Views blackout of `examples/minimal` and `examples/auth-cms`** (lead's
   headline finding): both currently pass no Views and live off the dying
   fallback — forgetting their rewiring ships two dark hosts (auth-cms loses
   its auth-gated-admin reason to exist). Mitigation: task-3 wires both
   explicitly; task-4's run-and-look proves public + admin pages actually
   render on each.

## Tasks

### task-1: capture baseline rendered HTML (pre-change)

- **depends_on:** []
- **model:** opus
- **files:** [none in-repo — write captures to
  `/private/tmp/claude-502/-Users-jrazmi-code-gopernicus-ecosystem-gopernicus/c98f7a13-8dc3-46d0-ae1a-7071c5405dec/scratchpad/b2-baseline/`]
- **verify:** every capture file exists, is non-empty, and contains its
  expected marker (e.g. `<h1>Latest posts</h1>` on `/`, form fields on
  `/articles/new`); the URL list is recorded alongside the captures.
- **description:** Boot `examples/cms` via `make run` (runs `generate` +
  `migrate` first — the standing dev flow). If the dev DB has no content,
  create one published article (with a category term) and one page through the
  admin UI first. `curl -s` and save byte-exact: `/`, the article's public
  single, `/category/<slug>`, `/contact`, `/articles`, `/articles/new`,
  `/terms`, `/menus`, `/media`, `/inquiries`, and one 404 (`/no-such-page`).
  Then boot `examples/minimal` (in-memory store) and capture `/` and
  `/products` (its host-registered type's admin list). Record the exact URL
  list + any content created — task-4 replays it verbatim.

### task-2: extract the module, define the port, re-seam the core

- **depends_on:** [task-1]
- **model:** opus
- **files:** [
  `features/cms/views/templ/go.mod` (new), `features/cms/views/templ/go.sum` (new),
  `features/cms/views/templ/views.go` (new — concrete `Views` + `New()` + wrappers + conformance pin),
  `features/cms/views/templ/*.templ` + `*_templ.go` (git mv from `features/cms/internal/inbound/http/views/`, package + import edits, regenerated),
  `features/cms/views/templ/helpers.go`, `features/cms/views/templ/markdown.go` (git mv),
  `features/cms/internal/inbound/http/views.go` (new — the exported port interface),
  `features/cms/internal/inbound/http/models.go` (git mv from `views/models.go` + absorb `ListItem`, `ContactModel`),
  `features/cms/internal/inbound/http/router.go`, `.../public.go`, `.../entries.go`, `.../terms.go`, `.../menus.go`, `.../media.go`, `.../contact.go`,
  `features/cms/internal/inbound/http/*_test.go` (stub Views; seam tests rewritten),
  `features/cms/views/templ/views_test.go` (new — relocated rendered-HTML assertions),
  `features/cms/views.go` (new — root aliases), `features/cms/cms.go`, `features/cms/seeds.go`,
  `features/cms/logic/content/registry.go` (or a sibling file — `TemplateBinding`),
  `features/cms/theme/` (deleted), `features/cms/go.mod`, `features/cms/go.sum`,
  `Makefile` (header comment lines 3-5, `generate` target lines 18-20, `MODULES` line 7, G5 carve-out + TODO lines 110-124),
  `go.work`]
- **verify:** `cd features/cms && go build ./... && go test ./... && go vet ./...`;
  `cd features/cms/views/templ && go build ./... && go test ./... && go vet ./...`;
  `make generate` twice — `git status --porcelain -- '*_templ.go'` identical after
  both runs (idempotence); `make guard` (all six; G5 now WITHOUT the cms
  carve-out); `grep -c 'a-h/templ' features/cms/go.mod` returns 0. Full
  `make check` is NOT expected green yet (examples rewire in task-3).
- **description:** Execute D-1 through D-5 as one coherent, same-commit unit.
  Module: create the go.mod (module path
  `github.com/gopernicus/gopernicus/features/cms/views/templ`, package `templ`,
  requires + replaces + tool directive per Module/API impact), `git mv` the
  views directory contents, edit `.templ` `package` declarations and model
  references (`cms.` aliases), delete the now-core-owned type declarations from
  `public.templ`/`contact.templ`, retarget the Makefile `generate` target, and
  regenerate. Core: define the port + models in `internal/inbound/http`
  (D-1/D-2), add `content.TemplateBinding` + root aliases, thread the port
  through all six handler constructors (mechanical — only render calls change;
  `views.ErrorPage` sites become `h.views.AdminError`), implement D-4's nil
  branch in `Mount` + the `media.Serve` `RespondJSONDomainError` change, rename
  `WithPublicViews` → `WithViews(Views)` on `BuildRouter`, replace
  `registerSeedTemplates` with the D-3 `SeedTemplates()` registration in
  `cms.Register`, retype `Config.Views`, delete `features/cms/theme`, and
  `go mod tidy` both modules. Tests: internal handler tests get a package-local
  stub `Views` (marker renderers); `public_views_test.go`'s override-seam test
  is recast on the stub (embed + override one method), and its
  rendered-real-chrome assertions move to `features/cms/views/templ/views_test.go`
  (render `New().Home(...)` etc. directly and assert markup — including one
  embed-the-default-override-one-method test proving the partial-override path).
  Makefile: add the module to `MODULES`, delete G5's cms carve-out and its
  dated TODO, update the header + generate comments (templ pin now
  `features/cms/views/templ/go.mod`). Add the module to `go.work`.

### task-3: rewire the three example hosts

- **depends_on:** [task-2]
- **model:** opus
- **files:** [
  `examples/minimal/go.mod`, `examples/minimal/cmd/server/main.go`,
  `examples/auth-cms/go.mod`, `examples/auth-cms/cmd/server/main.go`,
  `examples/cms/go.mod`, `examples/cms/internal/theme/theme.go`,
  `examples/cms/internal/theme/theme_test.go`, `examples/cms/cmd/server/main.go`]
- **verify:** `make check` — fully green: drift gate, all 27 modules
  (including `features/cms/views/templ`), integration-tag vet, all six guards.
- **description:** `examples/minimal` + `examples/auth-cms` (the FS3 one-line
  default wiring the examples must demonstrate): add the views/templ require +
  relative replace, import it (suggest alias `cmstempl`), set
  `Views: cmstempl.New()` in the `cms.Config` literal; update minimal's header
  comment ("(+ theme deps)" → the views-module framing; its zero-libsql claim
  is unchanged — templ was already in its graph via features/cms and now
  arrives via views/templ instead). `examples/cms` (the partial-override
  demonstration): rewrite `internal/theme` to implement `cms.Views` by
  embedding `cmstempl.Views` and overriding exactly the four chrome methods
  (`Home`, `Archive`, `Single`, `Error`) with the existing ACME
  `html/template` chrome; `cmstheme.ListItem` → `cms.ListItem`; add the
  views/templ require + replace and delete the vestigial
  `tool github.com/a-h/templ/cmd/templ` directive from its go.mod; update
  `theme_test.go` — keep the ACME chrome assertions and ADD one asserting a
  non-overridden port method (e.g. `EntriesList`) renders the bundled default
  through the embedded struct.

### task-4: real-interaction verification, sync touches, records

- **depends_on:** [task-3]
- **model:** opus
- **files:** [
  `.github/workflows/check.yml` (header comment lines 22-25 only — the
  workflow runs `make check`; no functional YAML change),
  `.claude/plans/repo-hardening/plan.md` (task-5 sync note; task-9 precondition
  record + new-module tag-slice note),
  `ARCHITECTURE.md` (line ~75: "(lands at feature-standard B2)" → landed),
  `features/README.md` (line ~55: "it migrates to `views/templ` at
  feature-standard B2" → past tense),
  `.claude/plans/feature-standard/02-b2-views-extraction.md` (execution log),
  `.claude/plans/feature-standard/01-convergence.md` (B2 execution-log entry)]
- **verify:** `make check` green. Run-and-look (green tests alone do NOT close
  this task): (1) `make run` (examples/cms) → replay task-1's exact URL list,
  byte-compare against the baseline captures — expected identical; any diff
  halts and is investigated before proceeding. Browser-check the public home,
  one admin CRUD page, and a contact-form submit. (2) Boot `examples/minimal`:
  `/`, `/products`, one product public page render with the bundled default
  chrome. (3) Boot `examples/auth-cms`: public page renders; log in via the
  auth-v2 protocol; an admin page renders behind `RequireUser`. (4) Nil-Views
  FS3 proof: while `examples/minimal` still has Views wired, upload one media
  asset; then temporarily set `Views: nil`, rebuild, assert `/` and
  `/articles` return 404 and `GET /media/{id}/file` still serves the bytes;
  revert the temporary change (`git diff` clean afterward).
- **description:** Close the leg per the standing protocol. Sync touches (the
  remaining silent-breakage paths): update check.yml's templ-pin header comment
  to `features/cms/views/templ/go.mod`; append a sync note to repo-hardening
  task-5 (its CI description pins templ via "the `tool` directive in
  `features/cms/go.mod`" — now `features/cms/views/templ/go.mod`); record on
  repo-hardening task-9 that the "feature-standard B1+B2 landed" precondition
  is satisfied, and note the new module for its tag-slice consideration
  (examples/cms now requires it); confirm RELEASING.md doesn't enumerate
  modules by name (spot-check; add the new prefix only if it does). Write this
  plan's execution log (including the two intentional wire deltas: media.Serve
  error responses now JSON, nil-Views semantics) and the one-paragraph B2
  entry in 01-convergence's log ("cms carve-out gone" — its Acceptance line).

## Sequencing

Strictly sequential, task-1 → task-4. Task boundaries are execution
sequencing, not necessarily commit boundaries: task-2 alone leaves the three
example modules uncompilable, so if the loop's protocol demands every commit
pass `make check`, land task-2 + task-3 as ONE commit (task-2's own verify
block still gates the midpoint). Task-2's internal same-commit constraint
(git mv + package rename + Makefile `generate` retarget + regeneration) is
non-negotiable regardless. Task-1 must complete before any code change — it
is the only chance to capture the baseline.

## Consultation notes

`lead-frontend-engineer` consulted 2026-07-07 (single hop). Verdict:
ship-with-edits. Adopted: (1) the headline correction — `examples/minimal` and
`examples/auth-cms` silently rely on the dying nil→default fallback and need
full rewires (go.mod require + import + Config field), now explicit in task-3
and Risk 3; (2) `media.Serve` always responds via `web.RespondJSONDomainError`
(latent HTML-on-byte-endpoint bug, FS9-clean fix) — folded into D-4; (3) the
same-commit drift-gate constraint — folded into Generated-artifact impact and
task-2; (4) port doc must state the blessed partial-override path (embed the
concrete default) — folded into D-2/D-5; (5) contact non-PRG flow noted, not
fixed. Partially adopted (planner's call): the lead wanted seed templates
entirely off the port, exposed as `[]cms.TemplateBinding` wired through
`Config.Templates`; that reintroduces a two-line wiring with a silent-404 trap
(wire Views, forget Templates) and breaks FS3's one-line-default promise.
D-3's `SeedTemplates()` method honors the lead's actual objection — per-entry
rendering stays on the Registry rail, distinct from chrome — while the Views
value merely carries the default bindings. Confirmed by the lead: G2 guard
reading, A1 alias mechanics, AdminError/Error split, tooling-move inventory.

## Open questions

_None._ All decision points are resolved above (port location D-1, shape D-2,
seeds D-3, nil disposition D-4, package name `templ` — scratch-verified).

## Recommended reviews

- **product-manager** — scope/sequencing sanity; the three examples remain
  coherent demonstrations (one-line default ×2, partial override ×1).
- **lead-frontend-engineer** — post-hoc pass on the landed port + templ module
  (was consulted pre-plan; D-3 diverges from their recommendation).
- **architecture-steward** — D-1's port placement + alias pattern, G2/G5 guard
  fit, module-boundary correctness.
- **platform-sre** — templ-pin migration (Makefile/CI comment), MODULES/go.work
  completeness, tag-prefix implications for repo-hardening task-9.

## Notes

- Auth naming rule and Go conventions apply throughout (consts/vars top of
  file, then interfaces; accept interfaces, return structs).
- Wire deltas of record after this plan: (1) `GET /media/{id}/file` error
  responses are JSON (`web.RespondJSONDomainError`), previously HTML
  `ErrorPage`; (2) nil `Config.Views` → HTML surface not registered
  (previously bundled default). Both intentional, both per ratified FS3/FS9.
- The scratch package-name verification lives at
  `/private/tmp/claude-502/-Users-jrazmi-code-gopernicus-ecosystem-gopernicus/c98f7a13-8dc3-46d0-ae1a-7071c5405dec/scratchpad/templpkg`
  (disposable).

## Execution log

### 2026-07-07 — tasks 1–4 executed (sequential; tasks 2+3 landed as one
uncommitted unit, task-4 verified + synced)

**task-1 (baseline capture) — landed.** 14 byte-exact pre-change captures at
`scratchpad/b2-baseline/` with a `manifest.md` recording boot commands, the
content created in the examples/cms Turso dev DB (News category, "B2 Baseline
Article", "B2 Baseline Page"), and the exact 11-URL (examples/cms) + 3-URL
(examples/minimal) replay set. All markers verified present; both servers killed.

**task-2 (extract module, define port, re-seam core) — landed.** New module
`features/cms/views/templ` (package `templ`) with the concrete embeddable
`Views` struct + `New()` + 19 wrapper methods + `var _ cms.Views = New()`
conformance pin; the 11 `.templ` sources + `helpers.go` + `markdown.go` `git
mv`'d in, `package` renamed, models retargeted to `cms.` aliases, regenerated.
Core: `cms.Views` port + view models exported from `internal/inbound/http`
(`views.go`, `models.go`), re-exported via root `features/cms/views.go` aliases;
`content.TemplateBinding` added; port threaded through all six handler
constructors (render calls only); D-4 nil branch in `Mount` + `media.Serve`
switched to `web.RespondJSONDomainError`; `registerSeedTemplates` replaced by
`SeedTemplates()` registration; `Config.Views` retyped; `features/cms/theme`
deleted; both go.mods tidied; Makefile `MODULES` + `generate` + G5 (carve-out +
dated TODO removed) + header updated; `go.work` updated. Verify:
`features/cms` and `features/cms/views/templ` build/test/vet PASS; `make
generate` idempotent; six guards PASS (G5 cms-clean); `grep -c a-h/templ
features/cms/go.mod` → 0.

**task-3 (rewire three example hosts) — landed.** `minimal` + `auth-cms`: added
the views/templ require + relative replace, imported it (`cmstempl`), set
`Views: cmstempl.New()`; minimal header comment updated. `examples/cms`:
`internal/theme` reworked to implement `cms.Views` by embedding `cmstempl.Views`
and overriding the four ACME chrome methods (`Home`/`Archive`/`Single`/`Error`);
`cmstheme.ListItem` → `cms.ListItem`; require + replace added, vestigial
`tool` directive dropped; `theme_test.go` keeps the ACME assertions and adds one
asserting a non-overridden port method renders the bundled default.

**task-4 (real-interaction verify, sync, records) — landed (this entry).**

*Baseline byte-compare (the leg's proof, not green tests):*
- **examples/cms (11 URLs, remote Turso dev DB, :8080):** `/`,
  `/articles/b2-baseline-article`, `/category/news`, `/contact`, `/articles`,
  `/articles/new`, `/terms`, `/menus`, `/media`, `/inquiries`, `/no-such-page`
  (404) — **all 11 byte-identical** (`cmp -s`) to the baseline. Contact-form POST
  drove the real flow → thanks page rendered (200, `<h1>Thanks!</h1>`), and the
  inquiry appeared on `/inquiries` (name + email + message body).
- **examples/minimal (3 URLs, in-memory, :8081):** `/products/widget-3000`
  (deterministic slug) **byte-identical**; `/` and `/products` differed ONLY by
  per-boot in-memory KSUIDs and same-timestamp article ordering. **Investigated,
  not rationalized:** a second independent reboot reproduced fresh differences
  between the two after-boots, and a normalized compare (strip 26-char base32
  IDs, sort article blocks) MATCHED baseline exactly — the render is identical;
  the store mints fresh random IDs each boot (byte-identity across boots is
  impossible for minimal regardless of the B2 change). No render regression.
- **examples/auth-cms (:8082, in-memory, AUTH_DEBUG=1):** public `/` renders the
  bundled default (`<h1>Latest posts</h1>`, 200); `GET /articles` without a
  session → 401 (`RequireUser`); register (201) → verify with the mailer-logged
  code (200) → login with cookie jar (200) → `GET /articles` with session →
  **200 with admin HTML** (`<h1>Articles</h1>`), rendered through the bundled
  default port behind the auth gate.

*Two wire deltas of record — confirmed LIVE (nil-Views FS3 proof, examples/minimal,
temporary `Views: nil` build):*
1. **nil-Views blackout:** `/`, `/products`, `/products/widget-3000` all → **404**
   (HTML surface not registered).
2. **media file route JSON error:** `GET /media/nonexistent/file` → **404 with
   `{"message":"not found","code":"not_found"}` and `Content-Type:
   application/json`** — proving the sole non-HTML route stays mounted and now
   error-responds via `web.RespondJSONDomainError` (previously HTML `ErrorPage`).
   Temporary edit reverted; `git diff examples/minimal/cmd/server/main.go` shows
   only task-3's intended change. All servers killed; ports 8080/8081/8082 free.

*Sync touches:* `check.yml` header templ-pin comment → `features/cms/views/templ/go.mod`;
repo-hardening task-5 sync note (pin path moved) + task-9 record ("feature-standard
B1+B2 landed 2026-07-07 — precondition satisfied" + new module noted for the
tag-slice, examples/cms now requires it); `ARCHITECTURE.md` ~L75 and
`features/README.md` ~L52/L55 flipped to landed/past tense (theme now deleted);
`RELEASING.md` enumerates by name — module count 26→27 and `features/cms`
(+ `views/templ`) added.

*`make check` result:* fails at the FIRST step, the templ drift gate — exact
error line: `ERROR: templ generation drift (git diff)` then `make: *** [check]
Error 1`. Cause: the `git mv`'d `*_templ.go` at HEAD still embed
`FileName: internal/inbound/http/views/…`, while regeneration in the new location
writes `FileName: contact.templ` etc.; `git diff --exit-code -- '*_templ.go'`
sees the delta until the move is committed (the same-commit constraint — expected,
not a regression). Because the drift gate aborts `check` before the rest runs, the
remaining stanzas were run directly and **all PASS**: build/vet/test across all 27
`MODULES` (incl. `features/cms/views/templ`), the three `%/turso` integration-tag
vets, and all six guards. `git status --porcelain` confirms only intended files
changed.

*Deviations (carried from tasks 2/3, not introduced here):*
- **`cms_test.go` package-local `stubViews`:** `Register`'s full HTML-route test
  uses a nil-rendering package-local `stubViews` rather than importing the bundled
  default (which would cycle — `views/templ` imports `cms`). Handlers aren't
  invoked in that test, so the renderers are nil. Intended per the plan's test
  strategy; rendered-markup assertions live in `features/cms/views/templ/views_test.go`.
- **Stale store-module go.mod indirects (pre-existing, out of scope):**
  `features/cms/stores/turso/go.mod` and `.../stores/pgx/go.mod` still carry
  `a-h/templ` + `goldmark` + `bluemonday` `// indirect` entries (3 each) inherited
  transitively from the pre-convergence `features/cms` graph. B2 leaves them
  untouched (store modules are out of scope here); they re-tidy naturally when
  those modules are tagged at repo-hardening task-9/10. Flagged, not fixed.
