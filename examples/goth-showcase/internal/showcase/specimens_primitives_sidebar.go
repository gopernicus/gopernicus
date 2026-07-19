package showcase

import (
	"net/http"
	"net/url"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// GOTH-5.4 Sidebar specimens (P54). The SERVER owns every authoritative state — the
// active navigation item, the desktop expanded/collapsed rail, and the mobile
// off-canvas open flag — and re-renders the shell for the submitted query. Every
// specimen renders the REAL Sidebar primitive (composing the gothDialog overlay
// mechanics for the mobile sheet and Collapsible/native-details is available for
// nested groups) so the browser/axe harness exercises the actual emitted surface.
//
// No-JS baseline: navigation items are real <a> links carrying aria-current, and the
// desktop collapse control (SidebarRail) is a real round-trip link that flips the
// server-owned collapsed state — every state has a shareable URL and reloads the
// whole document. Preference PERSISTENCE is server-owned here: the /sidebar route
// simply reflects the query it is given, so a host would persist the preference in
// its own namespaced cookie; the kit adds no client persistence surface. The mobile
// sheet's client enhancement is the frozen gothDialog (SidebarTrigger opens it,
// SidebarClose/scrim/Escape close it), and the server-open variant proves the sheet
// is readable with no JavaScript at all.

// sbItem is one navigation entry.
type sbItem struct {
	Key   string
	Label string
	Icon  string
}

var sidebarNav = []sbItem{
	{"dashboard", "Dashboard", svgSquare},
	{"projects", "Projects", svgLayers},
	{"team", "Team", svgUsers},
	{"settings", "Settings", svgGear},
}

const (
	svgSquare = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>`
	svgLayers = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M12 3 2 9l10 6 10-6-10-6Z"/></svg>`
	svgUsers  = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="9" cy="8" r="3"/><path d="M3 20a6 6 0 0 1 12 0"/></svg>`
	svgGear   = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M12 3v3M12 18v3M3 12h3M18 12h3"/></svg>`
	svgMenu   = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18"/></svg>`
	svgClose  = `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"/></svg>`
	svgChevrn = `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m9 6 6 6-6 6"/></svg>`
)

// sbState is the server-owned shell state parsed from the query.
type sbState struct {
	Active    string
	Collapsed bool
}

func parseSBState(q url.Values) sbState {
	st := sbState{Active: q.Get("active"), Collapsed: q.Get("collapsed") == "1"}
	if st.Active == "" {
		st.Active = "dashboard"
	}
	return st
}

// sbNavURL builds a shareable link that navigates to an item, preserving the
// collapsed preference (server-owned active-item + collapsed state).
func sbNavURL(st sbState, key string) string {
	v := url.Values{}
	v.Set("active", key)
	if st.Collapsed {
		v.Set("collapsed", "1")
	}
	return "/sidebar?" + v.Encode()
}

// sbToggleURL builds the SidebarRail round-trip: flip the collapsed state, keep the
// active item.
func sbToggleURL(st sbState) string {
	v := url.Values{}
	v.Set("active", st.Active)
	if !st.Collapsed {
		v.Set("collapsed", "1")
	}
	return "/sidebar?" + v.Encode()
}

// sidebarShell renders the whole app shell (provider + sidebar + inset) for the
// server-owned state. open renders the mobile sheet server-open (the no-JS baseline).
func sidebarShell(st sbState, open bool) string {
	// The menu: one group of real navigation links; the active item carries
	// aria-current. Projects nests a native-details Collapsible of sub-links, proving
	// the GOTH-3.1 composition inside the sidebar.
	var menu string
	for _, it := range sidebarNav {
		btn := compKids(primitives.SidebarMenuButton(primitives.SidebarMenuButtonProps{
			URL:    mustURL(sbNavURL(st, it.Key)),
			Active: st.Active == it.Key,
			Icon:   templ.Raw(it.Icon),
		}), templ.EscapeString(it.Label))

		if it.Key == "projects" {
			sub := compKids(primitives.SidebarMenuSub(primitives.SidebarMenuSubProps{}),
				compKids(primitives.SidebarMenuSubItem(primitives.SidebarMenuSubItemProps{}),
					compKids(primitives.SidebarMenuSubButton(primitives.SidebarMenuSubButtonProps{URL: mustURL(sbNavURL(st, "projects-active"))}), "Active"))+
					compKids(primitives.SidebarMenuSubItem(primitives.SidebarMenuSubItemProps{}),
						compKids(primitives.SidebarMenuSubButton(primitives.SidebarMenuSubButtonProps{URL: mustURL(sbNavURL(st, "projects-archived"))}), "Archived")))
			// A native <details> Collapsible discloses the sub-links with no JS.
			collapsible := compKids(primitives.Collapsible(primitives.CollapsibleProps{Open: true}),
				compKids(primitives.CollapsibleTrigger(primitives.CollapsibleTriggerProps{}), templ.EscapeString(it.Label))+
					compKids(primitives.CollapsibleContent(primitives.CollapsibleContentProps{}), sub))
			menu += compKids(primitives.SidebarMenuItem(primitives.SidebarMenuItemProps{}), collapsible)
			continue
		}
		menu += compKids(primitives.SidebarMenuItem(primitives.SidebarMenuItemProps{}), btn)
	}

	group := compKids(primitives.SidebarGroup(primitives.SidebarGroupProps{}),
		compKids(primitives.SidebarGroupLabel(primitives.SidebarGroupLabelProps{}), "Platform")+
			compKids(primitives.SidebarMenu(primitives.SidebarMenuProps{}), menu))

	// The rail collapse control: a real round-trip link. aria-expanded reflects the
	// current (server-owned) state.
	railLabel := "Collapse sidebar"
	if st.Collapsed {
		railLabel = "Expand sidebar"
	}
	rail := compKids(primitives.SidebarRail(primitives.SidebarRailProps{
		URL:      mustURL(sbToggleURL(st)),
		Expanded: !st.Collapsed,
		Label:    railLabel,
	}), svgChevrn+`<span data-slot="sidebar-rail-text">`+templ.EscapeString(railLabel)+`</span>`)

	header := compKids(primitives.SidebarHeader(primitives.SidebarHeaderProps{}),
		`<strong data-slot="sidebar-brand">Acme Inc</strong>`+
			compKids(primitives.SidebarClose(primitives.SidebarCloseProps{}), svgClose))

	footer := compKids(primitives.SidebarFooter(primitives.SidebarFooterProps{}), rail)

	panelInner := header +
		compKids(primitives.SidebarContent(primitives.SidebarContentProps{}), group) +
		footer

	panel := compKids(primitives.SidebarPanel(primitives.SidebarPanelProps{Label: "Primary"}), panelInner)

	trigger := compKids(primitives.SidebarTrigger(primitives.SidebarTriggerProps{}),
		svgMenu+`<span data-slot="sidebar-trigger-text">Menu</span>`)

	sidebar := compKids(primitives.Sidebar(primitives.SidebarProps{
		Open:      open,
		Collapsed: st.Collapsed,
	}), trigger+panel)

	// The inset shows the active item so a navigation round-trip is observable.
	active := st.Active
	inset := compKids(primitives.SidebarInset(primitives.SidebarInsetProps{}),
		`<h2 data-slot="sidebar-active-label">`+templ.EscapeString(activeTitle(active))+`</h2>`+
			`<p>The active navigation item, the collapsed rail, and the mobile sheet are all server-owned. Every link is shareable and reloads the whole document with no JavaScript.</p>`)

	return compKids(primitives.SidebarProvider(primitives.SidebarProviderProps{}), sidebar+inset)
}

func activeTitle(key string) string {
	for _, it := range sidebarNav {
		if it.Key == key {
			return it.Label
		}
	}
	switch key {
	case "projects-active":
		return "Active projects"
	case "projects-archived":
		return "Archived projects"
	}
	return "Dashboard"
}

func registerSidebarSpecimens(r *Registry) {
	// Sidebar (P54) interactive: the desktop rail + a server round-trip collapse
	// control + the mobile gothDialog sheet (SidebarTrigger opens it).
	r.Register(Specimen{
		ID:        "primitive-sidebar",
		Title:     "Sidebar (P54)",
		Section:   SectionPrimitive,
		Primitive: "P54",
		Profile:   goth.Interactive,
		Body:      sidebarInteractiveSpecimen,
	})

	// Sidebar (P54) no-JS: the mobile sheet is rendered server-open and readable with
	// no JavaScript; navigation and the rail collapse are real links.
	r.Register(Specimen{
		ID:        "primitive-sidebar-nojs",
		Title:     "Sidebar no-JS (P54)",
		Section:   SectionPrimitive,
		Primitive: "P54",
		Profile:   goth.StylesOnly,
		Body:      sidebarNoJSSpecimen,
	})

	// Sidebar (P54) RTL: the same server-open shell under dir="rtl". Logical
	// properties (inset-inline-*, margin-inline-*) flip the rail/sheet to the
	// inline-end edge without bespoke RTL markup.
	r.Register(Specimen{
		ID:        "primitive-sidebar-rtl",
		Title:     "Sidebar RTL (P54)",
		Section:   SectionPrimitive,
		Primitive: "P54",
		Profile:   goth.StylesOnly,
		Dir:       theme.DirectionRTL,
		Body:      sidebarNoJSSpecimen,
	})
}

func sidebarInteractiveSpecimen() string {
	st := sbState{Active: "dashboard"}
	body := `<section data-slot="sidebar-specimen">` +
		`<p>Desktop shows a collapsible rail; narrow the viewport and the same markup becomes an off-canvas sheet opened by the menu button (gothDialog). Collapse is a server round-trip.</p>` +
		sidebarShell(st, false) +
		`</section>`
	return page("Sidebar", body)
}

func sidebarNoJSSpecimen() string {
	st := sbState{Active: "team"}
	body := `<section data-slot="sidebar-nojs-specimen">` +
		`<p>No JavaScript: the mobile sheet is server-open and readable, navigation items are real links carrying aria-current, and the collapse control is a real round-trip link.</p>` +
		sidebarShell(st, true) +
		`</section>`
	return page("Sidebar (no-JS)", body)
}

// registerSidebarFixtures wires the server-owned round-trip route. /sidebar
// re-renders the shell for the submitted query (active item + collapsed rail): every
// navigation link and the rail collapse control land here, proving server ownership,
// shareable URLs, and history re-fetch correctness.
func (s *Server) registerSidebarFixtures() {
	s.handler.Handle(http.MethodGet, "/sidebar", func(w http.ResponseWriter, r *http.Request) {
		st := parseSBState(r.URL.Query())

				bundle := s.bundles[goth.Interactive]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := `<main data-slot="sidebar-page"><h1>Sidebar</h1>` +
			sidebarShell(st, false) +
			`<p><a href="/specimen/primitive-sidebar">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — sidebar"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
