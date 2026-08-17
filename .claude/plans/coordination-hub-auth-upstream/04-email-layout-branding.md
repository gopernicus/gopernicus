# Phase 4 — bundled email layout branding

## Outcome

Make the sdk's default transactional HTML layout honor `Branding.LogoURL`, with
tests and documentation that accurately describe which bundled layouts render
which branding fields.

## Audit correction

The coordination-hub flag is broader than the code:

- `marketing.html` already conditionally renders `Brand.LogoURL`;
- `transactional.html` renders name/tagline/address but omits the logo; and
- `minimal.html` is intentionally content-only/unbranded.

Authentication uses `email.LayoutTransactional` for every bundled delivery
purpose, so the transactional omission is the real adopter-visible bug.

## CHAU-4.1 — characterize the matrix

Add table-driven rendering tests that pin the bundled layouts' intended brand
surface:

| layout | logo | name | tagline | address | intent |
|---|---|---|---|---|---|
| transactional | yes after this phase | yes | yes | yes | normal app/auth mail |
| marketing | already yes | yes | no current change | yes | campaign mail |
| minimal | no | no | no | no | deliberately unbranded fallback |

If product owners want minimal to become branded, stop and make that a separate
design decision; do not smuggle it into a bug fix.

Prove empty branding and nil branding render without an image or template panic.
The registry currently initializes non-nil empty branding; keep that invariant
documented.

## CHAU-4.2 — transactional rendering

Add a conditional logo block to `templates/layouts/transactional.html` near the
brand name. Requirements:

- no `<img>` when `LogoURL` is empty;
- `src` is rendered through `html/template` escaping, never `template.HTML`;
- non-empty brand name is the `alt` text; define/document a safe fallback alt
  when name is empty;
- conservative email-client inline dimensions (`max-height` plus width-safe
  behavior) without scripts, CSS imports, or SVG assumptions;
- name/tagline remain visible so a failed image does not erase identity; and
- text layout remains image-free and unchanged.

Tests render the real embedded template and assert present/absent behavior,
escaped hostile input, alt text, and no raw URL in the text alternative solely
because it was a logo.

Do not add network URL fetching or validation to the renderer. Docs should
recommend an absolute HTTPS, publicly fetchable image; `html/template` remains
the injection safety boundary.

## CHAU-4.3 — documentation and release

Add an sdk email branding section covering:

- the field/layout matrix above;
- `Branding` versus app layout overrides;
- external-image blocking in mail clients and why text/name fallback remains;
- absolute HTTPS/public accessibility guidance;
- HTML autoescaping and the absence of image fetching; and
- a rendered example using the bundled transactional layout.

Update auth README's `EmailBranding`/`EmailLayouts` entries so adopters know a
logo now works without replacing the entire layout, while layout overrides still
win at `LayerApp`. Add `RELEASING.md` compatibility notes: output HTML changes
only when `LogoURL` is non-empty.

Verification:

```sh
(cd sdk && go test -race ./capabilities/email/... && go vet ./capabilities/email/... && go test -race ./...)
make check && make guard
```

## Execution log

Append only. Record each CHAU-4.x task's rendered-output evidence, email tests,
documentation changes, and release disposition.

### 2026-08-16 — CHAU-4.1, CHAU-4.2, CHAU-4.3 complete

**CHAU-4.1 — characterization.** New
`sdk/capabilities/email/templates_branding_test.go`.
`TestBundledLayoutBrandingMatrix` is the table-driven pin: it renders every
bundled layout with all seven `Branding` fields populated with distinguishable
markers and asserts presence/absence per field. The audited matrix is now
executable, and matches the plan (transactional: logo/name/tagline/address/social,
no unsubscribe; marketing: logo/name/address/social/unsubscribe, no tagline;
minimal: nothing). `minimal` was **not** branded — no smuggled design change.
`TestBrandingAbsentRendersCleanly` covers empty and nil branding across all three
layouts; `TestBundledTextLayoutsStayImageFree` pins that the `.txt` halves carry
no markup and never leak the logo URL.

Nil-branding provability required one small production change:
`TemplateRegistry.SetBranding(nil)` now normalizes to `&Branding{}` rather than
storing nil (`templates.go`). Without it, the public `email.WithBranding(nil)`
path could break the registry's own documented non-nil invariant and every
`{{.Brand.*}}` dereference with it. `TestSetBrandingNilKeepsInvariant` pins it.
`features/authentication` already guarded with `if d.Branding != nil`, so no
feature behavior changed.

**CHAU-4.2 — transactional rendering.**
`templates/layouts/transactional.html` gains a conditional block above the `<h1>`:

```
{{if .Brand.LogoURL}}
<img src="{{.Brand.LogoURL}}" alt="{{if .Brand.Name}}{{.Brand.Name}}{{else}}Your Company{{end}}"
     style="display: block; margin: 0 auto 16px auto; max-height: 48px; max-width: 100%;
            height: auto; border: 0; outline: none; text-decoration: none;">
{{end}}
```

Requirement-by-requirement evidence:

- **no `<img>` when empty** — `TestTransactionalLayoutOmitsLogoWhenUnset`
  asserts the substring `<img` is absent from the whole document.
- **`html/template` escaping, never `template.HTML`** —
  `TestTransactionalLogoEscaping` drives two hostile brands. An attribute
  break-out (`https://…/l.png" onerror="alert(1)`, name `"><script>…`) renders
  as `&#34;`/`&lt;script&gt;` with no live `onerror`; `javascript:alert(1)`
  renders as html/template's `#ZgotmplZ` marker. The template interpolates the
  raw string — the escaping is the engine's, which is the point.
- **alt text** — `TestTransactionalLayoutRendersLogo` asserts
  `alt="<brand name>"`; `TestTransactionalLogoAltFallback` pins the documented
  `Your Company` fallback, deliberately the same fallback the visible `<h1>`
  already used so image and text identity cannot disagree.
- **conservative dimensions** — `max-height: 48px` (asserted),
  `max-width: 100%`, `height: auto`, `border: 0`, `display: block`. No script,
  no CSS import, no SVG assumption, no network fetch or URL validation added to
  the renderer.
- **identity survives a failed image** — the same test asserts name and tagline
  are still present in the HTML alongside the `<img>`.
- **text layout unchanged** — `transactional.txt` was not edited;
  `TestBundledTextLayoutsStayImageFree` proves the text body carries no logo URL.

`TestAppLayoutOverrideWinsOverBundledLogo` additionally proves a `LayerApp`
layout override still wins wholesale and does not inherit the bundled logo — the
coordination-hub case, where this fix is a local no-op.

**CHAU-4.3 — documentation.**

- `sdk/capabilities/email/email.go` package doc gains a "Branding and bundled
  layouts" section (the field matrix, `Branding` as data override vs `LayerApp`
  layout override, and that an overriding host owns its own logo markup) and a
  "Logo rendering" section (absolute HTTPS/public-fetchability guidance, no
  fetching or validation, `html/template` as the injection boundary, the
  `#ZgotmplZ` behavior, external-image blocking and the name/tagline fallback,
  alt-text rule) plus a rendered `email.New` → `RenderAndSend` example on the
  bundled transactional layout.
- `Branding`'s own godoc (`templates.go`) carries the matrix so it is visible at
  the field a host actually sets; `SetBranding` documents the nil-reset.
- `features/authentication/authentication.go` — `Config.EmailBranding` godoc now
  states that all auth mail renders transactional, that `LogoURL` now works
  without replacing the layout, the HTTPS/escaping posture, and the
  `EmailLayouts`-override precedence.
- `features/authentication/README.md` config matrix gains a dedicated
  **`EmailBranding`** row (it previously had none — only an `EmailLayouts`
  cross-reference).
- `RELEASING.md` — new keyed entry `### sdk/capabilities/email — next tag:
  bundled transactional layout renders Brand.LogoURL (patch floor;
  RENDERED-OUTPUT change)`. It records: no exported symbol changed → patch
  floor; output differs **only** when `LogoURL` is non-empty and only on the
  bundled layout; `LayerApp` overriders see no change; text output untouched;
  and an explicit instruction not to split it from the phase-3 sdk minor if both
  ride the same commit.

**Verification (exactly as run, 2026-08-16):**

```
(cd sdk && go test -race ./capabilities/email/...)   ok  …/sdk/capabilities/email  3.190s
(cd sdk && go vet ./capabilities/email/...)          clean
(cd sdk && go test -race ./...)                      all packages ok
make check                                           all checks passed
```

`make check` runs `make guard` as part of its chain — all nineteen guards
printed and "all checks passed". No live-store or provider gate applies to this
phase; nothing was skipped.

**Release disposition:** patch floor for `sdk`, to be merged into whatever sdk
tag phase 8 freezes. Not independently cut.
