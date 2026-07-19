package primitives

import "github.com/a-h/templ"

// DatePicker (P53, family F4) composes a text field, a native popover trigger, and
// a popover panel the caller fills with a Calendar (P49). The SERVER owns the
// parse/format/error contract: DatePickerInput carries the already-formatted value
// and an Invalid flag; the server parses the submitted value, and on a parse error
// re-renders with Invalid set and an error message linked by Describedby. The
// primitive holds no clock and no time-zone policy.
//
// The panel rides the native popover/popovertarget attributes (no controller):
// the trigger toggles it, Escape and an outside press dismiss it, and a Calendar
// day button can dismiss it natively via CalendarProps.DismissTarget. Selection has
// a no-JS path (the Calendar's form submits to the server, which re-renders the
// field) and an optional HTMX-enhanced path (CalendarProps.DayAttributes carry
// hx-get/hx-target to swap the field fragment). data-slot hooks: date-picker,
// date-picker-input, date-picker-trigger, date-picker-content.
type DatePickerProps struct{ Base }

// DatePickerInputProps configures the date text input. Value is the formatted date
// string the server produced; Format is a human hint rendered as the placeholder.
type DatePickerInputProps struct {
	Base
	// Name is the submitted field name (required for form submission).
	Name string
	// Value is the current formatted date value.
	Value string
	// Format is a placeholder hint such as "YYYY-MM-DD".
	Format string
	// Invalid marks a parse/validation error (aria-invalid).
	Invalid bool
	// Describedby links the input to a server-rendered error/description element.
	Describedby string
	// Required marks the field required.
	Required bool
}

// DatePickerTriggerProps configures the popover trigger button. Target is the
// DatePickerContent id (the native popovertarget). Label is the accessible name for
// the icon-only trigger (default "Choose date").
type DatePickerTriggerProps struct {
	Base
	Target string
	Label  string
}

// DatePickerContentProps configures the native popover panel. Set Base.ID to the id
// DatePickerTrigger.Target points at; the caller places a Calendar inside.
type DatePickerContentProps struct {
	Base
	// Side/Align give the preferred placement the runtime anchor enhancement uses.
	Side  PopoverSide
	Align PopoverAlign
}

func (p DatePickerTriggerProps) label() string {
	if p.Label == "" {
		return "Choose date"
	}
	return p.Label
}

func datePickerClass(p DatePickerProps) string { return classNames("goth-date-picker", p.Class) }

func datePickerAttrs(p DatePickerProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "date-picker"})
}

func datePickerInputClass(p DatePickerInputProps) string {
	return classNames("goth-date-picker-input", p.Class)
}

func datePickerInputAttrs(p DatePickerInputProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":    "date-picker-input",
		"type":         "text",
		"inputmode":    "numeric",
		"autocomplete": "off",
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
	}
	if p.Format != "" {
		owned["placeholder"] = p.Format
	}
	if p.Required {
		owned["required"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
	}
	if p.Describedby != "" {
		owned["aria-describedby"] = p.Describedby
	}
	return ownedAttrs(p.Base, owned)
}

func datePickerTriggerClass(p DatePickerTriggerProps) string {
	return classNames("goth-date-picker-trigger", p.Class)
}

func datePickerTriggerAttrs(p DatePickerTriggerProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":           "date-picker-trigger",
		"type":                "button",
		"aria-label":          p.label(),
		"popovertargetaction": "toggle",
	}
	if p.Target != "" {
		owned["popovertarget"] = p.Target
	}
	return ownedAttrs(p.Base, owned)
}

func datePickerContentClass(p DatePickerContentProps) string {
	return classNames("goth-date-picker-content", p.Class)
}

func datePickerContentAttrs(p DatePickerContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":            "date-picker-content",
		"popover":              "auto",
		"data-side-preferred":  p.Side.attr(),
		"data-align-preferred": p.Align.attr(),
	})
}
