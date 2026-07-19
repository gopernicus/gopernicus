// Package data holds the opinionated, domain-neutral data compositions built from
// ui/goth primitives (GOTH-7.1): the searchable table toolbar every list page
// opens with. It composes primitives.DataTableToolbar + primitives.Input + a
// no-JS submit Button inside a form GET, so filtering has a shareable URL and
// works with no JavaScript; a host enhances it with explicit hx-* through
// SearchAttributes. It never imports a feature domain, adds a primitive, or emits
// a server-rendered style.
package data

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/components/internal/kit"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// TableToolbarProps configures TableToolbar, the searchable filter row above a
// list/table: a search Input, a no-JS submit Button, an optional Filters slot
// (extra controls), and an optional Actions slot (trailing, e.g. a create
// button). It renders inside a form GET to Action, so every filter state is a
// shareable URL and reloads work with no JavaScript. A host enhances it by
// passing explicit hx-* through SearchAttributes (built by ui/goth/htmx). The
// zero value renders a bare toolbar with a default-named search field.
type TableToolbarProps struct {
	primitives.Base
	// Action is the form GET target the toolbar submits to. The zero URL submits
	// to the current page.
	Action primitives.URL
	// SearchID is the search control's id; it also associates the visually-hidden
	// label. Empty uses "table-toolbar-search".
	SearchID string
	// SearchName is the query parameter name for the search input. Empty uses "q".
	SearchName string
	// SearchValue is the current (server-echoed) filter value.
	SearchValue string
	// SearchLabel is the search field's accessible name. Empty uses "Search".
	SearchLabel string
	// SearchPlaceholder is the input placeholder. Empty uses the accessible name.
	SearchPlaceholder string
	// SubmitLabel is the no-JS submit button's text. Empty uses "Search".
	SubmitLabel string
	// SearchAttributes are extra attributes merged onto the search input (e.g.
	// explicit hx-* for a debounced live filter).
	SearchAttributes templ.Attributes
	// Filters is an optional slot for additional filter controls placed after the
	// search field. nil omits it.
	Filters templ.Component
	// Actions is an optional trailing action cluster (e.g. a create button). nil
	// omits it.
	Actions templ.Component
}

func (p TableToolbarProps) searchID() string {
	if p.SearchID != "" {
		return p.SearchID
	}
	return "table-toolbar-search"
}

func (p TableToolbarProps) searchName() string {
	if p.SearchName != "" {
		return p.SearchName
	}
	return "q"
}

func (p TableToolbarProps) searchLabel() string {
	if p.SearchLabel != "" {
		return p.SearchLabel
	}
	return "Search"
}

func (p TableToolbarProps) placeholder() string {
	if p.SearchPlaceholder != "" {
		return p.SearchPlaceholder
	}
	return p.searchLabel()
}

func (p TableToolbarProps) submitLabel() string {
	if p.SubmitLabel != "" {
		return p.SubmitLabel
	}
	return "Search"
}

// searchAttrs merges the accessible-name aria-label with the caller's
// SearchAttributes so a labelled search field is guaranteed while the caller can
// still add explicit hx-* for enhancement.
func (p TableToolbarProps) searchAttrs() templ.Attributes {
	out := templ.Attributes{}
	for k, v := range p.SearchAttributes {
		out[k] = v
	}
	out["aria-label"] = p.searchLabel()
	return out
}

// formAttrs returns the form GET attributes as ONE merged spread.
func (p TableToolbarProps) formAttrs() templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{
		"method":    "get",
		"action":    p.Action.String(),
		"role":      "search",
		"data-slot": "table-toolbar-form",
	})
}
