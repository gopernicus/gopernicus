package primitives

import "github.com/a-h/templ"

// SelectProps configures the Select control (P46, family F4). Select ships a
// styled NATIVE <select> as its baseline (README §8 / the recorded F4-native
// precedent: a fully-sufficient native element satisfies the row with no
// controller). CSS hides only the native closed-state arrow (appearance:none) and
// draws a chevron; the native listbox, typeahead, keyboard navigation, form value,
// and constraint validation are all preserved and work with NO JavaScript. The
// caller composes SelectOption / SelectGroup children. Set Base.ID (it lands on
// the <select>) so an external <label for> can name the control. data-slot hooks:
// select, select-input, select-icon, select-option, select-group.
type SelectProps struct {
	Base
	// Name is the submitted field name.
	Name string
	// Required marks the control required for native constraint validation.
	Required bool
	// Disabled disables the control.
	Disabled bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
	// Placeholder, when set, renders a disabled, hidden, initially-selected empty
	// option so a required Select starts with no valid value chosen.
	Placeholder string
}

// SelectOptionProps configures one <option>; its label is templ children.
type SelectOptionProps struct {
	Base
	Value    string
	Selected bool
	Disabled bool
}

// SelectGroupProps configures an <optgroup>; its options are templ children.
type SelectGroupProps struct {
	Base
	// Label is the required group label.
	Label    string
	Disabled bool
}

func selectClass(p SelectProps) string { return classNames("goth-select", p.Class) }

func selectInputAttrs(p SelectProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "select-input"}
	if p.Name != "" {
		owned["name"] = p.Name
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

func selectOptionAttrs(p SelectOptionProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "select-option",
		"value":     p.Value,
	}
	if p.Selected {
		owned["selected"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return ownedAttrs(p.Base, owned)
}

func selectGroupAttrs(p SelectGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "select-group",
		"label":     p.Label,
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return ownedAttrs(p.Base, owned)
}
