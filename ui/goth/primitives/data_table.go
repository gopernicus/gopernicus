package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// DataTable (P52, family F4) is a server-owned data grid COMPOSED from existing
// primitives: it arranges a Table (P24, its responsive wrapper and semantics), an
// optional filter form, sortable column headers, Pagination (P19), row-selection
// Checkbox (P28) controls, row-action menus (Dropdown Menu), and loading/empty
// status regions. The server owns the authoritative sort, filter, page, and
// selection state; the primitive holds none.
//
// The compound parts the caller composes are:
//
//   - DataTable          the labelled region wrapper (the whole grid).
//   - DataTableToolbar   the row above the table for the filter form and actions;
//     it sits OUTSIDE the swappable content so a live filter input keeps its
//     focus/caret across an HTMX swap.
//   - DataTableContent   the swappable inner region (the HTMX target) holding the
//     Table, Pagination, and status. Busy renders the loading state.
//   - DataTableSortHeader a <th> column header whose sort control is a real link
//     (server-produced next-sort URL) carrying aria-sort for the CURRENT state.
//   - DataTableEmpty     a full-width "no results" body row.
//   - DataTableStatus    an aria-live region for loading/result/error announcements.
//
// No-JS baseline. Sorting, filtering, and paging are ordinary links and a form
// GET, so every state has a shareable URL and works with no JavaScript; the server
// re-renders the whole document. Selection checkboxes submit natively.
//
// HTMX enhancement (optional). The filter input, sort links, and pagination links
// carry explicit hx-* (built by the typed htmx.Attrs helper) that swap
// DataTableContent's outerHTML, preserving scroll via the frozen swap modifiers
// (htmx.SwapModifiers) so the viewport does not jump; the toolbar and its filter
// input stay mounted, so focus/caret survive. A non-HTMX request degrades to the
// full-document response. No new Alpine controller is introduced.
//
// data-slot hooks: data-table, data-table-toolbar, data-table-content,
// data-table-sort-header, data-table-sort-link, data-table-empty,
// data-table-status.

// SortDirection is a column's CURRENT sort state, surfaced as aria-sort on the
// header cell. The zero value is "not sorted by this column".
type SortDirection string

const (
	// SortNone is the zero value: this column is not the active sort.
	SortNone SortDirection = ""
	// SortAscending marks the active ascending sort (aria-sort="ascending").
	SortAscending SortDirection = "ascending"
	// SortDescending marks the active descending sort (aria-sort="descending").
	SortDescending SortDirection = "descending"
)

// Valid reports whether d is a known direction (the zero value included).
func (d SortDirection) Valid() bool {
	switch d {
	case SortNone, SortAscending, SortDescending:
		return true
	default:
		return false
	}
}

// ariaSort maps the direction to the aria-sort token. An unknown value renders
// the safe "none" default.
func (d SortDirection) ariaSort() string {
	switch d {
	case SortAscending:
		return "ascending"
	case SortDescending:
		return "descending"
	default:
		return "none"
	}
}

// attr maps the direction to the data-direction hook value.
func (d SortDirection) attr() string {
	switch d {
	case SortAscending:
		return "ascending"
	case SortDescending:
		return "descending"
	default:
		return "none"
	}
}

// DataTableProps configures the DataTable region wrapper.
type DataTableProps struct {
	Base
	// Label is the region's accessible name (aria-label). Empty omits it.
	Label string
}

// DataTableToolbarProps configures the toolbar row (filter form + actions).
type DataTableToolbarProps struct{ Base }

// DataTableContentProps configures the swappable content region (the HTMX target
// that holds the Table, Pagination, and status).
type DataTableContentProps struct {
	Base
	// Busy renders the loading state (aria-busy="true" + data-state="loading") so
	// CSS can reveal a busy affordance during an in-flight HTMX swap.
	Busy bool
}

// DataTableSortHeaderProps configures a sortable column header.
type DataTableSortHeaderProps struct {
	Base
	// URL is the server-produced link that re-sorts by this column (the NEXT sort
	// state). It works as a plain link with no JavaScript and carries hx-* (via
	// Base.Attributes) when the grid is HTMX-enhanced.
	URL URL
	// Direction is this column's CURRENT sort state, surfaced as aria-sort on the
	// header cell and as the visible direction glyph.
	Direction SortDirection
}

// DataTableEmptyProps configures the "no results" body row.
type DataTableEmptyProps struct {
	Base
	// Colspan spans the empty cell across all columns. Zero renders a single cell.
	Colspan int
}

// DataTableStatusProps configures the aria-live status region.
type DataTableStatusProps struct {
	Base
	// Assertive uses aria-live="assertive" (for errors); the default is polite.
	Assertive bool
}

func dataTableAttrs(p DataTableProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "data-table",
		"role":      "region",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func dataTableContentAttrs(p DataTableContentProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "data-table-content",
		"data-state": dataTableState(p.Busy),
	}
	if p.Busy {
		owned["aria-busy"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func dataTableState(busy bool) string {
	if busy {
		return "loading"
	}
	return "idle"
}

func dataTablePartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

func dataTableSortLinkAttrs(p DataTableSortHeaderProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "data-table-sort-link"})
}

func dataTableEmptyCellAttrs(p DataTableEmptyProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "data-table-empty-cell"}
	if p.Colspan > 0 {
		owned["colspan"] = strconv.Itoa(p.Colspan)
	}
	return owned
}

func dataTableStatusAttrs(p DataTableStatusProps) templ.Attributes {
	live := "polite"
	role := "status"
	if p.Assertive {
		live = "assertive"
		role = "alert"
	}
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "data-table-status",
		"role":      role,
		"aria-live": live,
	})
}
