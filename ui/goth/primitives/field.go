package primitives

import "github.com/a-h/templ"

// FieldOrientation is the typed layout enum for Field (P11). Unlike the shared
// Orientation (horizontal zero value), a field's natural default is vertical
// (label above control), so the zero value here is vertical.
type FieldOrientation string

const (
	// FieldVertical is the zero value: label above control.
	FieldVertical   FieldOrientation = ""
	FieldHorizontal FieldOrientation = "horizontal"
)

// Valid reports whether o is a known FieldOrientation.
func (o FieldOrientation) Valid() bool {
	switch o {
	case FieldVertical, FieldHorizontal:
		return true
	default:
		return false
	}
}

func (o FieldOrientation) attr() string {
	if o == FieldHorizontal {
		return "horizontal"
	}
	return "vertical"
}

// Field (P11, family F3) groups a form control with its label, description, and
// error message. The caller composes and places the parts — Field, FieldLabel,
// FieldDescription, FieldError — plus FieldGroup/FieldSet/FieldLegend for
// grouping. ARIA linkage is caller-passed via Base.ID per the frozen grammar:
// the caller sets the control's id, points FieldLabel.For at it, and wires the
// control's aria-describedby at the FieldDescription/FieldError ids (see the
// showcase specimen). Field itself owns no ambient id threading.
//
// data-slot hooks: field, field-label, field-description, field-error,
// field-group, field-set, field-legend. FieldProps.Invalid sets
// data-invalid="true" so the group styles its error affordance; the control
// still owns aria-invalid.
type FieldProps struct {
	Base
	// Orientation lays the label/control out vertically (zero value) or
	// horizontally (label beside control).
	Orientation FieldOrientation
	// Invalid marks the whole field invalid for styling (data-invalid); the
	// control owns the authoritative aria-invalid.
	Invalid bool
}

// FieldLabelProps configures FieldLabel, a native <label>. For is the id of the
// control it names (emitted as the native `for` attribute).
type FieldLabelProps struct {
	Base
	For string
}

// FieldDescriptionProps configures FieldDescription, the helper text a control
// references through aria-describedby (via the caller-set Base.ID).
type FieldDescriptionProps struct{ Base }

// FieldErrorProps configures FieldError, the validation message a control
// references through aria-describedby (via the caller-set Base.ID).
type FieldErrorProps struct{ Base }

// FieldGroupProps configures FieldGroup, a vertical stack of related fields.
type FieldGroupProps struct{ Base }

// FieldSetProps configures FieldSet (<fieldset>), grouping related controls
// (e.g. a set of radios) under a FieldLegend.
type FieldSetProps struct{ Base }

// FieldLegendProps configures FieldLegend (<legend>).
type FieldLegendProps struct{ Base }

func fieldClass(p FieldProps) string { return classNames("goth-field", p.Class) }

func fieldAttrs(p FieldProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "field",
		"data-orientation": p.Orientation.attr(),
	}
	if p.Invalid {
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func fieldLabelAttrs(p FieldLabelProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "field-label"}
	if p.For != "" {
		owned["for"] = p.For
	}
	return ownedAttrs(p.Base, owned)
}

func fieldPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func fieldPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}
