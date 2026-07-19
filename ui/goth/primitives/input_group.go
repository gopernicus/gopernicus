package primitives

import "github.com/a-h/templ"

// InputGroupAlign positions an InputGroupAddon at the start (leading, zero value)
// or end (trailing) edge of the group.
type InputGroupAlign string

const (
	// InputGroupAlignStart is the zero value: a leading addon.
	InputGroupAlignStart InputGroupAlign = ""
	InputGroupAlignEnd   InputGroupAlign = "end"
)

// Valid reports whether a is a known InputGroupAlign.
func (a InputGroupAlign) Valid() bool {
	switch a {
	case InputGroupAlignStart, InputGroupAlignEnd:
		return true
	default:
		return false
	}
}

func (a InputGroupAlign) attr() string {
	if a == InputGroupAlignEnd {
		return "end"
	}
	return "start"
}

// InputGroup (P13, family F3) joins a text/select control with leading/trailing
// addons (text, icons, or buttons) into one visually unified control. The caller
// composes the parts: InputGroup wraps an Input (P12) plus InputGroupAddon
// segments (each holding InputGroupText or InputGroupButton). The control submits
// natively with no JavaScript. Accessible labelling stays the caller's job: the
// inner control is a real Input/Textarea with its own name/id and an associated
// Label, and an icon-only InputGroupButton REQUIRES a Label (aria-label).
//
// data-slot hooks: input-group, input-group-addon, input-group-text,
// input-group-button.
type InputGroupProps struct {
	Base
	// Invalid marks the group invalid for styling (data-invalid); the inner
	// control still owns aria-invalid.
	Invalid bool
}

// InputGroupAddonProps configures InputGroupAddon, an edge segment aligned start
// or end.
type InputGroupAddonProps struct {
	Base
	Align InputGroupAlign
}

// InputGroupTextProps configures InputGroupText, a non-interactive text/icon
// addon.
type InputGroupTextProps struct{ Base }

// InputGroupButtonProps configures InputGroupButton, a compact button addon. For
// an icon-only button Label is REQUIRED and emitted as aria-label; a text button
// takes its accessible name from the visible children.
type InputGroupButtonProps struct {
	Base
	// Type is the native button type; the zero value is "button" (never an
	// implicit submit).
	Type ButtonType
	// Label is the accessible name for an icon-only button (aria-label). Optional
	// for a text button.
	Label    string
	Disabled bool
}

func inputGroupClass(p InputGroupProps) string { return classNames("goth-input-group", p.Class) }

func inputGroupAttrs(p InputGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "input-group",
		"role":      "group",
	}
	if p.Invalid {
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func inputGroupAddonAttrs(p InputGroupAddonProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "input-group-addon",
		"data-align": p.Align.attr(),
	})
}

func inputGroupTextClass(base Base) string { return classNames("goth-input-group-text", base.Class) }

func inputGroupTextAttrs(base Base) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": "input-group-text"})
}

func inputGroupButtonClass(base Base) string {
	return classNames("goth-input-group-button", base.Class)
}

func inputGroupButtonAttrs(p InputGroupButtonProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "input-group-button",
		"type":      p.Type.attr(),
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return ownedAttrs(p.Base, owned)
}
