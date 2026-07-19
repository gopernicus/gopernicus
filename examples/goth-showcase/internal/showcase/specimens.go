package showcase

import (
	"net/http"
	"sort"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/htmx"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// registerProfileSpecimens registers one page per bundle profile, each served
// from the self-hosted fingerprinted assets.
func registerProfileSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:      "profile-styles",
		Title:   "StylesOnly profile",
		Section: SectionProfile,
		Profile: goth.StylesOnly,
		Body:    stylesOnlyBody,
	})
	r.Register(Specimen{
		ID:      "profile-interactive",
		Title:   "Interactive profile (Alpine CSP)",
		Section: SectionProfile,
		Profile: goth.Interactive,
		Body:    interactiveBody,
	})
	r.Register(Specimen{
		ID:           "profile-full",
		Title:        "Full profile (Alpine + HTMX)",
		Section:      SectionProfile,
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         fullProfileBody,
	})
}

// registerThemeSpecimens registers the theme/appearance/direction/motion axes.
func registerThemeSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:         "theme-light",
		Title:      "Light appearance",
		Section:    SectionTheme,
		Profile:    goth.StylesOnly,
		Appearance: theme.AppearanceLight,
		Body:       themeSampleBody,
	})
	r.Register(Specimen{
		ID:         "theme-dark",
		Title:      "Dark appearance",
		Section:    SectionTheme,
		Profile:    goth.StylesOnly,
		Appearance: theme.AppearanceDark,
		Body:       themeSampleBody,
	})
	r.Register(Specimen{
		ID:      "theme-rtl",
		Title:   "RTL direction",
		Section: SectionTheme,
		Profile: goth.StylesOnly,
		Dir:     theme.DirectionRTL,
		Body:    themeSampleBody,
	})
	r.Register(Specimen{
		ID:      "theme-motion",
		Title:   "Reduced-motion sample",
		Section: SectionTheme,
		Profile: goth.StylesOnly,
		Body:    motionSampleBody,
	})
	r.Register(Specimen{
		ID:              "theme-host-stylesheet",
		Title:           "Host theme stylesheet (linked after the kit CSS)",
		Section:         SectionTheme,
		Profile:         goth.Interactive,
		UseThemedBundle: true,
		Body:            hostStylesheetSampleBody,
	})
}

// registerHTMXSpecimen registers the deliberate HTMX success/validation/error/
// history fixtures page.
func registerHTMXSpecimen(r *Registry) {
	r.Register(Specimen{
		ID:           "htmx-fixtures",
		Title:        "HTMX success / validation / error / history",
		Section:      SectionHTMX,
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         htmxFixturesBody,
	})
}

func stylesOnlyBody() string {
	return `<main data-slot="profile-styles">` +
		`<h1>StylesOnly profile</h1>` +
		`<p data-slot="lede">Compiled theme.css only — no JavaScript is loaded.</p>` +
		`<div data-slot="swatch" data-variant="primary">primary</div>` +
		`<div data-slot="swatch" data-variant="destructive">destructive</div>` +
		`</main>`
}

// interactiveBody exercises the CSP-safe Alpine build and the gothCollapse
// controller on a native <details> disclosure: the summary toggles natively (no-JS
// baseline) and x-data="gothCollapse" mirrors the open state onto data-state and
// dispatches goth:open/goth:close. The CSP build evaluates no inline expression, so
// this must run under a strict script-src 'self' with no 'unsafe-eval'.
func interactiveBody() string {
	return `<main data-slot="profile-interactive">` +
		`<h1>Interactive profile</h1>` +
		`<details class="goth-collapsible" data-slot="collapsible" data-state="closed" x-data="gothCollapse">` +
		`<summary data-slot="collapsible-trigger">Toggle disclosure</summary>` +
		`<div id="collapse-content" data-slot="collapsible-content">Revealed by the gothCollapse controller.</div>` +
		`</details>` +
		`</main>`
}

func fullProfileBody() string {
	return `<main data-slot="profile-full">` +
		`<h1>Full profile</h1>` +
		`<p>Alpine CSP runtime plus the self-hosted HTMX 2.0.10 asset.</p>` +
		htmxFixturesFragment() +
		`</main>`
}

func themeSampleBody() string {
	return `<main data-slot="theme-sample">` +
		`<h1>Theme sample</h1>` +
		`<div data-slot="card"><h2>Card surface</h2><p data-slot="muted">muted foreground text on a card.</p>` +
		`<button type="button" data-slot="button" data-variant="primary">Primary action</button></div>` +
		`</main>`
}

// hostStylesheetSampleBody renders a primary-driven surface and a kit badge so the
// host theme stylesheet's overrides are visible: the host retunes --primary (every
// primary surface shifts) and squares .goth-badge (border-radius: 0). The e2e
// theme.spec.ts measures the computed --primary and the badge radius here.
func hostStylesheetSampleBody() string {
	return `<main data-slot="theme-host-stylesheet">` +
		`<h1>Host theme stylesheet</h1>` +
		`<p data-slot="lede">A host-authored stylesheet linked after the kit CSS retunes --primary and squares the badge.</p>` +
		`<span class="goth-badge" data-slot="badge" data-variant="default">host-themed badge</span>` +
		`<button type="button" class="goth-button" data-slot="button" data-variant="primary">Primary action</button>` +
		`</main>`
}

func motionSampleBody() string {
	return `<main data-slot="theme-motion">` +
		`<h1>Reduced motion</h1>` +
		`<div data-slot="motion-box">Transitions collapse to 0 under prefers-reduced-motion.</div>` +
		`</main>`
}

// htmxFixturesBody is the standalone HTMX fixtures page; htmxFixturesFragment is
// the reusable region (also embedded in the Full profile page).
func htmxFixturesBody() string {
	return `<main data-slot="htmx-fixtures"><h1>HTMX fixtures</h1>` + htmxFixturesFragment() + `</main>`
}

func htmxFixturesFragment() string {
	// The success trigger's hx-* attributes are produced by the typed
	// htmx.Attrs helper (ui/goth/htmx) so the Go builder gets real-browser
	// coverage; the rest are literal to keep the fixtures legible.
	success, _ := htmx.Attrs{
		Method: htmx.MethodGet,
		URL:    "/htmx/fragment/success",
		Target: "#htmx-result",
		Swap:   htmx.SwapInnerHTML,
	}.Build()

	var b strings.Builder
	b.WriteString(`<section data-slot="htmx-demo">`)
	b.WriteString(`<button type="button" data-slot="htmx-success-trigger" ` + attrsToHTML(success) + `>Load success fragment</button>`)
	b.WriteString(`<form data-slot="htmx-validate-form" hx-post="/htmx/fragment/validate" hx-target="#htmx-result" hx-swap="innerHTML">` +
		`<button type="submit" data-slot="htmx-validate-trigger">Submit invalid</button></form>`)
	b.WriteString(`<button type="button" data-slot="htmx-error-trigger" hx-get="/htmx/fragment/error" hx-target="#htmx-result" hx-swap="innerHTML">Trigger 500</button>`)
	b.WriteString(`<a data-slot="htmx-history-trigger" hx-get="/htmx/page/two" hx-target="#htmx-page" hx-swap="innerHTML" hx-push-url="true" href="/htmx/page/two">Go to page two</a>`)
	b.WriteString(`<div id="htmx-result" data-slot="htmx-result"></div>`)
	b.WriteString(`<div id="htmx-page" data-slot="htmx-page"></div>`)
	b.WriteString(`</section>`)
	return b.String()
}

// attrsToHTML serializes templ.Attributes (string/bool values) into a
// deterministic attribute string for host-authored raw markup.
func attrsToHTML(attrs templ.Attributes) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		switch v := attrs[k].(type) {
		case string:
			b.WriteString(k + `="` + templ.EscapeString(v) + `"`)
		case bool:
			if v {
				b.WriteString(k)
			}
		}
	}
	return b.String()
}

// registerHTMXFixtures wires the fragment endpoints the HTMX specimens drive.
// Each degrades to a full document on a non-HTMX request where the route serves
// HTML, and non-2xx fragments are handled by the vendored htmx-config response
// policy (403/409/422 swap a complete fragment; other 4xx/5xx surface an error
// without a swap).
func (s *Server) registerHTMXFixtures() {
	h := s.handler

	h.Handle(http.MethodGet, "/htmx/fragment/success", func(w http.ResponseWriter, r *http.Request) {
		writeFragment(w, http.StatusOK, `<p data-slot="htmx-success" role="status">Loaded via HTMX.</p>`)
	})

	h.Handle(http.MethodPost, "/htmx/fragment/validate", func(w http.ResponseWriter, r *http.Request) {
		// 422 is an explicit swap code in the htmx-config response policy: the
		// validation fragment replaces the target even though it is non-2xx.
		writeFragment(w, http.StatusUnprocessableEntity, `<p data-slot="htmx-validation" role="alert">Name is required.</p>`)
	})

	h.Handle(http.MethodGet, "/htmx/fragment/error", func(w http.ResponseWriter, r *http.Request) {
		// 500 is NOT a swap code: htmx surfaces the error without replacing the
		// target, so #htmx-result stays empty (proved in the browser test).
		writeFragment(w, http.StatusInternalServerError, `<p data-slot="htmx-error">server error</p>`)
	})

	// History target: a fragment for an HTMX request, a full document on a direct
	// (re-fetch/back-to-a-hard-load) navigation, so history restoration is correct
	// when the browser re-fetches a full document.
	h.Handle(http.MethodGet, "/htmx/page/two", func(w http.ResponseWriter, r *http.Request) {
		if htmx.FromRequest(r).IsHTMX() {
			writeFragment(w, http.StatusOK, `<section data-slot="htmx-page-two">Page two fragment.</section>`)
			return
		}
		bundle := s.bundles[goth.Full]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		body := `<main data-slot="htmx-page-two-document"><h1>Page two</h1>` +
			`<section data-slot="htmx-page-two">Page two, served as a full document.</section>` +
			`<a href="/specimen/htmx-fixtures">Back to fixtures</a></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — page two"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}

func writeFragment(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}
