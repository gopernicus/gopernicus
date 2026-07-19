package primitives

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestNoCompactPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-3.3
// compact-selection primitive emits an inline style= in any state — the styled
// surfaces are drawn by component CSS off :checked / :has() / data-state.
func TestNoCompactPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, InputOTP(InputOTPProps{}), "x"),
		render(t, InputOTPSlot(InputOTPSlotProps{Name: "otp", Autocomplete: true})),
		render(t, InputOTPSeparator(InputOTPSeparatorProps{})),
		renderKids(t, Toggle(ToggleProps{Name: "b", Pressed: true, Variant: ToggleOutline}), "B"),
		renderKids(t, ToggleGroup(ToggleGroupProps{Type: ToggleGroupMultiple}), "x"),
		renderKids(t, ToggleGroupItem(ToggleGroupItemProps{Type: ToggleGroupMultiple, Name: "f", Value: "b", Pressed: true}), "B"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("compact primitive emitted an inline style=: %s", o)
		}
	}
}

// TestInputOTPNativeSlots proves InputOTP is a role="group" of native
// single-character inputs that submit and navigate with no JavaScript and bind no
// controller.
func TestInputOTPNativeSlots(t *testing.T) {
	group := renderKids(t, InputOTP(InputOTPProps{Base: Base{Attributes: templ.Attributes{"aria-label": "One-time code"}}}), "x")
	mustContain(t, group, `<div`, `class="goth-input-otp"`, `data-slot="input-otp"`, `role="group"`, `aria-label="One-time code"`)
	mustNotContain(t, group, `x-data`)

	// First slot: native single-char input with one-time-code autocomplete.
	first := render(t, InputOTPSlot(InputOTPSlotProps{Base: Base{ID: "otp-1"}, Name: "otp", Autocomplete: true}))
	mustContain(t, first, `<input`, `id="otp-1"`, `class="goth-input-otp-slot"`, `data-slot="input-otp-slot"`, `type="text"`, `inputmode="numeric"`, `maxlength="1"`, `autocomplete="one-time-code"`, `name="otp"`)
	mustNotContain(t, first, `x-data`)

	// A later slot omits autocomplete; a filled/disabled/invalid slot renders its attrs.
	filled := render(t, InputOTPSlot(InputOTPSlotProps{Name: "otp", Value: "4", Disabled: true, Invalid: true}))
	mustContain(t, filled, `value="4"`, `disabled`, `aria-invalid="true"`, `data-invalid="true"`)
	mustNotContain(t, filled, `autocomplete`)

	// Separator is decorative.
	sep := render(t, InputOTPSeparator(InputOTPSeparatorProps{}))
	mustContain(t, sep, `data-slot="input-otp-separator"`, `aria-hidden="true"`)
}

// TestToggleCheckboxBacked proves Toggle is a checkbox-backed toggle button that
// submits its pressed state natively with no controller.
func TestToggleCheckboxBacked(t *testing.T) {
	off := renderKids(t, Toggle(ToggleProps{Name: "bold", Value: "on"}), "B")
	mustContain(t, off, `<label`, `class="goth-toggle"`, `data-slot="toggle"`, `data-variant="default"`, `data-size="default"`, `data-state="unchecked"`,
		`<input class="goth-toggle-input"`, `type="checkbox"`, `name="bold"`, `value="on"`, `data-slot="toggle-label"`)
	mustNotContain(t, off, ` checked`, `x-data`)

	// Pressed renders the native checked attribute (submitted).
	on := renderKids(t, Toggle(ToggleProps{Name: "bold", Value: "on", Pressed: true, Variant: ToggleOutline, Size: ToggleSizeLarge}), "B")
	mustContain(t, on, ` checked`, `data-state="checked"`, `data-variant="outline"`, `data-size="lg"`)

	dis := renderKids(t, Toggle(ToggleProps{Name: "i", Disabled: true, Size: ToggleSizeSmall}), "I")
	mustContain(t, dis, `disabled`, `data-size="sm"`)
}

// TestToggleGroupSingleNativeRadios proves single-mode items are native radios with
// no controller (native single selection + roving), and that they submit.
func TestToggleGroupSingleNativeRadios(t *testing.T) {
	group := renderKids(t, ToggleGroup(ToggleGroupProps{Base: Base{Attributes: templ.Attributes{"aria-label": "Alignment"}}}), "x")
	mustContain(t, group, `<div`, `class="goth-toggle-group"`, `data-slot="toggle-group"`, `role="group"`, `data-type="single"`, `data-orientation="horizontal"`, `aria-label="Alignment"`)
	mustNotContain(t, group, `x-data`, `x-on:keydown`)

	item := renderKids(t, ToggleGroupItem(ToggleGroupItemProps{Base: Base{ID: "al-left"}, Type: ToggleGroupSingle, Name: "align", Value: "left", Pressed: true}), "Left")
	mustContain(t, item, `<label`, `id="al-left"`, `class="goth-toggle-group-item"`, `data-slot="toggle-group-item"`, `data-state="checked"`,
		`<input class="goth-toggle-group-input"`, `data-slot="item"`, `type="radio"`, `name="align"`, `value="left"`, ` checked`)
}

// TestToggleGroupMultipleRovingFocus proves multiple-mode items are native
// checkboxes and the group binds the frozen gothRovingFocus controller for roving.
func TestToggleGroupMultipleRovingFocus(t *testing.T) {
	group := renderKids(t, ToggleGroup(ToggleGroupProps{Type: ToggleGroupMultiple, Orientation: ToggleGroupVertical, Disabled: true}), "x")
	mustContain(t, group, `data-type="multiple"`, `data-orientation="vertical"`, `data-disabled="true"`, `x-data="gothRovingFocus"`, `x-on:keydown="onKeydown($event)"`)

	item := render(t, ToggleGroupItem(ToggleGroupItemProps{Type: ToggleGroupMultiple, Name: "fmt", Value: "bold", Disabled: true}))
	mustContain(t, item, `data-slot="item"`, `type="checkbox"`, `name="fmt"`, `value="bold"`, `disabled`, `data-state="unchecked"`)
	mustNotContain(t, item, `type="radio"`)
}

// TestCompactEnumsValid proves the GOTH-3.3 enums accept their known values and
// reject unknowns (fail-fast Valid()), and that unknown values render the default.
func TestCompactEnumsValid(t *testing.T) {
	for _, v := range []ToggleVariant{ToggleDefault, ToggleOutline} {
		if !v.Valid() {
			t.Errorf("variant %q should be valid", v)
		}
	}
	if ToggleVariant("ghost").Valid() {
		t.Error("ghost is not a toggle variant")
	}
	for _, s := range []ToggleSize{ToggleSizeDefault, ToggleSizeSmall, ToggleSizeLarge} {
		if !s.Valid() {
			t.Errorf("size %q should be valid", s)
		}
	}
	if ToggleSize("xl").Valid() {
		t.Error("xl is not a toggle size")
	}
	for _, tp := range []ToggleGroupType{ToggleGroupSingle, ToggleGroupMultiple} {
		if !tp.Valid() {
			t.Errorf("type %q should be valid", tp)
		}
	}
	if ToggleGroupType("radio").Valid() {
		t.Error("radio is not a toggle-group type")
	}
	for _, o := range []ToggleGroupOrientation{ToggleGroupHorizontal, ToggleGroupVertical} {
		if !o.Valid() {
			t.Errorf("orientation %q should be valid", o)
		}
	}
	if ToggleGroupOrientation("diagonal").Valid() {
		t.Error("diagonal is not a toggle-group orientation")
	}

	// Unknown enum values fall back to the documented defaults in the emitted attrs.
	out := renderKids(t, Toggle(ToggleProps{Variant: ToggleVariant("x"), Size: ToggleSize("y")}), "B")
	mustContain(t, out, `data-variant="default"`, `data-size="default"`)
}

// TestCompactBaseMergeAndClass proves the shared Base contract on a compact
// primitive: owned attributes win, a caller class in Attributes is dropped,
// Base.Class appends after the stable class, and Base.ID is honored.
func TestCompactBaseMergeAndClass(t *testing.T) {
	out := renderKids(t, Toggle(ToggleProps{
		Base: Base{
			ID:    "tg-1",
			Class: "mr-2",
			Attributes: templ.Attributes{
				"class":       "sneaky",  // dropped
				"data-slot":   "usurped", // owned wins
				"data-testid": "keep",
			},
		},
	}), "B")
	mustContain(t, out, `id="tg-1"`, `class="goth-toggle mr-2"`, `data-slot="toggle"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", `data-slot="usurped"`)
}
