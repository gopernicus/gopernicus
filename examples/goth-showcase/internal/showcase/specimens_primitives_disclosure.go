package showcase

import (
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// registerDisclosureSpecimens registers the GOTH-3.1 disclosure primitives
// (P27 Accordion, P29 Collapsible, P34 Tabs). Unlike the presentational Phase 2
// specimens, these run under the Interactive profile so the browser harness
// exercises the real Alpine controllers (gothCollapse, gothTabs) enhancing the
// native no-JS baseline. Each renders the real primitive component, not
// hand-written markup.
func registerDisclosureSpecimens(r *Registry) {
	specs := []struct {
		id, title, prim string
		body            func() string
	}{
		{"accordion", "Accordion (P27)", "P27", accordionSpecimen},
		{"collapsible", "Collapsible (P29)", "P29", collapsibleSpecimen},
		{"tabs", "Tabs (P34)", "P34", tabsSpecimen},
	}
	for _, s := range specs {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.Interactive,
			Body:      s.body,
		})
	}

	// An RTL Tabs specimen proves the roving-focus arrow direction follows the
	// document direction (GOTH-3.4): under dir="rtl" ArrowLeft advances to the
	// next tab and ArrowRight retreats, matching the visual layout and Radix/APG
	// parity. Same Interactive profile / P34 primitive; separate id and Dir.
	r.Register(Specimen{
		ID:        "primitive-tabs-rtl",
		Title:     "Tabs RTL (P34)",
		Section:   SectionPrimitive,
		Primitive: "P34",
		Profile:   goth.Interactive,
		Dir:       theme.DirectionRTL,
		Body:      tabsSpecimen,
	})
}

// collapsibleSpecimen shows a single native <details> disclosure: closed by
// default, enhanced by gothCollapse. It works with no JavaScript.
func collapsibleSpecimen() string {
	closed := compKids(primitives.Collapsible(primitives.CollapsibleProps{}),
		compKids(primitives.CollapsibleTrigger(primitives.CollapsibleTriggerProps{}), "What is progressive enhancement?")+
			compKids(primitives.CollapsibleContent(primitives.CollapsibleContentProps{}),
				"<p>The disclosure works natively; the controller only mirrors state and dispatches events.</p>"))
	open := compKids(primitives.Collapsible(primitives.CollapsibleProps{Open: true}),
		compKids(primitives.CollapsibleTrigger(primitives.CollapsibleTriggerProps{}), "Open by default")+
			compKids(primitives.CollapsibleContent(primitives.CollapsibleContentProps{}),
				"<p>This region is server-rendered open, so its content is readable without JavaScript.</p>"))
	return page("Collapsible", `<section data-slot="collapsibles">`+closed+open+`</section>`)
}

// accordionSpecimen shows a single-open accordion: the items share a Name, so the
// browser keeps only one open natively (no JavaScript required for exclusivity).
func accordionSpecimen() string {
	item := func(name, title, body string, open bool) string {
		return compKids(primitives.AccordionItem(primitives.AccordionItemProps{Name: name, Open: open}),
			compKids(primitives.AccordionTrigger(primitives.AccordionTriggerProps{}), title)+
				compKids(primitives.AccordionContent(primitives.AccordionContentProps{}), "<p>"+body+"</p>"))
	}
	single := compKids(primitives.Accordion(primitives.AccordionProps{Type: primitives.AccordionSingle}),
		item("faq", "Is it accessible?", "Yes — native details/summary give keyboard and focus behavior for free.", true)+
			item("faq", "Is it styled?", "Yes — the chevron and surface come from component CSS keyed off [open].", false)+
			item("faq", "Does it need JavaScript?", "No — single-open exclusivity uses the native details name attribute.", false))
	multi := compKids(primitives.Accordion(primitives.AccordionProps{Type: primitives.AccordionMultiple}),
		item("", "Section A", "Multiple-mode items open independently.", false)+
			item("", "Section B", "Both can be open at once.", false))
	return page("Accordion", `<section data-slot="accordions"><h2>Single</h2>`+single+`<h2>Multiple</h2>`+multi+`</section>`)
}

// tabsSpecimen shows a three-tab set. The server marks the first tab active, so
// its panel is visible and the others hidden — readable without JavaScript. The
// gothTabs controller adds roving focus and switching.
func tabsSpecimen() string {
	type tab struct {
		value, tabID, panelID, label, body string
		active                             bool
	}
	tabs := []tab{
		{"account", "tab-account", "panel-account", "Account", "Manage your account profile and email.", true},
		{"password", "tab-password", "panel-password", "Password", "Change your password and sessions.", false},
		{"notifications", "tab-notifications", "panel-notifications", "Notifications", "Choose which emails you receive.", false},
	}
	var triggers, panels string
	for _, t := range tabs {
		triggers += compKids(primitives.TabsTrigger(primitives.TabsTriggerProps{
			Base:     primitives.Base{ID: t.tabID},
			Value:    t.value,
			Active:   t.active,
			Controls: t.panelID,
		}), t.label)
		panels += compKids(primitives.TabsContent(primitives.TabsContentProps{
			Base:       primitives.Base{ID: t.panelID},
			Value:      t.value,
			Active:     t.active,
			Labelledby: t.tabID,
		}), "<p>"+t.body+"</p>")
	}
	list := compKids(primitives.TabsList(primitives.TabsListProps{Orientation: primitives.TabsHorizontal}), triggers)
	set := compKids(primitives.Tabs(primitives.TabsProps{Orientation: primitives.TabsHorizontal}), list+panels)
	return page("Tabs", `<section data-slot="tabsets">`+set+`</section>`)
}
