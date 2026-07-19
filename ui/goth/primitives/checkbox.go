package primitives

import "github.com/a-h/templ"

// Checkbox (P28, family F1) renders a native <input type="checkbox">. It submits
// in an ordinary HTML form with no JavaScript (the native input is always the
// submitted source of truth) and carries the native keyboard/focus behavior. The
// styled box is drawn by component CSS off the appearance-none input plus its
// :checked pseudo-class and the data-state hook, so no controller is needed. The
// zero value is a valid unchecked checkbox. data-slot="checkbox"; data-state is
// checked / unchecked / indeterminate.
//
// Indeterminate is a visual-only third state: HTML has no indeterminate attribute
// (it is a JS-only property), so it is expressed as data-state="indeterminate" and
// drawn by CSS. It does not change the submitted value, which is governed by the
// native checked attribute; a user click resolves the box to checked. No
// aria-checked="mixed" is emitted: on a native checkbox that would conflict with
// the element's own checked state (and is unreachable no-JS). Assistive-tech
// "mixed" state requires the native .indeterminate DOM property, which a host may
// set as an enhancement; the no-JS baseline conveys indeterminate visually.
type CheckboxProps struct {
	Base
	// Name is the submitted field name.
	Name string
	// Value is the value submitted when checked. Empty leaves the native default
	// ("on").
	Value string
	// Checked renders the native checked attribute (submitted and visually ticked).
	Checked bool
	// Indeterminate renders the visual indeterminate state (data-state only). It is
	// ignored for submission and is superseded by a user click.
	Indeterminate bool
	Required      bool
	Disabled      bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

// checkboxState maps the props to the frozen data-state value. Indeterminate takes
// visual precedence over checked.
func checkboxState(p CheckboxProps) string {
	switch {
	case p.Indeterminate:
		return "indeterminate"
	case p.Checked:
		return "checked"
	default:
		return "unchecked"
	}
}

func checkboxClass(p CheckboxProps) string { return classNames("goth-checkbox", p.Class) }

func checkboxAttrs(p CheckboxProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "checkbox",
		"type":       "checkbox",
		"data-state": checkboxState(p),
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
