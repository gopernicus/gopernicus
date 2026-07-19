package primitives

import "github.com/a-h/templ"

// PopoverSide is the preferred edge of the trigger a Popover panel opens from. The
// zero value is the bottom edge. It is emitted as data-side-preferred; the runtime
// anchor enhancement reads it and, after collision resolution, sets the resolved
// data-side the CSS keys off.
type PopoverSide string

const (
	// PopoverBottom is the zero value: the panel prefers the bottom edge.
	PopoverBottom PopoverSide = ""
	// PopoverTop prefers the top edge.
	PopoverTop PopoverSide = "top"
	// PopoverLeft prefers the left edge.
	PopoverLeft PopoverSide = "left"
	// PopoverRight prefers the right edge.
	PopoverRight PopoverSide = "right"
)

// Valid reports whether s is a known PopoverSide.
func (s PopoverSide) Valid() bool {
	switch s {
	case PopoverBottom, PopoverTop, PopoverLeft, PopoverRight:
		return true
	default:
		return false
	}
}

func (s PopoverSide) attr() string {
	switch s {
	case PopoverTop:
		return "top"
	case PopoverLeft:
		return "left"
	case PopoverRight:
		return "right"
	default:
		return "bottom"
	}
}

// PopoverAlign is the cross-axis alignment of a Popover panel against its trigger.
// The zero value aligns the panel's start edge to the trigger's start edge.
type PopoverAlign string

const (
	// PopoverAlignStart is the zero value.
	PopoverAlignStart PopoverAlign = ""
	// PopoverAlignCenter centers the panel on the trigger.
	PopoverAlignCenter PopoverAlign = "center"
	// PopoverAlignEnd aligns the panel's end edge to the trigger's end edge.
	PopoverAlignEnd PopoverAlign = "end"
)

// Valid reports whether a is a known PopoverAlign.
func (a PopoverAlign) Valid() bool {
	switch a {
	case PopoverAlignStart, PopoverAlignCenter, PopoverAlignEnd:
		return true
	default:
		return false
	}
}

func (a PopoverAlign) attr() string {
	switch a {
	case PopoverAlignCenter:
		return "center"
	case PopoverAlignEnd:
		return "end"
	default:
		return "start"
	}
}

// PopoverProps configures the Popover wrapper (P45, family F3). Popover rides the
// native popover/popovertarget attributes: the trigger toggles the panel, Escape
// closes it, an outside press light-dismisses it, and the panel enters the top
// layer — ALL with NO JavaScript and NO controller (README §8 "native popover
// where support and semantics suffice"). The only enhancement is the runtime
// anchor positioning (Interactive/Full): a delegated, CSP-safe listener positions
// the opened panel against its trigger with collision handling; the no-JS baseline
// falls back to the UA-centered position. Wire the pair by setting
// PopoverContent.Base.ID and PopoverTrigger.Target to the same id. data-slot
// hooks: popover, trigger, popover-content.
type PopoverProps struct{ Base }

// PopoverTriggerProps configures the trigger button. Target is the PopoverContent
// id it toggles (the native popovertarget).
type PopoverTriggerProps struct {
	Base
	// Target is the PopoverContent id (rendered as popovertarget). Required.
	Target string
}

// PopoverContentProps configures the popover panel. Set Base.ID to the id
// PopoverTrigger.Target points at. Side/Align give the preferred placement the
// runtime anchor enhancement uses.
type PopoverContentProps struct {
	Base
	// Side is the preferred trigger edge; zero value is the bottom edge.
	Side PopoverSide
	// Align is the cross-axis alignment; zero value is start.
	Align PopoverAlign
}

func popoverClass(p PopoverProps) string { return classNames("goth-popover", p.Class) }

func popoverAttrs(p PopoverProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "popover"})
}

func popoverTriggerAttrs(p PopoverTriggerProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":           "trigger",
		"type":                "button",
		"popovertargetaction": "toggle",
	}
	if p.Target != "" {
		owned["popovertarget"] = p.Target
	}
	return ownedAttrs(p.Base, owned)
}

func popoverContentAttrs(p PopoverContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":            "popover-content",
		"popover":              "auto",
		"data-side-preferred":  p.Side.attr(),
		"data-align-preferred": p.Align.attr(),
	})
}
