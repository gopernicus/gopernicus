# `ui/` — the Gopernicus UI implementation family

Status: **FOUNDATION BASELINE — established at GOTH-0.1 (2026-07-17).** This
directory is a **technology family**, not a single module and not a claim that
every implementation must use Go packaging. Its first and only member today is
[`goth/`](goth/) (templ + plain CSS + Alpine CSP + optional HTMX). Later `react/` and
`vue/` implementations are legitimate future members.

Authority for scope, contracts, and verification is
`.claude/plans/ui-goth/plan.md`. The exact primitive API, attribute/ID grammar,
bundle-profile API, and semantic token contract were **frozen in GOTH-0.3
(2026-07-17)** in [`goth/README.md`](goth/README.md), pending Gate B review; this
file is the family charter and the home of the cross-implementation vocabulary
those freezes fill in.

```text
ui/
  README.md          shared design vocabulary and parity rules (this file)
  goth/              templ + plain CSS + Alpine CSP + optional HTMX (first member)
  react/             possible later implementation
  vue/               possible later implementation
```

## What a UI implementation is

A **UI implementation** is a reusable presentation system for one
rendering/runtime family. It owns its view-library dependencies, semantic
tokens, components, interaction controllers, and distributable assets; it owns no
domain schema and no routes. This is the **seventh module kind** in the
repository taxonomy: the ratified **UI implementation** row in `ARCHITECTURE.md`
(amended, with `README.md`/`RELEASING.md`, in GOTH-0.2) defines it, and guard
**G17** enforces its dependency direction. `ui/` still carries only documentation
— no Go module is placed here before GOTH-1.1.

Dependency rules (enforced by guard **G17**; see the UI implementation row in
`ARCHITECTURE.md`):

- a UI implementation may depend on its own presentation libraries;
- it must **not** import a feature, integration, example, or Workshop package;
- feature view adapters (`features/<name>/views/goth`) may depend on their
  feature core, `sdk`, and `ui/goth`;
- a host wires assets and route registration; the UI implementation never
  registers routes, installs middleware, or writes HTTP response headers.

```text
feature core  <--- feature views/goth adapter ---> ui/goth ---> templ/runtime
      ^                       ^                        ^
      |                       |                        |
      +---------------------- host -------------------+
                              |
                              +-- sdk web static serving / route registration
```

## Relationship to app-local `internal/inbound/views/`

`ARCHITECTURE.md` reserves an app-local `internal/inbound/views/` tree as a
host's private theme/UI root. `ui/` is the **reusable, importable** counterpart:
a host consumes `ui/goth` (and feature view adapters) rather than growing a
bespoke kit in `internal/inbound/views/`. The precise wording of how the two
relate is the ratified UI implementation section of `ARCHITECTURE.md` (settled in
GOTH-0.2).

## Adopting a UI implementation

A host adopts `ui/goth` by wiring a bundle, serving its embedded assets, and
rendering pages through it (directly or through a feature `views/goth` adapter).
The complete adopter guide — install/wiring, profiles, the CSP recipe, the
`components/` layer, custom feature `Views`, the HTMX migration trigger, module
tags, the SRI/CDN caveats, and the brand-token override + Segovia/GPS360 handoff —
is [`goth/README.md`](goth/README.md) §11. The three reference wirings are
`examples/{cms,minimal,auth-cms}`; the two feature adapters are
`features/authentication/views/goth` and `features/cms/views/goth`.

## Cross-implementation semantic token vocabulary

The semantic **role names** below are the cross-implementation contract:
`ui/goth` provides their CSS implementation and neutral/default values (GOTH-1.3);
a later `ui/react`/`ui/vue` implementation reuses the same role names so themes
generalize. The exact frozen token list — names, light/dark slots, and fallback
rules — was ratified in GOTH-0.3; the authoritative frozen `--<token>` set and
its light/`.dark`/`[data-theme]` and neutral-fallback rules live in
[`goth/README.md`](goth/README.md) §5. Baseline roles:

- background/foreground, card, popover;
- primary, secondary, muted, accent, destructive, and their foregrounds;
- border, input, ring;
- chart 1–5;
- sidebar surface/foreground/primary/accent/border/ring;
- success, warning, and an optional tertiary role;
- radius, shadows, typography families, motion durations/easing, density, and
  named z-index layers.

Brand values (e.g. a Segovia/GPS theme) are proof/override inputs, not part of
the contract: the **role names** generalize, specific values do not. No Segovia
or GPS360 code is imported into this repository.

## Parity rules

The parity baseline is the dated Shadcn "All Components" catalog — exactly 64
entries, `P01`–`P64`, captured 2026-07-17. The frozen mapping and provenance live
in [`goth/catalog.md`](goth/catalog.md) and
[`goth/assets/THIRD_PARTY_NOTICES.md`](goth/assets/THIRD_PARTY_NOTICES.md).

Parity is **behavioral, not textual**: a primitive is complete only when it
provides the same recognizable purpose and interaction as its catalog entry
while remaining honest about the server-rendered platform (semantic HTML, a
useful no-JavaScript state, keyboard/focus/ARIA/RTL/reduced-motion/dark behavior,
showcase specimens, render + real-browser tests, and an explicit CSP/runtime
requirement). Updating Shadcn upstream later opens a separately reviewed parity
task; it never adds an entry to this milestone silently.
