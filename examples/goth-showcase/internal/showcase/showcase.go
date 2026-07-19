// Package showcase is the zero-datastore host for the ui/goth browser and
// accessibility harness (GOTH-1.5). It registers the embedded ui/goth asset FS
// through sdk/foundation/web.StaticFileServer, serves one page per specimen
// across all three bundle profiles (StylesOnly / Interactive / Full), themes,
// and HTMX fixtures, and writes a strict CSP header mapped from
// goth.Bundle.Requirements() plus the host-owned baseline. It owns no schema, no
// migration, and no database — the whole surface is served from the committed,
// fingerprinted assets embedded in ui/goth.
//
// The registry is the single source of truth: it drives the HTTP routes AND the
// completeness test, so a primitive declared implemented without a registered
// showcase specimen fails the Go suite automatically.
package showcase

import (
	_ "embed"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/assets"
)

// hostThemePath is the root-relative public path the showcase serves its
// host-authored theme stylesheet under, and the value it wires into
// goth.Config.ThemeStylesheetPath so the kit links it after the kit stylesheet.
const hostThemePath = "/theme/host.css"

// allStylePath is the public path the showcase serves its /all gallery stylesheet
// under, wired into the /all bundle's goth.Config.ThemeStylesheetPath so the kit
// links it after the kit stylesheet (same strict style-src 'self' pattern as the
// host theme stylesheet).
const allStylePath = "/all/gallery.css"

// hostThemeCSS is the embedded host-authored theme stylesheet (the WordPress-model
// specimen). It overrides --primary (token-level) and .goth-badge border-radius
// (class-level) over the kit default theme; the theme-host-stylesheet specimen and
// e2e theme.spec.ts prove both take effect.
//
//go:embed host_theme.css
var hostThemeCSS []byte

// allGalleryCSS is the embedded host-authored stylesheet for the /all gallery page.
// It contains only layout scaffolding (containment + cell chrome) so the combined
// single-page gallery reads coherently; it overrides no kit token or component.
//
//go:embed all_gallery.css
var allGalleryCSS []byte

// Server is the constructed showcase host. It is immutable after New.
type Server struct {
	handler  *web.WebHandler
	registry *Registry
	bundles  map[goth.Profile]*goth.Bundle
	themed   *goth.Bundle // Interactive + a host theme stylesheet linked after the kit CSS
	all      *goth.Bundle // Full + the /all gallery stylesheet linked after the kit CSS
}

// New builds the showcase server: it constructs one bundle per profile plus a
// host-theme bundle, registers the embedded asset FS and the host theme stylesheet
// route, and wires every specimen route and HTMX fixture onto handler.
func New(handler *web.WebHandler) (*Server, error) {
	bundles := map[goth.Profile]*goth.Bundle{}
	for _, p := range []goth.Profile{goth.StylesOnly, goth.Interactive, goth.Full} {
		b, err := goth.New(goth.Config{
			AssetBasePath: goth.DefaultAssetBasePath,
			Profile:       p,
		})
		if err != nil {
			return nil, fmt.Errorf("showcase: build profile %d bundle: %w", p, err)
		}
		bundles[p] = b
	}

	// The host-theme bundle links a real host-authored stylesheet AFTER the kit
	// stylesheet (goth.Config.ThemeStylesheetPath) — the WordPress model, proven
	// under a strict style-src 'self' with no nonce and no inline style.
	themed, err := goth.New(goth.Config{
		AssetBasePath:       goth.DefaultAssetBasePath,
		Profile:             goth.Interactive,
		ThemeStylesheetPath: hostThemePath,
	})
	if err != nil {
		return nil, fmt.Errorf("showcase: build host-theme bundle: %w", err)
	}

	// The /all bundle is the Full superset (Alpine + HTMX, so every Interactive/Full
	// specimen's runtime is present) with the gallery stylesheet linked after the kit
	// CSS — same strict style-src 'self' pattern as the host theme bundle.
	all, err := goth.New(goth.Config{
		AssetBasePath:       goth.DefaultAssetBasePath,
		Profile:             goth.Full,
		ThemeStylesheetPath: allStylePath,
	})
	if err != nil {
		return nil, fmt.Errorf("showcase: build /all bundle: %w", err)
	}

	s := &Server{handler: handler, registry: DefaultRegistry(), bundles: bundles, themed: themed, all: all}

	// Serve the embedded fingerprinted assets. WithAssetPrefix("dist/") matches
	// the dist/-rooted FS paths so the SDK server applies immutable caching. The
	// kit registers no route itself; the host does it here.
	static := web.NewStaticFileServer(assets.FS, web.WithAssetPrefix("dist/"))
	static.AddRoutes(handler, goth.DefaultAssetBasePath)

	s.registerRoutes()
	return s, nil
}

// serveHostTheme serves the embedded host-authored theme stylesheet with an
// explicit text/css content type, so the browser applies it as CSS (the kit links
// it via ThemeStylesheetPath). It is same-origin under style-src 'self'.
func (s *Server) serveHostTheme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(hostThemeCSS)
}

// serveAllStyle serves the embedded /all gallery stylesheet as text/css, same-origin
// under style-src 'self' (the kit links it via ThemeStylesheetPath).
func (s *Server) serveAllStyle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(allGalleryCSS)
}

// Handler returns the wired web handler.
func (s *Server) Handler() *web.WebHandler { return s.handler }

// Registry returns the specimen registry.
func (s *Server) Registry() *Registry { return s.registry }

// bundleFor returns the bundle a specimen renders against.
func (s *Server) bundleFor(spec Specimen) *goth.Bundle {
	if spec.UseThemedBundle {
		return s.themed
	}
	return s.bundles[spec.Profile]
}

func (s *Server) registerRoutes() {
	s.handler.Handle(http.MethodGet, "/", s.indexHandler())
	s.handler.Handle(http.MethodGet, allPath, s.allHandler())
	s.handler.Handle(http.MethodGet, "/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// A no-content favicon keeps the browser's automatic /favicon.ico fetch from
	// logging a console error during the strict-CSP smoke.
	s.handler.Handle(http.MethodGet, "/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.handler.Handle(http.MethodGet, hostThemePath, s.serveHostTheme)
	s.handler.Handle(http.MethodGet, allStylePath, s.serveAllStyle)
	for _, spec := range s.registry.All() {
		s.handler.Handle(http.MethodGet, spec.Path(), s.specimenHandler(spec))
	}
	s.registerHTMXFixtures()
	s.registerSelectionFixtures()
	s.registerCompactFixtures()
	s.registerAnchoredFixtures()
	s.registerDateFixtures()
	s.registerPaletteFixtures()
	s.registerDataFixtures()
	s.registerSidebarFixtures()
	s.registerAttachmentFixtures()
	s.registerMessageScrollerFixtures()
	s.registerNotificationFixtures()
	s.registerComponentFixtures()
}

// specimenHandler renders one specimen page under a strict CSP header mapped from
// the specimen bundle's Requirements plus the host-owned baseline.
func (s *Server) specimenHandler(spec Specimen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bundle := s.bundleFor(spec)

		w.Header().Set("Content-Security-Policy", buildCSP(bundle, spec.AllowConnect))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		doc := bundle.Document(goth.DocumentOptions{
			Title:      "goth showcase — " + spec.Title,
			Appearance: spec.Appearance,
			Dir:        spec.Dir,
		}, rawComponent(spec.Body()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	}
}

func (s *Server) indexHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))

		var b strings.Builder
		b.WriteString(`<main data-slot="showcase-index"><h1>goth showcase</h1>`)
		b.WriteString(`<p data-slot="all-link"><a data-all-link href="` + allPath + `">View every specimen on one page →</a></p>`)
		sections := s.registry.Sections()
		for _, section := range sections {
			b.WriteString(`<section data-section="` + templ.EscapeString(section) + `"><h2>` + templ.EscapeString(section) + `</h2><ul>`)
			for _, spec := range s.registry.BySection(section) {
				b.WriteString(`<li><a data-specimen="` + templ.EscapeString(spec.ID) + `" href="` + templ.EscapeString(spec.Path()) + `">` + templ.EscapeString(spec.Title) + `</a></li>`)
			}
			b.WriteString(`</ul></section>`)
		}
		b.WriteString(`</main>`)

		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase"}, rawComponent(b.String()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	}
}

// buildCSP maps a bundle's deterministic Requirements into a strict CSP header
// value and adds the host-owned baseline. The kit never writes a header; the
// host does. style-src is exactly 'self' (the kit emits no inline style and no
// server-rendered style attribute; the host theme stylesheet is same-origin under
// 'self'); connect-src 'self' is a host concern for HTMX pages.
func buildCSP(bundle *goth.Bundle, allowConnect bool) string {
	req := bundle.Requirements()
	directives := map[string][]string{
		"default-src":     {"'none'"},
		"base-uri":        {"'none'"},
		"form-action":     {"'self'"},
		"frame-ancestors": {"'none'"},
		"img-src":         {"'self'"},
		"font-src":        {"'self'"},
	}
	if allowConnect {
		directives["connect-src"] = []string{"'self'"}
	}
	for _, d := range req.Directives() {
		sources, _ := req.Sources(d)
		merged := append([]string{}, sources...)
		directives[string(d)] = merged
	}

	keys := make([]string, 0, len(directives))
	for k := range directives {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+strings.Join(directives[k], " "))
	}
	return strings.Join(parts, "; ")
}

// rawComponent wraps host-authored, trusted specimen markup as a templ.Component
// so it can be composed into goth.Bundle.Document. The showcase authors this
// markup itself; it is not caller/user input.
func rawComponent(html string) templ.Component {
	return templ.Raw(html)
}
