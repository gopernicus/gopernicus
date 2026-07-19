package primitives

import "github.com/a-h/templ"

// AccordionType selects single- versus multiple-open behavior. The zero value is
// single (only one item open at a time), matching the most common Shadcn default.
type AccordionType string

const (
	// AccordionSingle is the zero value: at most one item open at a time. Single
	// mode is achieved natively — sibling AccordionItem <details> that share a
	// Name attribute form a native exclusive group with no JavaScript.
	AccordionSingle AccordionType = ""
	// AccordionMultiple lets any number of items stay open at once.
	AccordionMultiple AccordionType = "multiple"
)

// Valid reports whether t is a known AccordionType.
func (t AccordionType) Valid() bool {
	switch t {
	case AccordionSingle, AccordionMultiple:
		return true
	default:
		return false
	}
}

func (t AccordionType) attr() string {
	if t == AccordionMultiple {
		return "multiple"
	}
	return "single"
}

// AccordionProps configures the Accordion container (P27, family F4). The caller
// composes AccordionItem parts as children. The container is presentational; the
// exclusive single-open behavior comes from the items sharing a Name (native
// <details name>), so the accordion discloses and stays keyboard-operable without
// JavaScript. data-slot hooks: accordion, accordion-item, accordion-trigger,
// accordion-content.
type AccordionProps struct {
	Base
	Type AccordionType
}

// AccordionItemProps configures one AccordionItem, a native <details>.
type AccordionItemProps struct {
	Base
	// Name groups items for native single-open behavior. For AccordionSingle, the
	// caller sets the SAME Name on every item so the browser keeps only one open —
	// no controller needed. Leave empty for AccordionMultiple.
	Name string
	// Open is the server-rendered default open state (native open attribute).
	Open bool
}

// AccordionTriggerProps configures the AccordionTrigger, a native <summary>.
type AccordionTriggerProps struct{ Base }

// AccordionContentProps configures the disclosed AccordionContent region.
type AccordionContentProps struct{ Base }

func accordionClass(p AccordionProps) string { return classNames("goth-accordion", p.Class) }

func accordionAttrs(p AccordionProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "accordion",
		"data-type": p.Type.attr(),
	})
}

func accordionItemClass(p AccordionItemProps) string {
	return classNames("goth-accordion-item", p.Class)
}

func accordionItemAttrs(p AccordionItemProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "accordion-item",
		"data-state": openState(p.Open),
		"x-data":     "gothCollapse",
		"open":       p.Open,
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	return ownedAttrs(p.Base, owned)
}

func accordionPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func accordionPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}
