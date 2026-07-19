package primitives

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// DirectionProps configures Direction (P09, family F1): a wrapper that propagates
// a text direction to its subtree via the native dir attribute. The principal
// content is passed as templ children. It is the server-rendered analogue of a
// direction provider — a page sets the document direction on <html>, and this
// primitive scopes a different direction to one region (e.g. an RTL quotation
// inside an LTR page). The zero value propagates LTR.
type DirectionProps struct {
	Base
	// Dir is the text direction applied to the subtree. The empty Direction is
	// treated as LTR (theme.DirectionLTR).
	Dir theme.Direction
}

// dirAttr resolves the propagated direction, defaulting the empty/unknown value
// to LTR to match theme.HTMLAttributes.
func (p DirectionProps) dirAttr() string {
	if p.Dir == theme.DirectionRTL {
		return string(theme.DirectionRTL)
	}
	return string(theme.DirectionLTR)
}

func directionClass(p DirectionProps) string { return classNames("goth-direction", p.Class) }

func directionAttrs(p DirectionProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "direction",
		"dir":       p.dirAttr(),
	})
}
