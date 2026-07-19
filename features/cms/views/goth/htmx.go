package goth

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/htmx"
)

// entriesContentID is the id of the entries-list swappable content region — the
// stable HTMX target the toolbar's filter/sort/pagination controls swap. The full
// EntriesList document and the EntriesListContent fragment both carry it, so an
// HTMX swap and a no-JS full reload render the same region.
const entriesContentID = "cms-entries-content"

// entriesContentTarget is the hx-target selector for the content region.
const entriesContentTarget = "#" + entriesContentID

// swapLinkAttrs builds the explicit hx-* attributes for an entries-list navigation
// link (a sort toggle or a pagination link): an hx-get that swaps the content
// region's outerHTML without jumping the viewport or stealing focus, pushing the
// URL so history/back re-fetches correctly. The link's own href is the no-JS
// fallback, so an empty result (a construction error) degrades to full navigation.
func swapLinkAttrs(url string) templ.Attributes {
	a, _ := htmx.Attrs{
		Method:   htmx.MethodGet,
		URL:      url,
		Target:   entriesContentTarget,
		Swap:     htmx.SwapOuterHTML,
		SwapMods: htmx.SwapModifiers{Show: "none", FocusScroll: boolPtr(false)},
		PushURL:  true,
	}.Build()
	return a
}

// filterFormAttrs builds the explicit hx-* attributes for the entries-list filter
// form: an hx-get that swaps the content region on a `change` (so selecting a
// status filters live), preserving the caret-free select focus and pushing the URL.
// The form itself is a real GET form, so with no JavaScript the submit button
// performs the same query. HTMX serializes the form's fields (the status select and
// the hidden order input), so no hx-vals/hx-include is needed.
func filterFormAttrs(url string) templ.Attributes {
	a, _ := htmx.Attrs{
		Method:   htmx.MethodGet,
		URL:      url,
		Target:   entriesContentTarget,
		Swap:     htmx.SwapOuterHTML,
		SwapMods: htmx.SwapModifiers{Show: "none", FocusScroll: boolPtr(false)},
		Trigger:  htmx.Trigger{Event: "change"},
		PushURL:  true,
	}.Build()
	return a
}

// boolPtr returns a pointer to b for the htmx.SwapModifiers.FocusScroll field.
func boolPtr(b bool) *bool { return &b }
