package primitives

import (
	"strings"
	"testing"
)

// TestNoAnchoredPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-4.3
// anchored primitive emits an inline style= in any state — all placement is
// data-side/data-state + the CSSOM anchor offsets + external CSS (native popover
// is UA-positioned), never a server-rendered style attribute.
func TestNoAnchoredPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Tooltip(TooltipProps{}), "x"),
		renderKids(t, TooltipTrigger(TooltipTriggerProps{Describedby: "tt"}), "Hover"),
		renderKids(t, TooltipContent(TooltipContentProps{Base: Base{ID: "tt"}}), "Info"),
		renderKids(t, HoverCard(HoverCardProps{}), "x"),
		renderKids(t, HoverCardTrigger(HoverCardTriggerProps{}), "Trigger"),
		renderKids(t, HoverCardTrigger(HoverCardTriggerProps{URL: mustParseURL(t, "/u/ada")}), "@ada"),
		renderKids(t, HoverCardContent(HoverCardContentProps{}), "Card"),
		renderKids(t, Popover(PopoverProps{}), "x"),
		renderKids(t, PopoverTrigger(PopoverTriggerProps{Target: "pop"}), "Open"),
		renderKids(t, PopoverContent(PopoverContentProps{Base: Base{ID: "pop"}, Side: PopoverTop, Align: PopoverAlignCenter}), "Body"),
		renderKids(t, Select(SelectProps{Name: "fruit", Placeholder: "Pick one"}), `<option value="a">A</option>`),
		renderKids(t, SelectOption(SelectOptionProps{Value: "a", Selected: true}), "A"),
		renderKids(t, SelectGroup(SelectGroupProps{Label: "Group"}), `<option value="a">A</option>`),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("anchored primitive emitted an inline style=: %s", o)
		}
	}
}

func mustParseURL(t *testing.T, s string) URL {
	t.Helper()
	u, err := ParseURL(s)
	if err != nil {
		t.Fatalf("ParseURL(%q): %v", s, err)
	}
	return u
}

// TestTooltipBaselineAndWiring proves the no-JS baseline (content in the DOM with
// role="tooltip", the controller binding + closed state on the root) and the
// caller-passed aria-describedby wiring between trigger and content.
func TestTooltipBaselineAndWiring(t *testing.T) {
	root := renderKids(t, Tooltip(TooltipProps{}), "x")
	mustContain(t, root,
		`class="goth-tooltip"`, `data-slot="tooltip"`, `data-state="closed"`,
		`x-data="gothTooltip"`, `x-on:pointerenter="enter($event)"`,
		`x-on:pointerleave="leave($event)"`, `x-on:focusin="focusShow($event)"`,
		`x-on:focusout="focusHide($event)"`)

	trig := renderKids(t, TooltipTrigger(TooltipTriggerProps{Describedby: "tip-1"}), "Help")
	mustContain(t, trig, `<button`, `data-slot="trigger"`, `type="button"`, `aria-describedby="tip-1"`, "Help")

	content := renderKids(t, TooltipContent(TooltipContentProps{Base: Base{ID: "tip-1"}}), "Explains it")
	mustContain(t, content, `data-slot="tooltip-content"`, `role="tooltip"`, `id="tip-1"`, "Explains it")
}

// TestHoverCardTriggerForms proves the trigger is a button by default and an
// anchor when a URL is set (links remain links), and the controller binding +
// content slot are present.
func TestHoverCardTriggerForms(t *testing.T) {
	root := renderKids(t, HoverCard(HoverCardProps{}), "x")
	mustContain(t, root, `class="goth-hover-card"`, `data-slot="hover-card"`, `data-state="closed"`, `x-data="gothHoverCard"`)

	btn := renderKids(t, HoverCardTrigger(HoverCardTriggerProps{}), "Ada")
	mustContain(t, btn, `<button`, `data-slot="trigger"`, `type="button"`, "Ada")

	link := renderKids(t, HoverCardTrigger(HoverCardTriggerProps{URL: mustParseURL(t, "/users/ada")}), "@ada")
	mustContain(t, link, `<a`, `href="/users/ada"`, `data-slot="trigger"`, "@ada")
	mustNotContain(t, link, `type="button"`)

	content := renderKids(t, HoverCardContent(HoverCardContentProps{}), "Profile")
	mustContain(t, content, `data-slot="hovercard-content"`, "Profile")
}

// TestPopoverRidesNativeAttributes proves Popover uses the native popover API (no
// controller): the trigger carries popovertarget + popovertargetaction, and the
// content carries popover="auto" with the preferred placement attributes.
func TestPopoverRidesNativeAttributes(t *testing.T) {
	root := renderKids(t, Popover(PopoverProps{}), "x")
	mustContain(t, root, `class="goth-popover"`, `data-slot="popover"`)
	mustNotContain(t, root, `x-data`) // native, no controller

	trig := renderKids(t, PopoverTrigger(PopoverTriggerProps{Target: "pop-1"}), "Open")
	mustContain(t, trig, `<button`, `data-slot="trigger"`, `type="button"`, `popovertarget="pop-1"`, `popovertargetaction="toggle"`, "Open")

	content := renderKids(t, PopoverContent(PopoverContentProps{Base: Base{ID: "pop-1"}}), "Body")
	mustContain(t, content, `data-slot="popover-content"`, `popover="auto"`, `id="pop-1"`, `data-side-preferred="bottom"`, `data-align-preferred="start"`, "Body")
	mustNotContain(t, content, `x-data`)
}

// TestPopoverSideAlignResolution proves the Side/Align enums resolve to the frozen
// attribute values with the documented zero-value defaults and no panic on an
// unknown value.
func TestPopoverSideAlignResolution(t *testing.T) {
	sides := map[PopoverSide]string{
		PopoverBottom: "bottom", PopoverTop: "top", PopoverLeft: "left",
		PopoverRight: "right", PopoverSide("nonsense"): "bottom",
	}
	for s, want := range sides {
		out := renderKids(t, PopoverContent(PopoverContentProps{Side: s}), "x")
		mustContain(t, out, `data-side-preferred="`+want+`"`)
	}
	aligns := map[PopoverAlign]string{
		PopoverAlignStart: "start", PopoverAlignCenter: "center",
		PopoverAlignEnd: "end", PopoverAlign("nope"): "start",
	}
	for a, want := range aligns {
		out := renderKids(t, PopoverContent(PopoverContentProps{Align: a}), "x")
		mustContain(t, out, `data-align-preferred="`+want+`"`)
	}
	if !PopoverBottom.Valid() || !PopoverRight.Valid() || PopoverSide("x").Valid() {
		t.Error("PopoverSide.Valid mismatch")
	}
	if !PopoverAlignStart.Valid() || !PopoverAlignEnd.Valid() || PopoverAlign("x").Valid() {
		t.Error("PopoverAlign.Valid mismatch")
	}
}

// TestSelectNativeBaseline proves Select is a styled native <select> (no
// controller): a real <select> submits the value, a placeholder renders a
// disabled+hidden initial option, and the invalid/required/disabled states map to
// native attributes. The chevron is decorative (aria-hidden).
func TestSelectNativeBaseline(t *testing.T) {
	out := renderKids(t, Select(SelectProps{Base: Base{ID: "fruit"}, Name: "fruit", Required: true, Invalid: true, Placeholder: "Choose a fruit"}),
		`<option value="apple">Apple</option>`)
	mustContain(t, out,
		`class="goth-select"`, `data-slot="select"`,
		`<select class="goth-select-input"`, `data-slot="select-input"`,
		`name="fruit"`, `required`, `id="fruit"`, `aria-invalid="true"`, `data-invalid="true"`,
		`class="goth-select-placeholder"`, `disabled`, `selected`, `hidden`, `Choose a fruit`,
		`data-slot="select-icon"`, `aria-hidden="true"`,
		`<option value="apple">Apple</option>`)
	mustNotContain(t, out, `x-data`) // native, no controller

	opt := renderKids(t, SelectOption(SelectOptionProps{Value: "a", Selected: true, Disabled: true}), "A")
	mustContain(t, opt, `<option`, `data-slot="select-option"`, `value="a"`, `selected`, `disabled`, "A")

	grp := renderKids(t, SelectGroup(SelectGroupProps{Label: "Citrus"}), `<option value="lime">Lime</option>`)
	mustContain(t, grp, `<optgroup`, `data-slot="select-group"`, `label="Citrus"`, "Lime")
}

// TestAnchoredMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch, while a benign caller data-* attribute merges.
func TestAnchoredMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Tooltip(TooltipProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"x-data":        "evil",
			"data-slot":     "hijack",
			"class":         "dropped",
			"data-testhook": "keep",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothTooltip"`, `data-slot="tooltip"`, `goth-tooltip custom-x`, `data-testhook="keep"`)
	mustNotContain(t, out, `evil`, `hijack`, `dropped`)
}
