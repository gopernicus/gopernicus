package primitives

import "github.com/a-h/templ"

// ScrollArea (P62, family F4 shipped native/no-controller) is a bounded scroll
// region built on native overflow. The kit does NOT replace the platform
// scrollbar with a synthetic JavaScript one: the baseline is a real overflow
// container that scrolls with wheel, touch, and — because it is a focusable
// region with an accessible name (tabindex=0 + aria-label, the P24/P60 keyboard
// precedent) — the arrow keys, Page Up/Down, Home, and End. The affordance
// enhancement (a slim themed scrollbar) is pure CSS (scrollbar-width/color and
// the WebKit scrollbar pseudo-elements) with no inline style and no controller.
//
// data-slot hook: scroll-area. data-orientation reflects the scroll axis.

// ScrollAreaOrientation is the typed scroll-axis enum. The zero value scrolls
// vertically; horizontal and both are the other axes.
type ScrollAreaOrientation string

const (
	// ScrollVertical is the zero value and the documented default.
	ScrollVertical   ScrollAreaOrientation = ""
	ScrollHorizontal ScrollAreaOrientation = "horizontal"
	ScrollBoth       ScrollAreaOrientation = "both"
)

// Valid reports whether o is a known ScrollAreaOrientation.
func (o ScrollAreaOrientation) Valid() bool {
	switch o {
	case ScrollVertical, ScrollHorizontal, ScrollBoth:
		return true
	default:
		return false
	}
}

func (o ScrollAreaOrientation) attr() string {
	switch o {
	case ScrollHorizontal:
		return "horizontal"
	case ScrollBoth:
		return "both"
	default:
		return "vertical"
	}
}

// ScrollAreaProps configures the ScrollArea (P62). The zero value is a valid
// vertical scroll region. The caller places the (potentially overflowing) content
// as children.
type ScrollAreaProps struct {
	Base
	// Orientation selects the scroll axis. Zero value is vertical.
	Orientation ScrollAreaOrientation
	// Label is the accessible name (aria-label) for the focusable scroll region.
	// Recommended: a keyboard user tabs to the region to scroll it, so it should be
	// nameable. When empty the region is still focusable/scrollable but unnamed.
	Label string
}

func scrollAreaClass(p ScrollAreaProps) string { return classNames("goth-scroll-area", p.Class) }

func scrollAreaAttrs(p ScrollAreaProps) templ.Attributes {
	// tabindex=0 makes the overflow region keyboard-operable (a scrollable region
	// must be reachable so a keyboard user can scroll it with the arrow keys) and
	// satisfies the scrollable-region-focusable rule; role=group + aria-label give
	// the region a name assistive technology can announce.
	owned := templ.Attributes{
		"data-slot":        "scroll-area",
		"data-orientation": p.Orientation.attr(),
		"role":             "group",
		"tabindex":         "0",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}
