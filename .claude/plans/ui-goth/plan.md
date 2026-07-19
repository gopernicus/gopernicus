# Gopernicus `ui/goth` — server-rendered UI kit and Shadcn parity

Status: **DRAFT FOR OWNER REVIEW — core direction ratified 2026-07-17; no
implementation has started.** Working name: `ui-goth`; task prefix: `GOTH`.
Catalog baseline: the 64 entries under the official Shadcn "All Components"
catalog, captured 2026-07-17 from <https://ui.shadcn.com/docs/components>.

## Outcome

Ship a top-level, independently importable GOTH presentation implementation
that gives Gopernicus applications:

- a complete, documented port of the 2026-07-17 Shadcn component catalog;
- a stable semantic theme contract that a host can replace without rewriting
  component markup;
- templ components, compiled Tailwind CSS, CSP-compatible Alpine behavior, and
  optional HTMX 2.0.10 behavior in one deliberately composed bundle;
- a `primitives/` layer for Shadcn-parity building blocks and a `components/`
  layer for repeatable Gopernicus compositions;
- embedded, fingerprinted, self-hosted assets with explicit host wiring;
- a real showcase and browser-level accessibility/interaction proof; and
- first-party authentication and CMS view adapters that demonstrate the kit
  without making either feature core depend on templ, Tailwind, Alpine, HTMX,
  or `ui/goth`.

The top-level `ui/` directory is a technology family, not a claim that every
implementation must use Go packaging:

```text
ui/
  README.md          shared design vocabulary and parity rules
  goth/              templ + Tailwind + Alpine + optional HTMX
  react/             possible later implementation
  vue/               possible later implementation
```

## Ratified basis

The owner ratified these calls in the 2026-07-17 planning session:

1. `ui/` is top-level. It is neither a feature nor an integration.
2. The server-rendered implementation is `ui/goth`, not `ui/templ`, because
   the supported unit is the whole GOTH stack.
3. Later `ui/react` and `ui/vue` implementations are legitimate; the semantic
   token and component vocabulary should make those additions predictable.
4. The first parity milestone covers every component in the dated Shadcn
   catalog, not only an initial hand-picked subset.
5. Authentication's current asset-free pages are a secure historical default,
   not a permanent restriction. A selected GOTH view implementation may
   declare and load the styles, scripts, fonts, and images it needs.
6. HTMX ships on exact 2.0.10 for this milestone. Code and response conventions
   are written to make a later HTMX 4 upgrade deliberate and inexpensive; beta
   HTMX 4 is not the production foundation.

## Current-state evidence

- `ARCHITECTURE.md` currently defines six module kinds and reserves an
  app-local `internal/inbound/views/` tree as the future global UI-kit/theme
  root. A reusable top-level UI implementation therefore needs one explicit
  taxonomy amendment rather than being mislabeled an integration or feature.
- `features/README.md` already keeps feature cores technology-neutral through
  `Views` ports returning `web.Renderer`. Bundled view technology lives in
  sibling modules, currently `features/{authentication,cms}/views/templ`.
- The two view modules pin templ 0.3.1020 and are already covered by root
  generation and drift checks. `ui/goth` should extend, not bypass, this
  generated-artifact discipline.
- `sdk/foundation/web.StaticFileServer` already serves an `fs.FS` and applies
  immutable caching under an asset prefix. The UI kit does not need to invent a
  static-file server or register routes itself.
- Authentication's `writeHTMLSecurity` currently fixes
  `default-src 'none'` and allows only a per-render script nonce. Its own README
  explicitly records styling/assets as deferred post-v3 work. The seam belongs
  in the authentication core, while the GOTH-specific policy implementation
  belongs in its sibling view adapter.
- Segovia v1 already demonstrates the desired React layering and the Shadcn
  semantic token vocabulary. Its GPS theme has light/dark slots for background,
  foreground, card, popover, primary, secondary, muted, accent, destructive,
  border/input/ring, charts, sidebar, radius, status colors, and z-index roles.
  That vocabulary is input to the shared contract, not code imported by this
  repository.
- The official Shadcn registry describes itself as a framework-agnostic code
  distribution system. A Workshop-driven source installer is therefore a
  credible follow-up, but it is not needed to prove the importable v1 kit.

## Architecture decision

### A seventh module kind: UI implementation

Amend the repository taxonomy with:

| kind | definition | examples | swap unit |
|---|---|---|---|
| **UI implementation** | A reusable presentation system for one rendering/runtime family. It owns view-library dependencies, semantic tokens, components, interaction controllers, and distributable assets; it owns no domain schema or routes. | `ui/goth`; later `ui/react`, `ui/vue` | a host/view-adapter import plus theme/bundle configuration |

A UI implementation may depend on its presentation libraries. It must not
import a feature, integration, example, or Workshop package. `ui/goth` should
depend directly on templ; it should expose standard `fs.FS`/templ-shaped values
and avoid needing the SDK merely to exist. Feature view adapters may depend on
their feature core, SDK, and `ui/goth`.

```text
feature core  <--- feature views/goth adapter ---> ui/goth ---> templ/runtime packages
      ^                       ^                        ^
      |                       |                        |
      +---------------------- host -------------------+
                              |
                              +-- sdk web static serving / route registration
```

The dependency rules are:

- feature core -> SDK only;
- `ui/goth` -> templ plus its pinned build/runtime inputs, never a feature;
- feature `views/goth` -> its feature core + SDK + `ui/goth`;
- host -> selected features, selected view adapters, `ui/goth`, and SDK;
- only the host registers an asset route and chooses the public asset base URL;
- only the host/owning feature writes HTTP security headers.

### Target repository shape

```text
ui/
  README.md
  goth/
    go.mod
    go.sum
    README.md
    goth.go                     Bundle, profiles, manifest, requirements
    document.templ              document/head/script/style composition
    document_templ.go           generated
    theme/
      contract.css              semantic-variable contract and neutral defaults
      default.css               default light/dark theme values
      theme.go                  theme and document-attribute helpers
    primitives/
      primitives.go             shared Props/Slot/attribute conventions
      button.templ              one source pair per catalog entry
      button_templ.go           generated
      ...                       all 64 catalog entries
    components/
      forms/
      layouts/
      feedback/
      data/
    htmx/
      attributes.go             typed explicit hx-* attribute helpers
      response.go               response/header interpretation helpers if earned
    icons/
      icons.templ               small internal UI-control icon set only
    assets/
      src/css/
      src/js/
      dist/                     committed fingerprinted outputs
      manifest.json             committed generated manifest
      assets.go                 go:embed and fs.FS/manifest access
      THIRD_PARTY_NOTICES.md
    tools/
      package.json              exact pins only
      package-lock.json         committed
      build scripts
    internal/testkit/
    catalog.md                  generated/checked parity status projection

examples/
  goth-showcase/
    go.mod
    cmd/server/
    internal/showcase/
    e2e/
```

Generated filenames are illustrative. The foundation phase freezes the exact
public names before downstream components are written.

### Importable module first; source installer later

The v1 delivery unit is the `ui/goth` Go module with embedded compiled assets.
Applications import it and can still wrap or replace any component. A future
`gopernicus ui add <item>` command may copy themes, components, or starter
compositions into a host, but it waits until the importable APIs and file
grammar have survived the authentication/CMS adopters. The installer is not a
prerequisite for the 64-component parity gate.

## Layering vocabulary

### Primitive

A primitive is a direct GOTH interpretation of one dated Shadcn catalog entry.
It lives in the single `primitives` Go package, normally one `.templ` source
file plus a small `.go` behavior/props file. Complex catalog entries such as
Data Table, Sidebar, Chart, and Calendar remain primitives because parity—not
size—defines this layer.

### Component

A component is an opinionated, domain-neutral composition built from
primitives. Examples include an error-summary form, page action bar,
authentication shell, searchable resource table, or destructive-confirmation
workflow. Components may select defaults and arrange primitives; they must not
import a feature domain.

### Feature view adapter

A feature view adapter translates feature-owned page models into GOTH
components and implements the feature core's `Views` port. Domain knowledge
belongs here, not in `ui/goth`.

### Parity

Parity does not mean line-by-line translation of React, Base UI, or Radix
source. A primitive is complete only when the GOTH version provides the same
recognizable purpose and expected interaction while remaining honest about the
server-rendered platform. Each entry needs:

- an exported, documented templ-facing API;
- stable `data-slot`, `data-state`, and `data-variant` hooks where applicable;
- semantic HTML and a useful pre-JavaScript/no-JavaScript state;
- keyboard, focus, ARIA, RTL, reduced-motion, light, and dark behavior as
  applicable;
- a showcase specimen for all meaningful states;
- render/contract tests and real-browser tests for interactive behavior;
- an explicit CSP/runtime requirement; and
- source/provenance notes when upstream source materially informed the port.

An engine-neutral adapter is acceptable for Chart or another specialized
entry, but a styled placeholder or undocumented loss of behavior is not.

## Standing invariants

1. **Server ownership.** The server owns authoritative state and validation.
   Alpine owns local interaction state; HTMX owns explicitly requested HTML
   transitions. Neither becomes a hidden domain store.
2. **Progressive baseline.** Readable content, form submission, links, native
   controls, and server validation work without JavaScript wherever the
   component's purpose permits it. Enhancement may improve interaction without
   changing authorization or validation semantics.
3. **No implicit HTTP mutation.** `ui/goth` never registers routes, installs
   middleware, or writes response headers. It exposes assets, renderers, and
   requirements; the host composes them.
4. **No remote runtime dependency.** Production defaults use self-hosted,
   fingerprinted assets. No CDN is needed for CSS, JS, fonts, or icons.
5. **No `unsafe-eval`.** Alpine uses `@alpinejs/csp`; complex behavior lives in
   named controllers rather than clever inline expressions.
6. **No server-rendered inline script/style.** Ordinary assets are external.
   The kit emits no server-rendered `style` attribute and no inline `<style>`
   element; dynamic positioning/sizing uses `data-*` attributes + external CSS
   and controller-owned CSSOM custom-property writes only. It must not silently
   force `unsafe-inline` on every adopter.
7. **HTMX is optional.** A host can choose CSS-only, CSS+Alpine, or the full
   CSS+Alpine+HTMX profile. Importing the kit does not force an HTMX route
   grammar on a feature.
8. **Technology-neutral features.** No feature core imports `ui/goth`, templ,
   Tailwind, Alpine, or HTMX. A nil `Config.Views` continues to mean no HTML
   surface and no view/runtime dependency in an API-only host.
9. **Host-overridable theme.** Component CSS consumes semantic variables.
   Theming flows through a host stylesheet that wiring injects (via
   `Config.ThemeStylesheetPath`) as a `<link>` after the kit stylesheet; it
   replaces token values by source-order cascade without recompiling or copying
   primitive markup, and an empty path injects the kit's embedded default theme.
10. **Generated artifacts are checked.** Generated templ Go, CSS, JS, and asset
    manifests are committed and have drift gates. Generated files are never
    hand-edited.
11. **Accessibility is release behavior.** Focus order/restoration, focus trap,
    escape behavior, roving tabindex, announcements, names/descriptions, and
    reduced motion are tested contracts, not showcase polish.
12. **Honest third-party provenance.** Exact pins, lockfiles, source revision,
    license text/notices, and artifact hashes travel with vendored/bundled
    runtime code.

## Public API grammar to freeze in phase 0

### Primitive functions and props

Use one `primitives` Go package so calls read consistently and the repository
does not acquire 64 tiny packages. Compound parts use the Shadcn-style prefix:
`Dialog`, `DialogTrigger`, `DialogContent`, `DialogTitle`, and so on.

Common rules:

- exported props are value types with a useful zero value;
- variants and sizes use typed string enums with validation tests;
- content and icons use `templ.Component` slots rather than raw trusted HTML;
- `Class` appends host classes after the primitive's stable class;
- `Attributes templ.Attributes` is the escape hatch for IDs, names, ARIA,
  `data-*`, and explicit HTMX attributes;
- behavior-critical attributes owned by the primitive cannot be accidentally
  overwritten by the escape hatch; the merge order is documented and tested;
- URL-bearing props accept typed/safely rendered URLs and never smuggle
  `templ.SafeURL` conversion through a generic string helper;
- an interactive primitive either requires caller-provided stable IDs or uses
  a documented request-scoped ID facility; duplicate/global counters are not
  accepted; and
- stable CSS/data hooks are public compatibility surface even when Tailwind
  utility classes change internally.

### Bundle profiles

The intended shape is conceptually:

```go
bundle, err := goth.New(goth.Config{
    AssetBasePath: "/assets/goth",
    Profile:       goth.Full,
})
```

Profiles:

| profile | assets | intended use |
|---|---|---|
| `StylesOnly` | compiled CSS | native/static pages and email-preview-like documents |
| `Interactive` | CSS + Alpine CSP + GOTH controllers | local widgets without server fragment navigation |
| `Full` | Interactive + HTMX 2.0.10/config | applications using explicit server fragment transitions |

The bundle exposes:

- its embedded `fs.FS` and immutable asset prefix;
- a parsed manifest of logical names to fingerprinted paths/integrity hashes;
- templ head/script/style components for the selected profile;
- a deterministic browser-resource requirements value suitable for mapping
  into a host/feature CSP policy; and
- the configured public asset base path.

It does not call `http.Handle`, accept a `feature.Mount`, or mutate headers.

### Theme contract

`ui/README.md` owns the cross-implementation semantic names. `ui/goth` provides
their CSS implementation and neutral/default values. Baseline slots include:

- background/foreground, card, popover;
- primary, secondary, muted, accent, destructive and their foregrounds;
- border, input, ring;
- chart 1–5;
- sidebar surface/foreground/primary/accent/border/ring;
- success, warning, and optional tertiary roles;
- radius, shadows, typography families, motion durations/easing, density, and
  named z-index layers.

Light values live on `:root`; dark values support both `.dark` and a documented
`data-theme` form. System preference is an opt-in theme-selection policy, not a
component concern. Every semantic variable has a neutral fallback. Host themes
override variables after the kit stylesheet and do not need Tailwind source
scanning of the Go module cache.

### Interaction grammar

- Native HTML (`details`, `dialog`, form controls, popover where support and
  semantics are sufficient) is preferred before Alpine.
- Alpine controllers are named by behavior (`gothDialog`, `gothRovingFocus`,
  `gothCombobox`) and registered once in the runtime entrypoint.
- Controllers discover parts through stable data attributes and dispatch
  documented custom events; feature-specific events are forbidden.
- Shared controller families carry the hard mechanics—focus restore/trap,
  escape/outside-click, typeahead, roving tabindex, live regions—so individual
  primitives do not fork slightly different accessibility behavior.
- Alpine Focus and Collapse may be pinned when they materially reduce risk.
  Every additional plugin must earn its bundle weight and appear in notices.
- The small internal icon set covers controls such as check, chevron, close,
  and spinner. Public APIs accept caller icons as `templ.Component`; the kit
  does not bundle an entire icon library by default.

## HTMX 2.0.10 and HTMX-4-forward contract

The exact runtime is 2.0.10 until HTMX 4 is stable, selected by the repository's
normal release-age/upgrade process, and no longer beta. The following rules are
part of the UI contract now:

1. Every `hx-*` attribute is placed on the element it affects. No behavior
   relies on inherited attributes.
2. `hx-boost` is not used as an application-wide shortcut.
3. Server handlers treat HTMX headers as presentation hints, never identity,
   CSRF, authorization, or business-state evidence.
4. An absent, malformed, or unsupported HTMX request degrades to the ordinary
   full-document response where that route supports HTML.
5. Success, validation, conflict, forbidden, and error fragments are complete
   swappable regions with stable targets. HTMX 2 response handling for non-2xx
   fragments is configured explicitly; code does not rely on HTMX 4's changed
   defaults before the upgrade.
6. History restoration is correct when the browser re-fetches a full document;
   no contract assumes a particular localStorage history-cache behavior.
7. OOB swaps, custom extensions, and morphing are not baseline dependencies.
   Introduce one only with a named use case and tests.
8. Typed attribute helpers may make method/URL/target/swap/indicator/confirm
   combinations easier, but they emit ordinary explicit `hx-*` attributes and
   never conceal server behavior.

## Authentication browser-policy design

The asset restriction is removed without deleting the security posture.

### Fixed protections remain feature-owned

Authentication continues to set, on every HTML page/redirect:

- `Cache-Control: no-store`;
- `Referrer-Policy: no-referrer`;
- `X-Frame-Options: DENY`;
- `X-Content-Type-Options: nosniff`;
- CSP `base-uri 'none'`, `form-action 'self'`, and
  `frame-ancestors 'none'`; and
- all existing CSRF/origin/live-session and secret-handling behavior.

A theme or view adapter cannot turn these off.

### Resource policy becomes an explicit port

Add a technology-neutral `HTMLPolicy`/`HTMLResourcePolicy` seam to
authentication `Config`. The exact exported names are frozen in **GOTH-0.4**
(not GOTH-0.3): the authentication resource-policy surface is **out of Gate B's
scope** and gets its own **Gate C** authentication/security review. GOTH-0.3/Gate
B freeze only the `ui/goth` grammar. The behavior of the seam is settled:

- nil selects the existing asset-free CSP;
- a policy supplies validated, deterministically ordered CSP resource
  directives for script, style, image, font, connect, media, worker, and other
  required resource classes;
- the handler passes the per-render nonce in policy context;
- directive/source validation rejects control characters and invalid directive
  keys at construction rather than emitting attacker-controlled headers;
- the policy may widen resource loading but cannot remove the fixed protections
  above; and
- configuring an HTML policy while `Views` is nil is either a loud construction
  error or documented no-op; phase 0 freezes one posture and tests the complete
  construction matrix.

The GOTH authentication adapter maps `goth.Bundle.Requirements()` into that
policy. Host wiring remains explicit so a view cannot silently weaken headers:

```go
authViews, err := authgoth.New(bundle)
authentication.Config{
    Views:      authViews,
    HTMLPolicy: authViews.HTMLPolicy(),
}
```

GOTH fragment-token readers move from bespoke inline page scripts into a pinned
external named controller. The authentication feature's per-render script nonce
remains available for that controller and host additions; the default GOTH
policy needs no `unsafe-eval`, remote origins, or static inline script/style.

## Shadcn parity matrix — 64-entry frozen baseline

Status key: `[ ]` planned, `[x]` accepted. Updating Shadcn later does not add an
entry to this milestone silently; it opens a separately reviewed parity task.

### Phase 2 — presentational and native/form foundations (26)

| done | ID | primitive | GOTH strategy / acceptance emphasis |
|---|---|---|---|
| [x] | P01 | Alert | semantic status/alert roles, icon/title/description/action slots |
| [x] | P02 | Aspect Ratio | CSS aspect-ratio wrapper with content fallback |
| [x] | P03 | Avatar | image/fallback states, accessible name and load failure behavior |
| [x] | P04 | Badge | semantic variants, link/button composition without invalid nesting |
| [x] | P05 | Breadcrumb | nav/list/current-page semantics and responsive collapse slot |
| [x] | P06 | Button | native button/link forms, type safety, loading/disabled states |
| [x] | P07 | Button Group | grouped actions/inputs, orientation and joined focus rings |
| [x] | P08 | Card | header/title/description/content/footer/action slots |
| [x] | P09 | Direction | `dir` propagation helper and RTL showcase axis |
| [x] | P10 | Empty | media/title/description/content/action composition |
| [x] | P11 | Field | label/control/description/error grouping and invalid state |
| [x] | P12 | Input | complete input attributes, invalid/disabled/file/search states |
| [x] | P13 | Input Group | prefix/suffix/button/addon layout and accessible labelling |
| [x] | P14 | Item | media/content/title/description/actions and link semantics |
| [x] | P15 | Kbd | keyboard-input semantics and grouped shortcuts |
| [x] | P16 | Label | explicit control association and required/disabled states |
| [x] | P17 | Marker | status/note/row/separator variants with icon/content and link/button forms |
| [x] | P18 | Native Select | native select baseline, option/group helpers, invalid state |
| [x] | P19 | Pagination | links, current page, ellipsis and server-owned URL state |
| [x] | P20 | Progress | determinate/indeterminate semantics and reduced motion |
| [x] | P21 | Separator | decorative versus semantic separator, orientations |
| [x] | P22 | Skeleton | non-announcing decoration and reduced-motion policy |
| [x] | P23 | Spinner | named/hidden busy indicator and live-region composition |
| [x] | P24 | Table | caption/header/body/footer/row/cell and responsive wrapper |
| [x] | P25 | Textarea | native attrs, invalid/disabled/autosize enhancement boundary |
| [x] | P26 | Typography | semantic text recipes without replacing native heading levels |

### Phase 3 — disclosure and selection behavior (10)

| done | ID | primitive | GOTH strategy / acceptance emphasis |
|---|---|---|---|
| [x] | P27 | Accordion | native/disclosure baseline, single/multiple, keyboard traversal |
| [x] | P28 | Checkbox | native form submission, indeterminate state, label interaction |
| [x] | P29 | Collapsible | disclosure semantics, controlled/default state, height animation |
| [x] | P30 | Input OTP | grouped native inputs, paste/navigation, autocomplete and no secret echo |
| [x] | P31 | Radio Group | native submission, arrow navigation, orientation and disabled items |
| [x] | P32 | Slider | native range baseline, keyboard/multi-thumb policy, output semantics |
| [x] | P33 | Switch | checkbox-backed submission, switch semantics and label behavior |
| [x] | P34 | Tabs | roving focus, manual/automatic activation and URL/server composition |
| [x] | P35 | Toggle | pressed semantics, button/form boundaries and variants |
| [x] | P36 | Toggle Group | single/multiple selection, roving focus and submitted values |

### Phase 4 — overlays and navigation (12)

| done | ID | primitive | GOTH strategy / acceptance emphasis |
|---|---|---|---|
| [x] | P37 | Alert Dialog | modal focus trap/restore, labelled destructive decision |
| [x] | P38 | Context Menu | keyboard/contextmenu opening, roving/typeahead, touch fallback |
| [x] | P39 | Dialog | native dialog where honest, modal/nonmodal, escape/outside policy |
| [x] | P40 | Drawer | dialog semantics, responsive edge, drag optional and reduced motion |
| [x] | P41 | Dropdown Menu | trigger/menu/item roles, keyboard/typeahead/submenu behavior |
| [x] | P42 | Hover Card | hover/focus intent, dismissal, touch-safe noncritical content |
| [x] | P43 | Menubar | horizontal roving focus, menu traversal and escape hierarchy |
| [x] | P44 | Navigation Menu | links remain links, disclosure/viewport and mobile fallback |
| [x] | P45 | Popover | anchor/placement, focus policy, outside/escape dismissal |
| [x] | P46 | Select | native fallback, listbox/typeahead, form value and validation |
| [x] | P47 | Sheet | dialog-backed edge panel with focus and scroll-lock behavior |
| [x] | P48 | Tooltip | hover/focus delay, escape dismissal, described-by semantics |

### Phase 5 — composite data/time/application primitives (6)

| done | ID | primitive | GOTH strategy / acceptance emphasis |
|---|---|---|---|
| [x] | P49 | Calendar | locale-aware month grid, keyboard dates, min/max/disabled selection |
| [x] | P50 | Combobox | input/listbox relationship, async/server option seam, form value |
| [x] | P51 | Command | filter/active-item controller, keyboard loop and grouped results |
| [x] | P52 | Data Table | server-owned sort/filter/page model, responsive table, HTMX optional |
| [x] | P53 | Date Picker | calendar+popover+field composition, parse/format/error contract |
| [x] | P54 | Sidebar | desktop/mobile shell, collapsible state, navigation semantics |

### Phase 6 — specialized, media, and messaging primitives (10)

| done | ID | primitive | GOTH strategy / acceptance emphasis |
|---|---|---|---|
| [x] | P55 | Attachment | media/metadata, upload-state presentation, sizes/orientation, group/trigger/actions |
| [x] | P56 | Bubble | seven variants, start/end alignment, grouping, reactions, interactive content |
| [x] | P57 | Carousel | CSS scroll-snap baseline, controls/status, autoplay opt-in only |
| [x] | P58 | Chart | themed frame/legend/tooltip plus server-SVG/engine adapter seam |
| [x] | P59 | Message | aligned row with avatar, content, header/footer and grouped-message slots |
| [x] | P60 | Message Scroller | anchored transcripts, live-edge following, history loading and message jumps |
| [x] | P61 | Resizable | pointer+keyboard separators, bounds, persisted state opt-in |
| [x] | P62 | Scroll Area | native scrolling baseline, affordance enhancement and keyboard access |
| [x] | P63 | Sonner | opinionated toast queue API over shared live-region runtime |
| [x] | P64 | Toast | composable toast parts, timing/pause/dismiss and live-region priority |

## Toolchain and asset policy

### Build-time dependencies

Use a repository-local Node toolchain only to author and verify distributable
assets. Consumers of the committed Go module need Go and the embedded outputs,
not Node, npm, or a bundler.

- `ui/goth/tools/package.json` uses exact versions—no caret/tilde ranges.
- `package-lock.json` is committed and CI uses `npm ci`.
- The JS bundler, Alpine CSP/plugins, HTMX 2.0.10, browser test tools,
  and accessibility tooling are pinned and pass the repository's release-age
  and license review before landing.
- Install scripts are audited/allowlisted; dependency updates are deliberate
  batches with regenerated assets and browser proof.
- Runtime dependencies and copied upstream source carry license/provenance and
  SHA-256 records in `THIRD_PARTY_NOTICES.md`.

### Outputs

Build separate fingerprinted artifacts so bundle profiles remain honest:

- one compiled theme/component CSS asset;
- one Alpine CSP + GOTH controller asset;
- one HTMX 2.0.10 + Gopernicus response-configuration asset; and
- optional small assets only when a primitive earns them.

The manifest maps logical names to hashed paths and integrity digests. The Go
package embeds `assets/dist` and the manifest. `make generate` regenerates templ
and UI outputs; `make check` proves no drift. A dedicated `make test-ui-browser`
owns the installed-browser leg so the ordinary Go module loop remains readable,
while the UI milestone/release gate requires both.

## Verification strategy

### Layer 1 — render and API contracts

- Go tests render each state to HTML and assert semantics, names, relationships,
  required data hooks, safe escaping, attribute merge rules, and no duplicate IDs.
- Public API examples compile.
- A generated catalog assertion fails if any of P01–P64 lacks an implementation,
  showcase registration, or required test classification.

### Layer 2 — assets, theme, CSP, and generation

- Manifest entries exist, hashes match bytes, and fingerprinted assets get the
  expected immutable-cache behavior through `sdk/foundation/web` in the showcase.
- Light, dark, RTL, reduced-motion, and host override themes are all exercised.
- The strict bundle runs without CDN traffic, `unsafe-eval`, or console CSP errors.
- A test asserts no primitive emits a server-rendered `style` attribute or an
  inline `<style>` element and that none widens the CSP; no hidden CSP
  requirement is accepted.
- Templ/CSS/JS/manifest generation is a clean no-op in `make check`.

### Layer 3 — real-browser behavior

The showcase is driven in Chromium, Firefox, and WebKit through an exact-pinned
Playwright harness. Tests cover keyboard-only journeys, focus trap/restoration,
escape/outside dismissal, roving focus/typeahead, native form submission, HTMX
success/validation/error swaps, history re-fetch, live-region announcements,
reduced motion, dark mode, and RTL. Automated axe checks run on every showcase
section, supplemented by explicit behavior assertions; an axe green result is
not a substitute for interaction testing.

### Layer 4 — run-and-look

Each primitive wave closes with a human visual pass at narrow/wide viewports in
light/dark and at least one RTL specimen. Curated screenshots are retained as
review artifacts. Pixel-golden enforcement is introduced only if it proves
stable across the pinned browser/font environment; semantic and behavioral
tests remain authoritative.

### Layer 5 — adopter proof

- Authentication pages load their declared GOTH assets under the configured
  CSP with no regression to secrets, CSRF, origin checks, no-store, or JSON API.
- CMS public/admin forms and tables prove compositions and optional HTMX
  fragments while API-only wiring still excludes view technology.
- Root `make check`, `make guard`, the UI browser leg, and feature-specific
  adversarial tests all pass before closeout.

## Scope

### In

- top-level `ui/` architecture and shared semantic contract;
- the `ui/goth` Go module, build tooling, assets, themes, primitives,
  components, HTMX helpers, tests, and docs;
- a zero-datastore showcase example and browser harness;
- root workspace, Makefile, generation/drift, guard, release, and module-count
  updates;
- authentication's technology-neutral HTML resource-policy seam;
- GOTH adapters/adoption for authentication and CMS; and
- a handoff note for Segovia/GPS360 adopters.

### Out

- implementing `ui/react` or `ui/vue`;
- editing Segovia or GPS360 from this repository milestone;
- a Figma library or brand redesign;
- bundling every Lucide icon or web font;
- turning HTMX headers into feature/domain contracts;
- generic authorization administration UI;
- adopting HTMX 4 before its stable/release-age gate;
- requiring a database for the showcase;
- a public component registry service; and
- `gopernicus ui add` implementation before the importable APIs stabilize.

## Preflight gate

Before GOTH-0.1:

1. Record branch/HEAD/worktree and preserve unrelated changes.
2. Re-run `git tag --list` for the root-adjacent/new module and both existing
   feature view modules. Current draft-time result is no tags. If a relevant
   view-module tag appears, do not remove/rename it; use a new `views/goth`
   module plus a compatibility/deprecation plan.
3. Run and retain `make check && make guard`.
4. Re-read `ARCHITECTURE.md`, `features/README.md`, `RELEASING.md`, both feature
   view modules, authentication HTML security code/tests, and the static-file
   server contract.
5. Capture the official Shadcn source commit/revision corresponding to the
   2026-07-17 catalog and inventory applicable licenses. The catalog names are
   frozen by this plan; code provenance still needs a precise revision.
6. Select exact, sufficiently aged Tailwind/Alpine/plugin/bundler/Playwright/axe
   versions; retain HTMX at exact 2.0.10. Record checksums and licenses before
   generated output is committed.
7. Freeze the public primitive props/attribute/ID grammar, bundle profile API,
   semantic token list, and authentication policy type. No 64-way
   implementation proceeds while those foundations are still moving.

## Tasks

### Phase 0 — freeze architecture and contracts

#### GOTH-0.1 — record preflight, catalog, and provenance

- **depends_on:** preflight
- **model:** fable
- **files:**
  - `.claude/plans/ui-goth/plan.md`
  - `ui/README.md`
  - `ui/goth/catalog.md`
  - `ui/goth/assets/THIRD_PARTY_NOTICES.md`
- **work:** Record the source revision, exact 64-entry mapping, licenses,
  dependency candidates, tag posture, and baseline verification transcript.
  Do not copy upstream implementation before provenance is settled.
- **verify:** catalog count is exactly 64; every P01–P64 appears once; baseline
  `make check && make guard` remains green.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Gate A ratified in
  full (owner, 2026-07-17), GOTH-0.1's only dependency (preflight + Gate A).
  Preflight recorded: branch `authorization-v3`, HEAD `a5d1a4a3`, `git tag --list`
  empty (no module tags — the `views/templ` → `views/goth` rename path stays
  open), worktree clean apart from the untracked `.claude/plans/ui-goth/` plan.
  Files created (documentation only — no Go files placed outside a module, no
  `go.work`/`Makefile` change): `ui/README.md` (UI-family charter + semantic-token
  role vocabulary, freeze deferred to GOTH-0.3), `ui/goth/catalog.md` (frozen
  64-entry P01–P64 parity projection), `ui/goth/assets/THIRD_PARTY_NOTICES.md`
  (provenance + license inventory + candidate pins). Provenance captured live:
  Shadcn catalog `shadcn-ui/ui` revision `d28738b183c5eaa69d8d540826e450f30d39ab6c`
  (main HEAD, committed 2026-07-17T15:40:59Z, MIT); no upstream code copied.
  License inventory verified 2026-07-17: htmx 0BSD (exact 2.0.10, owner-fixed),
  Alpine/@alpinejs plugins MIT, Tailwind MIT, a-h/templ MIT (`v0.3.1020`),
  esbuild MIT, Playwright Apache-2.0, axe-core MPL-2.0; exact non-HTMX version
  pins + checksums deferred to GOTH-1.2 with the committed lockfile. Verify
  results: catalog invariant proven mechanically — 64 table rows, 64 unique IDs,
  no duplicates, none of P01–P64 missing. `make guard` exit 0 and `make check`
  exit 0 both before and after the doc additions ("all checks passed").

#### GOTH-0.2 — amend the module taxonomy and dependency guards

- **depends_on:** GOTH-0.1
- **model:** opus
- **files:**
  - `ARCHITECTURE.md`
  - `README.md`
  - `RELEASING.md`
  - `Makefile`
- **work:** Add the UI-implementation module kind, dependency arrows, swap
  unit, release/tag shape, and guards preventing `ui/goth` from importing
  features, integrations, examples, or Workshop. Clarify how the reusable kit
  relates to app-local `internal/inbound/views`.
- **verify:** `make guard`; guard fixtures prove an illegal outward import is
  rejected without misclassifying legal templ/runtime dependencies.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Gate A ratified in
  full (owner, 2026-07-17) and GOTH-0.1 complete with recorded evidence — GOTH-0.2
  was dependency-ready. Branch `authorization-v3`, HEAD `a5d1a4a`, `git tag --list`
  empty; unrelated worktree (untracked `.claude/plans/ui-goth/` and the GOTH-0.1
  `ui/` docs) left untouched. Baseline `make guard` exit 0 before edits.
  Files changed (four, as scoped — no Go/module/`go.work` change): `Makefile` adds
  the seventeenth guard `guard-ui-no-inward` (G17) — grep-based, mirrors G13's
  outward-only discipline: rejects any `ui/` `.go` importing
  `github.com/gopernicus/gopernicus/(features|integrations|examples|workshop)`,
  wired into `.PHONY` + the `guard` aggregate, comment bumped "sixteen"→
  "seventeen". `ARCHITECTURE.md` amends the taxonomy intro "Six kinds"→"Seven kinds"
  (dated amendment note), adds the **UI implementation** row (definition, sdk+view/
  runtime-only dependency, guard G17, example `ui/goth`, swap unit), a
  dependency-arrows block, and a `ui/` vs app-local `internal/inbound/views/`
  clarification (reusable/importable theme root vs host-private escape hatch); the
  two Inbound-anatomy "future UI kit" mentions were disambiguated to "host's private
  kit / reusable ui/". `README.md` adds a UI-implementation rule bullet and corrects
  the guard count (the stale "thirteen" → the accurate "seventeen"; actual count was
  16 pre-change). `RELEASING.md` adds `ui/goth/v0.1.0` to the tag-shape example plus
  a UI-implementation tagging note (importable, independently tagged, requires its
  view/runtime libs + sdk only; `ui/goth` module lands GOTH-1.1, no `ui/*` tag cut
  this milestone). Verify results: `make guard` exit 0 with 17 guard sections after
  the edits. Guard fixtures (transient, created then removed — `ui/goth` left as
  `assets/` + `catalog.md`): a legal `ui/goth/*.go` importing `github.com/a-h/templ`
  + `sdk/foundation/web` PASSES (exit 0 — legal templ/runtime/sdk deps not
  misclassified); an illegal feature import (`features/cms`) and an illegal
  integration import (`integrations/datastores/turso`) each FAIL loudly with the G17
  error (exit 2); post-cleanup `guard-ui-no-inward` exit 0.

#### GOTH-0.3 — freeze API, theme, runtime, and HTMX contracts

- **depends_on:** GOTH-0.1
- **model:** opus
- **files:**
  - `ui/README.md`
  - `ui/goth/README.md`
  - `ui/goth/catalog.md`
- **work:** Freeze the exact proposed signatures and behavioral contracts for
  props, slots, attributes, IDs, data hooks, bundle profiles, manifest, browser
  requirements, theme tokens, Alpine controller rules, and explicit HTMX
  attributes. This is the design gate; the module and compile specimens land
  in GOTH-1.1 rather than placing Go files outside a module.
- **verify:** every proposed public type has zero-value/error/ownership rules;
  every P01–P64 entry maps to the grammar without an unresolved API family.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Gate A ratified in
  full (owner, 2026-07-17); GOTH-0.1 complete with recorded evidence; GOTH-0.3
  depends only on GOTH-0.1 — dependency-ready. Gate B (owner/API review of these
  frozen contracts) is the NEXT gate and comes before GOTH-1.1; this task produces
  the freeze artifacts for that review and does not cross Gate B. Branch
  `authorization-v3`, HEAD `a5d1a4a`, `git tag --list` empty; unrelated worktree
  (untracked `.claude/plans/ui-goth/`, the GOTH-0.1 `ui/` docs, and the GOTH-0.2
  `ARCHITECTURE.md`/`Makefile`/`README.md`/`RELEASING.md` edits) left untouched.
  Files changed (three, as scoped — documentation only, no Go/module/`go.work`/
  `Makefile` change): created `ui/goth/README.md` — the design-gate freeze:
  package/import layout (one `primitives` package, compound prefixes, guard-G17
  outward-only rule); `goth.Bundle`/`Config`/`Profile` (StylesOnly zero value,
  additive Interactive/Full supersets, `New` validation + error rules,
  `DefaultTheme`/`DefaultAssetBasePath`); `Manifest`/`Asset`/`assets.FS`/
  `ManifestBytes`; `Requirements`/`Directive` (self-hosted, no `unsafe-eval`, no
  blanket `unsafe-inline`, nonce only when the dynamic stylesheet is active); the
  full frozen theme token set (`--<token>` list, light/`.dark`/`[data-theme]`,
  neutral fallbacks, `NewTheme` validation, `Appearance`/`Direction` helpers);
  `Document`/`DocumentOptions`; the primitive grammar (`Base` ID/Class/Attributes,
  typed variant/size enums, `URL`/`ParseURL`, `IDFactory`/`NewIDFactory`,
  `MergeAttributes` documented merge order); Alpine controller rules (`goth`-
  prefixed, `@alpinejs/csp`, documented events, shared mechanics families); the
  explicit HTMX 2.0.10 grammar (`htmx.Attrs`/`Build`, `Method`/`Swap` enums,
  `Request` presentation-hints-only reader); and an ownership summary. Every
  proposed public type carries a zero-value / error-behavior / ownership row.
  Updated `ui/goth/catalog.md` — added the frozen `family` column mapping every
  P01–P64 entry to exactly one API family (F1 leaf props, F2 slotted props, F3
  compound parts, F4 controller-backed), the family-definition preamble, and the
  count line; updated the status header to record the GOTH-0.3 augmentation.
  Updated `ui/README.md` — flipped the two token/API-freeze deferral sentences
  from "frozen in GOTH-0.3" (future) to "frozen in GOTH-0.3 (2026-07-17)" pointing
  at `goth/README.md` §5 for the authoritative token set. Verify results: the two
  acceptance criteria met — (1) every proposed public type states zero-value,
  error, and ownership rules (per-section ownership tables in `goth/README.md`);
  (2) every P01–P64 entry maps to exactly one API family with none unresolved.
  Catalog invariants proven mechanically: 64 `P..` rows, 64 unique IDs, zero
  duplicates, none of P01–P64 missing, family tallies F1=11/F2=6/F3=14/F4=33
  (=64) matching the frozen summary in both docs. Baseline gates green after the
  doc additions: `make guard` exit 0 and `make check` exit 0 ("all checks
  passed"). Blocker: none. Gate B is the required owner/API review before
  GOTH-1.1 and is an owner decision gate, not an implementer task.
- **evidence — Gate B remediation (2026-07-17):** Gate B was accepted by the owner
  on 2026-07-17 conditional on applying the binding remediation items R1–R9 from
  `.claude/plans/ui-goth/gate-b-review.md` (five-reviewer wave, 5/5
  accept-with-changes) to the freeze artifacts. All nine applied, documentation
  only — no Go/templ/module file created (none exist under `ui/` yet):
  R1 asset recipe — `assets.FS` embed retains the `dist/` segment, served with
  `web.WithAssetPrefix("dist/")`; the `Asset.Path` example and host-join rule now
  read `dist/<hashed>` so immutable caching actually matches. R2 sdk clause —
  removed the open-ended "(where a helper needs it) sdk"; the frozen surface
  imports no sdk package and any sdk-importing addition reopens GOTH-0.3. R3 nonce
  coherence — §2 and §4 now agree a nonce is dynamic-stylesheet-only (scripts are
  external/SRI, unnonced); added frozen invariants (a) no primitive emits inline
  `style=` and (b) one nonce provider per render feeds both `Config.Nonce` and the
  CSP header. R4 toolchain — froze Node pinning (`.nvmrc`/`engines` +
  `package-lock.json`) with a Node-gated regeneration target and a plain-git
  `dist/` drift check in `make check`. R5 theme — partial themes compose OVER
  `DefaultTheme` (owner default); added the `overlay` scrim token; resolved package
  identity (`goth.Theme` is a re-export alias of `theme.Theme`, zero value the only
  unvalidated path); froze `shadow-lg` as the elevation ceiling and the `density`
  padding/gap derivation. R6 family grammar — added the F1/F2 and F2/F3 decision
  rule to §7 and the catalog preamble, re-verified P01/P04/P08/P10/P14/P17 (no
  reclassification; counts unchanged at F1=11/F2=6/F3=14/F4=33); froze Toggle P35
  checkbox-backed-in-form F4 with the Switch/Toggle asymmetry rationale; Button P06
  kept F2 with the one-function-with-optional-`URL` shape (inferred from the
  required URL+`type=submit`/`disabled` cross-field guard — flagged for owner
  confirmation); froze the F2 principal-children channel, the single-spread
  `MergeAttributes` emission rule with `class`-in-`Attributes` rejected, ARIA
  linkage via caller-passed `Base.ID` per part (no new context key), a required
  accessible-name field for icon-only interactive primitives, and `x-data="goth…"`
  in the emitted surface. R7 HTMX — reframed §9 to PROVISIONAL per owner:
  principles/package/`Method`+`Swap` enums/`Request` reader/`Build`→`Base.Attributes`
  merge rule frozen, the exact `Attrs` field set provisional until GOTH-5.3/7.3 with
  candidate gaps (`Vals`/`Include`/`Headers`/`DisabledElt`/swap modifiers/typed-URL)
  and the CSRF-posture question recorded. R8 — added an assembled host wiring
  specimen to §10 using only frozen names. R9 — `ui/README.md` seventh-kind/GOTH-0.2
  passages now point at the ratified ARCHITECTURE.md UI implementation row and guard
  G17; plan.md's auth-policy "frozen in GOTH-0.3" corrected to GOTH-0.4 and recorded
  as out of Gate B scope (its own Gate C review). Catalog invariants re-proven: 64
  `P..` rows, 64 unique IDs, zero duplicates, none of P01–P64 missing, tallies
  F1=11/F2=6/F3=14/F4=33 (=64) matching both docs. Internal consistency verified:
  §2/§4 nonce statements agree; no "(where a helper needs it) sdk" clause remains;
  the `overlay` token is present; Theme identity is unambiguous (alias); the R8
  specimen uses only frozen names. Gates green: `make guard` exit 0, `make check`
  exit 0. Flagged for owner: the Button one-function-vs-two-functions shape
  (inferred one function from R6's cross-field-guard requirement; owner may still
  choose two).

#### GOTH-0.4 — freeze authentication's HTML resource-policy seam

- **depends_on:** GOTH-0.3
- **model:** opus
- **files:**
  - `features/authentication/authentication.go`
  - `features/authentication/internal/inbound/authentication/security.go`
  - authentication construction/security tests
  - `features/authentication/README.md`
- **work:** Add the technology-neutral, validated resource-policy port while
  preserving all fixed auth headers and the nil asset-free policy. Test the
  Config matrix and malicious directive/source rejection. Do not import
  `ui/goth` or templ into the feature core.
- **verify:** `cd features/authentication && go test ./... && go vet ./...`;
  `make guard`; exact header tests prove fixed protections cannot be removed.
- **evidence — READY FOR GATE C REVIEW (2026-07-17):** Implementation complete and
  green; the TASKS.md box stays UNCHECKED pending the Gate C
  authentication/security review (GOTH-0.4 depends on GOTH-0.3 + Gate C, and Gate C
  must be recorded before this task is marked done). Dependency GOTH-0.3 + Gate B are
  complete/recorded. Branch `authorization-v3`; unrelated worktree (authorization-v3
  uncommitted work, the untracked `.claude/plans/ui-goth/` and `ui/` GOTH-0.1..0.3
  outputs) left intact; the authentication feature's existing behavior and diffs are
  preserved — every change is additive.
  **Frozen seam (exact public names).** New feature-owned, technology-neutral surface
  on the authentication public package (`features/authentication/authentication.go`,
  re-exporting from the internal inbound package per the `MutationSecurity`
  precedent — the feature core imports NO templ/Tailwind/Alpine/HTMX/`ui/goth`):
  - `type HTMLResourcePolicy` — opaque, validated, immutable, deterministically
    ordered set of ADDITIONAL CSP resource directives held by `Config.HTMLPolicy
    *HTMLResourcePolicy`.
  - `type HTMLResourceDirective struct { Kind HTMLResourceKind; Sources []string;
    Nonce bool }` — the caller-facing directive input. `Nonce` is the ONLY channel to
    the per-render CSP nonce (`'nonce-<value>'`); a caller never formats the nonce
    into `Sources`.
  - `type HTMLResourceKind string` + the frozen widenable-class allowlist constants
    `HTMLScriptSrc`/`HTMLStyleSrc`/`HTMLImgSrc`/`HTMLFontSrc`/`HTMLConnectSrc`/
    `HTMLMediaSrc`/`HTMLWorkerSrc` (each the literal CSP directive name). The fixed
    protection directives (`default-src`, `base-uri`, `form-action`,
    `frame-ancestors`) are deliberately NOT members, so a policy STRUCTURALLY cannot
    name, relax, or drop them.
  - `func NewHTMLResourcePolicy(...HTMLResourceDirective) (*HTMLResourcePolicy,
    error)` — validates LOUDLY at construction (errors wrap `sdk.ErrInvalidInput`):
    unknown/fixed directive key, a directive with neither a source nor a nonce, an
    empty source, or a source carrying a control character, whitespace, `;`, or `,`
    (the header-injection guard). Duplicate kinds merge, duplicate sources dedup,
    output ordered by directive name (byte-stable header).
  - `Config.HTMLPolicy *HTMLResourcePolicy` + `var ErrHTMLPolicyWithoutViews`.
  **Behavior frozen.** `nil` policy reproduces the historical asset-free CSP
  BYTE-FOR-BYTE (`default-src 'none'; base-uri 'none'; form-action 'self';
  frame-ancestors 'none'; script-src 'nonce-…'|'none'`). A non-nil policy appends its
  validated widening directives after the immutable `fixedCSPPrefix`. The four fixed
  headers (`Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
  `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`) plus the fixed CSP
  prefix are feature-owned and unremovable. **Construction matrix (frozen posture:
  loud, not silent no-op):** Views nil + policy nil → OK (API-only, asset-free); Views
  set + policy nil → OK (asset-free CSP, current behavior); Views set + policy set →
  OK (widened); **Views nil + policy set → `ErrHTMLPolicyWithoutViews`** at
  construction (a policy for an absent HTML surface is contradictory wiring — the
  `ErrInviteCheckWithoutGranter` posture).
  **Files changed.** `features/authentication/authentication.go` (public aliases +
  constructor + `ErrHTMLPolicyWithoutViews` + `Config.HTMLPolicy` field +
  construction check + `Service.htmlPolicy` threaded through `Register`→`Mount`);
  `features/authentication/internal/inbound/authentication/policy.go` (NEW — the
  type, validation, CSP rendering); `.../security.go` (`writeHTMLSecurity` now takes
  the policy; `fixedCSPPrefix`/`buildCSP` added, nil path byte-identical);
  `.../routes.go` (`handlers.htmlPolicy` + `Mount` 8th param); `.../html.go` +
  `.../forms.go` (the four `writeHTMLSecurity` call sites pass `h.htmlPolicy`);
  `.../policy_test.go` (NEW), `features/authentication/html_policy_test.go` (NEW),
  plus mechanical trailing-`nil` updates to the in-module `Mount(...)` test call
  sites and a non-breaking `newHTMLTestHandlerWithPolicy` harness delegation;
  `features/authentication/README.md` (HTML-surface section + Config table).
  **Verify results (all green).** `cd features/authentication && go test ./...` PASS
  and `go vet ./...` PASS; `make guard` exit 0 (all 17 guards, incl. G17 ui-no-inward
  and the feature-core-imports guard — no new outward import); `make build` exit 0
  (whole workspace still compiles; the seam is additive). Named proofs:
  `TestBuildCSPNilPreservesAssetFreePosture` + `TestWriteHTMLSecurityNilPolicyHeaders`
  (nil provably preserves the exact asset-free posture),
  `TestNewHTMLResourcePolicyRejectsFixedAndUnknownKinds` (default-src/base-uri/
  form-action/frame-ancestors + unknown classes rejected),
  `TestNewHTMLResourcePolicyRejectsMaliciousSources` (control chars, `;`, `,`, space,
  tab, CR, LF, NUL, DEL all rejected), `TestNewHTMLResourcePolicyRequiresSourceOrNonce`,
  `TestNewHTMLResourcePolicyMergesAndDedups`, `TestNewHTMLResourcePolicyValidWidening`
  + `TestBuildCSPPolicyNonceFailSafe` (deterministic order, nonce-only channel,
  fail-safe `'none'`), `TestHTMLPolicyWidensLiveResponse` (end-to-end GET: policy
  widens the CSP while every fixed header + fixed CSP directive persists),
  `TestHTMLPolicyConstructionMatrix` + `TestNewHTMLResourcePolicyPublicConstructorValidates`.
  **What Gate C reviewers must examine:** (1) the public resource-policy type +
  validation rules + Config construction matrix (loud `ErrHTMLPolicyWithoutViews`
  posture vs a documented no-op — confirm the loud choice); (2) the immutable fixed
  headers + `fixedCSPPrefix` and that the widenable allowlist excludes every fixed
  directive; (3) the nil-policy byte-for-byte preservation proof; (4) no feature-core
  dependency on templ or `ui/goth` (guard-clean). **Owner/reviewer call to confirm at
  Gate C:** the frozen widenable-class set is exactly the seven the plan enumerated
  (script/style/image/font/connect/media/worker); adding a class (e.g. `frame-src`,
  `child-src`, `object-src`) is new public surface that reopens this freeze. **Not
  done here (correctly out of scope):** the `ui/goth` authentication adapter's
  `HTMLPolicy()` mapping of `goth.Bundle.Requirements()` and moving the bundled inline
  fragment readers to an external controller both land in GOTH-7.2 (post-Gate-C).
- **evidence — GATE C ACCEPTED + REMEDIATION APPLIED (2026-07-17):** GOTH-0.4 is now
  COMPLETE. Gate C — the authentication/security review of the HTML resource-policy
  seam — was accepted by the owner on 2026-07-17 after a three-reviewer wave
  (lead-backend-engineer accept; architecture-steward accept-with-changes; platform-sre
  accept-with-changes; no blockers), recorded in `.claude/plans/ui-goth/gate-c-review.md`.
  Owner dispositions ratified: (1) Gate C accepted conditional on the doc/release-note
  remediation; (2) **seven-class widenable freeze ratified** — exactly
  script/style/img/font/connect/media/worker; `frame-src`/`child-src`/`object-src` stay
  excluded (they fall back to `default-src 'none'`) and adding any class later is a
  recorded freeze-reopen, not a silent edit; (3) **unsafe-source posture is
  document-only** — the seam stays value-neutral, source-value safety
  (`'unsafe-inline'`/`'unsafe-eval'`/`*`) is the host/adapter's responsibility stated in
  docs, with no RuntimeMode enforcement. Adjudication recorded and preserved: the
  linter's "unused method `render`" finding (policy.go) is a FALSE POSITIVE — `render` is
  the live non-nil rendering path (`buildCSP` → `writeHTMLSecurity`, four handler call
  sites) — so NO code changed. Binding remediation C1–C5 applied, **documentation /
  release-note only, zero code changes**: C1+C2 → `features/authentication/README.md`
  HTML-surface section (a non-nil policy replaces the default `script-src` tail entirely
  and is fail-closed on scripts unless it carries `HTMLScriptSrc` with `Nonce: true`;
  widening is unbounded by design — structure-validated, value-neutral). C3 →
  `policy.go` `HTMLResourcePolicy` doc comment (source order within a directive is
  preserved/dedup-first and load-bearing for cross-process header stability; the
  GOTH-7.2 adapter must emit sources deterministically). C4 → `RELEASING.md` (new
  adopter-facing additive HTML-policy upgrade note folding into the auth-v3 breaking
  cut; stale "patch-only (internal delegation)" note updated). C5 → this plan's GOTH-7.2
  block (unbudgeted contract test: a policy keeping the bundled inline readers must
  carry `HTMLScriptSrc`+`Nonce: true`; adapter owns deterministic source ordering and
  externalizing the fragment readers). Verify results (docs prove no code regression):
  `cd features/authentication && go test ./...` PASS and `go vet ./...` PASS (exit 0);
  `make guard` exit 0 (all 17 guards, incl. G17 ui-no-inward and the feature-core
  delivery/import guards — no new outward import). Phase 0 is now closed: GOTH-0.1–0.4
  green, Gate B and Gate C both recorded. Next executable task: GOTH-1.1.

### Phase 1 — build the GOTH foundation and showcase

#### GOTH-1.1 — create the module, workspace, and generated-source wiring

- **depends_on:** GOTH-0.2, GOTH-0.3
- **model:** opus
- **files:**
  - `ui/goth/go.mod`
  - `ui/goth/go.sum`
  - `go.work`
  - `Makefile`
  - `.gitignore`
  - `ui/goth/goth.go`
  - `ui/goth/primitives/primitives.go`
  - `ui/goth/theme/*`
  - `ui/goth/htmx/*`
  - contract tests
  - initial `.templ` sources and generated counterparts
- **work:** Add `ui/goth` to the workspace/module loop, pin templ, establish
  generation targets, implement the phase-0 contract types with small compile/
  render specimens, and extend templ drift checks without hand-editing generated
  files.
- **verify:** `make generate`; `cd ui/goth && go build ./... && go test ./... && go vet ./...`;
  API examples compile; invalid construction is table-tested; a second
  generation is a no-op.
- **evidence (2026-07-17):** Completed. Dependencies confirmed: GOTH-0.2 (taxonomy +
  guard G17) and GOTH-0.3 (frozen `ui/goth` contract) complete with recorded
  evidence; Gate B accepted + remediation applied. Branch `authorization-v3`, HEAD
  `a5d1a4a`; `git tag --list` empty; all unrelated worktree state preserved
  (authorization-v3 uncommitted work, GOTH-0.1–0.4 Phase 0 outputs). Implemented the
  frozen GOTH-0.3 surface EXACTLY (no reopen). **Module + wiring:** created
  `ui/goth/go.mod` (module `github.com/gopernicus/gopernicus/ui/goth`, single direct
  require `github.com/a-h/templ v0.3.1020` + `tool` directive, indirect block, NO
  sdk/feature/integration dep) and `ui/goth/go.sum`; added `./ui/goth` to `go.work`;
  added `ui/goth` to the Makefile `MODULES` loop and a third `cd ui/goth && go tool
  templ generate` line to the `generate` target. **Contract types (frozen names):**
  `goth.go` — `Profile` (StylesOnly zero value/Interactive/Full), `Config`, opaque
  immutable `Bundle` + `New` (validates AssetBasePath scheme/host/`..`/query/fragment/
  control, unknown Profile, malformed manifest; never returns a Bundle alongside an
  error), `Profile()/AssetBasePath()/Manifest()/Requirements()/Theme()/Head()/
  Document()`, `Asset`/`Manifest` (`Lookup`/`Assets`, sorted, dup-rejecting parser),
  `Directive` seven-const set + `Requirements` (`Sources`/`Directives`/`RequiresNonce`),
  `DocumentOptions`, `DefaultAssetBasePath`, `Theme = theme.Theme` re-export alias,
  `DefaultTheme`. `theme/theme.go` — canonical `Theme` (zero value = neutral, only
  unvalidated path), `Token` + the 59 frozen token constants, `NewTheme` (composes
  over `DefaultTheme`, rejects unknown tokens/unsafe values), `DefaultTheme`,
  `Appearance`/`Direction` + `HTMLAttributes` (dir + data-theme; system omits
  data-theme). `primitives/primitives.go` — `Base` (ID/Class/Attributes),
  `MergeAttributes` (owned wins, single-spread, caller `class` dropped), `URL`/
  `ParseURL` (scheme allowlist http/https/mailto/tel + relative; rejects
  javascript:/data:/scheme-relative/control), `IDFactory`/`NewIDFactory`
  (request-scoped, no global counter). `htmx/attributes.go`+`response.go` — `Attrs`
  (PROVISIONAL field set) + `Build` (errors on method-without-URL/bad verb/bad swap/
  control chars, no partial emit), `Method`/`Swap` enums + `Valid`, `Request`/
  `FromRequest` (HX-* headers as presentation hints only). **Render specimen +
  generation:** `document.templ` (`documentShell` + `headTags`) → generated
  `document_templ.go` via `go tool templ generate`; the shell emits no inline
  script/style, and `Head()` with the empty GOTH-1.1 placeholder manifest renders
  nothing (real asset/nonce head-link rendering is GOTH-1.3 scope, manifest embed is
  GOTH-1.2 scope — `manifestSource` is a documented placeholder swapped for
  `assets.ManifestBytes` in 1.2). **Guards (folded per gate records):** Makefile G18
  `guard-ui-require-whitelist` (G5 analogue: ui/*/go.mod requires ⊆ {templ, sdk}, and
  no `sdk/feature` import) added to `.PHONY` + the `guard` aggregate; G2
  `guard-feature-isolation` regex extended with `|ui` for grep-level symmetry
  (feature cores never import `ui/`); comment count bumped "seventeen"→"eighteen". No
  `.gitignore` change was needed (GOTH-1.1 produces only committed sources +
  generated Go; the Node tooling/`node_modules` ignore lands with the GOTH-1.2 asset
  pipeline). **Verify results (all green):** `make generate` exit 0 with zero drift in
  the two pre-existing feature `views/templ` modules; a second `make generate` is a
  byte-identical no-op (`document_templ.go` shasum stable across two runs);
  `cd ui/goth && go build ./... && go test ./... && go vet ./...` all exit 0 (4
  packages: goth, theme, primitives, htmx). API examples compile (`ExampleNew`,
  `ExampleNewTheme`). Invalid construction is table-tested: `TestNewAssetBasePath`
  (10 cases incl. absolute-URL/host/`..`/query/fragment/control rejection),
  `TestNewUnknownProfile`, `TestParseManifest` (malformed/missing-name/duplicate),
  `TestNewThemeRejectsUnknownToken`/`RejectsUnsafeValues`, `TestParseURL` (10 cases),
  `TestAttrsBuildInvalid` (5 cases). `make guard` exit 0 across all EIGHTEEN guards.
  Guard fixtures proved rejection (transient, created then reverted; go.mod restored):
  an illegal extra require FAILS G18; a `ui/` `sdk/feature` import FAILS G18; a `ui/`
  feature-core import FAILS G17; a clean tree PASSES both. Blocker: none. Owner
  decisions carried forward unchanged (not reopened): Button one-function-with-URL
  shape still flagged for owner in GOTH-0.3; HTMX `Attrs` field set remains
  PROVISIONAL (frozen `Method`/`Swap`/`Request`/`Build` only). Follow-ups correctly
  deferred: real asset pipeline + `assets` embed package (GOTH-1.2), real theme
  palette + `contract.css`/`default.css` + head-link/nonce rendering (GOTH-1.3),
  Alpine/HTMX runtime assets (GOTH-1.4), showcase (GOTH-1.5).

#### GOTH-1.2 — implement the exact-pinned asset pipeline

- **depends_on:** GOTH-1.1
- **model:** opus
- **files:**
  - `ui/goth/tools/package.json`
  - `ui/goth/tools/package-lock.json`
  - `ui/goth/tools/*`
  - `ui/goth/assets/src/**/*`
  - `ui/goth/assets/dist/**/*`
  - `ui/goth/assets/manifest.json`
  - `ui/goth/assets/assets.go`
  - `Makefile`
- **work:** Build separate CSS, Alpine/GOTH, and HTMX artifacts; fingerprint
  them, generate integrity metadata, embed them, and add install/generate/drift
  targets. Keep all runtime assets self-hosted and production-minified while
  retaining useful source maps only if their exposure policy is documented.
- **verify:** clean `npm ci` plus asset build; manifest/hash tests; `make check`
  fails on intentional source/output drift and passes after regeneration.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-1.1 complete
  with recorded evidence (module + frozen contract types + generation wiring).
  Branch `authorization-v3`; all unrelated worktree state preserved (authorization-v3
  uncommitted work, Phase 0 + GOTH-1.1 outputs). Started fresh — an earlier
  interrupted run left NO artifacts in the worktree (`ui/goth/assets` held only the
  GOTH-0.1 `THIRD_PARTY_NOTICES.md`; no `tools/`, `assets/src`, `assets/dist`,
  `assets.go`, lockfile, or Makefile asset stanza existed), so nothing was inherited
  and nothing had to be reconciled/removed. **Toolchain (exact pins, no ranges):**
  `ui/goth/tools/package.json` — Node pinned via `engines` (`node 24.0.1`, `npm 11.3.0`)
  and `tools/.nvmrc` (`24.0.1`); devDependencies `tailwindcss 3.4.19`, `esbuild 0.28.1`,
  `alpinejs 3.15.12`, `@alpinejs/csp 3.15.12`, `htmx.org 2.0.10` (owner-fixed); committed
  `tools/package-lock.json`. `npm ci --ignore-scripts` installs from the lockfile with
  ZERO install scripts run (esbuild's `postinstall` is skipped; its platform binary
  ships as the optional dep `@esbuild/<platform>`), so the install-script allowlist is
  empty — 0 vulnerabilities. **Build (`tools/build.mjs`, `npm run build`):** produces
  three separate self-hosted, production-minified, content-addressed artifacts under
  `ui/goth/assets/dist/` + a committed `ui/goth/assets/manifest.json`: `theme.css`
  (Tailwind-compiled from `assets/src/css/index.css`, 6408B, sha256 `2d356c2d…`),
  `runtime.js` (esbuild IIFE bundle of `assets/src/js/runtime.js` importing
  `@alpinejs/csp`, 61571B, sha256 `c99c3f72…`), `htmx.js` (vendored self-hosted
  `htmx.org/dist/htmx.min.js` byte-for-byte, 51238B, sha256 `71ea6718…`). Each manifest
  entry carries `logicalName`/`path` (retaining the `dist/` segment)/`integrity`
  (sha384 SRI)/`bytes`; the build cleans `dist/` first so a removed/renamed asset never
  lingers. NO test tooling is embedded (axe-core MPL-2.0 / Playwright deferred to the
  GOTH-1.5 harness, a separate package). **Embed + wiring:** new `assets/assets.go`
  (`package assets`, stdlib-only: `//go:embed dist` → `FS fs.FS` keeping the `dist/`
  segment for `web.WithAssetPrefix("dist/")`, `//go:embed manifest.json` →
  `ManifestBytes`); `goth.go` swaps the GOTH-1.1 `manifestSource` placeholder for
  `assets.ManifestBytes` (New now parses the real committed manifest). The GOTH-1.1
  tests whose premise was the empty placeholder were updated: `TestParseManifest`
  "empty" now uses a literal, and `TestHeadEmptyManifestRendersNothing` became
  `TestHeadRendersEmbeddedManifestAssets` (Head emits the fingerprinted SRI-guarded
  links). No new go.mod require (assets is stdlib-only) — G18 stays green. **Makefile:**
  added the NODE-GATED `generate-ui-assets` target (`npm ci --ignore-scripts && npm run
  build`), deliberately NOT a dep of `generate`/`build`/`check`; `check` gains a
  plain-git `git diff --exit-code -- ui/goth/assets/dist ui/goth/assets/manifest.json`
  drift line (Node never invoked in `check`). `.gitignore` ignores
  `ui/goth/tools/node_modules/` only (lockfile + `dist/` + `manifest.json` stay tracked).
  **Provenance (BLOCKING, now satisfied):** `THIRD_PARTY_NOTICES.md` records the exact
  pins, SHA-256 table (embedded outputs + vendored htmx source), sha384 SRI, license
  inventory (htmx 0BSD, Alpine/@alpinejs/csp/Tailwind/esbuild MIT, Node MIT), the empty
  install-script allowlist, and the Playwright/axe deferral. **Verify results (all
  green):** clean `npm ci --ignore-scripts` (106 pkgs, 0 vulnerabilities) + `node
  build.mjs` succeed; two consecutive builds are byte-identical (all four sha256 stable
  across run 1 / run 2 / post-`npm ci` rebuild) — reproducibility proven. `cd ui/goth &&
  go build ./... && go test ./... && go vet ./...` all exit 0 (5 packages incl. the new
  `assets`); manifest/hash tests green (`assets.TestManifestMatchesEmbeddedBytes` —
  every entry's bytes + sha384 match the embedded file; `TestFingerprintMatchesContent`
  — filename fingerprint == sha256 prefix; `TestExpectedAssetSet` — exactly the three
  outputs, no stale/orphan dist file, no `axe`/`playwright`/`test` artifact embedded).
  Drift mechanism demonstrated via the exact `check` command (assets staged, not
  committed): clean → exit 0; tamper a dist byte → exit 1 (drift caught); regenerate →
  exit 0; worktree state then restored to untracked. Full `make check` exit 0 ("all
  checks passed"), `make guard` exit 0 across all 18 guards. Blocker: none. Owner
  decisions carried forward unchanged: HTMX `Attrs` field set remains PROVISIONAL;
  Button one-function-with-URL still flagged. Follow-ups correctly deferred: profile-
  aware Head selection + nonced dynamic stylesheet + preloads (GOTH-1.3); real Alpine
  GOTH controllers + HTMX response config (GOTH-1.4); Playwright/axe browser harness
  (GOTH-1.5); formal release-age review of the non-owner-fixed pins (owner gate).

#### GOTH-1.3 — implement themes, document composition, and CSP profiles

- **depends_on:** GOTH-1.2
- **model:** opus
- **files:**
  - `ui/goth/theme/*`
  - `ui/goth/document.templ`
  - `ui/goth/goth.go`
  - related tests
- **work:** Implement neutral/default light/dark themes, host override ordering,
  bundle head/scripts, manifest URLs, integrity/crossorigin attributes, nonce
  flow, and the nonced dynamic stylesheet. Prove profile requirements are
  deterministic and minimal.
- **verify:** module tests plus browser CSP smoke page with no console violation;
  host override changes semantic values without recompiling kit CSS.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-1.2 complete with
  recorded evidence (fingerprinted `dist/` + manifest embed live). Branch
  `authorization-v3`, HEAD `a5d1a4a`; `git tag --list` empty; all unrelated worktree
  state preserved (authorization-v3 uncommitted work, Phase 0 + GOTH-1.1/1.2 outputs;
  `ui/` remains entirely untracked). Implemented the frozen GOTH-0.3/§2/§4/§5/§6 surface
  exactly — no name/shape reopened. **Themes (neutral/default light/dark, real CSS).**
  New `ui/goth/theme/contract.css` (the semantic-variable contract: every one of the 59
  frozen tokens declared as a `--<token>` custom property with a NEUTRAL fallback on
  `:root`, plus the density-derived `--space-*` scale — `calc(base × var(--density))` —
  so density is a real derivation input, not decoration) and `ui/goth/theme/default.css`
  (the owner-default palette: light values on `:root` byte-identical to package theme's
  `DefaultTheme`, dark values under BOTH `.dark` and `[data-theme="dark"]` — the form
  `theme.HTMLAttributes` emits). `assets/src/css/index.css` now `@import`s both (contract
  then default, so defaults compose over neutrals) ahead of the `@tailwind` directives;
  postcss-import (bundled with the pinned Tailwind CLI) resolves them at build time into
  the single fingerprinted `theme.<hash>.css`. Host override needs no recompile: the kit
  CSS defines every token as a custom property, so a host stylesheet loaded AFTER the kit
  stylesheet wins by source-order cascade (and a `Config.Theme` override rides the nonced
  dynamic stylesheet, below). **Profile-aware Head (the GOTH-1.2 follow-up now closed).**
  `Bundle.Head()` was emitting ALL manifest assets regardless of profile; it now selects
  exactly the profile's classes via `profileAssetOrder` — StylesOnly → `theme.css`;
  Interactive → `+ runtime.js`; Full → `+ htmx.js` — stylesheet link(s) first, then the
  optional nonced style, then the deferred SRI-guarded script(s); each external asset
  keeps `integrity` + `crossorigin="anonymous"`, scripts are never nonced. **Nonce flow +
  the single nonced dynamic stylesheet.** `themeOverrideCSS` diffs the resolved
  `Config.Theme` against `DefaultTheme` and renders ONLY the changed tokens as a `:root{…}`
  block (default palette already lives in `theme.css`; empty override ⇒ no stylesheet ⇒
  no nonce required — minimal by construction). The block is emitted by `nonceStyle`, a
  Go `templ.ComponentFunc` (templ does not evaluate expressions inside a `<style>`
  element — its content is raw text — so `@templ.Raw` inside `<style>` was inert; the
  element is written directly instead): it reads the per-render nonce from `ctx`, HTML-
  attribute-escapes it, emits nothing when the nonce resolves empty, and the CSS is
  injection-safe because `NewTheme` rejects any value carrying `<`/`>` or a `;{}<>\`
  escape, so the content can never contain `</style>` nor break out. It loads after the
  `theme.css` link so its `:root` override wins. `Requirements.RequiresNonce()` is true
  ONLY when a nonce provider is set AND the theme has an override; `style-src` sources
  stay `'self'` (the host adds `'nonce-<n>'` off `RequiresNonce`, one provider per render
  per §4(b)) — never a baked nonce value, never blanket `unsafe-inline`. A non-default
  `Config.Theme` without a nonce provider emits no inline style (host uses its own
  override stylesheet; `Bundle.Theme()` exposes the resolved values) — a documented,
  non-erroring posture that does NOT expand the frozen `New` error set. **Files changed
  (all under the untracked `ui/goth`; no `go.work`/root change; feature cores untouched
  → guard-clean):** `theme/contract.css` (NEW), `theme/default.css` (NEW),
  `assets/src/css/index.css` (import the two layers), `theme/theme.go` (comment: the Go
  default map is the light palette, byte-aligned with default.css; drift-guarded),
  `theme/contract_css_test.go` (NEW — every frozen token declared; resolved light `:root`
  == `DefaultTheme`; dark palette present under both selectors and differs from light),
  `goth.go` (profile-aware `headResources`/`profileAssetOrder`, `themeOverrideCSS`,
  `nonceStyle`, `headModel`, nonce/override wired into `New`→`Requirements`), `document.templ`
  + regenerated `document_templ.go` (`headTags(headModel)` emits styles → `@nonceStyle` →
  scripts), `goth_test.go` (NEW tests below), and the regenerated
  `assets/dist/theme.9a942d0b.css` + `assets/manifest.json` (theme.css 10656B, sha384
  `KZQH07TZ…`; `runtime.js`/`htmx.js` bytes unchanged — only the CSS carried the theme
  layers). **Verify results (all green):** `make generate` exit 0, a second
  `document_templ.go` generation is byte-identical (shasum `f6026bcd…` stable across two
  runs); `cd ui/goth && go build ./... && go test ./... && go vet ./...` all exit 0 (6
  packages). Module CSP smoke proven deterministically at the Go level (the real-browser
  Playwright/axe leg is GOTH-1.5's harness, consistent with 1.1/1.2's browser deferral):
  `TestHeadProfileAwareAssetSelection` (each profile serves exactly its classes; every
  asset SRI+crossorigin; no inline `<style>` without an override),
  `TestHeadNoncedDynamicStylesheet` (`<style nonce="…">:root{--primary:…;}` with only the
  changed token, after `theme.css`, scripts un-nonced, `RequiresNonce` true),
  `TestHeadThemeOverrideWithoutNonce` (no nonce ⇒ no inline style, `RequiresNonce` false),
  `TestHeadDefaultThemeWithNonceEmitsNoStyle` (default theme + nonce ⇒ nothing, minimal),
  `TestHeadNonceEmptyValueEmitsNoStyle`, `TestRequirementsNonceSourceCoherence` (style-src
  stays `'self'`, no `unsafe-inline`/baked nonce, directives stable). Host-override-without-
  recompile proven by `TestContractCSSDeclaresEveryToken` + `TestDefaultCSSMatchesDefaultTheme`
  + `TestDefaultCSSDefinesDarkPalette` (Go↔CSS palette alignment + the token custom-property
  surface a host restyles by cascade). Asset build reproducible: `make generate-ui-assets`
  (npm ci --ignore-scripts + build) byte-identical across runs (theme.css `9a942d0b`
  stable; the first post-`@import` build produced a transient hash from a cold Tailwind/
  browserslist cache — the committed output is the stable value, re-proven twice).
  `make check` exit 0 ("all checks passed" — templ no-op, the ui asset git-diff drift line
  clean since `ui/` is untracked, full per-module vet/build/test, integration-tag vet) and
  `make guard` exit 0 across all 18 guards. Blocker: none. Owner decisions carried forward
  unchanged (not reopened): Button one-function-with-URL still flagged (GOTH-0.3); HTMX
  `Attrs` field set remains PROVISIONAL. Design note for downstream: the frozen `New`
  error set was deliberately NOT expanded — a non-default `Config.Theme` with a nil
  `Config.Nonce` is a documented no-inline-emission (host ships its own override
  stylesheet), not a construction error, to avoid reopening the Gate B §2 contract.
  Follow-ups correctly deferred: real Alpine GOTH controllers + HTMX response config
  (GOTH-1.4); the Playwright/axe browser CSP smoke + showcase (GOTH-1.5).

#### GOTH-1.4 — implement Alpine CSP and HTMX runtime foundations

- **depends_on:** GOTH-1.2, GOTH-1.3
- **model:** opus
- **files:**
  - `ui/goth/assets/src/js/*`
  - `ui/goth/htmx/*`
  - runtime tests
- **work:** Register named Alpine controllers/shared mechanics, pin only earned
  plugins, configure HTMX 2 non-2xx fragment handling, and implement explicit
  typed attribute helpers. Add diagnostics proving the three bundle profiles
  load only their promised assets.
- **verify:** no `unsafe-eval`; no network request outside the showcase origin;
  HTMX full/fragment/error/history diagnostic browser tests pass.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-1.2 + GOTH-1.3
  complete with recorded evidence (fingerprinted `dist/` + manifest embed +
  profile-aware Head + nonced dynamic stylesheet live). Branch `authorization-v3`,
  HEAD `a5d1a4a`; `git tag --list` empty; all unrelated worktree state preserved
  (authorization-v3 uncommitted work, Phase 0 + GOTH-1.1/1.2/1.3 outputs; `ui/`
  remains untracked). Implemented the frozen §8 controller rules and the §9 HTMX
  contract exactly — no frozen name/shape reopened, the `Attrs` field set left
  PROVISIONAL (not finalized — that is the recorded GOTH-5.3/7.3 point). **Named
  Alpine controllers + shared mechanics (CSP build).** Restructured
  `assets/src/js/` into a real modular runtime: `runtime.js` (entrypoint — imports
  `@alpinejs/csp`, calls `registerControllers(Alpine)` ONCE, then `Alpine.start()`),
  `register.js` (binds all seven frozen goth-prefixed controllers via
  `Alpine.data(...)` — the CSP-safe data-component form, so no inline expression is
  evaluated and no `'unsafe-eval'` is required), seven controllers under
  `controllers/` (`gothDialog`, `gothCollapse`, `gothRovingFocus`, `gothMenu`,
  `gothTabs`, `gothCombobox`, `gothToast`), and five shared-mechanics modules under
  `mechanics/` (`focus` — trap+restore; `dismiss` — Escape/outside-click in the
  capture phase; `roving` — roving tabindex arrow/Home/End; `typeahead` — buffered
  type-to-focus; `live-region` — one shared polite/assertive `aria-live` announcer).
  Controllers discover parts through the frozen `data-slot`/`data-state` attributes,
  dispatch only the documented `goth:open`/`goth:close`/`goth:select`/`goth:change`
  events (no feature-specific event name), and hold local interaction state only.
  Scope note: the controllers are the Phase-1 FOUNDATION — they establish the
  registration pattern, shared mechanics families, event vocabulary, and data-hook
  discovery; each primitive phase (2–6) refines its controller's per-primitive
  semantics. **Pin only earned plugins.** Focus trap and collapse are implemented in
  the shared-mechanics modules, so NO new Alpine plugin was adopted — `@alpinejs/focus`
  and `@alpinejs/collapse` remain unearned this milestone (THIRD_PARTY_NOTICES note
  unchanged on that point). **HTMX 2 non-2xx fragment handling (explicit).** New
  `assets/src/js/htmx-config.js` — a minified IIFE appended to the vendored
  byte-for-byte htmx 2.0.10 to form the single self-hosted `htmx.js` asset (the
  "HTMX 2.0.10 + Gopernicus response configuration" the Full profile promises). It
  sets `htmx.config.responseHandling` EXPLICITLY so 403 forbidden / 409 conflict /
  422 validation responses swap a complete fragment (still flagged `error:true`),
  204 swaps nothing, 2xx/3xx swap, and other 4xx/5xx surface as an error without a
  swap — pinned to 2.0.10 semantics, not relying on HTMX 4 defaults (plan HTMX rule
  5). The reader derives no identity/CSRF/authorization from an hx header. **Typed
  attribute helpers.** The `htmx` Go package (`Attrs`/`Build`, `Method`/`Swap` enums,
  `Request` reader) already landed at GOTH-1.1/1.2 and is unchanged here; the
  PROVISIONAL `Attrs` field set was deliberately NOT expanded or finalized.
  **Diagnostics proving profiles load only promised assets.** `goth_test.go`
  `TestProfileScriptEmission`: StylesOnly emits no `<script`; Interactive adds the
  runtime script but not HTMX; Full adds both; promised scripts are deferred
  (progressive baseline) — complementing GOTH-1.3's `TestHeadProfileAwareAssetSelection`.
  **Files changed (all under the untracked `ui/goth`; no `go.work`/root/Makefile
  change; feature cores untouched → guard-clean):** `assets/src/js/runtime.js`
  (rewritten entrypoint), `assets/src/js/register.js` (NEW),
  `assets/src/js/controllers/{dialog,collapse,roving-focus,menu,tabs,combobox,toast}.js`
  (NEW), `assets/src/js/mechanics/{focus,dismiss,roving,typeahead,live-region}.js`
  (NEW), `assets/src/js/htmx-config.js` (NEW), `assets/src/css/index.css`
  (`.goth-sr-only` live-region utility — the runtime's aria-live nodes stay
  off-screen without an inline style, README §4 invariant a), `tools/build.mjs`
  (shared `bundleJS` esbuild helper; `vendorHtmx` now concatenates the minified
  config onto upstream htmx; upstream-sha provenance log), the regenerated
  `assets/dist/{theme.310dc55c.css,runtime.bcae1df7.js,htmx.78b635c6.js}` +
  `assets/manifest.json`, `assets/runtime_test.go` (NEW — asset-level diagnostics),
  `goth_test.go` (`TestProfileScriptEmission`), and `assets/THIRD_PARTY_NOTICES.md`
  (refreshed SHA-256 records + honest "upstream htmx + Gopernicus config" and
  "runtime + controllers" descriptions; the stale GOTH-1.3 theme.css hash row was
  corrected in the same pass). **Verify results (all green):** `make generate-ui-assets`
  (npm ci --ignore-scripts + build) byte-identical across two runs (theme.css
  `310dc55c`, runtime.js `bcae1df7`, htmx.js `78b635c6` stable both times);
  `cd ui/goth && go build ./... && go vet ./... && go test ./...` all exit 0 (5
  packages). The verify criteria proven at the asset/source + Go level (the
  real-browser Playwright/axe leg is GOTH-1.5's harness, consistent with the
  1.1/1.2/1.3 browser deferral): `assets/runtime_test.go` proves (a) NO `'unsafe-eval'`
  — `TestRuntimeIsCSPSafe` asserts the built `runtime.js` contains no `eval(`/`new
  Function`/`Function(` construct; (b) NO remote origin — `TestAssetsUseNoRemoteOrigin`
  asserts neither JS asset references a CDN host (unpkg/jsdelivr/cdnjs/googleapis/
  `cdn.`); (c) all seven controllers registered — `TestRuntimeRegistersNamedControllers`;
  (d) explicit non-2xx handling — `TestHTMXAssetHasResponseConfig` asserts
  `responseHandling` + the 403/409/422 swap codes are in the built `htmx.js`; (e)
  `TestThemeCSSHasLiveRegionUtility`. `make guard` exit 0 (18 guards — G17/G18 clean:
  the runtime imports no inward package, `go.mod` still requires only templ) and
  `make check` exit 0 ("all checks passed" — templ generation no-op, per-module
  vet/build/test, the untracked-`ui/` dist git-diff drift line clean). **Blocker:**
  none. The browser HTMX full/fragment/error/history diagnostic + no-unsafe-eval +
  no-remote-origin real-browser proofs are wired into GOTH-1.5's three-engine
  Playwright/axe showcase harness (GOTH-1.5 depends on GOTH-1.4); GOTH-1.4 delivers
  the runtime foundation those tests will drive. Owner decisions carried forward
  unchanged (not reopened): Button one-function-with-URL still flagged (GOTH-0.3);
  the HTMX `Attrs` field set remains PROVISIONAL until GOTH-5.3/7.3.
- **evidence — GOTH-1.5 browser-proof remediation (2026-07-17):** GOTH-1.5's
  three-engine Playwright proof (the browser verification this task deferred to it)
  found two runtime defects fixed at source there: `gothCollapse` reflected state on
  `this.$el` (the descendant handler element) instead of the cached root, and the
  htmx-config let htmx inject an inline `<style>` a strict `style-src 'self'` blocks.
  Both fixed (`assets/src/js/controllers/collapse.js` root-caching;
  `htmx.config.includeIndicatorStyles = false`), assets rebuilt
  (`runtime.9cb99b8b.js`, `htmx.8689e2e2.js`). GOTH-1.4 stays checked; see the
  GOTH-1.5 evidence for detail.

#### GOTH-1.5 — create the showcase and browser/a11y harness

- **depends_on:** GOTH-1.3, GOTH-1.4
- **model:** opus
- **files:**
  - `examples/goth-showcase/**/*`
  - `go.work`
  - `Makefile`
- **work:** Create a zero-datastore server that registers the embedded asset FS,
  shows every state/theme/runtime profile, exposes deliberate HTMX success and
  validation fixtures, and runs pinned Playwright+axe tests in three engines.
  Define a registry so missing primitive specimens fail automatically.
- **verify:** showcase Go suite; `make test-ui-browser`; manual run-and-look at
  narrow/wide, light/dark, reduced-motion, and RTL.
- **evidence (2026-07-17):** Completed. Dependencies confirmed: GOTH-1.3 + GOTH-1.4
  complete with recorded evidence (fingerprinted `dist/` + manifest embed +
  profile-aware Head + nonced dynamic stylesheet + named Alpine CSP controllers +
  explicit HTMX non-2xx response config). Branch `authorization-v3`; all unrelated
  worktree state preserved (authorization-v3 uncommitted work, Phase 0 +
  GOTH-1.1..1.4 outputs; `ui/` remains untracked). **Zero-datastore showcase host.**
  New module `examples/goth-showcase` (added to `go.work` + Makefile `MODULES`;
  requires only `sdk` + `ui/goth` + templ): `cmd/server` is the composition root;
  `internal/showcase` builds one `goth.Bundle` per profile (StylesOnly/Interactive/
  Full) plus a themed Interactive bundle (host `primary` override) to exercise the
  nonced dynamic stylesheet. Only the host registers the asset route
  (`web.NewStaticFileServer(assets.FS, web.WithAssetPrefix("dist/"))` under
  `goth.DefaultAssetBasePath`) and writes the CSP header (mapped from
  `Bundle.Requirements()` + host-owned baseline `default-src 'none'; base-uri
  'none'; form-action 'self'; frame-ancestors 'none'; img-src 'self'; font-src
  'self'`, plus `connect-src 'self'` on HTMX pages; the per-render nonce feeds both
  the bundle and `style-src` — one provider per render, README §4(b)). **Registry.**
  `registry.go` is the single source of truth: it drives both the HTTP routes and
  `TestEveryImplementedPrimitiveHasSpecimen`, which fails automatically if an id in
  `ImplementedPrimitives` (EMPTY at Phase 1 — no P01–P64 exists yet) lacks a
  registered specimen; each Phase 2–6 primitive appends its id + a specimen and the
  test enforces coverage. Phase-1 specimens: three profiles, five theme axes
  (light/dark/RTL/reduced-motion/nonced-override), and the HTMX success/validation/
  error/history fixtures. **Node-gated three-engine harness.** `examples/goth-showcase/e2e`
  is an isolated Node toolchain (exact pins: `@playwright/test 1.60.0` — matches the
  cached chromium 1223/firefox 1522/webkit 2287 builds — `@axe-core/playwright 4.12.1`,
  `axe-core 4.12.1`; committed `package-lock.json`, `node_modules`/`test-results`
  gitignored). **axe-core (MPL-2.0) lives ONLY here** — never imported by `ui/goth`,
  never embedded in `assets/dist` (guarded by `assets.TestExpectedAssetSet`). New
  Makefile `test-ui-browser` target (`npm ci && npx playwright install … && npm
  test`), deliberately NOT a dep of `check`/`build`/`test`. Playwright starts the Go
  server itself (`webServer: go run ./cmd/server`, cwd `..`). **Two GOTH-1.4 runtime
  defects the browser proof surfaced and fixed at source (edit + `make
  generate-ui-assets`, never a hand-patched dist):** (1) `gothCollapse` used
  `this.$el` in `_sync()`, but a method invoked from a descendant's `x-on` handler
  sees `$el` bound to that descendant, so `data-state` never updated on the root —
  fixed by caching the root at `init()`; (2) htmx 2 injects an inline `<style>` for
  its indicator rules, which a strict `style-src 'self'` blocks (violating the kit's
  own "no static inline style" invariant) — fixed by setting
  `htmx.config.includeIndicatorStyles = false` in the vendored htmx-config. Rebuilt
  `runtime.9cb99b8b.js` + `htmx.8689e2e2.js` (manifest + THIRD_PARTY_NOTICES SHA-256
  refreshed); `ui/goth` Go suite still green. The Playwright `x-on:click="toggle()"`
  binding confirmed the `@alpinejs/csp` build requires the explicit call form (bare
  `toggle` is inert). **Verify results (all green):** showcase Go suite —
  `cd examples/goth-showcase && go vet/build/test ./...` exit 0
  (`TestEverySpecimenServesPageUnderStrictCSP`, `TestProfileCSPMapping`,
  `TestNoncedOverrideEmitsNonceUnderStyleSrc`, `TestHTMXFixturesResponseCodes`,
  `TestEveryImplementedPrimitiveHasSpecimen`, `TestIndexListsEverySpecimen`).
  `make test-ui-browser` → **36 passed** (12 specs × chromium/firefox/webkit): the
  deferred REAL-BROWSER proofs are now actually run and green in all three engines —
  strict-CSP smoke with zero securitypolicyviolation + zero console errors on every
  specimen; strict script-src blocks inline script + eval; no-remote-origin (every
  request same-origin); Alpine CSP boots + `gothCollapse` toggles under no-unsafe-eval;
  each profile loads only its promised assets; HTMX 200 success swap / 422 validation
  swap / 500 no-swap error / push-url + correct full-document re-fetch; axe clean on
  every specimen + a reduced-motion pass. `make check` exit 0 ("all checks passed" —
  templ no-op, per-module vet/build/test incl. the new module, ui asset git-diff drift
  clean since `ui/` is untracked) and `make guard` exit 0 (18 guards — G17/G18 clean:
  the showcase is an example importing `ui/goth`, which no guard forbids; `ui/goth`
  imports nothing inward). Manual run-and-look (Chromium screenshots, viewed): index
  lists every specimen; RTL specimen renders right-aligned (dir propagation working);
  narrow (390px) HTMX fixtures render; pages are semantically correct and legible.
  Phase-1 pages are intentionally unstyled beyond the Tailwind base reset — no
  primitive is implemented yet, so component styling arrives in Phase 2+; the
  foundation (assets, CSP, profiles, controllers, HTMX, registry, three-engine gate)
  is proven. **Files changed:** `go.work` (+`./examples/goth-showcase`); `Makefile`
  (MODULES +`examples/goth-showcase`, new `test-ui-browser` target + `.PHONY`);
  `examples/goth-showcase/{go.mod,go.sum}`, `cmd/server/main.go`,
  `internal/showcase/{showcase.go,registry.go,specimens.go,showcase_test.go}`,
  `e2e/{package.json,package-lock.json,playwright.config.ts,.gitignore,README.md}`,
  `e2e/tests/{helpers.ts,csp.spec.ts,runtime.spec.ts,htmx.spec.ts,axe.spec.ts}`;
  and the GOTH-1.4 remediation under `ui/goth`:
  `assets/src/js/controllers/collapse.js`, `assets/src/js/htmx-config.js`,
  regenerated `assets/dist/{runtime.9cb99b8b.js,htmx.8689e2e2.js}` +
  `assets/manifest.json`, `assets/THIRD_PARTY_NOTICES.md`. **CI-job / cache / flake
  policy (Gate B GOTH-1.5 record, in `e2e/README.md`):** CI job `ui-goth-browser`
  (not in the default `check` gate), triggered on PRs touching `ui/goth/**` or
  `examples/goth-showcase/**`, plus a nightly `schedule` and `workflow_dispatch`;
  runs `make test-ui-browser`. Browser-binary cache keyed
  `playwright-${{ runner.os }}-<playwright-version-from-lockfile>` over the
  ms-playwright cache dir; cache miss → `npx playwright install --with-deps chromium
  firefox webkit`, hit → skip. Bounded flake policy: `retries: 2` (3 attempts) in CI,
  `0` locally; `workers: 1` in CI so the single Go webServer is deterministic;
  `forbidOnly` in CI; retries recorded in the html/github reporters, a spec failing
  all attempts fails the gate. **Blocker:** none. Owner decisions carried forward
  unchanged (not reopened): Button one-function-with-URL still flagged (GOTH-0.3);
  HTMX `Attrs` field set remains PROVISIONAL (the showcase uses only the frozen
  `Method`/`Swap`/`Build` surface). Scope note flagged for owner/reviewers: GOTH-1.5
  edited two GOTH-1.4 runtime SOURCE files (not generated output) to make the deferred
  browser proofs pass — this is the intended completion of 1.4's browser verification
  (1.4 explicitly wired those proofs into 1.5), not a silent reopen; GOTH-1.4 stays
  checked with a cross-reference note. A latent audit item for Phase 2+: the same
  `this.$el`-from-descendant pattern may exist in the other six controllers
  (dialog/menu/tabs/combobox/roving-focus/toast); each is exercised + fixed with its
  own primitive's browser specimen when that primitive lands.

### Phase 2 — presentational and native/form foundations

#### GOTH-2.1 — implement content/status primitives P01–P04, P08, P10, P14–P15, P17, P22–P23, P26

- **depends_on:** phase 1 gate
- **model:** opus
- **files:** matching `ui/goth/primitives/*` sources/tests and showcase registry
- **work:** Implement Alert, Aspect Ratio, Avatar, Badge, Card, Empty, Item,
  Kbd, Marker, Skeleton, Spinner, and Typography with all documented states.
- **verify:** module render tests, catalog registration, axe, theme/RTL/reduced
  motion browser specimens, and run-and-look.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 1 gate MET
  (2026-07-17); GOTH-2.1 was the sole dependency-ready Phase 2 task. Branch
  `authorization-v3`; unrelated worktree (authorization-v3 uncommitted work, the
  untracked `.claude/plans/ui-goth/`, and the Phase 0/1 `ui/`+`examples/goth-showcase`
  trees) left intact — all changes additive. Parity-matrix rows P01–P26 remain
  UNCHECKED per the Phase 2 gate (rows are checked only at the GOTH-2.4 wave audit).
  **Built (12 primitives, each its frozen family shape from catalog.md).** Sources
  under `ui/goth/primitives/` (`.templ` + `.go` pairs, generated `*_templ.go` via
  `make generate`, never hand-edited); styling via a new
  `assets/src/css/components.css` keyed off the stable `goth-<primitive>` +
  `data-slot`/`data-variant`/`data-state`/`data-tone` surface consuming the frozen
  theme tokens (no inline `style=` anywhere; regenerated through the Node-gated
  `make generate-ui-assets`). No controllers were needed — all twelve are
  presentational (StylesOnly specimens):
  - **P01 Alert (F2)** — icon/title/description(children)/action slots; role
    derives status (polite) vs alert (assertive) from variant
    (default/success/warning/destructive).
  - **P02 Aspect Ratio (F2)** — children fill a CSS `aspect-ratio` box selected by
    a typed `Ratio` enum via `data-ratio` (square/video/wide/four-three/portrait),
    no inline style.
  - **P03 Avatar (F3)** — Avatar/AvatarImage/AvatarFallback; image (validated
    `URL` src + required Alt) over a fallback so there is no blank avatar without JS.
  - **P04 Badge (F1)** — single inline label; variants default/secondary/
    destructive/outline; optional `URL` renders an anchor (no nested interactive).
  - **P08 Card (F3)** — Card/Header/Title/Description/Action/Content/Footer;
    title is a div (caller owns heading levels).
  - **P10 Empty (F2)** — media/title/description/content(children)/action fixed layout.
  - **P14 Item (F3)** — Item(+ItemMedia/Content/Title/Description/Actions);
    whole-row link via `URL`; variants default/outline/muted.
  - **P15 Kbd (F3)** — semantic `<kbd>` + KbdGroup shortcut grouping.
  - **P17 Marker (F2)** — status/note/row/separator variants, optional icon,
    tone (success/warning/destructive), optional `URL` link form.
  - **P22 Skeleton (F1)** — aria-hidden, non-announcing; CSS pulse disabled under
    reduced motion.
  - **P23 Spinner (F1)** — named `role="status"` + visually-hidden accessible name
    by default (never nameless); `Decorative` renders aria-hidden for live-region
    composition; CSS spin disabled under reduced motion.
  - **P26 Typography (F2)** — semantic recipes (h1–h4/p/lead/large/small/muted/
    blockquote/code/list); heading variants emit real `<h1>`–`<h4>` (no replaced
    heading levels).
  All Props embed the frozen `Base` (single-spread `MergeAttributes`, `class`-in-
  `Attributes` rejected, `Base.ID` honored); typed variant/size enums carry
  `Valid()` + a zero-value default (unknown → default render, no panic).
  **Showcase.** `examples/goth-showcase/internal/showcase/specimens_primitives.go`
  registers one specimen per primitive rendering the REAL component; the 12 ids
  were appended to `ImplementedPrimitives` so
  `TestEveryImplementedPrimitiveHasSpecimen` is now load-bearing. **Verify results
  (exact):** `cd ui/goth && go build ./... && go test ./... && go vet ./...` PASS
  (new `primitives/content_test.go` proves data hooks, role derivation, URL/anchor
  forms, accessible-name, no inline `style=`, and the Base merge/class/ID
  contract). `cd examples/goth-showcase && go test ./...` PASS (catalog
  registration + strict-CSP specimen serving). `make test-ui-browser` PASS —
  **36 passed (12 specs × Chromium/Firefox/WebKit)**; axe crawls every specimen
  (incl. all 12 new primitive pages) with zero violations in all three engines,
  and the strict-CSP spec shows zero `securitypolicyviolation`/console errors.
  `make check` exit 0 ("all checks passed" — templ drift clean/no-op, all 36
  modules vet/build/test, all 18 guards) and `make guard` exit 0. Contrast note:
  one axe pass surfaced that `muted-foreground` text only clears AA (4.5:1) on the
  near-white base surface, not on a `muted`-tinted surface — fixed at source by
  moving the avatar-fallback color to `--foreground` and making the Item `muted`
  variant de-emphasize text on the base surface (no theme-token change, drift test
  intact); speculative success/warning Badge variants were dropped to match Shadcn
  parity and avoid a 3.2:1 white-on-green badge. **Not done (by design):** the
  human "run-and-look" visual pass at narrow/wide × light/dark × one RTL specimen
  is Layer 4 owner work; the automated axe/CSP browser proof across three engines
  is green.

#### GOTH-2.2 — implement action/navigation primitives P05–P07, P09, P19, P21

- **depends_on:** GOTH-2.1
- **model:** opus
- **files:** matching primitive sources/tests and showcase registry
- **work:** Implement Breadcrumb, Button, Button Group, Direction, Pagination,
  and Separator. Pin native link/button/form semantics and RTL behavior.
- **verify:** keyboard/link/form browser tests; render semantics; no invalid
  nested interactive elements in showcase fixtures.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 1 gate MET and
  GOTH-2.1 DONE (2026-07-17); GOTH-2.2 was the numerically-first dependency-ready
  Phase 2 task (2.3 also ready, not touched). Branch `authorization-v3`; unrelated
  worktree (authorization-v3 uncommitted work, untracked `.claude/plans/ui-goth/`,
  the Phase 0/1 + GOTH-2.1 `ui/`+`examples/goth-showcase` trees) left intact — all
  changes additive. Parity-matrix rows P05–P26 remain UNCHECKED per the Phase 2 gate
  (checked only at the GOTH-2.4 wave audit). **Built (6 primitives, each its frozen
  family shape from catalog.md).** Sources under `ui/goth/primitives/` (`.templ` +
  `.go` pairs; generated `*_templ.go` via `make generate`, never hand-edited);
  styling appended to `assets/src/css/components.css` keyed off the stable
  `goth-<primitive>` + `data-slot`/`data-variant`/`data-orientation`/`data-active`
  surface consuming the frozen theme tokens (no inline `style=`; regenerated through
  the Node-gated `make generate-ui-assets`). No controllers were needed — all six are
  presentational (native links/buttons; StylesOnly specimens):
  - **P21 Separator (F1)** — one function; a leaf divider with a shared
    `Orientation` enum (horizontal zero value / vertical). Decorative by default
    (`role="none"` + `aria-hidden`); `Semantic:true` exposes `role="separator"` +
    `aria-orientation`. No content.
  - **P09 Direction (F1)** — wraps children in `<div dir=…>` (reusing
    `theme.Direction`, zero/unknown → LTR) so a subtree overrides document direction
    with pure native `dir` inheritance, no script. `display:contents` so it adds no
    box. data-slot="direction".
  - **P06 Button (F2)** — ONE `Button` function per the frozen freeze: empty `URL`
    → native `<button>` (safe default `type="button"`, never implicit submit),
    non-zero `URL` → anchor styled as a button. Variants default/secondary/
    destructive/outline/ghost/link; sizes default/sm/lg/icon; leading/trailing icon
    slots; `Loading` renders a decorative `Spinner` glyph + `aria-busy` + `disabled`.
    Cross-field guard: `Validate()` returns `ErrButtonURLConflict` for URL+submit/
    reset OR URL+disabled/loading; the render degrades to a native `<button>`
    honoring the submit/disabled intent (the safer resolution) and marks
    `data-goth-invalid="button-url-conflict"` — never a silent spec-violating anchor.
    Icon-only (`ButtonIcon`) REQUIRES `Label`, emitted as `aria-label` (render-time
    contract test).
  - **P07 Button Group (F3)** — ButtonGroup (`role="group"`, `data-orientation`,
    CSS joins borders/focus rings) + ButtonGroupText addon segment.
  - **P05 Breadcrumb (F3)** — Breadcrumb (labelled `<nav>`, default aria-label
    "Breadcrumb") / BreadcrumbList (`<ol>`) / BreadcrumbItem (`<li>`) / BreadcrumbLink
    (`<a>` via `URL`) / BreadcrumbPage (current page: not a link, `aria-current="page"`
    + `aria-disabled`) / BreadcrumbSeparator (presentational, default chevron glyph or
    `Icon` override) / BreadcrumbEllipsis (responsive collapse: glyph + `goth-sr-only`
    "More").
  - **P19 Pagination (F3)** — Pagination (labelled `<nav role="navigation">`) /
    PaginationContent (`<ul>`) / PaginationItem (`<li>`) / PaginationLink (real
    server-owned `URL`; `Active` → `aria-current="page"` + `data-active`) /
    PaginationPrevious / PaginationNext (edge links: chevron + visible/accessible
    label, server URL) / PaginationEllipsis (glyph + `goth-sr-only` "More pages").
    Page state is server-owned URLs — works with no JS.
  Shared additions: a package-scoped `Orientation` enum (Separator + ButtonGroup) and
  an internal unexported `glyphs.templ` (aria-hidden chevron-left/right + ellipsis
  SVGs) drawn as built-in defaults — no new public package/controller; public APIs
  still accept caller icons as `templ.Component`. All Props embed the frozen `Base`
  (single-spread `MergeAttributes`, `class`-in-`Attributes` rejected, `Base.ID`
  honored); typed enums carry `Valid()` + zero-value default (unknown → default
  render, no panic). **Showcase.** `examples/goth-showcase/internal/showcase/
  specimens_primitives_action.go` registers one specimen per primitive rendering the
  REAL component; the six ids were appended to `ImplementedPrimitives` so
  `TestEveryImplementedPrimitiveHasSpecimen` stays load-bearing; the breadcrumb/
  pagination fixtures avoid invalid nested-interactive markup (current page is a
  non-link span; whole-row links carry no nested controls). **Verify results
  (exact):** `cd ui/goth && go build ./... && go test ./... && go vet ./...` PASS
  (new `primitives/action_test.go` proves data hooks, decorative-vs-semantic
  separator roles, `dir` propagation/LTR fallback, button native/link forms +
  type/disabled, the icon-only accessible-name contract, the URL cross-field guard
  degradation, loading state, breadcrumb current-page/`aria-current`, pagination
  active/`aria-current`, edge-link names, and the Base merge/class/ID contract).
  `cd examples/goth-showcase && go build ./... && go vet ./... && go test ./...` PASS
  (catalog registration + strict-CSP specimen serving). `make test-ui-browser` PASS —
  **36 passed (12 specs × Chromium/Firefox/WebKit)**; axe crawls every specimen
  (incl. all 6 new pages) with zero violations in all three engines, and the
  strict-CSP spec shows zero `securitypolicyviolation`/console errors. `make check`
  exit 0 ("all checks passed" — templ drift clean/no-op, all modules vet/build/test,
  all guards incl. G17/G18 ui outward-import + go.mod discipline) and `make guard`
  exit 0. Contrast note: the axe pass surfaced the same muted-on-muted class of
  finding GOTH-2.1 hit — the ButtonGroup addon text (`muted-foreground` on `muted`,
  4.35:1) narrowly missed AA; fixed at source by moving `goth-button-group-text` to
  `--foreground` (no theme-token change, drift check intact). **Not done (by
  design):** the human "run-and-look" visual pass (narrow/wide × light/dark × RTL) is
  Layer 4 owner work; the automated axe/CSP three-engine browser proof is green (the
  Direction RTL specimen exercises the RTL axis).

#### GOTH-2.3 — implement field/data primitives P11–P13, P16, P18, P20, P24–P25

- **depends_on:** GOTH-2.1
- **model:** opus
- **files:** matching primitive sources/tests and showcase registry
- **work:** Implement Field, Input, Input Group, Label, Native Select, Progress,
  Table, and Textarea. Prove label/error/description relationships and ordinary
  form submission.
- **verify:** render and native-form tests; browser validation/focus tests; axe;
  no secret-value repopulation helpers are introduced.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-2.1 DONE (and
  GOTH-2.2 DONE) 2026-07-17; GOTH-2.3 was the sole remaining pre-audit Phase 2
  task. Branch `authorization-v3`; unrelated worktree (authorization-v3
  uncommitted work, untracked `.claude/plans/ui-goth/`, the Phase 0/1 + GOTH-2.1/2.2
  `ui/`+`examples/goth-showcase` trees) left intact — all changes additive.
  Parity-matrix rows P11–P26 remain UNCHECKED per the Phase 2 gate (checked only at
  the GOTH-2.4 wave audit). No new Alpine controller name was introduced — all eight
  are native/presentational (StylesOnly specimens), so no new frozen controller
  surface. **Built (8 primitives, each its frozen family shape from catalog.md).**
  Sources under `ui/goth/primitives/` (`.templ` + `.go` pairs; generated `*_templ.go`
  via `make generate`, never hand-edited); styling appended to
  `assets/src/css/components.css` keyed off the stable `goth-<primitive>` +
  `data-slot`/`data-variant`/`data-state`/`data-invalid`/`data-align`/`data-orientation`
  surface consuming the frozen theme tokens (no inline `style=`; regenerated through
  the Node-gated `make generate-ui-assets`):
  - **P16 Label (F1)** — native `<label>`; `For` → native `for`, `Required` adds a
    decorative aria-hidden indicator (the control owns authoritative `required`),
    `Disabled` → `data-disabled` styling only.
  - **P12 Input (F1)** — native `<input>` with the full attribute surface (typed
    `InputType` text/email/password/search/tel/url/number/file/date/time; zero →
    text; unknown → text, no panic), name/value/placeholder/required/disabled/
    readonly and an `Invalid` state (`aria-invalid` + `data-invalid`). No
    secret-value repopulation: a password Input NEVER echoes `Value` back into the
    markup (dedicated contract test); text inputs do.
  - **P25 Textarea (F1)** — native `<textarea>`; value is the element's text
    content (native submission), optional `Rows`, invalid/disabled/readonly; autosize
    is an explicit enhancement boundary (no controller forced).
  - **P20 Progress (F1)** — native `<progress>`: determinate geometry rides
    `value`/`max` NATIVE attributes (never an inline `style=` — plan geometry rule),
    `data-state=determinate/indeterminate`, Value clamps to [0,Max] (Max default 100),
    indeterminate omits `value`; the CSS indeterminate animation collapses under
    prefers-reduced-motion.
  - **P18 Native Select (F3)** — NativeSelect (`<select>`, name/required/disabled/
    invalid) + NativeSelectOption (`<option>` value/selected/disabled) +
    NativeSelectGroup (`<optgroup label>`). Native form submission, no JS.
  - **P11 Field (F3)** — Field (data-orientation via a Field-specific
    `FieldOrientation` whose zero value is vertical, so a zero FieldProps is the
    natural default; `Invalid` → `data-invalid`) + FieldLabel (`<label for>`) +
    FieldDescription + FieldError + FieldGroup + FieldSet (`<fieldset>`) + FieldLegend
    (`<legend>`). ARIA linkage is caller-passed via `Base.ID` per part (label `for` →
    control id; control `aria-describedby` → description/error ids via the
    `Base.Attributes` escape hatch) — no ambient context key; proven by
    `TestFieldAriaLinkage`.
  - **P13 Input Group (F3)** — InputGroup (`role="group"`, single shared border/focus
    ring; `Invalid`) + InputGroupAddon (`InputGroupAlign` start/end) + InputGroupText
    (prefix/suffix) + InputGroupButton (compact button; icon-only REQUIRES `Label` →
    `aria-label`, `Type` default button). The inner control is a real Input (P12);
    CSS flattens `.goth-input-group .goth-input` so the group owns the border.
  - **P24 Table (F3)** — Table (native `<table>` inside a responsive
    `goth-table-wrapper` scroll container) + TableCaption/TableHeader/TableBody/
    TableFooter/TableRow/TableCell + TableHead (`<th>` with `scope="col"` default,
    `Scope:"row"`, or `"none"` to omit).
  Added a Field-local `FieldOrientation` enum (vertical zero value) because the shared
  package `Orientation` defaults to horizontal, which would misrepresent a field's
  natural vertical default; every other typed enum (InputType, InputGroupAlign) carries
  `Valid()` + a zero-value default (unknown → default render, no panic). All Props embed
  the frozen `Base` (single-spread `MergeAttributes`, `class`-in-`Attributes` rejected,
  `Base.ID` honored). **Showcase.** `examples/goth-showcase/internal/showcase/
  specimens_primitives_forms.go` registers one specimen per primitive rendering the REAL
  component; the eight ids were appended to `ImplementedPrimitives` so
  `TestEveryImplementedPrimitiveHasSpecimen` stays load-bearing. The Field specimen is a
  real `<form method="get">` (native, no-JS submission specimen) wiring label `for`/id +
  `aria-describedby` across text/email(invalid+error)/select/textarea/fieldset-radio
  fields with a submit button. **Verify results (exact):** `cd ui/goth && go build ./...
  && go test ./... && go vet ./...` PASS (new `primitives/forms_test.go` proves data
  hooks, no inline `style=` across all eight incl. Progress, the password no-echo /
  text-echo contract, Progress value clamp + native value/max + indeterminate, native
  select option/optgroup, the Field ARIA linkage, input-group align/role/icon-button
  aria-label, table scope defaults, enum `Valid()`, and the Base merge/class/ID
  contract). `cd examples/goth-showcase && go build ./... && go vet ./... && go test ./...`
  PASS (catalog registration + strict-CSP specimen serving). `make test-ui-browser` PASS —
  **36 passed (12 specs × Chromium/Firefox/WebKit)**; axe crawls every specimen (incl.
  all 8 new pages) with zero violations in all three engines, and the strict-CSP spec
  shows zero `securitypolicyviolation`/console errors. `make check` exit 0 ("all checks
  passed" — templ drift clean/no-op, committed `dist/`+manifest in sync, all modules
  vet/build/test, all guards incl. G17/G18 ui outward-import + go.mod discipline) and
  `make guard` exit 0. Contrast note: the first axe pass surfaced the same muted-on-muted
  finding class as GOTH-2.1/2.2 — the InputGroupText addon (`muted-foreground` on `muted`)
  failed AA; fixed at source by moving `goth-input-group-text` to `--foreground` (no
  theme-token change, dist regenerated, drift check intact). **Not done (by design):** the
  human "run-and-look" visual pass (narrow/wide × light/dark × RTL) is Layer 4 owner work;
  the automated axe/CSP three-engine browser proof is green.

#### GOTH-2.4 — close the 26-entry foundation wave

- **depends_on:** GOTH-2.2, GOTH-2.3
- **model:** fable for audit, opus for remediation
- **files:** `ui/goth/catalog.md`, phase sources/tests/docs
- **work:** Audit P01–P26 against the parity definition, remediate findings,
  capture visual review, and mark only passing rows complete.
- **verify:** module suite; `make test-ui-browser`; `make check && make guard`.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-2.2 and GOTH-2.3
  both DONE (2026-07-17); GOTH-2.4 was the sole dependency-ready Phase 2 task.
  Branch `authorization-v3`; unrelated worktree (authorization-v3 uncommitted work,
  the untracked `.claude/plans/ui-goth/`, the untracked `ui/` + `examples/goth-showcase/`
  trees, the untracked authentication `policy.go`/`policy_test.go`/`html_policy_test.go`)
  left intact — the only new files are Layer-4 review artifacts under the already-untracked
  `examples/goth-showcase/e2e/screenshots/` plus its `capture-screenshots.mjs` driver.
  **Adversarial audit — P01–P26 against the full parity definition (family shape,
  props/variants vs the dated Shadcn baseline, a11y contract, no-JS behavior,
  data-slot/state/variant surface, token-only styling, RTL/reduced-motion).** Every
  row was verified at source (the `.templ`/`.go` pairs, `components.css`, the showcase
  registry) and by run-and-look. Result: **no primitive fails the parity definition;
  no Phase 2 task was reopened.** Grouped findings:
  - **Family/shape + data hooks (all 26):** each matches its frozen catalog family
    (F1=11 leaf, F2=6 slotted, F3=9 compound across the wave) and emits the stable
    `goth-<primitive>` class + `data-slot`/`data-variant`/`data-state`/`data-orientation`/
    `data-tone`/`data-invalid` surface through the single-spread `ownedAttrs`→
    `MergeAttributes` merge (owned keys win, `class` in `Attributes` dropped, `Base.ID`
    honored). Typed enums all carry `Valid()` + a zero-value default (unknown → default
    render, no panic).
  - **No-JS baseline + semantics:** Input/Textarea/NativeSelect/Field/InputGroup submit
    natively; Progress rides native `value`/`max` geometry (never inline `style=`);
    Pagination/Breadcrumb are real server-owned `<nav>`/links with `aria-current="page"`
    on the current page; Button defaults to `type="button"` and degrades a URL+submit/
    disabled conflict to a marked native `<button>` rather than a spec-violating anchor;
    the icon-only Button/InputGroupButton require `Label`→`aria-label` (render-time
    contract). Password Input proven never to echo `Value`.
  - **Token-only styling + motion:** `components.css` contains **zero** hardcoded
    hex/rgb/hsl literals — every color/space/radius/shadow reads a frozen `--token`; a
    single `@media (prefers-reduced-motion: reduce)` block collapses all three kit
    animations (skeleton pulse, spinner rotate, progress-indeterminate) and the button/
    input/textarea transitions. No primitive emits an inline `style=` (invariant a).
  - **RTL:** the Direction (P09) primitive propagates native `dir` with no script and
    the showcase RTL specimen right-aligns correctly. Three physical-property recipes
    (Typography blockquote `border-left`/list `padding-left`, ButtonGroup horizontal
    radii/`margin-left`) are **not** RTL-mirrored, but they reproduce the dated Shadcn
    baseline recipes verbatim, so they are at parity; a future logical-property
    (`border-inline-start`/`margin-inline`) pass is recorded as a Phase 7 polish note,
    not a wave-2 blocker.
  **Verification (exact):** `cd ui/goth && go build ./... && go test ./... && go vet ./...`
  PASS. `make generate` clean (no `*.templ`/`*_templ.go` git drift). `make generate-ui-assets`
  reproducible — `dist/` + `manifest.json` byte-identical (empty `git diff`). `make guard`
  exit 0 (18 guards incl. G17/G18 ui outward-import + go.mod discipline). `make check` exit
  0 ("all checks passed"). `make test-ui-browser` **36 passed (12 specs ×
  Chromium/Firefox/WebKit)** — axe crawls every specimen (all 26 primitive pages) with
  zero violations in all three engines, strict-CSP spec shows zero
  `securitypolicyviolation`/console errors, HTMX success/validation/error/history + Alpine
  CSP runtime specs green.
  **Layer-4 run-and-look (viewed, not asserted from exit codes):** the showcase server was
  launched and driven with Playwright/Chromium; ~50 curated full-page screenshots were
  captured at narrow (390px) / wide (1280px) × light / dark × one RTL specimen and stored
  under `examples/goth-showcase/e2e/screenshots/`, then opened and inspected. Confirmed:
  Button (all six variants + sizes + disabled/loading spinner), Card (bordered surface,
  title/desc/badge/footer), Field (invalid email red border + error text, radios/select/
  textarea, submit), Table (header/rows/footer total, responsive scroll container at 390px),
  Input Group (joined prefix/suffix addons), Pagination (active page outlined, ellipsis,
  edge chevrons), Alert (default/success/warning/destructive semantic borders), Avatar
  (image + initials fallbacks), Marker (status dots + labelled separator), Breadcrumb
  (ellipsis collapse), Button Group (horizontal/vertical joins), Native Select (invalid
  red border), Typography (real h1–h4/lead/blockquote/list/code recipes), Empty (dashed
  media/title/desc/action), Item (default/outline/muted/link variants — muted de-emphasis
  fix visible), Progress (determinate fills), Kbd (keycaps), Spinner (status). **Dark mode
  proven at the component level:** the dark-injected Card and Field restyle their owned
  surfaces to the dark tokens (near-black `--card`/`--input`, white text) with the invalid
  red border preserved. Observation (non-blocking, showcase-host not primitive): the
  showcase `<body>` does not paint `--background`/`--foreground`, so bare text placed
  directly on the page (not inside a component surface) is low-contrast under injected dark
  and the minimal theme-light/theme-dark sample specimens look alike; the shipped
  light-served specimens are axe-clean and every component-owned surface restyles
  correctly, so this is a GOTH-1.5 host-polish item, not a P01–P26 parity defect.
  **Actions:** catalog.md P01–P26 flipped `planned`→`accepted` (26 accepted / 38 planned;
  count invariant intact); plan.md parity-matrix rows P01–P26 checked (P27–P64 untouched);
  this note appended; GOTH-2.4 checked in TASKS.md; Phase 2 gate marked MET and the board
  pointer advanced to Phase 3 (GOTH-3.1/3.2/3.3 dependency-ready). **Blocked:** none.
  **Notes (follow-ups, not silently fixed):** (1) logical-property RTL pass for the three
  physical-property recipes; (2) showcase `<body>` background/foreground painting so the
  standalone dark specimen and bare on-page text render dark; (3) Avatar CSS-only image
  load-failure reveals the broken-image/alt rather than the fallback (fallback shows only
  when `Src` is absent) — an honest StylesOnly-baseline limitation, a JS `onerror`
  enhancement is out of scope for this wave.

### Phase 3 — disclosure and selection

#### GOTH-3.1 — implement disclosure primitives P27, P29, P34

- **depends_on:** phase 2 gate
- **model:** opus
- **files:** Accordion, Collapsible, Tabs sources/controllers/tests/showcase
- **work:** Share disclosure and roving-focus mechanics without forcing one
  state model onto all three primitives.
- **verify:** keyboard, controlled/default, no-JS, reduced-motion, and RTL tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 2 gate MET
  (GOTH-2.4 wave audit accepted P01–P26). No new frozen controller surface — P27
  and P29 use the already-frozen `gothCollapse`, P34 uses the frozen `gothTabs`
  (README §8 set unchanged; no new `x-data` name introduced). Unrelated worktree
  state preserved; the whole `ui/` tree remains untracked as before.
  **Progressive baseline is real per primitive:**
  - **P29 Collapsible (family F4)** — native `<details>`/`<summary>`. The summary
    toggles the region with pointer and keyboard (Enter/Space) and native focus
    with NO JavaScript; `Open` renders the native `open` attribute so a
    server-open region is readable no-JS. `x-data="gothCollapse"` is purely
    additive: it mirrors the native open state onto `data-state` and dispatches
    `goth:open`/`goth:close`, never owning disclosure. Parts: `Collapsible`,
    `CollapsibleTrigger`, `CollapsibleContent` (data-slot collapsible /
    collapsible-trigger / collapsible-content, plus a rotating chevron glyph).
  - **P27 Accordion (family F4)** — each `AccordionItem` is a native `<details>`.
    Single mode is achieved WITHOUT JavaScript via the native `<details name>`
    exclusive group (siblings sharing `Name` keep only one open); multiple mode
    omits `Name`. `gothCollapse` enhances each item's `data-state`/events. Parts:
    `Accordion` (data-type single/multiple), `AccordionItem`, `AccordionTrigger`
    (native `<summary>`; Tab traverses headers, Enter/Space toggles), and
    `AccordionContent`. `AccordionType` is a validated enum (zero value = single).
  - **P34 Tabs (family F4)** — the SERVER owns the active tab: the active
    `TabsContent` renders visible and the rest `hidden`, so the active panel is
    readable no-JS (baseline). `x-data="gothTabs"` adds roving focus
    (arrow/Home/End, single tab stop) and manual/automatic activation
    (`data-activation`, default automatic); it switches `aria-selected`/
    `data-state`/panel `hidden` and dispatches `goth:change`. ARIA linkage is
    caller-passed via `Base.ID` (each `TabsTrigger` sets `Controls`; each
    `TabsContent` sets `Labelledby`). `TabsOrientation`/`TabsActivation` are
    validated enums with zero-value defaults.
  **Shared-mechanics reuse + GOTH-1.5 audit fix at source:** `gothTabs` reuses the
  shared `createRovingFocus` mechanic (extended with an optional `onMove` hook for
  automatic activation — backward-compatible). While touching the controllers I
  applied the GOTH-1.5 descendant-`$el` audit directive: `gothTabs` was carrying
  the flagged bug — its `activate` panel query used `this.$el` (bound to the
  clicked tab when invoked from the tab's `x-on:click`), so panels never toggled;
  fixed by caching `this._root` at init and querying panels/tabs against the root.
  `gothCollapse` was rewritten from the old div+`hidden`+`toggle()` model (which
  had no no-JS baseline) to the native-`<details>` enhancement model, and it caches
  `this._root` at init and listens for the native `toggle` event. (Reported latent:
  `menu.js`/`dialog.js`/`combobox.js` `show()`/`hide()` use `this.$el` outside
  `init()` — the same pattern — but those controllers are not wired to any
  primitive until Phase 4, are untested, and are out of GOTH-3.1 scope; flagged for
  GOTH-4.1/4.2/4.4 rather than changed unverified.) The GOTH-1.4/1.5 foundation
  `profile-interactive` specimen and its `runtime.spec.ts` assertion were updated
  to the new native-`<details>` `gothCollapse` model (dogfooding), since the
  controller contract changed.
  **CSS + generation:** component rules added to `assets/src/css/components.css`
  keyed off the stable `goth-<primitive>`/`data-slot`/`data-state`/`[open]` surface
  and the frozen tokens; no rule emits an inline `style=` (dynamic chevron rotation
  is `data-state`/`[open]` + CSS transition, reduced-motion-disabled). Native
  `<details>` UA hiding is the honest collapse baseline (a `::details-content`
  height animation was authored then removed for cross-engine reliability — height
  animation of native `<details>` is engine-inconsistent; deferred as an optional
  progressive enhancement, flagged for the GOTH-3.4 audit). `make generate` (templ)
  and `make generate-ui-assets` (Node-gated) regenerated; the asset build is
  byte-identical on re-run (theme.3bf6b227.css / runtime.74f7133e.js /
  htmx.8689e2e2.js). Generated `*_templ.go`/dist never hand-edited.
  **Verify results:**
  - `cd ui/goth && go build ./... && go test ./... && go vet ./...` — PASS
    (`primitives` render/contract suite incl. `disclosure_test.go`: no-inline-style,
    native-baseline, single-mode-name exclusivity, tab roles/ARIA/hidden baseline,
    enum `Valid()` tables, and the Base merge/`x-data`-owned-wins contract).
  - `examples/goth-showcase` `go build`/`go test` — PASS (registry completeness:
    P27/P29/P34 added to `ImplementedPrimitives` with Interactive-profile specimens;
    strict-CSP per-specimen test green).
  - `make test-ui-browser` — **45 passed** across Chromium/Firefox/WebKit (was 36):
    the new `disclosure.spec.ts` proves, per engine, the P29 native toggle +
    keyboard Enter + `data-state` mirror + server-open baseline, P27 native
    single-open exclusivity + `data-state`, and P34 no-JS active-panel baseline +
    roving focus + ArrowRight/Home automatic activation + pointer switch; the
    updated `runtime.spec.ts` interactive-collapse spec green; axe (crawls every
    specimen incl. the three new ones) and strict-CSP/no-eval/no-remote-origin green
    (no `unsafe-eval`/`unsafe-inline`; the CSP-safe Alpine build boots and the
    `activate($event)`/`onKeydown($event)` handlers run under `script-src 'self'`).
    The single whole-catalog axe crawl test was marked `test.slow()` (3x budget) as
    it grew past WebKit's 30s default with three added specimens — the zero-violation
    assertion is unchanged.
  - `make check` — PASS ("all checks passed": templ no-op, per-module
    vet/build/test across every module, integration-tag vet, and all guards).
  - `make guard` — PASS (all guards incl. G17 `ui/` outward-import discipline).
  **Blocked:** none. **Notes:** parity-matrix rows P27/P29/P34 and catalog status
  intentionally left unchecked/`planned` until the GOTH-3.4 wave audit (per the
  wave protocol). Native-`<details>` height animation and the RTL arrow-direction
  nuance for tabs (logical next=ArrowRight retained) are flagged as GOTH-3.4 audit
  follow-ups, and the three latent `this.$el` menu/dialog/combobox controllers are
  flagged for Phase 4.

#### GOTH-3.2 — implement form-selection primitives P28, P31–P33

- **depends_on:** phase 2 gate
- **model:** opus
- **files:** Checkbox, Radio Group, Slider, Switch sources/controllers/tests/showcase
- **work:** Preserve native submitted values and validation while adding visual
  state, keyboard, indeterminate, orientation, and multi-thumb behavior.
- **verify:** real form POST fixture plus keyboard/touch/pointer browser tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 2 gate MET;
  GOTH-3.1 complete. **No new controller surface** — none of these four primitives
  binds an Alpine controller (README §8 frozen set unchanged, no new `x-data` name).
  Native form semantics are the submitted source of truth for every one; the styled
  surfaces are drawn by component CSS off native pseudo-classes/`data-*`, never an
  inline `style=`. Unrelated worktree state preserved; the `ui/` tree stays
  untracked. **Progressive baseline is real per primitive (all no-JS):**
  - **P28 Checkbox (family F1)** — one native `<input type="checkbox">`
    (`data-slot="checkbox"`). Toggles/keys/focus/submits natively with no JS; the
    styled box + tick/dash are CSS off `:checked` and `data-state` via a
    pseudo-element (no data-URI image, so the strict `img-src 'self'` is unchanged).
    `data-state` = checked/unchecked/indeterminate. Indeterminate is a visual-only
    server state: no `aria-checked="mixed"` (it conflicts with a native checkbox's
    own state and axe flags it, and it is unreachable no-JS); true AT "mixed" needs
    the native `.indeterminate` DOM property, documented as an optional host
    enhancement.
  - **P33 Switch (family F1, frozen)** — native `<input type="checkbox"
    role="switch">` (`data-slot="switch"`), checkbox-backed with NO controller per
    the frozen §7 Switch/Toggle asymmetry. The track/thumb are CSS (a
    `background-position` slide of a radial-gradient thumb, no pseudo-element, no
    JS). Submits natively.
  - **P31 Radio Group (catalog family F4)** — compound `RadioGroup` +
    `RadioGroupItem` (native radios sharing one `Name`). The native radio group IS
    the complete widget: single selection, arrow-key navigation, roving focus, and
    submission are all native, so **no controller is bound** (binding `gothRovingFocus`
    would double-handle and break the native keyboard model — the task's
    "keyboard/focus per the native element" rule). `RadioGroup` is
    `role="radiogroup"` with `data-orientation` (vertical zero value / horizontal),
    label it via `Base.Attributes["aria-labelledby"]`. `RadioOrientation` is a
    validated enum. **Family-label nuance:** catalog freezes P31 as F4
    (controller-backed) but native radios need no controller; shipped native-only,
    consistent with GOTH-3.1's F4-native-baseline precedent. Not a new frozen
    surface (no controller added, no frozen doc edited); flagged for the GOTH-3.4
    wave audit to reconcile the family column.
  - **P32 Slider (catalog family F4)** — single-thumb native `<input type="range">`
    (`data-slot="slider"`) with deterministic min/max/step/value (zero value = a
    usable 0–100 range). Submits natively; the full native range keyboard model
    (arrows, Home/End, PageUp/PageDown) and focus work with no JS. Track/thumb are
    styled through the vendor range pseudo-elements. **Policy (frozen for this
    milestone, per the plan's "multi-thumb policy"):** single-thumb native only —
    multi-thumb cannot be a native single range and would require a dedicated
    controller (new runtime/frozen surface), so it is out of scope; `<output>`
    shows the value (live sync deferred, needs JS). Same F4-vs-native family nuance
    as P31, flagged for GOTH-3.4.
  **CSS + generation:** component rules added to `assets/src/css/components.css`
  keyed off the stable `goth-checkbox`/`goth-switch`/`goth-radio-group(-item)`/
  `goth-slider` + `:checked`/`data-state`/`aria-invalid` surface and the frozen
  tokens; no rule emits inline `style=`; the checkbox tick/dash use a
  pseudo-element (not a data-URI image) so the strict `img-src 'self'` CSP is not
  widened; reduced-motion disables the checkbox/switch/radio transitions. `make
  generate` (templ) and `make generate-ui-assets` (Node-gated) regenerated;
  `runtime.js`/`htmx.js` hashes are unchanged (no controller touched), only
  `theme.css` re-fingerprinted (`theme.97c029a9.css`). Generated `*_templ.go`/dist
  never hand-edited.
  **Verify results:**
  - `cd ui/goth && go build ./... && go test ./... && go vet ./...` — PASS
    (`primitives` suite incl. `selection_test.go`: no-inline-style, native-submission
    for all four, three-state checkbox `data-state`, `role="switch"`, radiogroup +
    shared-name native radios, deterministic slider min/max/step/value, enum
    `Valid()` table, Base merge/`class`-dropped/owned-wins contract).
  - `examples/goth-showcase` `go build`/`go test`/`go vet` — PASS (registry
    completeness: P28/P31/P32/P33 added to `ImplementedPrimitives` with StylesOnly
    specimens; the `/selection/echo` GET route + no-JS `selection-form` specimen;
    strict-CSP per-specimen test green).
  - `make test-ui-browser` — **60 passed** across Chromium/Firefox/WebKit (was 45):
    the new `selection.spec.ts` proves, per engine and with an empty page-error/
    console guard, P28 native pointer + Space toggle + indeterminate `data-state`,
    P33 `role="switch"` native toggle + disabled, P31 native single-selection +
    ArrowDown roving selection + disabled-item skip, P32 native ArrowRight/Home/End
    range keyboard + stepped increment, and a real no-JS `<form>` GET submission
    echoing `terms=yes&notify=on&plan=free&volume=30`; the whole-catalog axe crawl
    (incl. the five new specimens) and strict-CSP/no-eval/no-remote-origin sweep
    stay green (an early `aria-checked="mixed"` axe violation and a data-URI
    `img-src` CSP refusal were both found by the harness and fixed at source).
  - `make check` — PASS ("all checks passed": templ no-op, `dist/`+manifest no
    drift, per-module vet/build/test, integration-tag vet, all guards).
  - `make guard` — PASS (exit 0, all guards incl. G17/G18 `ui/` outward-import +
    require-whitelist discipline).
  **Blocked:** none. **Notes:** parity-matrix rows P28/P31/P32/P33 and catalog
  status intentionally left unchecked/`planned` until the GOTH-3.4 wave audit.
  Flagged for GOTH-3.4: (1) the catalog F4 family label for P31 Radio Group and P32
  Slider vs the shipped native-only (no-controller) reality — reconcile the family
  column or record the native-baseline rationale; (2) Slider multi-thumb + live
  `<output>` sync and true AT-indeterminate for Checkbox as optional JS
  enhancements deliberately deferred from the no-JS baseline.

#### GOTH-3.3 — implement compact selection primitives P30, P35–P36

- **depends_on:** phase 2 gate
- **model:** opus
- **files:** Input OTP, Toggle, Toggle Group sources/controllers/tests/showcase
- **work:** Implement paste/navigation/autocomplete for OTP and honest pressed/
  submitted-value contracts for toggles.
- **verify:** paste/backspace/focus browser tests, form submission, axe, no
  secret echo in validation specimens.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 2 gate MET;
  GOTH-3.1 and GOTH-3.2 complete. **No new controller surface** — the only Alpine
  binding used is the already-frozen `gothRovingFocus` (README §8 set unchanged, no
  new `x-data` name); Toggle and Input OTP bind no controller. Native form semantics
  are the submitted source of truth for every one; the styled surfaces are drawn by
  component CSS off native pseudo-classes/`:has()`/`data-*`, never an inline
  `style=`. Unrelated worktree state preserved; the `ui/` tree stays untracked.
  **Progressive baseline is real per primitive:**
  - **P30 Input OTP (catalog family F4; shipped native-only)** — `InputOTP`
    (`role="group"`, `data-slot="input-otp"`) + `InputOTPSlot` (native
    `<input type="text" inputmode="numeric" maxlength="1">`, `data-slot="input-otp-slot"`)
    + decorative `InputOTPSeparator` (`aria-hidden`). Each slot accepts one character,
    native Tab/Shift-Tab move between slots, native Backspace clears within a slot,
    and every slot submits its value in a plain HTML form with NO JavaScript;
    `autocomplete="one-time-code"` on the first slot lets the browser offer a code.
    **No secret echo:** the invalid-state specimen renders empty `aria-invalid` slots
    and a generic digit-free `FieldError` (no entered code echoed), and the compact
    echo route reports the OTP by slot COUNT only, never the digits. **Deferral
    (frozen for this wave):** auto-advance and paste-distribution across slots require
    a dedicated controller (new runtime/frozen surface, out of the frozen §8 set), so
    they are out of scope; native paste into a slot and native navigation are the
    shipped model. Same F4-vs-native family nuance as P31/P32 — flagged for GOTH-3.4
    to reconcile the catalog family column.
  - **P35 Toggle (catalog family F4; checkbox-backed, no standalone controller)** —
    per the frozen Switch/Toggle asymmetry (README §7 / §8, Gate B), the toggle
    *button* is a native `<input type="checkbox">` wrapped in the styled `<label>`
    (`data-slot="toggle"`), so the pressed state submits and toggles with the native
    Space key and pointer with NO JavaScript. The pressed visual is drawn by CSS off
    the input's `:checked` via `:has()`; the accessible pressed state is the native
    checkbox's checked state (honest checkbox-backed equivalent of `aria-pressed`, no
    invalid ARIA). `data-state` = checked/unchecked; `ToggleVariant`
    (default/outline) and `ToggleSize` (sm/default/lg) are validated enums with
    zero-value defaults. The "controller only enhances" clause of the frozen decision
    is realized through the Toggle Group sibling (P36) roving focus, not a standalone
    `gothToggle` — no such controller was created (it would be new frozen surface).
  - **P36 Toggle Group (catalog family F4; gothRovingFocus)** — `ToggleGroup`
    (`role="group"`, `data-type` single/multiple, `data-orientation`, `data-variant`/
    `data-size`) + `ToggleGroupItem` (checkbox-backed for multiple, radio-backed for
    single; `data-slot="toggle-group-item"` on the label, `data-slot="item"` on the
    input for roving discovery). Selection has a submittable no-JS representation for
    BOTH modes: **single** = native radios sharing one Name (native single selection,
    arrow navigation, roving focus, submission — **no controller bound**, exactly the
    P31 precedent: binding `gothRovingFocus` to native radios would double-handle and
    break the native keyboard model); **multiple** = native checkboxes enhanced by
    `x-data="gothRovingFocus"` + `x-on:keydown="onKeydown($event)"` for a single tab
    stop + arrow navigation (which native checkboxes lack), with native Space toggling
    each and multi-selection submitting each pressed value. `ToggleGroupType`/
    `ToggleGroupOrientation` are validated enums (zero values single/horizontal).
    **Row nuance:** the plan row says "gothRovingFocus for single/multiple"; single
    ships native-radio-roving without the controller (native already provides the
    single tab stop) and only multiple binds it — flagged for GOTH-3.4 to record the
    native-baseline rationale in the family column.
  **CSS + generation:** component rules added to `assets/src/css/components.css` keyed
  off the stable `goth-input-otp(-slot/-separator)`/`goth-toggle(-input)`/
  `goth-toggle-group(-item/-input)` + `:checked`/`:has()`/`data-*`/`aria-invalid`
  surface and the frozen tokens (incl. `--font-mono` for the OTP figure); no rule
  emits an inline `style=`; the strict `img-src 'self'` is unchanged (no data-URI);
  reduced-motion disables the new transitions. `make generate` (templ) and
  `make generate-ui-assets` (Node-gated) regenerated; only `theme.css` re-fingerprinted
  (`theme.9db00039.css`) — `runtime.js`/`htmx.js` hashes are unchanged because the only
  controller used (`gothRovingFocus`) already existed. Generated `*_templ.go`/dist never
  hand-edited.
  **Verify results:**
  - `cd ui/goth && go build ./... && go test ./... && go vet ./...` — PASS
    (`primitives` suite incl. `compact_test.go`: no-inline-style for all six parts,
    native OTP slots + autocomplete + no-echo invalid slots, checkbox-backed Toggle
    submission + variant/size, single-mode native radios (no `x-data`) + multiple-mode
    `gothRovingFocus` + `x-on:keydown`, radio-vs-checkbox item type, enum `Valid()`
    tables + default fallback, and the Base merge/`class`-dropped/owned-wins contract).
  - `examples/goth-showcase` `go build`/`go test`/`go vet` — PASS (registry
    completeness: P30/P35/P36 added to `ImplementedPrimitives` with specimens — OTP &
    Toggle StylesOnly, Toggle Group Interactive; the `/compact/echo` GET route + no-JS
    `compact-form` specimen; strict-CSP per-specimen test green).
  - `make test-ui-browser` — **72 passed** across Chromium/Firefox/WebKit (was 60):
    the new `compact.spec.ts` proves, per engine and with an empty page-error/console
    guard, P30 native single-char typing + `maxlength` (no auto-advance) + Tab between
    slots + Backspace + one-time-code autocomplete + a digit-free invalid group (no
    secret echo), P35 pointer + Space checkbox-backed toggle + server-pressed + disabled,
    P36 single native-radio arrow selection and multiple `gothRovingFocus` single-tab-stop
    + Space multi-select, and a real no-JS `<form>` GET submission echoing
    `bold=on&align=center` with `otp-count=6` (digits not echoed); the whole-catalog
    axe crawl (incl. the four new specimens) and strict-CSP/no-eval/no-remote-origin
    sweep stay green (an early axe unlabeled-input violation on the OTP slots was found
    by the harness and fixed at source with per-slot `aria-label`s).
  - `make check` — PASS ("all checks passed": templ no-op, `dist/`+manifest no drift,
    per-module vet/build/test, integration-tag vet, all guards).
  - `make guard` — PASS (exit 0, all guards incl. G17/G18 `ui/` outward-import +
    require-whitelist discipline).
  **Blocked:** none. **Notes:** parity-matrix rows P30/P35/P36 and catalog status
  intentionally left unchecked/`planned` until the GOTH-3.4 wave audit. Flagged for
  GOTH-3.4: (1) the catalog F4 family label for P30 Input OTP, and the "single/multiple"
  `gothRovingFocus` wording for P36, vs the shipped native-baseline reality (OTP
  native-only; P36 single native-radio, only multiple controller-backed) — reconcile
  the family column or record the native-baseline rationale; (2) OTP auto-advance +
  paste-distribution as an optional controller enhancement deliberately deferred from
  the no-JS baseline (needs a new frozen controller name — stop-and-report surface, not
  added here).

#### GOTH-3.4 — close the 10-entry stateful wave

- **depends_on:** GOTH-3.1, GOTH-3.2, GOTH-3.3
- **model:** fable for audit, opus for remediation
- **files:** catalog and phase sources/tests/docs
- **work:** Run the parity/accessibility/CSP audit for P27–P36 and remediate.
- **verify:** `make test-ui-browser`; `make check && make guard`.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-3.1, GOTH-3.2,
  GOTH-3.3 all DONE (2026-07-17); GOTH-3.4 was the sole dependency-ready Phase 3
  task. Branch `authorization-v3`; unrelated worktree (authorization-v3 uncommitted
  work, the untracked `.claude/plans/ui-goth/`, the untracked `ui/` +
  `examples/goth-showcase/` trees, the untracked authentication
  `policy.go`/`policy_test.go`/`html_policy_test.go`) left intact.
  **Adversarial audit — P27–P36 against the full Phase-3 gate (native form,
  keyboard/focus incl. roving where frozen, RTL, reduced-motion, CSP, family
  shape).** Every row verified at source (`.templ`/`.go`, `components.css`, the
  showcase registry, the controllers/mechanics JS) and by run-and-look. Result:
  **no primitive fails the gate; no Phase 3 primitive task (GOTH-3.1/3.2/3.3) was
  reopened.** One shared-mechanic source fix was applied under board rule 3 (see
  Tabs RTL below). Per-row:
  - **P27 Accordion (F4):** native `<details>`/`<summary>`; single-mode exclusivity
    is native `<details name>` (no JS); Tab traverses headers, Enter/Space toggles
    natively; `gothCollapse` only mirrors `data-state`/events. Chevron rotates off
    `[open]` via CSS transition, collapsed under reduced-motion. PASS.
  - **P28 Checkbox (F1):** native `<input type=checkbox>`; checked/unchecked/
    indeterminate/disabled/invalid render from `:checked`/`data-state`/
    `aria-invalid` (pseudo-element tick, no data-URI — strict `img-src 'self'`
    intact); native Space + submit; no `aria-checked="mixed"` (documented AT-mixed
    deferral). PASS.
  - **P29 Collapsible (F4):** native `<details>`; server-`Open` baseline readable
    no-JS; `gothCollapse` additive. PASS.
  - **P30 Input OTP (F4, shipped native-only):** grouped native single-char inputs,
    `one-time-code` autocomplete, native Tab/Backspace; **no secret echo** (invalid
    group is generic + digit-free, echo route reports count only). PASS.
  - **P31 Radio Group (F4, native):** native radios sharing one `Name` — single
    selection, arrow roving, disabled-item skip, submission all native; no
    controller (binding one would double-handle native keyboard). PASS.
  - **P32 Slider (F4, native):** single-thumb `<input type=range>`, full native
    range keyboard, `<output>`; deterministic min/max/step/value. PASS.
  - **P33 Switch (F1):** native `<input type=checkbox role=switch>`, on/off/disabled,
    native submit. PASS.
  - **P34 Tabs (F4):** server-owned active panel (no-JS baseline visible/hidden);
    `gothTabs` roving + manual/automatic activation, ARIA linkage caller-passed.
    **RTL arrow-direction FIXED at source** (see below). PASS.
  - **P35 Toggle (F4, checkbox-backed):** native checkbox in a styled `<label>`,
    pressed via `:checked`/`:has()`, variant/size enums; native Space + submit. PASS.
  - **P36 Toggle Group (F4):** single = native radios (no controller); multiple =
    native checkboxes + `gothRovingFocus` (single tab stop + arrows); both submit
    natively. PASS.
  **Disposition of the three flagged reconciliation items (recorded, not silent):**
  1. **Catalog family labels (P30/P31/P32/P36 native vs F4; P36 single-mode
     native-radio).** Disposition: **recorded, column intact.** The frozen `family`
     column is a Gate-B surface and was NOT edited. Instead the catalog F4 definition
     now records the rationale: *F4 marks the family whose parity MAY need a
     controller, not a mandate to bind one; a fully-sufficient native baseline
     satisfies the row, and a controller is added only where enhancement is genuinely
     required.* This matches GOTH-3.1's F4-native precedent and needs no owner
     decision. No column value changed.
  2. **Deferred enhancements.** Recorded as deferred, NOT added: OTP auto-advance +
     paste-distribution (requires a NEW named Alpine controller = new frozen §8
     surface → owner-gated stop-and-report, not created here); native-`<details>`
     height animation (engine-inconsistent, GOTH-3.1 deferral); Slider multi-thumb +
     live `<output>` sync; Checkbox true AT `.indeterminate` (mixed) DOM enhancement.
     None blocks the P27–P36 gate.
  3. **Tabs RTL arrow-direction — FIXED at source.** The flagged nuance was a real
     defect: the shared `mechanics/roving.js` hardcoded `ArrowRight=next` for
     horizontal orientation regardless of `dir`, so under RTL the arrow keys did not
     follow the visual layout (contrary to Radix/shadcn parity and the WAI-ARIA RTL
     note). A failing RTL Playwright test was written first (chromium: ArrowLeft did
     not advance), then `createRovingFocus` was made direction-aware — horizontal
     `next`/`prev` resolve per keydown from the focused item's computed `direction`
     (RTL → ArrowLeft advances, ArrowRight retreats); vertical is unchanged. This is
     an internal mechanic change (no frozen §8 controller name/event added) fixing
     both Tabs and any horizontal `gothRovingFocus`; the LTR tabs test still passes.
     `runtime.js` re-fingerprinted `74f7133e`→`501ddb71`; `theme.css`/`htmx.js`
     hashes unchanged. A permanent `primitive-tabs-rtl` specimen (Dir=RTL) + a
     three-engine RTL disclosure test were added.
  **Verification (exact):** `cd ui/goth && go build ./... && go test ./... &&
  go vet ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS.
  `make generate` clean (no templ drift). `make generate-ui-assets` reproducible —
  after the roving fix `dist/`+`manifest.json` are byte-identical on a second run.
  `make guard` exit 0 (18 guards incl. G17/G18 ui outward-import). `make check` exit
  0 ("all checks passed"). `make test-ui-browser` **75 passed** (was 72; +1 RTL tabs
  test × 3 engines) across Chromium/Firefox/WebKit — the whole-catalog axe crawl and
  strict-CSP/no-eval/no-remote-origin sweep now include the new `primitive-tabs-rtl`
  specimen with zero violations; the RTL test proves ArrowLeft advances under
  `dir=rtl` in all three engines.
  **Layer-4 run-and-look (viewed, not asserted from exit codes):** the showcase
  server was launched and driven with Playwright/Chromium; ~22 curated full-page
  screenshots of the stateful specimens were captured (open/closed, checked/
  unchecked/indeterminate/disabled/invalid, active-tab, light/dark, RTL) under
  `examples/goth-showcase/e2e/screenshots/phase3/` via `capture-phase3.mjs`, then
  opened and inspected. Confirmed: Accordion (first item open with up-chevron,
  single/multiple, dark tokens restyle the surface), Collapsible (closed + server-
  open, then click-open), Tabs LTR (Account active underline → Password active on
  switch, dark), Tabs RTL (list lays out right-to-left, Account rightmost active,
  ArrowLeft moves the active/focus to Password on its left — the fix is visually
  correct), Checkbox (all five states incl. red invalid + error text), Switch
  (off/on/disabled), Radio Group (single-select Free + horizontal S/M/L with M),
  Slider (thumbs + `<output>` 60, dark), Input OTP (6 slots + separator, red invalid
  slots with a digit-free error — no secret echo), Toggle (B/I/U/Large pressed vs
  Off), Toggle Group (single Left + multiple B/I/U; dark shows pressed = filled).
  **Actions:** catalog.md P27–P36 flipped `planned`→`accepted` (36 accepted / 28
  planned; count invariant intact) + F4-native rationale recorded; plan.md
  parity-matrix rows P27–P36 checked; this note appended; GOTH-3.4 checked in
  TASKS.md; Phase 3 gate marked MET and the board pointer advanced to Phase 4
  (GOTH-4.1 dependency-ready). **Blocked:** none. **Notes (follow-ups, not silently
  fixed):** (1) OTP auto-advance/paste-distribution controller — owner-gated new §8
  surface; (2) native-`<details>` height animation; (3) Slider multi-thumb + live
  `<output>` sync; (4) Checkbox AT `.indeterminate` (mixed) DOM enhancement; (5) the
  three latent `this.$el` menu/dialog/combobox controllers still flagged for Phase 4.

### Phase 4 — overlays and navigation

#### GOTH-4.1 — freeze shared overlay/menu mechanics

- **depends_on:** phase 3 gate
- **model:** opus
- **files:** shared Alpine controllers, document/runtime CSS, test fixtures
- **work:** Implement focus trap/restore, inert/background handling, scroll
  lock, escape/outside policy, anchored placement, collision handling, roving
  tabindex, typeahead, submenu hierarchy, and the nonced dynamic-style path.
- **verify:** shared mechanics pass three-engine keyboard/focus/CSP tests before
  any component-specific wrapper claims completion.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 3 gate MET;
  GOTH-4.1 was the sole dependency-ready task and gates 4.2/4.3/4.4. Branch
  `authorization-v3`; unrelated worktree (authorization-v3 uncommitted work, the
  untracked `.claude/plans/ui-goth/`, the untracked `ui/` + `examples/goth-showcase/`
  trees, the untracked authentication `policy*.go`/`html_policy_test.go`) left intact.
  No new §8 controller name was introduced — the frozen `gothX` set is unchanged
  (gothDialog, gothCollapse, gothRovingFocus, gothMenu, gothTabs, gothCombobox,
  gothToast); this task only added shared `mechanics/` modules (not controllers) and
  refined the semantics of existing controllers, which is in-phase work per the
  frozen README §8.
  **Frozen/built per mechanic (all in `ui/goth/assets/src/js`, bundled into the
  Interactive/Full `runtime.js`):**
  - **Overlay stacking/nesting model** — new `mechanics/overlay-stack.js`: one LIFO
    stack owning the single document-level (capture-phase) Escape/pointer listeners.
    Escape dismisses ONLY the topmost overlay; an outside pointer press dismisses
    from the top down every overlay not containing the target, stopping at the first
    that does (so clicking inside a parent but outside a child closes only the child).
  - **Dismiss layers (escape/outside-click, nested-aware)** — `mechanics/dismiss.js`
    rewritten as a thin per-overlay handle over the stack (same `createDismisser`
    API); nesting now correct.
  - **Focus trap/restore** — `mechanics/focus.js` enhanced with a module-level trap
    stack so only the topmost trap enforces Tab under nesting; restore returns focus
    one level up on each deactivate.
  - **Scroll lock** — new `mechanics/scroll-lock.js`: ref-counted `<html
    class="goth-scroll-locked">` (a CLASS + external CSS, never an inline style), so
    nested modals do not double-lock or prematurely release.
  - **Inert/background handling** — new `mechanics/inert.js`: native `inert`
    attribute applied to the SIBLINGS of the overlay's ancestor chain up to `<body>`
    (correct for inline, non-portaled overlays); stacked, restoring only what each
    modal newly set; skips the shared live-region nodes.
  - **Anchored placement + collision** — new `mechanics/anchor.js`: viewport-collision
    flip + clamp; the RESOLVED placement is expressed as `data-side`/`data-align`
    plus the numeric `--goth-anchor-top/left` custom properties set through the CSSOM
    (`element.style.setProperty`, which is exempt from CSP style-src). Honors the
    no-inline-style invariant — no primitive/server inline `style=`, no `unsafe-inline`
    required (proven CSP-clean below).
  - **Roving tabindex** — reused the GOTH-3.4 direction-aware `mechanics/roving.js`.
  - **Typeahead** — reused `mechanics/typeahead.js`.
  - **Submenu hierarchy** — new `mechanics/submenu.js`: RTL-aware open/close keys
    (ArrowRight/ArrowLeft mirror under `dir=rtl`), anchored to the trigger side,
    own roving + overlay-stack membership so Escape/outside close the submenu before
    its parent, Escape returns focus to the parent menuitem.
  - **Overlay scrim + nonced dynamic-style path** — scrim uses the frozen `overlay`
    theme token on `--z-overlay`, panel on `--z-modal`/`--shadow-lg`; the existing
    GOTH-1.3 nonced dynamic stylesheet (host theme override) still passes CSP-clean.
    New shared-mechanics CSS added to `assets/src/css/components.css` (scroll-lock,
    `.goth-overlay`/scrim/panel with a direct-child open selector for nesting,
    `.goth-floating` anchored panel, `.goth-menu-item`, `.goth-submenu-root`).
  **this.$el-outside-init fixes (at source, the GOTH-3.4 Phase-4 follow-up #5):** the
  three latent controllers — `dialog.js`, `menu.js`, `combobox.js` — now cache
  `this._root = this.$el` in `init()` and reference the cached root in `show`/`hide`/
  `toggle`/`select` (methods invoked from a descendant's `x-on` handler see `$el`
  bound to that descendant). `gothDialog` also gained modal scroll-lock + background
  inert; `gothMenu` gained trigger/content anchoring + submenu wiring + top-level
  roving that includes submenu triggers; `gothCombobox` gained listbox anchoring.
  **Test fixtures + e2e:** two Interactive infrastructure specimens (no catalog
  Primitive id) — `mechanics-dialog` (modal + nested modal: trap/restore, nested
  Escape/outside dismiss, scroll lock, inert) and `mechanics-menu` (+ `-rtl`:
  anchored placement, roving, typeahead, submenu) — plus a 3-engine
  `e2e/tests/mechanics.spec.ts` (5 tests). A new `mechanics` registry section was
  added.
  **Verification (exact):** `cd ui/goth && go build ./... && go test ./... &&
  go vet ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS.
  `make generate-ui-assets` reproducible — `dist/`+`manifest.json` byte-identical on
  re-run (runtime.js re-fingerprinted to `38121aee`; theme.css to `cbe8622e`; htmx.js
  unchanged). `make guard` exit 0 (18 guards incl. G17/G18 ui outward-import).
  `make check` exit 0 ("all checks passed"). `make test-ui-browser` **90 passed**
  (was 75; +5 mechanics tests × Chromium/Firefox/WebKit) — the whole-catalog axe
  crawl and the strict-CSP/no-eval/no-remote-origin sweep now include
  `mechanics-dialog`/`mechanics-menu`/`mechanics-menu-rtl` with ZERO
  securitypolicyviolation, proving the CSSOM anchor positioning requires no
  `unsafe-inline`. Per-engine mechanics proof: nested Escape unwinds one level per
  press; the inner trap pulls focus into the nested panel; scrim outside-click
  dismisses; scroll-lock class toggles and background `inert` sets/clears; menu
  anchors under its trigger (`data-side="bottom"`, `--goth-anchor-top` set), roving
  ArrowDown + `d` typeahead + Home work; the submenu opens on ArrowRight, focuses its
  first item, and Escape unwinds submenu-then-menu with focus returning to each
  trigger — all green in all three engines. **Blocked:** none. **Notes (follow-ups,
  not silently fixed):** (1) native `<dialog>`/top-layer adoption vs the div-based
  overlay is a P39/P37 composition decision deferred to GOTH-4.2 (the mechanics are
  reusable either way); (2) hover-intent open/close delays for Hover Card/Tooltip
  (P42/P48) and submenu hover-close timers are left to GOTH-4.3/4.4 where the intent
  timing is a per-primitive concern; (3) the `htmx.Attrs` field set stays PROVISIONAL
  per Gate B.

#### GOTH-4.2 — implement modal/panel primitives P37, P39–P40, P47

- **depends_on:** GOTH-4.1
- **model:** opus
- **files:** Alert Dialog, Dialog, Drawer, Sheet sources/tests/showcase
- **work:** Compose shared mechanics with each primitive's distinct semantics,
  responsive edge behavior, and destructive-decision contract.
- **verify:** focus trap/restore, escape/outside, nested overlay, scroll lock,
  reduced-motion, and browser-back tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-4.1 complete
  (shared overlay mechanics MET); GOTH-4.2/4.3/4.4 all dependency-ready — did only
  4.2 (numeric order). Branch `authorization-v3`; unrelated worktree
  (authorization-v3 uncommitted work, untracked `.claude/plans/ui-goth/`, the
  untracked `ui/` + `examples/goth-showcase/` trees, the untracked authentication
  `policy*.go`/`html_policy_test.go`) left intact.
  **Native-`<dialog>` decision (the flagged P39/P37 open call, resolved within the
  frozen contract).** Adopted the **div-based overlay reusing the frozen GOTH-4.1
  mechanics via `gothDialog`** — NOT native `<dialog>`/top-layer. Rationale: (1)
  README §7 prefers native HTML only "where support and semantics are sufficient",
  and native modal behavior (top layer, `::backdrop`, browser focus trap) requires
  `showModal()` JS anyway — a server-rendered `<dialog open>` is inline and
  un-trapped, so native gives NO honest no-JS advantage over the div model's
  server-owned `data-state="open"` readable baseline; (2) native `<dialog>` cannot
  deliver the frozen GOTH-4.1 nested overlay-stack semantics (Escape dismisses only
  the topmost, outside-press top-down), the CSP-safe class-based scroll-lock/inert,
  or the shared trap stack without FORKING them — which §8 forbids ("shared
  families … so individual primitives do not fork slightly different accessibility
  behavior"); (3) GOTH-4.1 already froze and browser-proved the div
  `.goth-overlay`/scrim/panel mechanics CSP-clean across three engines. Adopting
  native `<dialog>` now would be a mechanics fork, not a composition. All four
  primitives therefore COMPOSE the frozen mechanics; no mechanics file was forked
  and **no new §8 controller name was introduced** (the `gothX` set is unchanged).
  **Per primitive (all in `ui/goth/primitives`, family F4, gothDialog-backed).**
  - **P39 Dialog** — compound parts `Dialog`/`DialogTrigger`/`DialogContent`
    (owns the frozen overlay→scrim→panel structure so a caller can't break the
    mechanics)/`DialogHeader`/`DialogFooter`/`DialogTitle`/`DialogDescription`/
    `DialogClose`. `Open` sets the server-owned `data-state` (no-JS baseline:
    server-open shows the panel via CSS, closed shows only the trigger). `NonModal`
    emits `data-modal="false"` (controller skips scroll-lock/inert). ARIA linkage is
    caller-passed via `Base.ID` (`DialogContent.Labelledby`/`Describedby`).
  - **P37 Alert Dialog** — same shape with `role="alertdialog"`, ALWAYS modal, and
    `data-dismiss-outside="false"` (the destructive-decision contract: a scrim
    press does NOT dismiss; Escape still cancels per APG). Adds `AlertDialogAction`
    (no forced type → submits inside a form) + `AlertDialogCancel`.
  - **P40 Drawer** — dialog-backed edge panel, `DrawerSide` (zero value bottom,
    all four edges), decorative drag handle on bottom/top (drag-to-dismiss out of
    scope this wave), reduced-motion-honored slide-in.
  - **P47 Sheet** — dialog-backed edge panel, `SheetSide` (zero value right, all
    four edges), same slide-in.
  **Controller refinement (no fork, no new name).** `gothDialog` now reads
  `data-dismiss-outside` (filters the "outside" reason the shared dismisser already
  reports) and asserts `aria-modal` on the content ONLY while it actually enforces
  background inert — so the assertion stays honest under the no-JS baseline (nothing
  is inert without JS). The GOTH-4.1 `mechanics/*` files are untouched.
  **CSS (`assets/src/css/components.css`).** Added the modal/panel block: shared
  header/footer/title/description layout; a centered dialog/alert zoom-in; Sheet/
  Drawer edge panels keyed off `data-side` with per-edge slide-in `@keyframes`; the
  drawer handle. No inline `style=` (proven by a render test); every animation is
  added to the `prefers-reduced-motion` collapse. Scrim uses the frozen `overlay`
  token via the GOTH-4.1 `.goth-overlay`.
  **Showcase + tests.** `primitives/overlay_test.go` (no-inline-style, server-owned
  baseline, content structure, decision contract, side resolution, drag handle,
  ownership merge). Eight showcase specimens (`specimens_primitives_overlay.go`):
  four Interactive (`primitive-dialog` incl. a NESTED dialog, `-alert-dialog`,
  `-sheet`, `-drawer`) + four StylesOnly server-open (`primitive-*-open`) proving
  the no-JS readable baseline; each renders the REAL primitive components.
  `ImplementedPrimitives` gained P37/P39/P40/P47 (the registry completeness test
  enforces a specimen per id). A 3-engine `e2e/tests/overlays.spec.ts` (7 tests):
  Dialog focus trap/restore + scroll-lock + inert + aria-modal lifecycle, scrim
  dismiss, close button, nested-Escape unwind, StylesOnly no-JS readability; Alert
  Dialog scrim-does-NOT-dismiss + Escape-cancels + action/cancel close; Sheet edge
  focus-trap/close; Drawer handle + focus-trap/close. The `csp.spec.ts` crawl gained
  `test.slow()` (the catalog grew 8 specimens past the 30s budget — same rationale
  and precedent as the axe crawl; the zero-violation assertion is unchanged).
  **Verify (exact).** `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS.
  `make generate` templ no-op; `make generate-ui-assets` reproducible —
  `manifest.json` byte-identical on re-run (runtime.js re-fingerprinted to
  `2b324ad7` for the gothDialog refinement, theme.css to `396a1595` for the panel
  CSS, htmx.js unchanged; `dist/` holds only the three current hashed files, no
  stale). `make check` exit 0 ("all checks passed"); `make guard` exit 0 (all
  guards incl. the ui outward-import G17/G18). `make test-ui-browser` **114 passed**
  (was 90; +8 overlay tests × Chromium/Firefox/WebKit) — the whole-catalog axe
  crawl and the strict-CSP/no-eval/no-remote sweep now include all eight overlay
  specimens with ZERO securitypolicyviolation (the panels emit no inline style; the
  slide-in animations are external CSS). Per-engine overlay proof green in all three
  engines: Dialog opens via keyboard, traps focus into the first field, locks scroll
  + sets background `inert` + asserts `aria-modal`, Escape restores focus to the
  trigger and releases all three; scrim + close button dismiss; the nested dialog
  unwinds exactly one level per Escape; the StylesOnly server-open dialog is visible
  and readable with `Alpine` absent; the Alert Dialog ignores a scrim press but
  cancels on Escape (focus restored) and closes on the destructive Action; Sheet/
  Drawer open edge-anchored (`data-side` right/bottom), trap focus, and close.
  (One pre-existing GOTH-4.1 `mechanics.spec.ts` menu-anchor assertion flaked once
  under full-parallel three-engine load — `--goth-anchor-top` read before the
  anchor rAF settled — then passed 3/3 in isolation and clean on the re-run; it is a
  GOTH-4.1 harness timing sensitivity, NOT a GOTH-4.2 regression, and no
  menu/anchor code was touched.) **Blocked:** none. **Notes (follow-ups, not
  silently fixed):** (1) parity rows P37/P39/P40/P47 stay UNCHECKED and catalog
  status stays `planned` until the GOTH-4.5 wave audit; (2) drag-to-dismiss gesture
  for Drawer and exit (close) animations are deferred — display:none removal makes
  exit instant (an honest no-exit-animation dialog), matching the frozen mechanics;
  (3) a server-open dialog under the Interactive profile is a static readable panel
  the controller does not auto-adopt (the no-JS baseline is proven on StylesOnly);
  wiring a controller to adopt a server-open state is a later concern if a real
  adopter needs it.

#### GOTH-4.3 — implement anchored information/selection primitives P42, P45–P46, P48

- **depends_on:** GOTH-4.1
- **model:** opus
- **files:** Hover Card, Popover, Select, Tooltip sources/tests/showcase
- **work:** Implement focus/hover intent, placement, touch fallbacks, listbox
  typeahead/form integration, and described-by behavior.
- **verify:** pointer/keyboard/touch emulation, placement/CSP, form POST, axe.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-4.1 complete
  (shared overlay/anchor mechanics MET); GOTH-4.3/4.4 both dependency-ready — did
  only 4.3 (numeric order). Branch `authorization-v3`; unrelated worktree
  (authorization-v3 uncommitted work, untracked `.claude/plans/ui-goth/`, the
  untracked `ui/` + `examples/goth-showcase/` trees, the untracked authentication
  `policy*.go`/`html_policy_test.go`) left intact.
  **§8 controller-set reopen (the flagged blocker, resolved by owner decision).**
  On starting 4.3 the implementer surfaced a hard conflict: the frozen §8 list
  named no controller able to back Tooltip (P48) / Hover Card (P42) — Escape-hide
  and hover-intent are not expressible in CSS, and every existing controller is
  semantically wrong (gothDialog traps + scroll-locks, gothMenu moves focus into
  the panel and sets aria-expanded). The owner ratified **Option A** (recorded as
  a dated addendum in `gate-b-review.md`): ADD `gothTooltip` and `gothHoverCard`
  to the frozen §8 set — thin controllers COMPOSING the frozen GOTH-4.1 mechanics
  (anchor, dismiss/overlay-stack) with no fork — while **Popover rides native
  `popover`/`popovertarget`** and **Select rides a native `<select>`** so neither
  adds a controller (minimum new frozen surface). README §8's controller list was
  updated to the nine-name set with a pointer to that addendum as the ratifying
  record (a recorded reopen, not a silent edit).
  **Per primitive (all in `ui/goth/primitives`).**
  - **P48 Tooltip (F4, gothTooltip).** No-JS baseline is a pure CSS
    `:hover`/`:focus-within` reveal: the `role="tooltip"` content is always in the
    DOM and the trigger's caller-passed `aria-describedby` (Base.ID linkage) points
    at it, so a tooltip works with zero JS. `gothTooltip` sets `data-enhanced` on
    the root so the CSS switches from the instant `:hover` reveal to the
    controller-owned `[data-state="open"]`: hover opens after an intent delay, focus
    opens immediately (no artificial keyboard delay), and Escape / blur / outside
    press hide it. It never traps and never moves focus into the tooltip.
  - **P42 Hover Card (F4, gothHoverCard).** Same CSS baseline; richer, possibly
    interactive content. Longer close grace bridges the trigger→panel gap; the
    trigger renders an `<a>` when a `URL` is set (links remain links) and a
    `<button>` otherwise. Composes the overlay-stack dismisser for Escape + outside
    (touch-safe) dismissal; does not trap.
  - **P45 Popover (F3, native).** Rides the native `popover`/`popovertarget`
    attributes: click toggles, Escape closes, an outside press light-dismisses, and
    the panel enters the top layer — ALL with no JS and no controller (the
    StylesOnly `-nojs` specimen proves it with Alpine absent). The one enhancement
    (Interactive/Full only) is a delegated, CSP-safe `mechanics/popover-anchor.js`
    that positions the opened panel against its invoker via the shared anchor
    mechanic (a capture-phase `toggle` listener, since the ToggleEvent does not
    bubble); the no-JS fallback is the UA-centered position. Frozen `Side`/`Align`
    enums emit `data-side-preferred`/`data-align-preferred`.
  - **P46 Select (F4, native baseline — the recorded F4-native precedent).** A
    styled native `<select>`: `appearance:none` hides only the closed-state arrow
    and a decorative chevron is drawn, while the native listbox, typeahead,
    keyboard, form value, and constraint validation are intact and work with no JS.
    `SelectOption`/`SelectGroup` parts + an optional disabled/hidden `Placeholder`.
    Base.ID lands on the `<select>` so an external `<label for>` names it.
  **Mechanics/JS (composed, no fork).** New `mechanics/hover-intent.js` (timer
  bookkeeping shared by both hover controllers) and `mechanics/popover-anchor.js`
  (native-popover anchoring); `controllers/tooltip.js` + `controllers/hover-card.js`
  register once in `register.js`; `runtime.js` calls `initPopoverAnchoring()`. The
  GOTH-4.1 `mechanics/*` and every existing controller are untouched. No inline
  `style=` (proven by a render test); all reveals animate via external CSS and
  collapse under `prefers-reduced-motion`; anchoring stays CSSOM-only (CSP-clean).
  **CSS (`assets/src/css/components.css`).** Added the anchored block: tooltip/
  hover-card baseline-vs-enhanced reveal switch (`:not([data-enhanced])` hover/focus
  vs `[data-enhanced][data-state="open"]`), fixed-positioning switch keyed off the
  anchor mechanic's `data-side`, native popover panel visuals (UA-centered until
  `[data-side]` anchors it), and the styled native select; new animations added to
  the reduced-motion collapse.
  **Showcase + tests.** `primitives/anchored_test.go` (no-inline-style, tooltip
  describedby wiring, hover-card link/button forms, native popover attributes,
  side/align resolution incl. unknown-value fallback, native select baseline +
  placeholder, ownership merge). `specimens_primitives_anchored.go`: seven specimens
  rendering the REAL components — `primitive-tooltip`/`-hover-card`/`-popover`
  (Interactive), `primitive-tooltip-css`/`-popover-nojs`/`-select` (StylesOnly), and
  a no-JS `select-form` GET→`/anchored/echo`. `ImplementedPrimitives` gained
  P42/P45/P46/P48 (the registry completeness test enforces a specimen per id). A
  3-engine `e2e/tests/anchored.spec.ts` (10 tests): tooltip focus-shows/Escape-hides/
  no-trap + describedby, hover-intent open/close, CSS-baseline no-JS reveal; hover
  card hover/focus open + Escape + link-stays-link + outside dismiss; popover native
  toggle + anchored `data-side` + Escape + outside dismiss + no-JS variant; select
  native selection + no-JS GET submission echo.
  **Verify (exact).** `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS.
  `make generate` templ no-op; `make generate-ui-assets` reproducible — `dist/` +
  `manifest.json` byte-identical on a second rebuild (runtime.js re-fingerprinted to
  `d53adce7` for the two new controllers + popover-anchor; theme.css to `a4e3065a`
  for the anchored CSS; htmx.js unchanged). `make check` exit 0 ("all checks
  passed", incl. the ui asset-drift + G5/G17/G18 ui guards); `make guard` exit 0.
  `make test-ui-browser` **144 passed** (was 114; +10 anchored tests ×
  Chromium/Firefox/WebKit) — the whole-catalog axe crawl and the strict-CSP/
  no-eval/no-remote sweep now include all seven anchored specimens with ZERO
  securitypolicyviolation (no inline style; the CSSOM anchor positioning needs no
  `unsafe-inline`; native popover needs no script). Per-engine anchored proof green
  in all three engines: tooltip opens on focus (immediately) and hover (after the
  intent delay), Escape hides it while focus stays on the trigger (no trap), and the
  StylesOnly CSS baseline reveals on hover with Alpine absent; the hover card opens
  on hover/focus, keeps its link a link, is dismissed by Escape and by an outside
  press, and never traps; the native popover toggles on click, is anchored
  (`data-side` set by the enhancement), and closes on Escape and outside press — and
  the StylesOnly variant does all of that with Alpine absent; the native select
  selects a value and submits it with GET (no JS), echoed back by `/anchored/echo`.
  **Blocked:** none. **Notes (follow-ups, not silently fixed):** (1) parity rows
  P42/P45/P46/P48 stay UNCHECKED and catalog status stays `planned` until the
  GOTH-4.5 wave audit; (2) Select ships the native baseline per the owner decision
  and the recorded F4-native precedent — a custom listbox/combobox trigger is P50
  Combobox's concern (GOTH-5.2), not this row; (3) native popover CSS anchor
  positioning (`anchor-name`) is intentionally not used (uneven 3-engine support) —
  the JS anchor enhancement covers Interactive/Full and the no-JS fallback is the
  UA-centered position; (4) hover-card touch long-press/tap-hold nuances beyond the
  pointerenter+outside-dismiss model are left as a later refinement if a real
  adopter needs them.

#### GOTH-4.4 — implement menu primitives P38, P41, P43–P44

- **depends_on:** GOTH-4.1
- **model:** opus
- **files:** Context Menu, Dropdown Menu, Menubar, Navigation Menu sources/tests/showcase
- **work:** Share menu mechanics while preserving navigation links and distinct
  context/menubar/mobile behavior.
- **verify:** complete WAI-style keyboard matrix, submenu, typeahead, escape,
  touch fallback, RTL, and no-JS navigation tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-4.1 complete
  (shared overlay/menu mechanics MET); GOTH-4.2/4.3 complete; GOTH-4.4 was the last
  dependency-ready Phase-4 task before the GOTH-4.5 wave audit. Branch
  `authorization-v3`; unrelated worktree (authorization-v3 uncommitted work,
  untracked `.claude/plans/ui-goth/`, the untracked `ui/` + `examples/goth-showcase/`
  trees, the untracked authentication `policy*.go`/`html_policy_test.go`) left intact.
  **No new §8 controller name introduced.** All four primitives are backed by the
  existing frozen `gothMenu` controller (Dropdown/Context/Menubar) or are native
  (Navigation Menu). gothMenu's per-primitive semantics were REFINED in place
  (context-menu pointer/keyboard opening + menubar horizontal coordination) — that
  is in-phase per README §8; a new controller name would have been new frozen
  surface and stopped the task. The frozen nine-name `gothX` set is unchanged.
  **Per primitive (all family F4; Dropdown/Context/Menubar in `ui/goth/primitives`
  compose the frozen GOTH-4.1 menu mechanics via gothMenu with no fork).**
  - **P41 Dropdown Menu (gothMenu).** Compound parts `DropdownMenu`/`…Trigger`/
    `…Content`/`…Item`/`…CheckboxItem`/`…RadioItem`/`…Label`/`…Separator`/`…Group`/
    `…Sub`/`…SubTrigger`/`…SubContent`. Trigger toggles the anchored `.goth-floating`
    panel; roving focus + typeahead over items; submenu opens with the RTL-aware
    keys; Escape/outside dismiss. Item roles per the row: `role="menuitem"`, plus
    `role="menuitemcheckbox"`/`role="menuitemradio"` with server-owned `aria-checked`
    and a check/dot indicator shown by CSS off `[aria-checked="true"]`. No-JS
    baseline is server-owned (`DropdownMenuProps.Open` → `data-state="open"` on root
    and content, panel readable with no JS), matching the GOTH-4.2 modal/panel
    precedent.
  - **P38 Context Menu (gothMenu, refined).** `ContextMenuTrigger` is a focusable
    (`tabindex="0"`) right-click region; gothMenu gained `openContext($event)`
    (suppresses the native menu, anchors the panel at the pointer via a virtual
    zero-size rect through the frozen `anchor.js`) and `onContextKeydown($event)`
    (ContextMenu key / Shift+F10 / Enter/Space open at the region box — the keyboard
    + touch-focusable fallback). Escape returns focus to the region. Same item parts
    as Dropdown. No-JS baseline is the server-open state (a pointer-invoked menu has
    no CSS-only open path; with no JS a right-click yields the browser's native menu).
  - **P43 Menubar (gothMenu, refined).** `Menubar` (`role="menubar"`) wraps several
    `MenubarMenu` (each a gothMenu) with a `MenubarTrigger` (`role="menuitem"`,
    `aria-haspopup`) and `MenubarContent`. gothMenu detects the `data-slot="menubar"`
    ancestor and coordinates: a single horizontal tab stop across the triggers
    (ArrowLeft/ArrowRight, RTL-aware), ArrowDown/Enter/Space opens a menu and focuses
    the first item, ArrowLeft/ArrowRight while a menu is open closes it and opens the
    adjacent one (cross-instance via a registry of gothMenu instances on the menubar
    element — no fragile `.click()`), and Escape unwinds to the trigger. Plus items,
    label, separator, checkbox/radio, submenu.
  - **P44 Navigation Menu (native, NO controller — the recorded F4-native precedent).**
    Link-first: `NavigationMenu` is a `<nav>` landmark, `NavigationMenuLink` renders
    a REAL `<a href={ URL.SafeURL() }>` (active link carries `aria-current="page"`),
    and a menu of sub-destinations is a native `<details>`/`<summary>`
    (`NavigationMenuSub`/`NavigationMenuTrigger`/`NavigationMenuContent`) that opens,
    closes, and is keyboard-operable with NO JavaScript. gothMenu would be
    semantically wrong here (roving menuitems vs. real links), and a sufficient
    native baseline satisfies the F4 row with no controller — the same call Select
    (P46) and Popover (P45) made in GOTH-4.3. The href is emitted natively in the
    templ, never smuggled through the generic attribute spread.
  **Mechanics/JS.** Only `controllers/menu.js` changed (context + menubar
  refinements, additive — the default dropdown path is byte-for-byte behavior-
  unchanged); the GOTH-4.1 `mechanics/*` files and every other controller are
  untouched. No inline `style=` (proven by a render test); the menu-open fade reuses
  the frozen `goth-anchored-in` keyframe and is in the reduced-motion collapse;
  anchoring stays CSSOM-only (CSP-clean). Two internal glyphs added (`checkGlyph`,
  `dotGlyph`) for the check/radio indicators.
  **CSS (`assets/src/css/components.css`).** Added the GOTH-4.4 menu block: item
  indicators (shown off `[aria-checked="true"]`), label/separator/group, submenu
  chevron (mirrored under `[dir="rtl"]`), disabled-item dimming, the context-menu
  dashed region, the `.goth-menubar` bar + triggers, and the Navigation Menu nav/
  list/link/`<details>` disclosure; the nav chevron transition + the menu fade are
  added to the `prefers-reduced-motion` collapse.
  **Showcase + tests.** `primitives/menu_test.go` (no-inline-style, dropdown
  structure incl. all three item roles + submenu, server-open baseline, context
  trigger openers + focusability, menubar landmark/roles, navigation link+details +
  no-controller, ownership merge). `specimens_primitives_menu.go`: five specimens
  rendering the REAL components — `primitive-dropdown-menu`/`-context-menu`/
  `-menubar` (Interactive) + `primitive-dropdown-menu-open` (StylesOnly server-open
  no-JS baseline) + `primitive-navigation-menu` (StylesOnly link-first no-JS).
  `ImplementedPrimitives` gained P38/P41/P43/P44 (the registry completeness test
  enforces a specimen per id). A 3-engine `e2e/tests/menu.spec.ts` (9 tests):
  dropdown anchored-open/roving/typeahead/item-roles, submenu ArrowRight-open +
  nested-first Escape unwind, outside dismiss, server-open no-JS readability;
  context right-click-open-at-pointer + Escape-restores-focus + ContextMenu-key
  open; menubar horizontal roving + ArrowDown open + ArrowRight-switches-adjacent +
  Escape; navigation no-JS real-links + native-details disclosure.
  **Verify (exact).** `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS.
  `make generate` templ no-op; `make generate-ui-assets` reproducible — `dist/` +
  `manifest.json` byte-identical on a second rebuild (runtime.js re-fingerprinted to
  `f1380536` for the gothMenu refinement, theme.css to `475fc842` for the menu CSS,
  htmx.js unchanged at `8689e2e2`; `dist/` holds only the three current hashed files,
  no stale). `make check` exit 0 ("all checks passed", incl. the ui asset-drift +
  G5/ui-outward guards); `make guard` exit 0. `make test-ui-browser` **171 passed**
  (was 144; +27 = 9 menu tests × Chromium/Firefox/WebKit) — the whole-catalog axe
  crawl and the strict-CSP/no-eval/no-remote sweep now include all five menu
  specimens with ZERO securitypolicyviolation (no inline style; CSSOM anchor
  positioning needs no `unsafe-inline`; native `<details>` needs no script). Per-
  engine menu proof green in all three engines (see the spec list above). (Under
  full-parallel three-engine load the pre-existing GOTH-4.1 `mechanics.spec.ts`
  menu-anchor assertion flaked once — `--goth-anchor-top` read before the anchor rAF
  settled — then passed 5/5 in isolation on chromium; it is the documented GOTH-4.1
  harness timing sensitivity, NOT a GOTH-4.4 regression: the default dropdown/
  mechanics-menu gothMenu path is behavior-unchanged and no `mechanics/*` file was
  touched.) **Blocked:** none. **Notes (follow-ups, not silently fixed):** (1) parity
  rows P38/P41/P43/P44 stay UNCHECKED and catalog status stays `planned` until the
  GOTH-4.5 wave audit; (2) checkbox/radio menu items are server-owned (a click
  dispatches goth:select and closes; the app applies the toggle) rather than
  client-toggling like Radix — deliberate per invariant 1 (server owns state), flagged
  for the 4.5 audit; (3) Navigation Menu ships the native `<details>` click-disclosure
  baseline; a desktop hover-open enhancement is intentionally deferred (native details
  cannot honestly hover-reveal without fighting the UA toggle, and the click
  disclosure is fully no-JS/accessible/axe-clean); (4) Context Menu touch long-press
  beyond the contextmenu event + keyboard/focus fallback is left as a later refinement
  if a real adopter needs it.

#### GOTH-4.5 — close the 12-entry overlay/navigation wave

- **depends_on:** GOTH-4.2, GOTH-4.3, GOTH-4.4
- **model:** fable for audit, opus for remediation
- **files:** catalog and phase sources/tests/docs
- **work:** Run adversarial nested-overlay, focus-loss, CSP, and accessibility
  review; remediate before P37–P48 are checked.
- **verify:** `make test-ui-browser`; `make check && make guard`.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-4.2/4.3/4.4 all
  DONE (2026-07-17); GOTH-4.5 was the sole dependency-ready Phase-4 task (the wave
  closeout). Branch `authorization-v3`; unrelated worktree (authorization-v3
  uncommitted work, the untracked `.claude/plans/ui-goth/`, the untracked `ui/` +
  `examples/goth-showcase/` trees, the untracked authentication
  `policy*.go`/`html_policy_test.go`) left intact — the only source touches are two
  e2e spec deflakes + a new Layer-4 capture driver, all inside the already-untracked
  `examples/goth-showcase/` tree. (Observed, not mine: root `.gitignore` carries a
  pre-existing untracked-node_modules ignore for `ui/goth/tools/` from earlier GOTH
  toolchain work — left untouched.)
  **Adversarial audit — P37–P48 against the full Phase-4 gate (nested-overlay
  dialog-in-dialog + menu-in-dialog Escape unwind order, focus trap/restore chains,
  keyboard roving/typeahead/menubar-cross-menu/submenu-RTL, pointer/touch outside
  dismiss + context-menu pointer anchor, dynamic-style CSP via the CSSOM anchor path,
  RTL menus/submenus/anchored panels, reduced motion, and each row's no-JS baseline
  with Alpine absent).** Every row was verified at source (the `.templ`/`.go` pairs,
  the `mechanics/*` + `controllers/*` JS, `components.css`, the showcase registry) and
  by run-and-look. Result: **no primitive fails the parity definition; no Phase-4
  task was reopened.** Per-row disposition:
  - **P37 Alert Dialog / P39 Dialog / P40 Drawer / P47 Sheet (gothDialog).** The
    frozen GOTH-4.1 overlay-stack owns one document-level capture-phase Escape/pointer
    listener; Escape dismisses only the topmost entry (stopPropagation → exactly one
    level per press) and an outside press dismisses top-down, stopping at the first
    container — the nested-overlay contract. The module-level trap stack means only the
    topmost trap enforces Tab and each deactivate restores focus one level up. Scroll
    lock is a ref-counted `<html class>` (not inline style); inert is applied to the
    ancestor-chain siblings and stacked. Alert Dialog is `role=alertdialog`, always
    modal, `data-dismiss-outside="false"` (scrim does not dismiss, Escape cancels).
    No-JS baseline: server-owned `data-state` renders the panel readable with Alpine
    absent (StylesOnly `-open` specimens). Reduced motion collapses all zoom/slide
    keyframes. Verified visually (below): nested dialog stacks over a second scrim
    layer.
  - **P41 Dropdown / P38 Context / P43 Menubar (gothMenu) + P44 Navigation (native).**
    Anchored `.goth-floating` panel via the CSSOM anchor (no inline style), roving +
    typeahead over items and submenu triggers, RTL-aware submenu open/close keys
    (ArrowRight/ArrowLeft mirror under `dir=rtl`), submenu joins the overlay stack so
    Escape unwinds submenu-then-menu. Context menu anchors at the pointer via a virtual
    zero-size rect and has a keyboard/focus fallback (ContextMenu key / Shift+F10 /
    Enter/Space at the region box); its honest no-JS state is the browser's native menu
    (a pointer-invoked menu has no CSS-only open path). Menubar coordinates one
    horizontal tab stop and cross-menu ArrowLeft/ArrowRight (RTL-aware) via a
    per-menubar registry of gothMenu instances (no fragile `.click()`). Navigation Menu
    is real `<a href>` links + native `<details>` disclosure, fully no-JS. Item roles
    per row: menuitem + menuitemcheckbox/menuitemradio (server-owned aria-checked).
  - **P42 Hover Card / P48 Tooltip (gothTooltip/gothHoverCard) / P45 Popover (native) /
    P46 Select (native).** Tooltip/Hover Card compose anchor + overlay-stack dismiss +
    hover-intent with no trap and no focus move into the panel; the no-JS baseline is a
    pure CSS `:hover`/`:focus-within` reveal + `aria-describedby`. Popover rides native
    `popover`/`popovertarget` (click toggle, Escape, outside light-dismiss, top layer)
    with a delegated CSP-safe anchor enhancement; Select is a styled native `<select>`
    with intact listbox/typeahead/form value/constraint validation. All no-JS with
    Alpine absent.
  **Dynamic-style CSP (the CSSOM anchor path).** Every anchored primitive writes only
  `--goth-anchor-top/left` via `element.style.setProperty` (CSSOM, exempt from CSP
  `style-src`) plus `data-side`/`data-align`; no primitive/server emits an inline
  `style=` (render-test enforced) and no `unsafe-inline` is required. The whole-catalog
  strict-CSP crawl reports **zero securitypolicyviolation** across all 22 Phase-4
  specimens in all three engines.
  **Deflake (disposition 1 — recorded).** The GOTH-4.1 menu-anchor timing test flaked
  twice under full-parallel three-engine load: gothMenu flips `data-state="open"`
  synchronously in `show()` but writes `--goth-anchor-top` inside `$nextTick` (after the
  panel is measured), so a single read right after the data-state assertion races the
  deferred anchor write. **The race is in the TEST, not in `anchor.js`** — `anchor.js`'s
  `update()` sets the custom property synchronously within its own call and is correct;
  the controller correctly defers positioning until the panel is measurable. Fixed at
  source by awaiting the settled state (`expect.poll` on the CSSOM value) in BOTH
  `mechanics.spec.ts` and `menu.spec.ts` (the identical read-once pattern), rather than
  reading once. Re-ran the two anchor-sensitive specs three times three-engine (9
  passed each, no flake) plus the full suite clean.
  **Recorded dispositions (2, 3, 4).** (2) **Server-owned checkbox/radio menu items**
  (menuitemcheckbox/menuitemradio) are the deliberate invariant-1 posture: clicking an
  item dispatches `goth:select` and closes; the APP applies the toggle and re-renders
  the server-owned `aria-checked`, rather than the client optimistically toggling like
  Radix. This keeps the server the authority for state (standing invariant 1) and is
  documented in `primitives/menu.go`. ACCEPTED — no change. (3) **Per-primitive prefixed
  part sets** — Dropdown/Context/Menubar each re-declare their item parts under the
  primitive prefix (`DropdownMenuItem`/`ContextMenuItem`/`MenubarItem`,
  `…CheckboxItem`/`…RadioItem`/`…Sub…`). This matches the frozen "Public API grammar"
  Shadcn-style compound-prefix rule and the frozen catalog names + Shadcn parity naming;
  it is the accepted public surface. KEPT the precedent — recorded, no change. (4)
  **Deferred enhancements (recorded, not defects):** Drawer drag-to-dismiss gesture;
  overlay exit (close) animations (display:none removal makes exit instant — an honest
  no-exit-animation panel); Navigation Menu desktop hover-open (native `<details>`
  cannot honestly hover-reveal without fighting the UA toggle; the click disclosure is
  fully no-JS/axe-clean); Context Menu touch long-press beyond the contextmenu event +
  keyboard/focus fallback; Hover Card touch tap-hold nuances beyond
  pointerenter+outside-dismiss; and controller adoption of a server-open panel under the
  Interactive profile (the no-JS baseline is proven on StylesOnly). None blocks the
  parity rows.
  **Verification (exact).** `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS; `cd examples/goth-showcase && go build/test/vet ./...` PASS. `make
  generate` templ no-op (no `*.templ`/`*_templ.go` drift). `make generate-ui-assets`
  reproducible — `dist/` + `manifest.json` byte-identical on a second rebuild (SHA
  compared before/after: identical). `make guard` exit 0 (all guards incl. G17/G18 ui
  outward-import). `make check` exit 0 ("all checks passed"). `make test-ui-browser`
  **171 passed** (3 engines, no flaky) — the whole-catalog axe crawl + strict-CSP/
  no-eval/no-remote sweep include all Phase-4 specimens with zero violations; the
  deflaked anchor specs then re-ran 3× three-engine clean.
  **Layer-4 run-and-look (viewed, not asserted from exit codes).** The showcase server
  was launched and driven with Playwright/Chromium; 22 curated full-page screenshots of
  the overlay/menu specimens in meaningful states were captured under
  `examples/goth-showcase/e2e/screenshots/phase4/` (driver: `e2e/capture-phase4.mjs`)
  and opened/inspected. Confirmed: Dialog opens modal-centered over a grey scrim with
  the background scroll-locked; the NESTED dialog stacks on top with the parent dimmed
  under a SECOND scrim layer (nesting visually proven); Alert Dialog shows the
  destructive Cancel/Delete decision over scrim; Sheet slides from the right edge and
  Drawer from the bottom edge with a drag handle (dark-mode Drawer restyles to near-black
  surface + white text, handle visible); server-open StylesOnly dialog/sheet render
  readable with no JS. Dropdown Menu opens anchored under its trigger with the Share
  SUBMENU opening to the RIGHT (LTR); the RTL menu mirrors to the right edge with
  right-aligned items; Context Menu opens at the pointer with a submenu chevron; Menubar
  shows the horizontal File/Edit/View bar with File open; dark Dropdown restyles its
  surface; Navigation Menu shows real links (Home bold = `aria-current`) + a Products
  `<details>` chevron; Popover anchors below-start; Hover Card opens on hover with
  interactive "View posts"; Tooltip opens on focus. No visual defect found.
  **Actions.** catalog.md P37–P48 flipped `planned`→`accepted` (36→48 accepted / 16
  planned; 48 + 16 = 64, count invariant intact); plan.md parity-matrix rows P37–P48 checked (P49–P64
  untouched); this note appended; GOTH-4.5 checked in TASKS.md; Phase 4 gate marked MET
  and the board pointer advanced to Phase 5 (GOTH-5.1/5.2/5.3/5.4 dependency-ready).
  **Blocked:** none. **Notes (follow-ups, not silently fixed):** the four deferred
  enhancements above (Drawer drag-to-dismiss, exit animations, Navigation hover-open,
  Context/Hover touch long-press nuances, server-open controller adoption) carry forward
  as later polish, none blocking; the `htmx.Attrs` field set stays PROVISIONAL per Gate B.

### Phase 5 — composite data/time/application primitives

#### GOTH-5.1 — implement date primitives P49, P53

- **depends_on:** phase 4 gate
- **model:** opus
- **files:** Calendar, Date Picker sources/controllers/tests/showcase
- **work:** Define locale/week-start/time-zone boundaries, date-only value
  format, keyboard grid, disabled/min/max/range semantics, parse/format errors,
  and server validation. Do not smuggle application time-zone policy into the
  primitive.
- **verify:** deterministic clock/locale tests and three-engine keyboard/form tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 4 gate MET
  (GOTH-4.1–4.5 accepted 2026-07-17); GOTH-5.1 depends only on the Phase 4 gate —
  dependency-ready. Numeric order honored (only 5.1 done). Branch `authorization-v3`;
  unrelated worktree (the authorization-v3 edits, the untracked `ui/`/
  `examples/goth-showcase/` GOTH work) left untouched.

  **P49 Calendar (F4).** Server-ownership model: Go owns ALL date math and locale
  rendering — `calendarWeeks` computes the month grid (leading/trailing
  adjacent-month days included so every row is rectangular), the weekday-header order
  from `WeekStart`, the caption, the prev/next month values (`2006-01`), and the
  per-day selected/today/disabled/outside states; the client never computes calendar
  layout. The primitive holds NO clock and NO time-zone policy: `Today` is
  caller-supplied (zero = no marker) and every date operates on the year/month/day of
  the provided `time.Time` in its own location (invariant "do not smuggle application
  time-zone policy" honored). Locale approach: month/weekday labels default to the Go
  stdlib names but are fully overridable per-index via `MonthLabel`/`Weekdays`/
  `WeekdaysFull` (the server passes localized strings — no CLDR dependency pulled
  into the kit). No-JS baseline: day selection and prev/next are native `<button
  type="submit">` (day → `name="date"` value = ISO `2006-01-02`; nav → `name="month"`
  value = target month); the caller wraps the Calendar in a form and the server
  re-renders. Disabled/out-of-range and outside-month days render as non-focusable
  `<span>` (no submit value) so they cannot be selected with no JS. Grid semantics:
  `role="grid"` / `role="row"` / `role="columnheader"` / `role="gridcell"`, with
  `aria-selected` on the gridcell (the role that supports it — an axe run caught and
  I fixed `aria-selected` on the bare `<button>`), `aria-current="date"` +
  `data-today` on today, and a single server-chosen roving tab stop (selected → today
  → first-enabled). Enhanced path: the frozen `gothRovingFocus` controller was
  REFINED IN PLACE to add a grid mode (no new §8 controller name — the
  gothMenu/roving.js in-place-refinement precedent). A new generic
  `createGridRoving` in the shared `mechanics/roving.js` drives APG 2D keyboard
  (direction-aware horizontal arrows, same-column vertical arrows tolerant of
  month-edge holes via `data-col`/`data-row`, Home/End to the row edges); it respects
  the server-rendered `tabindex="0"` as the start index so enhanced and no-JS focus
  targets match. gothRovingFocus now discovers items via `[data-slot="item"],
  [data-roving-item]` and branches to grid mode on `data-orientation="grid"` — the
  Toggle Group (data-slot="item") default path is behavior-unchanged. PageUp/PageDown
  keyboard month-nav is a recorded deferral (prev/next are Tab-reachable; month change
  is server-owned navigation).

  **P53 Date Picker (F4).** Composes Field + native Popover + Calendar with NO new
  controller: `DatePickerInput` (the server-formatted value + `Invalid`/`Describedby`
  parse-error contract), `DatePickerTrigger` (native `popovertarget`), and
  `DatePickerContent` (native `popover="auto"` panel holding a Calendar). The
  parse/format/error contract is SERVER-owned — the fixture route parses the submitted
  value (a day button's ISO `date`, else the typed `date-text`), formats it, and
  re-renders with `Invalid` on a bad manual entry; the primitive never parses. No-JS
  path: native popover opens the calendar and a day is a native form-GET submit that
  reloads the re-rendered picker; the day also carries `popovertarget`+
  `popovertargetaction="hide"` (Calendar `DismissTarget`) so selection closes the
  popover natively. HTMX-enhanced path (the no-JS/HTMX parity proof): Calendar
  `DayAttributes` carry `hx-get`/`hx-target="#dp-fragment"`/`hx-swap="outerHTML"`, so
  selecting a day swaps the field fragment (input value updated) and the same native
  dismiss closes the popover — proven equivalent to the no-JS full-document reload.
  `/datepicker/pick` returns a fragment to an HTMX request and a full document to a
  direct request (history re-fetch correctness).

  Files: `ui/goth/primitives/calendar.{go,templ}` + `date_picker.{go,templ}` (+
  generated `_templ.go`), `date_test.go` (deterministic July-2026 clock/locale/state/
  merge tests); `assets/src/js/mechanics/roving.js` (createGridRoving),
  `assets/src/js/controllers/roving-focus.js` (grid mode), `assets/src/css/
  components.css` (calendar + date-picker rules keyed off the stable
  goth-*/data-* surface, no inline style, RTL chevron flip, reduced-motion entries);
  showcase `internal/showcase/specimens_primitives_date.go` (Calendar Interactive
  specimen + Date Picker Full/HTMX + StylesOnly no-JS specimens + `/calendar/select`
  and `/datepicker/pick` server-owned round-trip routes), `registry.go` (P49/P53 in
  ImplementedPrimitives + registration), `showcase.go` (fixture wiring); e2e
  `tests/date.spec.ts` (grid roving keyboard, no-JS day submit, disabled days, HTMX
  swap + popover close, no-JS popover form). No new §8 controller; catalog P49/P53
  stay `planned` until the GOTH-5.5 wave audit.

  Verify results: `ui/goth` `go build/test/vet` green (deterministic date_test
  passes — no wall-clock dependency). Showcase `go build/test` green (the
  completeness test now requires + finds P49/P53 specimens). `make generate` (templ)
  clean; `make generate-ui-assets` byte-identical on rebuild (runtime.js
  `6add0ff6`, theme.css `e2c71b15`, htmx.js `8689e2e2`). `make test-ui-browser`
  **186 passed** (was 171; +5 date specs × Chromium/Firefox/WebKit), including the
  crawl-based axe + strict-CSP specs over the new specimens (zero
  securitypolicyviolation, no `unsafe-eval`/`unsafe-inline`). `make check` and
  `make guard` both green ("all checks passed"; G17/G18 ui guards pass — the grid
  enhancement stays inside the frozen controller set).

#### GOTH-5.2 — implement command/combobox primitives P50–P51

- **depends_on:** phase 4 gate
- **model:** opus
- **files:** Combobox, Command sources/controllers/tests/showcase
- **work:** Implement shared filtering/active-item mechanics, grouped/empty/
  loading results, async/server option replacement, form value, and screenreader
  announcements.
- **verify:** local and HTMX-fed result tests, keyboard/typeahead, latency/race
  fixture, form POST, and axe.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 4 gate MET and
  GOTH-5.1 complete (2026-07-17); GOTH-5.2 depends only on the Phase 4 gate —
  dependency-ready. Numeric order honored (only 5.2 done). Branch
  `authorization-v3`; unrelated worktree (the authorization-v3 edits, the untracked
  `ui/`/`examples/goth-showcase/` GOTH trees, the untracked authentication
  `policy*.go`) left untouched. **No new §8 controller name introduced** — both
  primitives are backed by the frozen `gothCombobox`, refined in place (the
  pre-provisioned controller per README §8), gaining an `aria-activedescendant`
  keyboard loop, an inline (always-open) mode for Command, a client/server filter
  split, and post-HTMX-swap re-indexing. The three latent `this.$el` defects were
  already fixed in GOTH-4.1; this task builds on that.

  **P50 Combobox (F4).** Server-ownership model: the server owns the option DATA and,
  in server-filter mode, the FILTERING and empty-state markup — the async specimen's
  input fetches a freshly filtered option fragment from `/combobox/options` (server
  computes the match + renders its own empty state); in the default client-filter
  mode the controller only hides non-matching already-rendered options and toggles
  the empty state. The server always owns the authoritative submitted value.
  Compound parts: `Combobox` (root, `x-data="gothCombobox"`, `data-filter`,
  server-owned `data-state`), `ComboboxInput` (`role=combobox`, `aria-autocomplete=list`,
  `aria-expanded`, `aria-controls`, `aria-activedescendant`), `ComboboxListbox`
  (`role=listbox`), `ComboboxOption` (`role=option` on a native `<button
  type=submit>` carrying name/value), `ComboboxEmpty`. No-JS baseline: options are
  native submit buttons and the listbox is rendered server-open (`ComboboxProps.Open`),
  so picking an option submits the value to the enclosing form with Alpine/HTMX
  absent (proven POST). Enhanced path: `gothCombobox` opens the listbox on
  focus/typing, moves an active option with ArrowUp/Down/Home/End via
  `aria-activedescendant` (focus stays on the input, options demoted to
  `tabindex=-1`), Enter activates the active option (the same native submit / HTMX
  round-trip), Escape/outside dismiss through the shared overlay mechanics.
  HTMX/no-JS parity: the async input carries raw `hx-get`/`hx-trigger`/`hx-target`/
  `hx-swap` via `Base.Attributes` (the frozen §9 merge rule) to swap the listbox
  options; the no-JS equivalent is the same query submitting and reloading the
  filtered options. Listbox display keys off the ROOT's `data-state` from a single
  selector so the controller-owned state and the server-open no-JS baseline agree.

  **P51 Command (F4).** A command palette sharing `gothCombobox` in INLINE mode
  (`data-inline` on the root): the listbox is always visible (never a popup), so no
  overlay/dismiss is created; filtering + the active-item keyboard loop + activation
  are the same mechanics. Parts: `Command`, `CommandInput` (`aria-expanded="true"`),
  `CommandList` (`role=listbox`), `CommandGroup` (`role=group` + an `aria-hidden`
  heading referenced by `aria-labelledby`), `CommandItem`, `CommandEmpty`,
  `CommandSeparator`. Link-first no-JS baseline (the Navigation Menu precedent): a
  `CommandItem` with a URL renders a real `<a href role=option>` and otherwise a
  `<button type=submit role=option>` carrying name/value, so with no JavaScript the
  list is fully visible and items navigate/submit; enhanced, arrows loop the active
  item and Enter activates it.

  **ARIA fixes made during the browser/axe pass:** the empty-results message and the
  Command separator were originally `role="status"` / `role="separator"` — both are
  disallowed children of `role="listbox"` (axe `aria-required-children`, critical).
  Fixed at source: `ComboboxEmpty`/`CommandEmpty` are `role="option"` +
  `aria-disabled="true"` (an allowed listbox child), `hidden` until Open and never
  treated as a selectable option by the controller (`data-slot="empty"`);
  `CommandSeparator` is a decorative `aria-hidden` divider (no `role="separator"`).

  **`Attrs` gaps noted for the GOTH-5.3/7.3 §9 finalization** (recorded, not frozen
  here): the async option seam uses raw `hx-*` on `Base.Attributes` because the
  provisional `htmx.Attrs` struct lacks (1) `hx-trigger` MODIFIERS — the combobox
  needs `input changed delay:150ms`, and `Attrs.Trigger` is a plain string with no
  first-class debounce/`changed`/event-filter support; (2) `hx-swap` on a
  non-default target set from the input; and (3) aligning `Attrs.URL` with the typed
  `primitives.URL`. These are exactly the candidate gaps §9 lists; the combobox is a
  concrete consumer for that review.

  Files: `ui/goth/primitives/combobox.{go,templ}` + `command.{go,templ}` (+ generated
  `_templ.go`), `palette_test.go` (render/ARIA/merge/enum tests);
  `assets/src/js/controllers/combobox.js` (refined `gothCombobox`: inline mode,
  activedescendant loop, client/server filter, post-swap re-index — no new §8 name);
  `assets/src/css/components.css` (combobox + command rules keyed off the stable
  `goth-combobox`/`goth-command`/`data-*` surface, no inline style, listbox display
  from the root `data-state`, reduced-motion already covered); showcase
  `internal/showcase/specimens_primitives_palette.go` (Combobox client/async/no-JS +
  Command Interactive/no-JS specimens + `/combobox/options` async seam,
  `/combobox/pick` GET+POST echo, `/command/run` echo routes), `registry.go` (P50/P51
  in `ImplementedPrimitives` + registration), `showcase.go` (fixture wiring); e2e
  `e2e/tests/palette.spec.ts`.

  Verify results (exact): `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS (deterministic render tests). `cd examples/goth-showcase && go
  build/test/vet ./...` PASS (the completeness test now requires + finds P50/P51
  specimens). `make generate` (templ) clean; `make generate-ui-assets` byte-identical
  on a second rebuild (runtime.js `37f16f3d` for the gothCombobox refinement,
  theme.css `17040d91` for the new CSS, htmx.js unchanged `8689e2e2`).
  `make test-ui-browser` **204 passed** (was 186; +18 = 6 palette specs ×
  Chromium/Firefox/WebKit), including the whole-catalog axe crawl (zero violations)
  and the strict-CSP/no-eval/no-remote sweep (zero securitypolicyviolation) over the
  five new specimens. Per-engine palette proof green in all three engines: the
  combobox opens on focus, client-filters as you type, moves the active option with
  the arrows (aria-activedescendant), and Enter submits the value; the async combobox
  swaps in a server-filtered option list (10 → 1 for "cher", server-rendered empty
  for "zzz"); the no-JS combobox picks an option and POSTs the value with Alpine
  absent; the command palette shows an always-visible grouped list, filters, loops
  the active item, and Enter follows the item; the no-JS command renders the grouped
  list and its links navigate. `make check` and `make guard` both green ("all checks
  passed"; G17/G18 ui guards pass — the refinement stays inside the frozen controller
  set). **Blocked:** none. **Notes (follow-ups, not silently fixed):** (1) parity
  rows P50/P51 stay UNCHECKED and catalog status stays `planned` until the GOTH-5.5
  wave audit; (2) the §9 `Attrs` gaps above are recorded for the GOTH-5.3/7.3
  finalization; (3) selection uses commit-on-select (a native submit/navigate) rather
  than a set-a-hidden-value-then-submit-later model — the DatePicker/Calendar
  precedent — which keeps the no-JS and HTMX paths identical; a deferred "stage a
  value without a round-trip" variant is available via a caller-owned hidden input if
  a real adopter needs it.

#### GOTH-5.3 — implement Data Table P52

- **depends_on:** GOTH-2.3, GOTH-3.2, GOTH-4.3
- **model:** opus
- **files:** Data Table sources/models/tests/showcase HTMX fixtures
- **work:** Keep rows/sort/filter/pagination authoritative on the server;
  compose Table, Checkbox, menus, pagination, loading/empty/error regions, and
  optional HTMX transitions. Preserve shareable URLs and full-document fallback.
- **verify:** non-JS and HTMX journeys produce equivalent state; validation/
  error/history swaps; keyboard table actions; narrow viewport.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 4 gate MET and
  the component prerequisites GOTH-2.3 (Table P24, Checkbox families / Input),
  GOTH-3.2 (Checkbox P28), GOTH-4.3 (anchored prims, menus reachable) all complete;
  GOTH-5.3 dependency-ready. Numeric order honored (only 5.3 done). Branch
  `authorization-v3`; unrelated worktree (the authorization-v3 edits, the untracked
  `ui/`/`examples/goth-showcase/` GOTH trees, the untracked authentication
  `policy*.go`) left untouched. **No new §8 Alpine controller name introduced** —
  the Data Table is server-owned and composes existing primitives (runtime.js
  unchanged `37f16f3d`).

  **Job 1 — Data Table (P52, family F4).** A server-owned data grid COMPOSED from
  existing primitives, not re-implemented: Table (P24, its responsive scroll
  wrapper + semantics), Pagination (P19), Checkbox (P28), and the Badge status
  cell — plus a thin new compound surface for the genuinely-new bits: `DataTable`
  (the labelled `role="region"` wrapper), `DataTableToolbar` (the filter/actions
  row, deliberately OUTSIDE the swappable content so a live filter input keeps its
  focus/caret across an HTMX swap), `DataTableContent` (the swappable inner region
  = the HTMX target `#dt-content`; `Busy` → `aria-busy`+`data-state="loading"`),
  `DataTableSortHeader` (a `<th>` carrying `aria-sort` for the CURRENT state whose
  sort control is a real `<a href>` to the server-produced next-sort URL + a
  decorative direction glyph), `DataTableEmpty` (a colspan no-results body row), and
  `DataTableStatus` (an `aria-live` region for result-count/error announcements,
  `role=status` polite / `role=alert` assertive). `SortDirection` is a typed enum
  (none/ascending/descending) with a zero-value `none` default and a `Valid` test.
  The server owns ALL of sort/filter/page/selection: Go filters (name-contains),
  sorts (name/age/role, asc/desc), paginates (page size 4), and reflects
  server-owned selection into each row's Checkbox `Checked`. The primitive holds
  none of it.

  No-JS baseline: sort headers and pagination are real links, and the filter is a
  form GET — every state has a shareable URL (`/data-table?q=…&sort=…&dir=…&page=…&
  sel=…`) and reloads the whole document; selection checkboxes submit natively.
  HTMX-enhanced parity: the filter input, sort links, and pagination links carry
  explicit `hx-*` (from the typed `htmx.Attrs`) that swap `#dt-content`'s
  `outerHTML` with `show:none`+`focus-scroll:false` so the viewport neither jumps
  nor steals focus; the toolbar (and its filter input) stay mounted, so the caret
  survives — proven equivalent to the no-JS reload. The `/data-table` route returns
  the swappable content region to an HTMX request and the full document to a direct
  request (shareable URLs + history re-fetch correctness). No inline `style=`;
  responsive Table wrapper reused; component CSS keyed off the stable
  `goth-data-table` / `data-*` surface (loading affordance driven by the
  server-owned `data-state` and HTMX's own `.htmx-request` class, never JS
  geometry). Row-action menus and a select-all master control are recorded
  deferrals (compose the existing Dropdown Menu / add a controller-free server
  "select all matching" — neither is frozen by the P52 catalog row, which names only
  sort/filter/page + responsive + HTMX-optional).

  **Job 2 — HTMX `Attrs` finalization (R7 first recorded review point).** With the
  Data Table + the 5.2 Combobox as real consumers, the §9 `Attrs` field set is now
  FROZEN (see the dated GOTH-5.3 addendum in `gate-b-review.md` and README §9).
  Finalized because a real consumer demonstrated the need: (1) `Trigger` became a
  typed `htmx.Trigger` (`Event`/`Changed`/`Delay`/`Throttle`) — closes the exact
  gap GOTH-5.2 recorded (`input changed delay:150ms`); the 5.2 combobox async
  specimen was migrated off raw `hx-*` onto it. (2) `SwapMods htmx.SwapModifiers`
  (`Show`/`Scroll`/`FocusScroll`/`Settle`) — the Data Table's scroll/focus-preserving
  content swap. Kept PROVISIONAL for GOTH-7.3 (no Phase-5 consumer, markers NOT
  removed): `Vals`, `Include`, `Headers`, `DisabledElt`, further trigger/swap
  modifiers, typed-URL alignment, and the CSRF hidden-input-vs-`hx-headers` posture
  — the Data Table is GET-only with server-rendered shareable URLs already carrying
  full state, so none of these has a consumer yet.

  Files: `ui/goth/primitives/data_table.{go,templ}` (+ generated `_templ.go`),
  `data_table_test.go` (region/content/sort-header/empty/status/merge/enum +
  no-inline-style tests), `glyphs.templ` (chevronUp + sort-neutral decorative
  glyphs), `assets/src/css/components.css` (P52 rules + reduced-motion entry);
  `ui/goth/htmx/attributes.go` (finalized `Attrs`: typed `Trigger` + `SwapModifiers`)
  + `htmx_test.go`; showcase `internal/showcase/specimens_primitives_data.go`
  (no-JS + HTMX specimens + the `/data-table` server round-trip route),
  `specimens_primitives_palette.go` (combobox async migrated to typed `Trigger`),
  `registry.go` (P52 in `ImplementedPrimitives` + registration), `showcase.go`
  (fixture wiring); e2e `e2e/tests/data-table.spec.ts`.

  Verify results (exact): `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS (htmx `TestAttrsTriggerModifiers`/`TestAttrsSwapModifiers` +
  data_table deterministic render tests). `cd examples/goth-showcase && go
  build/test/vet ./...` PASS (completeness test finds the P52 specimens). `make
  generate` (templ) clean; `make generate-ui-assets` byte-identical on rebuild
  (runtime.js unchanged `37f16f3d` — no controller; htmx.js unchanged `8689e2e2`;
  theme.css `d9c83ee5` for the new P52 CSS). `make test-ui-browser` **225 passed**
  (was 204; +21 = 7 data-table specs × Chromium/Firefox/WebKit), including the
  whole-catalog axe crawl (zero violations) and the strict-CSP/no-eval/no-remote
  sweep (zero securitypolicyviolation) over the two new specimens. Per-engine
  Data Table proof green: no-JS sort links re-sort via full navigation (aria-sort
  flips, youngest/oldest first row), the filter form GET narrows rows, pagination
  links page, selection checkboxes submit and the server reflects "1 selected";
  HTMX sorting swaps only `#dt-content` (URL pushed, toolbar/intro stay put,
  aria-sort updates), the debounced live filter swaps content while the filter
  caret + value survive, and pagination swaps the region. `make check` and `make
  guard` both green ("all checks passed"; G17/G18 ui guards pass). **Blocked:**
  none. **Notes (follow-ups, not silently fixed):** (1) parity row P52 stays
  UNCHECKED and catalog status stays `planned` until the GOTH-5.5 wave audit; (2)
  the residual §9 `Attrs` gaps + CSRF posture stay PROVISIONAL for GOTH-7.3 (the
  second recorded review point); (3) row-action dropdown menus and a select-all
  master control are recorded deferrals (not frozen by the P52 row).

#### GOTH-5.4 — implement Sidebar P54

- **depends_on:** GOTH-3.1, GOTH-4.2, GOTH-4.4
- **model:** opus
- **files:** Sidebar sources/controllers/tests/showcase
- **work:** Compose semantic navigation with desktop collapse and mobile sheet;
  keep preference persistence opt-in and namespaced.
- **verify:** desktop/mobile keyboard, focus, RTL, reduced motion, persistence
  off/on, and no-JS navigation tests.
- **evidence (2026-07-17):** Completed. Dependency confirmed: Phase 4 gate MET and
  the component prerequisites GOTH-3.1 (Collapsible/gothCollapse), GOTH-4.2
  (Sheet/gothDialog), GOTH-4.4 (navigation link patterns) all complete —
  dependency-ready. Branch `authorization-v3`; the whole `ui/` tree is untracked
  worktree, unrelated changes untouched.
  **State-ownership model.** The SERVER owns all three authoritative states and the
  primitive holds none: (1) the desktop expanded/collapsed rail via
  `SidebarProps.Collapsed` → root `data-collapsed` (CSS narrows the rail + visually
  hides labels, accessible names kept); (2) the mobile off-canvas open/closed via
  `SidebarProps.Open` → the gothDialog `data-state` (server-open sheet readable with
  no JS); (3) the active navigation item via real `<a>` links carrying
  `aria-current="page"` (the P44 link-first precedent). Every state change is a server
  round-trip: `SidebarRail` is a real link to a host toggle URL (flips collapsed) with
  `aria-expanded`; nav items are real links; the `/sidebar` route re-renders the full
  document for the query. Preference PERSISTENCE is server-owned/host-namespaced (the
  route reflects the query; a host persists in its own cookie/param) — the kit adds
  **no client persistence surface**, honoring the row's opt-in/namespaced freeze
  without introducing a cookie seam into the public API.
  **No new controller (frozen §8 unchanged).** Sidebar is a COMPOSITION: the mobile
  off-canvas reuses the frozen gothDialog overlay mechanics (scrim/panel, focus
  trap+restore, scroll lock, inert, nested Escape/outside dismiss) exactly as Sheet
  (P47), and nested groups reuse Collapsible (P29, native `<details>`). One markup
  renders both breakpoints via CSS (mobile-first off-canvas sheet; ≥48rem static
  rail; scrim/trigger/close suppressed on desktop, rail collapse shown). The
  desktop collapse ships as the honest server round-trip (the recorded F4-native
  precedent: Navigation Menu/Select/Popover shipped controller-free) — no `gothSidebar`
  name was added, so the plan's "controllers" file line is satisfied by composing
  gothDialog + gothCollapse. `runtime.js` (`37f16f3d`) and `htmx.js` (`8689e2e2`)
  rebuilt **byte-identical**; only `theme.css` changed (`d9c83ee5`→`7acbbd55`),
  proving the JS runtime surface is untouched.
  **No-JS + enhanced paths.** No-JS baseline proven three-engine: mobile sheet
  server-open and readable with Alpine absent, nav links carry aria-current and
  navigate, the rail collapse round-trip flips `data-collapsed`, persistence rides
  the URL across navigation, zero inline `style=`. Client enhancement proven:
  gothDialog opens the mobile sheet (keyboard Enter/Escape, focus trap, scrim
  dismiss, focus restore to trigger, honest `aria-modal` lifecycle). Responsive
  proven: desktop rail visible in flow, trigger/scrim hidden. RTL proven: logical
  properties flip the panel to the inline-end edge (`right:0`). Reduced motion
  proven: the sheet still opens with the slide animation collapsed.
  **Files.** New: `ui/goth/primitives/sidebar.go` (+ generated `sidebar_templ.go`),
  `ui/goth/primitives/sidebar.templ`, `ui/goth/primitives/sidebar_test.go`,
  `examples/goth-showcase/internal/showcase/specimens_primitives_sidebar.go`,
  `examples/goth-showcase/e2e/tests/sidebar.spec.ts`. Edited:
  `ui/goth/assets/src/css/components.css` (P54 block + reduced-motion + display:contents
  layout), regenerated `ui/goth/assets/dist/theme.7acbbd55.css` + `manifest.json`,
  `examples/goth-showcase/internal/showcase/registry.go` (P54 + register),
  `examples/goth-showcase/internal/showcase/showcase.go` (route wiring). 19 Sidebar
  parts: SidebarProvider/Inset, Sidebar, SidebarTrigger, SidebarPanel, SidebarHeader/
  Content/Footer, SidebarGroup/GroupLabel, SidebarMenu/MenuItem/MenuButton,
  SidebarMenuSub/SubItem/SubButton, SidebarSeparator, SidebarRail, SidebarClose;
  SidebarSide enum.
  **Verify results.** `ui/goth` `go build`/`go test`/`go vet` green (12 Sidebar
  render/enum/merge/no-inline-style tests pass). Showcase `go build`/`go test` green
  (P54 registered → completeness test passes). `make check` and `make guard` green
  (all 17 guards incl. G17 ui isolation; templ + asset drift clean; assets
  reproducible byte-identical). `make test-ui-browser` **255 passed** (3 engines,
  Chromium/Firefox/WebKit; +30 Sidebar tests over GOTH-5.3's 225), axe green on every
  specimen incl. the new `primitive-sidebar`/`-nojs`/`-rtl`, strict CSP clean (zero
  securitypolicyviolation). Parity row P54 stays UNCHECKED and catalog stays
  `planned` until the GOTH-5.5 wave audit.

#### GOTH-5.5 — close the six-entry composite wave

- **depends_on:** GOTH-5.1, GOTH-5.2, GOTH-5.3, GOTH-5.4
- **model:** fable for audit, opus for remediation
- **files:** catalog and phase sources/tests/docs
- **work:** Audit P49–P54 for locale, server ownership, progressive enhancement,
  CSP, and accessibility.
- **verify:** `make test-ui-browser`; `make check && make guard`.
- **evidence (2026-07-17):** Completed. Dependency confirmed: GOTH-5.1 (P49/P53),
  GOTH-5.2 (P50/P51), GOTH-5.3 (P52), GOTH-5.4 (P54) all complete with recorded
  evidence — GOTH-5.5 dependency-ready. Branch `authorization-v3`; unrelated worktree
  (the authorization-v3 edits, the untracked `ui/`/`examples/goth-showcase/` GOTH
  trees, the untracked authentication `policy*.go`) left untouched.

  **Adversarial audit — all six rows PASS the Phase 5 gate (no task reopened).**
  Each row was driven in the real browser (not asserted from exit codes), and the
  gate dimensions were checked per row:
  - **P49 Calendar / P53 Date Picker (locale/date + server-ownership + no-JS/HTMX
    parity + history).** The server owns the whole month grid, weekday order,
    prev/next values, and day states; labels are overridable per-index (no CLDR
    dependency); deterministic July-2026 tests hold with no wall clock. No-JS day
    selection submits natively (`/calendar/select?date=2026-07-20` → parsed echo);
    the HTMX day-select swaps `#dp-fragment` and closes the native popover — proven
    equivalent to the no-JS full reload. **Back-button correctness driven:** the
    no-JS Date Picker field went `2026-07-15` → pick `2026-07-20` → browser Back
    restored `2026-07-15` (full-document re-fetch), matching HTMX-4-forward rule 6.
    APG grid semantics + a single server-chosen roving tab stop + the in-place
    `gothRovingFocus` grid mode drive the keyboard; disabled/outside days are
    non-focusable spans.
  - **P50 Combobox / P51 Command (server value + activedescendant loop + filter).**
    The server owns the option data and (server-filter mode) the filtering +
    empty-state markup; the input keeps focus and `aria-activedescendant` moves the
    active option. No-JS baseline is native submit-button/link options (proven POST /
    navigation with Alpine/HTMX absent). Filtered-open combobox verified visually
    (typing "a" narrows to the "a"-containing options); the Command inline palette
    renders the always-visible grouped Files/Navigation list.
  - **P52 Data Table (server sort/filter/page + no-JS/HTMX parity + history +
    responsive).** No-JS sort headers/pagination are real links and the filter is a
    form GET — every state has a shareable URL and full-document reload; selection
    checkboxes submit natively. HTMX swaps only `#dt-content` (`outerHTML`
    `show:none focus-scroll:false`) preserving the toolbar filter caret. **Back-button
    correctness driven and unambiguous:** name-asc (Ada Lovelace) → HTMX sort Age asc
    → sort Age desc (Grace Hopper, `?dir=desc&sort=age`); Back#1 restored
    `?dir=asc&sort=age` with Ada Lovelace (youngest); Back#2 restored the original
    name-asc specimen URL with Ada Lovelace — both URL and server-rendered content
    restored, not just the URL. **Narrow viewport driven:** at 360px the
    `[data-slot="table-container"]` responsive wrapper reports `overflow-x: auto` with
    `scrollWidth 405 > clientWidth 360` (scrollable), so the table scrolls
    horizontally while the toolbar/pagination stay readable.
  - **P54 Sidebar (server-ownership + responsive breakpoints + RTL + history).** The
    server owns desktop-collapsed, mobile-open, and active-item; every change is a
    server round-trip; persistence rides the URL (host-owned, no client persistence
    surface). Driven at three viewports: mobile off-canvas gothDialog sheet (480px,
    focus trap/Escape/scrim), desktop static rail expanded/collapsed (1280px), and
    RTL (480px). Collapse persistence carried across navigation via the URL.
  - **CSP + accessibility (whole wave).** Zero `securitypolicyviolation` and zero
    console errors across all specimens incl. the HTMX swap paths; axe green on every
    specimen (grid semantics, `aria-activedescendant`, `aria-sort`, `aria-current`,
    mobile-sidebar focus trap). No `unsafe-eval`/`unsafe-inline`, no remote origin.

  **Disposition 1 — GOTH-5.4 sidebar RTL entrance-animation nuance: FIXED (not
  deferred).** The panel is positioned with logical insets, so under RTL a
  `data-side="left"` panel settles at the physical right edge; the entrance keyframe
  used physical `translateX(-100%)`, so the ~200ms slide-in transient came from the
  wrong physical direction (the settled position was already correct — GOTH-5.4's
  RTL test deliberately measured settled geometry to avoid the transient). Fixed at
  source in `assets/src/css/components.css` with two `[dir="rtl"]` `animation-name`
  overrides (idiomatic to the file's existing RTL chevron-flip pattern) so the
  entrance follows the resolved physical edge; the `sidebar.spec.ts` RTL test now
  asserts the panel's computed `animationName` resolves to `goth-sidebar-in-right`,
  and a Chromium probe confirmed it live. This is a closeout remediation recorded here
  WITHOUT unchecking GOTH-5.4 (its acceptance — correct settled RTL geometry — still
  holds), matching the GOTH-3.4 (Tabs RTL) / GOTH-4.5 (anchor flake) precedent of
  fixing a wave nuance at source in the closeout. theme.css `7acbbd55` → `a40f4572`
  (runtime.js `37f16f3d` / htmx.js `8689e2e2` byte-identical; JS untouched).

  **Disposition 2 — recorded deferrals carried forward (none blocks the gate):**
  Calendar PageUp/PageDown month navigation (prev/next are Tab-reachable; month change
  is server-owned); Date Picker HTMX month navigation; Data Table row-action menus +
  select-all master control (neither frozen by the P52 row); Combobox "stage a value
  without a round-trip" variant (commit-on-select is the shipped model, matching the
  Calendar/Date Picker precedent; a caller-owned hidden input covers the staged case);
  Sidebar client-side persistence (server-owned/host-namespaced by design — the kit
  adds no client persistence surface). All are enhancements, not parity gaps.

  **Disposition 3 — §9 `Attrs` finalization state confirmed coherent.** Verified
  against `ui/goth/htmx/attributes.go` and README §9: the typed `Trigger`
  (`Event`/`Changed`/`Delay`/`Throttle`) and `SwapModifiers`
  (`Show`/`Scroll`/`FocusScroll`/`Settle`) are FROZEN at GOTH-5.3 as real fields with
  live consumers (Combobox async, Data Table content swap); `Vals`, `Include`,
  `Headers`, `DisabledElt`, further trigger/swap modifiers, typed-URL alignment, and
  the CSRF hidden-input-vs-`hx-headers` posture remain PROVISIONAL for GOTH-7.3 and
  appear only as recorded comment gaps (not struct fields). Markers intact.

  **Verify results (exact).** `cd ui/goth && go build ./... && go test ./... && go vet
  ./...` PASS. `cd examples/goth-showcase && go build ./... && go test ./... && go vet
  ./...` PASS. `make generate` (templ) clean no-op (updates=0). `make
  generate-ui-assets` byte-identical on a second rebuild (theme.css `a40f4572` for the
  RTL fix; runtime.js `37f16f3d` + htmx.js `8689e2e2` unchanged). `make check` and
  `make guard` both green ("all checks passed"; all 18 guards incl. G17/G18 ui
  isolation). `make test-ui-browser` **255 passed** (3 engines,
  Chromium/Firefox/WebKit; incl. the new RTL animation-name assertion), axe green on
  every specimen, strict CSP clean (zero securitypolicyviolation). **Layer-4
  screenshots captured and VIEWED** (calendar light/dark, date picker open, combobox
  filtered-open, command palette, data table sorted + narrow viewport, sidebar desktop
  expanded/collapsed + mobile sheet + RTL) — all correct; the initial dark-calendar
  capture caught the `color` CSS transition mid-flight (transient dark-on-dark) and a
  settle-wait recapture plus a computed-color probe (`oklch(0.98 0 0)` on
  `oklch(0.15 0 0)`) confirmed correct dark contrast — not a defect. Catalog P49–P54
  flipped to `accepted`; parity-matrix rows P49–P54 checked. **Blocked:** none.

### Phase 6 — specialized, media, and messaging primitives

#### GOTH-6.1 — implement Attachment P55

- **depends_on:** phase 5 gate
- **model:** opus
- **files:** Attachment sources/tests/showcase
- **work:** Implement media, content/title/description, actions, full-card
  trigger, group, three sizes, two orientations, and idle/uploading/processing/
  error/done presentation. Preserve independently focusable trigger/actions and
  meaning beyond color. The primitive displays caller-owned upload state; it
  does not select files or own upload routes, storage, progress, retries, or
  authorization.
- **verify:** icon/image, state, size/orientation, group scrolling, trigger/
  action keyboard order, accessible labels, and error-description tests.
- **evidence (2026-07-18):** Completed. Dependency confirmed: Phase 5 gate MET
  (2026-07-17, GOTH-5.5 wave audit). P55 Attachment shipped as a pure F3
  compound-parts primitive with NO new §8 controller name (the frozen nine-set is
  unchanged; StylesOnly, no Alpine/JS). Parts (Shadcn-style prefix grammar):
  `Attachment` root + `AttachmentMedia`/`AttachmentContent`/`AttachmentTitle`/
  `AttachmentDescription`/`AttachmentActions`/`AttachmentTrigger`/`AttachmentGroup`;
  enums `AttachmentSize` (sm/md/lg, zero=md), `AttachmentOrientation`
  (horizontal/vertical, zero=horizontal), `AttachmentState`
  (idle/uploading/processing/error/done, zero=idle) — each with `Valid()` +
  zero-value default. **State ownership (invariant 1).** The primitive DISPLAYS
  caller-owned upload state only: the server decides each card's `AttachmentState`
  and progress; the card renders the matching presentation. It selects no file and
  owns no upload route, storage, progress, retries, or authorization. **Meaning
  beyond color.** Every non-idle state renders an owned status affordance = a state
  glyph (spinner/check/alert-triangle) + a text label (a per-state default when
  `StatusLabel` is empty), plus a determinate native `<progress>` while uploading
  (value/max, no inline `style=`); the error label is the accessible error
  description. The state hue rides only the glyph and the card border, never the
  low-contrast label text (fixed during the axe pass — `--success` at 13px failed
  AA, so the label stays AA-safe muted and shape+text carry the meaning).
  **Independently focusable trigger/actions.** `AttachmentTrigger` (real `<a>` when
  URL set, else `<button type=button>`, required `Label` accessible name enforced
  by a contract test) stretches over the whole card via a CSS `::after` (no inline
  style); `AttachmentActions`/status sit above it (position/z-index in
  components.css) so each action button stays a separate, nameable tab stop, and
  keyboard order follows DOM order (trigger before actions).
  **No-JS baseline + real multipart round-trip.** Two StylesOnly specimens: a
  static gallery (all five states, icon+image media, three sizes, two orientations,
  a horizontally-scrolling `tabindex=0` group, and the trigger+independent-actions
  card) and `primitive-attachment-upload` — a REAL multipart no-JS upload:
  host-owned native `<input type=file>` + `<form enctype=multipart/form-data>`
  POSTs to the host `/attachment/upload` route, the SERVER stores the record and
  decides its state (done, or error with a description when a file exceeds the
  512 KB demo limit), then 303-redirects back to the specimen which re-renders the
  cards from the server-owned in-memory store — a full-document round-trip with no
  JavaScript. The primitive owns none of the input/route/storage. Component CSS in
  `assets/src/css/components.css` (no inline style, `goth-spinner-rotate` reused +
  added to the reduced-motion collapse, RTL-neutral logical sizing). Verify
  results: `ui/goth` + `examples/goth-showcase` build/test/vet green; module
  render/contract tests cover icon/image, each state, size/orientation, group
  scrolling + focusability, trigger link-vs-button + required label, error
  description, enum defaults, and attribute-merge ownership + no-inline-style;
  `make generate` no-op (templ committed); `make generate-ui-assets` reproducible
  (only `theme.css` → `541ed3a7`; `runtime.js`/`htmx.js` byte-identical); a
  3-engine `attachment.spec.ts` (18 tests: gallery states/sizes/orientation/group
  scroll/trigger+action focus order + the real multipart upload done/error
  round-trip); `make test-ui-browser` **273 passed** (3 engines, up from 255 — the
  axe/CSP crawl covers both new specimens with zero violations after the
  contrast/scroll-focus fixes); `make check` + `make guard` green; assets
  reproducible. Parity row P55 stays UNCHECKED and catalog status stays `planned`
  until the GOTH-6.6 wave closeout.

#### GOTH-6.2 — implement messaging primitives P56, P59–P60

- **depends_on:** phase 5 gate
- **model:** opus
- **files:** Bubble, Message, Message Scroller sources/controllers/tests/showcase
- **work:** Implement Bubble variants/alignment/groups/reactions and interactive
  link/button content; implement Message row/avatar/content/header/footer/group
  layout; then implement an append/prepend-capable scroller that anchors turns,
  follows streaming only at the live edge, opens saved transcripts, loads earlier
  history without jumping, jumps to named messages, preserves focus/reader
  intent, and exposes current/unread/scroll state without assuming chat domain
  models or transport.
- **verify:** keyboard/read-order, saved-thread initial position, streaming at/
  away from live edge, HTMX prepend/append, jump-to-message, focus and scroll
  preservation, Bubble variant/reaction/action semantics, Message alignment,
  RTL, and reduced motion.
- **evidence (2026-07-18) — COMPLETE.** Dependency confirmed: Phase 5 gate MET
  (2026-07-17), GOTH-6.1 complete. All three messaging primitives shipped. P56/P59
  are pure F3 compound-parts primitives with no controller; P60 is the F4
  controller-backed scroller. **Blocker history (resolved).** P60 first stopped on an
  unresolved owner decision: it demanded a scroll-anchoring controller none of the
  frozen nine §8 controllers could back, and the F4-native precedent could not apply
  (a no-JS transcript covers read/jump but not live-follow). The owner ratified the
  recommendation in a recorded 2026-07-18 `gate-b-review.md` addendum: **`gothMessageScroller`
  is ADDED to the frozen §8 set (now TEN controllers)** — a thin controller composing the
  frozen GOTH-4.1 mechanics + `mechanics/live-region.js` over a server-rendered
  `role=log` transcript. README §8's controller list now names the tenth controller and
  references that addendum.
  **P56 Bubble (F3).** Parts: `Bubble` root (`data-align` start/end, logical properties,
  RTL-correct) + `BubbleContent` (`data-variant`), `BubbleReactions`/`BubbleReaction`,
  `BubbleGroup`. Enums: `BubbleVariant` (SEVEN: default/primary/secondary/muted/accent/
  destructive/outline, each a frozen theme role, zero=default) and `BubbleAlign`
  (start/end), each `Valid()`+default+unknown→default. `BubbleReaction` is a real
  `<button>` (toggled `aria-pressed`/`data-pressed`) or `<a>` when a URL is set; the emoji
  is `aria-hidden`, the required `Label` is the accessible name (contract-tested), the
  count is text. The `muted` variant was made a quiet no-fill bubble in the axe pass so
  its text meets AA on the page background (`--muted-foreground` on `--muted` was 4.35:1).
  **P59 Message (F3).** Parts: `Message` root (`data-align`, RTL-safe row flip) +
  `MessageAvatar` (composes Avatar P03), `MessageContent`, `MessageHeader`,
  `MessageFooter`, `MessageGroup` (grouped-message slots — one avatar heading several
  MessageContent). `MessageAlign` enum. A Message composing a Bubble body is proven.
  **P60 Message Scroller (F4, `gothMessageScroller`).** Parts: `MessageScroller` root
  (binds the controller; `data-at-edge`/`data-unread` state), `MessageScrollerViewport`
  (`role=log`, required aria-label, tabindex=0 scroll region), `MessageScrollerHistory`
  (link when URL set — the no-JS reload — else button; `data-goth-history` prepend
  marker), `MessageScrollerContent` (the HTMX prepend/append target),
  `MessageScrollerStatus` (unread/jump-to-latest affordance, CSS-hidden while
  `data-unread="0"` so no-JS shows no dead control), `MessageScrollerJump` (native `#id`
  anchor). **No-JS baseline:** a readable server-rendered `role=log` transcript, a real
  "load earlier" link (the earlier route degrades to a full-document reload), native `#id`
  jump anchors, and CSS `overflow-anchor` to keep a prepend from jumping. **Enhancement
  (all delivered, 3-engine proven):** (a) live-edge following — sticks to the bottom while
  the reader is at the live edge, stops when they scroll up; (b) history-prepend-without-jump
  — the controller owns anchoring (`overflow-anchor:none` under `[data-enhanced]` so native
  and JS never double-correct; the snapshot is taken on `htmx:beforeSwap` and the scroll
  correction runs on `htmx:afterSettle` so it wins over HTMX's settle scroll; prepend vs
  append is detected by whether the leading/trailing child changed, not by which element
  initiated the request; the history trigger sits above the scroll region so its focus
  never steals the anchor position); (c) jump-to-message — a `data-goth-jump` anchor scrolls
  to `#id` and moves focus to the target (made programmatically focusable, not tab-ordered)
  preserving reader intent; (d) unread/scroll-state exposure — `data-at-edge`/`data-unread`
  on the root, documented `goth:change`/`goth:select` events, a revealed status affordance
  with a live count, and a polite `live-region.js` summary + `role=log` `aria-live`
  toggled off-edge (no double speech). **Verify results.** `ui/goth` +
  `examples/goth-showcase` build/test/vet green; module render/contract tests
  (`messaging_test.go` + `message_scroller_test.go`) cover the seven variants, alignment
  hooks, reaction button-vs-link + name + toggled state, grouped slots, the scroller
  controller-binding/state hooks, role=log naming, history link-vs-button + prepend marker,
  content/status/jump parts, enum defaults, attribute-merge ownership (incl. the x-data
  binding cannot be hijacked while caller hx-* still flows), and no-inline-style; README §8
  updated to the tenth controller; `make generate` no-op; `make generate-ui-assets`
  reproducible (theme.css `d25161d1`, runtime.js `2b6cbcd1` [new controller], htmx.js
  byte-identical); a 3-engine `messaging.spec.ts` (9 tests) + a 3-engine
  `message-scroller.spec.ts` (6 tests: honest markup, open-at-live-edge, live-edge follow,
  scroll-up-stops-following + unread + status jump-back, prepend-without-jump, jump-to-message
  focus); `make test-ui-browser` **318 passed** (3 engines, up from 273 — axe/CSP crawl
  covers all three new specimens with zero violations); `make check` + `make guard` green.
  Parity rows P56/P59/P60 stay UNCHECKED and catalog status stays `planned` until the
  GOTH-6.6 wave closeout.

#### GOTH-6.3 — implement spatial primitives P57, P61–P62

- **depends_on:** phase 5 gate
- **model:** opus
- **files:** Carousel, Resizable, Scroll Area sources/controllers/tests/showcase
- **work:** Prefer native scroll-snap/scrolling; add controls, status, keyboard
  resize, pointer capture, bounds, and opt-in persistence. Autoplay is off by
  default and must pause under all required conditions.
- **verify:** keyboard/pointer/touch, zoom, reduced-motion, overflow, RTL, and
  dynamic-style CSP tests.
- **evidence (2026-07-18) — COMPLETE: P57 + P61 + P62 SHIPPED.** All three spatial
  rows shipped. **Blocker history (resolved).** P61 Resizable first stopped on an
  unresolved owner decision: split-pane drag is not expressible in CSS and no frozen
  controller provided pointer-drag geometry, so it was reported with a concrete
  recommendation rather than adding a controller name silently (the GOTH-6.2
  gothMessageScroller precedent). P57 Carousel + P62 Scroll Area shipped in that first
  pass on a native/CSS baseline with no new controller. **Resolution:** the owner
  ratified the recommendation in a recorded 2026-07-18 `gate-b-review.md` addendum —
  **`gothResizable` is ADDED to the frozen §8 set (now ELEVEN controllers)**; README
  §8's list names the eleventh controller and references that addendum. Amendment 1
  (drop-Tailwind / host-stylesheet; RATIFIED + COMPLETE, GOTH-A.1–A.4) landed in the
  interim, so P61 was built on the post-Amendment-1 stack: the frozen invariant is now
  absolute — NO server-rendered `style` attribute or inline `<style>` ever; a
  controller-owned CSSOM `element.style.setProperty` write is the sanctioned
  dynamic-geometry path. The shipped P57/P62 work survived Amendment 1 unchanged.
  **P57 Carousel (F4, shipped native/no-controller).** Parts: `Carousel` root
  (`<section>` with `data-orientation` + `aria-roledescription="carousel"` + the
  region name), `CarouselContent` (the CSS scroll-snap track — a focusable,
  keyboard-scrollable overflow region: `tabindex=0` + `role=group` + `aria-label`,
  `scroll-snap-type: x/y mandatory`), `CarouselItem` (a snap-aligned slide,
  `role=group` + `aria-roledescription="slide"` + "n of m" name + stable id),
  `CarouselDots`/`CarouselDot`. Enum `CarouselOrientation` (horizontal zero /
  vertical) with `Valid()`+default. **No-JS baseline:** the track swipes/drags and
  scrolls slide-by-slide with the arrow keys natively, each slide snaps into place,
  and the `CarouselDot`s are REAL in-page anchor links (`Target` = the slide's
  `#id`) that scroll the target slide into view with Alpine absent — proven in the
  browser (clicking "Go to slide 3" navigates to `#carousel-slide-3` and advances
  `scrollLeft`). The server-chosen current dot carries `aria-current="true"` +
  `data-current`. **Deferred (recorded, not shipped):** relative previous/next arrow
  stepping, current-dot tracking as the reader manually scrolls, and opt-in autoplay
  all require scroll-position observation — the same class of behavior that earned
  P60 its controller. Flagged for the same owner decision as P61 below; they are
  enhancements over a complete, usable no-JS carousel, not a parity gap in the
  baseline.
  **P62 Scroll Area (F4, shipped native/no-controller).** `ScrollArea` is a single
  native overflow region (NOT a synthetic JS scrollbar): `data-orientation`
  (vertical zero / horizontal / both) picks the axis, `tabindex=0` + `role=group` +
  `aria-label` make it a focusable, named, keyboard-scrollable region (the P24/P60
  precedent — proven: End scrolls the region), and the affordance enhancement is a
  slim themed scrollbar via `scrollbar-width`/`scrollbar-color` + the WebKit
  scrollbar pseudo-elements. Enum `ScrollAreaOrientation` with `Valid()`+default.
  **P61 Resizable (F4, `gothResizable`).** Parts: `Resizable` root (`role=group`
  implied via composition; binds `x-data="gothResizable"`, carries `data-orientation`
  + the `data-default-size` geometry bucket), `ResizablePane` (`role=group` scrollable
  region, the first pane is the primary sized from `--goth-resize-basis`),
  `ResizableHandle` (`role=separator`, `tabindex=0`, `aria-orientation`,
  `aria-valuenow/valuemin/valuemax`, `aria-controls` → the primary pane id). Enum
  `ResizableOrientation` (horizontal zero → vertical separator / vertical → horizontal
  separator); `DefaultSize` snaps to the nearest 5 in [5,95]; handle `Value` clamps
  into [`Min`,`Max`] (defaults 10/90). **No-JS baseline (server-owned split):** the
  root's `data-default-size` maps to the primary pane's `flex-basis` through the
  `--goth-resize-basis` custom property via 19 external CSS buckets (no server-rendered
  `style=`), so the split renders at the server-chosen ratio with Alpine absent —
  proven in the browser (primary pane ~40% of the container, zero `[style]` on the
  StylesOnly page). The separator is a static divider without JS. **Enhancement
  (gothResizable, ELEVENTH §8 controller):** pointer drag with `setPointerCapture`,
  APG window-splitter keyboard (ArrowLeft/Right for horizontal, ArrowUp/Down for
  vertical, Home→min, End→max) with a 5% step, min/max clamping, and RTL-aware arrow
  direction (`getComputedStyle(root).direction === "rtl"` — the roving.js precedent:
  ArrowLeft grows the inline-end primary under `dir=rtl`). The controller writes the
  new geometry through the CSSOM `--goth-resize-basis` write (`element.style.setProperty`
  — the sanctioned dynamic-geometry path, CSP-safe, never in rendered HTML) and mirrors
  `aria-valuenow`. All proven 3-engine: keyboard 40→45→35, End→85 (clamped), Home→15;
  pointer drag grows the primary and sets `--goth-resize-basis`; vertical ArrowUp/Down;
  RTL ArrowLeft grows. It composes the GOTH-4.1 descendant-`$el` init discipline; no
  `mechanics/*` fork. **Deferred (recorded, not shipped):** opt-in persisted geometry
  (a host round-trip — server owns it, no client persistence surface added); the
  Carousel relative prev/next + current-tracking + autoplay enhancements remain
  deferred (would need a `gothCarousel` controller; not requested).
  **Verify results.** `ui/goth` + `examples/goth-showcase` build/test/vet green;
  module render/contract tests (`spatial_test.go`) cover the scroll-area
  region/axis/keyboard-reachability, the carousel region/track/slide roles + dot
  link-vs-inert-marker + current status, the resizable server-split baseline
  (role=group panes, role=separator handle with the APG value attributes + controller
  binding + geometry bucket), the handle bound defaults/clamp/separator-orientation
  flip, the enum defaults + DefaultSize snap, attribute-merge ownership (incl. the
  `x-data` binding + geometry bucket cannot be hijacked), and the
  no-server-rendered-style invariant; README §8 updated to eleven; `make generate`
  no-op (templ committed); `make generate-ui-assets` reproducible (`runtime.js`
  `2b6cbcd1` → `80ebc7d6` — the new controller, expected; `theme.css` `ed23f5fb`;
  `htmx.js` `8689e2e2` + `theme-default.css` `ae49d971` byte-identical); a 3-engine
  `spatial.spec.ts` (9 tests: scroll-area overflow+name+keyboard, carousel snap track +
  roles + no-JS dot navigation, resizable server-split baseline + keyboard/bounds +
  pointer-drag CSSOM + vertical + RTL); `make test-ui-browser` **360 passed** (3
  engines, up from 345 — the axe/CSP crawl covers all new specimens incl. the
  Interactive resizable CSSOM-write pages with zero violations); `make warm-scaffold-cache
  && make build && make vet && make test && make guard` all green; `make check` green.
  Parity rows P57/P61/P62 stay UNCHECKED and catalog status stays `planned` until the
  GOTH-6.6 wave closeout.

#### GOTH-6.4 — implement Chart P58

- **depends_on:** GOTH-2.1, GOTH-4.3
- **model:** opus
- **files:** Chart frame/models/adapters/tests/showcase
- **work:** Provide themed chart container, title/description, legend, tooltip,
  tabular fallback, semantic palette, and an engine-neutral adapter that accepts
  server-rendered SVG or a host renderer. Do not bundle a fake Recharts clone.
- **verify:** SVG and table fallback, keyboard/data description, dark/theme
  override, print, and contrast checks.
- **evidence (2026-07-18) — COMPLETE: P58 Chart SHIPPED (native/no-controller,
  server-rendered SVG).** Dependencies confirmed complete: GOTH-2.1 (content/status
  primitives incl. Card, the themed frame precedent) and GOTH-4.3 (the anchored/
  tooltip machinery) both accepted; Phase 5 gate MET; GOTH-6.1/6.2/6.3 done. Built
  strictly within the frozen post-Amendment-1 contract with **NO new §8 controller**
  and **NO owner decision required**: Chart is the F4-native precedent (Carousel/
  Scroll Area) — the row froze "no client-side charting library, no canvas dependency
  for the baseline", and a complete server-rendered SVG needs no controller.
  **Chart kinds (3).** `ChartKind` enum — `ChartBar` (zero value, grouped vertical
  bars), `ChartLine` (polyline + data-point dots), `ChartArea` (line + filled closed
  path). One `ChartSVG(ChartData)` renders all three.
  **Server-geometry model.** `computeChartLayout(ChartData)` is a pure Go function
  (unit-tested for deterministic coordinates): it collects the value domain, scales
  the value axis to a 1/2/5·10ⁿ "nice" ceiling (`niceCeil`), places a 0-baseline axis
  line + 5 grid/tick lines, computes category centers, and emits every mark as
  pre-formatted coordinate strings. Bars are grouped side-by-side per category (rect
  x/y/width/height on the baseline, negative values flip below it); lines/areas map
  each point to (category-center, value-y) as a `<polyline points>` (+ a closed
  `<path d>` for area) with `<circle cx/cy/r>` dots. ChartSVG emits ONLY sanctioned
  SVG presentation/geometry attributes (viewBox, x/y/width/height, points, d, cx/cy/r,
  text-anchor) — proven ZERO `style=` and ZERO per-mark `fill=` in every kind (a
  render test + the 3-engine `[style]` count == 0). Series color rides `data-series`
  ("1".."5" via `seriesToken`, honoring an explicit `ChartSeries.Color` and wrapping,
  else the series index) + external `.goth-chart-*[data-series]` CSS over the frozen
  `--chart-1..--chart-5` tokens, so a host retints the whole chart by redeclaring the
  tokens (browser-proven: series 1 ≠ series 2 computed fill, neither black/none).
  **Frame parts (F3, caller-composed):** `Chart` (themed `<figure>`, optional
  role=group + aria-label), `ChartHeader`/`ChartTitle`/`ChartDescription`,
  `ChartContent` (the **engine-neutral seam** — drop in `ChartSVG` OR a host-rendered
  `<svg>`; the row's "accepts server-rendered SVG or a host renderer"),
  `ChartLegend`/`ChartLegendItem` (swatches keyed to `data-series`), `ChartTable`
  (native `<details>` disclosure). No fake Recharts clone — the kit ships a modest
  geometry helper + the neutral seam.
  **Tooltips.** Every mark carries a native SVG `<title>` (`"<series> — <label>:
  <value>"`) → a no-JS, CSP-safe hover tooltip on all profiles; no gothTooltip binding
  needed (the row's "CSS hover / existing machinery" allowance, satisfied natively).
  **Accessibility.** The SVG is `role="img"` named via `<title>`/`<desc>` +
  `aria-labelledby` (server-wired ids from `ChartData.BaseID`); the **tabular
  fallback** is `ChartTable` composing **Table (P24)** — a native `<details>` that
  opens with no JS and exposes the identical numbers (the keyboard/AT data path,
  browser-proven closed→open→4 rows→"100"). axe green on the crawl (dark included).
  **Files.** `ui/goth/primitives/chart.go` + `chart.templ` (+ generated
  `chart_templ.go`), `chart_test.go` (kind enum, niceCeil, seriesToken, geometry,
  SVG bar/line/area, frame parts, merge-ownership, no-inline-style); component CSS
  appended to `ui/goth/assets/src/css/components.css` (frame/axis/grid/series-token
  mapping/legend/table + reduced-motion transition-none for chart marks);
  showcase `examples/goth-showcase/internal/showcase/specimens_primitives_chart.go`
  (one StylesOnly `primitive-chart` specimen: bar+line+area, each with header/SVG/
  legend/table fallback off shared data) + registry wiring + `P58` appended to
  `ImplementedPrimitives`; 3-engine `e2e/tests/chart.spec.ts` (4 tests: named role=img
  no-JS/no-style, line+area polyline/path/dots, CSS-token color, legend + no-JS table
  disclosure). **Verify results.** `ui/goth` + `examples/goth-showcase` build/test/vet
  green; `make generate` no-op (templ committed); `make generate-ui-assets`
  reproducible — `runtime.js 80ebc7d6` + `htmx.js 8689e2e2` + `theme-default.css
  ae49d971` byte-identical (NO controller/runtime change), only `theme.css` →
  `701432c3` (the chart CSS, expected); `make check` green (templ drift + per-module
  build/vet/test + all guards + asset drift); `make guard` green; `make
  test-ui-browser` **372 passed** (up from 360 — 4 chart tests × 3 engines; the axe +
  strict-CSP whole-catalog crawls auto-cover `primitive-chart` with zero violations/
  zero securitypolicyviolation on Chromium/Firefox/WebKit). Stale :8099 cleared before
  the run. Parity row P58 stays UNCHECKED and catalog status stays `planned` until the
  GOTH-6.6 wave closeout.

#### GOTH-6.5 — implement notification primitives P63–P64

- **depends_on:** GOTH-4.1
- **model:** opus
- **files:** Sonner, Toast sources/shared controller/tests/showcase
- **work:** Implement one live-region queue with composable Toast and an
  opinionated Sonner facade; pin priority, dedupe, pause, duration, focus,
  dismissal, action, and reduced-motion behavior.
- **verify:** polite/assertive announcements, pause on hover/focus/hidden page,
  keyboard dismissal/action, queue overflow, HTMX-triggered toast.
- **evidence (2026-07-18) — COMPLETE: P64 Toast + P63 Sonner SHIPPED over ONE
  gothToast live-region queue (NO new §8 controller, NO owner decision).** Dependency
  GOTH-4.1 (shared overlay/mechanics) confirmed complete; GOTH-6.1–6.4 done. Built
  strictly inside the frozen post-Amendment-1 contract: the pre-provisioned §8
  `gothToast` controller (a GOTH-1.4 foundation) was **refined in place** into the ONE
  live-region queue backing both primitives — the eleven-name §8 set is UNCHANGED (no
  new name, no reopen).
  **One queue, two primitives.** `gothToast` binds the **Toaster region** (not each
  toast) and owns the whole queue of `[data-slot="toast"]` entries — server-rendered
  and HTMX-appended alike (re-scanned on the bubbling `htmx:afterSettle`, the
  message-scroller idiom): announce-once, timers, pause, dedupe, overflow, dismissal,
  action, stacking. **P64 Toast** is the composable F4 shape — `Toaster` (region) +
  `Toast` (`Priority` polite/assertive, `Duration`, `DedupKey`) + `ToastTitle`/
  `ToastDescription`/`ToastAction` (link OR button) / `ToastClose`. **P63 Sonner** is
  the opinionated facade over the SAME queue with NO runtime of its own: `Sonner` is a
  `data-sonner` Toaster (opinionated `SonnerDefaultMax=3`), and `SonnerToast` renders a
  COMPLETE variant toast (success/info/warning/error) in one call — status accent +
  title + optional description/action + close — mapping variant → live-region priority
  (error/warning assertive, else polite) with an opinionated `SonnerDefaultDuration=4000`.
  **Announcement (no double-fire).** The single announcement channel is the frozen
  `mechanics/live-region.js` (`announce`, polite/assertive by `data-priority`, guarded
  once per toast via `data-goth-announced`). The visible Toaster is a reachable,
  non-trapping `role=region` landmark (`aria-label`, `tabindex=-1`) that is **NOT**
  itself aria-live, and the toasts carry **no** role=status/alert — so a toast is
  announced exactly once (browser-proven: server-rendered toasts announce on load; an
  HTMX assertive toast lands in the shared `role=alert` region; polite/assertive routed
  correctly; toasts expose no live role).
  **No-JS baseline.** The Toaster is a server-rendered region whose toasts are readable
  with no JS; auto-dismiss/pause/close/announce are enhancement (Alpine-absent StylesOnly
  keeps the markup honest). **No inline `style=` / no server-rendered style anywhere** —
  placement rides `data-position`, stacking `data-index`, all in external CSS
  (proven: Go render tests + a 3-engine `[style]==null` sweep + axe/CSP crawl).
  **Pause/timers.** Per-toast remaining-time timers PAUSE on region hover (bubbling
  `pointerover`/`pointerout` — the region is click-through, so the non-bubbling
  pointerenter cannot be used) and focus, and while `document.hidden`, and RESUME
  afterward (`data-paused` hook browser-proven across all three engines). Overflow caps
  visible toasts at `data-max` (oldest out); reduced-motion collapses the entrance/exit
  animation.
  **Files.** `ui/goth/primitives/toast.go`+`toast.templ` (+ generated `toast_templ.go`),
  `sonner.go`+`sonner.templ` (+ `sonner_templ.go`), `toast_test.go`+`sonner_test.go`, a
  `templAttrsWith` helper in `render.go`; the refined `assets/src/js/controllers/toast.js`
  (register.js/runtime unchanged — gothToast already registered); toast/sonner CSS
  appended to `assets/src/css/components.css` (+ the reduced-motion collapse); showcase
  `examples/goth-showcase/internal/showcase/specimens_primitives_notification.go`
  (`primitive-toast` + `primitive-sonner`, Full profile, server-rendered baseline +
  HTMX-triggered timed/dismissible/variant toasts + fragment routes) + registry wiring +
  `P63`/`P64` in `ImplementedPrimitives`; 3-engine `e2e/tests/toast.spec.ts` (8) +
  `sonner.spec.ts` (4). **Verify results.** `ui/goth` + `examples/goth-showcase`
  build/test/vet green; `make generate` no-op (templ committed); `make
  generate-ui-assets` reproducible — `htmx.js 8689e2e2` + `theme-default.css ae49d971`
  byte-identical (NO htmx/theme-palette change), `runtime.js → ddc6f2ce` (the refined
  controller) and `theme.css → f2474b2b` (the toast/sonner CSS), both expected; `make
  check` green (templ drift + per-module build/vet/test + all 18 guards + asset drift);
  `make guard` green; `make test-ui-browser` **408 passed** (3 engines, up from 372 —
  36 new = 12 tests × 3 engines; the axe + strict-CSP whole-catalog crawls auto-cover
  `primitive-toast`/`primitive-sonner` with zero violations / zero
  securitypolicyviolation on Chromium/Firefox/WebKit). Stale :8099 cleared before every
  run. Parity rows P63/P64 stay UNCHECKED and catalog status stays `planned` until the
  GOTH-6.6 wave closeout.

#### GOTH-6.6 — close the 64-entry parity milestone

- **depends_on:** GOTH-6.1, GOTH-6.2, GOTH-6.3, GOTH-6.4, GOTH-6.5
- **model:** fable for audit, opus for remediation
- **files:** entire catalog, primitive docs/tests/showcase
- **work:** Audit every P01–P64 against the parity definition and official dated
  source. No checkbox is granted for a stub, untested state, or showcase-only
  facade. Record deliberate GOTH differences entry by entry.
- **verify:** catalog completeness test; full browser matrix; `make check && make guard`.
- **evidence (2026-07-18) — COMPLETE: the 64-entry Shadcn-parity milestone is CLOSED.**
  Dependencies confirmed: GOTH-6.1–6.5 all complete (Phase 6 primitives P55–P64
  shipped). This is the wave closeout and the milestone record for all 64 parity
  rows. The ten Phase-6 rows (P55–P64) each got a full per-row parity check; the
  other 54 rows (P01–P54, already accepted at their wave closeouts) got a
  confirmation sweep that Phase 6 + Amendment 1 regressed nothing. **No task was
  reopened; no row failed; the gate was not softened.**

  **Per-row parity — P55–P64 (family shape / no-JS baseline with Alpine absent /
  enhancement / accessibility / CSP / reduced-motion / RTL):**
  - **P55 Attachment (F3, no controller) — PASS.** Compound media/content/actions/
    trigger/group parts + size/orientation/state enums; no-JS baseline is a static
    gallery AND a real multipart `<form enctype=multipart>` upload round-trip (host
    owns input/route/storage; server decides done-vs-error>512KB and 303-redirects);
    meaning-beyond-color via glyph + AA-safe muted label + determinate `<progress>`;
    independently focusable trigger (CSS `::after` stretch, no inline style) + actions
    in DOM order; group is a `role=group` `tabindex=0` scroll shelf. Browser-viewed:
    five upload states, icon+image media, three sizes, two orientations render styled.
  - **P56 Bubble (F3, no controller) — PASS.** Seven `data-variant` surfaces, start/end
    logical alignment, `BubbleGroup`, `BubbleReaction` (real `<button>`/`<a>`, required
    accessible name, `aria-pressed`), interactive link/button content; muted variant is a
    quiet no-fill bubble (axe AA). No-JS presentational. Browser-viewed: seven variants +
    aligned thread + reactions render styled.
  - **P57 Carousel (F4-native, no controller) — PASS.** CSS scroll-snap track (focusable
    keyboard-scrollable `role=group` named region) + snap-aligned slides
    (`aria-roledescription=slide`) + no-JS in-page anchor `CarouselDot`s (server-chosen
    `aria-current`); `aria-roledescription=carousel`. The row is "CSS scroll-snap baseline,
    controls/status, autoplay opt-in only": the dots ARE the controls+status, and
    autoplay opt-in-only is satisfied by its deliberate absence. Browser-viewed:
    horizontal snap track + active dot + vertical variant.
  - **P58 Chart (F4-native, no controller) — PASS.** Server-owned `computeChartLayout`
    (pure, unit-tested) + `ChartSVG` `role=img` emitting ONLY sanctioned geometry
    attributes with series color on `data-series` + external CSS over `--chart-1..5`
    (ZERO server-rendered `style=`/`fill=`); bar/line/area kinds; native `<title>` no-JS
    tooltips; engine-neutral `ChartContent` seam; `ChartTable` tabular fallback composing
    Table (P24). Browser-viewed: bar/line/area render with axes/grid/legend/table
    fallback, distinct series colors (dark included).
  - **P59 Message (F3, no controller) — PASS.** Aligned row with avatar/content/header/
    footer + `MessageGroup` grouped-message slots (one avatar); composes Avatar (P03) and
    Bubble (P56); RTL-safe logical properties; no-JS presentational. Browser-viewed:
    aligned rows, avatar+header+footer, message-composing-a-bubble, grouped messages.
  - **P60 Message Scroller (F4, `gothMessageScroller`) — PASS.** No-JS baseline is a
    readable server-rendered `role=log` transcript + a real "load earlier" link +
    native `#id` jump anchors + CSS `overflow-anchor`; the tenth §8 controller adds
    live-edge following, HTMX prepend-without-jump (beforeSwap snapshot / afterSettle
    restore), jump-to-message with focus, and unread/scroll-state exposure (`aria-live`
    toggled `off` off-edge to avoid double speech). Browser-viewed: bounded transcript,
    load-earlier link, jump anchors render styled.
  - **P61 Resizable (F4, `gothResizable`) — PASS.** Server-owned split baseline via
    external CSS `--goth-resize-basis` buckets (no server-rendered `style=`); `role=group`
    panes + `role=separator` handle with `aria-valuenow/min/max` + `aria-controls`; the
    eleventh §8 controller adds pointer drag (`setPointerCapture`) + APG window-splitter
    keyboard + RTL-aware direction, writing geometry through the CSSOM `setProperty`. Row's
    "persisted state opt-in" is satisfied by the server-owned round-trip with no client
    persistence surface. Browser-viewed: server 40/60 split (LTR) and mirrored inline-end
    primary (RTL) render styled.
  - **P62 Scroll Area (F4-native, no controller) — PASS.** A single native overflow region
    (NOT a synthetic scrollbar): `data-orientation` axis, `tabindex=0`/`role=group`/
    `aria-label` focusable keyboard-scrollable region, themed scrollbar via
    `scrollbar-width`/`scrollbar-color` + WebKit pseudo-elements. Browser-viewed: vertical
    and horizontal overflow regions clip + scroll.
  - **P63 Sonner (F4, `gothToast`) — PASS.** The opinionated facade over the SAME queue —
    `data-sonner` Toaster + single-call `SonnerToast` variants mapping variant→priority
    with opinionated duration/overflow defaults; adds no controller/runtime of its own.
    Browser-viewed (dark): variant toasts with status accent + close render styled.
  - **P64 Toast (F4, `gothToast`) — PASS.** Composable `Toaster`/`Toast`/`ToastTitle`/
    `ToastDescription`/`ToastAction`/`ToastClose`; no-JS baseline is a server-rendered
    `role=region` landmark holding readable toasts (region NOT aria-live, toasts carry no
    role=status/alert → ONE announcement channel); the pre-provisioned §8 controller adds
    announce-once (polite/assertive by priority), pause-on-hover/focus/hidden-page timers,
    dedupe, `data-max` overflow, keyboard/pointer dismissal + actions. Browser-viewed
    (wide + 390px narrow): corner-anchored stack + close render styled and responsive.

  **Confirmation sweep — P01–P54 (no Phase-6 / Amendment-1 regression):** the
  three-engine axe + strict-CSP whole-catalog crawls cover EVERY registered specimen
  (Phase 2–6) with zero violations and zero console/`securitypolicyviolation` errors —
  which directly proves the two highest-risk regressions are absent: (a) Amendment 1
  touched every stylesheet, and a Layer-4 run-and-look at a sample of earlier-phase
  specimens (Alert P01, Card P08, Tabs P34, Dialog P39, Data Table P52; light + dark)
  confirms they still render fully styled; (b) the §8 controller registry grew to
  eleven with NO name collision (`register.js` binds each once; `Alpine.data` would
  throw on a dup) and NO registration failure (the strict-CSP crawl asserts a clean
  console on every Interactive/Full specimen). The `TestEveryImplementedPrimitiveHasSpecimen`
  catalog-completeness test (all 64 ids in `ImplementedPrimitives`, each with a
  registered specimen) is green.

  **Explicit dispositions (recorded):**
  1. **The GOTH-6.5 shared-live-region flag (last-wins vs per-toast queued
     announcement) — ACCEPTED, not a parity gap.** `mechanics/live-region.js` owns ONE
     pair of visually-hidden polite/assertive regions; `announce()` clears then sets
     `textContent` on the next frame, so a burst of near-simultaneous toasts collapses to
     the last message (standard ARIA live-region last-wins). Judged against the frozen row
     text — P64 "…and live-region **priority**", P63 "opinionated toast queue API over
     **shared live-region runtime**" — the rows mandate a SHARED live-region runtime with
     polite/assertive priority routing (both delivered and 3-engine proven), NOT a
     per-toast serial-announcement queue. Last-wins is exactly the "shared live-region
     runtime" contract and matches upstream Sonner's single shared region; per-toast
     queued announcement is a recorded enhancement beyond the row, not a required state.
     No task reopened.
  2. **All recorded Phase-6 deferrals confirmed enhancement-not-parity per each row's
     frozen text (none reopened):** Carousel relative prev/next + current-dot tracking +
     autoplay (P57 — the row's "controls/status" is met by the no-JS dots and "autoplay
     opt-in only" is met by its absence; the extras need scroll-observation, an
     enhancement); Resizable persisted geometry (P61 — the row's "persisted state opt-in"
     is a server-owned round-trip, no client persistence surface required); Chart pie/radial
     (P58 — the row froze "themed frame/legend/tooltip + server-SVG/engine seam" with no
     chart-kind mandate; bar/line/area + the neutral seam satisfy it); Message Scroller
     transport/SSE (P60 — the row is explicitly transport-agnostic, "without assuming …
     transport"; HTMX prepend/append + the no-JS reload satisfy it). The OTP auto-advance
     (P30) and Drawer drag-to-dismiss (P40) deferrals named for cross-reference belong to
     the already-closed Phase-3 (GOTH-3.4) and Phase-4 (GOTH-4.5) waves and were
     dispositioned there; both remain enhancements.
  3. **No parity row failed; no task was reopened; the gate was not softened.**

  **Verification matrix (all green):** `ui/goth` build/vet/test PASS and
  `examples/goth-showcase` build/vet/test PASS (both modules); `make check` PASS
  ("all checks passed" — includes `make generate` templ **no-op** drift check +
  `ui/goth/assets/dist`+`manifest.json` asset-drift check + per-module vet/build/test +
  integration-tag vet + `make guard`); `make generate-ui-assets` **reproducible /
  byte-identical** (zero git diff on `dist/`+`manifest.json`; `runtime.js ddc6f2ce`,
  `theme.css f2474b2b`, `htmx.js 8689e2e2`, `theme-default.css ae49d971`); `make guard`
  PASS (all 18 layering guards, incl. the ui outward-import G17 + require-whitelist G18);
  `make test-ui-browser` **408 passed** (3 engines — Chromium/Firefox/WebKit — including
  the whole-catalog axe crawl with zero a11y violations and the strict-CSP crawl with
  zero `securitypolicyviolation`/console errors). Stale `:8099` servers were killed before
  each run. **Layer-4 run-and-look (captured AND viewed, not asserted from exit codes):**
  the ten Phase-6 specimens at wide light + wide dark, one RTL (`primitive-resizable-rtl`,
  real `dir=rtl` — primary pane mirrored to the inline-end), two narrow (390px toast +
  message-scroller), plus a regression sample of earlier phases (Alert/Card/Tabs/Dialog/
  Data Table, light + dark) — all render styled with correct theming/RTL/responsive
  behavior. Screenshots under `examples/goth-showcase/e2e/screenshots/phase6/`
  (`capture-phase6.mjs`).

  **Catalog + matrix bookkeeping:** parity-matrix rows P55–P64 checked; `catalog.md`
  P55–P64 flipped `planned` → `accepted` (all 64 rows now `accepted`). **Phase 6 is
  COMPLETE and its gate is MET; the 64-entry Shadcn-parity milestone is CLOSED. Phase 7
  (GOTH-7.1, the first `components/` layer) is now dependency-ready.**

### Phase 7 — Gopernicus compositions and first-party adopters

#### GOTH-7.1 — build the first components layer

- **depends_on:** phase 6 gate
- **model:** opus
- **files:** `ui/goth/components/{forms,layouts,feedback,data}/**/*`
- **work:** Build only compositions proven repeatedly by feature/showcase use:
  document/app/auth shells, page header/action bar, form section/field/error
  summary/actions, empty/loading/error panels, confirmation workflow, and
  searchable table toolbar. Keep feature domain types out.
- **verify:** composition render/browser tests; each composition has at least
  two real/specimen consumers or a documented adopter need.
- **DONE (2026-07-18).** The first `components/` layer landed as four per-dir
  packages under `ui/goth/components/{layouts,forms,feedback,data}` (plus an
  internal `components/internal/kit` sharing the frozen `Base`/`MergeAttributes`
  grammar), building exactly the fourteen compositions the plan names and no more —
  every one composes primitives only (NO new primitive surface, NO new §8
  controller, NO domain type, NO route), emits zero server-rendered `style=`/inline
  `<style>`, and inherits its server-ownership + no-JS baseline from the composed
  primitives:
  - **layouts** — `DocumentShell` (centered content page: header/main/footer
    slots), `AppShell` (admin chrome: sidebar + header + main, one markup driving
    both breakpoints via CSS at ≥48rem), `AuthShell` (centered `Card`-composed auth
    layout: brand/title/description/footer), `PageHeader` (h1 title + description +
    end-aligned actions + breadcrumb slot), `ActionBar` (`role=toolbar` start/end
    clusters).
  - **forms** — `FormField` (the repeatable `Field`-composed label/control/
    description/error group with `DescriptionID`/`ErrorID` helpers so the caller
    wires the control's `aria-describedby`, and an `Error` that flags the field
    invalid), `FormSection` (titled `FieldGroup`), `ErrorSummary` (a destructive
    `Alert` listing in-page links to each offending field; empty → renders nothing),
    `FormActions` (submit/cancel row with end/start/between align enum).
  - **feedback** — `EmptyPanel`/`ErrorPanel` (bordered surfaces composing `Empty`;
    error tone is a `role=alert`), `LoadingPanel` (the panel is the single
    `role=status` live region and the composed `Spinner` renders decorative so AT
    hears the message once), `ConfirmDialog` (the destructive-confirmation workflow
    composing the gothDialog-backed `AlertDialog` family with NO new controller; the
    confirm button submits a server-owned form, destructive-by-default, ids derived
    from the required `Base.ID`).
  - **data** — `TableToolbar` (the searchable `role=search` form GET composing
    `DataTableToolbar` + `Input(search)` + a no-JS submit `Button` + filters/actions
    slots; a host enhances it with explicit `hx-*` through `SearchAttributes`).
  - **Consumers.** Every component has a real showcase specimen (new
    `SectionComponent`, ids `component-*`, driven by a new
    `TestEveryImplementedComponentHasSpecimen` gate over `ImplementedComponents`) —
    the specimen consumer — plus a documented adopter need in GOTH-7.2
    (authentication: `AuthShell`/`FormField`/`ErrorSummary`/`FormActions`) and
    GOTH-7.3 (CMS: `AppShell`/`PageHeader`/`ActionBar`/`TableToolbar`/`ConfirmDialog`/
    the feedback panels). The ConfirmDialog specimen submits a real server-owned
    `POST /components/confirm` so the workflow is proven end to end.
  - **Verification (exact).** `ui/goth`: `go build/test/vet ./...` green (per-package
    render/contract tests over label association, derived-id wiring, the frozen
    caller/owned attribute merge with the dropped `class` key, the invalid/error
    flag, align enum validity, HTMX pass-through, and a no-inline-style assertion per
    package). Component CSS added to `assets/src/css/components.css` (layout-only, no
    animation) and rebuilt via the Node-gated `make generate-ui-assets`
    (reproducible: `theme.css` → `b554d930`; `runtime.js` `ddc6f2ce` / `htmx.js`
    `8689e2e2` / `theme-default.css` `ae49d971` byte-identical — no controller
    change). New 3-engine `components.spec.ts` (7 specs × Chromium/Firefox/WebKit)
    proves ConfirmDialog open/focus-trap/Escape-restore + scrim-does-not-dismiss +
    cancel + end-to-end confirm submit, FormField label/aria wiring, ErrorSummary
    link-to-field focus, TableToolbar no-JS shareable-URL GET, the layout shells'
    regions with zero inline style, and the feedback panels' tones/roles;
    `make test-ui-browser` **429 passed** (up from 408; the axe + strict-CSP crawls
    auto-cover the 14 new specimens with zero violations across all three engines).
    `make warm-scaffold-cache` + `make build`/`make vet`/`make test` + `make guard`
    (18 guards, incl. `guard-ui-no-inward` and the `go.mod` whitelist — no new
    dependency) + `make check` all green.

#### GOTH-7.2 — migrate authentication's view adapter to GOTH

- **depends_on:** GOTH-0.4, GOTH-7.1
- **model:** opus
- **files:**
  - `features/authentication/views/templ/**/*` or replacement `views/goth/**/*`
  - authentication view/security tests
  - `examples/auth-cms/**/*`
  - `go.work`, `Makefile`, `RELEASING.md`
- **work:** At preflight's no-tag posture, rename the sibling module to
  `features/authentication/views/goth` and package/import alias it clearly;
  if a tag exists, add `views/goth` and retain a compatibility path instead.
  Render all auth pages through `ui/goth`, replace bespoke fragment scripts
  with the shared controller, wire assets and policy explicitly in auth-cms,
  and preserve every security/secret/JSON contract.
- **verify:** authentication module + view module + auth-cms tests; CSP browser
  journey for login/register/verify/reset/passwordless/step-up/account pages;
  curl proves unchanged JSON API; asset-free nil-Views posture remains.
- **Gate C carry-forward (2026-07-17, see `gate-c-review.md` C3/C5):**
  - **Unbudgeted contract test.** A policy intended to keep the bundled inline
    fragment readers running MUST carry `HTMLScriptSrc` with `Nonce: true` — a
    non-nil policy replaces the default `script-src 'nonce-…'` tail entirely, so a
    policy omitting it fails closed and the readers never run. Add a contract test
    over the adapter's produced policy asserting this.
  - **Deterministic source ordering.** The adapter maps
    `goth.Bundle.Requirements()` into the policy and OWNS emitting each directive's
    sources deterministically; source order within a directive is load-bearing for
    cross-process header stability (feature-side dedup keeps the first occurrence).
  - **Externalize the fragment readers.** The bespoke inline magic-link/reset
    fragment scripts move to the shared external named controller as part of this
    migration.
- **DONE (2026-07-18).** Preflight re-confirmed `git tag --list` empty, so Gate A's
  tag-sensitive rule applied: the sibling module was **renamed in place**
  `features/authentication/views/{templ → goth}` via `git mv` (package `templ` →
  `goth`, module path updated) with no compatibility shim. `go.work`, the `Makefile`
  `MODULES` list + the `generate` target, and `examples/auth-cms/go.mod`
  (require/replace) were repointed; `ui/goth` was added to the views module and to
  auth-cms as a require + `../..`-relative replace.
  - **Rendering.** All sixteen `Views` port methods re-implemented on `ui/goth`. The
    shared chrome composes `layouts.AuthShell` and emits the fingerprinted GOTH
    stylesheet through `Bundle.Head()`; forms compose `forms.FormField`/`ErrorSummary`/
    `FormActions` over `primitives.Input`/`Button`/`NativeSelect`. Every load-bearing
    contract is preserved verbatim — form `action=`, field `name=`/`autocomplete=`/
    `inputmode=`, the `csrf_token`/`return_to` hidden fields, `aria-describedby` error
    wiring, masked-value display, and no secret repopulation (a password `Input` never
    echoes its value). The feature core gains **no** templ/`ui/goth` dependency (guard
    G5 stays green — GOTH lives only in the sibling views module). `New(bundle
    *goth.Bundle) (Views, error)` replaces the old zero-arg `New()`.
  - **HTMLPolicy mapping (C3/C5).** `Views.HTMLPolicy()` maps
    `goth.Bundle.Requirements()` into `authentication.HTMLResourcePolicy` through an
    explicit `goth.Directive → HTMLResourceKind` table, appends `script-src 'self'` for
    the externalized reader, and sets `Nonce: true` so the per-render nonce channel
    stays available. The adapter OWNS deterministic source ordering (built from the
    already-stable `Requirements` accessors; the private `resourceDirectives()` seam is
    unit-tested for determinism). The **C5 contract test** asserts the produced policy
    carries `HTMLScriptSrc` with `Nonce: true` **and** `'self'` across StylesOnly /
    Interactive / Full — a policy omitting either fails closed under the feature's
    default-`script-src`-replacement rule.
  - **Externalized fragment readers.** The bespoke inline nonced reset/magic-link
    scripts were replaced by one served `assets/fragment.js` (embedded;
    `FragmentScriptHandler()` + `DefaultFragmentScriptPath`, overridable via
    `WithFragmentScriptPath`). The reset form carries `data-auth-fragment="hash"` and
    the magic form `data-auth-fragment="token" … -submit="true"`; the deferred
    same-origin `<script src>` scrubs history and populates/submits with **no inline
    script**, so the pages run under `script-src 'self'`.
  - **Host wiring.** `examples/auth-cms` builds the StylesOnly bundle
    (`AssetBasePath:"/assets/goth"`), serves the `ui/goth` fingerprinted assets +
    `FragmentScriptHandler()` on the host router, and `buildAuthConfig` sets
    `Config.Views = authpages.New(bundle)` (the branded Login override now embeds the
    GOTH `Views`) and `Config.HTMLPolicy = authViews.HTMLPolicy()`.
  - **Verification.** `views/goth` `go build/test/vet` green (rewritten render tests +
    `policy_test.go` C5/C3/handler tests). `features/authentication` and
    `examples/auth-cms` build/test/vet green — every pre-existing auth + authorization-v3
    test stays green. **Real-router HTTP proof** (`examples/auth-cms/cmd/server/
    goth_proof_test.go`) drives the composed router: `/auth/register` renders styled
    (`goth-input`/`goth-button`, stylesheet resolves 200 `text/css`) under
    `default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none';
    script-src 'self' 'nonce-…'; style-src 'self'`; the mapped CSP applies even to the
    host's asset-free Login override; the reset landing wires the externalized reader
    (no `replaceState` inline) and `fragment.js` serves as `application/javascript`; the
    JSON API answers JSON unchanged; nil-Views leaves the HTML GET unregistered. A
    **live-server curl journey** (port 8099) reconfirmed register/passwordless/magic/
    login pages + the CSP header + asset/fragment serving. `make check` (templ drift +
    ui/goth asset drift + per-module build/vet/test + 18 guards) green;
    `make test-ui-browser` unaffected at **429** (no `ui/goth` source/asset change).
  - **Follow-up (not silently done).** No 3-engine Playwright/axe spec was added for the
    auth pages: `examples/goth-showcase` is the zero-datastore Playwright host and
    auth-cms has no browser harness. The auth browser proof here is the real-router
    httptest journey + the live curl journey; standing up a Playwright journey against a
    stateful auth host is an owner decision for GOTH-7.5's audit if desired.

#### GOTH-7.3 — migrate CMS's view adapter to GOTH and prove HTMX grammar

- **depends_on:** GOTH-7.1
- **model:** opus
- **files:**
  - `features/cms/views/templ/**/*` or replacement `views/goth/**/*`
  - CMS handler/view tests
  - `examples/cms/**/*`
  - workspace/build/release docs
- **work:** Apply the same tag-sensitive module-path rule, compose public/admin
  pages from the kit, and select a small real workflow—recommended: admin
  entries table filtering/pagination plus form validation—to prove explicit
  HTMX full/fragment/error/history behavior. Do not turn every CMS route into
  HTMX during the proof.
- **verify:** CMS/view/example module tests; non-JS and HTMX browser journeys;
  API-only/minimal host graph remains free of UI technology when views are nil.
- **DONE (2026-07-18).** Preflight re-confirmed `git tag --list` empty, so Gate A's
  tag-sensitive rule applied: the sibling module was **renamed in place**
  `features/cms/views/{templ → goth}` via `git mv` (package `templ` → `goth`, module
  path updated, `ui/goth` added as require + `../../../../ui/goth` replace), no
  compatibility shim. `go.work`, the `Makefile` `MODULES` list + `generate` target +
  header comment, and the three consuming hosts' go.mods (`examples/cms`,
  `examples/auth-cms`, `examples/minimal` — the last two were also on the old path)
  were repointed; `ui/goth` added to `examples/cms` + `examples/minimal`.
  - **Rendering.** All eighteen `Views` methods re-implemented on `ui/goth`. Admin
    pages compose `layouts.AppShell`/`PageHeader` + `forms.FormField`/`FormActions`
    over `primitives.Input`/`Textarea`/`NativeSelect`/`Checkbox`/`Button`/`Badge`/
    `Table`/`DataTable`; the entries list is a `DataTable`; public pages compose
    `layouts.DocumentShell`; every page emits the fingerprinted GOTH stylesheet via
    `Bundle.Head()`. Every load-bearing contract is preserved verbatim — form
    `action=`, field `name=` (title/excerpt/body/author/template/parent_id/
    menu_order/status/`field_<key>`/term_id/kind/label/url/position/file), the
    `enctype="multipart/form-data"` upload, prefix-safe edit/new/delete hrefs, and the
    bidirectional pager. `New(bundle *goth.Bundle) (Views, error)` replaces the old
    zero-arg `New()`. The CMS feature core gains **no** templ/`ui/goth` dependency
    (guard G2 `guard-feature-isolation` stays green — GOTH lives only in the sibling
    views module; the handler reads the `HX-Request` header as a presentation hint
    directly, importing no UI package).
  - **HTMX grammar proof (the named flow: admin entries table filtering/pagination).**
    The entries-list toolbar (a real `role="search"` GET form + a `created_at` sort
    toggle) sits OUTSIDE the swappable `#cms-entries-content` region; the status
    filter, sort toggle, and pagination links carry explicit `hx-*` built from the
    frozen `htmx.Attrs` (`hx-get` + `hx-target="#cms-entries-content"` +
    `hx-swap="outerHTML show:none focus-scroll:false"` + `hx-push-url="true"`, the
    filter form adding `hx-trigger="change"`). The `List` handler parses `?status`
    (the store already supports `EntryQuery.Status`) and, on `HX-Request`, returns the
    new `EntriesListContent` fragment (the content region only) — the full document
    otherwise. So the HTMX swap degrades to exactly the no-JS full reload; history is
    correct via `hx-push-url` + full re-fetch. Form validation stays a full-document
    `<form>` POST (the existing 400/409 re-render). **CSRF posture DECIDED:
    hidden-input-only** — every CMS mutation rides a `<form>`, the HTMX surface is
    GET-only, so no `hx-headers` consumer exists.
  - **§9 `Attrs` finalization (R7 second and final recorded review point).** No struct
    field was added or changed — `htmx/attributes.go` is byte-for-byte the GOTH-5.3
    shape, so `runtime.js`/`htmx.js`/CSS are untouched. The typed `Trigger` and
    `SwapModifiers` are CONFIRMED FROZEN (the CMS filter/swap is a third consumer). The
    residual candidates are RETIRED as no-demonstrated-need (each reopenable): `Vals`/
    `Include` (the filter form serializes its own fields; shareable URLs carry state),
    `Headers`/`hx-headers` (CSRF settled hidden-input-only — mutations ride `<form>`),
    `DisabledElt` (busy rides `data-state`/`.htmx-request`), typed-`URL` alignment
    (caller passes an already-validated string), and further trigger/swap modifiers.
    README §9 and the `htmx/attributes.go` doc comments were updated to the final
    state; a dated addendum was appended to `gate-b-review.md`. **Nothing in §9 is
    provisional after this task.**
  - **Host wiring.** `examples/cms` builds a StylesOnly bundle
    (`AssetBasePath:"/assets/goth"`), serves the `ui/goth` fingerprinted assets, and
    its ACME `theme` now embeds the `ui/goth`-backed default (public chrome overridden;
    admin/forms fall through to GOTH). `examples/minimal` (memstore, driver-free) and
    `examples/auth-cms` (auth's `RequireUser` gates the HTMX admin list) wire the same
    bundle + asset route.
  - **Verification (exact).** `features/cms` + `features/cms/views/goth` +
    `examples/{cms,minimal,auth-cms}` `go build`/`go test`/`go vet` green (every
    pre-existing CMS/theme/auth-cms test stays green). **Real-router HTTP proof**
    (`examples/minimal/cmd/server/goth_htmx_proof_test.go`) drives the composed
    memstore router: `/articles` full document renders the styled DataTable and its
    stylesheet resolves 200 `text/css`; the same URL with `HX-Request:true` returns the
    `#cms-entries-content` fragment ONLY (no `<html>`, toolbar stays mounted); the
    seeded articles appear in both; `?status=draft` narrows to the empty state with the
    draft option selected (shareable URL); the media byte endpoint still mounts. A
    **live-server curl journey** (port 8099, `examples/minimal`) reconfirmed the full
    DataTable + `hx-*` grammar, the HTMX fragment (`grep -c "<html"` = 0), the
    `status=draft` empty state, and the stylesheet serving 200 `text/css`. `make
    warm-scaffold-cache` + `make build`/`make vet`/`make test` + `make guard` (18
    guards, incl. `guard-feature-isolation`, `guard-ui-no-inward`, the `go.mod`
    whitelist) + `make check` (templ drift clean, ui/goth **asset drift clean → assets
    byte-identical**) all green. **`make test-ui-browser` unaffected at 429** — no
    `ui/goth` source/asset change (the asset-drift guard is the recorded proof; only
    doc comments in `htmx/attributes.go` and README §9 changed).
  - **Follow-up (not silently done).** No 3-engine Playwright/axe spec was added for
    the CMS pages: `examples/goth-showcase` is the zero-datastore Playwright host and
    the CMS hosts have no browser harness (same posture GOTH-7.2 recorded). The CMS
    HTMX/no-JS proof here is the real-router httptest journey + the live curl journey;
    standing up a Playwright journey against a stateful CMS host is an owner decision
    for GOTH-7.5's audit if desired. The port grew one method (`EntriesListContent`)
    and `Pager` one field (`Status`) — a hand-written host `Views` (not embedding the
    default) must add the method; embedding hosts are unaffected.

#### GOTH-7.4 — document adoption, theming, and security recipes

- **depends_on:** GOTH-7.2, GOTH-7.3
- **model:** fable
- **files:**
  - `ui/README.md`
  - `ui/goth/README.md`
  - feature/example READMEs
  - `ARCHITECTURE.md`
  - `RELEASING.md`
  - `NOTES.md`
- **work:** Document installation/wiring, asset route, profiles, host themes,
  CSP requirements, component API, HTMX conventions/migration trigger, custom
  feature Views, module tags, and a Segovia/GPS360 handoff. Include complete
  compiling snippets and the GPS-token override example without importing
  Segovia code.
- **verify:** snippet compile tests where practical; link/check catalog count;
  doc review against actual public APIs.
- **DONE (2026-07-18).** Documented the real shipped adopter surface; no Go/`.templ`/
  asset/generated file changed except one new host snippet-proof test (assets
  byte-identical — `make check` drift-clean is the proof). Per file:
  - **`ui/goth/README.md`** — added **§11 Adoption, theming, security, and handoff
    recipes** (Contents list extended) covering: §11.1 install/wiring (the complete
    host recipe, matching `examples/minimal`), §11.2 profiles + the asset route,
    §11.3 the `Requirements`→CSP-directive-string formatter recipe, §11.4 the
    `components/` layer API (the fourteen `layouts`/`forms`/`feedback`/`data`
    compositions), §11.5 the "host forgot to mount the asset route" failure mode +
    boot-time `Manifest.Assets()` reachability self-check, §11.6 the custom feature
    `views/goth` adapter + `HTMLPolicy` recipe (auth's C5 posture), §11.7 the HTMX
    migration trigger + settled hidden-input CSRF, §11.8 the `views/templ`→`views/goth`
    module-tag posture, §11.9 the SRI + CDN-relocation caveats, §11.10 the GPS/Segovia
    brand-token override (illustrative CSS, no Segovia code imported) + the handoff
    steps. The frozen §1–§10 contract was NOT touched.
  - **Gate-C GOTH-7.4 obligations discharged:** the asset-route self-check pattern
    (§11.5) and the SRI CDN-relocation caveat (§11.9) are documented. The
    `Requirements`→CSP-directive-string formatter is documented as the sanctioned
    **host-side recipe** (§11.3, proven by test) and **not** added as a new exported
    kit helper — a first-class helper reopens the GOTH-0.3 frozen surface and re-enters
    Gate B, so it is recorded as a deferred owner decision. **No owner decision was
    resolved unilaterally.**
  - **`RELEASING.md`** — recorded the gate-b convention: any `ui/goth`
    `Requirements`-surface change is an adopter-facing upgrade note even at semver-patch.
  - **`ui/README.md`** — added an "Adopting a UI implementation" pointer to §11 + the
    reference wirings/adapters.
  - **`ARCHITECTURE.md`** — added a one-line `Requirements`→CSP / feature-`HTMLPolicy`
    ownership note under the UI dependency arrows (pointing at §11).
  - **`NOTES.md`** — appended a dated 2026-07-18 GOTH-7.4 decision-log entry.
  - **Doc lags folded in:** `ui/goth/assets/THIRD_PARTY_NOTICES.md` (`views/templ`→
    `views/goth`; rename recorded as applied, not pending). Stale post-7.2/7.3
    `views/templ`/`authtempl`/`cmstempl`/`.templ`-location references corrected in
    `features/authentication/README.md`, `examples/auth-cms/README.md`,
    `examples/cms/README.md`, `features/README.md`, `features/events/README.md`.
  - **Snippet verification (exact).** Every load-bearing Go snippet was verified against
    the real public API. New `examples/minimal/cmd/server/goth_doc_snippets_test.go`
    compiles and RUNS the two Phase-7-learning recipes: `TestDocSnippet_CSPHeaderFormatter`
    (the `Requirements`→CSP recipe yields `style-src 'self'` with no remote origin, no
    nonce, no `unsafe-*`) and `TestDocSnippet_AssetReachabilitySelfCheck` (the boot-time
    self-check passes on the wired router and FAILS when the asset route is unmounted —
    the exact "forgot to mount" mistake, caught at boot). The host-wiring + HTMX +
    `HTMLPolicy`/fragment snippets are proven by the existing GOTH-7.2/7.3 proof tests
    (`examples/auth-cms/cmd/server/goth_proof_test.go`,
    `examples/minimal/cmd/server/goth_htmx_proof_test.go`); the `components/` signatures
    against the GOTH-7.1 package tests.
  - **Verification results.** `make warm-scaffold-cache` + `make build` + `make vet` +
    `make test` + `make guard` (18 guards) + `make check` (templ drift + ui/goth asset
    drift clean → assets byte-identical; per-module build/vet/test + guards) all green.
    `make test-ui-browser` was not required and not affected (no `ui/goth` source/asset
    change — the asset-drift guard is the recorded proof). Unrelated authorization-v3
    worktree state preserved; global `git status` was not used as evidence.

#### GOTH-7.5 — final adversarial, accessibility, and release audit

- **depends_on:** GOTH-7.4
- **model:** opus
- **files:** plan/catalog status, tests, remediation, release inventory
- **work:** Run independent design-system, frontend, architecture, security/CSP,
  and product reviews; remediate accepted findings; record exact verification
  and tag floors. Do not cut tags or create a PR without owner direction.
- **verify:** `make check && make guard`; full three-engine UI browser suite;
  authentication/CMS adversarial tests; clean generation; clean worktree except
  the intended milestone diff.
- **DONE (2026-07-18) — MILESTONE COMPLETE. This note is the Phase 7 gate
  statement and the `ui/goth` milestone completion record.** An independent
  adversarial/accessibility/release audit ran the full gate and found **no
  BLOCKER and no SHOULD-FIX requiring a code change**. No tag and no PR were
  created; the intended milestone diff is preserved and unrelated
  authorization-v3 worktree state was left untouched.

  **1. Adversarial contract sweep — all PASS.**
  - **No server-rendered inline style/`<style>`** anywhere in the shipped
    surface: `rg`-grep across every `*_templ.go` and `.templ` in `ui/goth`,
    `features/authentication/views/goth`, and `features/cms/views/goth` found
    zero `style=` attributes and zero `<style>` elements (only a doc comment).
    Controller JS writes geometry ONLY through CSSOM `setProperty` of
    `--goth-anchor-*`/`--goth-resize-basis` custom properties (invariant 6); the
    only `.style.display`/`setProperty("display")` in the compiled runtime is the
    vendored Alpine `x-show` (client-side CSSOM, CSP-exempt), never
    server-rendered.
  - **Frozen §8 controller set intact — exactly eleven**, all `goth`-prefixed
    and registered once: gothDialog, gothCollapse, gothRovingFocus, gothMenu,
    gothTabs, gothCombobox, gothToast, gothTooltip, gothHoverCard,
    gothMessageScroller, gothResizable. No controller name outside the set; every
    `x-data` in templ/generated resolves to one of the eleven.
  - **Boundaries clean:** `make guard` (18 guards) green — `guard-ui-no-inward`
    (G17: no `ui/` import of features/integrations/examples/workshop),
    `guard-ui-require-whitelist` (G18: `ui/goth` go.mod requires only templ+sdk,
    imports no sdk/feature), and `guard-feature-isolation` (G2) all pass. Both
    feature cores import no templ and no `ui/goth` (verified by grep and by their
    `go.mod` require sets); GOTH lives only in the sibling `views/goth` adapter
    modules.
  - **Public surface bounded:** 64 primitive rows (`P01–P64` in
    `ImplementedPrimitives`), 14 `components/` compositions, 11 controllers — no
    undocumented additions; README ships §1–§11 covering the whole surface and
    catalog.md marks all 64 rows `accepted` (the lone `planned` token is the
    glossary legend). Every primitive/component is specimen-gated
    (`TestEveryImplementedPrimitiveHasSpecimen` /
    `TestEveryImplementedComponentHasSpecimen`, green under `make check`).
  - **CSP posture proven in every serving host at runtime** (kit writes no
    header — invariant 3 — the host maps `Requirements`): showcase →
    `default-src 'none'; … style-src 'self'` (no `unsafe-*`, no nonce; StylesOnly
    loads no script); auth-cms `/auth/register` →
    `script-src 'self' 'nonce-…'; style-src 'self'` — **the per-render nonce is
    in `script-src` only** (auth's own fragment-reader/inline-script channel,
    Gate C), style-src stays `'self'`; the mapped `HTMLPolicy` is exactly C5
    (`script-src 'self'` + `Nonce:true`, deterministic source order per C3),
    contract-tested across all three profiles.

  **2. Accessibility — all PASS.** `make test-ui-browser` **429 passed** (3
  engines: Chromium/Firefox/WebKit; 0 failed, 0 flaky). The whole-catalog axe
  crawl (`axe.spec.ts` "every specimen has no accessibility violations") and the
  strict-CSP crawl ("no violations or console errors on any specimen") are green
  on all three engines. The highest-risk claims each have a proving spec with
  substantive assertions (not superficial): focus trap/restore →
  `overlays.spec.ts` + `mechanics.spec.ts` (`toBeFocused()` on the trapped
  control, Escape restores to trigger, nested unwind one level per press); live
  regions → `message-scroller.spec.ts` (role=log, aria-label, unread/edge state),
  `toast.spec.ts`/`sonner.spec.ts` (polite/assertive announce-once); roving focus
  → `mechanics`/`menu`/`compact`/`disclosure` specs; grid semantics →
  `date.spec.ts` (APG calendar grid, single tab stop, arrow/Home). **No claim
  lacks a proving spec.**

  **3. Release — all PASS.**
  - **API-only host stays view-technology-free:** `examples/jobs-minimal` is the
    living API-only exemplar — its go.mod requires only `features/jobs`,
    `integrations/scheduling/robfig-cron`, and `sdk`; zero templ/`ui/goth`/htmx in
    any Go file. Invariant 8 also holds at the code path (auth's nil-`Views`
    proof leaves the HTML GET unregistered). (`examples/minimal` is intentionally
    a GOTH view host post-GOTH-7.3, not the API-only exemplar — see finding N2.)
  - **Taxonomy require sets match (G18):** the `ui/goth` require whitelist guard
    is green; no `ui/*` module requires beyond templ+sdk.
  - **RELEASING.md floors/notes accurate:** `ui/goth` (first tag, v0.1.0
    example), `features/authentication/views/goth` and `features/cms/views/goth`
    (both NEW modules / first tag, renamed in place from `views/templ` under Gate
    A's tag-sensitive rule), the auth `Config.HTMLPolicy` surface (C4), and the
    `Requirements`-surface upgrade-note convention are all recorded. **Recommended
    first-tag floors (owner cuts them — no tag created here):** `ui/goth@v0.1.0`,
    `features/authentication/views/goth@v0.1.0`,
    `features/cms/views/goth@v0.1.0`, each independently tagged and depending on
    the pinned `ui/goth` tag.
  - **Review-finding sweep — every accepted finding remediated, no orphans.**
    Gate B R1 (dist/ re-root + `WithAssetPrefix("dist/")`), R2 (sdk-clause
    reword), R3 (SUPERSEDED by Amendment 1 — no style-nonce channel; `Config.Nonce`
    /`RequiresNonce`/`nonceStyle`/`Bundle.Theme` all removed), R4 (Node pinned via
    `.nvmrc` 24.0.1 + Node-gated drift), R5 (overlay scrim token present;
    Go theme-value machinery removed per Amendment 1; token names frozen), R6
    (MergeAttributes drops the `class` key — `Base.Class` the only channel; Button
    `ErrButtonURLConflict` guard; family/ID/name freezes), R7 (§9 `Attrs` FROZEN
    at GOTH-5.3, FINALIZED at GOTH-7.3 — nothing provisional), R8 (README §10
    wiring specimen), R9 (staleness) — all applied. Gate B addenda (gothTooltip/
    gothHoverCard, gothMessageScroller, gothResizable added to §8) all present in
    the frozen eleven. Gate C C1/C2 (auth README: non-nil replaces script-src,
    fail-closed; widening unbounded/value-neutral), C3 (policy.go source-order doc),
    C4 (RELEASING auth note), C5 (contract test asserting Nonce+`'self'` across
    StylesOnly/Interactive/Full) — all applied. Amendment 1 (drop Tailwind / host
    stylesheet becomes the theming channel via `Config.ThemeStylesheetPath`) —
    applied. Wave-audit deferrals (GOTH-2.4…6.6) — all recorded as
    enhancement-not-parity, none reopened. **Supply chain:** axe-core (MPL-2.0) is
    absent from `assets/dist/`, `THIRD_PARTY_NOTICES.md` records htmx 2.0.10 +
    Alpine provenance, and the manifest carries exactly the four frozen logical
    names (theme.css, theme-default.css, runtime.js, htmx.js).

  **4. Layer-4 run-and-look — real behavior confirmed.** Three hosts served
  (showcase :8099, memstore CMS `examples/minimal` :8081, memstore `auth-cms`
  :8082) and screenshots captured and VIEWED: (a) the **auth `/auth/register`**
  page renders fully styled via GOTH under the mapped strict CSP — AuthShell Card,
  FormField groups with required markers, bordered/rounded Inputs, dark primary
  "Create account" button; (b) the **CMS admin `/articles`** renders the GOTH
  DataTable with a styled PageHeader, "New" button, NativeSelect status filter,
  "published" Badge pills, Edit links, and the sidebar nav — and the HTMX contract
  round-trips live (full document has one `<html>`; the same URL with
  `HX-Request:true` returns the `#cms-entries-content` fragment with zero `<html>`;
  the fingerprinted stylesheet serves 200 `text/css`); (c) the **showcase index**
  serves the full specimen directory (bare navigation page by design; each link
  reaches a styled specimen — the axe/CSP crawls prove the specimens).

  **Findings (severity + disposition).** No BLOCKER. No SHOULD-FIX.
  - **N1 (NIT, no fix — host choice).** `examples/minimal` sets no
    `Content-Security-Policy` header on its pages. This is not a kit violation:
    the kit writes no headers (invariant 3) and emits no inline style/script, so a
    host *can* apply a strict CSP — showcase and auth-cms both do and prove the
    mapping. `minimal` is a driver-free demo; adding CSP middleware is out of
    scope. Recorded for an adopter's benefit, not a defect.
  - **N2 (NIT, doc nuance — no fix).** GOTH-7.3's verify line spoke of "API-only/
    minimal host graph remains free of UI technology when views are nil," but
    `examples/minimal` is now a GOTH view host (imports `ui/goth` in production
    `main.go`). The genuine API-only exemplar is `examples/jobs-minimal`
    (verified view-technology-free above); invariant 8's nil-`Views` code path is
    separately proven by the auth proof test. No architecture breach.
  - **N3 (recorded owner decision, carried forward — satisfied for this gate).**
    GOTH-7.2/7.3 flagged that no permanent 3-engine Playwright/axe spec exists for
    the *stateful* auth/CMS hosts (the showcase is the zero-datastore Playwright
    host). This audit discharged the Layer-4 obligation by serving both stateful
    hosts and VIEWING real-browser screenshots plus the live HTMX round-trip;
    standing up a permanent Playwright journey against a stateful host remains an
    **owner decision (YOUR CALL)**, not a milestone blocker.

  **Verification matrix (exact).** `make check` (templ drift + `ui/goth` asset
  drift clean → assets byte-identical; per-module vet/build/test across all
  modules; integration-tag vet; 18 guards) → **PASS (exit 0)**. `make guard` (18
  guards) → **PASS** (run inside `check`). `make test-ui-browser` → **429 passed,
  3 engines, exit 0**. Authentication + CMS + auth-cms + minimal adversarial/
  proof suites → **green** (inside `make check`). `make generate` +
  `make generate-ui-assets` → **no drift** (the asset-drift git-diff gate in
  `check`). No source or asset file was modified by this task; only the milestone
  records (this note, TASKS.md board/gate/summary) change. **Recommended owner
  next steps:** commit the milestone diff as the `ui/goth` completion point, then
  cut the three first-tag floors above at the owner's discretion; N3's stateful-
  host Playwright journey is an optional follow-up.

## Sequencing

```text
preflight
   |
phase 0: taxonomy/API/auth policy
   |
phase 1: module/assets/runtime/showcase
   |
phase 2: presentational/native primitives P01-P26
   |
phase 3: disclosure/selection P27-P36
   |
phase 4: overlays/navigation P37-P48
   |
phase 5: composite data/time P49-P54
   |
phase 6: specialized/media/messaging P55-P64
   |
phase 7: compositions -> authentication -> CMS -> closeout
```

Within a phase, tasks marked only on the phase gate may proceed independently,
but the wave closes as one reviewed unit. A shared behavior defect reopens the
owning foundation/mechanics task and every dependent primitive; it is not
patched independently 12 times.

## Definition of done

- `ui/goth` is a documented, independently taggable module in the workspace
  and repository taxonomy.
- All 64 dated catalog rows are complete under the parity definition, not merely
  exported.
- Theme overrides, dark mode, RTL, reduced motion, and strict CSP work through
  documented contracts.
- Styles-only, Interactive, and Full bundle profiles load exactly their stated
  self-hosted fingerprinted assets.
- HTMX remains exact 2.0.10 and optional; full-document and fragment behavior
  is equivalent for the proof workflow and follows the 4-forward conventions.
- The showcase and full browser/accessibility matrix are green.
- Authentication can explicitly load GOTH assets without losing fixed security
  headers, secret discipline, or API-only behavior.
- Authentication and CMS demonstrate GOTH view adapters; feature cores remain
  SDK-only and technology-neutral.
- Generated artifacts have clean drift checks; provenance/licenses/checksums
  are recorded.
- Root checks/guards, feature suites, and adopter journeys are green, and docs
  match the shipped APIs.

## Risks

1. **Catalog breadth hides shallow ports.** Sixty-four exported names can mask
   missing keyboard, focus, error, mobile, or server-rendered behavior. The
   per-entry matrix and wave closeout gates reject facade-only completion.
2. **React mechanics leak into GOTH.** Copying component shape without rethinking
   native HTML, form submission, server state, and progressive enhancement can
   produce a brittle mini-SPA. The parity definition is behavioral, not textual.
3. **One giant JavaScript runtime.** Sharing mechanics can become a monolith or
   make CSS-only/native components pay for unused behavior. Separate profile
   assets and controller registration tests keep the runtime honest.
4. **CSP is weakened globally for convenience.** Overlay positioning and
   resizable/chart behavior are the highest risk. Use a nonced dynamic
   stylesheet/declared requirements and test browser console policy errors;
   never add global `unsafe-eval` or an undocumented `unsafe-inline`.
5. **Feature boundary erosion.** Authentication/CMS adoption may tempt direct
   `ui/goth` imports into feature cores or HTMX-specific domain APIs. Guards and
   sibling view adapters preserve FS3.
6. **Generated-asset supply chain.** npm/bundler inputs add a new toolchain.
   Exact pins, lockfiles, release-age checks, install-script audit, provenance,
   hashes, and committed outputs are completion requirements.
7. **Theme contract freezes accidental Segovia details.** GPS values are a proof
   theme; semantic role names are the cross-implementation contract. Brand-only
   additions require evidence they generalize.
8. **Browser suite becomes optional in practice.** The ordinary Go loop cannot
   prove focus/typeahead/scroll behavior. The browser target is a release gate
   even if kept separate from hermetic `make check` mechanics.
9. **Module rename collides with tags.** Both feature view modules are currently
   untagged. Preflight rechecks; a discovered tag switches to additive
   `views/goth` modules and compatibility rather than deleting published paths.
10. **HTMX beta enthusiasm bypasses upgrade posture.** The plan pins 2.0.10 and
    records an explicit stable/latest/release-age trigger for 4; no beta-only
    primitive contract is accepted.

## Deferred follow-ups

- `ui/react` and `ui/vue` implementations consuming the shared token/parity
  contract.
- a Workshop command or registry manifest for `gopernicus ui add`, after the
  importable file/API grammar stabilizes;
- automated upstream Shadcn catalog-diff reporting;
- additional branded theme packages;
- visual-regression enforcement if the pinned environment proves stable; and
- HTMX 4 adoption after stable, normal package-manager latest, minimum release
  age, migration audit, and proof-host success.

## Ratification checklist

- [x] top-level `ui/` family and `ui/goth` name (owner, 2026-07-17)
- [x] full dated Shadcn catalog as the primitive baseline (owner, 2026-07-17)
- [x] authentication may explicitly load view assets; asset-free is not a
  permanent restriction (owner, 2026-07-17)
- [x] HTMX 2.0.10 with HTMX-4-forward conventions (owner, 2026-07-17)
- [ ] seventh module-kind wording and dependency arrows
- [ ] exact primitive public API/attribute/ID grammar from GOTH-0.3
- [ ] repository-local Node build/test toolchain with Node-free consumers
- [ ] authentication resource-policy public type and construction matrix
- [ ] tag-sensitive rename of feature `views/templ` modules to `views/goth`
- [ ] Playwright three-engine + axe browser gate

## Recommended reviews

- `design-system-reviewer` — parity, tokens, variants, composition boundary;
- `lead-frontend-engineer` — templ/Alpine/HTMX/browser mechanics;
- `architecture-steward` — seventh module kind, import arrows, feature seams;
- `platform-sre` — asset caching, CSP, build/CI/release operations;
- `product-manager` — catalog scope and adopter-facing usability; and
- authentication/security owner — fixed headers, policy validation, and secret
  discipline before GOTH-7.2.
