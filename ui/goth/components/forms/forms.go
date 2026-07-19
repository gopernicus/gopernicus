// Package forms holds the opinionated, domain-neutral form compositions built
// from ui/goth primitives (GOTH-7.1): the repeatable label/control/description/
// error field group, a titled section of fields, a top-of-form validation
// summary, and the submit/cancel action row. Each composes primitives (Field,
// Alert, ButtonGroup) and reuses the frozen primitives.Base grammar; none imports
// a feature domain, adds a primitive, or emits a server-rendered style.
//
// ARIA linkage follows the frozen Field contract (README §7): the caller owns the
// control's id and points its aria-describedby at the description/error ids this
// package derives from FormFieldProps.For. DescriptionID and ErrorID expose those
// ids so an adopter can wire the control without guessing the convention.
package forms

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/components/internal/kit"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// FormFieldProps configures FormField, the repeatable group of a label, a control
// (templ children), an optional helper description, and an optional validation
// error. It composes primitives.Field/FieldLabel/FieldDescription/FieldError. The
// caller owns the control's id (For) and wires the control's aria-describedby at
// DescriptionID(For)/ErrorID(For); an Error also flags the field invalid for
// styling (the control still owns the authoritative aria-invalid). The zero value
// renders a bare vertical field.
type FormFieldProps struct {
	primitives.Base
	// Label is the field label text. Empty omits the label.
	Label string
	// For is the id of the control this field wraps; it associates the label
	// (native for) and seeds the description/error ids. Empty leaves them
	// unassociated.
	For string
	// Description is optional helper text under the control.
	Description string
	// Error is the validation message; a non-empty Error flags the field invalid
	// and renders the error region.
	Error string
	// Required renders a decorative required marker after the label (the control
	// owns the authoritative required/aria-required).
	Required bool
	// Orientation lays the label above (zero value) or beside the control.
	Orientation primitives.FieldOrientation
}

// DescriptionID returns the deterministic id of a FormField's description region
// for a given control id, so the caller can point the control's aria-describedby
// at it. It returns "" for an empty control id.
func DescriptionID(controlID string) string {
	if controlID == "" {
		return ""
	}
	return controlID + "-description"
}

// ErrorID returns the deterministic id of a FormField's error region for a given
// control id, so the caller can point the control's aria-describedby at it. It
// returns "" for an empty control id.
func ErrorID(controlID string) string {
	if controlID == "" {
		return ""
	}
	return controlID + "-error"
}

// FormSectionProps configures FormSection, a titled group of related fields: an
// optional Title (rendered as an h2) and Description, and the fields (templ
// children) stacked in a primitives.FieldGroup. The zero value renders a bare
// group. data-slot hooks: form-section, form-section-header, form-section-title,
// form-section-description.
type FormSectionProps struct {
	primitives.Base
	// Title is the section heading (h2). Empty omits the header.
	Title string
	// Description is the supporting line under the title. Empty omits it.
	Description string
}

// FieldMessage is one validation error in an ErrorSummary: a human Message and
// the FieldID it points at (an in-page anchor so a click focuses the offending
// control). FieldID may be empty for a form-level message with no target.
type FieldMessage struct {
	Message string
	FieldID string
}

// ErrorSummaryProps configures ErrorSummary, the top-of-form validation summary
// that composes a destructive primitives.Alert with a list of messages, each an
// in-page link to its field. It is a role="alert" region (the Alert supplies it).
// The zero value with no Errors renders nothing. data-slot hooks: error-summary,
// error-summary-list, error-summary-item.
type ErrorSummaryProps struct {
	primitives.Base
	// Title is the summary heading (e.g. "Please fix the following"). Empty uses a
	// sensible default.
	Title string
	// Errors is the list of messages; an empty list renders nothing.
	Errors []FieldMessage
}

func (p ErrorSummaryProps) title() string {
	if p.Title != "" {
		return p.Title
	}
	return "Please fix the following"
}

// FormActionsAlign controls how FormActions distributes its buttons. The zero
// value aligns them to the end (the common submit-on-the-right layout).
type FormActionsAlign string

const (
	// FormActionsEnd is the zero value: actions clustered at the end.
	FormActionsEnd FormActionsAlign = ""
	// FormActionsStart clusters the actions at the start.
	FormActionsStart FormActionsAlign = "start"
	// FormActionsBetween pushes the first and last actions apart (e.g. a
	// destructive control on the left, submit on the right).
	FormActionsBetween FormActionsAlign = "between"
)

// Valid reports whether a is a known FormActionsAlign.
func (a FormActionsAlign) Valid() bool {
	switch a {
	case FormActionsEnd, FormActionsStart, FormActionsBetween:
		return true
	default:
		return false
	}
}

func (a FormActionsAlign) attr() string {
	if a.Valid() && a != FormActionsEnd {
		return string(a)
	}
	return "end"
}

// FormActionsProps configures FormActions, the submit/cancel button row placed at
// the foot of a form. The buttons are templ children (e.g. primitives.Button
// values); Align distributes them. data-slot hook: form-actions with a
// data-align variant.
type FormActionsProps struct {
	primitives.Base
	Align FormActionsAlign
}

func formSectionAttrs(p FormSectionProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{"data-slot": "form-section"})
}

func errorSummaryListAttrs() templ.Attributes {
	return templ.Attributes{"data-slot": "error-summary-list"}
}

func formActionsAttrs(p FormActionsProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{
		"data-slot":  "form-actions",
		"data-align": p.Align.attr(),
	})
}
