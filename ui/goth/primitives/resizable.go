package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// Resizable (P61, family F4) is a split-pane layout with a draggable/keyboard
// separator. The caller composes three parts in order: a primary ResizablePane,
// a ResizableHandle, and a secondary ResizablePane.
//
// No-JS baseline. The server owns the default split: the root carries
// data-default-size (the primary pane's percentage) and external component CSS
// maps it to the primary pane's flex-basis through the --goth-resize-basis custom
// property, so the split renders at the server-chosen ratio with no JavaScript.
// The panes are role=group regions; the handle is a role=separator with
// aria-valuenow/min/max and aria-controls.
//
// Enhancement (gothResizable, §8 controller, ratified by the 2026-07-18
// gate-b-review.md addendum). The controller reads pointer-drag (with pointer
// capture) and APG window-splitter keyboard input (Arrow keys, Home, End; RTL-aware
// per the roving.js precedent), clamps to the min/max bounds, and writes the new
// geometry through the controller-owned CSSOM custom property
// --goth-resize-basis — NEVER a server-rendered style attribute (README §4 frozen
// invariant). It updates the handle's aria-valuenow and dispatches goth:change.
//
// data-slot hooks: resizable, resizable-pane, resizable-handle.

// ResizableOrientation is the typed pane-arrangement enum. The zero value arranges
// the panes horizontally (side by side, a vertical separator); vertical stacks them
// (a horizontal separator).
type ResizableOrientation string

const (
	// ResizableHorizontal is the zero value and the documented default.
	ResizableHorizontal ResizableOrientation = ""
	ResizableVertical   ResizableOrientation = "vertical"
)

// Valid reports whether o is a known ResizableOrientation.
func (o ResizableOrientation) Valid() bool {
	switch o {
	case ResizableHorizontal, ResizableVertical:
		return true
	default:
		return false
	}
}

func (o ResizableOrientation) attr() string {
	if o == ResizableVertical {
		return "vertical"
	}
	return "horizontal"
}

// separatorOrientation is the aria-orientation of the handle: a horizontal pane
// arrangement uses a vertical separator, and vice versa.
func (o ResizableOrientation) separatorOrientation() string {
	if o == ResizableVertical {
		return "horizontal"
	}
	return "vertical"
}

// clampSize clamps v into [min, max].
func clampSize(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// snap5 snaps v to the nearest multiple of 5 within [5, 95]. The server default
// split is expressed through discrete data-default-size buckets so external CSS
// (not a server-rendered inline style) can render the initial geometry.
func snap5(v int) int {
	v = ((v + 2) / 5) * 5
	return clampSize(v, 5, 95)
}

// ResizableProps configures the Resizable root (P61). The zero value is a valid
// horizontal 50/50 split with 10/90 bounds.
type ResizableProps struct {
	Base
	// Orientation selects the pane arrangement. Zero value is horizontal.
	Orientation ResizableOrientation
	// DefaultSize is the primary pane's percentage of the container for the no-JS
	// baseline (snapped to the nearest 5, clamped to [5,95]). Zero value is 50.
	// Keep it equal to the ResizableHandle's Value so the AT value and the rendered
	// geometry agree.
	DefaultSize int
}

func (p ResizableProps) defaultSize() int {
	v := p.DefaultSize
	if v <= 0 {
		v = 50
	}
	return snap5(v)
}

// ResizablePaneProps configures one ResizablePane (a scrollable content region).
type ResizablePaneProps struct {
	Base
	// Label is the pane's accessible name (aria-label). Recommended so a separator's
	// aria-controls target is nameable and the region is announced.
	Label string
}

// ResizableHandleProps configures the ResizableHandle (the role=separator between
// the panes).
type ResizableHandleProps struct {
	Base
	// Orientation must match the root's Orientation so the separator's
	// aria-orientation is correct. Zero value is horizontal (a vertical separator).
	Orientation ResizableOrientation
	// Value is the primary pane's current percentage (aria-valuenow). Zero value is
	// 50. Keep it equal to the root's DefaultSize.
	Value int
	// Min and Max are the resize bounds (aria-valuemin/valuemax). Zero values are
	// 10 and 90.
	Min int
	Max int
	// ControlsID is the id of the primary pane the separator resizes (aria-controls).
	// Recommended so assistive technology links the separator to the pane it sizes.
	ControlsID string
	// Label is the separator's accessible name (aria-label). Empty renders the
	// default "Resize".
	Label string
}

func (p ResizableHandleProps) bounds() (value, min, max int) {
	min = p.Min
	if min <= 0 {
		min = 10
	}
	max = p.Max
	if max <= 0 || max > 100 {
		max = 90
	}
	if max < min {
		min, max = max, min
	}
	value = p.Value
	if value <= 0 {
		value = 50
	}
	return clampSize(value, min, max), min, max
}

func resizableClass(p ResizableProps) string { return classNames("goth-resizable", p.Class) }

func resizableAttrs(p ResizableProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":         "resizable",
		"data-orientation":  p.Orientation.attr(),
		"data-default-size": strconv.Itoa(p.defaultSize()),
		"x-data":            "gothResizable",
	}
	return ownedAttrs(p.Base, owned)
}

func resizablePaneClass(p ResizablePaneProps) string {
	return classNames("goth-resizable-pane", p.Class)
}

func resizablePaneAttrs(p ResizablePaneProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "resizable-pane",
		"role":      "group",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func resizableHandleClass(p ResizableHandleProps) string {
	return classNames("goth-resizable-handle", p.Class)
}

func resizableHandleAttrs(p ResizableHandleProps) templ.Attributes {
	value, min, max := p.bounds()
	label := p.Label
	if label == "" {
		label = "Resize"
	}
	owned := templ.Attributes{
		"data-slot":        "resizable-handle",
		"role":             "separator",
		"tabindex":         "0",
		"aria-orientation": p.Orientation.separatorOrientation(),
		"aria-valuenow":    strconv.Itoa(value),
		"aria-valuemin":    strconv.Itoa(min),
		"aria-valuemax":    strconv.Itoa(max),
		"aria-label":       label,
	}
	if p.ControlsID != "" {
		owned["aria-controls"] = p.ControlsID
	}
	return ownedAttrs(p.Base, owned)
}
