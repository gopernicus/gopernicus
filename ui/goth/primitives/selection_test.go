package primitives

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestNoSelectionPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-3.2
// form-selection primitive emits an inline style= in any state — the styled
// surfaces are drawn by component CSS off :checked / data-state, never style.
func TestNoSelectionPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		render(t, Checkbox(CheckboxProps{Name: "a", Checked: true})),
		render(t, Checkbox(CheckboxProps{Indeterminate: true})),
		render(t, Switch(SwitchProps{Name: "s", Checked: true})),
		renderKids(t, RadioGroup(RadioGroupProps{Orientation: RadioHorizontal}), "x"),
		render(t, RadioGroupItem(RadioGroupItemProps{Name: "g", Value: "a", Checked: true})),
		render(t, Slider(SliderProps{Name: "vol", Value: 40})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("selection primitive emitted an inline style=: %s", o)
		}
	}
}

// TestCheckboxNativeSubmission proves Checkbox is a native checkbox that submits
// with no JavaScript, exposes the three data-state values, and never binds a
// controller.
func TestCheckboxNativeSubmission(t *testing.T) {
	// Unchecked default.
	def := render(t, Checkbox(CheckboxProps{Name: "terms", Value: "yes"}))
	mustContain(t, def, `<input`, `class="goth-checkbox"`, `data-slot="checkbox"`, `type="checkbox"`, `name="terms"`, `value="yes"`, `data-state="unchecked"`)
	mustNotContain(t, def, ` checked`, `x-data`, `aria-checked`)

	// Checked submits (native checked attribute).
	on := render(t, Checkbox(CheckboxProps{Name: "terms", Checked: true}))
	mustContain(t, on, ` checked`, `data-state="checked"`)

	// Indeterminate is visual-only: data-state="indeterminate", NOT checked, and no
	// aria-checked (which would conflict with the native checkbox state).
	ind := render(t, Checkbox(CheckboxProps{Name: "all", Indeterminate: true}))
	mustContain(t, ind, `data-state="indeterminate"`)
	mustNotContain(t, ind, ` checked`, `aria-checked`)

	// Invalid + disabled + required native attributes.
	full := render(t, Checkbox(CheckboxProps{Invalid: true, Disabled: true, Required: true}))
	mustContain(t, full, `aria-invalid="true"`, `data-invalid="true"`, `disabled`, `required`)
}

// TestSwitchNativeCheckbox proves Switch is a native checkbox carrying
// role="switch" that submits with no JavaScript and binds no controller.
func TestSwitchNativeCheckbox(t *testing.T) {
	off := render(t, Switch(SwitchProps{Name: "notify"}))
	mustContain(t, off, `<input`, `class="goth-switch"`, `data-slot="switch"`, `type="checkbox"`, `role="switch"`, `name="notify"`, `data-state="unchecked"`)
	mustNotContain(t, off, ` checked`, `x-data`)

	on := render(t, Switch(SwitchProps{Name: "notify", Value: "1", Checked: true}))
	mustContain(t, on, ` checked`, `value="1"`, `data-state="checked"`)

	full := render(t, Switch(SwitchProps{Disabled: true, Invalid: true, Required: true}))
	mustContain(t, full, `disabled`, `aria-invalid="true"`, `data-invalid="true"`, `required`)
}

// TestRadioGroupNative proves the group is a role="radiogroup" of native radios
// sharing one Name (native single-selection / arrow navigation / roving focus, no
// controller) and that items submit natively.
func TestRadioGroupNative(t *testing.T) {
	group := renderKids(t, RadioGroup(RadioGroupProps{}), "x")
	mustContain(t, group, `<div`, `class="goth-radio-group"`, `data-slot="radio-group"`, `role="radiogroup"`, `data-orientation="vertical"`)
	mustNotContain(t, group, `x-data`)

	horiz := renderKids(t, RadioGroup(RadioGroupProps{Orientation: RadioHorizontal, Disabled: true}), "x")
	mustContain(t, horiz, `data-orientation="horizontal"`, `data-disabled="true"`)

	// Unknown orientation falls back to vertical.
	def := renderKids(t, RadioGroup(RadioGroupProps{Orientation: RadioOrientation("diag")}), "x")
	mustContain(t, def, `data-orientation="vertical"`)

	// Selected item: native radio, shared name, checked, submits.
	on := render(t, RadioGroupItem(RadioGroupItemProps{Base: Base{ID: "r-a"}, Name: "plan", Value: "pro", Checked: true}))
	mustContain(t, on, `<input`, `id="r-a"`, `class="goth-radio-group-item"`, `data-slot="radio-group-item"`, `type="radio"`, `name="plan"`, `value="pro"`, ` checked`, `data-state="checked"`)

	off := render(t, RadioGroupItem(RadioGroupItemProps{Name: "plan", Value: "free", Disabled: true}))
	mustContain(t, off, `data-state="unchecked"`, `disabled`, `value="free"`)
	mustNotContain(t, off, ` checked`)
}

func TestRadioOrientationValid(t *testing.T) {
	for _, o := range []RadioOrientation{RadioVertical, RadioHorizontal} {
		if !o.Valid() {
			t.Errorf("orientation %q should be valid", o)
		}
	}
	if RadioOrientation("diagonal").Valid() {
		t.Error("diagonal should be invalid")
	}
}

// TestSliderNativeRange proves Slider is a native <input type="range"> with
// deterministic min/max/step/value that submits with no JavaScript and binds no
// controller.
func TestSliderNativeRange(t *testing.T) {
	// Zero value: usable 0–100 range at 0, step 1.
	def := render(t, Slider(SliderProps{Name: "vol"}))
	mustContain(t, def, `<input`, `class="goth-slider"`, `data-slot="slider"`, `type="range"`, `name="vol"`, `min="0"`, `max="100"`, `step="1"`, `value="0"`)
	mustNotContain(t, def, `x-data`)

	// Explicit bounds are emitted verbatim.
	custom := render(t, Slider(SliderProps{Name: "temp", Min: 10, Max: 30, Step: 5, Value: 20}))
	mustContain(t, custom, `min="10"`, `max="30"`, `step="5"`, `value="20"`)

	full := render(t, Slider(SliderProps{Disabled: true, Invalid: true}))
	mustContain(t, full, `disabled`, `aria-invalid="true"`, `data-invalid="true"`)
}

// TestSelectionBaseMergeAndClass proves the shared Base contract on a
// form-selection primitive: owned attributes win, a caller class in Attributes is
// dropped, Base.Class appends after the stable class, and Base.ID is honored.
func TestSelectionBaseMergeAndClass(t *testing.T) {
	out := render(t, Checkbox(CheckboxProps{
		Base: Base{
			ID:    "cb-1",
			Class: "mt-2",
			Attributes: templ.Attributes{
				"class":       "sneaky",  // dropped
				"type":        "text",    // owned wins
				"data-testid": "keep",
			},
		},
		Name: "x",
	}))
	mustContain(t, out, `id="cb-1"`, `class="goth-checkbox mt-2"`, `type="checkbox"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", `type="text"`)
}
