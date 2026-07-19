package primitives

import (
	"strings"
	"testing"
)

// GOTH-5.3 Data Table (P52). These tests prove the server-owned compound parts:
// the region wrapper, the swappable content region + loading state, the sortable
// header (real link + aria-sort), the empty row, the live status region, the
// attribute-merge ownership, and the no-inline-style invariant.

// TestNoDataTablePrimitiveEmitsInlineStyle proves invariant (a): no Data Table
// part emits an inline style= in any state.
func TestNoDataTablePrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, DataTable(DataTableProps{Label: "People"}), "x"),
		renderKids(t, DataTableToolbar(DataTableToolbarProps{}), "x"),
		renderKids(t, DataTableContent(DataTableContentProps{Busy: true}), "x"),
		renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{URL: mustParseURL(t, "/?sort=name"), Direction: SortAscending}), "Name"),
		renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{URL: mustParseURL(t, "/?sort=age"), Direction: SortNone}), "Age"),
		renderKids(t, DataTableEmpty(DataTableEmptyProps{Colspan: 4}), "No results"),
		renderKids(t, DataTableStatus(DataTableStatusProps{Assertive: true}), "Error"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("data-table primitive emitted an inline style=: %s", o)
		}
	}
}

// TestDataTableRegionAndContent proves the region wrapper's aria-label and the
// content region's server-owned loading state.
func TestDataTableRegionAndContent(t *testing.T) {
	region := renderKids(t, DataTable(DataTableProps{Base: Base{ID: "dt"}, Label: "People"}), "x")
	mustContain(t, region,
		`class="goth-data-table"`, `data-slot="data-table"`, `role="region"`,
		`aria-label="People"`, `id="dt"`)

	idle := renderKids(t, DataTableContent(DataTableContentProps{Base: Base{ID: "dt-content"}}), "rows")
	mustContain(t, idle,
		`class="goth-data-table-content"`, `data-slot="data-table-content"`,
		`data-state="idle"`, `id="dt-content"`, "rows")
	mustNotContain(t, idle, `aria-busy`)

	busy := renderKids(t, DataTableContent(DataTableContentProps{Busy: true}), "rows")
	mustContain(t, busy, `data-state="loading"`, `aria-busy="true"`)
}

// TestDataTableSortHeader proves the sortable header: aria-sort reflects the
// CURRENT direction, the sort control is a real link to the server-produced URL,
// and the direction hook drives the glyph.
func TestDataTableSortHeader(t *testing.T) {
	asc := renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{
		URL: mustParseURL(t, "/people?sort=name&dir=desc"), Direction: SortAscending,
	}), "Name")
	mustContain(t, asc,
		`<th`, `class="goth-data-table-sort-header"`, `scope="col"`,
		`aria-sort="ascending"`, `data-slot="data-table-sort-header"`, `data-direction="ascending"`,
		`<a`, `href="/people?sort=name&amp;dir=desc"`, `class="goth-data-table-sort"`,
		`data-slot="data-table-sort-link"`, `data-slot="data-table-sort-label"`, "Name")

	none := renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{
		URL: mustParseURL(t, "/people?sort=age"), Direction: SortNone,
	}), "Age")
	mustContain(t, none, `aria-sort="none"`, `data-direction="none"`)

	desc := renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{
		URL: mustParseURL(t, "/people?sort=age"), Direction: SortDescending,
	}), "Age")
	mustContain(t, desc, `aria-sort="descending"`, `data-direction="descending"`)
}

// TestDataTableSortHeaderCarriesHXAttributes proves the caller's hx-* (built by
// the typed htmx.Attrs helper) merges onto the sort LINK — the interactive
// element — without overwriting the owned data-slot.
func TestDataTableSortHeaderCarriesHXAttributes(t *testing.T) {
	out := renderKids(t, DataTableSortHeader(DataTableSortHeaderProps{
		URL:       mustParseURL(t, "/people?sort=name"),
		Direction: SortNone,
		Base: Base{Attributes: map[string]any{
			"hx-get":    "/people?sort=name",
			"hx-target": "#dt-content",
			"hx-swap":   "outerHTML show:none",
			"data-slot": "hijack",
		}},
	}), "Name")
	mustContain(t, out,
		`hx-get="/people?sort=name"`, `hx-target="#dt-content"`, `hx-swap="outerHTML show:none"`,
		`data-slot="data-table-sort-link"`)
	mustNotContain(t, out, `data-slot="hijack"`)
}

// TestDataTableEmpty proves the no-results row spans the given column count.
func TestDataTableEmpty(t *testing.T) {
	spanned := renderKids(t, DataTableEmpty(DataTableEmptyProps{Colspan: 5}), "No results found.")
	mustContain(t, spanned,
		`<tr`, `class="goth-data-table-empty"`, `data-slot="data-table-empty"`,
		`<td`, `colspan="5"`, `data-slot="data-table-empty-cell"`, "No results found.")

	single := renderKids(t, DataTableEmpty(DataTableEmptyProps{}), "Empty")
	mustNotContain(t, single, `colspan`)
}

// TestDataTableStatus proves the live-region roles for polite (default) and
// assertive (error) announcements.
func TestDataTableStatus(t *testing.T) {
	polite := renderKids(t, DataTableStatus(DataTableStatusProps{}), "12 results")
	mustContain(t, polite,
		`class="goth-data-table-status"`, `data-slot="data-table-status"`,
		`role="status"`, `aria-live="polite"`, "12 results")

	alert := renderKids(t, DataTableStatus(DataTableStatusProps{Assertive: true}), "Load failed")
	mustContain(t, alert, `role="alert"`, `aria-live="assertive"`, "Load failed")
}

// TestDataTableSortDirectionEnum proves the SortDirection zero-value default and
// Valid membership.
func TestDataTableSortDirectionEnum(t *testing.T) {
	if SortNone.ariaSort() != "none" {
		t.Error("zero-value SortDirection should map to aria-sort none")
	}
	if !SortNone.Valid() || !SortAscending.Valid() || !SortDescending.Valid() {
		t.Error("known SortDirection values should be Valid")
	}
	if SortDirection("sideways").Valid() {
		t.Error("unknown SortDirection should not be Valid")
	}
	if SortDirection("sideways").ariaSort() != "none" {
		t.Error("unknown SortDirection should render the safe none default")
	}
}

// TestDataTableMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch.
func TestDataTableMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, DataTable(DataTableProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"role":      "banner",
			"data-slot": "hijack",
			"class":     "dropped",
		},
	}}), "x")
	mustContain(t, out, `role="region"`, `data-slot="data-table"`, `goth-data-table custom-x`)
	mustNotContain(t, out, `banner`, `hijack`, `dropped`)
}
