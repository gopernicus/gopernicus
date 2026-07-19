package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// Default slider bounds mirror the native <input type="range"> defaults.
const (
	sliderDefaultMin  = 0
	sliderDefaultMax  = 100
	sliderDefaultStep = 1
)

// SliderProps configures Slider (P32): a single-thumb native <input type="range">.
// It submits its value in an ordinary HTML form with no JavaScript and carries the
// native range keyboard model (Arrow keys, Home/End, PageUp/PageDown) and native
// focus, so no controller is bound. The track/thumb are drawn by component CSS off
// the appearance-none input. The zero value is a valid 0–100 slider at 0.
//
// Multi-thumb (range) selection is intentionally out of scope: it cannot be a
// native single <input type="range"> and would require a dedicated controller
// (new runtime surface). The frozen policy for this milestone is single-thumb
// native only; a paired <output> shows the value (see the showcase specimen).
type SliderProps struct {
	Base
	// Name is the submitted field name.
	Name string
	// Min/Max bound the slider. A zero Max is treated as the native default 100 so
	// the zero value is a usable 0–100 range.
	Min int
	Max int
	// Step is the increment; a zero Step is treated as 1 (the native default).
	Step int
	// Value is the current thumb value (submitted). It is clamped natively.
	Value int
	Disabled bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

func (p SliderProps) maxOrDefault() int {
	if p.Max == 0 {
		return sliderDefaultMax
	}
	return p.Max
}

func (p SliderProps) stepOrDefault() int {
	if p.Step <= 0 {
		return sliderDefaultStep
	}
	return p.Step
}

func sliderClass(p SliderProps) string { return classNames("goth-slider", p.Class) }

func sliderAttrs(p SliderProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "slider",
		"type":      "range",
		"min":       strconv.Itoa(p.Min),
		"max":       strconv.Itoa(p.maxOrDefault()),
		"step":      strconv.Itoa(p.stepOrDefault()),
		"value":     strconv.Itoa(p.Value),
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
