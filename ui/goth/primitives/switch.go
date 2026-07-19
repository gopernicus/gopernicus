package primitives

import "github.com/a-h/templ"

// SwitchProps configures Switch (P33, family F1): a boolean form input rendered as
// a native <input type="checkbox"> carrying role="switch". It submits in an
// ordinary HTML form with no JavaScript (the native checkbox is the submitted
// source of truth) and toggles with the native Space key and pointer, so no
// controller is bound. The track/thumb are drawn by component CSS off the
// appearance-none input and its :checked pseudo-class. The zero value is a valid
// off switch. data-slot="switch"; data-state is checked / unchecked.
type SwitchProps struct {
	Base
	// Name is the submitted field name.
	Name string
	// Value is the value submitted when on. Empty leaves the native default ("on").
	Value string
	// Checked renders the switch on (native checked attribute, submitted).
	Checked  bool
	Required bool
	Disabled bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

func switchClass(p SwitchProps) string { return classNames("goth-switch", p.Class) }

func switchAttrs(p SwitchProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "switch",
		"type":       "checkbox",
		"role":       "switch",
		"data-state": onOffState(p.Checked),
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
	}
	if p.Checked {
		owned["checked"] = true
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

// onOffState maps a boolean on flag to the frozen data-state selection value,
// reusing the checked / unchecked vocabulary of the other form-selection controls.
func onOffState(on bool) string {
	if on {
		return "checked"
	}
	return "unchecked"
}
