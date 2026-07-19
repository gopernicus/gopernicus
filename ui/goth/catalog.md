# `ui/goth` — Shadcn parity catalog (64-entry frozen baseline)

Status: **COMPLETE — all 64 parity rows `accepted`. Baseline recorded at GOTH-0.1
(2026-07-17); API-family mapping frozen at GOTH-0.3 (2026-07-17); Phase 2 primitives
P01–P26 accepted at the GOTH-2.4 wave audit (2026-07-17); Phase 3 primitives P27–P36
at GOTH-3.4 (2026-07-17); Phase 4 primitives P37–P48 at GOTH-4.5 (2026-07-17); Phase 5
primitives P49–P54 at GOTH-5.5 (2026-07-17); Phase 6 primitives P55–P64 accepted at the
GOTH-6.6 wave closeout (2026-07-18) — the 64-entry milestone is CLOSED.** This file is
the parity-status projection for the `ui/goth` milestone. From GOTH-1.1 forward it is
regenerated/checked against the primitive sources, showcase registry, and test
classifications; at GOTH-0.1 it froze the exact 64 catalog IDs, and GOTH-0.3 added the
frozen API-family column mapping every entry to the grammar in [`README.md`](README.md).
All six phases (P01–P64) are now `accepted`.

`plan.md` (`.claude/plans/ui-goth/plan.md`) is the authority for scope, parity
definition, per-entry acceptance emphasis, and verification. This table adds no
entry silently: updating Shadcn later opens a separately reviewed parity task, it
does not extend this milestone.

## API family mapping (frozen at GOTH-0.3, 2026-07-17)

GOTH-0.3 froze the public grammar in [`README.md`](README.md). Every P01–P64
entry maps to exactly one of four API families, so no entry has an unresolved API
family. The `family` column below records that mapping.

- **F1 — leaf props primitive:** one exported function whose content is a single
  inline/scalar value (or none), with no named content roles and no sub-parts.
- **F2 — slotted props primitive:** ONE exported function arranging a principal
  `{ children... }` region plus optional auxiliary `templ.Component` slot fields
  inside a fixed layout the primitive owns.
- **F3 — compound-parts primitive:** prefix-named functions the CALLER composes,
  sharing the `data-slot`/`data-state`/`data-variant` contract.
- **F4 — controller-backed primitive:** an F3 family plus a named Alpine
  controller and/or an explicit HTMX seam. **F4 marks the family whose parity
  MAY need a controller, not a mandate to bind one.** Where a native element is a
  fully-sufficient baseline (native radios, `<input type="range">`, grouped OTP
  inputs, native-`<details>` exclusivity), the F4 row is satisfied with no
  controller bound; a controller is added only where enhancement is genuinely
  required (e.g. `gothTabs` roving/activation, `gothRovingFocus` for a
  multiple-select toggle group's single tab stop). Recorded at the GOTH-3.4 wave
  audit (2026-07-17) for P30/P31/P32/P36; the frozen family column is unchanged.

**Decision rule (frozen, mirrors `README.md` §7).** F1 vs F2: F1 when content is
one inline/scalar value (or none) with no named roles; F2 when a single function
arranges a principal children region and/or named content roles it owns. F2 vs
F3: F2 when the arrangement is fixed and owned by the one function; F3 when the
caller composes and places multiple named part functions itself. This rule was
re-verified at Gate B remediation (2026-07-17) against Alert (P01, F2), Badge
(P04, F1), Card (P08, F3), Empty (P10, F2), Item (P14, F3), and Marker (P17, F2);
no reclassification was demanded and the counts below are unchanged.

All four families share the frozen `primitives.Base` (ID/Class/Attributes),
typed variant/size enums, `templ.Component` slots, the `URL` type for URL-bearing
props, `IDFactory` for interactive ids, and `MergeAttributes` merge order.
Family counts: F1 = 11, F2 = 6, F3 = 14, F4 = 33 (11 + 6 + 14 + 33 = 64).

## Provenance

- Catalog source: the official Shadcn "All Components" catalog at
  <https://ui.shadcn.com/docs/components>, captured 2026-07-17.
- Upstream code repository for later ports: `shadcn-ui/ui`
  (<https://github.com/shadcn-ui/ui>), MIT-licensed. Revision pinning, license
  inventory, and dependency provenance are recorded in
  [`assets/THIRD_PARTY_NOTICES.md`](assets/THIRD_PARTY_NOTICES.md).
- Catalog **names and IDs (P01–P64) are frozen by the plan** and owner-ratified
  (2026-07-17). Upstream **code** is not copied at GOTH-0.1; per-entry source
  provenance is recorded when an entry is actually ported (GOTH-0.3+).

## Status key

- `planned` — in the frozen baseline, not yet implemented.
- `accepted` — the entry's exact plan verification passed and its parity was
  accepted at its wave closeout. No entry is `accepted` until then.

## Count invariant

Exactly **64** entries. Every ID `P01`–`P64` appears exactly once. The
per-phase subtotals are 26 + 10 + 12 + 6 + 10 = 64.

## Phase 2 — presentational and native/form foundations (26)

| ID | component | upstream doc slug | task | family | status |
|---|---|---|---|---|---|
| P01 | Alert | alert | GOTH-2.1 | F2 | accepted |
| P02 | Aspect Ratio | aspect-ratio | GOTH-2.1 | F2 | accepted |
| P03 | Avatar | avatar | GOTH-2.1 | F3 | accepted |
| P04 | Badge | badge | GOTH-2.1 | F1 | accepted |
| P05 | Breadcrumb | breadcrumb | GOTH-2.2 | F3 | accepted |
| P06 | Button | button | GOTH-2.2 | F2 | accepted |
| P07 | Button Group | button-group | GOTH-2.2 | F3 | accepted |
| P08 | Card | card | GOTH-2.1 | F3 | accepted |
| P09 | Direction | direction | GOTH-2.2 | F1 | accepted |
| P10 | Empty | empty | GOTH-2.1 | F2 | accepted |
| P11 | Field | field | GOTH-2.3 | F3 | accepted |
| P12 | Input | input | GOTH-2.3 | F1 | accepted |
| P13 | Input Group | input-group | GOTH-2.3 | F3 | accepted |
| P14 | Item | item | GOTH-2.1 | F3 | accepted |
| P15 | Kbd | kbd | GOTH-2.1 | F3 | accepted |
| P16 | Label | label | GOTH-2.3 | F1 | accepted |
| P17 | Marker | marker | GOTH-2.1 | F2 | accepted |
| P18 | Native Select | native-select | GOTH-2.3 | F3 | accepted |
| P19 | Pagination | pagination | GOTH-2.2 | F3 | accepted |
| P20 | Progress | progress | GOTH-2.3 | F1 | accepted |
| P21 | Separator | separator | GOTH-2.2 | F1 | accepted |
| P22 | Skeleton | skeleton | GOTH-2.1 | F1 | accepted |
| P23 | Spinner | spinner | GOTH-2.1 | F1 | accepted |
| P24 | Table | table | GOTH-2.3 | F3 | accepted |
| P25 | Textarea | textarea | GOTH-2.3 | F1 | accepted |
| P26 | Typography | typography | GOTH-2.1 | F2 | accepted |

## Phase 3 — disclosure and selection behavior (10)

| ID | component | upstream doc slug | task | family | status |
|---|---|---|---|---|---|
| P27 | Accordion | accordion | GOTH-3.1 | F4 | accepted |
| P28 | Checkbox | checkbox | GOTH-3.2 | F1 | accepted |
| P29 | Collapsible | collapsible | GOTH-3.1 | F4 | accepted |
| P30 | Input OTP | input-otp | GOTH-3.3 | F4 | accepted |
| P31 | Radio Group | radio-group | GOTH-3.2 | F4 | accepted |
| P32 | Slider | slider | GOTH-3.2 | F4 | accepted |
| P33 | Switch | switch | GOTH-3.2 | F1 | accepted |
| P34 | Tabs | tabs | GOTH-3.1 | F4 | accepted |
| P35 | Toggle | toggle | GOTH-3.3 | F4 | accepted |
| P36 | Toggle Group | toggle-group | GOTH-3.3 | F4 | accepted |

## Phase 4 — overlays and navigation (12)

| ID | component | upstream doc slug | task | family | status |
|---|---|---|---|---|---|
| P37 | Alert Dialog | alert-dialog | GOTH-4.2 | F4 | accepted |
| P38 | Context Menu | context-menu | GOTH-4.4 | F4 | accepted |
| P39 | Dialog | dialog | GOTH-4.2 | F4 | accepted |
| P40 | Drawer | drawer | GOTH-4.2 | F4 | accepted |
| P41 | Dropdown Menu | dropdown-menu | GOTH-4.4 | F4 | accepted |
| P42 | Hover Card | hover-card | GOTH-4.3 | F4 | accepted |
| P43 | Menubar | menubar | GOTH-4.4 | F4 | accepted |
| P44 | Navigation Menu | navigation-menu | GOTH-4.4 | F4 | accepted |
| P45 | Popover | popover | GOTH-4.3 | F4 | accepted |
| P46 | Select | select | GOTH-4.3 | F4 | accepted |
| P47 | Sheet | sheet | GOTH-4.2 | F4 | accepted |
| P48 | Tooltip | tooltip | GOTH-4.3 | F4 | accepted |

## Phase 5 — composite data/time/application primitives (6)

| ID | component | upstream doc slug | task | family | status |
|---|---|---|---|---|---|
| P49 | Calendar | calendar | GOTH-5.1 | F4 | accepted |
| P50 | Combobox | combobox | GOTH-5.2 | F4 | accepted |
| P51 | Command | command | GOTH-5.2 | F4 | accepted |
| P52 | Data Table | data-table | GOTH-5.3 | F4 | accepted |
| P53 | Date Picker | date-picker | GOTH-5.1 | F4 | accepted |
| P54 | Sidebar | sidebar | GOTH-5.4 | F4 | accepted |

## Phase 6 — specialized, media, and messaging primitives (10)

| ID | component | upstream doc slug | task | family | status |
|---|---|---|---|---|---|
| P55 | Attachment | attachment | GOTH-6.1 | F3 | accepted |
| P56 | Bubble | bubble | GOTH-6.2 | F3 | accepted |
| P57 | Carousel | carousel | GOTH-6.3 | F4 | accepted |
| P58 | Chart | chart | GOTH-6.4 | F4 | accepted |
| P59 | Message | message | GOTH-6.2 | F3 | accepted |
| P60 | Message Scroller | message-scroller | GOTH-6.2 | F4 | accepted |
| P61 | Resizable | resizable | GOTH-6.3 | F4 | accepted |
| P62 | Scroll Area | scroll-area | GOTH-6.3 | F4 | accepted |
| P63 | Sonner | sonner | GOTH-6.5 | F4 | accepted |
| P64 | Toast | toast | GOTH-6.5 | F4 | accepted |

Upstream doc slugs point at `https://ui.shadcn.com/docs/components/<slug>` as of
the 2026-07-17 capture. A slug is a provenance pointer, not a promise of
line-by-line translation; parity is behavioral per the plan's parity definition.
