package primitives

import "github.com/a-h/templ"

// HoverCardProps configures the Hover Card container (P42, family F4): a
// supplementary, non-critical floating panel opened by hovering or focusing its
// trigger. No-JS baseline: the content is always in the DOM and revealed by a pure
// CSS hover/focus-within rule, so it works without JavaScript. The gothHoverCard
// controller enhances that baseline (it sets data-enhanced on the root, so the CSS
// follows the controller-owned data-state): hover opens after an intent delay with
// a close grace period that bridges the gap to the (possibly interactive) panel,
// focus opens immediately, and Escape, blur outside the card, or an outside press
// (touch-safe) dismisses it. It never traps focus and never moves focus into the
// panel — the card is supplementary, not a dialog. data-slot hooks: hover-card,
// trigger, hovercard-content.
type HoverCardProps struct{ Base }

// HoverCardTriggerProps configures the HoverCardTrigger. With a zero URL it
// renders a <button>; with a URL set it renders an anchor (hover cards commonly
// wrap a profile/resource link), so "links remain links".
type HoverCardTriggerProps struct {
	Base
	// URL, when set, renders the trigger as an anchor to that destination.
	URL URL
}

// HoverCardContentProps configures the HoverCardContent floating panel.
type HoverCardContentProps struct{ Base }

func hoverCardClass(p HoverCardProps) string { return classNames("goth-hover-card", p.Class) }

func hoverCardAttrs(p HoverCardProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":         "hover-card",
		"data-state":        "closed",
		"x-data":            "gothHoverCard",
		"x-on:pointerenter": "enter($event)",
		"x-on:pointerleave": "leave($event)",
		"x-on:focusin":      "focusShow($event)",
		"x-on:focusout":     "focusHide($event)",
	})
}

func hoverCardTriggerButtonAttrs(p HoverCardTriggerProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "trigger",
		"type":      "button",
	})
}

func hoverCardTriggerLinkAttrs(p HoverCardTriggerProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "trigger",
	})
}

func hoverCardContentAttrs(p HoverCardContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "hovercard-content",
	})
}
