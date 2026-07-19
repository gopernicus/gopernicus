package primitives

import "github.com/a-h/templ"

// ButtonGroup (P07, family F3) is a compound the caller composes: ButtonGroup
// wraps a row/column of related actions or inputs so they render as one joined
// control with shared borders and focus rings; ButtonGroupText holds a
// non-interactive addon/label segment inside the group. The zero value of each
// Props is valid. data-slot hooks: button-group, button-group-text.
type ButtonGroupProps struct {
	Base
	// Orientation lays the group out horizontally (zero value) or vertically.
	Orientation Orientation
}

// ButtonGroupTextProps configures ButtonGroupText, a non-interactive text/addon
// segment (e.g. a prefix label) joined into the group.
type ButtonGroupTextProps struct{ Base }

func buttonGroupClass(p ButtonGroupProps) string {
	return classNames("goth-button-group", p.Class)
}

func buttonGroupAttrs(p ButtonGroupProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":        "button-group",
		"data-orientation": p.Orientation.attr(),
		"role":             "group",
	})
}

func buttonGroupTextClass(p ButtonGroupTextProps) string {
	return classNames("goth-button-group-text", p.Class)
}

func buttonGroupTextAttrs(p ButtonGroupTextProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "button-group-text"})
}
