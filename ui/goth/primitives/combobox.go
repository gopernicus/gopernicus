package primitives

import "github.com/a-h/templ"

// Combobox (P50, family F4). A text input paired with a listbox of options the
// SERVER owns. Compound parts the caller composes: Combobox (root, x-data=
// gothCombobox), ComboboxInput (the editable role=combobox field), ComboboxListbox
// (the role=listbox popup), ComboboxOption (a role=option submit control), and
// ComboboxEmpty (the no-results message).
//
// Server ownership. The server owns the option DATA and, in server-filter mode,
// the FILTERING and empty-state markup: typing re-fetches a fresh option list
// (async option replacement). In the default client-filter mode the controller
// hides non-matching already-rendered options and toggles ComboboxEmpty; either
// way the server owns the authoritative value.
//
// No-JS baseline. Each ComboboxOption is a native <button type="submit"> carrying
// name/value, so with the listbox rendered server-open (ComboboxProps.Open) a user
// picks an option and the enclosing form submits the value with no JavaScript —
// the same round-trip an HTMX-enhanced option performs via hx-* on Base.Attributes.
// The input carries its own Name so a free-text query also round-trips (the
// "native input + server round-trip" baseline).
//
// Enhancement. gothCombobox opens the listbox on focus/typing, moves an active
// option with the arrow keys via aria-activedescendant (focus stays on the input),
// activates the active option on Enter, and dismisses on Escape/outside press
// through the shared overlay mechanics — dispatching goth:open/goth:close/
// goth:select. data-slot hooks: combobox, input, listbox, option, empty.

// ComboboxFilter selects who owns filtering. The zero value is client filtering.
type ComboboxFilter string

const (
	// ComboboxFilterClient hides non-matching already-rendered options in the
	// browser (the zero value).
	ComboboxFilterClient ComboboxFilter = "client"
	// ComboboxFilterServer leaves filtering and the empty state to the server
	// (async option replacement); the controller only opens the listbox and moves
	// the active option.
	ComboboxFilterServer ComboboxFilter = "server"
)

// Valid reports whether f is a known filter mode.
func (f ComboboxFilter) Valid() bool {
	return f == ComboboxFilterClient || f == ComboboxFilterServer
}

func (f ComboboxFilter) attr() string {
	if f == ComboboxFilterServer {
		return "server"
	}
	return "client"
}

// ComboboxProps configures the Combobox root.
type ComboboxProps struct {
	Base
	// Filter selects client (default) or server/async filtering.
	Filter ComboboxFilter
	// Open renders the listbox server-open (the readable no-JS baseline). Zero
	// value is closed (only the input shows without JavaScript).
	Open bool
}

// ComboboxInputProps configures the editable combobox field.
type ComboboxInputProps struct {
	Base
	// Name is the submitted free-text query field (the no-JS round-trip carries it).
	Name string
	// Value is the current input text.
	Value string
	// Placeholder is the empty-field hint.
	Placeholder string
	// Listbox is the ComboboxListbox id this input controls (aria-controls).
	Listbox string
	// Required marks the field required.
	Required bool
	// Disabled disables the field.
	Disabled bool
	// Invalid marks the field invalid (aria-invalid + data-invalid).
	Invalid bool
}

// ComboboxListboxProps configures the role=listbox popup.
type ComboboxListboxProps struct {
	Base
	// Open must match ComboboxProps.Open so the popup's data-state agrees with the
	// root for the server-owned no-JS baseline.
	Open bool
	// Label is the listbox accessible name (aria-label).
	Label string
}

// ComboboxOptionProps configures one role=option submit control.
type ComboboxOptionProps struct {
	Base
	// Name is the submitted value field name; Value is the submitted value.
	Name  string
	Value string
	// Selected marks the committed option (aria-selected + data-selected).
	Selected bool
	// Disabled marks the option non-actionable.
	Disabled bool
}

// ComboboxEmptyProps configures the no-results message.
type ComboboxEmptyProps struct {
	Base
	// Open renders the empty state visible (server-owned no-results state).
	Open bool
}

func comboboxAttrs(p ComboboxProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "combobox",
		"data-state":  openState(p.Open),
		"data-filter": p.Filter.attr(),
		"x-data":      "gothCombobox",
	})
}

func comboboxInputAttrs(p ComboboxInputProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":         "input",
		"type":              "text",
		"role":              "combobox",
		"aria-autocomplete": "list",
		"aria-expanded":     "false",
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
	if p.Required {
		owned["required"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func comboboxListboxAttrs(p ComboboxListboxProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "listbox",
		"role":       "listbox",
		"data-state": openState(p.Open),
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func comboboxOptionAttrs(p ComboboxOptionProps) templ.Attributes {
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

func comboboxEmptyAttrs(p ComboboxEmptyProps) templ.Attributes {
	// The no-results message lives inside the listbox, so it carries role="option"
	// (an allowed listbox child) + aria-disabled rather than role="status" (which a
	// listbox may not contain). It is data-slot="empty" so the controller never
	// treats it as a selectable option, and hidden until Open.
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
