# `ui/goth` — third-party provenance and notices

Status: **ASSET PIPELINE LANDED — GOTH-1.2 (2026-07-17).** The exact-pinned Node
build toolchain, the committed `package-lock.json`, the fingerprinted `dist/`
outputs, and the generated `manifest.json` are in place. The version pins,
SHA-256 records, license inventory, and install-script allowlist below are now
FROZEN (not candidates). Browser/accessibility tooling (Playwright, axe-core)
remains deferred to GOTH-1.5's harness and is intentionally NOT part of the
asset-build toolchain — axe-core is MPL-2.0 and must never reach `dist/`.

Plan authority: `.claude/plans/ui-goth/plan.md` — "Toolchain and asset policy"
and preflight items 5–6.

## Design-source provenance (Shadcn)

`ui/goth` is a from-scratch GOTH interpretation of the dated Shadcn catalog. The
catalog **names/IDs (P01–P64) are frozen by the plan** and owner-ratified
(2026-07-17). Upstream code is a design reference for later ports, not a copied
base at GOTH-0.1.

| field | value |
|---|---|
| project | shadcn/ui |
| repository | <https://github.com/shadcn-ui/ui> |
| catalog page | <https://ui.shadcn.com/docs/components> (captured 2026-07-17) |
| repository license | MIT (`LICENSE.md`) |
| repository revision at capture | `d28738b183c5eaa69d8d540826e450f30d39ab6c` |
| revision commit date | 2026-07-17T15:40:59Z |

The recorded revision is `shadcn-ui/ui` `main` HEAD at the 2026-07-17 capture. It
is the provenance anchor for the catalog snapshot; it is **not** an assertion
that any GOTH primitive derives from that commit. Per-entry code provenance —
the exact upstream file(s) and revision that materially informed a port, plus
license text where required — is recorded against the individual primitive when
that primitive is actually written (GOTH-0.3 freezes the grammar; GOTH-1.x+
implement). Nothing is copied before that per-entry provenance is settled.

## Runtime dependency candidates (bundled/self-hosted)

These are the runtime dependencies whose compiled/vendored outputs are embedded
and served self-hosted by the kit. HTMX is owner-fixed at exact **2.0.10** (plan
ratified basis #6). All pins are exact (no caret/tilde) and locked in
`tools/package-lock.json`.

| dependency | role | license (SPDX) | exact version |
|---|---|---|---|
| htmx.org | optional server-fragment runtime (Full profile); vendored self-hosted as `htmx.js` (upstream 2.0.10 + the Gopernicus non-2xx response config) | 0BSD (Zero-Clause BSD) | `2.0.10`, owner-fixed |
| alpinejs | CSP-safe interaction runtime, bundled into `runtime.js` via `@alpinejs/csp` with the registered GOTH controllers | MIT | `3.15.12` |
| @alpinejs/csp | CSP-safe Alpine build (no `new Function`/eval → no `'unsafe-eval'`); the `runtime.js` entry | MIT | `3.15.12` |

No CSS framework is embedded. `theme.css` is the kit's own component/theme CSS
(hand-written plain CSS consuming token custom properties) plus the kit's owned
base reset (`assets/src/css/reset.css`, authored for ui/goth — no vendored or
copied third-party reset text); esbuild bundles the `@import` graph and minifies
it. Tailwind CSS was removed from the toolchain at GOTH-A.1 (amendment 1): the "T"
in GOTH is templ, and a host that wants Tailwind runs it in its own build against
the stable `.goth-*` surface — never in the kit.

Not adopted this milestone (no bundle weight earned yet; would be pinned + noted
here when introduced): `@alpinejs/focus` (MIT), `@alpinejs/collapse` (MIT).

Consumers of the committed Go module receive Go and the embedded compiled
outputs only — never Node, npm, Tailwind, Alpine sources, or a bundler.

## Build/test tooling candidates (repository-local only)

Authoring/verification tools. They are never shipped to a Go consumer.

| tool | role | license (SPDX) | version posture |
|---|---|---|---|
| a-h/templ | templ codegen + runtime | MIT | pinned `v0.3.1020` (matches existing `features/*/views/goth`) |
| esbuild | JS bundler (`runtime.js` IIFE) + CSS bundler/minifier (`theme.css`, GOTH-A.1) | MIT | exact `0.28.1` |
| Node.js | build runtime, pinned via `tools/.nvmrc` + `package.json` engines | MIT | `24.0.1` (npm `11.3.0`) |
| Playwright | three-engine browser harness (Chromium/Firefox/WebKit) | Apache-2.0 | deferred to GOTH-1.5 harness (separate package) |
| axe-core | accessibility assertions | MPL-2.0 | deferred to GOTH-1.5 harness; **build/test only, never embedded in `dist/`** |

Playwright and axe-core are intentionally excluded from the GOTH-1.2 asset-build
`tools/package.json`: they belong to the browser/accessibility harness landed in
GOTH-1.5, and keeping them out of the asset toolchain guarantees the MPL-2.0
axe-core never reaches an embedded artifact (enforced by
`assets/assets_test.go:TestExpectedAssetSet`).

All licenses above were confirmed against each project's published license on
2026-07-17. `NOASSERTION` returned by GitHub's classifier for htmx was resolved
by reading its `LICENSE` file directly: Zero-Clause BSD (0BSD).

## Provenance obligations — status (GOTH-1.2)

1. **Exact version pins** — SATISFIED. Alpine + `@alpinejs/csp` `3.15.12`, esbuild
   `0.28.1` (now also the CSS bundler/minifier), htmx `2.0.10`, Node `24.0.1`/npm
   `11.3.0`; no plugin adopted; no CSS framework. All exact, no ranges. Owner note:
   these are current stable releases pinned + locked + hashed; the formal
   release-age review of the non-owner-fixed pins remains an owner gate (htmx is
   owner-fixed at 2.0.10). Tailwind `3.4.19` was removed at GOTH-A.1.
2. **Committed lockfile + `npm ci`** — SATISFIED. `tools/package-lock.json` is
   committed; the Node-gated build target runs `npm ci --ignore-scripts`.
3. **SHA-256 records** — SATISFIED (table below).
4. **Per-entry Shadcn source revision** — N/A at GOTH-1.2 (no primitive ported
   yet; the foundation CSS/JS reuse no upstream Shadcn source). Recorded per
   primitive as they land (GOTH-2.x+).
5. **Install-script audit/allowlist** — SATISFIED. `npm ci --ignore-scripts` runs
   NO install scripts. The only dependency carrying one is `esbuild` (a
   `postinstall` that fetches/validates its native binary); it is skipped because
   the platform binary ships as the optional dependency package
   `@esbuild/<platform>` (plain files, no script). The allowlist is therefore
   **empty**: no install script is permitted or required to build the assets.

### Artifact SHA-256 records

Embedded fingerprinted outputs (`assets/dist/`) — the fingerprint in each
filename is the sha256 hex prefix, and the manifest carries the sha384 SRI digest:

Updated at GOTH-1.4 (2026-07-17): `runtime.js` now bundles the registered GOTH
controllers + shared mechanics, `htmx.js` now appends the Gopernicus HTMX
response configuration, and `theme.css` adds the `.goth-sr-only` live-region
utility (GOTH-1.3 had already changed `theme.css` for the theme layers).
Updated at GOTH-1.5 (2026-07-17): the three-engine browser proof surfaced two
runtime defects fixed at source and rebuilt — `runtime.js` (`gothCollapse` now
caches its root element so state reflects on the root when toggled from a
descendant handler) and `htmx.js` (the Gopernicus config now sets
`htmx.config.includeIndicatorStyles = false` so htmx no longer injects an inline
`<style>` that a strict `style-src 'self'` blocks). `theme.css` is unchanged.
Updated at GOTH-A.1 (2026-07-18): `theme.css` is now esbuild-bundled with Tailwind
removed from the toolchain (owned reset replaces the Tailwind preflight; dead
unused utility classes and the `--tw-*` custom properties are gone; esbuild's
target-based prefixing retains the functionally significant `-webkit-appearance`/
`-webkit-user-select`/`-webkit-text-size-adjust`). `runtime.js` and `htmx.js` are
byte-identical across this change (no JS source touched). The row values below are
refreshed to the current committed artifacts (the prior rows had drifted to
GOTH-1.5-era values across Phases 2–6, which did not update this table).

| logical | embedded path | bytes | sha256 |
|---|---|---|---|
| `theme.css` | `dist/theme.533fe4bd.css` | 70973 | `533fe4bd7dec05d9d99088c5c033efbd1afdd12bc97a108143245b65d0e12122` |
| `runtime.js` | `dist/runtime.2b6cbcd1.js` | 87704 | `2b6cbcd1f888564b0ed25b2f51457450498071454ec38fdb59cfeb6b8b7d0190` |
| `htmx.js` | `dist/htmx.8689e2e2.js` | 51533 | `8689e2e2948c8e41ca2505f9b3af6df848fac5a83ac652dd518ba16a4c9b0bcc` |

Vendored upstream source (self-hosted, byte-for-byte, no re-minification):

| source | version | embedded in | upstream sha256 |
|---|---|---|---|
| htmx.org `dist/htmx.min.js` | 2.0.10 | `dist/htmx.8689e2e2.js` | `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de` |

`theme.css` is esbuild-bundled (no Tailwind) from `assets/src/css/index.css` — the
owned base reset, the semantic token contract + default palette, and the
hand-written component CSS, with esbuild resolving the `@import` graph, applying
its target-based vendor prefixing, and minifying. `runtime.js` is
the esbuild IIFE bundle of `assets/src/js/runtime.js` (`@alpinejs/csp` + the
registered GOTH controllers and shared mechanics under `assets/src/js/`).
`htmx.js` is the vendored byte-for-byte htmx 2.0.10 `dist/htmx.min.js` followed by
the minified Gopernicus HTMX response configuration
(`assets/src/js/htmx-config.js`, an IIFE that sets `htmx.config.responseHandling`
for explicit non-2xx fragment handling — GOTH-1.4 — and, from GOTH-1.5,
`htmx.config.includeIndicatorStyles = false` so no inline `<style>` is injected);
the upstream htmx bytes are
unchanged (upstream sha256 above) and the combined asset carries its own
content-addressed fingerprint. All three are content-addressed and reproducible:
two builds on the pinned toolchain are byte-identical. Regenerate with
`make generate-ui-assets`; `make check` proves the committed `dist/` +
`manifest.json` are in sync via a plain-git diff and never invokes Node.

## Tag posture

No module in this repository is tagged as of 2026-07-18 (`git tag --list` empty;
confirmed at GOTH-0.1 preflight and re-confirmed at GOTH-7.2/7.3). Because the
feature view modules were untagged, the plan's tag-sensitive rename path was applied:
`features/authentication/views/templ` and `features/cms/views/templ` were **renamed
in place** to `features/authentication/views/goth` and `features/cms/views/goth`
(GOTH-7.2/7.3, 2026-07-18), with no compatibility module. If a relevant tag ever
appears before a future view-module migration, the additive-`views/goth` fallback
(retain the old path as a compatibility module) applies instead.
