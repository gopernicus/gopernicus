package showcase

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/components/layouts"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// allPath is the route for the single-page "kitchen sink" gallery.
const allPath = "/all"

// idDefRE captures every id an embedded specimen body defines. idRefAttrRE captures
// the attributes that reference an id so the combine-time namespacer can rewrite a
// definition and every same-document reference in lockstep. Only these attributes
// carry id references in the rendered specimen markup; data-slot/name values that
// happen to equal an id are style/test hooks, not references, and are left alone.
var (
	idDefRE     = regexp.MustCompile(`\bid="([^"]+)"`)
	idRefAttrRE = regexp.MustCompile(`(\s)(id|for|popovertarget|aria-controls|aria-labelledby|aria-describedby|href|hx-target)="([^"]*)"`)
)

// allHandler renders every registered specimen on one scrollable page, grouped by
// section, each under a stable #anchor with a back-to-top affordance. It dogfoods
// the kit's own layout components (DocumentShell + PageHeader) for the page chrome
// and serves under the Full-profile bundle — the superset — so Interactive/Full
// specimens get their runtime while StylesOnly bodies render fine under it. The CSP
// follows the same strict pattern as every other page, with connect-src 'self'
// added for the embedded HTMX fixtures.
func (s *Server) allHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bundle := s.all

		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		doc := bundle.Document(goth.DocumentOptions{
			Title: "goth showcase — all specimens",
		}, rawComponent(s.allBody()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	}
}

// allBody composes the combined page: a PageHeader as the DocumentShell header (the
// #top anchor), a navigation table of contents, and every specimen body reused from
// the registry (no markup is duplicated), grouped by section under stable anchors.
func (s *Server) allBody() string {
	header := layouts.PageHeader(layouts.PageHeaderProps{
		Base:        primitives.Base{ID: "top"},
		Title:       "goth showcase — all specimens",
		Description: "Every registered specimen on one scrollable page, grouped by section.",
	})

	var b strings.Builder

	// Table of contents: a nav landmark of in-page anchor links, grouped by section.
	b.WriteString(`<nav data-slot="all-toc" aria-label="Table of contents"><h2>Contents</h2>`)
	for _, section := range s.registry.Sections() {
		b.WriteString(`<h3>` + templ.EscapeString(sectionLabel(section)) + `</h3><ul>`)
		for _, spec := range s.registry.BySection(section) {
			b.WriteString(`<li><a href="#` + allAnchor(spec.ID) + `">` + templ.EscapeString(spec.Title) + `</a></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</nav>`)

	// Specimen sections: each specimen body reused verbatim from the registry under
	// a labelled section with a stable anchor and a back-to-top link.
	for _, section := range s.registry.Sections() {
		b.WriteString(`<section data-all-section="` + templ.EscapeString(section) + `" aria-labelledby="all-section-` + templ.EscapeString(section) + `">`)
		b.WriteString(`<h2 id="all-section-` + templ.EscapeString(section) + `">` + templ.EscapeString(sectionLabel(section)) + `</h2>`)
		for _, spec := range s.registry.BySection(section) {
			anchor := allAnchor(spec.ID)
			b.WriteString(`<section id="` + anchor + `" data-all-specimen="` + templ.EscapeString(spec.ID) + `" aria-labelledby="` + anchor + `-heading">`)
			b.WriteString(`<div data-slot="all-specimen-head">`)
			b.WriteString(`<h3 id="` + anchor + `-heading" tabindex="-1">` + templ.EscapeString(spec.Title) + `</h3>`)
			b.WriteString(`<a data-slot="all-back-to-top" href="#top">Back to top</a>`)
			b.WriteString(`</div>`)
			b.WriteString(namespaceIDs(anchor+"-", spec.Body()))
			b.WriteString(`</section>`)
		}
		b.WriteString(`</section>`)
	}

	return compKids(layouts.DocumentShell(layouts.DocumentShellProps{Header: header}), b.String())
}

// allAnchor is the stable per-specimen anchor id on the combined page.
func allAnchor(id string) string { return "all-" + id }

// namespaceIDs prefixes every id a specimen body defines — and every same-document
// reference to it (for, popovertarget, aria-controls/labelledby/describedby, and the
// #fragment of href/hx-target) — with prefix, so specimens carrying the same
// caller-authored ids no longer collide when combined onto one page. Standalone
// specimen pages are untouched; only the /all composition rewrites. The named
// controllers read the live DOM id (opt.id, el.id), so uniform prefixing keeps
// combobox activedescendant, popover anchoring, and HTMX targeting consistent.
func namespaceIDs(prefix, body string) string {
	ids := map[string]bool{}
	for _, m := range idDefRE.FindAllStringSubmatch(body, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		return body
	}
	return idRefAttrRE.ReplaceAllStringFunc(body, func(match string) string {
		m := idRefAttrRE.FindStringSubmatch(match)
		space, attr, val := m[1], m[2], m[3]
		switch attr {
		case "id", "for", "popovertarget":
			if ids[val] {
				return space + attr + `="` + prefix + val + `"`
			}
		case "aria-controls", "aria-labelledby", "aria-describedby":
			toks := strings.Fields(val)
			changed := false
			for i, tok := range toks {
				if ids[tok] {
					toks[i] = prefix + tok
					changed = true
				}
			}
			if changed {
				return space + attr + `="` + strings.Join(toks, " ") + `"`
			}
		case "href", "hx-target":
			if strings.HasPrefix(val, "#") && ids[val[1:]] {
				return space + attr + `="#` + prefix + val[1:] + `"`
			}
		}
		return match
	})
}

// sectionLabel is the human label for a section key on the combined page.
func sectionLabel(section string) string {
	switch section {
	case SectionProfile:
		return "Bundle profiles"
	case SectionTheme:
		return "Themes & appearance"
	case SectionHTMX:
		return "HTMX fixtures"
	case SectionMechanics:
		return "Overlay mechanics"
	case SectionPrimitive:
		return "Primitives"
	case SectionComponent:
		return "Components"
	default:
		return section
	}
}
