package primitives

import "github.com/a-h/templ"

// Orientation is the shared horizontal/vertical axis enum used by Separator (P21)
// and ButtonGroup (P07). The zero value is horizontal.
type Orientation string

const (
	// OrientationHorizontal is the zero value and the documented default.
	OrientationHorizontal Orientation = ""
	OrientationVertical   Orientation = "vertical"
)

// Valid reports whether o is a known Orientation. An unknown value renders the
// default horizontal orientation (primitives never panic); Valid lets callers
// fail fast.
func (o Orientation) Valid() bool {
	switch o {
	case OrientationHorizontal, OrientationVertical:
		return true
	default:
		return false
	}
}

func (o Orientation) orDefault() Orientation {
	if o.Valid() {
		return o
	}
	return OrientationHorizontal
}

func (o Orientation) attr() string {
	if o.orDefault() == OrientationHorizontal {
		return "horizontal"
	}
	return "vertical"
}

// SeparatorProps configures Separator (P21, family F1): a thin divider in one of
// two orientations. By default a separator is decorative (presentational, hidden
// from assistive technology). Set Semantic to expose it as an ARIA separator
// (role="separator") that conveys a thematic break to assistive technology. The
// zero value is a valid decorative horizontal separator.
type SeparatorProps struct {
	Base
	Orientation Orientation
	// Semantic, when true, renders role="separator" with aria-orientation so the
	// divider is announced as a meaningful thematic break. When false (the zero
	// value) the separator is decorative: role="none" + aria-hidden.
	Semantic bool
}

func separatorClass(p SeparatorProps) string { return classNames("goth-separator", p.Class) }

func separatorAttrs(p SeparatorProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "separator",
		"data-orientation": p.Orientation.attr(),
	}
	if p.Semantic {
		owned["role"] = "separator"
		owned["aria-orientation"] = p.Orientation.attr()
	} else {
		owned["role"] = "none"
		owned["aria-hidden"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
