package primitives

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestNoDisclosurePrimitiveEmitsInlineStyle proves invariant (a): no GOTH-3.1
// disclosure primitive emits an inline style= in any state — dynamic geometry is
// data-state + CSS, never style.
func TestNoDisclosurePrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Collapsible(CollapsibleProps{Open: true}), "x"),
		renderKids(t, CollapsibleTrigger(CollapsibleTriggerProps{}), "Toggle"),
		renderKids(t, CollapsibleContent(CollapsibleContentProps{}), "body"),
		renderKids(t, Accordion(AccordionProps{}), "x"),
		renderKids(t, AccordionItem(AccordionItemProps{Name: "g", Open: true}), "x"),
		renderKids(t, AccordionTrigger(AccordionTriggerProps{}), "Item"),
		renderKids(t, AccordionContent(AccordionContentProps{}), "body"),
		renderKids(t, Tabs(TabsProps{}), "x"),
		renderKids(t, TabsList(TabsListProps{}), "x"),
		renderKids(t, TabsTrigger(TabsTriggerProps{Value: "a", Active: true, Controls: "p"}), "Tab"),
		renderKids(t, TabsContent(TabsContentProps{Value: "a", Active: false, Labelledby: "t"}), "panel"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("disclosure primitive emitted an inline style=: %s", o)
		}
	}
}

// TestCollapsibleNativeBaseline proves Collapsible is a native <details>/<summary>
// disclosure (works with no JS) that the gothCollapse controller enhances.
func TestCollapsibleNativeBaseline(t *testing.T) {
	// Default (closed): native details, no open attribute, data-state closed, and
	// the enhancement binding.
	closed := renderKids(t, Collapsible(CollapsibleProps{}), "content")
	mustContain(t, closed, `<details`, `class="goth-collapsible"`, `data-slot="collapsible"`, `data-state="closed"`, `x-data="gothCollapse"`)
	mustNotContain(t, closed, ` open`)

	// Open: native open attribute so the region is visible without JavaScript.
	open := renderKids(t, Collapsible(CollapsibleProps{Open: true}), "content")
	mustContain(t, open, `<details`, ` open`, `data-state="open"`)

	// Trigger is a native <summary> (native keyboard/focus, no controller needed).
	trig := renderKids(t, CollapsibleTrigger(CollapsibleTriggerProps{}), "Toggle me")
	mustContain(t, trig, `<summary`, `data-slot="collapsible-trigger"`, "Toggle me", `data-slot="collapsible-chevron"`, `aria-hidden="true"`)

	content := renderKids(t, CollapsibleContent(CollapsibleContentProps{}), "revealed")
	mustContain(t, content, `data-slot="collapsible-content"`, "revealed")
}

// TestAccordionNativeExclusive proves Accordion items are native <details>. Single
// mode is achieved with a shared name attribute (native exclusivity, no JS), and
// each item carries the enhancement binding + disclosure data-state.
func TestAccordionNativeExclusive(t *testing.T) {
	root := renderKids(t, Accordion(AccordionProps{Type: AccordionSingle}), "x")
	mustContain(t, root, `class="goth-accordion"`, `data-slot="accordion"`, `data-type="single"`)

	multi := renderKids(t, Accordion(AccordionProps{Type: AccordionMultiple}), "x")
	mustContain(t, multi, `data-type="multiple"`)

	// Unknown type falls back to single without panicking.
	def := renderKids(t, Accordion(AccordionProps{Type: AccordionType("weird")}), "x")
	mustContain(t, def, `data-type="single"`)

	// Single-mode item: shared name = native exclusive group.
	item := renderKids(t, AccordionItem(AccordionItemProps{Name: "faq", Open: true}), "x")
	mustContain(t, item, `<details`, `data-slot="accordion-item"`, `name="faq"`, ` open`, `data-state="open"`, `x-data="gothCollapse"`)

	// Multiple-mode item: no name (independent), closed by default.
	free := renderKids(t, AccordionItem(AccordionItemProps{}), "x")
	mustContain(t, free, `data-state="closed"`)
	mustNotContain(t, free, `name=`, ` open`)

	trig := renderKids(t, AccordionTrigger(AccordionTriggerProps{}), "Question")
	mustContain(t, trig, `<summary`, `data-slot="accordion-trigger"`, "Question", `data-slot="accordion-chevron"`)

	content := renderKids(t, AccordionContent(AccordionContentProps{}), "Answer")
	mustContain(t, content, `data-slot="accordion-content"`, "Answer")
}

func TestAccordionTypeValid(t *testing.T) {
	for _, ty := range []AccordionType{AccordionSingle, AccordionMultiple} {
		if !ty.Valid() {
			t.Errorf("%q should be valid", ty)
		}
	}
	if AccordionType("both").Valid() {
		t.Error("both should be invalid")
	}
}

// TestTabsRolesAndBaseline proves the tab set carries the correct ARIA roles and a
// readable no-JS baseline: the active panel is visible, inactive panels hidden.
func TestTabsRolesAndBaseline(t *testing.T) {
	root := renderKids(t, Tabs(TabsProps{Orientation: TabsHorizontal, Activation: TabsAutomatic}), "x")
	mustContain(t, root, `class="goth-tabs"`, `data-slot="tabs"`, `data-orientation="horizontal"`, `data-activation="automatic"`, `x-data="gothTabs"`)

	vert := renderKids(t, Tabs(TabsProps{Orientation: TabsVertical, Activation: TabsManual}), "x")
	mustContain(t, vert, `data-orientation="vertical"`, `data-activation="manual"`)

	// Unknown enum values fall back to the documented defaults.
	def := renderKids(t, Tabs(TabsProps{Orientation: TabsOrientation("x"), Activation: TabsActivation("y")}), "x")
	mustContain(t, def, `data-orientation="horizontal"`, `data-activation="automatic"`)

	list := renderKids(t, TabsList(TabsListProps{Orientation: TabsHorizontal}), "x")
	mustContain(t, list, `data-slot="tab-list"`, `role="tablist"`, `aria-orientation="horizontal"`, `x-on:keydown="onKeydown($event)"`)

	// Active trigger: single tab stop (tabindex 0), aria-selected true, aria-controls.
	active := renderKids(t, TabsTrigger(TabsTriggerProps{Base: Base{ID: "t-a"}, Value: "a", Active: true, Controls: "p-a"}), "Account")
	mustContain(t, active, `<button`, `id="t-a"`, `role="tab"`, `type="button"`, `data-slot="tab"`, `data-value="a"`, `data-state="active"`, `aria-selected="true"`, `tabindex="0"`, `aria-controls="p-a"`, `x-on:click="activate($event)"`, "Account")

	// Inactive trigger: removed from the tab order (tabindex -1), aria-selected false.
	inactive := renderKids(t, TabsTrigger(TabsTriggerProps{Base: Base{ID: "t-b"}, Value: "b", Controls: "p-b"}), "Password")
	mustContain(t, inactive, `data-state="inactive"`, `aria-selected="false"`, `tabindex="-1"`)

	// Active panel: visible (not hidden), labelled by its trigger.
	activePanel := renderKids(t, TabsContent(TabsContentProps{Base: Base{ID: "p-a"}, Value: "a", Active: true, Labelledby: "t-a"}), "Account settings")
	mustContain(t, activePanel, `id="p-a"`, `role="tabpanel"`, `data-slot="tab-panel"`, `data-value="a"`, `aria-labelledby="t-a"`, `tabindex="0"`, "Account settings")
	mustNotContain(t, activePanel, `hidden`)

	// Inactive panel: hidden in the no-JS baseline.
	inactivePanel := renderKids(t, TabsContent(TabsContentProps{Base: Base{ID: "p-b"}, Value: "b", Active: false, Labelledby: "t-b"}), "Password settings")
	mustContain(t, inactivePanel, `hidden`, `aria-labelledby="t-b"`)
}

func TestTabsEnumsValid(t *testing.T) {
	for _, o := range []TabsOrientation{TabsHorizontal, TabsVertical} {
		if !o.Valid() {
			t.Errorf("orientation %q should be valid", o)
		}
	}
	if TabsOrientation("diagonal").Valid() {
		t.Error("diagonal should be invalid")
	}
	for _, a := range []TabsActivation{TabsAutomatic, TabsManual} {
		if !a.Valid() {
			t.Errorf("activation %q should be valid", a)
		}
	}
	if TabsActivation("eager").Valid() {
		t.Error("eager should be invalid")
	}
}

// TestDisclosureBaseMergeAndClass proves the shared Base contract on a disclosure
// primitive: owned attributes win, a caller class in Attributes is dropped,
// Base.Class appends after the stable class, and Base.ID is honored — while the
// owned x-data binding cannot be overwritten through the escape hatch.
func TestDisclosureBaseMergeAndClass(t *testing.T) {
	out := renderKids(t, Tabs(TabsProps{
		Base: Base{
			ID:    "tabs-1",
			Class: "mt-4",
			Attributes: templ.Attributes{
				"class":       "sneaky",   // dropped
				"x-data":      "gothEvil", // owned wins
				"data-testid": "keep",
			},
		},
	}), "x")
	mustContain(t, out, `id="tabs-1"`, `class="goth-tabs mt-4"`, `x-data="gothTabs"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", "gothEvil")
}
