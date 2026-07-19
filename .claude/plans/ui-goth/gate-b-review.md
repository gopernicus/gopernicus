# Gate B review record — 2026-07-17

Five-reviewer wave over the GOTH-0.3 freeze artifacts (`ui/goth/README.md`,
`ui/goth/catalog.md`, `ui/README.md`): design-system-reviewer,
lead-frontend-engineer, architecture-steward, platform-sre, product-manager.

**Verdicts: 5/5 accept-with-changes. No blockers; no re-plan requested.**

**Owner disposition (2026-07-17, interactive session): Gate B accepted,
conditional on the remediation pass below being applied to the freeze
artifacts and verified green. HTMX `Attrs` field set: owner chose
PROVISIONAL — principles, package existence, and the `Request`
presentation-hints-only reader freeze now; the exact `Attrs` field set and
CSRF posture are finalized when the first real consumers land (GOTH-5.3 Data
Table, GOTH-7.3 CMS), and that finalization is a recorded review point, not a
silent edit.**

## Binding remediation items (apply before recording Gate B as closed)

R1. **Asset-serving recipe** (architecture): §3's recipe cannot deliver
immutable caching — `sdk/foundation/web/static.go` applies
`Cache-Control: immutable` only when a non-empty `WithAssetPrefix` matches the
**FS path**, but the frozen `assets.FS` roots files at FS root. Re-root the
embed so served paths are `dist/<hashed>`, document
`WithAssetPrefix("dist/")`, and fix the `Asset.Path` example plus the
host-join rule to match.

R2. **sdk clause** (architecture): §1's "(where a helper needs it) `sdk`" is
an open-ended pre-authorization. Replace with: the frozen surface imports no
sdk package; the taxonomy permits sdk, and any sdk-importing addition is new
public surface that reopens GOTH-0.3.

R3. **CSP nonce coherence** (platform, frontend): resolve the §2 vs §4
contradiction — decide script tags carry **no** nonce (nonce is
dynamic-stylesheet-only) OR `Requirements` reflects a script nonce whenever
`Head()` emits one; make §2 and §4 agree. Add two frozen invariants:
(a) primitives never emit an inline `style=` attribute — dynamic geometry goes
through the nonced stylesheet or data-attributes + external CSS
(caller-injected `style` via `Base.Attributes` is the caller's CSP problem,
say so); (b) one nonce provider per render feeds both the host/policy CSP
header and `Config.Nonce` — mismatched channels break `style-src`.

R4. **Node toolchain pinning** (platform): freeze that the Node runtime
version is pinned (`.nvmrc`/`engines`) alongside `package-lock.json`, and
state where the CSS/JS/manifest drift check lives: a Node-gated target
(mirroring `make test-ui-browser`) vs always-on `make check`. Default to
Node-gated with the committed `dist/` diffed by a plain-git check unless the
owner overrides at GOTH-1.2.

R5. **Theme contract** (product, design, frontend):
- Freeze partial-theme composition: state explicitly whether
  `NewTheme` overrides compose **over `DefaultTheme`** or over neutral-only.
  (Recommended: over `DefaultTheme` — the common adopter action is "set
  `primary`, keep the rest".)
- Add an `overlay` (scrim color/opacity) token to the frozen token set for
  P37/P39/P40/P47/P51 backdrops.
- Resolve the `goth.Theme` vs `theme.Theme` package identity: pick the
  qualified type `Config.Theme` actually holds (re-export, alias, or single
  package) and state which construction path (zero value vs `NewTheme`) is
  the only unvalidated one.
- Confirm the shadow ceiling (`shadow-lg` intended for overlays, or add
  `shadow-xl`); note how `density` derives component padding/gap so GOTH-1.3
  implements rather than decorates it. (NIT-level; include.)

R6. **Family-grammar consistency** (design, frontend):
- Add the explicit F1-vs-F2 and F2-vs-F3 decision rule to §7 and the catalog
  preamble; re-verify Alert (P01), Card (P08), Empty (P10), Item (P14),
  Badge (P04), Marker (P17) against it and adjust families/counts if the rule
  demands.
- Toggle (P35): freeze checkbox-backed in-form (native submits, controller
  enhances). Keep F4 with that recorded rationale, or reclassify F1 if the
  new rule demands — either way the Switch/Toggle asymmetry gets an explicit
  stated reason.
- Button (P06): keep F2 (both reviewers endorse). Pin the Button/ButtonLink
  API shape (one function with optional URL vs two functions) and require a
  guard on the invalid `Href` + `type=submit`/`disabled` cross-field combo.
- Freeze F2's principal content channel: principal slot is templ children
  (`{ children... }`); auxiliary slots stay `templ.Component` fields.
- Freeze the `MergeAttributes` emission rule: owned + caller attributes
  funnel through ONE merged spread on the element (never sibling static
  attrs, which duplicate in templ 0.3.1020); `class` inside
  `Base.Attributes` is rejected/ignored — `Base.Class` is the only class
  channel.
- Freeze `IDFactory` threading for compound-part ARIA linkage: pick
  context-carried (freeze the context key) or caller-passed via `Base.ID`
  per part, and align §7's text.
- Require an accessible name for icon-only-capable interactive primitives
  (dedicated field or render-time contract test), not just the
  `Base.Attributes` escape hatch.
- Name the Alpine controller-binding attribute (`x-data="goth…"`) in the
  frozen emitted-surface list alongside `data-slot`/`data-state`.

R7. **HTMX provisional reframe** (product, frontend; owner-decided): §9 keeps
the principles (server owns markup, presentation-hints-only `Request`, no
security decisions from hx headers), package existence, `Method`/`Swap`
enums, and the `Build`/`Base.Attributes` merge rule (specify who merges);
the exact `Attrs` field set is marked **provisional until GOTH-5.3/7.3**,
with the known candidate gaps recorded (`Vals`, `Include`, `Headers`,
`DisabledElt`, swap modifiers, typed-URL alignment) plus the CSRF posture
question (hidden-input-only vs `hx-headers` seam) as the finalization
checklist.

R8. **Adopter wiring specimen** (product): add a ~15-line assembled host
wiring block to `ui/goth/README.md` §10 — serve `assets.FS` via
`StaticFileServer`+`WithAssetPrefix`, construct `goth.New`, emit `Head()`,
map `Requirements` into a CSP header value — written against the frozen
names.

R9. **Staleness fixes** (architecture): update `ui/README.md`'s
"proposed seventh kind / to be enforced in GOTH-0.2 / settled in GOTH-0.2"
passages to point at the ratified ARCHITECTURE.md row and guard G17; correct
plan.md's "exact exported names are frozen in GOTH-0.3" claim for the
authentication policy type to GOTH-0.4, and record that the auth policy
surface is out of Gate B's scope (it gets its own Gate C review).

## Addendum — 2026-07-17 (GOTH-4.3): §8 controller-set growth

The frozen §8 controller list named no controller capable of backing Tooltip
(P48) / Hover Card (P42) — Escape-hide and hover-intent are not expressible
in CSS and every existing controller is semantically wrong (Dialog traps,
Menu moves focus into the panel). Owner decision (interactive session):

- **`gothTooltip` and `gothHoverCard` are ADDED to the frozen §8 set** —
  thin controllers composing the frozen GOTH-4.1 mechanics (anchor, dismiss/
  overlay-stack), no mechanics forks, all §8 rules (goth-prefix, CSP build,
  data-slot discovery, documented goth:* events) apply.
- **P45 Popover rides the native `popover`/`popovertarget` attributes**;
  **P46 Select rides a native `<select>` baseline** — no `gothPopover`/
  `gothSelect`; minimum new frozen surface.

The §8 list is now: gothDialog, gothCollapse, gothRovingFocus, gothMenu,
gothTabs, gothCombobox, gothToast, gothTooltip, gothHoverCard.

## Addendum — 2026-07-18 (GOTH-6.3): §8 controller-set growth for P61

Resizable (P61)'s split-pane drag (pointer capture + keyboard resize +
bounds) is not expressible in CSS and has no honest no-JS live-resize
baseline; no frozen controller provides pointer-drag geometry. Owner decision
(interactive session): **`gothResizable` is ADDED to the frozen §8 set** — a
thin controller composing the frozen GOTH-4.1 mechanics, reading separator
drag/keyboard input and writing pane geometry through CSSOM custom
properties (`--goth-resize-*`, the anchor-mechanic pattern, no inline
`style=`) over a server-rendered `role=separator`/`role=group` baseline
whose default split is server-owned. All §8 rules apply. The §8 list is now
eleven: gothDialog, gothCollapse, gothRovingFocus, gothMenu, gothTabs,
gothCombobox, gothToast, gothTooltip, gothHoverCard, gothMessageScroller,
gothResizable. (Implementation lands when GOTH-6.3 resumes on the
post-Amendment-1 stack.)

## Addendum — 2026-07-18 (GOTH-6.2): §8 controller-set growth for P60

Message Scroller (P60)'s frozen purpose — live-edge following,
history-prepend-without-jump, jump-to-message with reader-intent
preservation, unread/scroll state — requires scroll-position management no
frozen controller provides, and the F4-native precedent cannot apply (the
no-JS transcript covers read/jump but not live-follow). Owner decision
(interactive session): **`gothMessageScroller` is ADDED to the frozen §8
set** — a thin controller composing the frozen GOTH-4.1 mechanics +
`mechanics/live-region.js` over a server-rendered `role=log` transcript
(fragment-jump anchors + HTMX prepend/append) as the no-JS baseline. All §8
rules apply. The §8 list is now ten: gothDialog, gothCollapse,
gothRovingFocus, gothMenu, gothTabs, gothCombobox, gothToast, gothTooltip,
gothHoverCard, gothMessageScroller.

## Addendum — 2026-07-17 (GOTH-5.3): R7 HTMX `Attrs` finalization (first recorded review point)

R7 marked the §9 `Attrs` field set PROVISIONAL, to be finalized at the first real
consumers (GOTH-5.3 Data Table, GOTH-7.3 CMS), as a recorded review point — not a
silent edit. GOTH-5.3 is that first point. With two concrete consumers now in hand
— the Data Table (P52) and the Combobox (P50, whose GOTH-5.2 evidence recorded the
exact gaps) — the field set is **FROZEN** as follows.

**Finalized (frozen now — real consumers demonstrated the need):**

- **`Trigger` → typed `htmx.Trigger`** (`Event`/`Changed`/`Delay`/`Throttle`),
  replacing the free string. It emits a validated `hx-trigger` (`input changed
  delay:150ms`). Consumers: the Combobox async input (`input changed delay:150ms`,
  the exact string GOTH-5.2 flagged) and the Data Table live filter (`input changed
  delay:300ms`). The 5.2 combobox async specimen was migrated off raw `hx-*` onto
  this typed field to prove the gap is closed. `Build` errors on modifiers without
  an `Event`.
- **`SwapMods htmx.SwapModifiers`** (`Show`/`Scroll`/`FocusScroll`/`Settle`),
  appending validated `hx-swap` modifiers. Consumer: the Data Table swaps its
  content region on every sort/filter/page and uses `Show:"none"` +
  `focus-scroll:false` so the viewport neither jumps nor steals focus (the row's
  scroll/focus-preservation requirement).

**Still PROVISIONAL (kept for GOTH-7.3 — no Phase-5 consumer; do NOT drop these
markers):**

- `Vals`, `Include`, `Headers`, `DisabledElt`; additional trigger modifiers
  (`from:`/`target:`/`once`/`queue:`/comma-separated multi-triggers); additional
  swap-delay modifier; and aligning `Attrs.URL` with the typed `primitives.URL`.
  Rationale: the Data Table is GET-only with server-rendered shareable URLs that
  already carry the full sort/filter/page/selection state, so cross-field state
  preservation needs no `hx-include`/`Vals`; and typed-URL alignment would couple
  `htmx`→`primitives` (or need a shared url package), while the Data Table already
  passes an *already-validated* `primitives.URL.String()` into `Attrs.URL` — so the
  coupling buys no safety today and the decision is deferred.
- **CSRF posture** (hidden-input-only vs an `hx-headers` seam) stays 7.3's: the
  Data Table demonstrates only GET requests, so no mutation/CSRF consumer exists at
  Phase 5. The reader derives no CSRF/authorization from any hx header either way.

README §9 now marks each of the above FROZEN-vs-provisional; `htmx/attributes.go`
implements it with tests (`TestAttrsTriggerModifiers`, `TestAttrsSwapModifiers`).
The `Method`/`Swap`/`Request`/merge-rule freezes from the original Gate B record are
unchanged. No owner reopen was required (R7 pre-authorized this finalization).

## Addendum — 2026-07-18 (GOTH-7.3): R7 HTMX `Attrs` finalization (second and final recorded review point)

R7 named GOTH-7.3 (the CMS mutating adopter) as the second and final recorded review
point for the residual §9 candidates the GOTH-5.3 addendum kept provisional. That
adopter has now landed: the CMS admin entries-list is HTMX-enhanced — the status
filter form, the created_at sort toggle, and the pagination links carry explicit
`hx-*` (built from the frozen `htmx.Attrs`) that swap the `#cms-entries-content`
region (`outerHTML` + `Show:"none"` + `focus-scroll:false` + `hx-push-url`), and the
`List` handler returns that fragment for an `HX-Request` while the full document
backs the no-JS reload. Driven over the real composed router (examples/minimal
`goth_htmx_proof_test.go`) and a live curl journey.

**Confirmed FROZEN (a third real consumer):** the typed `Trigger` (the filter form's
`Event:"change"`) and `SwapModifiers` (`Show:"none"` + `FocusScroll:false`). No field
was added or changed — `htmx/attributes.go` struct surface is byte-for-byte the
GOTH-5.3 shape, so `runtime.js`/`htmx.js`/CSS are untouched and `make test-ui-browser`
stays at 429.

**RETIRED as no-demonstrated-need (each reopenable by the standard rule):**

- `Vals` / `Include` — the CMS filter form serializes its own fields (a hidden
  `order` input beside the status `<select>`); full state rides shareable URLs. No
  cross-element include/vals consumer.
- **`Headers` (`hx-headers`) — the CSRF seam.** DECIDED: **hidden-input-only.** Every
  CMS mutation (create/update/delete, term/menu/media, contact) rides a `<form>`
  full-document POST; the HTMX-enhanced surface is GET-only (sort/filter/page). So a
  hidden `csrf_token` input (as authentication already uses) is sufficient and no
  non-form hx mutation exists that would need `hx-headers`. The reader derives no
  CSRF/authorization from any hx header either way.
- `DisabledElt` — the in-flight busy affordance rides the server-owned `data-state`
  and HTMX's `.htmx-request` class, not `hx-disabled-elt`.
- typed-`URL` alignment (`Attrs.URL` → `primitives.URL`) — the caller passes an
  already-validated URL string; coupling `htmx`→`primitives` buys no safety.
- additional trigger modifiers (`from:`/`target:`/`once:`/`queue:`/multi) and the
  `swap:` delay modifier — no consumer through GOTH-7.3.

**Nothing in §9 is provisional after this task.** Every once-provisional item is now
either FROZEN with a named consumer or RETIRED as no-demonstrated-need. README §9 and
`htmx/attributes.go` doc comments are updated to the final state; no owner reopen was
required (R7 pre-authorized this finalization).

## Recorded for later phases (not part of this remediation)

- GOTH-1.1: add a go.mod-level require-whitelist guard for `ui/goth` (G5
  analogue) and optionally a grep forbidding `sdk/feature` inside `ui/`.
- GOTH-1.2: verify `assets/dist/**` never contains test tooling (axe-core is
  MPL-2.0); supply-chain obligations in THIRD_PARTY_NOTICES.md are blocking.
- GOTH-1.5: name the CI job, trigger, browser-binary cache, and bounded
  flake/retry policy for the three-engine Playwright+axe gate.
- GOTH-7.4: document the "host forgot to mount the asset route" failure mode
  + boot-time self-check pattern (`Manifest.Assets()` reachability); the SRI
  CDN-relocation caveat; consider a `Requirements`→CSP-directive-string
  formatter helper (product #4).
- RELEASING.md: adopt the convention that any `Requirements`-surface change
  (new directive/source, `RequiresNonce` flip) is an adopter-facing upgrade
  note even when semver-patch.
- Product observation (no action): P55–P64 have no adopter beyond the
  showcase; if breadth must ever be trimmed, that is the recorded cut line.

## Addendum — 2026-07-18 (Amendment 1): drop Tailwind + host theme stylesheet becomes the theming channel

**Amendment 1 (`amendment-1-drop-tailwind-host-stylesheet.md`) was RATIFIED by
the owner on 2026-07-18** and applied via GOTH-A.1→A.4. Ratifying it is the
recorded owner reopen of the GOTH-0.3 freeze for exactly the items below
("changes to any name/shape reopen GOTH-0.3"). Everything else frozen at Gate B
(primitive grammar, `MergeAttributes`, ID factory, §8 controllers, §9 HTMX,
manifest/`Asset` shape, asset-serving recipe, profiles) is untouched.

**Superseded prior Gate B artifacts:**

- **R3 (CSP nonce coherence) resolution is SUPERSEDED.** The §2-vs-§4
  contradiction it resolved no longer exists: there is no kit style-nonce
  channel. `Config.Nonce`, `Requirements.RequiresNonce()`, `nonceStyle`, and the
  nonced dynamic stylesheet are removed. R3's two frozen invariants (a)/(b)
  (primitives never emit inline `style=`; one nonce provider feeds both the CSP
  header and `Config.Nonce`) are replaced by one stronger frozen invariant: *the
  kit emits no server-rendered `style` attribute and no inline `<style>`
  element — dynamic geometry uses `data-*` attributes + external CSS and
  controller-owned CSSOM custom-property writes only.* (The authentication
  feature's own per-render **script** nonce, Gate C / C1, is unrelated and
  unaffected.)
- **R5's partial-theme-composition freeze is SUPERSEDED/OBSOLETE.** `NewTheme`
  composing overrides over `DefaultTheme` is obsolete — there is no Go theme
  value to compose (`theme.Theme`/`NewTheme`/`DefaultTheme`/`Value`/`Values` and
  the `goth.Theme` alias are removed). The `goth.Theme`-vs-`theme.Theme` package
  identity question is moot. Token **names** stay frozen (they are the CSS
  contract); the override mechanism is "host stylesheet after kit stylesheet"
  (invariant 9's cascade story), injected via `Config.ThemeStylesheetPath`.

**Frozen items this amendment amends (owner-authorized Gate B reopen):**

1. README §2 `Config`: `Theme`/`Nonce` removed; `ThemeStylesheetPath string`
   added; `New` error modes updated.
2. README §4 `Requirements`: `RequiresNonce()` removed; the single
   no-server-rendered-`style`/inline-`<style>` invariant replaces the
   nonce-coherence pair (R3 superseded above).
3. README §5 theme contract: R5 composition freeze obsolete; the Go theme value
   machinery is gone; token names stay frozen; override = host stylesheet after
   kit stylesheet.
4. README §3 manifest: the frozen logical-name set gains `"theme-default.css"`
   (four: `theme.css`, `theme-default.css`, `runtime.js`, `htmx.js`).
5. `Bundle.Theme()` removed from §2's method set.
6. plan.md invariants: invariant 6 loses the "documented nonced runtime
   stylesheet" alternative (data-attributes + external CSS only); invariant 9's
   mechanism updated to the required-injection model; the Layer-2 nonced-dynamic-
   stylesheet inventory bullet becomes a no-server-rendered-style/no-CSP-widening
   assertion; toolchain-policy text drops Tailwind.
7. README §10 wiring specimen rewritten without `Nonce`/`Theme` and with the
   theme-stylesheet link ordering (kit CSS → theme link → scripts).

Tailwind is dropped 100% from the kit toolchain/output; the only sanctioned
Tailwind mention is the README §5 adopter recipe ("using Tailwind in YOUR app"
against the stable `.goth-*` surface). No task was deleted; the amendment added
GOTH-A.1–A.4 (numbered tasks 38→42). Execution evidence for A.1–A.4 lives in the
amendment file; the milestone records (plan.md, TASKS.md) were updated by
GOTH-A.4.
