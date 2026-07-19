package primitives

import "github.com/a-h/templ"

// EmptyProps configures Empty (P10, family F2): a centered empty-state block with
// an optional media slot, title, description, and action, arranged around a
// principal content region passed as templ children. The zero value is a valid
// (blank) empty state. Media and Action are auxiliary templ.Component slots;
// Title and Description are inline text roles.
type EmptyProps struct {
	Base
	// Media is an optional leading illustration/icon slot.
	Media templ.Component
	// Title is the short empty-state heading.
	Title string
	// Description is the supporting explanation.
	Description string
	// Action is an optional trailing action slot (e.g. a button).
	Action templ.Component
}

func emptyClass(p EmptyProps) string { return classNames("goth-empty", p.Class) }

func emptyAttrs(p EmptyProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "empty"})
}
