package primitives

import "github.com/a-h/templ"

// NativeSelect (P18, family F3) is a native <select> with caller-composed
// NativeSelectOption and NativeSelectGroup parts. It submits in an ordinary HTML
// form with no JavaScript. The zero value is a valid empty select. data-slot
// hooks: native-select, native-select-option, native-select-group.
type NativeSelectProps struct {
	Base
	Name     string
	Required bool
	Disabled bool
	// Invalid marks the control invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

// NativeSelectOptionProps configures one <option>. Its label is templ children.
type NativeSelectOptionProps struct {
	Base
	Value    string
	Selected bool
	Disabled bool
}

// NativeSelectGroupProps configures an <optgroup>; its options are templ
// children. Label is the required group label.
type NativeSelectGroupProps struct {
	Base
	Label    string
	Disabled bool
}

func nativeSelectClass(p NativeSelectProps) string {
	return classNames("goth-native-select", p.Class)
}

func nativeSelectAttrs(p NativeSelectProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "native-select"}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Required {
		owned["required"] = true
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

func nativeSelectOptionAttrs(p NativeSelectOptionProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "native-select-option",
		"value":     p.Value,
	}
	if p.Selected {
		owned["selected"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return ownedAttrs(p.Base, owned)
}

func nativeSelectGroupAttrs(p NativeSelectGroupProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "native-select-group",
		"label":     p.Label,
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	return ownedAttrs(p.Base, owned)
}
