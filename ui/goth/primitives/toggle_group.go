package primitives

import "github.com/a-h/templ"

// ToggleGroupType selects single-selection (radio-backed) versus multiple-selection
// (checkbox-backed) items. The zero value is single.
type ToggleGroupType string

const (
	// ToggleGroupSingle is the zero value: exactly one item may be pressed. Items are
	// native radios sharing one Name, so single selection, arrow-key navigation, and
	// roving focus are all native (no controller).
	ToggleGroupSingle ToggleGroupType = ""
	// ToggleGroupMultiple allows any number of items pressed. Items are native
	// checkboxes; the group binds the frozen gothRovingFocus controller for a single
	// tab stop and arrow-key navigation, which native checkboxes lack.
	ToggleGroupMultiple ToggleGroupType = "multiple"
)

// Valid reports whether t is a known ToggleGroupType.
func (t ToggleGroupType) Valid() bool {
	switch t {
	case ToggleGroupSingle, ToggleGroupMultiple:
		return true
	default:
		return false
	}
}

func (t ToggleGroupType) attr() string {
	if t == ToggleGroupMultiple {
		return "multiple"
	}
	return "single"
}

// ToggleGroupOrientation is the ToggleGroup layout/traversal axis. The zero value is
// horizontal.
type ToggleGroupOrientation string

const (
	// ToggleGroupHorizontal is the zero value: a row traversed with ArrowLeft/Right.
	ToggleGroupHorizontal ToggleGroupOrientation = ""
	// ToggleGroupVertical stacks items and traverses with ArrowUp/Down.
	ToggleGroupVertical ToggleGroupOrientation = "vertical"
)

// Valid reports whether o is a known ToggleGroupOrientation.
func (o ToggleGroupOrientation) Valid() bool {
	switch o {
	case ToggleGroupHorizontal, ToggleGroupVertical:
		return true
	default:
		return false
	}
}

func (o ToggleGroupOrientation) attr() string {
	if o == ToggleGroupVertical {
		return "vertical"
	}
	return "horizontal"
}

// ToggleGroupProps configures the ToggleGroup container (P36, catalog family F4). The
// caller composes one ToggleGroupItem per option, passing the group Type to each so
// items render the matching native input. Selection has a submittable no-JS
// representation for both modes: single items are native radios (native single
// selection + roving), multiple items are native checkboxes. Only the multiple mode
// binds gothRovingFocus (single-mode radios already rove natively — binding a second
// handler would double-move, per the P31 Radio Group precedent).
//
// data-slot="toggle-group"; role="group"; data-type single/multiple; data-orientation;
// data-variant/data-size expose the shared Toggle enums. Label the group via
// Base.Attributes["aria-label"]/["aria-labelledby"].
type ToggleGroupProps struct {
	Base
	Type        ToggleGroupType
	Orientation ToggleGroupOrientation
	Variant     ToggleVariant
	Size        ToggleSize
	// Disabled marks the whole group disabled (data-disabled); disable individual
	// items via ToggleGroupItemProps.Disabled so the native inputs are inert.
	Disabled bool
}

// ToggleGroupItemProps configures one ToggleGroupItem. Set Type to the group's Type.
type ToggleGroupItemProps struct {
	Base
	// Type must match the group Type: single renders a radio, multiple a checkbox.
	Type ToggleGroupType
	// Name is the submitted field name. Single-mode items in one group share one Name
	// so the browser enforces single selection; multiple-mode items submit each
	// pressed value.
	Name string
	// Value is the value submitted when the item is pressed.
	Value string
	// Pressed marks the item the server-selected/pressed choice.
	Pressed  bool
	Disabled bool
	Variant  ToggleVariant
	Size     ToggleSize
}

func toggleGroupClass(p ToggleGroupProps) string { return classNames("goth-toggle-group", p.Class) }

func toggleGroupAttrs(p ToggleGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "toggle-group",
		"role":             "group",
		"data-type":        p.Type.attr(),
		"data-orientation": p.Orientation.attr(),
		"data-variant":     p.Variant.attr(),
		"data-size":        p.Size.attr(),
	}
	// Only the multiple (checkbox) mode is enhanced with roving focus; native radios
	// already provide a single tab stop and arrow navigation.
	if p.Type == ToggleGroupMultiple {
		owned["x-data"] = "gothRovingFocus"
		owned["x-on:keydown"] = "onKeydown($event)"
	}
	if p.Disabled {
		owned["data-disabled"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func toggleGroupItemClass(p ToggleGroupItemProps) string {
	return classNames("goth-toggle-group-item", p.Class)
}

func toggleGroupItemAttrs(p ToggleGroupItemProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "toggle-group-item",
		"data-variant": p.Variant.attr(),
		"data-size":    p.Size.attr(),
		"data-state":   onOffState(p.Pressed),
	})
}

func toggleGroupItemInputAttrs(p ToggleGroupItemProps) templ.Attributes {
	inputType := "radio"
	if p.Type == ToggleGroupMultiple {
		inputType = "checkbox"
	}
	// data-slot="item" is the hook gothRovingFocus discovers for the multiple mode.
	owned := templ.Attributes{
		"data-slot": "item",
		"type":      inputType,
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
