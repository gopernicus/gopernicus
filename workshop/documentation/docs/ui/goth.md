---
title: GOTH UI
description: An optional templ and plain-CSS presentation system for Gopernicus hosts and features.
---

# GOTH UI

`ui/goth` is an optional Go presentation system for hosts that serve HTML. It owns semantic tokens, templates, components, browser controllers, and fingerprinted assets—but no business schema, routes, middleware, or response headers.

The stack is **templ + plain CSS + Alpine CSP + optional HTMX**. Tailwind is not part of the kit's build or emitted output.

## Package families

| Package | Purpose |
|---|---|
| `ui/goth` | immutable bundle, profiles, manifest, resource requirements, document composition |
| `ui/goth/primitives` | 64 Shadcn-parity component primitives in one package |
| `ui/goth/components/*` | opinionated forms, layouts, feedback, and data compositions |
| `ui/goth/htmx` | typed explicit `hx-*` attributes and response interpretation |
| `ui/goth/theme` | semantic token names and document helpers |
| `ui/goth/assets` | embedded fingerprinted CSS/JS plus manifest |

Feature adapters such as `features/cms/views/goth` and `features/authentication/views/goth` translate feature view ports into GOTH renderers.

## Bundle profiles

```go
const (
    goth.StylesOnly  // zero value: CSS only
    goth.Interactive // adds Alpine CSP + GOTH controllers
    goth.Full        // adds HTMX
)
```

Profiles are additive. Zero configuration chooses the smallest and safest profile, the default asset path, and the built-in neutral theme.

```go
bundle, err := goth.New(goth.Config{
    AssetBasePath:      "/assets/goth",
    Profile:            goth.Full,
    ThemeStylesheetPath: "/assets/app/theme.css",
})
if err != nil {
    return err
}
```

`Bundle` is immutable after construction and safe to share across requests.

## Serve assets from the host

The kit exposes assets but never registers a URL:

```go
static := web.NewStaticFileServer(
    gothassets.FS,
    web.WithAssetPrefix("dist/"),
)
static.AddRoutes(router, bundle.AssetBasePath())
```

`bundle.Head()` renders links/scripts to the fingerprinted manifest entries. The retained `dist/` path lets the SDK static server apply immutable caching.

## CSP and browser requirements

`bundle.Requirements()` reports the minimal resource directives for the selected profile. A host maps those requirements into its own CSP; the kit does not write headers.

The default self-hosted bundle needs only `'self'` for styles and, for interactive profiles, scripts. It emits no inline style or script and needs neither `unsafe-inline` nor `unsafe-eval`.

When GOTH backs authentication views, the feature adapter converts bundle requirements into authentication's technology-neutral HTML resource policy while preserving the feature's fixed no-store, frame, base, and form protections.

## Themes use semantic roles

Themes override stable custom-property roles rather than component internals:

- background, foreground, card, and popover;
- primary, secondary, muted, accent, destructive;
- border, input, and ring;
- success, warning, charts, sidebar;
- radius, shadows, typography, motion, density, and z-index layers.

Supply a host stylesheet after the kit CSS with `ThemeStylesheetPath`. Keep brand values in the host; role names are the reusable contract.

## Component posture

GOTH's 64-entry primitive baseline follows a frozen Shadcn catalog for recognizable behavior, not source-code parity. Components must provide:

- useful HTML and no-JavaScript states;
- keyboard, focus, and ARIA behavior;
- right-to-left, reduced-motion, narrow-layout, and dark-mode handling;
- explicit CSP/runtime requirements;
- render tests and real-browser coverage.

Use `examples/goth-showcase` to inspect the catalog. Its Playwright suite runs Chromium, Firefox, WebKit, and accessibility checks.

## Custom feature views

For a small change, embed a feature's bundled GOTH view implementation and override selected methods. For a completely different presentation stack, implement the feature's public `Views` interface with anything satisfying `web.Renderer`.

The core never imports GOTH. A host can remain API-only by leaving `Views` nil, and no templ/UI dependency enters its feature-core graph.
