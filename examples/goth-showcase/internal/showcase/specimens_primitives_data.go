package showcase

import (
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/htmx"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-5.3 Data Table specimens (P52). The SERVER owns the sort, filter, page, and
// selection state end to end: /data-table re-renders the grid for the submitted
// query and returns the swappable content region to an HTMX request or a full
// document to a direct one. Every specimen renders the REAL DataTable primitive
// (composing Table, Pagination, Checkbox) so the browser/axe harness exercises the
// actual emitted surface.
//
// The no-JS baseline and the HTMX-enhanced variant share the SAME server rendering
// and the SAME shareable URLs: sort headers and pagination are real links carrying
// the full state; the filter is a form GET. HTMX only swaps the content region
// (outerHTML, show:none to preserve scroll) while the toolbar filter input stays
// mounted, so focus/caret survive. The typed htmx helpers are the load-bearing
// GOTH-5.3 finalization consumers: htmx.Trigger debounces the live filter and
// htmx.SwapModifiers preserves scroll on the content swap.

const dataTablePageSize = 4

type dtPerson struct {
	Name   string
	Role   string
	Age    int
	Status string
}

var dataTablePeople = []dtPerson{
	{"Ada Lovelace", "Engineer", 36, "active"},
	{"Alan Turing", "Researcher", 41, "active"},
	{"Barbara Liskov", "Architect", 52, "active"},
	{"Carl Sagan", "Analyst", 46, "inactive"},
	{"Dorothy Vaughan", "Manager", 58, "active"},
	{"Edsger Dijkstra", "Engineer", 49, "inactive"},
	{"Grace Hopper", "Architect", 61, "active"},
	{"Katherine Johnson", "Analyst", 55, "active"},
	{"Linus Torvalds", "Engineer", 44, "active"},
	{"Margaret Hamilton", "Manager", 47, "inactive"},
	{"Radia Perlman", "Researcher", 51, "active"},
	{"Tim Berners-Lee", "Architect", 59, "active"},
}

// dtState is the server-owned grid state parsed from the request query. The
// primitive holds none of it.
type dtState struct {
	Q    string
	Sort string // name | age | role
	Dir  string // asc | desc
	Page int
	Sel  map[string]bool
	HTMX bool // whether to attach hx-* (the Full-profile enhanced variant)
}

// parseDTState reads the server-owned state from a query string.
func parseDTState(q url.Values, htmxEnhanced bool) dtState {
	st := dtState{
		Q:    strings.TrimSpace(q.Get("q")),
		Sort: q.Get("sort"),
		Dir:  q.Get("dir"),
		Page: 1,
		Sel:  map[string]bool{},
		HTMX: htmxEnhanced,
	}
	switch st.Sort {
	case "name", "age", "role":
	default:
		st.Sort = "name"
	}
	if st.Dir != "desc" {
		st.Dir = "asc"
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		st.Page = p
	}
	for _, name := range q["sel"] {
		st.Sel[name] = true
	}
	return st
}

// dtMatching returns the filtered + sorted people (server-owned).
func dtMatching(st dtState) []dtPerson {
	var out []dtPerson
	q := strings.ToLower(st.Q)
	for _, p := range dataTablePeople {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		var less bool
		switch st.Sort {
		case "age":
			less = out[i].Age < out[j].Age
		case "role":
			less = out[i].Role < out[j].Role
		default:
			less = out[i].Name < out[j].Name
		}
		if st.Dir == "desc" {
			return !less
		}
		return less
	})
	return out
}

// dtValues builds the shared query values (q + sort + dir + page + committed
// selection) so every sort/page link is a shareable URL preserving filter state.
func dtValues(st dtState) url.Values {
	v := url.Values{}
	if st.Q != "" {
		v.Set("q", st.Q)
	}
	v.Set("sort", st.Sort)
	v.Set("dir", st.Dir)
	if st.Page > 1 {
		v.Set("page", strconv.Itoa(st.Page))
	}
	names := make([]string, 0, len(st.Sel))
	for name, on := range st.Sel {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		v.Add("sel", name)
	}
	return v
}

func dtURL(v url.Values) string { return "/data-table?" + v.Encode() }

// dtLinkAttrs returns the hx-* for a sort/page link: swap the content region and
// preserve scroll via the frozen swap modifiers. Empty unless the grid is enhanced.
func dtLinkAttrs(st dtState, u string) templ.Attributes {
	if !st.HTMX {
		return nil
	}
	no := false
	attrs, _ := htmx.Attrs{
		Method:   htmx.MethodGet,
		URL:      u,
		Target:   "#dt-content",
		Swap:     htmx.SwapOuterHTML,
		SwapMods: htmx.SwapModifiers{Show: "none", FocusScroll: &no},
		PushURL:  true,
	}.Build()
	return attrs
}

// dtSortHeader renders a sortable column header: the link re-sorts by col (the
// next direction), carrying the full state; aria-sort reflects the current state.
func dtSortHeader(st dtState, col, label string) string {
	dir := primitives.SortNone
	next := "asc"
	if st.Sort == col {
		if st.Dir == "desc" {
			dir = primitives.SortDescending
		} else {
			dir = primitives.SortAscending
			next = "desc"
		}
	}
	target := dtState{Q: st.Q, Sort: col, Dir: next, Page: 1, Sel: st.Sel}
	u := dtURL(dtValues(target))
	return compKids(primitives.DataTableSortHeader(primitives.DataTableSortHeaderProps{
		URL:       mustURL(u),
		Direction: dir,
		Base:      primitives.Base{Attributes: dtLinkAttrs(st, u)},
	}), templ.EscapeString(label))
}

// dtPagination renders the server-owned page links (prev / numbers / next), each a
// shareable URL carrying the current filter/sort/selection.
func dtPagination(st dtState, pages int) string {
	if pages <= 1 {
		return ""
	}
	pageURL := func(p int) string {
		target := st
		target.Page = p
		return dtURL(dtValues(target))
	}
	var items strings.Builder
	if st.Page > 1 {
		u := pageURL(st.Page - 1)
		items.WriteString(compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
			comp(primitives.PaginationPrevious(primitives.PaginationPreviousProps{
				URL: mustURL(u), Base: primitives.Base{Attributes: dtLinkAttrs(st, u)},
			}))))
	}
	for p := 1; p <= pages; p++ {
		u := pageURL(p)
		items.WriteString(compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
			compKids(primitives.PaginationLink(primitives.PaginationLinkProps{
				URL:    mustURL(u),
				Active: p == st.Page,
				Label:  "Page " + strconv.Itoa(p),
				Base:   primitives.Base{Attributes: dtLinkAttrs(st, u)},
			}), strconv.Itoa(p))))
	}
	if st.Page < pages {
		u := pageURL(st.Page + 1)
		items.WriteString(compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
			comp(primitives.PaginationNext(primitives.PaginationNextProps{
				URL: mustURL(u), Base: primitives.Base{Attributes: dtLinkAttrs(st, u)},
			}))))
	}
	content := compKids(primitives.PaginationContent(primitives.PaginationContentProps{}), items.String())
	return compKids(primitives.Pagination(primitives.PaginationProps{Label: "People pages"}), content)
}

// dtContentRegion renders the swappable content region (#dt-content): the status
// line, the table (sortable headers + selection checkboxes + rows or the empty
// row), and pagination. This is exactly what an HTMX sort/page/filter swaps.
func dtContentRegion(st dtState) string {
	matching := dtMatching(st)
	total := len(matching)
	pages := int(math.Ceil(float64(total) / float64(dataTablePageSize)))
	if pages == 0 {
		pages = 1
	}
	if st.Page > pages {
		st.Page = pages
	}
	start := (st.Page - 1) * dataTablePageSize
	end := start + dataTablePageSize
	if end > total {
		end = total
	}
	var visible []dtPerson
	if start < total {
		visible = matching[start:end]
	}

	selected := 0
	for _, on := range st.Sel {
		if on {
			selected++
		}
	}
	status := compKids(primitives.DataTableStatus(primitives.DataTableStatusProps{}),
		templ.EscapeString(strconv.Itoa(total)+" of "+strconv.Itoa(len(dataTablePeople))+" people · "+strconv.Itoa(selected)+" selected"))

	head := compKids(primitives.TableHeader(primitives.TableHeaderProps{}),
		compKids(primitives.TableRow(primitives.TableRowProps{}),
			compKids(primitives.TableHead(primitives.TableHeadProps{}), "Select")+
				dtSortHeader(st, "name", "Name")+
				dtSortHeader(st, "age", "Age")+
				dtSortHeader(st, "role", "Role")+
				compKids(primitives.TableHead(primitives.TableHeadProps{}), "Status")))

	var rows strings.Builder
	if len(visible) == 0 {
		rows.WriteString(compKids(primitives.DataTableEmpty(primitives.DataTableEmptyProps{Colspan: 5}),
			"No people match this filter."))
	}
	for _, p := range visible {
		cells := compKids(primitives.TableCell(primitives.TableCellProps{}),
			comp(primitives.Checkbox(primitives.CheckboxProps{
				Name:    "sel",
				Value:   p.Name,
				Checked: st.Sel[p.Name],
				Base:    primitives.Base{Attributes: templ.Attributes{"aria-label": "Select " + p.Name}},
			}))) +
			compKids(primitives.TableCell(primitives.TableCellProps{}), templ.EscapeString(p.Name)) +
			compKids(primitives.TableCell(primitives.TableCellProps{}), strconv.Itoa(p.Age)) +
			compKids(primitives.TableCell(primitives.TableCellProps{}), templ.EscapeString(p.Role)) +
			compKids(primitives.TableCell(primitives.TableCellProps{}), dtStatusBadge(p.Status))
		rows.WriteString(compKids(primitives.TableRow(primitives.TableRowProps{}), cells))
	}
	body := compKids(primitives.TableBody(primitives.TableBodyProps{}), rows.String())

	table := compKids(primitives.Table(primitives.TableProps{Base: primitives.Base{Attributes: templ.Attributes{"aria-label": "People"}}}),
		head+body)

	inner := status + table + dtPagination(st, pages)
	return compKids(primitives.DataTableContent(primitives.DataTableContentProps{Base: primitives.Base{ID: "dt-content"}}), inner)
}

func dtStatusBadge(status string) string {
	variant := primitives.BadgeDefault
	if status == "inactive" {
		variant = primitives.BadgeSecondary
	}
	return compKids(primitives.Badge(primitives.BadgeProps{Variant: variant}), templ.EscapeString(status))
}

// dataTableForm renders the whole grid inside one GET form: the toolbar (filter
// input + no-JS submit + bulk "Update selection" submit) sits OUTSIDE the swappable
// content so the filter input keeps focus/caret across an HTMX content swap.
func dataTableForm(st dtState) string {
	var filterAttrs templ.Attributes
	if st.HTMX {
		// The typed htmx.Trigger debounces the live filter — the exact GOTH-5.3
		// finalization consumer (input changed delay). It sends the query only and
		// swaps the content region preserving scroll.
		no := false
		filterAttrs, _ = htmx.Attrs{
			Method:   htmx.MethodGet,
			URL:      "/data-table",
			Target:   "#dt-content",
			Swap:     htmx.SwapOuterHTML,
			SwapMods: htmx.SwapModifiers{Show: "none", FocusScroll: &no},
			Trigger:  htmx.Trigger{Event: "input", Changed: true, Delay: 300 * time.Millisecond},
			PushURL:  true,
		}.Build()
	}

	filter := comp(primitives.Input(primitives.InputProps{
		Type:        primitives.InputSearch,
		Name:        "q",
		Value:       st.Q,
		Placeholder: "Filter by name…",
		Base:        primitives.Base{ID: "dt-filter", Attributes: mergeAria(filterAttrs, "Filter people by name")},
	}))
	submit := compKids(primitives.Button(primitives.ButtonProps{Type: primitives.ButtonTypeSubmit, Variant: primitives.ButtonSecondary}), "Filter")
	toolbar := compKids(primitives.DataTableToolbar(primitives.DataTableToolbarProps{}), filter+submit)

	grid := compKids(primitives.DataTable(primitives.DataTableProps{Base: primitives.Base{ID: "dt"}, Label: "People data table"}),
		toolbar+dtContentRegion(st))

	return `<form method="get" action="/data-table" data-slot="data-table-form">` + grid + `</form>`
}

// mergeAria adds an aria-label to a (possibly nil) attribute set.
func mergeAria(attrs templ.Attributes, label string) templ.Attributes {
	if attrs == nil {
		attrs = templ.Attributes{}
	}
	attrs["aria-label"] = label
	return attrs
}

func registerDataSpecimens(r *Registry) {
	// Data Table (P52) no-JS: sort/filter/page are real links and a form GET; every
	// state is a shareable URL and works with Alpine and HTMX absent.
	r.Register(Specimen{
		ID:        "primitive-data-table-nojs",
		Title:     "Data Table no-JS (P52)",
		Section:   SectionPrimitive,
		Primitive: "P52",
		Profile:   goth.StylesOnly,
		Body:      dataTableNoJSSpecimen,
	})

	// Data Table (P52) HTMX: the SAME server rendering, enhanced — live filter
	// (debounced), sort, and page swap the content region while the toolbar stays
	// mounted (focus/scroll preserved). Degrades to the no-JS full-document path.
	r.Register(Specimen{
		ID:           "primitive-data-table",
		Title:        "Data Table (P52)",
		Section:      SectionPrimitive,
		Primitive:    "P52",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         dataTableHTMXSpecimen,
	})
}

func dataTableNoJSSpecimen() string {
	st := dtState{Sort: "name", Dir: "asc", Page: 1, Sel: map[string]bool{}}
	body := `<section data-slot="data-table-nojs-specimen">` +
		`<p>No JavaScript: sort headers and pagination are real links, and the filter is a form GET — every state has a shareable URL and reloads the whole document.</p>` +
		dataTableForm(st) +
		`</section>`
	return page("Data Table (no-JS)", body)
}

func dataTableHTMXSpecimen() string {
	st := dtState{Sort: "name", Dir: "asc", Page: 1, Sel: map[string]bool{}, HTMX: true}
	body := `<section data-slot="data-table-htmx-specimen">` +
		`<p>HTMX-enhanced: typing filters (debounced), and sorting/paging swap only the content region — scroll and the filter caret are preserved. With no JavaScript this is exactly the no-JS grid.</p>` +
		dataTableForm(st) +
		`</section>`
	return page("Data Table", body)
}

// registerDataFixtures wires the server-owned round-trip route. /data-table
// re-renders the grid for the submitted query: an HTMX request gets the swappable
// content region; a direct request gets the full document (shareable URLs +
// history re-fetch correctness).
func (s *Server) registerDataFixtures() {
	s.handler.Handle(http.MethodGet, "/data-table", func(w http.ResponseWriter, r *http.Request) {
		// This route is the Full-profile enhanced grid: it always renders hx-*
		// links, so both the fragment and the full-document fallback stay
		// interactive. An HTMX request gets the swappable content region; a direct
		// request (a shared URL or a history re-fetch) gets the full document.
		st := parseDTState(r.URL.Query(), true)

		if htmx.FromRequest(r).IsHTMX() {
			writeFragment(w, http.StatusOK, dtContentRegion(st))
			return
		}

				bundle := s.bundles[goth.Full]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := `<main data-slot="data-table-page"><h1>Data Table</h1>` +
			dataTableForm(st) +
			`<p><a href="/specimen/primitive-data-table">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — data table"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
