package primitives

import "github.com/a-h/templ"

// LabelProps configures Label (P16, family F1): a native <label> naming a form
// control. Its content is the label text passed as templ children. The zero
// value is a valid unassociated label; set For to the control's id for explicit
// native association. data-slot="label" is the stable emitted surface.
type LabelProps struct {
	Base
	// For is the id of the control this label names, emitted as the native `for`
	// attribute so a click focuses the control and assistive tech announces the
	// name. Empty leaves the label unassociated (wrap the control instead).
	For string
	// Required renders a decorative required indicator after the text; the control
	// still carries the authoritative `required`/`aria-required` state.
	Required bool
	// Disabled marks the label's control as disabled for styling only
	// (data-disabled); it does not disable anything by itself.
	Disabled bool
}

func labelClass(p LabelProps) string { return classNames("goth-label", p.Class) }

func labelAttrs(p LabelProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "label"}
	if p.For != "" {
		owned["for"] = p.For
	}
	if p.Disabled {
		owned["data-disabled"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
