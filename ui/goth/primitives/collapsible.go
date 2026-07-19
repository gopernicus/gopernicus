package primitives

import "github.com/a-h/templ"

// Collapsible (P29, family F4) is a single disclosure region built on the native
// <details>/<summary> element, so it discloses and toggles WITHOUT JavaScript
// (native keyboard: Enter/Space on the summary; native focus). The gothCollapse
// controller enhances the native baseline only: it reflects the open/closed state
// onto data-state (an animation/style hook) and dispatches goth:open/goth:close,
// never taking over disclosure itself. The caller composes CollapsibleTrigger and
// CollapsibleContent. data-slot hooks: collapsible, collapsible-trigger,
// collapsible-content.
type CollapsibleProps struct {
	Base
	// Open is the server-rendered default open state. When true the <details>
	// renders with the native open attribute (visible without JS); the enhancement
	// keeps data-state in sync afterwards. Zero value is closed.
	Open bool
}

// CollapsibleTriggerProps configures CollapsibleTrigger, the native <summary>.
type CollapsibleTriggerProps struct{ Base }

// CollapsibleContentProps configures CollapsibleContent, the disclosed region.
type CollapsibleContentProps struct{ Base }

func collapsibleClass(p CollapsibleProps) string { return classNames("goth-collapsible", p.Class) }

func collapsibleAttrs(p CollapsibleProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "collapsible",
		"data-state": openState(p.Open),
		"x-data":     "gothCollapse",
		"open":       p.Open,
	})
}

func collapsiblePartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func collapsiblePartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

// openState maps a boolean open flag to the frozen data-state disclosure value.
func openState(open bool) string {
	if open {
		return "open"
	}
	return "closed"
}
