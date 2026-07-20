# `ui/goth` — frozen public contracts (GOTH-0.3)

Status: **SHIPPED — the complete compiled module lives in this directory
(ui-goth milestone, 2026-07-18); the contracts below remain FROZEN (GOTH-0.3,
ratified Gate B).** Ground truth for the Go surface is `goth.go`; the tree
carries the `.templ` sources with their generated twins, the fingerprinted
asset bundle, the primitives/components/controllers, and the theme. This file
is the frozen public contract: it fixes the exact public grammar for props,
slots, attributes, IDs, data hooks, bundle profiles, the asset manifest,
browser requirements, theme tokens, Alpine controller rules, and explicit HTMX
attributes.

The GOTH stack is **templ + plain CSS + Alpine CSP + optional HTMX**: the "T" is
templ. Tailwind is **not** part of the kit's build, toolchain, or emitted output —
component styles are hand-written plain CSS consuming the token custom properties.
A host that wants Tailwind runs it in its own build against the stable `.goth-*`
surface, as the documented adopter option in §5 only.

Authority for scope and verification is `.claude/plans/ui-goth/plan.md`. The
cross-implementation family charter and semantic-role vocabulary are in
[`../README.md`](../README.md). The frozen 64-entry parity mapping and the API
family each entry uses are in [`catalog.md`](catalog.md).

This document uses Go signatures to fix names and shapes precisely. They are the
**frozen shipped surface** — the module implements exactly these names, and a
change reopens the contract under RELEASING.md's `ui/goth` release discipline.
Every public type below states its **zero value**,
its **error behavior**, and its **ownership** (who constructs it, who may mutate
it, who must not).

## Contents

1. Package and import layout
2. Bundle, `Config`, and profiles
3. Manifest and asset access
4. Browser resource requirements
5. Theme token contract
6. Document composition
7. Primitive props / slots / attributes / IDs / data hooks
8. Alpine controller rules
9. Explicit HTMX attribute grammar
10. Ownership summary and invariants
11. Adoption, theming, security, and handoff recipes

---

## 1. Package and import layout

| import path | package | purpose |
|---|---|---|
| `.../ui/goth` | `goth` | `Bundle`, `Config`, `Profile`, `Manifest`, `Requirements`, document components |
| `.../ui/goth/theme` | `theme` | semantic token names (values are CSS-only) and document-attribute helpers |
| `.../ui/goth/primitives` | `primitives` | all 64 catalog primitives in ONE package (Shadcn-style compound prefixes) |
| `.../ui/goth/components/{forms,layouts,feedback,data}` | per-dir | opinionated domain-neutral compositions (Phase 7) |
| `.../ui/goth/htmx` | `htmx` | typed explicit `hx-*` attribute helpers and response-header interpretation |
| `.../ui/goth/icons` | `icons` | small internal control-icon set only (check/chevron/close/spinner) |
| `.../ui/goth/assets` | `assets` | `go:embed` `dist/` + `manifest.json`, `fs.FS`/manifest access |

Rules frozen here:

- **One `primitives` package** for all 64 entries — the repository does not
  acquire 64 tiny packages. Compound parts use the Shadcn-style prefix
  (`Dialog`, `DialogTrigger`, `DialogContent`, `DialogTitle`).
- `goth`, `theme`, `primitives`, `htmx`, `icons`, `assets` depend only on templ
  plus their pinned runtime inputs; they never import a feature, integration,
  example, or workshop package (guard G17). **The frozen surface below imports no
  `sdk` package.** The module taxonomy (ARCHITECTURE.md, guard G17) *permits* a
  UI implementation to require `sdk`, but the frozen GOTH-0.3 grammar does not,
  and any later `sdk`-importing addition is new public surface that reopens
  GOTH-0.3 and re-enters Gate B.
- Nothing in `ui/goth` calls `http.Handle`, registers a route, installs
  middleware, accepts a `feature.Mount`, or writes an HTTP response header. It
  exposes assets, renderers, and requirements; the host composes them.

---

## 2. Bundle, `Config`, and profiles

```go
package goth

// Profile selects which self-hosted asset classes a Bundle serves and requires.
type Profile uint8

const (
	// StylesOnly is the zero value: compiled CSS only. Chosen deliberately so a
	// zero Config yields the safest, smallest, no-JavaScript bundle rather than
	// an accidental full runtime.
	StylesOnly Profile = iota
	// Interactive adds the Alpine CSP build + GOTH controllers.
	Interactive
	// Full adds HTMX 2.0.10 + the Gopernicus HTMX response configuration.
	Full
)

// Config is the value passed to New. Its zero value is valid and yields a
// StylesOnly bundle mounted at the default asset base path with the kit's
// embedded default theme injected.
type Config struct {
	// AssetBasePath is the public URL prefix the host will serve the embedded
	// asset FS under. Empty means DefaultAssetBasePath ("/assets/goth"). It is
	// normalized to a leading slash and no trailing slash; a value containing a
	// scheme, host, "..", control characters, or a query/fragment is a
	// construction error.
	AssetBasePath string

	// Profile selects the asset set. Zero value StylesOnly.
	Profile Profile

	// ThemeStylesheetPath is the root-relative public path of the HOST's theme
	// stylesheet. Head() emits it as a <link rel="stylesheet"> AFTER the kit
	// stylesheet (source-order cascade: the host wins). Empty selects the kit's
	// embedded compiled default theme (theme-default.css) — the WordPress model's
	// fallback. A non-empty value must begin with exactly one "/" and is emitted
	// verbatim with NO integrity attribute (the kit cannot know host bytes). A
	// scheme, authority/host form ("//"), any additional leading slash, backslash,
	// "..", control character, query, or fragment is a construction error.
	// Root-relative-only is deliberately conservative: widening to absolute URLs
	// later is compatible; narrowing is not.
	ThemeStylesheetPath string
}

// Bundle is the constructed, immutable presentation bundle. Returned by value
// intent is a pointer receiver on methods but Bundle itself is safe to share
// across goroutines after New returns; it holds no request state.
type Bundle struct { /* unexported */ }

// New validates cfg and returns a Bundle. It returns a non-nil error for an
// invalid AssetBasePath, an invalid ThemeStylesheetPath, an unknown Profile, or a
// malformed embedded manifest. It never returns a partially built Bundle alongside
// an error.
func New(cfg Config) (*Bundle, error)

func (b *Bundle) Profile() Profile
func (b *Bundle) AssetBasePath() string
func (b *Bundle) Manifest() Manifest
func (b *Bundle) Requirements() Requirements

// Head returns the templ component that emits, for the selected profile: the
// fingerprinted kit stylesheet link, then the theme-stylesheet link (the
// configured host path emitted verbatim with no integrity, or the kit's embedded
// default theme with integrity + crossorigin), then — for Interactive/Full — the
// deferred, SRI-guarded script tag(s), each kit asset pointing at fingerprinted
// manifest URLs under AssetBasePath. It emits NO server-rendered style attribute
// and NO inline <style> element under any configuration. It never writes an HTTP
// header.
func (b *Bundle) Head() templ.Component
```

### Ownership and rules

| type / field | zero value | error behavior | ownership |
|---|---|---|---|
| `Profile` | `StylesOnly` | unknown value → `New` error | set by host in `Config` |
| `Config` | valid; StylesOnly + default path + injected default theme | invalid `AssetBasePath`/`ThemeStylesheetPath` → `New` error | constructed by host |
| `Config.AssetBasePath` | `""` → `DefaultAssetBasePath` | scheme/host/`..`/control/query → error | host chooses; only the host serves the route |
| `Config.ThemeStylesheetPath` | `""` → kit's embedded `theme-default.css` injected | non-`/`-leading / `//` / extra slash / backslash / scheme / host / `..` / control / query / fragment → error | host supplies + serves the file; emitted verbatim, no integrity |
| `Bundle` | N/A (constructed only via `New`) | — | immutable; shared read-only; never mutated after `New` |

`New` is the only constructor. There is no exported mutator on `Bundle`; a host
that wants a different profile/theme/path builds another `Bundle`.
`DefaultAssetBasePath` is an exported package-level value; theming is CSS-only, so
there is no exported Go theme value (a host overrides tokens through its own
stylesheet — see §5).

Profiles are additive supersets: `Interactive` ⊇ `StylesOnly`,
`Full` ⊇ `Interactive`. A `Bundle` serves and requires exactly its profile's
asset classes — never more (proved by the requirements test in GOTH-1.4).

---

## 3. Manifest and asset access

The manifest carries exactly four fingerprinted logical assets: `theme.css` (owned
reset + neutral contract fallbacks + component CSS + `.goth-sr-only`),
`theme-default.css` (the compiled default light/dark palette, injected only when
the host supplies no `ThemeStylesheetPath`), `runtime.js` (Alpine CSP + GOTH
controllers), and `htmx.js`. `StylesOnly` serves `theme.css` (+ the theme link),
`Interactive` adds `runtime.js`, `Full` adds `htmx.js`.

```go
package goth

// Asset is one fingerprinted output.
type Asset struct {
	// LogicalName is the stable key. The frozen logical-name set is the four
	// assets "theme.css", "theme-default.css", "runtime.js", and "htmx.js".
	LogicalName string
	// Path is the fingerprinted path relative to the asset FS root, which keeps
	// the dist/ segment ("dist/theme.4f3a1c.css"). Join it to the bundle
	// AssetBasePath for a URL: AssetBasePath + "/" + Path (e.g.
	// "/assets/goth/dist/theme.4f3a1c.css"). The retained dist/ prefix is what
	// makes WithAssetPrefix("dist/") match and apply immutable caching.
	Path string
	// Integrity is the "sha384-..." Subresource Integrity value for the bytes.
	Integrity string
	// Bytes is the uncompressed size, for preload hints and diagnostics.
	Bytes int64
}

// Manifest maps logical names to fingerprinted assets. Parsed once from the
// embedded manifest.json at New; immutable thereafter.
type Manifest struct { /* unexported */ }

// Lookup returns the asset for a logical name. ok is false for an unknown name;
// it never panics and never returns a zero Asset as if it were real.
func (m Manifest) Lookup(logical string) (Asset, bool)

// Assets returns all assets in a deterministic (logical-name-sorted) order.
func (m Manifest) Assets() []Asset
```

```go
package assets

// FS is the embedded, read-only asset filesystem. The embed RETAINS the dist/
// path segment (go:embed dist), so served FS paths are "dist/<hashed>" rather
// than root-level. Serve it under the bundle AssetBasePath with
// sdk/foundation/web.NewStaticFileServer + WithAssetPrefix("dist/"); the "dist/"
// prefix is what the SDK server matches to apply immutable caching, and the kit
// registers no route itself.
var FS fs.FS

// ManifestBytes is the committed, embedded manifest.json used by goth.New.
var ManifestBytes []byte
```

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `Asset` | empty struct = "no asset"; only meaningful when returned with `ok == true` | — | produced by the asset build (GOTH-1.2), read-only |
| `Manifest` | empty manifest, `Lookup` always `ok == false` | malformed `manifest.json` → `New` error, never a silent empty manifest | parsed by `New`, immutable |
| `assets.FS` / `assets.ManifestBytes` | populated by `go:embed`; generated + committed | drift caught by the plain-git `dist/` diff in `make check` (regeneration itself is Node-gated) | never hand-edited |

The host maps `bundle.AssetBasePath()` to `assets.FS` through the SDK static
file server constructed with `web.WithAssetPrefix("dist/")`. Because the embed
retains the `dist/` segment, the served FS path (`dist/theme.4f3a1c.css`)
matches that prefix and the SDK server applies
`Cache-Control: public, max-age=31536000, immutable`; without the retained
segment the prefix would not match and immutable caching would silently not
apply. The kit does not invent a static server or choose the public base URL.

### Toolchain pinning and drift check (frozen posture)

The Node runtime used to build the assets is pinned via `.nvmrc` (and the
`package.json` `engines` field) alongside `package-lock.json`, so a rebuild is
reproducible. Regenerating `dist/` + `manifest.json` from source is a
**Node-gated** target — it mirrors the Node-gated `make test-ui-browser` and does
not run inside the always-on `make check`. `make check` instead verifies the
committed `dist/`/`manifest.json` are in sync by a **plain-git diff** of the
committed tree, never by invoking Node, so a Node-free contributor's `make check`
still fails on stale assets. This is the default posture unless the owner
overrides it at GOTH-1.2.

---

## 4. Browser resource requirements

```go
package goth

// Directive is a CSP resource directive key the kit can require.
type Directive string

const (
	DirectiveScript  Directive = "script-src"
	DirectiveStyle   Directive = "style-src"
	DirectiveImg     Directive = "img-src"
	DirectiveFont    Directive = "font-src"
	DirectiveConnect Directive = "connect-src"
	DirectiveMedia   Directive = "media-src"
	DirectiveWorker  Directive = "worker-src"
)

// Requirements is the deterministic, minimal set of browser resource needs for
// a bundle's selected profile. A host/feature maps it into its own CSP policy;
// the kit never writes a header. Ordering is stable and duplicate-free.
type Requirements struct { /* unexported */ }

// Sources returns the required sources for a directive in deterministic order.
// For a self-hosted default bundle these are exactly 'self' (kit CSS, default
// theme, and the root-relative host stylesheet are all served under 'self'). ok is
// false for a directive the bundle does not require.
func (r Requirements) Sources(d Directive) (sources []string, ok bool)

// Directives returns every required directive in a stable order.
func (r Requirements) Directives() []Directive
```

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `Directive` | `""` (invalid key) | callers use the exported constants; an unknown key yields `ok == false` from `Sources` | frozen constant set |
| `Requirements` | empty = "requires nothing" | total, non-erroring accessors; never panics | produced by `Bundle.Requirements()`, read-only |

Frozen guarantees: the default self-hosted bundle requires no remote origin, no
`unsafe-eval`, and no `unsafe-inline`. `StylesOnly` requires `style-src 'self'`;
`Interactive`/`Full` add `script-src 'self'`. Because the kit emits no
server-rendered `style` attribute and no inline `<style>` element (see the frozen
invariant below), `style-src` is exactly `'self'` with **no nonce** — the entire
nonced/dynamic-stylesheet channel is gone (amendment 1). Script tags carry no
nonce either: they are external, fingerprinted, deferred, and SRI-guarded, so
`'self'` + integrity covers them under `script-src`. GOTH-1.4 tests inventory
every primitive that widens requirements; no hidden CSP requirement is accepted.

One frozen invariant governs dynamic styling:

- **The kit emits no server-rendered `style` attribute and no inline `<style>`
  element under any configuration.** Dynamic geometry (e.g. GOTH-4.1 anchored
  positioning) uses `data-*` attributes read by external CSS only. A `style` value
  a caller injects via `Base.Attributes` is the caller's own CSP problem; the kit
  neither adds nor requires `'unsafe-inline'` for `style-src` to cover its own
  primitives. **Controller-owned CSSOM writes are the one intentional exception:**
  a controller may call `element.style.setProperty("--goth-anchor-*", …)` after
  runtime activation. Those custom-property writes may serialize as a DOM `style`
  attribute in the *live* DOM, but they are absent from rendered HTML, are governed
  by `script-src` not `style-src`, and never widen CSP.

---

## 5. Theme token contract (frozen semantic names)

`ui/README.md` owns the cross-implementation role names; `ui/goth/theme`
implements them as CSS custom properties with neutral fallbacks. The **exact
frozen token list** is below. The token NAMES are the stable public compatibility
surface — they are the CSS contract. Their VALUES live in CSS
(`theme/contract.css` neutral fallbacks + `theme/default.css` palette), never in a
Go value.

```go
package theme

// Token is a frozen semantic token name. Its CSS variable form is "--<token>".
type Token string

// Tokens returns every frozen token name in a deterministic (sorted) order. It is
// the public vocabulary; there is no Go theme value to construct or validate.
func Tokens() []Token
```

**Theming is CSS-only (amendment 1).** There is no Go `Theme` value type,
`NewTheme`, or `DefaultTheme`: the amendment removed the entire Go theme-value
machinery. A host themes the app the WordPress way — the kit ships compiled
component CSS with neutral contract fallbacks, and the host ships a plain
stylesheet loaded *after* the kit stylesheet, so CSS source-order cascade lets the
last `:root` declaration win. Set a token by redeclaring its custom property:

```css
/* the host's own stylesheet, served under 'self' and pointed at by */
/* Config.ThemeStylesheetPath */
:root {
  --primary: oklch(0.55 0.22 265);
  --radius: 0.25rem;
}
.dark, [data-theme="dark"] {
  --primary: oklch(0.72 0.19 265);
}
```

### The host-stylesheet override story

- **Default (empty `ThemeStylesheetPath`).** `Head()` injects the kit's compiled
  `theme-default.css` (the polished light/dark palette) after `theme.css`. Zero
  wiring yields a fully themed page.
- **Host theme.** Set `Config.ThemeStylesheetPath` to a root-relative path your
  host serves. `Head()` emits it as a `<link rel="stylesheet">` after the kit
  stylesheet and **does not** inject `theme-default.css`, so your file is the sole
  palette; anything you omit falls through to the neutral `contract.css` fallbacks
  baked into `theme.css`, so a page is never fully unstyled.
- **Copyable starter.** `ui/goth/theme/default.css` is the canonical starter:
  clone it, edit the values, serve it, and point `ThemeStylesheetPath` at it. It
  enumerates every appearance-dependent token with the owner-default values.
- **Token- and class-level overrides both work.** Redeclare a `--token` custom
  property to retint, or write a rule against a stable `.goth-*` class (e.g.
  `.goth-badge { border-radius: 0; }`) for structural tweaks. Both ride ordinary
  cascade after the kit stylesheet.

### Using Tailwind in YOUR app against the `.goth-*` surface

Tailwind is **not** part of the kit — the kit's build and output contain no
Tailwind. But because the kit's styling surface is stable `.goth-*` classes and
`--token` custom properties, a host is free to run **any** Tailwind version in its
**own** build and use it alongside the kit. The clean bridge in Tailwind v4 is to
map the kit's semantic tokens into Tailwind's theme so `bg-primary`/`text-primary`
resolve to the same custom properties the kit uses:

```css
/* the host's Tailwind v4 entry (the host's build, never the kit's) */
@import "tailwindcss";

@theme {
  --color-primary: var(--primary);
  --color-background: var(--background);
  --color-foreground: var(--foreground);
  /* …bridge whatever kit tokens the host wants as Tailwind colors… */
}
```

The host then emits this compiled Tailwind stylesheet as its
`ThemeStylesheetPath` (or as additional host-owned CSS) — it loads after the kit
stylesheet under `'self'`, so the cascade story is unchanged. This is an adopter
convenience only: the kit never depends on, ships, or recompiles Tailwind, and any
Tailwind version is entirely the host's choice.

### Frozen token set

CSS variable form is `--<token>` on `:root` (light) with dark values under both
`.dark` and `[data-theme="dark"]`. Every token has a neutral fallback.

**Surfaces & text**
`background`, `foreground`, `card`, `card-foreground`, `popover`,
`popover-foreground`, `overlay` (scrim color/opacity for modal, dialog, drawer,
sheet, command, and combobox backdrops — P37/P39/P40/P47/P51).

**Semantic roles (+ foregrounds)**
`primary`, `primary-foreground`, `secondary`, `secondary-foreground`,
`muted`, `muted-foreground`, `accent`, `accent-foreground`,
`destructive`, `destructive-foreground`.

**Status roles (+ foregrounds)**
`success`, `success-foreground`, `warning`, `warning-foreground`,
`tertiary`, `tertiary-foreground` (tertiary is the documented optional role).

**Form / outline**
`border`, `input`, `ring`.

**Charts**
`chart-1`, `chart-2`, `chart-3`, `chart-4`, `chart-5`.

**Sidebar**
`sidebar`, `sidebar-foreground`, `sidebar-primary`,
`sidebar-primary-foreground`, `sidebar-accent`, `sidebar-accent-foreground`,
`sidebar-border`, `sidebar-ring`.

**Shape & elevation**
`radius`, `shadow-sm`, `shadow`, `shadow-md`, `shadow-lg`. `shadow-lg` is the
frozen elevation ceiling and is the shadow intended for overlays; there is no
`shadow-xl` in the contract.

**Typography**
`font-sans`, `font-serif`, `font-mono`.

**Motion**
`duration-fast`, `duration`, `duration-slow`, `ease`, `ease-emphasized`.

**Density & layering**
`density` (spacing scale multiplier), `z-base`, `z-sticky`, `z-overlay`,
`z-modal`, `z-popover`, `z-toast`. `density` is a real spacing-scale multiplier
from which GOTH-1.3 DERIVES each component's padding and gap (component padding =
base spacing × `density`); it is implemented as a derivation input, not a
decorative token.

```go
package theme

// Document-attribute helpers. Light lives on :root. Dark supports both the
// .dark class and the data-theme form; system preference is an opt-in
// selection POLICY (a host/controller choice), never a component concern.
type Appearance string

const (
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
	AppearanceSystem Appearance = "system" // resolved by the host/controller, not by a primitive
)

// HTMLAttributes returns the <html> attributes (dir + data-theme) for an
// appearance and text direction, for a host to apply on the document element.
func HTMLAttributes(a Appearance, dir Direction) templ.Attributes

type Direction string

const (
	DirectionLTR Direction = "ltr"
	DirectionRTL Direction = "rtl"
)
```

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `Token` | `""` (invalid) | `Tokens()` is total; there is no value validation (values are CSS-only) | frozen name set |
| `Appearance` | `""` → treated as `AppearanceLight` by helpers | unknown → light fallback | host/controller selects; never a primitive |
| `Direction` | `""` → `DirectionLTR` | unknown → LTR fallback | host/page sets; primitives inherit |

Brand values (Segovia/GPS) are proof/override inputs only. The role names are the
contract; specific values do not generalize and no Segovia/GPS360 code is
imported.

---

## 6. Document composition

```go
package goth

// Document composes <html>/<head>/<body> with the bundle's Head, an appearance,
// and a direction, wrapping caller body content. It is a convenience for whole
// pages; a host may instead call Bundle.Head() inside its own document. It
// writes no HTTP header and no route.
func (b *Bundle) Document(opts DocumentOptions, body templ.Component) templ.Component

// DocumentOptions is the value type for Document. Zero value: light appearance,
// LTR, empty <title>, no extra head content.
type DocumentOptions struct {
	Title      string
	Appearance theme.Appearance
	Dir        theme.Direction
	Lang       string          // defaults to "en" when empty
	HeadExtra  templ.Component // host-owned extra head content; nil is fine
}
```

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `DocumentOptions` | light/LTR/empty title/lang "en" | no error path; rendering is total | host constructs per page |

**Configured-host-link SRI limitation + composition escape.** When
`Config.ThemeStylesheetPath` is set, `Head()`/`Document()` emit the host theme link
**verbatim with no `integrity` attribute** — the kit cannot know the host's bytes,
so it cannot compute a Subresource Integrity hash for them (the kit's own assets,
including the default `theme-default.css`, always carry integrity + crossorigin).
`HeadExtra` is not an equivalent substitute: it would retain the default-theme
link or duplicate the configured link and would not preserve the frozen pre-script
ordering. A host that requires SRI on its theme stylesheet must **manually compose
the complete `<head>` from `Bundle.Manifest()`** (computing its own integrity for
its own bytes) instead of calling `Bundle.Head()`/`Bundle.Document()`. Widening the
first-class field to carry integrity later is compatible.

---

## 7. Primitive props / slots / attributes / IDs / data hooks

All 64 primitives live in package `primitives`. Each catalog entry maps to one of
four **API families** (see [`catalog.md`](catalog.md) for the per-entry mapping):

- **F1 — leaf props primitive:** one exported function whose entire content is a
  single inline/scalar value (or nothing) with no caller-composed content roles
  and no independently-addressable sub-parts (e.g. `Badge`, `Skeleton`,
  `Separator`).
- **F2 — slotted props primitive:** ONE exported function that arranges caller
  content — a principal `{ children... }` region plus optional auxiliary
  `templ.Component` slot fields (icon, action, media) — inside a fixed internal
  layout the primitive owns (e.g. `Alert`, `Empty`, `Button`).
- **F3 — compound-parts primitive:** a set of prefix-named functions the CALLER
  composes and places, sharing the `data-slot`/`data-state`/`data-variant`
  contract (e.g. `Card`/`CardHeader`/`CardContent`, `Dialog`/`DialogTrigger`/
  `DialogContent`, `Table` family).
- **F4 — controller-backed primitive:** an F3 family plus a named Alpine
  controller and/or explicit HTMX seam (e.g. `Combobox`, `DataTable`, `Command`,
  `Sidebar`).

**Decision rule (frozen).**

- **F1 vs F2:** a primitive is **F1** when its content is one inline/scalar value
  (or none) and it exposes no named content roles; it is **F2** when a single
  function arranges a principal rich-content region (`{ children... }`) and/or
  one or more named content roles inside a layout the primitive owns.
- **F2 vs F3:** a primitive stays **F2** when the internal arrangement is fixed
  and owned by the one function (the caller only fills roles through a single
  `Props` value); it is **F3** when the caller composes and places multiple named
  part functions itself, sharing the data-hook contract.

Re-verified against this rule at Gate B remediation (2026-07-17): Alert (P01, F2 —
icon/title/description roles in a fixed layout), Badge (P04, F1 — single inline
label), Card (P08, F3 — caller-composed header/content/footer parts), Empty (P10,
F2 — icon/title/description/action roles fixed), Item (P14, F3 — caller-composed
media/content/actions parts), Marker (P17, F2 — marker glyph + content region
fixed). No reclassification was demanded; the frozen counts are unchanged.

Across the frozen catalog the family split is F1 = 11, F2 = 6, F3 = 14,
F4 = 33 (11 + 6 + 14 + 33 = 64); the per-entry mapping is in
[`catalog.md`](catalog.md).

### Common props contract (frozen)

Every `Props` type is a value type with a useful zero value and embeds the shared
base:

```go
package primitives

// Base is embedded by every primitive Props type.
type Base struct {
	// ID is the caller-provided stable element id. An interactive primitive
	// that needs an id REQUIRES a non-empty ID (documented per primitive) OR
	// takes ids from a request-scoped IDFactory; the kit uses NO global/
	// duplicate counter. A non-interactive primitive leaves ID empty.
	ID string

	// Class is appended AFTER the primitive's stable base class, so callers add
	// utilities without dropping the compatibility class. Class is the ONLY class
	// channel: a "class" key inside Attributes is rejected/ignored (never merged),
	// so the compatibility class can never be dropped through the escape hatch.
	Class string

	// Attributes is the escape hatch for ids, names, ARIA, data-*, and explicit
	// hx-* attributes. Behavior-critical attributes the primitive owns (role,
	// aria-* it manages, data-slot/data-state/data-variant, x-data controller
	// binding, type on a submit control) are applied AFTER Attributes and cannot
	// be silently overwritten; owned and caller attributes funnel through ONE
	// merged spread on the element (see MergeAttributes). A "class" key here is
	// rejected — use Base.Class.
	Attributes templ.Attributes
}

// Variant / Size are typed string enums per primitive; each has a validation
// test and a documented default that is the zero value. An unknown value renders
// the documented default (primitives do not panic), and the enum's Valid method
// reports membership for callers that want to fail fast.
```

Frozen common rules:

- exported props are value types with a useful zero value;
- variants and sizes are typed string enums with validation tests and a
  zero-value default;
- content and icons are `templ.Component` slots — never raw trusted HTML
  strings;
- `Class` appends after the stable base class;
- `Attributes templ.Attributes` is the only escape hatch; the primitive's
  behavior-critical attributes win the documented merge and are tested;
- URL-bearing props use a typed safe-URL type (`URL` below), never a generic
  string smuggled into `templ.SafeURL`;
- an interactive primitive requires a caller ID or uses `IDFactory`; no global
  counter;
- **F2 principal content channel:** an F2 primitive's principal content is templ
  children (`{ children... }`); auxiliary content roles are `templ.Component`
  slot fields on the `Props`. F1 has neither; F3 exposes parts the caller
  composes;
- **compound-part ARIA linkage** (F3/F4) is caller-passed via `Base.ID` per part:
  the caller assigns each part's `Base.ID` (or draws it from an `IDFactory`) and
  the parts reference those ids for `aria-labelledby`/`aria-controls`. The kit
  freezes no ambient context key for id threading; the linkage uses the already
  frozen `Base.ID`/`IDFactory` surface only;
- **accessible name** for any interactive primitive that can render icon-only
  (Button, toggle controls, icon buttons): a dedicated accessible-name field is
  required and enforced by a render-time contract test — it is NOT left to the
  `Base.Attributes` escape hatch, so an icon-only control cannot ship nameless;
- **no server-rendered `style=`:** primitives never emit a server-rendered
  `style` attribute or inline `<style>` element (the frozen invariant in §4);
  dynamic geometry uses `data-*` attributes + external CSS, with controller-owned
  CSSOM custom-property writes the one runtime-only exception;
- stable `data-slot`, `data-state`, `data-variant`, and the Alpine
  controller-binding attribute `x-data="goth…"` are the public compatibility
  emitted surface even when utility classes change.

### Safe URL type

```go
package primitives

// URL is a validated, safely rendered URL for href/src/action props. Construct
// with ParseURL; the zero value is the empty URL and renders nothing. It never
// exposes a raw templ.SafeURL conversion through a generic string helper.
type URL struct { /* unexported */ }

// ParseURL validates s (scheme allowlist http/https/mailto/tel + relative
// same-origin paths; rejects javascript:, data:, control chars) and returns a
// URL. An invalid input is an error, not a silently dropped attribute.
func ParseURL(s string) (URL, error)
```

### Request-scoped IDs

```go
package primitives

// IDFactory yields stable, unique element ids within one request/render. A host
// or component supplies one (request-scoped); the kit ships a default
// constructor. There is NO package-level counter, so ids are deterministic per
// render and never collide across concurrent requests.
type IDFactory interface {
	// NextID returns a new unique id with the given human-readable prefix.
	NextID(prefix string) string
}

// NewIDFactory returns a fresh request-scoped IDFactory.
func NewIDFactory() IDFactory
```

### Attribute merge helper

```go
package primitives

// MergeAttributes returns owned attributes layered over caller attributes so a
// caller can add ids/aria/data/hx-* via Base.Attributes while the primitive's
// behavior-critical keys (owned) always win. This is the single documented,
// tested merge order used by every primitive.
//
// Frozen emission rule: the merged result is applied as ONE spread on the
// element ({ attrs... }). Owned and caller attributes never appear as sibling
// static attributes on the same element, because templ 0.3.1020 emits duplicate
// attributes rather than overriding when a static attr and a spread collide. A
// "class" key in caller attributes is dropped here — Base.Class is the only
// class channel.
func MergeAttributes(caller templ.Attributes, owned templ.Attributes) templ.Attributes
```

### Per-primitive freezes

- **Button (P06), family F2.** One exported `Button` function (not a separate
  `ButtonLink`) whose `Props` carries an optional URL field of type `URL`: an
  empty URL renders a `<button>`, a set URL renders an anchor styled as a button.
  A render-time guard rejects the invalid cross-field combination of a set URL
  together with `type=submit`/`type=reset` or `disabled` (an anchor is neither a
  submit control nor disable-able); the guard is a required contract test, not a
  silent drop. (The one-function-with-optional-URL shape is inferred from R6's
  cross-field-guard requirement, which is only expressible under one function —
  see the Gate B remediation note; owner may still choose two functions.)
- **Toggle (P35) vs Switch (P33) asymmetry (frozen rationale).** Switch (P33) is
  F1: a boolean form input rendered as a native styled checkbox that submits
  natively with no controller. Toggle (P35) is F4: a toggle *button*
  (`aria-pressed`) whose in-form variant is **checkbox-backed** (the native
  checkbox submits; the controller only enhances the pressed-state button
  semantics) and whose Toggle Group sibling needs roving focus — so it carries a
  named Alpine controller and stays F4. The asymmetry is deliberate: a boolean
  input (native, F1) versus a pressed-state button with group mechanics
  (controller-enhanced, F4).

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `Base` | empty id/class/attrs = non-interactive defaults | interactive primitive with required id empty → documented render-time no-op + failing contract test, never a duplicate/global id | caller sets; primitive owns behavior-critical attrs |
| `Variant`/`Size` enums | documented default | unknown → default render; `Valid()` for fail-fast | frozen per primitive |
| `URL` | empty → renders nothing | `ParseURL` error on unsafe input | caller constructs |
| `IDFactory` | interface; `NewIDFactory()` default | — | host/component supplies request-scoped |
| `MergeAttributes` | — | total function | kit-owned merge order |

---

## 8. Alpine controller rules (frozen)

- Native HTML (`details`, `dialog`, native form controls, popover where support
  and semantics suffice) is preferred before Alpine.
- Controllers are **named by behavior**, `goth`-prefixed: `gothDialog`,
  `gothRovingFocus`, `gothCombobox`, `gothMenu`, `gothTabs`, `gothToast`,
  `gothCollapse`, `gothTooltip`, `gothHoverCard`, `gothMessageScroller`,
  `gothResizable`. They are
  registered **once** in the runtime entrypoint using `@alpinejs/csp` — no inline
  expression is evaluated, so no `unsafe-eval`. (`gothTooltip`/`gothHoverCard` were
  added for the Hover Card/Tooltip primitives by a recorded owner reopen of this
  frozen list — see the 2026-07-17 GOTH-4.3 addendum in
  `.claude/plans/ui-goth/gate-b-review.md`. Escape-hide and hover-intent are not
  expressible in CSS and no existing controller fits a non-trapping,
  hover/focus-opened, describedby overlay; both new controllers compose the
  frozen GOTH-4.1 mechanics with no fork. Popover rides native
  `popover`/`popovertarget` and Select rides a native `<select>`, so neither adds
  a controller. `gothMessageScroller` was added for Message Scroller (P60) by a
  recorded owner reopen — see the 2026-07-18 GOTH-6.2 addendum in
  `.claude/plans/ui-goth/gate-b-review.md`; scroll-position management — live-edge
  following, history-prepend-without-jump, jump-to-message focus, unread/scroll
  state — is expressible in no frozen controller, and the F4-native precedent cannot
  apply because the no-JS transcript covers read/jump but not live-follow. It
  composes the frozen GOTH-4.1 mechanics + `mechanics/live-region.js` over a
  server-rendered `role=log` transcript with no fork. `gothResizable` was added for
  Resizable (P61) by a recorded owner reopen — see the 2026-07-18 GOTH-6.3 addendum
  in `.claude/plans/ui-goth/gate-b-review.md`; split-pane drag (pointer capture +
  APG keyboard resize + bounds) is expressible in no CSS and no frozen controller
  provides pointer-drag geometry, and there is no honest no-JS live-resize baseline.
  It reads separator drag/keyboard input and writes pane geometry through a
  controller-owned CSSOM custom property (`--goth-resize-basis`, the anchor-mechanic
  pattern — no server-rendered `style=`) over a server-rendered
  `role=separator`/`role=group` split whose default geometry is server-owned via a
  `data-default-size` attribute + external CSS. The §8 list is now eleven.)
- A controller is bound to its root element with the `x-data="goth…"` attribute
  (e.g. `x-data="gothDialog"`). That binding attribute is part of the frozen
  public emitted surface alongside `data-slot`/`data-state`/`data-variant`.
- Controllers discover their parts through the stable `data-slot` / `data-state`
  attributes and dispatch **documented custom events** (`goth:open`,
  `goth:close`, `goth:select`, `goth:change`). Feature-specific event names are
  forbidden.
- Shared controller **families** own the hard mechanics — focus restore/trap,
  escape/outside-click, typeahead, roving tabindex, live regions — so individual
  primitives never fork slightly different accessibility behavior.
- Alpine Focus and Collapse may be pinned when they materially reduce risk; every
  additional plugin earns its bundle weight and appears in `THIRD_PARTY_NOTICES`.
- The internal icon set covers control glyphs only (check, chevron, close,
  spinner). Public APIs accept caller icons as `templ.Component`; the kit does
  not bundle a full icon library.

Ownership: controllers live in `assets/src/js`, are compiled into the
`Interactive`/`Full` runtime asset, and are registered by the kit. A host neither
registers nor renames them. State they hold is local interaction state only —
never a domain store; the server owns authoritative state and validation.

---

## 9. Explicit HTMX attribute grammar (HTMX 2.0.10; `Attrs` field set FROZEN at GOTH-5.3, FINALIZED at GOTH-7.3 — nothing provisional)

**Gate B disposition (owner-decided, 2026-07-17): PROVISIONAL until the first real
consumers land.** Frozen at Gate B: the `htmx` package existence; the principles
(server owns markup, `hx-*` per element, no inherited behavior, no `hx-boost`
shortcut, headers are presentation hints only, no security decision from an hx
header); the `Method` and `Swap` enum sets; the `Request` presentation-hints-only
reader; and the `Build`/`Base.Attributes` merge rule (below).

**GOTH-5.3 finalization (2026-07-17).** The first real consumers landed — the Data
Table (P52) and the Combobox (P50) — so the `Attrs` field set is now **FROZEN**
against what they actually demonstrated. Two recorded candidate gaps had concrete
consumers and are now first-class, typed fields:

- **`Trigger` is a typed `htmx.Trigger`** (`Event`/`Changed`/`Delay`/`Throttle`),
  replacing the earlier free string. It emits a validated `hx-trigger` such as
  `input changed delay:150ms` — exactly the debounce the Combobox async input and
  the Data Table live filter need.
- **`SwapMods htmx.SwapModifiers`** (`Show`/`Scroll`/`FocusScroll`/`Settle`) appends
  validated `hx-swap` modifiers. The Data Table swaps its content region on every
  sort/filter/page and uses `Show: "none"` + `focus-scroll:false` so the viewport
  neither jumps nor steals focus.

**GOTH-7.3 finalization (2026-07-18) — nothing is provisional anymore.** The CMS
(mutating) adopter landed: the admin entries-list is HTMX-enhanced (sort/filter/page
swap the `#cms-entries-content` region, degrading to full-document no-JS reloads).
It is a third consumer of the frozen typed `Trigger` (the filter form's
`Event:"change"`) and `SwapModifiers` (`Show:"none"` + `FocusScroll:false`), and it
demonstrates that the once-residual candidates have **no consumer** — so each is
**RETIRED as no-demonstrated-need** (reopenable by the standard rule):

- **`Vals` / `Include`** — the filter form serializes its own fields (a hidden
  `order` input beside the status select) and full state rides shareable URLs, so no
  cross-element include is needed. RETIRED.
- **`Headers` (`hx-headers`) — the CSRF seam** — every CMS mutation rides a
  `<form>` (full-document POST); the HTMX surface is GET-only. So a hidden
  `csrf_token` input (as authentication already uses) is the whole story and there
  is **no non-form hx mutation** needing `hx-headers`. **CSRF posture SETTLED:
  hidden-input-only.** RETIRED. The reader still derives no CSRF/authorization from
  any hx header either way.
- **`DisabledElt`** — the in-flight busy affordance rides the server-owned
  `data-state` and HTMX's own `.htmx-request` class, not `hx-disabled-elt`. RETIRED.
- **typed-`URL` alignment** (`Attrs.URL` → `primitives.URL`) — the caller passes an
  already-validated URL string, so coupling `htmx` → `primitives` buys no safety.
  RETIRED (decided against).
- **additional trigger/swap modifiers** (`from:`/`target:`/`once`/`queue:`/
  multi-trigger; `swap:` delay) — no consumer through GOTH-7.3. RETIRED.

**Merge rule (frozen):** `Attrs.Build()` returns `templ.Attributes` that the
CALLER (handler or component) passes into a primitive's `Base.Attributes`. The
primitive funnels them through `MergeAttributes` in §7, so the primitive's owned
behavior-critical keys win and everything lands in one merged spread. `htmx` never
touches an element directly and never merges on its own.

```go
package htmx

// Attrs builds explicit hx-* attributes for ONE element. It emits ordinary
// hx-* attributes only — it never inherits, never uses hx-boost, and never
// conceals server behavior. The zero value emits nothing.
//
// FINALIZED field set — frozen at GOTH-5.3 (Data Table, Combobox) and confirmed
// final at GOTH-7.3 (CMS entries-list HTMX). The once-residual candidates (Vals,
// Include, Headers/hx-headers, DisabledElt, typed-URL alignment) are RETIRED as
// no-demonstrated-need; CSRF posture is hidden-input-only (mutations ride <form>).
type Attrs struct {
	Method    Method        // GET/POST/PUT/PATCH/DELETE; zero value = none emitted
	URL       string        // request URL; required when Method is set, else error at Build
	Target    string        // hx-target selector; empty = default (this element)
	Swap      Swap          // hx-swap strategy; zero value = HTMX default innerHTML
	SwapMods  SwapModifiers // hx-swap modifiers (scroll/focus preservation); zero = none
	Select    string        // hx-select
	Indicator string        // hx-indicator selector
	Confirm   string        // hx-confirm text
	Trigger   Trigger       // typed hx-trigger; zero value = element's natural default
	PushURL   bool          // hx-push-url="true" when set
}

// Trigger is a typed hx-trigger for ONE element (FROZEN at GOTH-5.3). The zero
// value emits nothing. It gives the debounced "changed" support its consumers need
// (Combobox, Data Table, CMS filter). Additional modifiers (from:/target:/once/
// queue:) are RETIRED at GOTH-7.3 as no-demonstrated-need.
type Trigger struct {
	Event    string        // "input", "keyup", "submit", "load", …
	Changed  bool          // adds "changed"
	Delay    time.Duration // adds "delay:<ms>" (debounce)
	Throttle time.Duration // adds "throttle:<ms>"
}

// SwapModifiers appends validated hx-swap modifiers after the strategy (FROZEN at
// GOTH-5.3) so a server-owned fragment swap can preserve scroll/focus. The zero
// value adds nothing.
type SwapModifiers struct {
	Show        string        // "show:<value>" — "none" suppresses scroll-into-view
	Scroll      string        // "scroll:top|bottom|<selector>:top"
	FocusScroll *bool         // "focus-scroll:true|false"; nil omits
	Settle      time.Duration // "settle:<ms>"
}

// Build validates the combination and returns templ.Attributes. It errors when
// Method is set without a URL, when a selector contains control characters, or
// when Method is not a known verb. It NEVER emits a partially valid attribute
// set alongside an error.
func (a Attrs) Build() (templ.Attributes, error)

type Method string
const (
	MethodGet    Method = "get"
	MethodPost   Method = "post"
	MethodPut    Method = "put"
	MethodPatch  Method = "patch"
	MethodDelete Method = "delete"
)

type Swap string
const (
	SwapInnerHTML   Swap = "innerHTML" // zero value maps here (HTMX default)
	SwapOuterHTML   Swap = "outerHTML"
	SwapBeforeBegin Swap = "beforebegin"
	SwapAfterBegin  Swap = "afterbegin"
	SwapBeforeEnd   Swap = "beforeend"
	SwapAfterEnd    Swap = "afterend"
	SwapDelete      Swap = "delete"
	SwapNone        Swap = "none"
)

// Request interprets the incoming request's HTMX headers as PRESENTATION HINTS
// ONLY. It never yields identity, CSRF, authorization, or business-state
// evidence. A non-HTMX request returns a zero Request (IsHTMX() == false), which
// the handler treats as "render the full document".
type Request struct { /* unexported */ }

// FromRequest reads HX-Request / HX-Target / HX-Trigger etc. from r.
func FromRequest(r *http.Request) Request

func (rq Request) IsHTMX() bool
func (rq Request) Target() string
func (rq Request) TriggerName() string
```

| type | zero value | error behavior | ownership |
|---|---|---|---|
| `Attrs` (FROZEN at GOTH-5.3; FINALIZED at GOTH-7.3 — nothing provisional) | emits nothing | `Build` errors on method-without-URL / bad verb / control chars / trigger-modifiers-without-event | caller (handler/component) constructs; `Build` output flows into `Base.Attributes` |
| `Method`/`Swap` | `""` / `SwapInnerHTML` | unknown `Method` → `Build` error; `Swap` zero → HTMX default | frozen constant sets |
| `Trigger` | `IsZero()` → no `hx-trigger` | modifiers without an `Event` → `Build` error | frozen typed field (GOTH-5.3) |
| `SwapModifiers` | zero → no modifiers | control char in a selector → `Build` error | frozen typed field (GOTH-5.3) |
| `Request` | `IsHTMX()==false` → full-document response | total accessors; presentation hints only | read by handler; never a security input |

Frozen HTMX contract (mirrors the plan's HTMX-4-forward rules):

1. every `hx-*` attribute is placed on the element it affects; no inherited
   behavior; `hx-boost` is never an app-wide shortcut;
2. handlers treat HTMX headers as presentation hints, never identity/CSRF/
   authorization/business state;
3. an absent/malformed/unsupported HTMX request degrades to the full-document
   response where the route supports HTML;
4. success/validation/conflict/forbidden/error fragments are complete swappable
   regions with stable targets; non-2xx fragment handling is configured
   explicitly (does not rely on HTMX 4's changed defaults);
5. history restoration is correct on a full-document re-fetch; no contract
   assumes a specific localStorage history-cache behavior;
6. OOB swaps, extensions, and morphing are not baseline dependencies — each is
   introduced only with a named use case and tests;
7. typed helpers emit ordinary explicit `hx-*` attributes and never conceal
   server behavior.

---

## 10. Ownership summary and invariants

- **Host owns:** the asset route + public base URL, HTTP response headers/CSP
  (mapping `Requirements` in), the theme stylesheet it serves + its
  `ThemeStylesheetPath`, appearance/direction selection, and profile choice.
- **Feature core owns:** page models and the technology-neutral `Views` port
  returning `web.Renderer`; it never imports `ui/goth`, templ, Alpine, or HTMX. A
  nil `Views` remains "no HTML surface, no view/runtime dependency".
- **Feature `views/goth` adapter owns:** translating feature page models into
  GOTH primitives/components and implementing the feature's `Views` port. Domain
  knowledge lives here, not in `ui/goth`.
- **`ui/goth` owns:** semantic tokens + neutral defaults, primitives/components,
  named controllers, typed HTMX helpers, embedded fingerprinted assets, the
  manifest, and a deterministic `Requirements` value. It owns no route, no
  header, no schema.

### Assembled host wiring (illustrative; frozen names only)

This specimen shows a host assembling the current surface. `handler` is
host-owned; the host also serves its own theme stylesheet under the path it passes.

```go
// 1. Construct the immutable bundle. Theming is CSS-only: point ThemeStylesheetPath
//    at a stylesheet the host serves, or leave it empty to inject the kit default.
bundle, err := goth.New(goth.Config{
	AssetBasePath:       "/assets/goth",
	Profile:             goth.Full,
	ThemeStylesheetPath: "/theme/host.css", // empty → kit's theme-default.css
})
if err != nil {
	return err
}

// 2. Serve the embedded fingerprinted assets. WithAssetPrefix("dist/") matches
//    the dist/-rooted FS paths so the SDK server applies immutable caching.
static := web.NewStaticFileServer(assets.FS, web.WithAssetPrefix("dist/"))
static.AddRoutes(handler, bundle.AssetBasePath())

// 3. Serve the host's own theme stylesheet at ThemeStylesheetPath as text/css.
//    (Host-owned; the kit only emits the <link>, never the bytes.)

// 4. Map Requirements into a CSP header value the HOST writes (the kit never does).
//    style-src is exactly 'self'; the kit needs no nonce (amendment 1 removed the
//    whole nonce channel), so there is no nonce branch to add here.
req := bundle.Requirements()
var csp strings.Builder
for _, d := range req.Directives() {
	sources, _ := req.Sources(d)
	csp.WriteString(string(d) + " " + strings.Join(sources, " ") + "; ")
}

// 5. Emit bundle.Head() inside the page <head>, or bundle.Document(opts, body)
//    for the whole-page convenience. Head order is: kit theme.css → the theme
//    stylesheet link (host path verbatim, or the kit default with integrity) →
//    deferred scripts.
```

Standing invariants carried from the plan: server ownership of authoritative
state; a progressive no-JS baseline; no implicit HTTP mutation; no remote runtime
dependency; no `unsafe-eval`; no server-rendered `style` attribute or inline
`<style>` element (controller-owned CSSOM writes excepted); HTMX optional;
technology-neutral feature cores; host-overridable theme via a host stylesheet;
checked generated artifacts; accessibility as tested release behavior; honest
third-party provenance.

This surface is complete for every P01–P64 entry: [`catalog.md`](catalog.md)
maps each entry to exactly one API family (F1–F4) with no unresolved family.
Changes to any name/shape above reopen GOTH-0.3 and re-enter Gate B.

---

## 11. Adoption, theming, security, and handoff recipes

§1–§10 are the frozen contract. This section is the adopter guide: how a host wires
the kit, how a feature ships a `views/goth` adapter, the CSP recipe, the component
layer, the HTMX migration trigger, and the Segovia/GPS360 handoff. Every Go snippet
here is proven against the real API by executed tests — the host wiring and HTMX
grammar by `examples/minimal/cmd/server/goth_htmx_proof_test.go` and
`examples/auth-cms/cmd/server/goth_proof_test.go`, and the CSP-formatter +
asset-reachability recipes by `examples/minimal/cmd/server/goth_doc_snippets_test.go`.

### 11.1 Install and wire (the complete host recipe)

The kit is an ordinary Go module — `go get`/workspace-replace
`github.com/gopernicus/gopernicus/ui/goth`. It registers no route and writes no
header; a host does exactly three things: build the immutable `Bundle`, serve its
embedded assets, and emit `Bundle.Head()` (or `Bundle.Document`) in the page. This
is the shape `examples/minimal` ships:

```go
import (
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"
)

const gothAssetBasePath = "/assets/goth"

// 1. Immutable bundle. The zero Config is a valid StylesOnly bundle with the kit's
//    default theme injected; pin the asset base path so the emitted hrefs and the
//    route below agree.
bundle, err := uigoth.New(uigoth.Config{AssetBasePath: gothAssetBasePath})
if err != nil {
	return err
}

// 2. Serve the embedded fingerprinted assets. WithAssetPrefix("dist/") matches the
//    dist/-rooted FS paths so the SDK server applies immutable caching. The kit owns
//    no route; the host mounts it.
static := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
static.AddRoutes(router, gothAssetBasePath)
```

The feature `views/goth` adapter (§11.6) then renders pages through `bundle`, and the
host wires it into the feature's `Config.Views`. `examples/cms` (Turso),
`examples/minimal` (memstore), and `examples/auth-cms` (auth + CMS on one router) are
the three reference wirings.

### 11.2 Profiles and the asset route

`Config.Profile` selects the served/required asset classes (§2): `StylesOnly` (CSS
only, the zero value and the safest no-JS bundle), `Interactive` (adds the Alpine CSP
runtime), `Full` (adds HTMX 2.0.10). All three reference hosts run `StylesOnly` — the
primitives' no-JS baselines carry them, and the CMS admin list's HTMX grammar rides
raw `hx-*` on `Base.Attributes` without needing the runtime asset. Move to
`Interactive`/`Full` only when a page composes a controller-backed (F4) primitive that
needs the runtime. Profiles are additive supersets and a bundle serves/requires exactly
its profile's classes — never more (§2).

The asset route is the single most common wiring mistake: forget
`static.AddRoutes(...)` and every page renders unstyled with a 404 stylesheet. Because
the kit owns no route, the kit cannot catch this — so run the **boot-time
asset-reachability self-check** (§11.5) after wiring.

### 11.3 CSP: mapping `Requirements` into a header the host writes

The kit never writes a header. It exposes a deterministic, minimal `Requirements`
value (§4) the host maps into its own CSP. For a self-hosted default bundle the whole
requirement is `style-src 'self'` (+`script-src 'self'` on Interactive/Full) — no
remote origin, no `unsafe-*`, no nonce. The documented formatter recipe (proven in
`goth_doc_snippets_test.go`):

```go
// cspHeader maps a bundle's Requirements into one CSP header value. Directive and
// source order are stable, so the emitted value is byte-identical run-to-run.
func cspHeader(req uigoth.Requirements) string {
	var b strings.Builder
	for i, d := range req.Directives() {
		if i > 0 {
			b.WriteString("; ")
		}
		sources, _ := req.Sources(d)
		b.WriteString(string(d))
		for _, s := range sources {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	return b.String()
}
```

A host writes `cspHeader(bundle.Requirements())` into its own
`Content-Security-Policy` (typically alongside its other fixed protections). The
authentication feature does not use this raw recipe — it consumes a
technology-neutral `HTMLResourcePolicy` the `views/goth` adapter derives from the same
`Requirements` (§11.6), so the feature's fixed protections stay feature-owned.

> **Deferred (owner decision).** Gate B product #4 asked whether the kit should ship a
> first-class `Requirements`→CSP-directive-string helper. It is NOT added here: a new
> exported `goth` function reopens the GOTH-0.3 frozen surface and re-enters Gate B.
> The host-side recipe above is the sanctioned pattern until an owner rules on
> promoting it into the kit.

### 11.4 The `components/` layer (component API)

Above the 64 primitives, the kit ships fourteen opinionated, domain-neutral
compositions under `ui/goth/components/{layouts,forms,feedback,data}` (GOTH-7.1). Each
takes a single `…Props` value (embedding no domain type), composes primitives only,
emits zero server-rendered `style=`, and inherits its no-JS baseline from those
primitives. They are the shapes proven repeatedly by the feature adapters, promoted so
adopters do not re-derive them:

| package | compositions | signature shape |
|---|---|---|
| `components/layouts` | `DocumentShell`, `AppShell`, `AuthShell`, `PageHeader`, `ActionBar` | `func(p XProps) templ.Component` |
| `components/forms` | `FormField`, `FormSection`, `ErrorSummary`, `FormActions` (+ `DescriptionID`/`ErrorID` helpers, `FormActionsAlign` enum) | `func(p XProps) templ.Component` |
| `components/feedback` | `EmptyPanel`, `ErrorPanel`, `LoadingPanel`, `ConfirmDialog` | `func(p XProps) templ.Component` |
| `components/data` | `TableToolbar` (a `role=search` GET form; a host adds `hx-*` via `SearchAttributes`) | `func(p XProps) templ.Component` |

`forms.FormField` wires label/description/error association for the caller: pass the
control's id and it exposes `forms.DescriptionID(id)`/`forms.ErrorID(id)` so the
control's `aria-describedby` points at the right nodes, and a non-empty `Error` flags
the field invalid. `feedback.ConfirmDialog` composes the gothDialog-backed
`AlertDialog` family (destructive-by-default; the confirm button submits a server-owned
form) with no new controller. `forms.ErrorSummary` renders nothing when empty.
Authentication composes `AuthShell`/`FormField`/`ErrorSummary`/`FormActions`; CMS
composes `AppShell`/`PageHeader`/`FormField`/`FormActions`/`TableToolbar` — see the
adapters for real usage.

For finer control, compose the primitives in `ui/goth/primitives` directly (§7); the
`components/` layer is a convenience, never the only path.

### 11.5 The "forgot to mount the asset route" failure mode + boot-time self-check

Because the kit registers no route, a host that omits `static.AddRoutes(...)` serves
pages whose fingerprinted stylesheet 404s — a silent, all-pages-unstyled failure the
kit cannot detect for you. Fail loud at boot instead by asserting every manifest asset
resolves over the asset route (proven in `goth_doc_snippets_test.go`):

```go
// assertAssetsReachable verifies every fingerprinted asset resolves over the host's
// asset route. Call it once after wiring (serve issues an in-process GET and returns
// the status). A host that forgot AddRoutes fails here at boot, not per page in prod.
func assertAssetsReachable(bundle *uigoth.Bundle, serve func(path string) int) error {
	base := bundle.AssetBasePath()
	for _, a := range bundle.Manifest().Assets() {
		url := base + "/" + a.Path
		if code := serve(url); code != http.StatusOK {
			return fmt.Errorf("ui/goth asset %q not reachable at %s (got %d) — is the asset route mounted?", a.LogicalName, url, code)
		}
	}
	return nil
}
```

`Manifest.Assets()` (§3) returns all four fingerprinted assets in deterministic order;
`serve` is a host-supplied closure (an `httptest`-style in-process GET against the
composed router). The check catches the mistake before the first real request.

### 11.6 Custom feature `Views` — the `views/goth` adapter recipe

A feature core exposes a technology-neutral `Views` port returning
`sdk/foundation/web.Renderer` and never imports `ui/goth`, templ, Alpine, or HTMX (a
nil `Views` keeps the feature HTML-free). The GOTH rendering ships as a **sibling
module** `features/<name>/views/goth` that implements that port over a `*goth.Bundle`.
The two reference adapters are `features/authentication/views/goth` and
`features/cms/views/goth`. The shape:

```go
package goth // features/<name>/views/goth

// Views implements <feature>.Views over the immutable presentation bundle.
type Views struct{ bundle *goth.Bundle }

var _ <feature>.Views = Views{}

// New fails loudly on a nil bundle rather than nil-panicking at first render.
func New(bundle *goth.Bundle) (Views, error) {
	if bundle == nil {
		return Views{}, errNilBundle
	}
	return Views{bundle: bundle}, nil
}
```

A host wires it as `views, _ := featuregoth.New(bundle)` →
`feature.Config{Views: views}`, and serves the bundle assets (§11.1). **Partial
override** is the blessed customization: embed the default `Views` in a host type and
override only the methods you rebrand (`examples/auth-cms/internal/authpages` overrides
just `Login`; `examples/cms/internal/theme` overrides the public chrome and falls
through to the GOTH admin pages).

**Security-sensitive features map `Requirements` into their own policy, not a raw
header.** Authentication's adapter carries an `HTMLPolicy()` that maps
`bundle.Requirements()` into the feature's `authentication.HTMLResourcePolicy` through
an explicit `goth.Directive → HTMLResourceKind` table, owning deterministic source
ordering:

```go
authViews, _ := authgoth.New(bundle)
cfg := authentication.Config{
	Views:      authViews,
	HTMLPolicy: authViews.HTMLPolicy(), // widens the feature CSP exactly enough
}
```

The adapter appends `script-src 'self'` with `Nonce: true` so the externalized
fragment-reader script (below) and any per-render inline script run — a non-nil policy
REPLACES the feature's default `script-src` tail, so a policy omitting it fails closed
(the C5 contract test guards this). The feature's fixed protections (`default-src
'none'`, `base-uri 'none'`, `form-action 'self'`, `frame-ancestors 'none'`, and the
no-store/no-referrer/frame/content-type headers) stay feature-owned and unremovable.

**Externalized inline scripts.** Where a page needs a tiny script (auth's reset and
magic-link landings read the token from the URL fragment), it ships as an embedded,
same-origin served file — never inline — so it runs under `script-src 'self'`. The auth
adapter serves it via `authgoth.FragmentScriptHandler()` mounted at
`authgoth.DefaultFragmentScriptPath` (overridable with `authgoth.WithFragmentScriptPath`).

### 11.7 HTMX conventions and the migration trigger

HTMX is opt-in (`Profile: Full`, or raw `hx-*` on `Base.Attributes` under any profile).
The frozen grammar and the typed `htmx.Attrs` builder are §9. Two adopter rules:

- **When to reach for HTMX (the migration trigger).** Start every surface as a working
  no-JS `<form>`/link flow. Add HTMX only to a route that already works without it and
  where a fragment swap is a *measurable* UX win (a filter/sort/paginate that would
  otherwise full-reload). HTMX must degrade to exactly that no-JS path — the CMS
  entries list is the reference: sort/filter/page carry `hx-get` +
  `hx-target="#cms-entries-content"` + `hx-swap="outerHTML show:none focus-scroll:false"`
  + `hx-push-url="true"`, and the same URL without `HX-Request` returns the full
  document. Do **not** HTMX-convert every route during a migration.
- **Mutation protection is host-owned (revised 2026-07-20; supersedes the GOTH-7.3
  hidden-input-only wording).** Every mutation rides a `<form>` full-document POST; the
  HTMX surface is GET-only until a mutation design is separately ratified. How those
  POSTs are protected against cross-origin forgery is the HOST's decision, made per the
  ratified web posture (`ARCHITECTURE.md`, "Host HTML cross-origin posture"): the
  recommended modern-browser posture is origin-only — `http.CrossOriginProtection`
  mounted on the host's HTML groups — with no kit-mandated hidden-field mechanism.
  Feature-owned forms may still carry tokens when their owning feature requires them
  (auth's account mutations do). Either way there is no `hx-headers` CSRF seam, and
  handlers derive no identity/CSRF/authorization from any HTMX header — those are
  presentation hints only (§9).

### 11.8 Module tags and the `views/templ` → `views/goth` rename

At the untagged repository posture (confirmed empty `git tag --list` at preflight), the
feature view modules were **renamed in place** `features/<name>/views/{templ → goth}`
(GOTH-7.2/7.3): the module path and package changed, `go.work` + `Makefile` + consuming
host `go.mod`s were repointed, and no compatibility shim was added. If a relevant tag
ever exists when a future view module migrates, the additive fallback applies instead:
add a new `views/goth` module and retain the old path as a compatibility module rather
than renaming. Adopters pin `features/<name>/views/goth`.

### 11.9 SRI and the CDN-relocation caveat

The kit's own assets always carry Subresource Integrity (`sha384-…`) + `crossorigin`.
Two caveats for a host:

- **A configured host theme link carries no integrity.** When `ThemeStylesheetPath` is
  set, `Head()`/`Document()` emit that `<link>` verbatim with no `integrity` (the kit
  cannot know the host's bytes). A host that requires SRI on its own theme stylesheet
  must manually compose the `<head>` from `Bundle.Manifest()` and compute integrity for
  its own bytes — see the §6 composition-escape note.
- **CDN relocation breaks SRI + immutable caching.** The self-hosted model serves the
  fingerprinted assets under the host's own origin (`'self'`), which is what keeps
  `Requirements` at `'self'` with no remote origin. Relocating the `dist/` assets to a
  third-party CDN changes their origin — the host must then add that origin to its CSP
  `style-src`/`script-src` (widening past `'self'`), re-host the exact fingerprinted
  bytes so the manifest `Integrity` still matches (any CDN re-compression breaks SRI),
  and re-apply immutable caching itself (the SDK static server's `dist/`-prefix cache
  headers no longer apply). Self-hosting is the supported and tested path; CDN
  relocation is a host responsibility outside the kit's contract.

### 11.10 Theming a brand (the GPS/Segovia token override) and handoff

Theming is CSS-only (§5): the kit ships neutral contract fallbacks + a default palette,
and a host overrides tokens by serving a stylesheet loaded *after* the kit stylesheet
(`Config.ThemeStylesheetPath`). Brand values are override inputs, not part of the
contract — the token **names** generalize, specific values do not, and **no Segovia or
GPS360 code is imported into this repository.**

One rule is REQUIRED, not branding: **the kit reset deliberately leaves the document
surface unpainted** — the kit reset touches `<body>` only for `margin`/line
metrics and never sets a background or text color on it, so components theme
themselves but the page around them does not. A host theme MUST carry the `body`
paint rule below or dark mode flips the components and leaves the page background
behind (the exact symptom that surfaced this rule in the first adopter's dark-mode
screenshot). Because the rule reads the tokens, redeclaring `--background`/
`--foreground` in the dark block repaints both appearances — verify light AND dark.

A GPS-branded host ships its own stylesheet redeclaring the tokens it wants:

```css
/* the host's own brand stylesheet, served under 'self' and pointed at by     */
/* Config.ThemeStylesheetPath. This is illustrative GPS branding — no Segovia  */
/* code or asset is imported; only the frozen kit token NAMES are used.        */

/* REQUIRED: the kit reset leaves the document surface unpainted. */
body {
  background: var(--background);
  color: var(--foreground);
}

:root {
  --primary: oklch(0.52 0.17 250);           /* GPS brand blue */
  --primary-foreground: oklch(0.99 0 0);
  --ring: oklch(0.52 0.17 250);
  --radius: 0.375rem;
  --font-sans: "Inter", system-ui, sans-serif;
}
.dark, [data-theme="dark"] {
  --primary: oklch(0.70 0.15 250);
  --background: oklch(0.18 0.02 250);
  --foreground: oklch(0.98 0 0);
}
```

Clone `ui/goth/theme/default.css` as the starter (it enumerates every
appearance-dependent token), retint the values, serve it, and set
`ThemeStylesheetPath`. A host that already runs Tailwind can instead bridge the tokens
into its own Tailwind build (§5, "Using Tailwind in YOUR app") — that is an adopter
convenience; the kit never ships or recompiles Tailwind.

**Segovia/GPS360 handoff.** A downstream product (e.g. Segovia/GPS360) adopts `ui/goth`
by: (1) `go get`-ing the module and wiring a host per §11.1; (2) choosing a profile
(§11.2); (3) mapping `Requirements` into its CSP (§11.3) and running the boot-time
self-check (§11.5); (4) shipping a brand stylesheet redeclaring the token names above;
(5) consuming the primitives/`components/` layer and any feature `views/goth` adapters
it mounts, overriding only the pages it rebrands (§11.6). No kit source, brand value, or
downstream product code crosses into this repository — the handoff is the stable public
surface (`.goth-*` classes, `--token` names, the Go API in §1–§10) only.
