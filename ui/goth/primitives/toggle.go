package primitives

import "github.com/a-h/templ"

// ToggleVariant is the typed style enum for Toggle (P35) and ToggleGroupItem (P36).
// The zero value renders the documented default (transparent, hover-highlighted)
// variant.
type ToggleVariant string

const (
	// ToggleDefault is the zero value: transparent with a hover highlight.
	ToggleDefault ToggleVariant = ""
	// ToggleOutline draws a bordered variant.
	ToggleOutline ToggleVariant = "outline"
)

// Valid reports whether v is a known ToggleVariant.
func (v ToggleVariant) Valid() bool {
	switch v {
	case ToggleDefault, ToggleOutline:
		return true
	default:
		return false
	}
}

func (v ToggleVariant) attr() string {
	if v == ToggleOutline {
		return "outline"
	}
	return "default"
}

// ToggleSize is the typed size enum for Toggle (P35) and ToggleGroupItem (P36).
// The zero value is the default size.
type ToggleSize string

const (
	// ToggleSizeDefault is the zero value and the documented default.
	ToggleSizeDefault ToggleSize = ""
	ToggleSizeSmall   ToggleSize = "sm"
	ToggleSizeLarge   ToggleSize = "lg"
)

// Valid reports whether s is a known ToggleSize.
func (s ToggleSize) Valid() bool {
	switch s {
	case ToggleSizeDefault, ToggleSizeSmall, ToggleSizeLarge:
		return true
	default:
		return false
	}
}

func (s ToggleSize) attr() string {
	switch s {
	case ToggleSizeSmall:
		return "sm"
	case ToggleSizeLarge:
		return "lg"
	default:
		return "default"
	}
}

// ToggleProps configures Toggle (P35, catalog family F4): a two-state toggle button.
// It is CHECKBOX-BACKED (the frozen Switch/Toggle asymmetry, README §7): the button
// is a native <input type="checkbox"> wrapped in the styled <label>, so the pressed
// state submits in an ordinary HTML form and toggles with the native Space key and
// pointer with NO JavaScript — no standalone controller is bound. The pressed
// visual is drawn by component CSS off the input's :checked pseudo-class; the
// accessible pressed state is the native checkbox's checked state. Its Toggle Group
// (P36) sibling adds roving focus via the frozen gothRovingFocus controller.
//
// data-slot="toggle"; data-state is checked / unchecked (the checkbox-backed pressed
// state); data-variant and data-size expose the frozen enums. The zero value is a
// valid, unpressed default toggle.
type ToggleProps struct {
	Base
	// Name is the submitted field name.
	Name string
	// Value is the value submitted when pressed. Empty leaves the native default
	// ("on").
	Value string
	// Pressed renders the toggle on (native checked attribute, submitted).
	Pressed  bool
	Disabled bool
	Variant  ToggleVariant
	Size     ToggleSize
}

func toggleClass(p ToggleProps) string { return classNames("goth-toggle", p.Class) }

func toggleAttrs(p ToggleProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "toggle",
		"data-variant": p.Variant.attr(),
		"data-size":    p.Size.attr(),
		"data-state":   onOffState(p.Pressed),
	})
}

func toggleInputAttrs(p ToggleProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "toggle-input",
		"type":      "checkbox",
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
	}
	if p.Pressed {
		owned["checked"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return owned
}
