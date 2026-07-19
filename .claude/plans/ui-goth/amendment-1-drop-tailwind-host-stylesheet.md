# Amendment 1 — drop Tailwind from the kit build; host theme stylesheet becomes the theming channel

Status: **RATIFIED 2026-07-18 (owner, interactive session, after owner/Codex
review edits) — open questions resolved: D3 split asset; D4 root-relative
path-only as hardened; D5 delete outright. Execution GOTH-A.1→A.4 authorized
before GOTH-6.3 resumes.** Task prefix:
`GOTH-A`. Applies to the `ui-goth` milestone (plan.md, TASKS.md, gate-b-review.md).

## Context

On 2026-07-18 the owner ratified three decisions: (1) Tailwind is dropped 100%
from the goth stack — the T is templ; hosts MAY use Tailwind in their own build
against the stable `.goth-*` surface, as a documented adopter option only;
(2) the host's theme stylesheet becomes a first-class injection at wiring (the
WordPress model: kit ships compiled component CSS, host ships a stylesheet
loaded after it), with the kit default theme demoted to copyable scaffolding
that wiring injects only when the host supplies nothing; (3) the
`Config.Theme` → nonced-dynamic-stylesheet channel is removed — theming is
CSS-only. This is a scope-reduction amendment: it removes machinery, amends the
frozen GOTH-0.3 surface where those decisions require it, and adds no new scope.

Evidence base (verified 2026-07-18, this session):

- Primitives emit only stable semantic classes (`.goth-*`,
  `data-variant`/`data-slot`/`data-state`); zero non-`goth-*` class literals in
  any `.templ` under `ui/goth`. Component styles are hand-written plain CSS in
  `ui/goth/assets/src/css/components.css` (3,533 lines) consuming token custom
  properties; no `@apply`.
- Tailwind's only contribution to the compiled `theme.<hash>.css` is preflight
  (base reset) + `@import` inlining via the bundled postcss-import. Pinned
  3.4.19 in `ui/goth/tools/package.json`; config `ui/goth/tools/tailwind.config.cjs`.
- **No primitive or controller uses the nonced dynamic stylesheet.** `nonce`
  appears only in `ui/goth/goth.go`, `document.templ`/`document_templ.go`,
  `goth_test.go`, and the showcase host (plus htmx's own vendored internals).
  GOTH-4.1 anchored geometry rides `data-side`/`data-align` + CSSOM
  `--goth-anchor-*` writes, which CSP `style-src` does not govern. The
  plan.md:245 alternative ("documented nonced runtime stylesheet") was never
  exercised by a primitive — there is nothing to migrate to data-attributes.
- `esbuild` 0.28.1 bundles the Alpine/HTMX runtime JS; that part of the Node
  toolchain stays regardless.
- Stale claim at `ui/goth/assets/src/css/index.css:22` ("a caller's Base.Class
  utility can still override"): host utilities were never in the kit's compiled
  CSS (the content scan covered kit sources only). Must be fixed.
- The showcase wires `Nonce: nonceFromContext` on every bundle and builds a
  themed bundle via `Config.Theme` + nonce
  (`examples/goth-showcase/internal/showcase/showcase.go:70`, specimen
  `theme-nonced-override`, `TestNoncedOverrideEmitsNonceUnderStyleSrc`). No e2e
  `.ts` spec asserts on the nonce directly.

## Goal

The kit builds, tests, and showcases green with zero Tailwind anywhere in its
toolchain or docs, theming flows exclusively through a host stylesheet link
emitted after the kit stylesheet (defaulting to the kit's own compiled default
theme), and the entire nonce/dynamic-stylesheet channel is gone.

## Definition of Done

- `tailwindcss` absent from `ui/goth/tools/package.json`/lockfile;
  `tailwind.config.cjs` deleted; `make generate-ui-assets` reproducible
  (byte-identical on rebuild) on the pinned Node/esbuild toolchain alone.
- `goth.Config` = `{AssetBasePath, Profile, ThemeStylesheetPath}`;
  `Config.Theme`, `Config.Nonce`, `nonceStyle`, `themeOverrideCSS`,
  `Requirements.RequiresNonce`, `Bundle.Theme()`, and the `theme.Theme` value
  machinery are removed. `Head()` emits: kit CSS → theme stylesheet link
  (host path, or the embedded default) → scripts. The kit emits **no
  server-rendered `style` attribute and no inline `<style>` element** under any
  configuration. Controller-owned CSSOM custom-property writes remain allowed;
  they may serialize as a DOM `style` attribute after runtime activation but do
  not widen CSP and are never present in rendered HTML.
- Showcase demonstrates the host-stylesheet model with a real host-authored CSS
  file; all nonce plumbing removed; 3-engine `make test-ui-browser` green at
  **333 or higher** (the pre-amendment 330 plus one new host-theme test across
  three engines), with no test disappearance.
- `rg -i tailwind` over the repo (excluding `node_modules`, `.git`, lockfile
  history, and `.claude/`) matches only the adopter recipe ("using Tailwind in
  YOUR app"); the amendment records remain only under the excluded `.claude/`
  tree.
- The compiled-CSS delta is reviewed as an artifact: an unminified
  before/after diff showing only the preflight→owned-reset swap (plus import
  reordering), backed by a Layer-4 visual pass against pre-change baseline
  screenshots.
- `make check`, `make guard`, `make generate` (no-op) all green in the final
  integrated review/CI checkout, where the amendment's intended generated
  artifacts are part of the comparison baseline.

## Out of scope

- Any change to primitive markup, the `.goth-*` class surface, `data-*` hooks,
  controllers, or the §8 controller set (ten names, unchanged).
- The authentication resource-policy seam (Gate C surface) — unchanged; its
  script-nonce is the auth feature's own and unrelated to the removed kit
  style-nonce. GOTH-7.2's mapping merely gets simpler.
- Remaining Phase 6/7 tasks' content (GOTH-6.3–6.6, 7.1–7.5) beyond the impact
  map below.
- Editing Segovia/GPS360; a `gopernicus ui add` installer; any HTMX change.
- Renaming assets or modules; cutting tags (none exist for `ui/*`).

## Frozen items this amendment amends (Gate B reopen, owner-authorized)

Ratifying this amendment is the recorded owner reopen of GOTH-0.3 for exactly
these items ("changes to any name/shape reopen GOTH-0.3", README §10):

1. **README §2 `Config`**: `Theme` and `Nonce` fields removed;
   `ThemeStylesheetPath string` added; `New` error modes updated.
2. **README §4 `Requirements`**: `RequiresNonce()` removed. Nonce-coherence
   invariants (a)/(b) replaced by one stronger frozen invariant: *the kit emits
   no server-rendered `style` attribute and no inline `<style>` element —
   dynamic geometry uses `data-*` attributes + external CSS and controller-owned
   CSSOM custom-property writes only.* Gate B remediation
   **R3's resolution is superseded** (the contradiction it resolved no longer
   exists: there is no kit style-nonce channel).
3. **README §5 theme contract**: R5's partial-theme-composition freeze
   (`NewTheme` composes over `DefaultTheme`) is obsolete — there is no Go theme
   value to compose. The `goth.Theme`/`theme.Theme` identity resolution is
   obsolete. Token **names** stay frozen (they are the CSS contract); the
   override mechanism becomes "host stylesheet after kit stylesheet", which was
   already invariant 9's cascade story.
4. **README §3 manifest**: the frozen logical-name set gains
   `"theme-default.css"` (now four: `theme.css`, `theme-default.css`,
   `runtime.js`, `htmx.js`).
5. **`Bundle.Theme()`** removed from §2's method set.
6. **plan.md invariants**: invariant 6 loses the "documented nonced runtime
   stylesheet" alternative (data-attributes + external CSS is the only path);
   invariant 9's mechanism text is updated to the required-injection model;
   the Layer-2 bullet inventorying "primitives needing the nonced dynamic
   stylesheet" becomes "no server-rendered style attribute/inline style
   element, no CSP widening"; toolchain-policy text drops Tailwind.
7. **README §10 wiring specimen** rewritten without `Nonce`/`Theme` and with
   the theme-stylesheet link ordering.

Everything else frozen at Gate B (primitive grammar, `MergeAttributes`, ID
factory, §8 controllers, §9 HTMX, manifest/`Asset` shape, asset-serving recipe,
profiles) is untouched.

## Design decisions (recommendations — review these closely)

### D1. The CSS build stays on Node, driven by esbuild

esbuild (already pinned, 0.28.1) natively bundles CSS: it resolves `@import`
and minifies. `buildCss()` in `tools/build.mjs` becomes an `esbuild.build`
call over the CSS entry instead of shelling out to the Tailwind CLI. No new
dependency; `postcss-import` (which arrived bundled inside Tailwind) is no
longer needed; deterministic output is preserved. A no-Node/pure-concatenation
CSS path was considered and rejected: Node is already required for the JS
assets, and esbuild gives minification + import resolution for free.

### D2. Owned reset, written from scratch (~50 lines)

`ui/goth/assets/src/css/reset.css`, authored for this kit (informed by widely
known reset practice, no copied text): border-box sizing, margin zeroing,
`-webkit-text-size-adjust`, media defaults (`img/svg/video: display:block;
max-width:100%`), form controls inherit font, button reset,
`text-wrap: balance`-free (keep minimal), list/anchor left alone (components
own their styling). Vendoring Tailwind's preflight (MIT) would be
licensing-clean but violates "dropped 100%" — no Tailwind-derived text ships.
The reset is imported **first** in `index.css` (today `@tailwind base` sat
*after* `components.css` due to postcss-import ordering; the new order is
normal-cascade and is called out in the diff review). Component rules keep
winning by class/attribute specificity. Tailwind's removal also deletes
`@tailwind components/utilities` (both contributed nothing: no `@apply`, no
scanned utility classes).

### D3. The default theme becomes a real, separate artifact

`theme/default.css` (the light/dark default palette) is **removed from the
compiled `theme.css`** and compiled into its own fingerprinted asset,
`theme-default.css`. `theme.css` keeps: owned reset + `theme/contract.css`
(neutral fallbacks, so nothing is ever unstyled) + `components.css` +
`.goth-sr-only`. This is the honest reading of "not silently baked in": the
default theme is an artifact wiring injects, not bytes fused into the
component CSS. `theme/default.css` doubles as the documented **copyable
starter file** for hosts writing their own stylesheet.

### D4. Config surface

```go
type Config struct {
    AssetBasePath string // unchanged
    Profile       Profile // unchanged

    // ThemeStylesheetPath is the root-relative public path of the HOST's theme
    // stylesheet. Head() emits it as a <link rel="stylesheet"> AFTER the kit
    // stylesheet
    // (source-order cascade: the host wins). Empty selects the kit's embedded
    // compiled default theme (theme-default.css) — the WordPress model's
    // fallback. A non-empty value must begin with exactly one "/" and is emitted
    // verbatim. A scheme, authority/host form, backslash, "..", control
    // character, query, or fragment is a construction error.
    ThemeStylesheetPath string
}
```

- Head emission order (frozen): `theme.css` link → theme stylesheet link
  (validated root-relative host path verbatim, or the manifest's
  `theme-default.css` under
  `AssetBasePath` with integrity + crossorigin) → deferred scripts. The host
  link carries **no** integrity attribute because the kit cannot know host
  bytes. The first-class configured host-theme path deliberately does not
  support SRI in this amendment; `HeadExtra` is not presented as an equivalent
  substitute because it would retain the default-theme link or duplicate the
  configured link and would not preserve the frozen pre-script ordering. A host
  that requires SRI must manually compose the complete head from `Manifest()`
  rather than call `Bundle.Head()`/`Bundle.Document()`.
- Root-relative path-only is deliberately conservative: widening to absolute
  URLs later is compatible; narrowing is not. Validation uses browser URL
  semantics, not merely `net/url`'s `Scheme`/`Host` fields: reject `//...`,
  `///...`, any additional leading slash, and any `\\` so a value Go parses as a
  path can never resolve as a remote authority in a browser. The validation
  matrix includes those adversarial forms. Invariant 4 (no remote runtime
  dependency) keeps applying to kit defaults.
- Zero `Config` remains valid: StylesOnly + default path + injected default
  theme. "Required at wiring" is satisfied by the field being first-class and
  loudly documented, with the default-theme fallback the owner allowed
  ("maybe it sets up as injecting the default theme").

### D5. Go theme package: slim, not retain

Remove `theme.Theme`, `NewTheme`, `DefaultTheme`, `Value`, `Values`,
`ErrInvalidToken`/`ErrInvalidValue` validation machinery, the `goth.Theme`
alias, and `Bundle.Theme()` — with `Config.Theme` gone they have zero
consumers, and the scaffolding artifact is the CSS file itself, not a Go
value. Keep: `Token` constants + `Tokens()` (frozen public vocabulary),
`Appearance`/`Direction`/`HTMLAttributes` (used by `DocumentOptions` and the
showcase), and the Go↔CSS alignment test (`contract_css_test.go` keeps
guarding that the token list matches `contract.css`/`default.css`; the
default-values map may become unexported test data). Rationale: smallest
honest surface; retaining a validated-value type "for scaffolding" is
machinery with no caller — the rejected alternative.

### D6. CSP collapses; Requirements shrinks

With no server-rendered style attribute or inline style element, `style-src`
needs are exactly `'self'` (kit CSS + default theme are self-hosted; the
root-relative host stylesheet is host-served under 'self' too). Controller
CSSOM custom-property writes remain CSP-permitted. `Requirements` keeps
`Sources`/`Directives` (still needed for
`script-src 'self'` at Interactive/Full and for the GOTH-7.2 policy mapping);
only `RequiresNonce` and the internal `requiresNonce` plumbing are deleted.
This is a pre-tag breaking change to the module surface; no `ui/*` tag exists
(preflight evidence), so no RELEASING.md upgrade note is owed — recorded here
instead.

## Module / API impact

- `ui/goth` (one module): exported surface shrinks — removals per D4/D5/D6,
  one added `Config` field. No `go.mod`/`go.work` change; no new module; no
  tagging action (untagged).
- `examples/goth-showcase`: internal rework only.
- Guards unaffected (G17 and the require-whitelist keep passing; nothing new
  imported).

## Generated-artifact impact

- `ui/goth/document.templ` edited (drop the nonced-style branch; add the theme
  link) → regenerate via `make generate`; `document_templ.go` never
  hand-edited.
- `ui/goth/assets/dist/*` + `manifest.json` regenerated (Node-gated
  `make generate-ui-assets`); `theme.css` hash changes, `theme-default.css`
  appears, `runtime.js`/`htmx.js` must stay byte-identical in GOTH-A.1 and in
  GOTH-A.2 (no JS source change is part of this amendment).
- `tools/package-lock.json` regenerated after the `tailwindcss` pin removal.

## Risks

1. **Preflight→owned-reset visual regressions** (the only visual-risk change):
   form controls, headings, and image defaults are preflight-sensitive.
   Mitigated by baseline screenshots captured *before* GOTH-A.1, the
   unminified diff artifact, the 3-engine suite (330 tests incl. axe), and a
   human Layer-4 pass. Severity: medium, contained.
2. **Frozen-surface churn mid-milestone**: Phase 6/7 tasks and the GOTH-6.6
   audit must run against the amended contract. Mitigated by sequencing the
   amendment before GOTH-6.3 resumes and recording the reopen here.
3. **Intermediate red states across modules**: removing `Config.Nonce` breaks
   the showcase compile if split across tasks — so kit + showcase land as one
   task (GOTH-A.2) and every task ends compile/test/guard-green with stable
   generated outputs. The integrated amendment ends full `make check`-green.

## Verification convention

- Reproducibility is proved by recording the sorted file list + SHA-256 for
  `ui/goth/assets/dist/*` and `ui/goth/assets/manifest.json` after the first
  build, rebuilding, and comparing the second snapshot byte-for-byte. Global
  `git status` cleanliness is never used as evidence: the implementation may
  run in a worktree containing unrelated owner changes, which remain untouched.
- Task-local verification runs build/vet/test/guard directly against the working
  tree. The final `make check` assertion applies to the integrated review/CI
  checkout after the intended generated artifacts are part of its comparison
  baseline; this amendment authorizes no commit, tag, or PR action.

## Tasks

### GOTH-A.1: de-Tailwind the kit CSS build (owned reset + esbuild CSS)

- **depends_on:** [owner ratification of this amendment]
- **model:** opus
- **files:**
  - `ui/goth/assets/src/css/reset.css` (new, owned, ~50 lines)
  - `ui/goth/assets/src/css/index.css` (drop `@tailwind` directives; import
    reset first; fix the stale line-22 Base.Class comment and the
    postcss-import/Tailwind references)
  - `ui/goth/tools/build.mjs` (`buildCss` → esbuild CSS bundle+minify)
  - `ui/goth/tools/package.json` (remove `tailwindcss`)
  - `ui/goth/tools/package-lock.json` (regenerated)
  - `ui/goth/tools/tailwind.config.cjs` (deleted)
  - `ui/goth/assets/THIRD_PARTY_NOTICES.md` (drop Tailwind entry; record the
    owned reset)
  - `ui/goth/assets/dist/*`, `ui/goth/assets/manifest.json` (regenerated)
- **verify:** capture BASELINE Layer-4 screenshots first (showcase index +
  representative form/typography/overlay specimens, light/dark, one RTL) to
  the scratchpad; build an unminified before/after `theme.css` pair and retain
  the diff as a review artifact (expected semantic delta: preflight→reset +
  import order only; esbuild source-boundary comments/formatting are normalized
  out of the comparison). Then: `cd ui/goth/tools && npm install && npm ci
  --ignore-scripts`; `make generate-ui-assets` twice → compare the sorted
  file-list + SHA-256 snapshots per the verification convention (byte-identical;
  `runtime.js`/`htmx.js` hashes unchanged);
  `cd ui/goth && go build ./... && go test ./... && go vet ./...`;
  `make warm-scaffold-cache && make build && make vet && make test && make guard`;
  `make test-ui-browser` (3 engines, ≥330 passed). Run-and-look: `make run`
  equivalent for the showcase
  (`cd examples/goth-showcase && go run ./cmd/server`), eyeball the baseline
  specimens against the captured screenshots.
- **description:** Remove Tailwind from the asset pipeline. Author the owned
  reset from scratch (no vendored preflight text), switch CSS compilation to
  the already-pinned esbuild, delete the Tailwind config and pin, and prove
  the compiled output delta is exactly the reset swap. `theme.css` still
  contains the default palette in this task (the split is GOTH-A.2, so the
  page never loses its palette between tasks).

### GOTH-A.2: new Config surface, nonce-channel removal, default-theme asset split, showcase rework

- **depends_on:** [GOTH-A.1]
- **model:** opus
- **files:**
  - `ui/goth/tools/build.mjs` (second CSS entry → `theme-default.css` asset)
  - `ui/goth/assets/src/css/index.css` (drop the `default.css` import)
  - `ui/goth/goth.go` (Config per D4; delete `themeOverrideCSS`, `nonceStyle`,
    `dynamicCSS`, `requiresNonce`, `Bundle.Theme`, `goth.Theme` alias,
    `DefaultTheme` re-export; head model gains the theme link)
  - `ui/goth/document.templ` (+ regenerated `ui/goth/document_templ.go`)
  - `ui/goth/theme/theme.go` (slim per D5; keep Token/Tokens/Appearance/
    Direction/HTMLAttributes and the CSS-alignment test data)
  - `ui/goth/goth_test.go`, `ui/goth/theme/*_test.go` (rework: link-ordering
    test kit-CSS→theme-link→scripts; no-server-rendered-style assertion;
    Requirements without nonce; host-path validation matrix including relative,
    `//`, `///`, additional-leading-slash, and backslash rejection)
  - `ui/goth/assets/dist/*`, `ui/goth/assets/manifest.json` (four assets)
  - `examples/goth-showcase/internal/showcase/showcase.go` (delete
    `nonceContextKey`/`setNonce`/`newNonce`/`nonceFromContext`; `buildCSP`
    loses the nonce parameter; themed bundle → a bundle with
    `ThemeStylesheetPath` pointing at a showcase-served host stylesheet; serve
    the embedded host CSS with an explicit `text/css; charset=utf-8` response)
  - `examples/goth-showcase/internal/showcase/host_theme.css` (new, embedded:
    overrides `--primary` and `.goth-badge { border-radius: 0; }` — proves both
    token- and class-level override)
  - `examples/goth-showcase/internal/showcase/registry.go`,
    `specimens.go` (`theme-nonced-override` → `theme-host-stylesheet`)
  - `examples/goth-showcase/internal/showcase/showcase_test.go`
    (`TestNoncedOverrideEmitsNonceUnderStyleSrc` → assert link order, zero
    `<style` in output, configured host link has no integrity attribute, CSP
    `style-src 'self'` with no nonce)
  - `examples/goth-showcase/internal/showcase/specimens_primitives_*.go`
    (mechanical: drop `newNonce`/`setNonce` call sites in fixture handlers)
  - `examples/goth-showcase/e2e/tests/*` (sweep for the renamed specimen path;
    no existing spec asserts on the nonce directly — verified)
  - `examples/goth-showcase/e2e/tests/theme.spec.ts` (new: in all three engines,
    assert kit-CSS→host-theme→scripts source order; custom page omits
    `theme-default.css`; host stylesheet returns `text/css`; computed `--primary`
    equals the host value; computed badge radius is `0px`)
- **verify:** `make generate` (templ drift no-op after regen);
  `make generate-ui-assets` twice → byte-identical, manifest has exactly
  `htmx.js`/`runtime.js`/`theme-default.css`/`theme.css`;
  `cd ui/goth && go build ./... && go test ./... && go vet ./...`;
  `cd examples/goth-showcase && go build ./... && go test ./... && go vet ./...`;
  `make warm-scaffold-cache && make build && make vet && make test && make guard`;
  `make test-ui-browser` (3 engines, ≥333, including the new host-theme proof).
  Run-and-look: `go run ./cmd/server`, open the index, a default-theme
  specimen (default palette present via the injected `theme-default.css`
  link), and `/…/theme-host-stylesheet` (primary color + badge visibly
  overridden), confirm view-source shows kit-CSS→theme-link→script order, zero
  inline `<style>`, and no server-rendered `style=`; confirm the console shows no
  CSP error.
- **description:** Land the new `Config` surface and delete the entire nonce/
  dynamic-stylesheet channel atomically across kit and showcase, splitting the
  default palette into its own injected artifact per D3/D4. Kit and showcase
  move together so every task boundary is compile/test/guard-green with stable
  generated output; A.4 owns the final integrated `make check` proof.

### GOTH-A.3: documentation — stack description, frozen-surface text, adopter Tailwind recipe

- **depends_on:** [GOTH-A.2]
- **model:** opus
- **files:**
  - `ui/goth/README.md` (intro/stack: T = templ, Tailwind nowhere in the
    stack; §2 Config; §3 manifest four-asset set; §4 requirements + the single
    no-server-rendered-style/inline-style-element invariant; §5 theme contract → host-stylesheet override
    story, `theme/default.css` as the copyable starter, plus a short adopter
    recipe "Using Tailwind in YOUR app against the `.goth-*` surface"
    including the v4 token bridge, e.g.
    `@theme { --color-primary: var(--primary); }`, and a note that any
    Tailwind version is the host's build, never the kit's; §6 document
    composition; explicit configured-host-link SRI limitation/manual-Manifest
    composition escape; §10 ownership + rewritten wiring specimen)
  - `ui/README.md` (family charter: goth = templ + plain CSS + Alpine CSP +
    optional HTMX)
  - `ARCHITECTURE.md`, `RELEASING.md` (drop the Tailwind mentions in the UI
    rows/notes)
  - `features/authentication/README.md`,
    `features/authentication/internal/inbound/authentication/policy.go`
    (comment-level "templ, Tailwind, Alpine, HTMX" enumerations → drop
    Tailwind)
  - `ui/goth/theme/theme.go`, `ui/goth/assets/assets.go` (stale
    Tailwind-referencing comments)
  - `ui/goth/theme/contract.css` (drop the stale "no Tailwind recompilation"
    wording)
  - `ui/goth/theme/default.css` (rewrite the stale "imported/compiled into
    theme.css" comments for the standalone `theme-default.css` artifact)
  - `examples/goth-showcase/e2e/README.md` (replace the obsolete Go-suite
    "nonce coherence" claim with host-theme link/cascade coverage)
- **verify:** `rg -i tailwind` across the repo (excluding `node_modules`,
  `.git`, `.claude/`, `package-lock.json`) matches only the adopter recipe;
  an `rg` sweep for the removed-channel terms (`nonced dynamic stylesheet`,
  `nonce coherence`, `Config.Nonce`, `RequiresNonce`, `style-nonce`)
  under `ui/goth` + `examples/goth-showcase` finds no removed kit style-nonce
  channel (auth's independent script nonce and htmx vendored internals are out of
  scope); `make warm-scaffold-cache && make build && make vet && make test &&`
  `make guard` (comment edits touch Go files → rebuild proof).
- **description:** Make every document tell the new truth: Tailwind is not in
  the stack; theming is the host stylesheet; the nonce sections are gone.
  Add the adopter recipe as the one sanctioned place Tailwind is mentioned.

### GOTH-A.4: milestone bookkeeping and amendment closeout

- **depends_on:** [GOTH-A.3]
- **model:** opus
- **files:**
  - `.claude/plans/ui-goth/plan.md` (invariants 6/9, toolchain/asset policy,
    Layer-2 bullets, bundle-profile sketch, auth-policy nonce sentence —
    the auth script-nonce text stays, the kit style-nonce text goes)
  - `.claude/plans/ui-goth/TASKS.md` (status header + board addendum:
    GOTH-1.2/1.3/1.4/1.5 deliverables amended in place per the GOTH-5.5
    precedent — completed tasks are NOT unchecked; GOTH-A.1–A.4 recorded)
  - `.claude/plans/ui-goth/gate-b-review.md` (dated addendum: amendment-1
    ratified; R3 resolution and R5's composition freeze superseded; frozen
    items list per this file)
- **verify:** `make check && make guard && make generate` (no-op) and
  `make test-ui-browser` one final time (3 engines, ≥333) as the closeout matrix;
  final
  run-and-look screenshot set (light/dark/RTL, narrow/wide) filed next to the
  GOTH-A.1 baselines.
- **description:** Apply the ratified amendment to the milestone records and
  close with the full verification matrix. This task, not the amendment
  draft, is when plan.md/TASKS.md change.

## Sequencing

GOTH-A.1 → A.2 → A.3 → A.4, strictly. The amendment executes **before
GOTH-6.3 resumes**, so the remaining Phase-6 primitives (P58/P61/P63–P64 —
Chart and Toast being the likeliest to be tempted by server-rendered inline
styles) are built on the final stack and the GOTH-6.6 64-entry audit covers the amended
foundation exactly once.

## Task impact map (existing board)

| task | state | impact |
|---|---|---|
| GOTH-1.2 (asset pipeline) | done | amended in place by A.1 (Tailwind out, esbuild CSS in); not unchecked (GOTH-5.5 precedent) |
| GOTH-1.3 (theme + nonced stylesheet) | done | nonced-stylesheet deliverable removed by A.2; token/appearance work intact |
| GOTH-1.4 (requirements/CSP tests, controllers) | done | requirements tests reworked by A.2; controllers untouched; its inventory answered the "does anything use the nonce" question: nothing does |
| GOTH-1.5 (showcase) | done | reworked by A.2 (nonce plumbing out, host-stylesheet specimen in) |
| GOTH-2.x–6.2 | done | untouched (primitives never referenced the nonce or any Tailwind class) |
| GOTH-6.3 | partial | shipped P57/P62 stay intact; remaining P61 now depends on GOTH-A.4 in addition to its controller decision |
| GOTH-6.4, 6.5 | open | unchanged content; now depend on GOTH-A.4 in addition to the Phase 5 gate |
| GOTH-6.6 | open | audit runs against the amended contract (no server-rendered style attribute/inline style element; CSSOM exception explicit) |
| GOTH-7.1 | open | unchanged |
| GOTH-7.2 | open | simpler: the adapter maps `Requirements` without any `RequiresNonce`→style-nonce branch; the auth policy's own script-nonce (Gate C, C1) is unaffected |
| GOTH-7.3 | open | unchanged |
| GOTH-7.4 | open | adoption/theming docs largely pre-paid by A.3; keep the task for the auth/CMS-specific recipes and cross-link the Tailwind adopter recipe |
| GOTH-7.5 | open | final audit inherits the amended invariants |

Obsoleted freeze artifacts: Gate B R3's resolution and R5's
partial-theme-composition freeze (superseded, recorded in A.4's gate-b
addendum). No task is deleted; none of the 38 numbered tasks is removed —
this amendment adds 4 (total 42).

## Consultation notes

No lead consulted this pass. The three owner decisions are prescriptive, the
open design points (D1–D6) resolve from verified code evidence (notably: zero
nonce consumers among primitives, zero Tailwind class literals), and this is a
scope reduction against an already Gate-B-reviewed surface. The Config-surface
recommendations (D3/D4/D5) are flagged above for the owner's close review, and
lead-frontend-engineer + platform-sre are listed below as post-hoc reviewers
of exactly those sections.

## Open questions

1. **D3 split** — confirm the default palette moves to a separate
   `theme-default.css` asset (recommended) rather than staying compiled into
   `theme.css` with the host link merely appended after. The split is the
   literal reading of "not silently baked in"; the non-split is one fewer
   asset. **YOUR CALL at ratification.**
2. **D4 root-relative path-only** — confirm `ThemeStylesheetPath` requires
   exactly one leading `/`, rejects browser authority forms (`//`, `///`,
   additional leading slashes) and backslashes, and rejects absolute URLs for
   now (widening later is compatible).
3. **D5 slim** — confirm deleting the Go `Theme` value type outright versus
   keeping a validated map for future scaffolding tooling (recommended:
   delete; `gopernicus ui add`-era tooling can re-introduce what it needs).

## Recommended reviews

- **product-manager** — scope discipline: confirm this stays a reduction and
  the 4-task insertion before GOTH-6.3 is the right sequencing cost.
- **lead-frontend-engineer** — D2 reset coverage, D4 Head ordering/validation,
  the reworked §2/§4/§5 text, and the showcase host-stylesheet specimen.
- **platform-sre** — D6 CSP collapse, the explicit lack of SRI support on the
  first-class host link, browser-safe root-relative path validation, lockfile
  regeneration hygiene, and the reproducible-build verify steps.
- **design-system-reviewer** — the preflight→owned-reset visual delta and the
  Layer-4 baseline/after comparison.

## Notes

- The `.goth-sr-only` utility stays in the compiled `theme.css` (it is
  component machinery, not theme).
- `contract.css` neutral fallbacks staying in `theme.css` is what makes a
  missing/broken host stylesheet degrade to readable neutral pages rather
  than unstyled HTML — the WordPress analogy's safety net.
- "No inline style" in this amendment always means no server-rendered `style`
  attribute and no inline `<style>` element. Controller-owned
  `element.style.setProperty` writes are intentionally retained; they may appear
  in the live DOM after activation but are absent from rendered HTML and do not
  require CSP widening.
- htmx's vendored source mentions nonces internally (its own config surface);
  the kit does not use that surface — no action.

## GOTH-A.1 execution evidence (2026-07-18)

Executed GOTH-A.1 exactly as scoped (kit CSS build only; `Config`/nonce/
default-theme split are GOTH-A.2 and were NOT touched — `theme.css` still carries
the default palette this task). Worktree runs with unrelated owner changes present;
global `git status` was not used as evidence (verification convention).

**Changes made**

- `ui/goth/assets/src/css/reset.css` — NEW, ~90 lines, owned reset authored from
  scratch (no vendored/copied preflight text): border-box on `*`; `html`
  line-height/`-webkit-text-size-adjust`/tab-size + base `font-family:
  var(--font-sans)`; `body` margin/line-height; margin-zero on headings/p/
  figure/blockquote/dl/dd; heading font-size/weight inherit; media block +
  max-width:100% + height:auto; form controls `font/color/margin/padding` reset;
  button background/border reset + `cursor:pointer`; textarea `resize:vertical`;
  table `border-collapse`. Lists/anchors/borders deliberately left to the
  component layer (D2) — verified components own all list-style (7 rules) and use
  explicit `border:… solid` shorthand (68 rules), so no base rule is relied on.
- `ui/goth/assets/src/css/index.css` — `@import "./reset.css"` FIRST; dropped the
  three `@tailwind` directives; fixed the stale line-22 `Base.Class` comment and
  the postcss-import/Tailwind references. Restored `[hidden] { display: none }`
  placed LAST (after components), mirroring the previous Tailwind preflight's
  source-order-last position, so it beats a component `display` at equal
  specificity (see regression note below).
- `ui/goth/tools/build.mjs` — `buildCss()` now calls `esbuild.build` over the CSS
  entry (bundle + minify, `loader { .css: css }`), replacing the Tailwind CLI +
  temp-dir shell-out. A `CSS_TARGET` (`chrome111,edge111,firefox111,safari15`)
  preserves the functionally significant vendor prefixes the old autoprefixer
  emitted (notably `-webkit-appearance` for custom form controls). Removed the
  now-orphaned `execFileSync`/`mkdtempSync`/`rmSync`/`tmpdir` imports; `buildCss`
  is awaited in `main`.
- `ui/goth/tools/package.json` — removed the `tailwindcss` pin.
- `ui/goth/tools/package-lock.json` — regenerated (`npm install`): 74
  tailwind-related packages removed; 0 tailwind refs; `npm ci --ignore-scripts`
  clean (32 packages, 0 vulnerabilities).
- `ui/goth/tools/tailwind.config.cjs` — deleted.
- `ui/goth/assets/THIRD_PARTY_NOTICES.md` — dropped the Tailwind runtime-dependency
  row; recorded the owned reset + esbuild-CSS build; updated obligation #1 and the
  esbuild tool row; refreshed the SHA-256 records (also corrected the runtime/htmx
  rows that had drifted to GOTH-1.5-era values across Phases 2–6).
- `ui/goth/assets/dist/*` + `manifest.json` — regenerated via
  `make generate-ui-assets`.

**Compiled-CSS delta review** (unminified before[Tailwind]/after[esbuild] pair +
selector-keyed normalized diff, retained at
`…/goth-a1-baselines/{theme.before.unmin.css,theme.after.unmin.css,normalized-diff.txt}`).
Of 673 canonicalized rules: **616 byte-identical**. The delta is exactly:
(1) Tailwind preflight base-element rules → the owned reset (86 preflight/base
selectors removed, reset selectors added); (2) the universal preflight
`*{ border-style:solid; border-width:0; --tw-* }` block removed — dead: components
set borders with explicit `border:… solid`, verified; (3) dead unused Tailwind
utility classes (`.flex`/`.block`/`.container`/`.w-full`/`.mt-4`/… + the `--tw-*`
custom-property engine) removed — verified zero non-`goth-*` class literals in any
templ/go, so nothing referenced them; (4) 57 rules where esbuild's target dropped
pre-2020 vendor prefixes (`-moz-appearance`, `-moz-user-select`, `-o-object-fit`,
reduced-motion `-webkit-transition`, `-moz-*-content`, `-moz-tab-size`,
`::-moz-placeholder`) while retaining the unprefixed property and the
functionally-significant `-webkit-appearance`/`-webkit-user-select`/
`-webkit-text-size-adjust`; (5) import order (reset first vs preflight last).
`oklch()` (117×) and `:has()` pass through unchanged; esbuild build is warning-free.

**Regression found + fixed at source (kit):** the first `make test-ui-browser` run
had 6 failures (Combobox P50 + Command P51 filtering, ×3 engines): options carrying
the `hidden` attribute rendered visible because `.goth-command-item`/
`.goth-combobox-option` set `display:flex` and the components have NO `[hidden]`
handling — they had silently relied on Tailwind preflight's source-last
`[hidden]{display:none}`. Restoring `[hidden]{display:none}` last in `index.css`
fixed all 6; re-run **330 passed** across Chromium/Firefox/WebKit, 0 failed
(incl. axe + strict-CSP specs).

**Reproducibility / hashes** — `make generate-ui-assets` run twice, `dist/` +
`manifest.json` **byte-identical** (sorted file-list + SHA-256 snapshots compared).
`runtime.js` `2b6cbcd1…` and `htmx.js` `8689e2e2…` **UNCHANGED** (no JS source
touched). `theme.css` `3aaa0998`→`533fe4bd` (78088B→70973B; smaller: preflight +
dead utilities gone, reset + `[hidden]` added).

**Verification results**
- `cd ui/goth && go build ./… && go test ./… && go vet ./…` — all green
  (incl. `assets` embed/manifest test).
- `make warm-scaffold-cache && make build && make vet && make test && make guard`
  — all green (guard exit 0; ui/* guards pass; nothing new imported).
- `make test-ui-browser` — **330 passed**, 0 failed, 3 engines.

**Run-and-look** (baselines captured BEFORE any change; after captured from the
rebuilt server serving `theme.533fe4bd.css`; both under `…/goth-a1-baselines/`):
every kit primitive specimen (form controls, typography incl. its list variant,
table, dialog/sheet overlays, buttons, inputs — light/dark + one RTL) is
pixel-identical or differs only by text antialiasing (viewed the Input specimen
directly: bordered/rounded fields, file button, red-invalid, muted-disabled all
correct; `-webkit-appearance` retention confirmed on native controls). The one
visible delta is on the **showcase host index chrome**: its bare `<ul>`/`<a>`
nav now shows default UA styling (blue underlined links; bulleted/indented lists;
+160px page height) because the ratified D2 reset deliberately leaves anchors/lists
alone and the host index does not use `.goth-*` classes. This is an EXPECTED
consequence of D2 confined to dev-showcase chrome (no kit primitive regresses; no
test asserts on it); the showcase is reworked in GOTH-A.2. Not fixed here (kit-only
scope; fixing it would contradict D2).

Stale `rg -i tailwind` hits remain across README/docs/comments — those are GOTH-A.3's
scope, intentionally not touched. `plan.md`/`TASKS.md` task boxes untouched (GOTH-A.4).

## GOTH-A.2 execution evidence (2026-07-18)

Executed GOTH-A.2 exactly as scoped (new `Config` surface, entire nonce/dynamic-
stylesheet channel deleted, default-theme asset split per D3, Go `Theme` value
machinery deleted per D5, showcase rework). Kit + showcase landed together so every
boundary is compile/test/guard-green. Worktree runs with unrelated owner changes
present (`ui/` is untracked on this branch); global `git status` was NOT used as
evidence — reproducibility proved by SHA-256 snapshots (verification convention).
Docs/README (A.3) and plan.md/TASKS.md boxes (A.4) intentionally untouched.

**Kit changes (`ui/goth`)**

- `tools/build.mjs` — added a SECOND CSS entry: `buildCssEntry(entry)` generalizes
  the esbuild CSS bundle+minify; `buildCss()` compiles the kit stylesheet,
  `buildDefaultCss()` compiles `theme/default.css` into the standalone
  `theme-default.css` asset; `main` emits four assets in logical-name order.
- `assets/src/css/index.css` — dropped the `@import "../../../theme/default.css"`
  so the default palette is no longer baked into `theme.css`; reset + neutral
  contract + components + `.goth-sr-only` + `[hidden]` remain. Comment rewritten to
  the D3 injected-asset story.
- `goth.go` — `Config = {AssetBasePath, Profile, ThemeStylesheetPath}`. Deleted the
  `goth.Theme` alias, `DefaultTheme` re-export, `Config.Theme`, `Config.Nonce`,
  `themeOverrideCSS`, `nonceStyle`, `Bundle.theme/nonce/dynamicCSS`, `Bundle.Theme()`,
  `Requirements.requiresNonce`, `Requirements.RequiresNonce()`. Added
  `normalizeThemeStylesheetPath` (D4 hardened, browser-URL semantics: rejects
  relative, `//`, `///`, additional leading slash, backslash, scheme, authority,
  `..`, control char, query, fragment; accepts exactly-one-leading-slash and emits
  verbatim). `Bundle.themeResource()` computes the theme link (host path verbatim,
  NO integrity; else manifest `theme-default.css` under AssetBasePath WITH
  integrity+crossorigin). Head model: kit CSS → theme link → deferred scripts.
  Removed now-unused `context`/`io` imports.
- `document.templ` (+ regenerated `document_templ.go` via `make generate`) — the
  nonced-`<style>` branch is gone; headTags emits the theme link (integrity branch
  vs verbatim-no-integrity branch) between the kit styles and the scripts.
- `theme/theme.go` — slimmed per D5: kept `Token` constants, `Tokens()` (now backed
  by an unexported `frozenTokens` slice), `Appearance`/`Direction`/`HTMLAttributes`.
  Removed `Theme`, `NewTheme`, `DefaultTheme`, `Value`, `Values`, `IsZero`,
  `ErrInvalidToken`/`ErrInvalidValue`, `validateValue`, `cloneValues`, and the
  exported `defaultTokenValues`.
- `theme/contract_css_test.go` — `defaultTokenValues` moved here as UNEXPORTED test
  data; `TestDefaultCSSMatchesDefaultTheme` now compares the resolved light palette
  against it; added `TestDefaultTokenValuesCoverFrozenSet`.
- `theme/theme_test.go` — removed the deleted-machinery tests; kept the 59-token
  count + HTMLAttributes; added a determinism/uniqueness check.
- `goth_test.go` — reworked: adversarial `TestNewThemeStylesheetPathValidation`
  matrix (relative, `//`, `///`, `////`, backslash, leading backslash, http/https
  scheme, `javascript:`, `..`, query, fragment, control char, newline);
  `TestHeadDefaultThemeLink` (order kit→default→script, integrity present);
  `TestHeadHostThemeLink` (verbatim, NO integrity, default asset absent, after kit
  CSS); no-server-rendered-style + no `style=` assertions; Requirements without a
  nonce; `TestEmbeddedManifestHasFourAssets`.
- `assets/assets_test.go` — `TestExpectedAssetSet` locks the FOUR-asset set
  (adds `theme-default.css`).
- `assets/dist/*` + `manifest.json` — regenerated. Manifest is EXACTLY
  `{htmx.js, runtime.js, theme-default.css, theme.css}`.

**Showcase changes (`examples/goth-showcase`)**

- `internal/showcase/host_theme.css` — NEW embedded host stylesheet: `:root{--primary:
  oklch(0.55 0.22 265)}` (token) + `.goth-badge{border-radius:0}` (class).
- `internal/showcase/showcase.go` — deleted `nonceContextKey`/`setNonce`/`newNonce`/
  `nonceFromContext`; `buildCSP` lost the `nonce` param and the RequiresNonce branch
  (style-src is now `'self'` only). Bundles built with no `Nonce`. The `themed`
  bundle is now Interactive + `ThemeStylesheetPath: "/theme/host.css"`. Added
  `serveHostTheme` (serves the embedded CSS as `text/css; charset=utf-8`) and its
  route. Handlers use `r.Context()`.
- `internal/showcase/registry.go` — `UseThemedBundle` comment updated (host
  stylesheet, not nonce).
- `internal/showcase/specimens.go` — `theme-nonced-override` → `theme-host-stylesheet`
  with a new `hostStylesheetSampleBody` (real `.goth-badge` + `.goth-button`); the
  page-two fixture handler's nonce plumbing removed.
- `internal/showcase/specimens_primitives_{anchored,compact,data,date,messaging,
  palette,selection,sidebar}.go` — mechanical `newNonce`/`setNonce` removal;
  `buildCSP(bundle, …)` and `web.Render(r.Context(), …)`.
- `internal/showcase/showcase_test.go` — `TestNoncedOverrideEmitsNonceUnderStyleSrc`
  replaced by `TestHostStylesheetSpecimenLinksHostThemeNoNonce` (link order, zero
  `<style`, zero `style=`, no-integrity host link, style-src `'self'` no nonce) +
  `TestHostThemeStylesheetServedAsCSS`.
- `e2e/tests/theme.spec.ts` — NEW, 5 tests × 3 engines: kit→host→script source
  order + verbatim no-integrity host link + `theme-default` omission on the host
  page; a default specimen DOES link `theme-default.css`; host stylesheet served as
  `text/css`; computed `--primary` equals `oklch(0.55 0.22 265)`; computed badge
  `borderTopLeftRadius` is `0px`. No existing spec referenced the nonce or the old
  specimen path (verified) — no sweep edits were needed.

**Verification results**

- `make generate` — templ NO-OP: `document_templ.go` SHA-256 identical across two
  runs (`03934474…`).
- `make generate-ui-assets` twice — dist/ + manifest.json BYTE-IDENTICAL (SHA-256
  snapshots compared). Manifest is exactly the four assets. `runtime.js`
  `2b6cbcd1…` and `htmx.js` `8689e2e2…` UNCHANGED (no JS source touched).
  `theme.css` `533fe4bd`→`4d0b758a` (70973B→68450B; default palette moved out);
  new `theme-default.css` `ae49d971` (2524B).
- `cd ui/goth && go build/test/vet ./…` — all green.
- `cd examples/goth-showcase && go build/test/vet ./…` — all green.
- `make warm-scaffold-cache && make build && make vet && make test && make guard`
  — all green (guard exit 0; ui/* + require-whitelist guards pass; nothing new
  imported).
- `make test-ui-browser` — **345 passed**, 0 failed, 3 engines (Chromium/Firefox/
  WebKit). That is 330 pre-amendment + 15 new (the 5 theme.spec.ts host-theme proofs
  × 3 engines); no test disappeared.

**Run-and-look** (stale `:8099` killed first; server `go run ./cmd/server` on
`:8099`; screenshots at `/tmp/host-theme.png` + `/tmp/default-theme.png`, viewed
directly). View-source of `/specimen/theme-host-stylesheet` shows, in order: the
kit `theme.4d0b758a.css` link (integrity+crossorigin) → `<link rel="stylesheet"
href="/theme/host.css">` (verbatim, NO integrity) → deferred `runtime.js` script;
zero `<style` and zero `style=` in the served HTML. The default `theme-light`
specimen links BOTH `theme.4d0b758a.css` and `theme-default.ae49d971.css`. Computed
values (Chromium): host page `--primary = oklch(0.55 0.22 265)`, badge
`borderTopLeftRadius = 0px`, badge background = the host primary (square vivid-blue
badge + rounded host-primary button, visually confirmed); default page `--primary =
oklch(.21 .02 265)` (kit default). Zero console/CSP errors on either page.
`/theme/host.css` served with `Content-Type: text/css; charset=utf-8`.

## GOTH-A.3 execution evidence (2026-07-18)

Executed GOTH-A.3 exactly as scoped (documentation + stale comments only; no Go
API, `.templ`, asset, or generated file changed). Only the named A.3 file list was
touched. Worktree runs with unrelated authorization-v3 changes present; global
`git status` was NOT used as evidence. plan.md/TASKS.md task boxes intentionally
untouched (GOTH-A.4).

**Changes made (per file)**

- `ui/goth/README.md` — intro gained a stack sentence (**templ + plain CSS + Alpine
  CSP + optional HTMX**; the "T" is templ; Tailwind is not in the kit build/
  toolchain/output; adopter pointer to §5). §1 table dropped the `Theme` alias
  export and clarified `theme` values are CSS-only. §2 rewritten to the real API:
  `Config = {AssetBasePath, Profile, ThemeStylesheetPath}` (Theme/Nonce gone), `New`
  error modes updated, `Head()` doc = kit CSS → theme link → scripts + the
  no-server-rendered-style clause; ownership table swapped the `Config.Nonce` row
  for `ThemeStylesheetPath` and dropped the `DefaultTheme` export. §3 states the
  four-asset manifest set (`theme.css`, `theme-default.css`, `runtime.js`,
  `htmx.js`) with the injected-default explanation and updated the `LogicalName`
  example. §4 removed `RequiresNonce()` from the surface and the nonce prose;
  replaced the two nonce-coherence invariants (a)/(b) with the single frozen
  invariant (no server-rendered `style` attribute / inline `<style>` element) plus
  the controller-CSSOM custom-property exception; `Sources` doc now says `'self'`
  only. §5 removed the Go `Theme`/`NewTheme`/`DefaultTheme` machinery and the alias,
  documented CSS-only theming, the host-stylesheet override story with
  `theme/default.css` as the copyable starter, and added the sanctioned adopter
  recipe "Using Tailwind in YOUR app against the `.goth-*` surface" incl. the v4
  `@theme { --color-primary: var(--primary); }` token bridge and the "any Tailwind
  version is the host's build, never the kit's" note; the §5 ownership table
  dropped the `Theme` row. Added (after §6) the configured-host-link SRI limitation
  + manual-`Manifest()` composition escape. §7's dynamic-style bullet re-pointed to
  the §4 single invariant (the (a)-reference my §4 rewrite orphaned) with the CSSOM
  exception. §10 host-owns list swapped "per-render nonce supply" → theme stylesheet
  ownership, dropped Tailwind from the feature-core line, and rewrote the wiring
  specimen against the real current API (no `Theme`/`Nonce`; `ThemeStylesheetPath`;
  head order kit→theme-link→scripts; `style-src 'self'`, no nonce branch); standing
  invariants updated to the no-server-rendered-style + host-stylesheet theme
  wording.
- `ui/README.md` — family charter: `goth/` = `templ + plain CSS + Alpine CSP +
  optional HTMX` (two occurrences: prose + tree diagram).
- `ARCHITECTURE.md` — UI-implementation row example: `ui/goth (templ + plain CSS +
  Alpine + optional HTMX)`. Single-line, no other authorization-v3 content touched.
- `RELEASING.md` — auth "No new dependency" note drops Tailwind from the
  `templ, …, Alpine, HTMX` enumeration. Single-line.
- `features/authentication/README.md` — feature-core import enumeration drops
  Tailwind. Single-line.
- `features/authentication/internal/inbound/authentication/policy.go` — the
  resource-policy seam comment drops Tailwind from `templ, Tailwind, Alpine, HTMX`.
  Single-line; the feature's own per-render script nonce text (Gate C) left intact.
- `ui/goth/assets/assets.go` — package comment drops Tailwind from "never Node,
  npm, Tailwind, Alpine sources".
- `ui/goth/theme/contract.css` — dropped the stale "no Tailwind recompilation"
  wording (now "a plain CSS file is all a host needs; no recompilation/module-cache
  scan").
- `ui/goth/theme/default.css` — header rewritten for the standalone
  `theme-default.css` artifact: no longer "imported/compiled into theme.css"; states
  it is its own fingerprinted asset injected after `theme.css` only when no host
  `ThemeStylesheetPath` is set, doubles as the copyable starter, and dropped the
  removed Go-value references (`DefaultTheme`, `theme.go defaultTokenValues`,
  `Bundle.Theme().Value(tok)`); "hand-edit" caution now names
  `dist/theme-default.<hash>.css`.
- `examples/goth-showcase/e2e/README.md` — Go-suite line replaced the obsolete
  "nonce coherence" claim with host-theme link-ordering + cascade + `text/css`
  coverage.
- `ui/goth/theme/theme.go` — **no change needed**: GOTH-A.2's D5 slim already
  removed the stale Tailwind-referencing comment; the current file mentions no
  Tailwind (verified by sweep).

**rg sweeps**

- `rg -i tailwind` (excl. `node_modules`, `.git`, `.claude/`, `package-lock.json`):
  the intended mentions are `ui/goth/README.md` (the new adopter recipe §5, plus the
  intro sentence that states Tailwind is NOT in the stack and points to §5). Residual
  hits remain ONLY in GOTH-A.1-owned files that are OUTSIDE the A.3 file list and are
  honest "Tailwind was removed / no Tailwind" provenance, not false-stack claims:
  `ui/goth/tools/build.mjs` (build comments), `ui/goth/assets/src/css/index.css` +
  `reset.css` (owned-reset-vs-preflight comparison comments), and
  `ui/goth/assets/THIRD_PARTY_NOTICES.md` (removed-dependency provenance — the "honest
  third-party provenance" invariant argues to KEEP it). None present Tailwind as part
  of the kit's stack. **Owner call flagged:** if the literal "only the adopter recipe"
  reading must hold byte-for-byte, those four A.1-owned files need a follow-up scrub;
  A.3 deliberately did not touch files outside its named list.
- `rg -in 'nonced dynamic stylesheet|nonce coherence|Config.Nonce|RequiresNonce|
  style-nonce'` under `ui/goth` + `examples/goth-showcase`: **no matches** (exit 1).
  Every remaining `nonce` token under `ui/goth` (goth.go/goth_test.go/README) states
  the channel's ABSENCE (amendment-1 removed it); auth's independent script nonce and
  htmx's vendored internals are out of scope.

**Verification results**

- `make warm-scaffold-cache` — ok.
- `make build` — all modules green.
- `make vet` — exit 0.
- `make test` — exit 0 (all modules; showcase + ui/goth cached-green).
- `make guard` — exit 0 (ui/* guards + require-whitelist pass; nothing imported).

Comment-only Go edits (policy.go, assets.go) rebuilt clean; no `.templ`/asset/
generated file was touched, so `make generate`/`make generate-ui-assets` are not in
A.3's path.

## GOTH-A.4 execution evidence (2026-07-18)

Executed GOTH-A.4 exactly as scoped (milestone bookkeeping + amendment closeout;
no Go/`.templ`/asset/generated file touched — this task changes only the three
milestone records). Worktree runs with unrelated `authorization-v3` changes
present; global `git status` was NOT used as evidence (verification convention).
Unrelated worktree state was preserved.

**Records amended (per file)**

- `.claude/plans/ui-goth/plan.md` — six surgical edits, only the passages the
  amendment names: **invariant 6** (dropped the "documented nonced runtime
  stylesheet" alternative → data-`*` attributes + external CSS + controller-owned
  CSSOM writes only, no server-rendered `style`/inline `<style>`); **invariant 9**
  (mechanism → the required-injection model: host stylesheet injected via
  `Config.ThemeStylesheetPath` after the kit stylesheet, empty path injects the
  embedded default); **Layer-2 bullet** (nonced-dynamic-stylesheet inventory →
  a no-server-rendered-`style`/inline-`<style>` + no-CSP-widening assertion);
  **toolchain/asset policy** (dropped Tailwind from both the "not Node, npm,
  Tailwind" build-free line and the pinned-tools list); **bundle-profile sketch**
  (removed `Theme: goth.DefaultTheme` — `Config.Theme`/`DefaultTheme` deleted);
  **auth-policy nonce sentence** (the kit style-nonce "dynamic runtime stylesheet"
  channel removed; the authentication feature's per-render **script** nonce for
  the external named controller + host additions retained). No other plan.md
  passage was touched.
- `.claude/plans/ui-goth/TASKS.md` — status header notes Amendment 1 ratified +
  applied 2026-07-18 (Gate B reopen, Phase-1 deliverables amended in place per the
  GOTH-5.5 precedent). New "Amendment 1 (GOTH-A)" board addendum records
  GOTH-A.1–A.4 (A.1–A.3 checked with 2026-07-18 evidence, A.4 checked with this
  note). GOTH-1.2/1.3/1.4/1.5 board entries gained in-place amendment notes and
  stay `[x]` (no completed box unchecked). The next-task pointer now reads
  **GOTH-6.3 resume** — P61 unblocked (`gothResizable` ratified per the
  gate-b-review.md addendum). Completion summary updated: numbered tasks **38→42**,
  completed **29→33**; the Gate B line records the 2026-07-18 reopen; the
  dependency-ready block reflects P61 unblocked + Amendment 1 closed.
- `.claude/plans/ui-goth/gate-b-review.md` — dated 2026-07-18 Amendment 1
  addendum: ratification recorded; **R3's nonce-coherence resolution superseded**
  (no kit style-nonce channel exists; the two (a)/(b) invariants replaced by the
  single no-server-rendered-`style`/inline-`<style>` invariant); **R5's
  partial-theme-composition freeze superseded/obsolete** (no Go theme value to
  compose); the frozen-items list per this amendment file recorded; auth's own
  script nonce noted as unaffected.

**Closeout verification matrix (2026-07-18)**

- `make generate` — **no-op**: `ui/goth/document_templ.go` SHA-256 identical
  before/after (`79480e00…`); zero tracked `*_templ.go` drift.
- `make check` — **PASS** (exit 0): `make generate` + git-diff drift legs +
  `warm-scaffold-cache` + per-module `vet`/`build`/`test` + integration-tag vet +
  all eighteen guards green ("all checks passed"). **Asset-drift leg recorded
  honestly:** `ui/` is untracked on this branch (`git status` shows `?? ui/goth/
  assets/dist/`, `?? ui/goth/assets/manifest.json`), so `check`'s `git diff
  --exit-code -- ui/goth/assets/dist ui/goth/assets/manifest.json` is a **no-op**
  for the UI assets — exactly as the GOTH-5.5 closeout recorded. Reproducibility
  was proven byte-identically via the SHA-256 snapshots in GOTH-A.1/A.2 (the
  verification convention), not via a tracked git diff.
- `make guard` — **PASS** (exit 0), standalone: all eighteen guards incl. the two
  `ui/*` guards (no inward import; go.mod requires only templ + sdk) green.
- `make test-ui-browser` — **345 passed**, 0 failed, 3 engines
  (Chromium/Firefox/WebKit); ≥333 threshold met (the current suite is the
  post-A.2 345 incl. the 15 host-theme `theme.spec.ts` proofs, axe, strict-CSP).

**Run-and-look** (stale `:8099` killed first; showcase served on `:8099` via
`PORT=8099 go run ./cmd/server`; final set captured with the pinned Playwright
Chromium next to the A.1 baselines at
`…/goth-a1-baselines/final-a4-{light,dark,rtl}-{wide,narrow}.png` +
`final-a4-host-theme-{wide,narrow}.png`; a sample VIEWED directly). The
host-stylesheet specimen renders the Amendment-1 deliverable live: a **squared**
`.goth-badge` and a `.goth-button` painted in the **host** `--primary`
(`oklch(0.55 0.22 265)` vivid blue), proving both token- and class-level host
override. The `theme-light`/`theme-dark` token samplers link, in order, the kit
`theme.4d0b758a.css` (integrity+crossorigin) → the injected
`theme-default.ae49d971.css` (integrity+crossorigin) — the D3/D4 injected-default
model — with **zero** inline `<style>` and **zero** server-rendered `style=`
(verified on both the sampler and host-theme pages). RTL flips correctly
(right-aligned inline-start). The theme sampler pages are minimal plain-text
token samplers (no `goth-*` surface classes), so their neutral chrome is
pre-existing specimen authoring, not an amendment regression — the styled
component evidence is the host-theme page, which paints fully.

## Amendment 1 — COMPLETE (2026-07-18)

**Amendment 1 is complete.** GOTH-A.1→A.4 all landed and are recorded with dated
evidence above; the kit builds, tests, and showcases green with **zero Tailwind
anywhere in its toolchain or output** (only the README §5 "using Tailwind in YOUR
app" adopter recipe mentions it), theming flows exclusively through a host
stylesheet link emitted after the kit stylesheet (defaulting to the injected
compiled `theme-default.css`), and the entire nonce/dynamic-stylesheet channel is
gone (`Config = {AssetBasePath, Profile, ThemeStylesheetPath}`; no
`Config.Theme`/`Config.Nonce`/`RequiresNonce`/`Bundle.Theme()`/`theme.Theme`
value machinery). The frozen `ui/goth` surface is re-frozen per the seven amended
items; Gate B's R3 resolution and R5 composition freeze are superseded (recorded
in the gate-b-review.md addendum). The milestone records (plan.md, TASKS.md,
gate-b-review.md) now tell the amended truth. Numbered tasks 38→42; no task was
deleted. This amendment authorized no commit, tag, or PR action. **GOTH-6.3
resumes next on this post-Amendment-1 stack** (P61 Resizable on the ratified
`gothResizable` controller).
