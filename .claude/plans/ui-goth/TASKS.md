# `ui/goth` implementation task board

Status: **MILESTONE COMPLETE (2026-07-18) — awaiting owner commit/PR/tag
decisions. All 42/42 numbered tasks done (GOTH-0.1–7.5 + Amendment 1's
GOTH-A.1–A.4); all 64 primitive parity rows accepted; every phase gate MET
including the Phase 7 gate (GOTH-7.5 final audit: `make check`/`make guard`/`make
test-ui-browser` 429×3-engine green, full Gate B/C + Amendment 1 review-finding
sweep clean with no orphans, Layer-4 real-browser proof viewed). NO tag and NO PR
created — the commit/PR/tag are the owner's call. Recommended first-tag floors:
`ui/goth@v0.1.0`, `features/authentication/views/goth@v0.1.0`,
`features/cms/views/goth@v0.1.0`. Historical progress record follows.**

Prior status: **IN PROGRESS — Gates A/B/C ratified by owner 2026-07-17; Amendment 1
(drop Tailwind / host theme stylesheet becomes the theming channel) RATIFIED +
APPLIED 2026-07-18 via GOTH-A.1–A.4 (Gate B reopen recorded in gate-b-review.md;
Phase-1 deliverables amended in place per the GOTH-5.5 precedent, no completed
box unchecked); Phase 0 complete;
Phase 1 COMPLETE (GOTH-1.1..1.5 done 2026-07-17): module + frozen contract types +
generation wiring + exact-pinned asset pipeline + light/dark themes + profile-aware Head
+ nonced dynamic stylesheet + CSP profiles + named Alpine CSP controllers/shared
mechanics + explicit HTMX 2 non-2xx response config + zero-datastore showcase host +
three-engine Playwright/axe harness (Chromium/Firefox/WebKit) green. Phase 2 IN
PROGRESS: GOTH-2.1 DONE (2026-07-17) — 12 content/status primitives (P01–P04, P08,
P10, P14–P15, P17, P22–P23, P26); GOTH-2.2 DONE (2026-07-17) — 6 action/navigation
primitives (P05–P07, P09, P19, P21); GOTH-2.3 DONE (2026-07-17) — 8 field/data
primitives (P11–P13, P16, P18, P20, P24–P25). All implemented with component CSS,
render/native-form tests, showcase specimens (incl. a no-JS `<form>` submission
specimen), and 3-engine axe/CSP browser proof (36 passed); `make check`/`make guard`
green. GOTH-2.4 DONE (2026-07-17) — the wave closeout: an adversarial P01–P26 parity
audit passed with no task reopened, catalog P01–P26 flipped to `accepted`, the
parity-matrix rows checked, and Layer-4 run-and-look screenshots captured/viewed at
narrow/wide × light/dark × RTL. **Phase 2 is COMPLETE and its gate is MET.** Phase 3 IN
PROGRESS: GOTH-3.1 DONE (2026-07-17) — 3 disclosure primitives (P27 Accordion, P29
Collapsible, P34 Tabs) on a real no-JS baseline (native `<details>`/`<summary>` +
native single-open exclusivity for Accordion/Collapsible; server-owned active-panel
for Tabs) enhanced by the frozen `gothCollapse`/`gothTabs` controllers (no new
controller surface; the GOTH-1.5 descendant-`$el` audit bug fixed at source in
`gothTabs`, `gothCollapse` moved to the native-details model). Component CSS,
render/contract tests, Interactive-profile showcase specimens, a 3-engine
`disclosure.spec.ts` keyboard/interaction proof (`make test-ui-browser` 45 passed),
and `make check`/`make guard` green. Parity rows P27/P29/P34 stay unchecked until the
GOTH-3.4 wave audit. GOTH-3.2 DONE (2026-07-17) — 4 form-selection primitives (P28
Checkbox, P31 Radio Group, P32 Slider, P33 Switch), all native (no controller), each
submitting correct values in a plain HTML form with no JS; component CSS, render/
native-submission tests, StylesOnly showcase specimens incl. a no-JS `<form>` GET
submission specimen + `/selection/echo` route, and a 3-engine `selection.spec.ts`
keyboard/pointer/submission proof (`make test-ui-browser` 60 passed);
`make check`/`make guard` green. Parity rows P28/P31/P32/P33 stay unchecked until
GOTH-3.4 (the F4 catalog family for P31/P32 vs native-only shipping is flagged for
that audit). GOTH-3.3 DONE (2026-07-17) — 3 compact-selection primitives (P30 Input
OTP, P35 Toggle, P36 Toggle Group) on a real no-JS baseline: grouped native
single-char OTP inputs (native tab/backspace, one-time-code autocomplete, no secret
echo — invalid group and echo route report nothing/count only), checkbox-backed
Toggle (native pressed/submit, no standalone controller), and Toggle Group with a
submittable no-JS representation in both modes (single = native radios no controller;
multiple = native checkboxes enhanced by the frozen `gothRovingFocus` for a single
tab stop). No new controller surface. Component CSS off `:checked`/`:has()`/`data-*`,
render/native-submission tests, StylesOnly/Interactive specimens incl. a no-JS
`/compact/echo` GET form, and a 3-engine `compact.spec.ts` proof (`make
test-ui-browser` **72 passed**); `make check`/`make guard` green. GOTH-3.4 DONE
(2026-07-17) — the wave closeout: an adversarial P27–P36 audit passed with no Phase 3
primitive reopened, the Tabs RTL arrow-direction nuance was FIXED at source (the shared
`mechanics/roving.js` is now direction-aware; ArrowLeft advances under `dir=rtl` for
Radix/APG parity) with a new `primitive-tabs-rtl` specimen + three-engine RTL test, and
the P30/P31/P32/P36 F4-vs-native family labels were reconciled by recording the rationale
without editing the frozen family column. `make test-ui-browser` **75 passed** (3
engines), `make check`/`make guard` green; catalog P27–P36 flipped to `accepted`, the
parity-matrix rows checked, and Layer-4 stateful screenshots captured/viewed
(open/closed × checked/unchecked × active-tab × light/dark × RTL). **Phase 3 is COMPLETE
and its gate is MET.** Phase 4 IN PROGRESS: GOTH-4.1 DONE (2026-07-17) — froze the
shared overlay/menu mechanics the whole overlay/navigation wave (P37–P48) builds on:
the overlay stacking/nesting model + nested-aware Escape/outside dismiss, focus
trap/restore (trap stack), ref-counted class-based scroll lock, native-`inert`
background handling, CSP-safe anchored placement + collision (`data-side`/`data-align`
+ CSSOM `--goth-anchor-*`, no inline `style=`), reused roving/typeahead, and submenu
hierarchy; overlay scrim on the frozen `overlay` token. No new §8 controller name
(mechanics are internal modules; the `gothX` set is unchanged). The three latent
`this.$el`-outside-init defects in `dialog.js`/`menu.js`/`combobox.js` were FIXED at
source. Two Interactive mechanics fixtures + a 3-engine `mechanics.spec.ts`;
`make test-ui-browser` **90 passed**, `make check`/`make guard` green, assets
byte-identical on rebuild. GOTH-4.2 DONE (2026-07-17) — 4 gothDialog-backed modal/
panel primitives (P39 Dialog, P37 Alert Dialog, P40 Drawer, P47 Sheet), each
COMPOSING the frozen GOTH-4.1 overlay mechanics with no fork and no new §8 controller
name. The flagged P39/P37 native-`<dialog>` call was resolved WITHIN the frozen
contract: the div-based overlay is reused (native modal needs `showModal()` JS anyway
→ no honest no-JS gain, and native cannot deliver the frozen nested overlay-stack /
CSP-safe class scroll-lock+inert / shared trap stack without forking them). No-JS
baseline is server-owned `data-state` (server-open panel readable with no JS); Alert
Dialog is the destructive-decision contract (`role=alertdialog`, always modal, scrim
does not dismiss, Escape cancels); Drawer/Sheet are edge panels (`data-side`,
reduced-motion slide-in). `gothDialog` refined (dismiss-outside filter + honest
aria-modal lifecycle) without touching `mechanics/*`. Component CSS (no inline style,
all animations in the reduced-motion collapse), render/contract tests, 8 showcase
specimens (4 Interactive incl. a nested dialog + 4 StylesOnly server-open), and a
3-engine `overlays.spec.ts`; `make test-ui-browser` **114 passed**,
`make generate-ui-assets` byte-identical, `make check`/`make guard` green. Parity rows
P37/P39/P40/P47 stay unchecked until GOTH-4.5. GOTH-4.3 DONE (2026-07-17) — 4 anchored
primitives (P48 Tooltip + P42 Hover Card on the two NEW `gothTooltip`/`gothHoverCard`
controllers added to the frozen §8 set by a recorded owner reopen [`gate-b-review.md`
addendum], composing the frozen GOTH-4.1 anchor/dismiss mechanics with no fork; P45
Popover on native `popover`/`popovertarget` + a delegated CSP-safe anchor enhancement;
P46 Select as a styled native `<select>`, no controller). No-JS baselines proven (CSS
hover/focus-within reveal + describedby; native popover click/Escape/light-dismiss with
Alpine absent; native select GET submission). Component CSS, render/contract tests, seven
specimens, a 3-engine `anchored.spec.ts`; `make test-ui-browser` **144 passed**, assets
byte-identical, `make check`/`make guard` green. Parity rows P42/P45/P46/P48 stay
unchecked until GOTH-4.5. GOTH-4.4 DONE (2026-07-17) — 4 menu primitives (P41
Dropdown Menu, P38 Context Menu, P43 Menubar all backed by the existing frozen
`gothMenu` with NO new §8 controller name — gothMenu's context-menu pointer/keyboard
opening + menubar horizontal coordination REFINED in place, additive, default dropdown
path behavior-unchanged; and P44 Navigation Menu native + link-first (real `<a href>`
links + native `<details>`/`<summary>` disclosure, NO controller — the recorded
F4-native precedent). Item roles per row: menuitem + menuitemcheckbox/menuitemradio
(server-owned aria-checked), menubar/menuitem. No-JS baselines proven (Dropdown
server-open, navigation real links + native details, Alpine absent). Only
`controllers/menu.js` + component CSS changed (no `mechanics/*` fork, no inline style,
reduced-motion honored); render/contract tests, five specimens (3 Interactive + 2
StylesOnly), a 3-engine `menu.spec.ts` (WAI keyboard matrix, submenu, typeahead,
Escape unwind, RTL keys, context right-click + ContextMenu-key, menubar traversal,
no-JS navigation); `make test-ui-browser` **171 passed** (3 engines), assets
byte-identical, `make check`/`make guard` green. Parity rows P38/P41/P43/P44 stay
unchecked until GOTH-4.5. GOTH-4.5 DONE (2026-07-17) — the wave closeout: an
adversarial P37–P48 audit passed with no Phase-4 primitive reopened; the flagged
GOTH-4.1 menu-anchor timing flake was FIXED at source in the tests (`mechanics.spec.ts`
+ `menu.spec.ts` now `expect.poll` the settled CSSOM anchor value — the race was the
read-once test assumption, not `anchor.js`); server-owned checkbox/radio menu items and
per-primitive prefixed part sets both recorded as KEPT (invariant 1 / frozen
Shadcn-prefix grammar), deferred enhancements recorded. `make test-ui-browser` **171
passed** (3 engines, no flaky; deflaked anchor specs re-ran 3× clean), `make
check`/`make guard` green, assets byte-identical; Layer-4 screenshots (22 overlay/menu
specimens) captured/viewed. Catalog P37–P48 flipped to `accepted`, parity-matrix rows
checked. **Phase 4 is COMPLETE and its gate is MET.** Phase 5 IN PROGRESS: GOTH-5.1
DONE (2026-07-17) — P49 Calendar + P53 Date Picker. Server owns all date math/locale
rendering (Go month grid, weekday order, prev/next values, day states; no clock, no
time-zone policy in the primitive); no-JS baseline is native form-submit day/nav
buttons with APG grid semantics; the keyboard grid enhancement refines the frozen
`gothRovingFocus` IN PLACE with a grid mode (new generic `createGridRoving` in
`mechanics/roving.js`) — NO new §8 controller name; Date Picker composes Field +
native Popover + Calendar with a server-owned parse/format/error contract and a
proven no-JS (full-reload) ↔ HTMX (fragment-swap) selection parity. Component CSS,
deterministic render tests, showcase specimens (+ `/calendar/select`,
`/datepicker/pick` routes), a 3-engine `date.spec.ts`; `make test-ui-browser`
**186 passed**, `make generate-ui-assets` byte-identical, `make check`/`make guard`
green. Parity rows P49/P53 stay unchecked (catalog `planned`) until GOTH-5.5. GOTH-5.2
DONE (2026-07-17) — P50 Combobox + P51 Command, both backed by the frozen
`gothCombobox` refined IN PLACE (NO new §8 controller name): an `aria-activedescendant`
active-option keyboard loop, an inline always-open mode for Command, a client/server
filter split (server owns the async option replacement + empty-state markup via
`/combobox/options`), and post-HTMX-swap re-indexing. No-JS baseline is native
submit-button/link options (Combobox server-open listbox; Command fully-visible
grouped link-first list) — proven POST/navigation with Alpine/HTMX absent; HTMX parity
is raw `hx-*` on `Base.Attributes` (frozen §9 merge rule). The empty/separator ARIA
was fixed at source during the axe pass (listbox may only contain option/group). New
runtime.js `37f16f3d` / theme.css `17040d91`; `make test-ui-browser` **204 passed**
(3 engines), `make check`/`make guard` green. Parity rows P50/P51 stay unchecked until
GOTH-5.5. GOTH-5.3 DONE (2026-07-17) — P52 Data Table: a server-owned
sort/filter/page/selection grid COMPOSING Table (P24), Pagination (P19), and Checkbox
(P28) with a thin new compound surface (DataTable region, DataTableToolbar,
DataTableContent = the HTMX swap target, DataTableSortHeader with aria-sort + a real
sort link, DataTableEmpty, DataTableStatus; SortDirection enum). No new §8 controller
(runtime.js unchanged). No-JS baseline = real sort/page links + a filter form GET with
shareable URLs and full-document reload; HTMX parity swaps only the content region
(`outerHTML show:none focus-scroll:false`, toolbar caret preserved), degrading to the
no-JS path. Job 2 = the R7 HTMX `Attrs` finalization (first recorded review point):
`Trigger` became a typed `htmx.Trigger` and `SwapMods` was added — both with real
consumers (the 5.2 Combobox async migrated onto the typed `Trigger`); the residual
gaps and the CSRF posture stay PROVISIONAL for GOTH-7.3. Component CSS,
render/enum/merge + htmx tests, two showcase specimens + the `/data-table` round-trip
route, a 3-engine `data-table.spec.ts`; `make test-ui-browser` **225 passed** (3
engines), `make generate-ui-assets` byte-identical (theme.css `d9c83ee5`; runtime/htmx
unchanged), `make check`/`make guard` green. Parity row P52 stays unchecked and catalog
stays `planned` until GOTH-5.5. GOTH-5.4 DONE (2026-07-17) — P54 Sidebar: a
server-owned navigation shell COMPOSING the frozen gothDialog overlay mechanics for
the mobile off-canvas sheet (as Sheet P47) and Collapsible (P29 native `<details>`)
for nested groups, with NO new §8 controller name (runtime.js `37f16f3d`/htmx.js
`8689e2e2` byte-identical; only theme.css → `7acbbd55`). The server owns all three
states — desktop collapsed (data-collapsed), mobile open (gothDialog data-state), and
active item (real `<a>` links + aria-current, P44 link-first) — and every change is a
server round-trip (the SidebarRail collapse link with aria-expanded, nav links, the
/sidebar full-document route); persistence is server-owned/host-namespaced with no
client persistence surface. One markup renders both breakpoints via CSS (mobile-first
off-canvas sheet; ≥48rem static rail). 19 compound parts + SidebarSide enum, component
CSS (no inline style, RTL logical properties, reduced-motion collapse), 12 render
tests, three specimens (Interactive/no-JS/RTL) + the /sidebar round-trip route, a
3-engine `sidebar.spec.ts`; `make test-ui-browser` **255 passed** (3 engines), `make
check`/`make guard` green. Parity row P54 stays unchecked and catalog stays `planned`
until GOTH-5.5. GOTH-5.5 DONE (2026-07-17) — the wave closeout: an adversarial P49–P54
audit passed with no Phase-5 primitive reopened; each row was driven in the real
browser against the full gate (locale/date, server-ownership, no-JS/HTMX parity,
back-button history on the Data Table + Date Picker, responsive breakpoints incl. the
360px Data Table scroll wrapper and the 480px/1280px Sidebar, CSP, accessibility). The
GOTH-5.4 sidebar RTL entrance-animation nuance (physical `translateX` transient) was
FIXED at source with `[dir="rtl"]` `animation-name` overrides (theme.css `7acbbd55` →
`a40f4572`; runtime/htmx byte-identical) and recorded without unchecking GOTH-5.4;
deferrals carried; §9 confirmed coherent (typed `Trigger`/`SwapModifiers` frozen,
`Vals`/`Include`/`Headers`/`DisabledElt`/CSRF provisional for 7.3, markers intact).
`make test-ui-browser` **255 passed** (3 engines), `make check`/`make guard` green,
assets reproducible; Layer-4 screenshots captured/VIEWED; catalog P49–P54 → `accepted`,
parity-matrix rows checked. **Phase 5 is COMPLETE and its gate is MET.** Phase 6 IN
PROGRESS: GOTH-6.1 DONE (2026-07-18) — P55 Attachment as a pure F3 compound-parts
primitive with NO new §8 controller (StylesOnly, no JS): media/content/actions/
full-card trigger/group parts + size/orientation/state enums; the primitive DISPLAYS
caller-owned upload state (server owns state/progress; selects no file, owns no route/
storage/retries/authorization); meaning beyond color via a state glyph + text label +
determinate `<progress>` (the hue rides glyph+border, the label stays AA-safe muted —
fixed in the axe pass); independently focusable trigger/actions via a CSS `::after`
stretch (no inline style) in DOM order; a no-JS static gallery specimen + a REAL
multipart no-JS upload round-trip (host owns `<input type=file>`/`/attachment/upload`/
storage, the SERVER decides done-vs-error>512KB and 303-redirects back). Component CSS,
render/contract tests, a 3-engine `attachment.spec.ts`; `make test-ui-browser` **273
passed** (up from 255), `make generate-ui-assets` reproducible (theme.css `541ed3a7`;
runtime/htmx byte-identical), `make check`/`make guard` green. Parity row P55 stays
unchecked and catalog stays `planned` until the GOTH-6.6 wave closeout. GOTH-6.2 DONE
(2026-07-18) — messaging P56 Bubble + P59 Message (pure F3, no controller: seven Bubble
variants/alignment/groups/interactive reactions; aligned Message row with avatar/header/
body/footer + grouped-message slots; muted-variant AA fix) + P60 Message Scroller (F4).
P60 added **`gothMessageScroller` as the tenth frozen §8 controller** by a recorded
2026-07-18 owner reopen (gate-b-review.md addendum; README §8 updated) — a thin controller
composing the frozen GOTH-4.1 mechanics + live-region.js over a server-rendered role=log
transcript; no-JS baseline (readable log + real "load earlier" link + native #id jump
anchors + overflow-anchor) enhanced with live-edge following, HTMX prepend-without-jump,
jump-to-message focus, and unread/scroll-state exposure. 3-engine messaging.spec.ts (9) +
message-scroller.spec.ts (6); `make test-ui-browser` **318 passed** (up from 273); assets
reproducible (runtime.js `2b6cbcd1`, theme.css `d25161d1`; htmx byte-identical); `make
check`/`make guard` green. Parity rows P56/P59/P60 stay unchecked and catalog `planned`
until GOTH-6.6. GOTH-6.3 DONE (2026-07-18) — spatial P57 Carousel + P62 Scroll Area +
P61 Resizable all shipped. P62 = a native overflow region (focusable+named
keyboard-scrollable, themed scrollbar, no synthetic bar, no controller). P57 = CSS
scroll-snap track + snap-aligned slides + no-JS in-page anchor dots (server-chosen
current dot, no controller). P61 Resizable first stopped on an owner decision, then the
owner ratified the recommendation — **`gothResizable` ADDED as the ELEVENTH frozen §8
controller** (recorded 2026-07-18 gate-b-review.md addendum; README §8 updated). Built
on the post-Amendment-1 stack (absolute no-server-rendered-style invariant): server-owned
split baseline (data-default-size → primary flex-basis via external CSS `--goth-resize-basis`
buckets; role=group panes + role=separator handle with aria-valuenow/min/max) enhanced by
gothResizable — pointer drag (setPointerCapture) + APG keyboard (arrows/Home/End, clamp) +
RTL-aware direction, writing geometry through the CSSOM setProperty (never a rendered
style=). Assets: runtime.js `2b6cbcd1`→`80ebc7d6` (new controller, expected), theme.css
`ed23f5fb`, htmx.js `8689e2e2` + theme-default.css `ae49d971` byte-identical. 3-engine
spatial.spec.ts (9); `make test-ui-browser` **360 passed** (up from 345);
`make check`/`make guard` + build/vet/test green; assets reproducible. Parity rows
P57/P61/P62 stay unchecked and catalog `planned` until GOTH-6.6. GOTH-6.4 DONE
(2026-07-18) — P58 Chart shipped native/no-controller (the F4-native precedent; NO new
§8 controller, no owner decision). The server owns the chart: `computeChartLayout`
(pure, unit-tested) computes nice-scaled value axes + grid + grouped-bar/line/area
geometry, and `ChartSVG` emits a role=img SVG using ONLY sanctioned presentation/
geometry attributes (viewBox, x/y/width/height, points, d, cx/cy/r) with series color
on `data-series` + external `.goth-chart-*` CSS over the frozen `--chart-1..5` tokens —
ZERO server-rendered `style=`/`fill=` (a host retints by redeclaring the tokens). Three
kinds (bar/line/area); native `<title>` per-mark tooltips (no-JS, CSP-safe); F3 frame
parts (Chart/Header/Title/Description/Content = engine-neutral SVG-or-host-renderer
seam/Legend/LegendItem/Table); accessible tabular fallback = `ChartTable` native
`<details>` composing Table (P24). Files: `primitives/chart.{go,templ}` + tests, chart
CSS in components.css, `specimens_primitives_chart.go` (one StylesOnly `primitive-chart`
specimen: bar+line+area off shared data) + registry + `P58` in ImplementedPrimitives,
3-engine `chart.spec.ts` (4). Assets reproducible: runtime.js `80ebc7d6`/htmx.js
`8689e2e2`/theme-default.css `ae49d971` byte-identical (no controller change), only
theme.css → `701432c3`. `make test-ui-browser` **372 passed** (up from 360; axe + strict-
CSP crawls auto-cover the chart with zero violations across Chromium/Firefox/WebKit);
`make check`/`make guard` + build/vet/test green. Parity row P58 stays unchecked and
catalog `planned` until GOTH-6.6.
GOTH-6.5 DONE (2026-07-18) — P64 Toast + P63 Sonner over ONE gothToast live-region
queue (the pre-provisioned §8 controller refined in place; NO new §8 name, NO owner
decision): announce-once through the shared live-region (polite/assertive by priority),
pause-on-hover/focus/hidden-page timers, dedupe, data-max overflow, keyboard/pointer
dismissal + actions, reduced-motion collapse; P64 is the composable shape, P63 the
opinionated facade over the same queue; no inline style anywhere. 3-engine toast.spec.ts
(8) + sonner.spec.ts (4); `make test-ui-browser` **408 passed** (up from 372); assets
reproducible (runtime.js `ddc6f2ce`, theme.css `f2474b2b`; htmx/theme-default
byte-identical); `make check`/`make guard` green. GOTH-6.6 DONE (2026-07-18) — the
64-entry parity milestone CLOSED: an adversarial per-row audit of P55–P64 passed (all
PASS, no task reopened), a confirmation sweep found no Phase-6/Amendment-1 regression in
P01–P54, the three flagged dispositions were recorded (the 6.5 last-wins live-region
flag ACCEPTED as the frozen shared-runtime contract; all Phase-6 deferrals confirmed
enhancement-not-parity; no row failed), the full matrix is green (both modules
build/vet/test, `make generate` no-op, `make generate-ui-assets` byte-identical, `make
check`/`make guard`, `make test-ui-browser` 408 passed 3 engines), Layer-4 screenshots
captured/viewed, catalog P55–P64 → `accepted` (all 64 accepted), and parity rows
P55–P64 checked. **Phase 6 is COMPLETE and its gate is MET; the milestone is CLOSED.**
Phase 7 IN PROGRESS: GOTH-7.1 DONE (2026-07-18) — the first `components/` layer
landed: the fourteen named domain-neutral compositions as four per-dir packages
under `ui/goth/components/{layouts,forms,feedback,data}` (+ an internal `kit` sharing
the frozen `Base`/`MergeAttributes` grammar), each composing primitives ONLY (no new
primitive/§8 controller/domain type/route), emitting zero server-rendered style, and
inheriting server-ownership + no-JS baseline from its primitives. Per-package render/
contract tests + real showcase specimens (new `SectionComponent` under a
`TestEveryImplementedComponentHasSpecimen` gate) + a 3-engine `components.spec.ts`;
component CSS rebuilt via `make generate-ui-assets` (reproducible: theme.css →
`b554d930`; runtime/htmx/theme-default byte-identical — no controller change); `make
test-ui-browser` **429 passed** (up from 408); `make build`/`make vet`/`make test`/
`make guard`/`make check` green. GOTH-7.2 DONE (2026-07-18) — authentication's view
adapter migrated to GOTH: the sibling module renamed in place
`features/authentication/views/{templ→goth}` (empty `git tag` posture; go.work +
Makefile + auth-cms go.mod repointed), all sixteen `Views` methods re-implemented on
`ui/goth` (AuthShell/FormField + Input/Button/NativeSelect + `Bundle.Head()` assets)
with every `action`/`name`/`autocomplete`/csrf/return-to/secret-discipline contract
preserved and the feature core still templ/ui-goth-free (guard G5). `HTMLPolicy()` maps
`Bundle.Requirements()` → `HTMLResourcePolicy` with deterministic source ordering (C3)
and `script-src 'self' + Nonce:true` (C5 contract test across all three profiles); the
inline reset/magic-link fragment readers were externalized to a served `fragment.js`
(no inline script). auth-cms wires the bundle, serves assets + the fragment route, and
sets `Config.HTMLPolicy` off the promoted adapter. Real-router httptest proof
(`goth_proof_test.go`) + a live-server curl journey confirmed
login/register/reset/passwordless/magic render styled via GOTH under the mapped CSP,
the assets + fragment.js serve, the JSON API is unchanged, and the nil-Views asset-free
posture holds. All existing auth + auth-cms tests green; `make check`/`make guard`
green; `make test-ui-browser` unaffected at **429** (no ui/goth asset change). GOTH-7.3
DONE (2026-07-18) — CMS's view adapter migrated to GOTH + the HTMX grammar proved and
FINALIZED: the sibling module renamed in place `features/cms/views/{templ→goth}` (empty
`git tag` posture; go.work + Makefile + all three consuming hosts' go.mods
[`examples/{cms,minimal,auth-cms}`] repointed, `ui/goth` added), all eighteen `Views`
methods re-implemented on `ui/goth` (AppShell/PageHeader/FormField/DataTable/Table +
Input/Textarea/NativeSelect/Checkbox/Button/Badge + `Bundle.Head()`) with every
`action`/`name`/`enctype`/prefix-href/pager contract preserved and the CMS feature core
still templ/ui-goth-free (guard G2 — the handler reads `HX-Request` directly, importing
no UI package). HTMX proof (the named admin entries-table filtering/pagination flow): the
status filter form, `created_at` sort toggle, and pagination links carry explicit `hx-*`
(hx-get + hx-target `#cms-entries-content` + hx-swap `outerHTML show:none focus-scroll:false`
+ hx-push-url; filter adds hx-trigger `change`) swapping the content region, and the `List`
handler returns the new `EntriesListContent` fragment on `HX-Request` else the full
document — degrading to the exact no-JS reload. **CSRF posture SETTLED: hidden-input-only**
(every CMS mutation rides a `<form>`; the HTMX surface is GET-only). R7 §9 finalization (the
second and final recorded review point): the typed `Trigger`/`SwapModifiers` are CONFIRMED
FROZEN (CMS is a third consumer) and `Vals`/`Include`/`Headers`/`DisabledElt`/typed-URL are
**RETIRED** as no-demonstrated-need — **nothing in §9 is provisional after this task**; no
`htmx/attributes.go` field changed (assets byte-identical), README §9 + a dated
gate-b-review.md addendum record it. Real-router httptest proof
(`examples/minimal/cmd/server/goth_htmx_proof_test.go`) + a live curl journey drove
full-document↔HTMX-fragment↔no-JS parity, shareable status-filter URLs, and the stylesheet
serving 200 `text/css`. All existing CMS/theme/auth-cms tests green; `make warm-scaffold-cache`
+ `make build`/`make vet`/`make test` + `make guard` (18 guards) + `make check` (templ +
ui/goth asset drift clean) all green; `make test-ui-browser` unaffected at **429** (asset-drift
guard is the recorded proof — only doc comments changed in ui/goth).
GOTH-7.4 DONE (2026-07-18) — the adopter documentation landed: `ui/goth/README.md`
gained **§11** (install/wiring, profiles + asset route, the `Requirements`→CSP
formatter recipe, the `components/` layer API, the "forgot to mount the asset route"
failure mode + boot-time `Manifest.Assets()` reachability self-check, the `views/goth`
adapter + `HTMLPolicy` recipe, the HTMX migration trigger + hidden-input CSRF, the
`views/templ`→`views/goth` tag posture, the SRI + CDN-relocation caveats, and the
GPS/Segovia brand-token override + handoff — no Segovia code imported; the frozen §1–§10
contract untouched). The gate-C GOTH-7.4 obligations were discharged (the self-check and
SRI CDN caveat documented; the CSP formatter kept as a host-side recipe proven by test,
NOT a new kit helper — that would reopen GOTH-0.3/Gate B, recorded as a deferred owner
decision). `RELEASING.md` recorded the `Requirements`-surface upgrade-note convention;
`ui/README.md`/`ARCHITECTURE.md`/`NOTES.md` + the stale `views/templ`/`authtempl`/
`cmstempl`/`.templ`-location doc lags across the feature/example READMEs and
`THIRD_PARTY_NOTICES.md` were all folded in. New
`examples/minimal/cmd/server/goth_doc_snippets_test.go` compiles+runs the two
Phase-7-learning recipes (the asset self-check also proves the failure mode is caught);
the wiring/HTMX/`HTMLPolicy` snippets are proven by the existing 7.2/7.3 proof tests.
`make warm-scaffold-cache` + `make build`/`make vet`/`make test` + `make guard` (18
guards) + `make check` all green; assets byte-identical (`make test-ui-browser`
unaffected at **429**). **Next dependency-ready: GOTH-7.5** (final adversarial/
accessibility/release audit) — the last Phase 7 task; owner directs any tag/PR.

This file is status and dispatch only. The full authority for scope, files,
work, acceptance criteria, risks, and exact verification is
[`plan.md`](plan.md). Do not implement from this checklist alone.

## Implementer protocol

Suggested prompt:

> Execute task `GOTH-x.y` from `.claude/plans/ui-goth/plan.md`. Read the full
> plan, `.claude/plans/ui-goth/TASKS.md`, `.claude/agents/implementer.md`,
> `ARCHITECTURE.md`, `features/README.md` when a feature is touched, the
> `Makefile`, and every file/module contract cited by the task. Confirm every
> dependency and ratification gate below is complete. Preserve unrelated
> worktree changes, implement only that task, run its exact verification until
> green, append a dated evidence note under the task in `plan.md`, and only then
> check the task here. Stop on an unresolved owner decision or architecture
> conflict; do not implement around it.

Execution rules:

1. Run one numbered task at a time. Numeric order is the default whenever more
   than one task is dependency-ready.
2. `[x]` means the task's exact plan verification passed and its evidence was
   recorded. Partial code, a compile-only result, or deferred browser proof is
   not complete.
3. A wave closeout task may reopen any task in that wave. Uncheck the affected
   task and record why rather than hiding remediation in closeout.
4. Never hand-edit `*_templ.go`, compiled CSS/JS, or the asset manifest. Edit
   sources and use the planned generation targets.
5. Do not cross Gate A, Gate B, or Gate C without the recorded owner/reviewer
   decision. These are decision gates, not implementer tasks.
6. `plan.md` and this board must change together when a task is split, merged,
   reordered, or materially re-scoped.

## Ratification gates

### Gate A — authorize foundation work

Ratified in full by the owner on 2026-07-17 (interactive session):

- [x] owner accepts the seventh `UI implementation` module kind and dependency
  arrows;
- [x] owner accepts a repository-local exact-pinned Node build/test toolchain
  while downstream Go consumers remain Node-free;
- [x] owner accepts Playwright Chromium/Firefox/WebKit plus axe as a release
  gate; and
- [x] owner accepts the tag-sensitive plan to rename untagged feature
  `views/templ` modules to `views/goth`, falling back to additive compatibility
  modules if a tag appears.

### Gate B — freeze public UI contracts

Required after `GOTH-0.3` and before `GOTH-1.1`:

- [x] owner/API review accepts the exact primitive props/slots/attributes/ID
  grammar; accepted by owner 2026-07-17 conditional on remediation (applied — see
  gate-b-review.md)
- [x] owner/API review accepts bundle profiles, manifest/asset API, theme token
  contract, and HTMX helper contract; accepted by owner 2026-07-17 conditional on
  remediation (applied — see gate-b-review.md)
- [x] the accepted signatures and decisions are recorded in `plan.md` and
  `ui/goth` design docs; accepted by owner 2026-07-17 conditional on remediation
  (applied — see gate-b-review.md)

### Gate C — freeze authentication policy

Required before `GOTH-0.4` is marked complete and before `GOTH-7.2`:

- [x] authentication/security review accepts the public resource-policy type,
  validation rules, Config construction matrix, and immutable fixed headers; accepted
  2026-07-17 (see gate-c-review.md)
- [x] nil policy is proven to preserve the current asset-free posture; accepted
  2026-07-17 (see gate-c-review.md)
- [x] no feature-core dependency on templ or `ui/goth` is introduced. accepted
  2026-07-17 (see gate-c-review.md)

## Phase 0 — architecture and contract freeze

Gate B was accepted by the owner on 2026-07-17 conditional on the R1–R9
remediation, which is applied (see `gate-b-review.md` and the GOTH-0.3 Gate B
remediation evidence in `plan.md`). Gate C was accepted by the owner on 2026-07-17
conditional on the C1–C5 doc/release-note remediation, which is applied (see
`gate-c-review.md` and the GOTH-0.4 Gate C evidence in `plan.md`); GOTH-0.4 is now
complete and Phase 0 is closed. GOTH-1.1 (module, workspace, frozen contract types,
generated-source wiring) and GOTH-1.2 (exact-pinned Node asset pipeline: fingerprinted
`dist/` CSS/JS/HTMX + committed manifest, embed, Node-gated regenerate + plain-git
drift check, provenance/SHA-256 recorded) are complete with recorded evidence
(2026-07-17). GOTH-1.3 (neutral/default light/dark themes via contract.css/default.css,
profile-aware `Bundle.Head()`, the nonced dynamic stylesheet for `Config.Theme`
overrides, deterministic minimal CSP requirements) is complete with recorded evidence
(2026-07-17). GOTH-1.4 (named goth-prefixed Alpine controllers + shared accessibility
mechanics registered once against the `@alpinejs/csp` build, no `unsafe-eval`; the
single self-hosted `htmx.js` = vendored htmx 2.0.10 + the explicit Gopernicus non-2xx
response config; profile-load diagnostics; asset-level CSP/no-remote-origin proofs) is
complete with recorded evidence (2026-07-17). GOTH-1.5 (zero-datastore
`examples/goth-showcase` host serving every profile/theme/HTMX fixture from the embedded
fingerprinted assets under a strict CSP mapped from `Requirements`; the exact-pinned
three-engine Playwright + axe harness behind the Node-gated `make test-ui-browser`;
a registry that fails automatically on a missing primitive specimen) is complete with
recorded evidence (2026-07-17) — the deferred GOTH-1.3/1.4 real-browser proofs now run
and pass in Chromium/Firefox/WebKit, and the browser proof surfaced + fixed two 1.4
runtime defects at source. **Phase 1 is complete and its gate is MET.** Next executable
wave: **Phase 2** (GOTH-2.1 content/status primitives), gated on the Phase 1 gate now
satisfied.

- [x] **GOTH-0.1** — record preflight, catalog, and provenance
  - depends on: repository preflight + Gate A
- [x] **GOTH-0.2** — amend the module taxonomy and dependency guards
  - depends on: GOTH-0.1
- [x] **GOTH-0.3** — freeze API, theme, runtime, and HTMX contracts
  - depends on: GOTH-0.1
- [x] **GOTH-0.4** — freeze authentication's HTML resource-policy seam
  - depends on: GOTH-0.3 + Gate C review

Phase 0 gate: **MET (2026-07-17)** — GOTH-0.1–0.4 are green, Gate B and Gate C are
both recorded, and the architecture/API/security contracts are stable enough to
implement without guessing.

## Phase 1 — GOTH foundation and showcase

- [x] **GOTH-1.1** — create the module, workspace, public contract types, and
  generated-source wiring
  - depends on: GOTH-0.2 + GOTH-0.3 + Gate B
- [x] **GOTH-1.2** — implement the exact-pinned asset pipeline (amended in place
  by Amendment 1 / GOTH-A.1 2026-07-18: Tailwind removed from the pipeline, CSS
  compiled by the pinned esbuild + an owned reset; box NOT unchecked per the
  GOTH-5.5 precedent)
  - depends on: GOTH-1.1
- [x] **GOTH-1.3** — implement themes, document composition, and CSP profiles
  (amended in place by Amendment 1 / GOTH-A.2 2026-07-18: the nonced dynamic
  stylesheet + `Config.Theme` channel removed and the default palette split into
  the injected `theme-default.css`; token/appearance work intact; box NOT
  unchecked)
  - depends on: GOTH-1.2
- [x] **GOTH-1.4** — implement Alpine CSP and HTMX runtime foundations (amended
  in place by Amendment 1 / GOTH-A.2 2026-07-18: `Requirements.RequiresNonce`
  removed, `style-src 'self'`; controllers untouched; box NOT unchecked)
  - depends on: GOTH-1.2 + GOTH-1.3
- [x] **GOTH-1.5** — create the showcase and browser/accessibility harness
  (amended in place by Amendment 1 / GOTH-A.2 2026-07-18: showcase nonce
  plumbing removed, host-stylesheet specimen added; box NOT unchecked)
  - depends on: GOTH-1.3 + GOTH-1.4

Phase 1 gate: **MET (2026-07-17)** — all three runtime profiles work from
self-hosted fingerprinted assets (browser-proven), generation is reproducible
(`make generate-ui-assets` byte-identical, `make check` clean), strict CSP
diagnostics are clean (zero securitypolicyviolation across all specimens), and the
three-engine `examples/goth-showcase` Playwright + axe harness is green (36 passed:
12 specs × Chromium/Firefox/WebKit via `make test-ui-browser`). Phase 2 is now
dependency-ready.

## Phase 2 — presentational and native/form foundations

- [x] **GOTH-2.1** — implement content/status primitives P01–P04, P08, P10,
  P14–P15, P17, P22–P23, P26 (done 2026-07-17; module tests + catalog registration
  + 3-engine axe/CSP browser proof + `make check`/`make guard` green; parity rows
  P01–P26 stay unchecked until the GOTH-2.4 wave audit)
  - depends on: Phase 1 gate
- [x] **GOTH-2.2** — implement action/navigation primitives P05–P07, P09, P19,
  P21 (done 2026-07-17; module tests + catalog registration + 3-engine axe/CSP
  browser proof + `make check`/`make guard` green; parity rows P05–P26 stay
  unchecked until the GOTH-2.4 wave audit)
  - depends on: GOTH-2.1
- [x] **GOTH-2.3** — implement field/data primitives P11–P13, P16, P18, P20,
  P24–P25 (done 2026-07-17; 8 native/presentational primitives — Field, Input,
  Input Group, Label, Native Select, Progress, Table, Textarea — with component
  CSS, render/native-form tests, showcase specimens incl. a no-JS `<form>`
  submission specimen, and 3-engine axe/CSP browser proof (36 passed);
  `make check`/`make guard` green; no new controller surface. Parity rows P11–P26
  stay unchecked until the GOTH-2.4 wave audit)
  - depends on: GOTH-2.1
- [x] **GOTH-2.4** — close the 26-entry foundation wave (done 2026-07-17; adversarial
  P01–P26 parity audit passed with no task reopened; module build/test/vet +
  `make generate` no-op + `make generate-ui-assets` byte-identical + `make check` +
  `make guard` + `make test-ui-browser` (36 passed, 3 engines) all green; Layer-4
  screenshots captured/viewed at narrow/wide × light/dark × RTL; catalog P01–P26 →
  `accepted`; parity-matrix rows P01–P26 checked)
  - depends on: GOTH-2.2 + GOTH-2.3

Phase 2 gate: **MET (2026-07-17)** — the GOTH-2.4 wave audit accepted P01–P26 against
the full parity definition (no primitive reopened), the three-engine browser/axe/CSP
proof is green (36 passed), generation is reproducible, and `make check`/`make guard`
pass. P01–P26 are checked in the plan's parity matrix and `accepted` in catalog.md.

## Phase 3 — disclosure and selection

- [x] **GOTH-3.1** — implement disclosure primitives P27, P29, P34 (done 2026-07-17;
  Accordion/Collapsible on native `<details>`/`<summary>` with a real no-JS baseline —
  single-open exclusivity via native `<details name>` — and Tabs with a
  server-owned active-panel baseline; enhanced by the frozen `gothCollapse`/`gothTabs`
  controllers (no new controller surface); the GOTH-1.5 descendant-`$el` audit bug in
  `gothTabs` fixed at source and `gothCollapse` moved to the native-details model;
  component CSS, render/contract tests, Interactive-profile showcase specimens, and a
  3-engine `disclosure.spec.ts` keyboard/interaction proof — `make test-ui-browser`
  45 passed, `make check`/`make guard` green. Parity rows stay unchecked until GOTH-3.4)
  - depends on: Phase 2 gate
- [x] **GOTH-3.2** — implement form-selection primitives P28, P31–P33 (done
  2026-07-17; 4 native form-selection primitives — P28 Checkbox (F1), P33 Switch
  (F1, checkbox-backed, no controller), P31 Radio Group (native radios), P32 Slider
  (native single-thumb range) — every one submits correct values in a plain HTML
  form with no JS and binds no Alpine controller; component CSS off native
  pseudo-classes/`data-*` with the strict `img-src 'self'` unchanged (pseudo-element
  tick, no data-URI), render/native-submission tests, StylesOnly showcase specimens
  incl. a no-JS `<form>` GET submission specimen + `/selection/echo` route, and a
  3-engine `selection.spec.ts` keyboard/pointer/submission proof — `make
  test-ui-browser` **60 passed**, `make check`/`make guard` green. Parity rows
  P28/P31/P32/P33 stay unchecked until GOTH-3.4; the F4 catalog family label for
  P31/P32 vs their native-only shipping is flagged for the GOTH-3.4 audit)
  - depends on: Phase 2 gate
- [x] **GOTH-3.3** — implement compact-selection primitives P30, P35–P36 (done
  2026-07-17; 3 primitives — P30 Input OTP (grouped native single-char inputs, no
  controller), P35 Toggle (checkbox-backed toggle button, native submit, no
  standalone controller), P36 Toggle Group (single = native radios no controller;
  multiple = native checkboxes enhanced by the frozen `gothRovingFocus` for a single
  tab stop) — every one submits correct values in a plain HTML form with no JS; no new
  controller surface; component CSS off `:checked`/`:has()`/`data-*`, render/native-
  submission tests, StylesOnly/Interactive showcase specimens incl. a no-JS
  `/compact/echo` GET form (OTP reported by count only — no secret echo), and a
  3-engine `compact.spec.ts` paste/backspace/focus/roving/submission proof — `make
  test-ui-browser` **72 passed**, `make check`/`make guard` green. Parity rows
  P30/P35/P36 stay unchecked until GOTH-3.4; the F4 family label for P30 and the
  "single/multiple gothRovingFocus" wording for P36 vs native-baseline shipping are
  flagged for that audit)
  - depends on: Phase 2 gate
- [x] **GOTH-3.4** — close the 10-entry stateful wave (done 2026-07-17; adversarial
  P27–P36 audit passed with no Phase 3 primitive task reopened; the Tabs RTL
  arrow-direction nuance was FIXED at source — `mechanics/roving.js` is now
  direction-aware so ArrowLeft advances under `dir=rtl` (Radix/APG parity), with a
  new `primitive-tabs-rtl` specimen + three-engine RTL test; the P30/P31/P32/P36
  F4-vs-native family labels were reconciled by recording the "F4 = controller
  where enhancement is required; a sufficient native baseline satisfies the row"
  rationale WITHOUT editing the frozen family column; OTP auto-advance/paste,
  native-details height animation, Slider multi-thumb, and Checkbox AT-mixed
  recorded as deferred follow-ups. Module build/test/vet + `make generate` no-op +
  `make generate-ui-assets` byte-identical + `make check` + `make guard` +
  `make test-ui-browser` (75 passed, 3 engines) all green; Layer-4 stateful
  screenshots captured/viewed at open/closed × checked/unchecked × active-tab ×
  light/dark × RTL; catalog P27–P36 → `accepted`; parity-matrix rows P27–P36 checked)
  - depends on: GOTH-3.1 + GOTH-3.2 + GOTH-3.3

Phase 3 gate: **MET (2026-07-17)** — the GOTH-3.4 wave audit accepted P27–P36
against the full Phase-3 gate (native form, keyboard/focus incl. roving, RTL,
reduced-motion, CSP; no primitive reopened), the three-engine browser/axe/CSP proof
is green (75 passed), generation is reproducible, and `make check`/`make guard`
pass. P27–P36 are checked in the parity matrix and `accepted` in catalog.md.

## Phase 4 — overlays and navigation

- [x] **GOTH-4.1** — freeze shared overlay/menu mechanics (done 2026-07-17; shared
  mechanics frozen — overlay stacking/nesting model + nested-aware Escape/outside
  dismiss (`mechanics/overlay-stack.js` + rewritten `dismiss.js`), focus trap/restore
  with a trap stack, ref-counted class-based scroll lock, native-`inert` background
  handling, CSP-safe anchored placement + collision (`data-side/align` +
  CSSOM `--goth-anchor-*`, no inline style), roving/typeahead reuse, and submenu
  hierarchy; overlay scrim on the frozen `overlay` token. No new §8 controller name.
  The three latent `this.$el`-outside-init defects in `dialog.js`/`menu.js`/
  `combobox.js` FIXED at source (cache `_root` in init); gothDialog gained modal
  scroll-lock+inert, gothMenu gained anchoring+submenu, gothCombobox gained listbox
  anchoring. Two Interactive mechanics fixtures + a 3-engine `mechanics.spec.ts`;
  `make test-ui-browser` **90 passed**, `make check`/`make guard` green, assets
  reproducible)
  - depends on: Phase 3 gate
- [x] **GOTH-4.2** — implement modal/panel primitives P37, P39–P40, P47 (done
  2026-07-17; 4 gothDialog-backed F4 primitives — P39 Dialog, P37 Alert Dialog, P40
  Drawer, P47 Sheet — each COMPOSING the frozen GOTH-4.1 overlay mechanics, no
  mechanics fork and no new §8 controller name. Native-`<dialog>` decision resolved
  WITHIN the frozen contract: div-based overlay reused (native modal needs
  showModal() JS anyway → no honest no-JS gain, and native cannot deliver the frozen
  nested overlay-stack / CSP-safe class scroll-lock+inert / shared trap stack without
  forking them). No-JS baseline is server-owned `data-state` (server-open panel
  readable with no JS, proven on StylesOnly); Alert Dialog is the destructive-decision
  contract (`role=alertdialog`, always modal, `data-dismiss-outside="false"` — scrim
  does not dismiss, Escape cancels); Drawer/Sheet are edge panels keyed off
  `data-side` with reduced-motion-honored slide-in. `gothDialog` refined (dismiss-
  outside filter + honest aria-modal lifecycle) without touching `mechanics/*`.
  Component CSS (no inline style, all animations in the reduced-motion collapse),
  render/contract tests, eight showcase specimens (4 Interactive incl. a nested
  dialog + 4 StylesOnly server-open), and a 3-engine `overlays.spec.ts` (focus
  trap/restore, scroll lock, inert, nested Escape, scrim policy, no-JS readability) —
  `make test-ui-browser` **114 passed** (3 engines), `make generate-ui-assets`
  byte-identical, `make check`/`make guard` green. Parity rows P37/P39/P40/P47 stay
  unchecked until the GOTH-4.5 wave audit)
  - depends on: GOTH-4.1
- [x] **GOTH-4.3** — implement anchored information/selection primitives P42,
  P45–P46, P48 (done 2026-07-17; 4 anchored primitives — P48 Tooltip + P42 Hover
  Card on the two NEW `gothTooltip`/`gothHoverCard` controllers [owner reopen of the
  frozen §8 set, recorded in `gate-b-review.md`], composing the frozen GOTH-4.1
  anchor/dismiss mechanics with no fork; P45 Popover riding native
  `popover`/`popovertarget` + a delegated CSP-safe anchor enhancement; P46 Select as
  a styled native `<select>`, no controller. No-JS baselines proven: tooltip/hover
  card CSS `:hover`/`:focus-within` reveal + describedby, native popover click/Escape/
  light-dismiss with Alpine absent, native select GET submission. Component CSS,
  render/contract tests, seven showcase specimens (3 Interactive + 3 StylesOnly + a
  no-JS select form), and a 3-engine `anchored.spec.ts` — `make test-ui-browser`
  **144 passed**, `make generate-ui-assets` byte-identical, `make check`/`make guard`
  green. Parity rows P42/P45/P46/P48 stay unchecked until GOTH-4.5)
  - depends on: GOTH-4.1
- [x] **GOTH-4.4** — implement menu primitives P38, P41, P43–P44 (done 2026-07-17;
  4 menu primitives — P41 Dropdown Menu, P38 Context Menu, P43 Menubar all backed by
  the existing frozen `gothMenu` (NO new §8 controller name — gothMenu's context-menu
  pointer/keyboard opening + menubar horizontal coordination were REFINED in place,
  additive; the default dropdown path is behavior-unchanged), and P44 Navigation Menu
  native + link-first (real `<a href>` links + a native `<details>`/`<summary>`
  disclosure, NO controller — the recorded F4-native precedent). Item roles per row:
  menuitem + menuitemcheckbox/menuitemradio with server-owned aria-checked;
  menubar/menuitem for the bar. No-JS baselines proven: Dropdown server-open readable
  panel, navigation real links + native details disclosure all with Alpine absent.
  Only `controllers/menu.js` + component CSS changed (no `mechanics/*` fork, no inline
  style, reduced-motion honored); render/contract tests, five showcase specimens
  (3 Interactive + 2 StylesOnly no-JS), and a 3-engine `menu.spec.ts` (WAI keyboard
  matrix, submenu, typeahead, Escape unwind, RTL-aware keys, context right-click +
  ContextMenu-key, menubar traversal, no-JS navigation) — `make test-ui-browser`
  **171 passed**, `make generate-ui-assets` byte-identical, `make check`/`make guard`
  green. Parity rows P38/P41/P43/P44 stay unchecked until the GOTH-4.5 wave audit)
  - depends on: GOTH-4.1
- [x] **GOTH-4.5** — close the 12-entry overlay/navigation wave (done 2026-07-17;
  adversarial P37–P48 audit passed with no Phase-4 primitive reopened; the flagged
  GOTH-4.1 menu-anchor timing flake was FIXED at source in the TESTS —
  `mechanics.spec.ts` + `menu.spec.ts` now `expect.poll` the settled CSSOM anchor value
  rather than reading once; the race is the test's timing assumption, not `anchor.js`
  (which sets the property synchronously and is correct). Recorded dispositions:
  server-owned checkbox/radio menu items KEPT per invariant 1; per-primitive prefixed
  part sets KEPT per the frozen Shadcn-style compound-prefix grammar; deferred
  enhancements (Drawer drag-to-dismiss, exit animations, Navigation hover-open,
  Context/Hover touch long-press, server-open controller adoption) recorded. Module
  build/test/vet + `make generate` no-op + `make generate-ui-assets` byte-identical +
  `make check` + `make guard` + `make test-ui-browser` (171 passed, 3 engines, no
  flaky; deflaked anchor specs re-ran 3× three-engine clean) all green; Layer-4
  screenshots (22 overlay/menu specimens: open dialogs/sheets/drawers with scrim,
  nested dialog stacking, anchored popover/menus with submenu open, light/dark, RTL
  menu) captured/viewed; catalog P37–P48 → `accepted`; parity-matrix rows P37–P48
  checked)
  - depends on: GOTH-4.2 + GOTH-4.3 + GOTH-4.4

Phase 4 gate: **MET (2026-07-17)** — the GOTH-4.5 wave audit accepted P37–P48 against
the full Phase-4 gate (nested-overlay, focus trap/restore, keyboard/typeahead/menubar/
submenu-RTL, pointer/touch, dynamic-style CSP via the CSSOM anchor path with zero
securitypolicyviolation, RTL, reduced motion, and each row's no-JS baseline; no
primitive reopened), the three-engine browser/axe/CSP proof is green (171 passed),
generation is reproducible, and `make check`/`make guard` pass. P37–P48 are checked in
the parity matrix and `accepted` in catalog.md. Phase 5 is now dependency-ready.

## Phase 5 — composite data/time/application primitives

- [x] **GOTH-5.1** — implement date primitives P49, P53 (done 2026-07-17; P49
  Calendar + P53 Date Picker. The server owns all date math and locale rendering (Go
  computes the month grid, weekday order, prev/next month values, and selected/today/
  disabled/outside states; the primitive holds no clock and no time-zone policy —
  `Today` is caller-supplied and labels are overridable, no CLDR dependency). No-JS
  baseline: day selection and prev/next month are native form-submit buttons (day →
  `name="date"` ISO value; nav → `name="month"`), disabled/outside days are
  non-focusable spans; APG grid semantics (role=grid/row/columnheader/gridcell,
  aria-selected on the gridcell, single server-chosen roving tab stop). The keyboard
  grid enhancement REFINES the frozen `gothRovingFocus` controller IN PLACE with a
  grid mode (new generic `createGridRoving` in the shared `mechanics/roving.js`;
  direction-aware 2D arrows + Home/End tolerant of month-edge holes via
  data-col/data-row) — NO new §8 controller name (the gothMenu/roving.js
  in-place-refinement precedent); the Toggle Group default path is behavior-unchanged.
  Date Picker composes Field + native Popover + Calendar with no controller; the
  parse/format/error contract is server-owned; day selection has a no-JS full-reload
  path and an HTMX fragment-swap path (Calendar `DayAttributes` hx-get + native
  `popovertarget` dismiss) proven equivalent. Component CSS (no inline style, RTL
  chevron flip, reduced motion), deterministic render tests, showcase specimens
  (Calendar Interactive; Date Picker Full/HTMX + StylesOnly no-JS; `/calendar/select`
  and `/datepicker/pick` server round-trip routes), a 3-engine `date.spec.ts` —
  `make test-ui-browser` **186 passed**, `make generate-ui-assets` byte-identical,
  `make check`/`make guard` green. Parity rows P49/P53 stay unchecked and catalog
  status stays `planned` until the GOTH-5.5 wave audit)
  - depends on: Phase 4 gate
- [x] **GOTH-5.2** — implement command/combobox primitives P50–P51 (done 2026-07-17;
  P50 Combobox + P51 Command, both backed by the frozen `gothCombobox` refined IN
  PLACE — NO new §8 controller name. Server owns the option data and, in server-filter
  mode, the filtering + empty-state markup (async option replacement via
  `/combobox/options`); the input keeps focus and an `aria-activedescendant` loop moves
  the active option. No-JS baseline: submit-button options + server-open listbox
  (Combobox) and a fully-visible link-first grouped list (Command) that navigate/submit
  with Alpine/HTMX absent — proven POST. HTMX/no-JS parity: raw `hx-*` on
  `Base.Attributes` per the frozen §9 merge rule swaps the option list, equivalent to
  the no-JS reload. During the axe pass the empty/separator ARIA was fixed at source
  (a listbox may only contain option/group: empty → `role=option`+`aria-disabled`,
  separator → decorative `aria-hidden`). Component CSS, render/ARIA/merge/enum tests,
  five showcase specimens (Combobox client/async/no-JS + Command Interactive/no-JS) +
  `/combobox/options` (async seam), `/combobox/pick` (GET+POST echo), `/command/run`
  routes, a 3-engine `palette.spec.ts` — `make test-ui-browser` **204 passed**,
  `make generate-ui-assets` byte-identical, `make check`/`make guard` green. Parity
  rows P50/P51 stay unchecked and catalog stays `planned` until GOTH-5.5. §9 `Attrs`
  gaps (hx-trigger modifiers, hx-swap-from-input, URL typing) recorded for the
  GOTH-5.3/7.3 finalization)
  - depends on: Phase 4 gate
- [x] **GOTH-5.3** — implement Data Table P52 (done 2026-07-17; server-owned
  sort/filter/page/selection grid COMPOSING Table (P24), Pagination (P19), and
  Checkbox (P28) with a thin new surface — DataTable region, DataTableToolbar,
  DataTableContent (the HTMX swap target), DataTableSortHeader (real link +
  aria-sort), DataTableEmpty, DataTableStatus; SortDirection enum. No new §8
  controller (runtime.js unchanged). No-JS baseline = real sort/page links + a
  filter form GET with shareable URLs and full-document reload; HTMX parity swaps
  only the content region (outerHTML show:none focus-scroll:false) with the toolbar
  filter caret preserved. Job 2 = the R7 HTMX Attrs finalization: Trigger became a
  typed htmx.Trigger and SwapMods added (both with real consumers — Combobox async
  migrated onto the typed Trigger); the residual gaps + CSRF posture stay
  PROVISIONAL for GOTH-7.3. Component CSS, render/enum/merge tests, htmx tests, two
  showcase specimens + the /data-table round-trip route, a 3-engine
  data-table.spec.ts — `make test-ui-browser` **225 passed**, `make
  generate-ui-assets` byte-identical (theme.css d9c83ee5; runtime/htmx unchanged),
  `make check`/`make guard` green. Parity row P52 stays unchecked and catalog stays
  `planned` until the GOTH-5.5 wave audit)
  - depends on: Phase 4 gate; component prerequisites GOTH-2.3, GOTH-3.2,
    GOTH-4.3
- [x] **GOTH-5.4** — implement Sidebar P54 (done 2026-07-17; a server-owned
  navigation shell COMPOSING the frozen gothDialog overlay mechanics for the mobile
  off-canvas sheet (exactly as Sheet P47) and Collapsible (P29 native `<details>`) for
  nested groups, with NO new §8 controller name — runtime.js/htmx.js rebuilt
  byte-identical, only theme.css changed. The SERVER owns all three states: desktop
  collapsed (data-collapsed), mobile open (gothDialog data-state), and active item
  (real `<a>` links + aria-current, P44 link-first precedent); every change is a
  server round-trip (SidebarRail collapse link with aria-expanded, nav links, the
  /sidebar full-document route). Persistence is server-owned/host-namespaced — no
  client persistence surface. One markup renders both breakpoints via CSS
  (mobile-first off-canvas sheet; ≥48rem static rail). 19 compound parts +
  SidebarSide enum, component CSS (no inline style, RTL logical properties,
  reduced-motion collapse), 12 render/enum/merge tests, three showcase specimens
  (Interactive + no-JS + RTL) + the /sidebar round-trip route, a 3-engine
  `sidebar.spec.ts` (no-JS nav/collapse/persistence, gothDialog open/Escape/scrim/
  focus-restore, RTL edge flip, reduced motion, desktop rail) — `make test-ui-browser`
  **255 passed**, `make generate-ui-assets` byte-identical (runtime/htmx unchanged),
  `make check`/`make guard` green. Parity row P54 stays unchecked and catalog stays
  `planned` until the GOTH-5.5 wave audit)
  - depends on: Phase 4 gate; component prerequisites GOTH-3.1, GOTH-4.2,
    GOTH-4.4
- [x] **GOTH-5.5** — close the six-entry composite wave (done 2026-07-17; adversarial
  P49–P54 audit passed with no Phase-5 primitive reopened. Each row driven in the real
  browser against the full gate: locale/date + server-owned grid (P49/P53,
  deterministic July-2026), server-ownership of all state (calendar selection, combobox
  value, table sort/filter/page/selection, sidebar collapse), no-JS/HTMX parity (both
  paths driven and equivalent), history (shareable URLs + **back-button correctness
  driven** — Data Table Grace Hopper → Ada Lovelace → original name-asc on successive
  Back; Date Picker 2026-07-20 → 2026-07-15 on Back), responsive (Data Table
  `table-container` `overflow-x:auto` scrollable at 360px; Sidebar mobile-sheet 480px /
  desktop-rail 1280px), CSP (zero securitypolicyviolation incl. HTMX swap paths), and
  accessibility (grid semantics, aria-activedescendant, aria-sort, aria-current, mobile
  sidebar focus trap — axe green everywhere). **Disposition 1:** the GOTH-5.4 sidebar
  RTL entrance-animation nuance (physical `translateX` transient) was FIXED at source
  (`[dir="rtl"]` `animation-name` overrides so the entrance follows the resolved edge;
  spec asserts computed `animationName == goth-sidebar-in-right`) — recorded here
  WITHOUT unchecking GOTH-5.4, per the GOTH-3.4/4.5 precedent; theme.css `7acbbd55` →
  `a40f4572`, runtime/htmx byte-identical. **Disposition 2:** carried deferrals
  (Calendar PageUp/PageDown, Date Picker HTMX month nav, Data Table row-action menus +
  select-all, Combobox stage-without-round-trip, Sidebar client persistence) — all
  enhancements, none a parity gap. **Disposition 3:** §9 confirmed coherent — typed
  `Trigger`/`SwapModifiers` FROZEN with live consumers; `Vals`/`Include`/`Headers`/
  `DisabledElt`/CSRF still PROVISIONAL for GOTH-7.3 (comment gaps only, not fields);
  markers intact. `make generate` no-op + `make generate-ui-assets` reproducible +
  `make check` + `make guard` + `make test-ui-browser` (**255 passed**, 3 engines) all
  green; Layer-4 screenshots captured/VIEWED; catalog P49–P54 → `accepted`; parity-matrix
  rows P49–P54 checked)
  - depends on: GOTH-5.1 + GOTH-5.2 + GOTH-5.3 + GOTH-5.4

Phase 5 gate: **MET (2026-07-17)** — the GOTH-5.5 wave audit accepted P49–P54 against
the full Phase-5 gate (locale/date, server-ownership, no-JS/HTMX parity with
back-button history, responsive breakpoints, CSP, and accessibility; no primitive
reopened), the three-engine browser/axe/CSP proof is green (255 passed), generation is
reproducible, and `make check`/`make guard` pass. P49–P54 are checked in the parity
matrix and `accepted` in catalog.md. Phase 6 (GOTH-6.1–6.5) is now dependency-ready.

## Phase 6 — specialized, media, and messaging primitives

- [x] **GOTH-6.1** — implement Attachment P55 (done 2026-07-18; P55 Attachment as a
  pure F3 compound-parts primitive with NO new §8 controller (frozen nine-set
  unchanged; StylesOnly, no JS). Parts: Attachment root + AttachmentMedia/Content/
  Title/Description/Actions/Trigger/Group; AttachmentSize (sm/md/lg), Orientation
  (horizontal/vertical), State (idle/uploading/processing/error/done) enums. The
  primitive DISPLAYS caller-owned upload state — the server owns the state/progress;
  it selects no file and owns no route/storage/retries/authorization. Meaning beyond
  color: each non-idle state renders a glyph + text label (per-state default) + a
  determinate native <progress> while uploading; the state hue rides only the glyph +
  border (label stays AA-safe muted — fixed in the axe pass), and the error label is
  the accessible description. AttachmentTrigger (link/button, required Label) stretches
  the card via CSS ::after with no inline style while actions/status sit above it, so
  trigger + each action stay independent tab stops in DOM order. No-JS baseline + a
  REAL multipart no-JS upload round-trip: host-owned <input type=file>/<form
  enctype=multipart> POSTs to /attachment/upload, the SERVER stores + decides state
  (done, or error>512KB with a description) and 303-redirects back, re-rendering from
  the server-owned store. Component CSS (no inline style, reduced-motion collapse),
  render/contract tests, two StylesOnly specimens (static gallery + upload), a 3-engine
  attachment.spec.ts (18 tests); make generate no-op; make generate-ui-assets
  reproducible (theme.css → 541ed3a7; runtime/htmx byte-identical); make test-ui-browser
  **273 passed** (3 engines, up from 255); make check/make guard green. Parity row P55
  stays unchecked and catalog stays `planned` until GOTH-6.6)
  - depends on: Phase 5 gate
- [x] **GOTH-6.2** — implement messaging primitives P56, P59–P60 (done 2026-07-18; all
  three messaging primitives shipped. P56 Bubble + P59 Message are pure F3 compound-parts
  primitives with NO controller (StylesOnly). Bubble: root(data-align)/BubbleContent(seven
  semantic-role variants)/BubbleReactions/BubbleReaction(button-or-link, required accessible
  name + toggled aria-pressed)/BubbleGroup; the muted variant was made a quiet no-fill
  bubble in the axe pass for AA. Message: root(data-align, RTL-safe)/MessageAvatar(composes
  Avatar P03)/MessageContent/MessageHeader/MessageFooter/MessageGroup(grouped-message slots,
  one avatar). P60 Message Scroller is the F4 controller-backed scroller: after the owner
  ratified the recommendation (recorded 2026-07-18 gate-b-review.md addendum),
  **`gothMessageScroller` was ADDED to the frozen §8 set (now TEN controllers)** and named
  in README §8 — a thin controller composing the frozen GOTH-4.1 mechanics + live-region.js
  over a server-rendered role=log transcript. No-JS baseline: readable role=log transcript +
  a real "load earlier" link (full-document reload) + native #id jump anchors + CSS
  overflow-anchor. Enhancement (3-engine proven): live-edge following (stick to bottom at
  the edge, stop on scroll-up), HTMX prepend-without-jump (controller-owned anchoring via a
  beforeSwap snapshot restored on afterSettle, prepend/append detected by child change,
  overflow-anchor:none when enhanced, history trigger above the scroll region), jump-to-
  message with focus preservation, and unread/scroll-state exposure (data-at-edge/data-unread
  + goth:change/goth:select + revealed status count + polite live-region summary off-edge).
  Module render/contract tests (messaging_test.go + message_scroller_test.go); README §8
  updated to ten; make generate no-op; make generate-ui-assets reproducible (theme.css
  d25161d1, runtime.js 2b6cbcd1 [new controller], htmx byte-identical); 3-engine
  messaging.spec.ts (9) + message-scroller.spec.ts (6); make test-ui-browser **318 passed**
  (up from 273); make check/make guard green. Parity rows P56/P59/P60 stay unchecked and
  catalog `planned` until the GOTH-6.6 wave closeout.)
  - depends on: Phase 5 gate
- [x] **GOTH-6.3** — implement spatial primitives P57, P61–P62 (done 2026-07-18; all
  three shipped. **P62 Scroll Area** (native, no controller): a single native overflow
  region (not a synthetic scrollbar), data-orientation axis, focusable+named
  keyboard-scrollable region (tabindex=0/role=group/aria-label, P24/P60 precedent),
  themed scrollbar via scrollbar-width/color + WebKit pseudo-elements. **P57 Carousel**
  (native, no controller): CSS scroll-snap track (focusable keyboard-scrollable region)
  + snap-aligned slides (role=group/aria-roledescription=slide) + no-JS in-page anchor
  dots (CarouselDot Target=slide #id; server-chosen current dot with aria-current) —
  proven no-JS dot navigation + snap. **P61 Resizable** (F4, gothResizable): first
  BLOCKED on an owner decision, then the owner ratified the recommendation (recorded
  2026-07-18 gate-b-review.md addendum) — **`gothResizable` ADDED to the frozen §8 set
  (now ELEVEN controllers)**, README §8 updated. Built on the post-Amendment-1 stack
  (absolute no-server-rendered-style invariant; CSSOM setProperty is the sanctioned
  dynamic-geometry path). Server-owned split baseline: root data-default-size → primary
  pane flex-basis via 19 external CSS `--goth-resize-basis` buckets (no server-rendered
  style=, proven zero [style] on StylesOnly); role=group panes + role=separator handle
  with aria-valuenow/min/max + aria-controls. Enhancement: pointer drag (setPointerCapture)
  + APG keyboard (arrows/Home/End, 5% step, min/max clamp) + RTL-aware direction
  (roving.js precedent), writing --goth-resize-basis via the CSSOM + mirroring
  aria-valuenow. spatial_test.go render/contract tests (incl. resizable baseline/handle
  bounds/enum snap/x-data+bucket merge-ownership); make generate no-op;
  make generate-ui-assets reproducible (runtime.js 2b6cbcd1→80ebc7d6 [new controller,
  expected]; theme.css ed23f5fb; htmx.js 8689e2e2 + theme-default.css ae49d971
  byte-identical); 3-engine spatial.spec.ts (9: scroll-area×2, carousel×2, resizable
  baseline/keyboard+bounds/pointer-drag/vertical/RTL); make test-ui-browser **360 passed**
  (up from 345); make warm-scaffold-cache && make build && make vet && make test &&
  make guard + make check all green. Parity rows P57/P61/P62 stay unchecked and catalog
  `planned` until the GOTH-6.6 wave closeout.)
  - depends on: Phase 5 gate; P61 on the gothResizable §8 addendum + Amendment 1 (GOTH-A.4)
- [x] **GOTH-6.4** — implement Chart P58 (done 2026-07-18; P58 Chart shipped
  native/no-controller as the F4-native precedent — NO new §8 controller, no owner
  decision. The server owns the chart: `computeChartLayout` (pure, unit-tested)
  computes nice-scaled axes + grid + grouped-bar/line/area geometry, and `ChartSVG`
  emits a role=img SVG using ONLY sanctioned presentation/geometry attributes
  (viewBox, x/y/width/height, points, d, cx/cy/r) with series color on `data-series` +
  external CSS over the frozen `--chart-1..5` tokens — ZERO server-rendered `style=`/
  `fill=` proven. Three kinds (bar/line/area); native `<title>` per-mark tooltips;
  F3 frame parts (Chart/ChartHeader/ChartTitle/ChartDescription/ChartContent =
  engine-neutral SVG seam/ChartLegend/ChartLegendItem/ChartTable); accessible tabular
  fallback = `ChartTable` native `<details>` composing Table (P24). One StylesOnly
  `primitive-chart` specimen + `P58` in `ImplementedPrimitives`; module render/geometry
  tests; 3-engine `chart.spec.ts` — `make test-ui-browser` **372 passed** (up from 360),
  assets reproducible (runtime/htmx byte-identical, only theme.css → `701432c3`),
  `make check`/`make guard` green. Parity row P58 stays unchecked and catalog stays
  `planned` until GOTH-6.6.)
  - depends on: Phase 5 gate; component prerequisites GOTH-2.1, GOTH-4.3
- [x] **GOTH-6.5** — implement notification primitives P63–P64 (done 2026-07-18; P64
  Toast + P63 Sonner shipped over ONE gothToast live-region queue — the pre-provisioned
  §8 `gothToast` controller (GOTH-1.4 foundation) REFINED IN PLACE, NO new §8 name and
  NO owner decision. `gothToast` binds the Toaster region (not each toast) and owns the
  whole queue: announce-once through the shared `mechanics/live-region.js` (polite/
  assertive by priority, guarded so it never double-fires — the region is a reachable
  non-trapping `role=region` landmark, NOT itself aria-live, and toasts carry no
  role=status/alert), remaining-time timers that PAUSE on hover (bubbling pointerover —
  the region is click-through)/focus/hidden-page and RESUME, dedupe by key, `data-max`
  overflow (oldest out), keyboard/pointer dismissal + actions, and reduced-motion
  collapse. P64 is the composable shape (Toaster/Toast + Title/Description/Action/Close);
  P63 Sonner is the opinionated facade over the SAME queue (`data-sonner` Toaster +
  single-call `SonnerToast` variants mapping variant→priority with opinionated
  duration/overflow defaults). No inline `style=` anywhere (placement=data-position,
  stacking=data-index, external CSS). `toast.go`/`toast.templ`, `sonner.go`/
  `sonner.templ`, module tests, refined `controllers/toast.js`, component CSS, two Full
  showcase specimens (server-rendered baseline + HTMX-triggered timed/dismissible/variant
  toasts + fragment routes), `P63`/`P64` in `ImplementedPrimitives`; 3-engine
  `toast.spec.ts` (8) + `sonner.spec.ts` (4) — `make test-ui-browser` **408 passed** (up
  from 372), assets reproducible (htmx/theme-default byte-identical; `runtime.js →
  ddc6f2ce`, `theme.css → f2474b2b`), `make check`/`make guard` green. Parity rows
  P63/P64 stay unchecked and catalog stays `planned` until GOTH-6.6.)
  - depends on: Phase 5 gate; shared-mechanics prerequisite GOTH-4.1
- [x] **GOTH-6.6** — close the complete 64-entry parity milestone (done 2026-07-18;
  the wave closeout and the 64-entry milestone record. Ten Phase-6 rows (P55–P64) got a
  full per-row parity check (family shape / no-JS baseline with Alpine absent /
  enhancement / accessibility / CSP / reduced-motion / RTL) — all PASS; the other 54
  rows (P01–P54) got a confirmation sweep proving Phase 6 + Amendment 1 regressed
  nothing (Amendment 1 touched every stylesheet — a sampled Layer-4 look confirms
  earlier specimens still render styled; the §8 registry grew to eleven with no name
  collision or registration failure — the strict-CSP crawl proves a clean console on
  every specimen). **Disposition 1:** the GOTH-6.5 shared-live-region flag (last-wins
  vs per-toast queued announcement) ACCEPTED — last-wins IS the frozen "shared
  live-region runtime" contract with polite/assertive priority, matching upstream
  Sonner; per-toast queued announcement is a recorded enhancement, not a required state.
  **Disposition 2:** every recorded Phase-6 deferral (Carousel prev/next+autoplay,
  Resizable persistence, Chart pie/radial, Message-Scroller SSE transport; plus the
  cross-referenced Phase-3 OTP auto-advance and Phase-4 Drawer drag) confirmed
  enhancement-not-parity per each row's frozen text. **Disposition 3:** no row failed;
  no task reopened; the gate was not softened. `ui/goth` + `examples/goth-showcase`
  build/vet/test green; `make generate` no-op; `make generate-ui-assets` byte-identical
  (runtime.js `ddc6f2ce`, theme.css `f2474b2b`, htmx.js `8689e2e2`, theme-default.css
  `ae49d971`); `make check` + `make guard` green; `make test-ui-browser` **408 passed**
  (3 engines, axe + strict-CSP whole-catalog crawls clean); Layer-4 screenshots of the
  ten Phase-6 specimens (light/dark) + one RTL + two narrow + an earlier-phase
  regression sample captured AND viewed; catalog P55–P64 → `accepted` (all 64 accepted);
  parity-matrix rows P55–P64 checked)
  - depends on: GOTH-6.1 + GOTH-6.2 + GOTH-6.3 + GOTH-6.4 + GOTH-6.5

Phase 6 gate: **MET (2026-07-18)** — the GOTH-6.6 wave closeout accepted P55–P64
against the plan's full parity definition (no primitive reopened), the three-engine
browser/axe/CSP proof is green (408 passed), generation is reproducible
(`make generate-ui-assets` byte-identical), and `make check`/`make guard` pass. All
64 rows (P01–P64) are checked in the parity matrix and `accepted` in catalog.md. **The
64-entry Shadcn-parity milestone is CLOSED.** Phase 7 (GOTH-7.1) is now dependency-ready.

## Amendment 1 — drop Tailwind / host theme stylesheet (GOTH-A)

Ratified by the owner 2026-07-18 (`amendment-1-drop-tailwind-host-stylesheet.md`;
Gate B reopen recorded in `gate-b-review.md`). A scope-reduction executed
**before GOTH-6.3 resumes**: Tailwind is dropped 100% from the kit
toolchain/output, theming flows through a host stylesheet injected after the kit
stylesheet (`Config.ThemeStylesheetPath`, defaulting to the injected
`theme-default.css`), and the entire nonce/dynamic-stylesheet channel
(`Config.Theme`/`Config.Nonce`/`RequiresNonce`/`Bundle.Theme()`/the `theme.Theme`
value machinery) is removed. Phase-1 boxes (GOTH-1.2–1.5) are amended in place,
not unchecked (GOTH-5.5 precedent). Full execution evidence for each task is in
the amendment file.

- [x] **GOTH-A.1** — de-Tailwind the kit CSS build (owned reset + esbuild CSS)
  (done 2026-07-18; Tailwind pin/config removed, `reset.css` authored from
  scratch, `buildCss` → esbuild, assets reproducible, `make test-ui-browser` 330
  passed, `make check`/`make guard` green; `theme.css` still carried the palette
  this task — the split is A.2)
  - depends on: owner ratification of Amendment 1
- [x] **GOTH-A.2** — new Config surface, nonce-channel removal, default-theme
  asset split, showcase rework (done 2026-07-18; `Config = {AssetBasePath,
  Profile, ThemeStylesheetPath}`, the nonce/dynamic-stylesheet channel and Go
  theme value machinery deleted, `theme-default.css` split out (four-asset
  manifest), showcase host-stylesheet specimen + theme.spec.ts; `make
  test-ui-browser` **345 passed** (3 engines), assets reproducible, `make
  check`/`make guard` green)
  - depends on: GOTH-A.1
- [x] **GOTH-A.3** — documentation: stack description, frozen-surface text,
  adopter Tailwind recipe (done 2026-07-18; READMEs/ARCHITECTURE/RELEASING and
  stale comments updated so Tailwind is nowhere in the kit stack and the only
  sanctioned mention is the §5 "using Tailwind in YOUR app" adopter recipe;
  removed-channel `rg` sweeps clean; `make build`/`vet`/`test`/`guard` green)
  - depends on: GOTH-A.2
- [x] **GOTH-A.4** — milestone bookkeeping and amendment closeout (done
  2026-07-18; plan.md invariants 6/9 + toolchain/asset policy + Layer-2 bullet +
  bundle-profile sketch + auth-policy nonce sentence amended; TASKS.md +
  gate-b-review.md addenda recorded; closeout matrix `make check`/`make
  guard`/`make generate` no-op + `make test-ui-browser` 3-engine green; final
  run-and-look screenshot set captured/viewed next to the A.1 baselines)
  - depends on: GOTH-A.3

## Phase 7 — Gopernicus components and first-party adopters

- [x] **GOTH-7.1** — build the first `components/` layer (done 2026-07-18; the
  fourteen named compositions landed as four per-dir packages under
  `ui/goth/components/{layouts,forms,feedback,data}` + an internal `kit` sharing the
  frozen `Base`/`MergeAttributes` grammar — layouts `DocumentShell`/`AppShell`/
  `AuthShell`/`PageHeader`/`ActionBar`, forms `FormField`/`FormSection`/
  `ErrorSummary`/`FormActions`, feedback `EmptyPanel`/`LoadingPanel`/`ErrorPanel`/
  `ConfirmDialog`, data `TableToolbar`. Every one composes primitives ONLY (no new
  primitive/§8 controller/domain type/route), emits zero server-rendered style, and
  inherits server-ownership + no-JS baseline from its primitives; each has a real
  showcase specimen (new `SectionComponent`, `component-*` ids, a
  `TestEveryImplementedComponentHasSpecimen` gate) plus a documented GOTH-7.2/7.3
  adopter need, and ConfirmDialog submits a real server-owned `POST
  /components/confirm`. Per-package render/contract tests + a new 3-engine
  `components.spec.ts`; component CSS added and rebuilt via `make generate-ui-assets`
  (reproducible: theme.css → `b554d930`; runtime/htmx/theme-default byte-identical —
  no controller change); `make test-ui-browser` **429 passed** (up from 408, axe +
  strict-CSP crawls auto-cover the 14 new specimens with zero violations); `make
  warm-scaffold-cache`/`make build`/`make vet`/`make test`/`make guard`/`make check`
  all green)
  - depends on: Phase 6 gate
- [x] **GOTH-7.2** — migrate authentication's view adapter to GOTH
  - depends on: GOTH-0.4 + GOTH-7.1 + Gate C
- [x] **GOTH-7.3** — migrate CMS's view adapter to GOTH and prove the HTMX
  grammar (done 2026-07-18; the sibling module renamed in place
  `features/cms/views/{templ→goth}` [empty `git tag` posture; `go.work` + `Makefile`
  + all three consuming hosts' go.mods repointed, `ui/goth` added], all eighteen
  `Views` methods re-implemented on `ui/goth` [`AppShell`/`PageHeader`/`FormField`/
  `DataTable`/`Table`/`Input`/`NativeSelect`/`Button`/`Badge` + `Bundle.Head()`],
  every `action`/`name`/`enctype`/prefix-href contract preserved, the CMS feature
  core still templ/ui-goth-free [G2]. HTMX proof [the named admin
  entries-table filtering/pagination flow]: the status filter, `created_at` sort
  toggle, and pagination carry explicit `hx-*` [`hx-get`+`hx-target=#cms-entries-content`
  +`hx-swap="outerHTML show:none focus-scroll:false"`+`hx-push-url`; filter form
  +`hx-trigger="change"`] swapping the content region, and the `List` handler returns
  the new `EntriesListContent` fragment on `HX-Request` else the full document — the
  HTMX path degrades to the exact no-JS reload. **CSRF settled hidden-input-only**
  [mutations ride `<form>`; HTMX surface GET-only]. R7 §9 finalization [second/final
  review point]: typed `Trigger`/`SwapModifiers` CONFIRMED FROZEN [CMS is a third
  consumer]; `Vals`/`Include`/`Headers`/`DisabledElt`/typed-URL all **RETIRED** as
  no-demonstrated-need — **nothing provisional after this task**; no `htmx/attributes.go`
  field changed [assets byte-identical], README §9 + gate-b-review.md addendum updated.
  Real-router httptest proof [`examples/minimal/.../goth_htmx_proof_test.go`] + live
  curl journey drove full-doc↔HTMX-fragment↔no-JS parity + shareable filter URLs +
  stylesheet 200 `text/css`. All CMS/theme/auth-cms tests green; `make check`/`make
  guard` green [asset-drift guard clean → `make test-ui-browser` unaffected at **429**])
  - depends on: GOTH-7.1
- [x] **GOTH-7.4** — document adoption, theming, security, and handoff recipes
  (done 2026-07-18; added `ui/goth/README.md` §11 [install/wiring, profiles + asset
  route, the `Requirements`→CSP formatter recipe, the `components/` layer API, the
  "forgot to mount the asset route" failure mode + boot-time `Manifest.Assets()`
  reachability self-check, the `views/goth` adapter + `HTMLPolicy` recipe, the HTMX
  migration trigger + hidden-input CSRF, the `views/templ`→`views/goth` tag posture,
  the SRI + CDN-relocation caveats, and the GPS/Segovia brand-token override + handoff
  — no Segovia code imported; frozen §1–§10 untouched]. Gate-C obligations discharged:
  the self-check + SRI CDN caveat documented; the CSP formatter kept as a host-side
  recipe [proven by test], NOT a new kit helper [would reopen GOTH-0.3/Gate B — recorded
  as a deferred owner decision]. `RELEASING.md` recorded the `Requirements`-surface
  upgrade-note convention; `ui/README.md`/`ARCHITECTURE.md`/`NOTES.md` + the stale
  `views/templ`/`authtempl`/`cmstempl` doc lags across feature/example READMEs +
  `THIRD_PARTY_NOTICES.md` all folded in. New
  `examples/minimal/cmd/server/goth_doc_snippets_test.go` compiles+runs the CSP-formatter
  and asset-reachability recipes [the latter proves the failure mode is caught]; the
  wiring/HTMX/`HTMLPolicy` snippets are proven by the existing 7.2/7.3 proof tests.
  `make warm-scaffold-cache`+`make build`+`make vet`+`make test`+`make guard`+`make check`
  all green; assets byte-identical [`make test-ui-browser` unaffected])
  - depends on: GOTH-7.2 + GOTH-7.3
- [x] **GOTH-7.5** — run the final adversarial, accessibility, and release audit
  (done 2026-07-18; MILESTONE COMPLETE. Independent adversarial/accessibility/
  release audit ran the full gate with **no BLOCKER and no SHOULD-FIX requiring a
  code change**; NO tag/PR created. Adversarial: zero server-rendered `style=`/
  inline `<style>` across every `*_templ.go`/`.templ` in ui/goth + both views/goth
  [CSSOM `setProperty` only]; frozen §8 set intact at exactly eleven controllers;
  boundaries green [G17 ui-no-inward, G18 require-whitelist, G2 feature-isolation;
  both feature cores templ/ui-goth-free]; public surface bounded [64 primitives +
  14 components + 11 controllers, README §1–§11, catalog 64 `accepted`, every
  primitive/component specimen-gated]; CSP proven per host at runtime [showcase
  `style-src 'self'` no unsafe/nonce; auth-cms nonce in `script-src` ONLY,
  `style-src 'self'` — C5/C3 contract-tested across all three profiles].
  Accessibility: `make test-ui-browser` **429 passed** [3 engines, 0 failed/flaky],
  whole-catalog axe crawl + strict-CSP crawl green; every highest-risk claim
  [focus trap/restore, live regions, roving, calendar grid] has a substantive
  proving spec — none missing. Release: `examples/jobs-minimal` is the
  view-technology-free API-only exemplar [sdk+jobs+cron only]; G18 taxonomy green;
  RELEASING.md floors accurate [ui/goth + auth/cms views/goth first tags];
  **review-finding sweep — Gate B R1–R9 + 4 addenda, Gate C C1–C5, Amendment 1,
  all wave-audit deferrals: every accepted finding remediated or
  deferred-with-consent, NO orphans**; supply chain clean [no axe in dist,
  provenance recorded, 4 frozen manifest names, Node pinned]. Layer-4: served
  showcase + minimal-CMS + auth-cms, captured and VIEWED screenshots [auth
  register styled under mapped CSP, CMS admin GOTH DataTable + live HTMX fragment
  round-trip, showcase specimen index] — real behavior confirmed. Three NITs
  recorded [N1 minimal sets no CSP = host choice; N2 minimal is now a GOTH view
  host so jobs-minimal is the API-only exemplar; N3 stateful-host Playwright
  journey is a deferred owner decision]. `make check` [18 guards + drift +
  per-module build/vet/test] exit 0; `make test-ui-browser` exit 0; no source/
  asset file changed [only milestone records]; unrelated worktree state preserved.
  See the plan.md GOTH-7.5 evidence note = the Phase 7 gate statement)
  - depends on: GOTH-7.4

Phase 7 gate: **MET (2026-07-18)** — authentication and CMS prove the real
composition (both render via `ui/goth` under mapped CSP, viewed in-browser),
API-only hosts remain view-technology-free (`examples/jobs-minimal`), documentation
matches the public surface (README §1–§11 vs 64 primitives + 14 components + 11
controllers), all accepted review findings are remediated (Gate B/C + Amendment 1
sweep, no orphans), `make check`/`make guard`/`make test-ui-browser` (429, 3
engines) all green, and NO tag/PR was created — the commit/PR/tag decisions are the
owner's.

## Completion summary

- Numbered tasks: **42** (the original 38 plus Amendment 1's GOTH-A.1–A.4;
  Amendment 1 deletes no task).
- Primitive parity rows: **64**.
- Current completed tasks: **42 / 42 — ALL milestone tasks complete** (GOTH-0.1
  through GOTH-7.5 plus Amendment 1's GOTH-A.1–A.4). GOTH-7.5 (the final
  adversarial/accessibility/release audit) done 2026-07-18: `make check` +
  `make guard` (18) + `make test-ui-browser` (429, 3 engines) green, every accepted
  Gate B/C + Amendment 1 + wave-audit finding remediated (no orphans), Layer-4
  screenshots viewed, and NO tag/PR created — the commit/PR/tag decisions are the
  owner's. The historical per-task narrative below is preserved as-is: **34** (the original 29 plus GOTH-A.1–A.4 done 2026-07-18
  plus GOTH-6.3 done 2026-07-18: P57 Carousel + P62 Scroll Area (native/CSS, no
  controller) + P61 Resizable — the latter on **`gothResizable`, the ELEVENTH frozen §8
  controller**, ratified by the owner in the 2026-07-18 gate-b-review.md addendum after
  the recommendation was recorded; built on the post-Amendment-1 stack (absolute
  no-server-rendered-style invariant, CSSOM setProperty the sanctioned geometry path):
  server-owned split baseline via external CSS `--goth-resize-basis` buckets, enhanced by
  pointer drag + APG keyboard + RTL; runtime.js `2b6cbcd1`→`80ebc7d6` (new controller),
  theme.css `ed23f5fb`, htmx/theme-default byte-identical; 3-engine `spatial.spec.ts` (9),
  `make test-ui-browser` **360 passed**, make check/guard + build/vet/test green; parity
  rows P57/P61/P62 stay unchecked/`planned` until GOTH-6.6. GOTH-6.2 on 2026-07-18 — messaging P56 Bubble + P59
  Message + P60 Message Scroller. Bubble/Message are pure F3 no-controller primitives
  (seven Bubble variants, alignment, groups, interactive reactions; Message aligned
  row with avatar/header/body/footer + grouped-message slots; muted-variant AA fix).
  P60 Message Scroller is the F4 scroller: the owner ratified adding
  **`gothMessageScroller` as the TENTH frozen §8 controller** (recorded 2026-07-18
  gate-b-review.md addendum; README §8 updated), a thin controller composing the
  frozen GOTH-4.1 mechanics + live-region.js over a server-rendered role=log
  transcript. No-JS baseline (readable log + real "load earlier" link + native #id
  jump anchors + overflow-anchor) enhanced with live-edge following, HTMX
  prepend-without-jump, jump-to-message focus, and unread/scroll-state exposure;
  3-engine messaging.spec.ts (9) + message-scroller.spec.ts (6), make test-ui-browser
  **318 passed** (up from 273), assets reproducible (runtime.js 2b6cbcd1, theme.css
  d25161d1, htmx byte-identical), make check/make guard green; P56/P59/P60 stay
  unchecked/`planned` until GOTH-6.6. GOTH-6.1 on 2026-07-18 — P55 Attachment: a pure F3
  compound-parts primitive with NO new §8 controller (StylesOnly, no JS) that DISPLAYS
  caller-owned upload state — the server owns state/progress; it selects no file and
  owns no route/storage/retries/authorization. Media/content/actions/full-card trigger/
  group parts + size/orientation/state enums; meaning beyond color via a state glyph +
  text label + determinate `<progress>` (hue on glyph+border, AA-safe muted label — a
  contrast fix from the axe pass); independently focusable trigger/actions via a CSS
  `::after` stretch (no inline style) in DOM order; a no-JS static gallery + a REAL
  multipart no-JS upload round-trip (host owns the file input/route/storage; the SERVER
  decides done-vs-error>512KB and 303-redirects). 3-engine `attachment.spec.ts` (18
  tests), `make test-ui-browser` 273 passed (up from 255), `make generate-ui-assets`
  reproducible (theme.css `541ed3a7`; runtime/htmx byte-identical), `make check`/`make
  guard` green; P55 stays unchecked and catalog `planned` until GOTH-6.6. GOTH-5.5 on
  2026-07-17 — closed the six-entry
  composite wave: an adversarial P49–P54 audit passed with no Phase-5 primitive
  reopened; every gate dimension was driven in the real browser (locale/date,
  server-ownership, no-JS/HTMX parity, back-button history on the Data Table + Date
  Picker, responsive breakpoints, CSP, accessibility); the GOTH-5.4 sidebar RTL
  entrance-animation nuance was FIXED at source (`[dir="rtl"]` `animation-name`
  overrides; theme.css `7acbbd55` → `a40f4572`, runtime/htmx byte-identical) and
  recorded without unchecking GOTH-5.4; deferrals carried; §9 confirmed coherent;
  3-engine browser suite 255 passed; catalog P49–P54 → `accepted`, parity-matrix rows
  checked; Phase 5 gate MET. GOTH-5.4 on 2026-07-17 — P54 Sidebar: a
  server-owned navigation shell COMPOSING the frozen gothDialog overlay mechanics
  (mobile off-canvas sheet, as Sheet P47) and Collapsible (P29 native `<details>`)
  with NO new §8 controller — runtime.js/htmx.js byte-identical, only theme.css
  changed; server owns desktop-collapsed + mobile-open + active-item, all as server
  round-trips, persistence server-owned/host-namespaced with no client persistence
  surface; one markup renders both breakpoints via CSS; 3-engine browser suite 255
  passed. GOTH-5.3 on 2026-07-17 — P52 Data Table: a
  server-owned sort/filter/page/selection grid COMPOSING Table (P24), Pagination
  (P19), and Checkbox (P28) with a thin new compound surface and no new §8
  controller; a no-JS baseline of real links + a filter form GET (shareable URLs)
  and an HTMX parity that swaps only the content region preserving scroll/focus;
  plus Job 2, the R7 HTMX `Attrs` finalization — a typed `htmx.Trigger` and
  `SwapMods` with real consumers (Combobox async migrated onto the typed Trigger),
  the residual gaps + CSRF posture kept PROVISIONAL for GOTH-7.3; 3-engine browser
  suite 225 passed. GOTH-5.2 on 2026-07-17 — P50 Combobox + P51 Command;
  both backed by the frozen `gothCombobox` refined in place [no new §8 controller]:
  server-owned option data + async filtering/empty-state, an aria-activedescendant
  keyboard loop, a native submit/link no-JS baseline, and an HTMX↔no-JS option-replacement
  parity proof; 3-engine browser suite 204 passed. GOTH-5.1 on 2026-07-17 — P49 Calendar
  + P53 Date Picker; server-owned date math/locale, no-JS form-submit baseline, APG grid
  keyboard via an in-place `gothRovingFocus` grid-mode refinement with no new §8 controller,
  and a no-JS↔HTMX Date Picker selection parity proof).
  GOTH-0.1, GOTH-0.2, GOTH-0.3, GOTH-0.4 on
  2026-07-17; GOTH-1.1, GOTH-1.2, GOTH-1.3, GOTH-1.4, GOTH-1.5 on 2026-07-17; GOTH-2.1,
  GOTH-2.2, GOTH-2.3, GOTH-2.4 on 2026-07-17; GOTH-3.1, GOTH-3.2, GOTH-3.3, GOTH-3.4 on
  2026-07-17; GOTH-4.1, GOTH-4.2, GOTH-4.3, GOTH-4.4, GOTH-4.5 on 2026-07-17). Phase 0,
  Phase 1, Phase 2, Phase 3, and Phase 4 complete; parity rows P01–P48 accepted. The
  GOTH-3.4 wave audit accepted P27–P36 (no primitive reopened), fixed the Tabs RTL
  arrow-direction at source, and recorded the F4-native family rationale. GOTH-4.1 froze
  the shared overlay/menu mechanics (Phase 4 opened) and fixed the three latent
  `this.$el` controller defects at source. GOTH-4.2 built the 4 modal/panel primitives
  (P37/P39/P40/P47) by COMPOSING those mechanics (div-based overlay reused over native
  `<dialog>`; no fork, no new §8 controller name). GOTH-4.3 built the 4 anchored
  info/selection primitives (P42/P45/P46/P48): the frozen §8 controller set was reopened
  by owner decision to add `gothTooltip`/`gothHoverCard` (composing the frozen mechanics,
  no fork; recorded in `gate-b-review.md`), while Popover rode native `popover` and
  Select rode a native `<select>` (no controller). GOTH-4.4 built the 4 menu primitives
  (P38/P41/P43/P44) with NO new §8 controller name — Dropdown/Context/Menubar refine the
  existing `gothMenu` in place and Navigation Menu is native + link-first. GOTH-4.5 closed
  the wave: an adversarial P37–P48 audit passed with no primitive reopened; the flagged
  GOTH-4.1 menu-anchor flake was fixed at source in the tests (`expect.poll` the settled
  CSSOM anchor value — the race was the read-once test assumption, not `anchor.js`);
  server-owned checkbox/radio menu items (invariant 1) and per-primitive prefixed part
  sets (frozen Shadcn-prefix grammar) recorded as KEPT; deferred enhancements recorded.
  Parity rows P37–P48 are now checked and `accepted` in catalog.md. GOTH-5.5 closed the
  Phase-5 wave: an adversarial P49–P54 audit passed with no primitive reopened; the
  GOTH-5.4 sidebar RTL entrance-animation nuance (physical `translateX` transient) was
  FIXED at source (`[dir="rtl"]` `animation-name` overrides; theme.css `7acbbd55` →
  `a40f4572`, runtime/htmx byte-identical) and recorded without unchecking GOTH-5.4;
  back-button history was driven on the Data Table + Date Picker, the responsive scroll
  wrapper and Sidebar breakpoints were driven, the §9 typed `Trigger`/`SwapModifiers`
  freeze was confirmed coherent with the residual set provisional for 7.3. Parity rows
  P49–P54 are now checked and `accepted` in catalog.md.
- Gate A: ratified. Gate B: **accepted by owner 2026-07-17 conditional on remediation
  (applied — see `gate-b-review.md`); reopened + re-frozen 2026-07-18 for Amendment 1
  (drop Tailwind / host theme stylesheet; R3 nonce-coherence resolution and R5
  partial-theme-composition freeze superseded — see the gate-b-review.md addendum)**.
  Gate C: **accepted by owner 2026-07-17 conditional on remediation (applied — see
  `gate-c-review.md`)** (auth's own script nonce is unaffected by Amendment 1).
- Current dependency-ready tasks: **Phase 6 COMPLETE — the 64-entry milestone is
  CLOSED (GOTH-6.6, 2026-07-18).** GOTH-6.1 (Attachment P55), GOTH-6.2 (messaging
  P56/P59/P60), GOTH-6.3 (spatial P57/P61/P62), GOTH-6.4 (Chart P58), GOTH-6.5
  (notifications P63/P64), and the GOTH-6.6 wave closeout are all done. GOTH-6.2's P60
  unblocked with **`gothMessageScroller` (tenth §8 controller)** and GOTH-6.3's P61 with
  **`gothResizable` (eleventh §8 controller)**, both ratified by owner reopens recorded
  in the gate-b-review.md addenda (README §8 lists eleven); GOTH-6.4 (Chart) and GOTH-6.5
  (Toast/Sonner over the pre-provisioned `gothToast` queue) needed no new controller.
  Amendment 1 (GOTH-A.1–A.4) executed and closed 2026-07-18. GOTH-6.6 audited P55–P64
  per-row (all PASS, no reopen), swept P01–P54 for regressions (none), recorded the three
  dispositions (6.5 last-wins live-region ACCEPTED as the frozen shared-runtime contract;
  all Phase-6 deferrals confirmed enhancement-not-parity; no row failed), and passed the
  full matrix (both modules build/vet/test, `make generate` no-op, `make
  generate-ui-assets` byte-identical, `make check`/`make guard`, `make test-ui-browser`
  **408 passed** 3 engines) with Layer-4 screenshots viewed. Catalog P01–P64 all
  `accepted`; parity matrix fully checked. **GOTH-7.1 DONE (2026-07-18)** — the first
  `components/` layer (the fourteen named domain-neutral compositions under
  `ui/goth/components/{layouts,forms,feedback,data}`, primitives-only, no new
  controller/route/domain type; render/contract tests + a 3-engine
  `components.spec.ts`; `make test-ui-browser` **429 passed**; component CSS rebuilt
  reproducibly, theme.css → `b554d930`, runtime/htmx byte-identical; `make check`/
  `make guard` green). **GOTH-7.2 DONE (2026-07-18)** — authentication's view adapter
  migrated to GOTH: at preflight's still-empty `git tag` posture the sibling module
  was renamed in place `features/authentication/views/{templ→goth}` (package `goth`,
  go.work + Makefile MODULES/generate + auth-cms go.mod updated), and every one of the
  sixteen port methods re-implemented on `ui/goth` (AuthShell/FormField/ErrorSummary/
  FormActions + Input/Button/NativeSelect primitives, fingerprinted assets via
  `Bundle.Head()`). `Views.HTMLPolicy()` maps `goth.Bundle.Requirements()` → the
  feature `HTMLResourcePolicy` with deterministic source ordering (C3) and a `script-src
  'self' + Nonce:true` for the externalized fragment reader (C5 contract test asserts
  it across all three profiles). The bespoke inline reset/magic-link fragment readers
  were **externalized** into a served `fragment.js` (`FragmentScriptHandler`) so the
  pages run with no inline script; auth-cms wires the bundle, serves the assets +
  fragment route, and sets `Config.HTMLPolicy` off the promoted adapter. Real-router
  HTTP proof (new `goth_proof_test.go`) + a live-server curl journey confirmed
  login/register/reset/passwordless/magic render styled via GOTH under the mapped CSP
  (`default-src 'none' … script-src 'self' 'nonce-…'; style-src 'self'`), the stylesheet
  + fragment.js serve, the JSON API is unchanged, and the nil-Views asset-free posture
  holds. All existing auth + auth-cms tests stay green; `make check`/`make guard` green
  (18 guards incl. G5 feature-core-no-templ + ui/* guards); `make test-ui-browser`
  unaffected at **429** (no ui/goth asset change). **Next ready: GOTH-7.3** (CMS view
  adapter → GOTH + HTMX grammar).
- Deferred work such as `ui/react`, `ui/vue`, `gopernicus ui add`, automated
  upstream catalog diffs, and HTMX 4 adoption is not represented as executable
  work on this board.
