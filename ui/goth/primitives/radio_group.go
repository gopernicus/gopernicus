package primitives

import "github.com/a-h/templ"

// RadioOrientation is the RadioGroup layout axis. The zero value is vertical.
type RadioOrientation string

const (
	// RadioVertical is the zero value: items stack vertically.
	RadioVertical RadioOrientation = ""
	// RadioHorizontal lays items out in a row.
	RadioHorizontal RadioOrientation = "horizontal"
)

// Valid reports whether o is a known RadioOrientation.
func (o RadioOrientation) Valid() bool {
	switch o {
	case RadioVertical, RadioHorizontal:
		return true
	default:
		return false
	}
}

func (o RadioOrientation) attr() string {
	if o == RadioHorizontal {
		return "horizontal"
	}
	return "vertical"
}

// RadioGroupProps configures the RadioGroup container (P31). The caller composes a
// RadioGroupItem per choice; each item is a native <input type="radio"> sharing one
// Name. The native radio group is the complete widget: it submits with no
// JavaScript AND provides single-selection, arrow-key navigation, and roving focus
// natively, so no controller is bound. The container is role="radiogroup"; label it
// by pointing Base.Attributes["aria-labelledby"] at a heading/label id. data-slot
// hooks: radio-group, radio-group-item.
type RadioGroupProps struct {
	Base
	Orientation RadioOrientation
	// Disabled marks the whole group disabled (data-disabled); disable individual
	// inputs via RadioGroupItemProps.Disabled so the native controls are inert.
	Disabled bool
}

// RadioGroupItemProps configures one RadioGroupItem: a native <input type="radio">.
type RadioGroupItemProps struct {
	Base
	// Name is the shared radio group name; every item in one group uses the same
	// Name so the browser enforces single selection.
	Name string
	// Value is the value submitted when this item is selected.
	Value string
	// Checked marks this item the server-selected choice.
	Checked  bool
	Disabled bool
	Required bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

func radioGroupClass(p RadioGroupProps) string { return classNames("goth-radio-group", p.Class) }

func radioGroupAttrs(p RadioGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "radio-group",
		"role":             "radiogroup",
		"data-orientation": p.Orientation.attr(),
	}
	if p.Disabled {
		owned["data-disabled"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func radioGroupItemClass(p RadioGroupItemProps) string {
	return classNames("goth-radio-group-item", p.Class)
}

func radioGroupItemAttrs(p RadioGroupItemProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "radio-group-item",
		"type":       "radio",
		"data-state": onOffState(p.Checked),
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
	}
	if p.Checked {
		owned["checked"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	if p.Required {
		owned["required"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
