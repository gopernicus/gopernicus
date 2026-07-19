package primitives

import "github.com/a-h/templ"

// Command (P51, family F4). A command palette: a search input over a grouped,
// filterable list of actions. It shares the gothCombobox controller in INLINE mode
// (data-inline) — the listbox is always visible rather than a popup — so filtering,
// the active-item keyboard loop (aria-activedescendant), and activation are the
// same mechanics as Combobox with no new controller name (README §8).
//
// Server ownership. The server owns the item DATA and, in server-filter mode, the
// FILTERING and empty-state markup: typing re-fetches a filtered grouped list
// (async replacement). In client-filter mode the controller hides non-matching
// items and toggles CommandEmpty.
//
// No-JS baseline. Items are native controls: a CommandItem with a URL is a real
// <a href> (link-first, the Navigation Menu precedent) and otherwise a <button
// type="submit"> carrying name/value. With no JavaScript the list is fully visible
// (inline), typing + submit round-trips the query, and activating an item
// navigates or submits. Enhanced, gothCombobox moves the active item with the
// arrow keys and Enter activates it. data-slot hooks: command, input, listbox,
// group, group-heading, option, empty, separator.

// CommandProps configures the Command root.
type CommandProps struct {
	Base
	// Filter selects client (default) or server/async filtering.
	Filter ComboboxFilter
}

// CommandInputProps configures the palette search field.
type CommandInputProps struct {
	Base
	// Name is the submitted query field (the no-JS round-trip carries it).
	Name string
	// Value is the current input text.
	Value string
	// Placeholder is the empty-field hint.
	Placeholder string
	// Listbox is the CommandList id this input controls (aria-controls).
	Listbox string
}

// CommandListProps configures the always-visible role=listbox.
type CommandListProps struct {
	Base
	// Label is the list accessible name (aria-label).
	Label string
}

// CommandGroupProps configures a role=group of related items with a heading.
type CommandGroupProps struct {
	Base
	// Heading is the visible group label.
	Heading string
	// HeadingID is the heading element id the group is labelled by
	// (aria-labelledby). Required when Heading is set.
	HeadingID string
}

// CommandItemProps configures one role=option command. A set URL renders an <a>
// (link-first); otherwise it renders a submit <button> carrying name/value.
type CommandItemProps struct {
	Base
	// URL, when set, renders the item as a link. Zero value renders a submit button.
	URL URL
	// Name/Value are the submitted field for the button form; ignored for a link.
	Name  string
	Value string
	// Selected marks the committed item (aria-selected + data-selected).
	Selected bool
	// Disabled marks the item non-actionable.
	Disabled bool
}

// CommandEmptyProps configures the no-results message.
type CommandEmptyProps struct {
	Base
	// Open renders the empty state visible; the zero value is hidden (items present).
	Open bool
}

// CommandSeparatorProps configures a role=separator divider.
type CommandSeparatorProps struct{ Base }

func commandAttrs(p CommandProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "command",
		"data-inline": true,
		"data-filter": p.Filter.attr(),
		"x-data":      "gothCombobox",
	})
}

func commandInputAttrs(p CommandInputProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":         "input",
		"type":              "text",
		"role":              "combobox",
		"aria-autocomplete": "list",
		"aria-expanded":     "true",
		"autocomplete":      "off",
		"x-on:input":        "onInput($event)",
		"x-on:focus":        "onFocus($event)",
		"x-on:keydown":      "onKeydown($event)",
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
	}
	if p.Placeholder != "" {
		owned["placeholder"] = p.Placeholder
	}
	if p.Listbox != "" {
		owned["aria-controls"] = p.Listbox
	}
	return ownedAttrs(p.Base, owned)
}

func commandListAttrs(p CommandListProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "listbox",
		"role":       "listbox",
		"data-state": "open",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func commandGroupAttrs(p CommandGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "group",
		"role":      "group",
	}
	if p.HeadingID != "" {
		owned["aria-labelledby"] = p.HeadingID
	}
	return ownedAttrs(p.Base, owned)
}

func commandItemButtonAttrs(p CommandItemProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":     "option",
		"role":          "option",
		"type":          "submit",
		"aria-selected": boolString(p.Selected),
		"x-on:click":    "select($event)",
	}
	if p.Value != "" {
		owned["data-value"] = p.Value
		owned["value"] = p.Value
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Selected {
		owned["data-selected"] = "true"
	}
	if p.Disabled {
		owned["disabled"] = true
		owned["aria-disabled"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func commandItemLinkAttrs(p CommandItemProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":     "option",
		"role":          "option",
		"aria-selected": boolString(p.Selected),
		"x-on:click":    "select($event)",
	}
	if p.Value != "" {
		owned["data-value"] = p.Value
	}
	if p.Selected {
		owned["data-selected"] = "true"
	}
	if p.Disabled {
		owned["aria-disabled"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func commandEmptyAttrs(p CommandEmptyProps) templ.Attributes {
	// role="option" (an allowed listbox child) + aria-disabled rather than
	// role="status", which a listbox may not contain. data-slot="empty" keeps the
	// controller from treating it as a selectable option.
	owned := templ.Attributes{
		"data-slot":     "empty",
		"role":          "option",
		"aria-disabled": "true",
	}
	if !p.Open {
		owned["hidden"] = true
	}
	return ownedAttrs(p.Base, owned)
}

func commandSeparatorAttrs(p CommandSeparatorProps) templ.Attributes {
	// A decorative divider between groups. It is aria-hidden (no role="separator")
	// because a listbox may only contain option/group children — an exposed
	// separator would break the list's required-children contract.
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "separator",
		"aria-hidden": "true",
	})
}
