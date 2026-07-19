package primitives

import (
	"strings"
	"testing"
)

// GOTH-5.2 command/combobox primitives (P50 Combobox, P51 Command). These tests
// prove the server-owned no-JS baseline (submit-button / link options, a
// server-open listbox), the frozen gothCombobox controller binding, the
// listbox/option ARIA, and the attribute-merge ownership.

// TestNoPaletteePrimitiveEmitsInlineStyle proves invariant (a): no GOTH-5.2
// primitive emits an inline style= in any state (positioning is CSS + data-* only).
func TestNoPalettePrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Combobox(ComboboxProps{Open: true, Filter: ComboboxFilterServer}), "x"),
		render(t, ComboboxInput(ComboboxInputProps{Name: "q", Listbox: "lb", Invalid: true})),
		renderKids(t, ComboboxListbox(ComboboxListboxProps{Open: true, Label: "Fruits"}), "x"),
		renderKids(t, ComboboxOption(ComboboxOptionProps{Name: "fruit", Value: "apple", Selected: true}), "Apple"),
		renderKids(t, ComboboxEmpty(ComboboxEmptyProps{Open: true}), "No results"),
		renderKids(t, Command(CommandProps{Filter: ComboboxFilterServer}), "x"),
		render(t, CommandInput(CommandInputProps{Name: "q", Listbox: "cl"})),
		renderKids(t, CommandList(CommandListProps{Label: "Commands"}), "x"),
		renderKids(t, CommandGroup(CommandGroupProps{Heading: "Actions", HeadingID: "g1"}), "x"),
		renderKids(t, CommandItem(CommandItemProps{Name: "cmd", Value: "new"}), "New file"),
		renderKids(t, CommandItem(CommandItemProps{URL: mustParseURL(t, "/settings")}), "Settings"),
		renderKids(t, CommandEmpty(CommandEmptyProps{}), "Nothing"),
		render(t, CommandSeparator(CommandSeparatorProps{})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("palette primitive emitted an inline style=: %s", o)
		}
	}
}

// TestComboboxBaselineAndWiring proves the Combobox root controller binding + the
// server-owned filter/open state, the input's combobox ARIA + event wiring, and the
// listbox/option ARIA with the submit-button no-JS form value.
func TestComboboxBaselineAndWiring(t *testing.T) {
	root := renderKids(t, Combobox(ComboboxProps{Base: Base{ID: "cb"}, Open: true}), "x")
	mustContain(t, root,
		`class="goth-combobox"`, `data-slot="combobox"`, `id="cb"`,
		`x-data="gothCombobox"`, `data-state="open"`, `data-filter="client"`)

	closedServer := renderKids(t, Combobox(ComboboxProps{Filter: ComboboxFilterServer}), "x")
	mustContain(t, closedServer, `data-state="closed"`, `data-filter="server"`)

	input := render(t, ComboboxInput(ComboboxInputProps{
		Base: Base{ID: "cb-input"}, Name: "q", Value: "ap", Placeholder: "Search…",
		Listbox: "cb-lb", Required: true, Invalid: true,
	}))
	mustContain(t, input,
		`<input`, `class="goth-combobox-input"`, `data-slot="input"`, `type="text"`,
		`role="combobox"`, `aria-autocomplete="list"`, `aria-expanded="false"`,
		`aria-controls="cb-lb"`, `name="q"`, `value="ap"`, `placeholder="Search…"`,
		`required`, `aria-invalid="true"`, `data-invalid="true"`,
		`x-on:input="onInput($event)"`, `x-on:focus="onFocus($event)"`, `x-on:keydown="onKeydown($event)"`)

	lb := renderKids(t, ComboboxListbox(ComboboxListboxProps{Base: Base{ID: "cb-lb"}, Open: true, Label: "Fruits"}), "opts")
	mustContain(t, lb, `data-slot="listbox"`, `role="listbox"`, `data-state="open"`, `id="cb-lb"`, `aria-label="Fruits"`)

	opt := renderKids(t, ComboboxOption(ComboboxOptionProps{Name: "fruit", Value: "apple", Selected: true}), "Apple")
	mustContain(t, opt,
		`<button`, `data-slot="option"`, `role="option"`, `type="submit"`,
		`name="fruit"`, `value="apple"`, `data-value="apple"`,
		`aria-selected="true"`, `data-selected="true"`, `x-on:click="select($event)"`, "Apple")

	disabled := renderKids(t, ComboboxOption(ComboboxOptionProps{Value: "x", Disabled: true}), "X")
	mustContain(t, disabled, `disabled`, `aria-disabled="true"`, `aria-selected="false"`)

	// The no-results message is an aria-disabled option (an allowed listbox child),
	// hidden until Open.
	empty := renderKids(t, ComboboxEmpty(ComboboxEmptyProps{}), "No results")
	mustContain(t, empty, `data-slot="empty"`, `role="option"`, `aria-disabled="true"`, `hidden`, "No results")
	openEmpty := renderKids(t, ComboboxEmpty(ComboboxEmptyProps{Open: true}), "No results")
	mustNotContain(t, openEmpty, `hidden`)
}

// TestComboboxFilterEnum proves the ComboboxFilter enum's zero-value default and
// Valid membership.
func TestComboboxFilterEnum(t *testing.T) {
	if ComboboxFilter("").attr() != "client" {
		t.Error("zero-value ComboboxFilter should default to client")
	}
	if ComboboxFilterServer.attr() != "server" {
		t.Error("ComboboxFilterServer should render server")
	}
	if !ComboboxFilterClient.Valid() || !ComboboxFilterServer.Valid() || ComboboxFilter("nope").Valid() {
		t.Error("ComboboxFilter.Valid mismatch")
	}
}

// TestCommandBaselineAndWiring proves Command reuses gothCombobox in inline mode,
// the always-visible listbox, grouped ARIA, and the dual link/button item forms.
func TestCommandBaselineAndWiring(t *testing.T) {
	root := renderKids(t, Command(CommandProps{Base: Base{ID: "cmd"}}), "x")
	mustContain(t, root,
		`class="goth-command"`, `data-slot="command"`, `id="cmd"`,
		`x-data="gothCombobox"`, `data-inline`, `data-filter="client"`)

	input := render(t, CommandInput(CommandInputProps{Name: "q", Listbox: "cmd-list", Placeholder: "Type a command"}))
	mustContain(t, input,
		`data-slot="input"`, `role="combobox"`, `aria-expanded="true"`,
		`aria-autocomplete="list"`, `aria-controls="cmd-list"`, `name="q"`,
		`x-on:input="onInput($event)"`, `x-on:keydown="onKeydown($event)"`)

	list := renderKids(t, CommandList(CommandListProps{Base: Base{ID: "cmd-list"}, Label: "Commands"}), "x")
	mustContain(t, list, `data-slot="listbox"`, `role="listbox"`, `data-state="open"`, `id="cmd-list"`, `aria-label="Commands"`)

	group := renderKids(t, CommandGroup(CommandGroupProps{Heading: "Actions", HeadingID: "grp-1"}), `<button data-slot="option">New</button>`)
	mustContain(t, group,
		`data-slot="group"`, `role="group"`, `aria-labelledby="grp-1"`,
		`data-slot="group-heading"`, `id="grp-1"`, `aria-hidden="true"`, "Actions")

	btn := renderKids(t, CommandItem(CommandItemProps{Name: "cmd", Value: "new"}), "New file")
	mustContain(t, btn,
		`<button`, `data-slot="option"`, `role="option"`, `type="submit"`,
		`name="cmd"`, `value="new"`, `data-value="new"`, `x-on:click="select($event)"`, "New file")

	link := renderKids(t, CommandItem(CommandItemProps{URL: mustParseURL(t, "/settings"), Value: "settings"}), "Settings")
	mustContain(t, link,
		`<a`, `data-slot="option"`, `role="option"`, `href="/settings"`,
		`data-value="settings"`, "Settings")
	mustNotContain(t, link, `type="submit"`)

	empty := renderKids(t, CommandEmpty(CommandEmptyProps{}), "No commands")
	mustContain(t, empty, `data-slot="empty"`, `role="option"`, `aria-disabled="true"`, `hidden`, "No commands")

	sep := render(t, CommandSeparator(CommandSeparatorProps{}))
	mustContain(t, sep, `data-slot="separator"`, `aria-hidden="true"`)
	mustNotContain(t, sep, `role="separator"`)
}

// TestPaletteMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch, while a benign caller data-* / hx-* attribute merges
// (the async option seam uses Base.Attributes raw hx-* per the frozen merge rule).
func TestPaletteMergeHonorsOwnership(t *testing.T) {
	root := renderKids(t, Combobox(ComboboxProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"x-data":    "evil",
			"data-slot": "hijack",
			"class":     "dropped",
		},
	}}), "x")
	mustContain(t, root, `x-data="gothCombobox"`, `data-slot="combobox"`, `goth-combobox custom-x`)
	mustNotContain(t, root, `evil`, `hijack`, `dropped`)

	// hx-* on an option's Base.Attributes flows through the merge (the provisional
	// async seam: raw hx-* per README §9 until the GOTH-5.3/7.3 Attrs finalization).
	opt := renderKids(t, ComboboxOption(ComboboxOptionProps{
		Value: "apple",
		Base: Base{Attributes: map[string]any{
			"hx-get":    "/search?pick=apple",
			"hx-target": "#cb-fragment",
		}},
	}), "Apple")
	mustContain(t, opt, `hx-get="/search?pick=apple"`, `hx-target="#cb-fragment"`, `data-value="apple"`)
}
