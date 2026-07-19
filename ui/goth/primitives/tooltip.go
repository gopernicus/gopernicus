package primitives

import "github.com/a-h/templ"

// TooltipProps configures the Tooltip container (P48, family F4). No-JS baseline:
// the content (role="tooltip") is always in the DOM and revealed by a pure CSS
// hover/focus-within rule, and the trigger's aria-describedby points at it — so a
// tooltip works without JavaScript. The gothTooltip controller enhances that
// baseline (it sets data-enhanced on the root, so the CSS switches from the
// instant :hover reveal to the controller-owned data-state): hover opens after an
// intent delay, focus opens immediately, and Escape, blur, or an outside press
// hides it. It never traps focus and never moves focus into the tooltip. ARIA
// linkage is caller-passed: set TooltipContent.Base.ID and reference it from
// TooltipTrigger.Describedby. data-slot hooks: tooltip, trigger, tooltip-content.
type TooltipProps struct{ Base }

// TooltipTriggerProps configures the TooltipTrigger button. Describedby is the
// TooltipContent id (aria-describedby), so a screen reader announces the tooltip
// as the trigger's description.
type TooltipTriggerProps struct {
	Base
	// Describedby is the TooltipContent id wired as aria-describedby.
	Describedby string
}

// TooltipContentProps configures the TooltipContent (role="tooltip"). Set Base.ID
// and reference it from TooltipTrigger.Describedby.
type TooltipContentProps struct{ Base }

func tooltipClass(p TooltipProps) string { return classNames("goth-tooltip", p.Class) }

func tooltipAttrs(p TooltipProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":         "tooltip",
		"data-state":        "closed",
		"x-data":            "gothTooltip",
		"x-on:pointerenter": "enter($event)",
		"x-on:pointerleave": "leave($event)",
		"x-on:focusin":      "focusShow($event)",
		"x-on:focusout":     "focusHide($event)",
	})
}

func tooltipTriggerAttrs(p TooltipTriggerProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "trigger",
		"type":      "button",
	}
	if p.Describedby != "" {
		owned["aria-describedby"] = p.Describedby
	}
	return ownedAttrs(p.Base, owned)
}

func tooltipContentAttrs(p TooltipContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "tooltip-content",
		"role":      "tooltip",
	})
}
