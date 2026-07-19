package primitives

import "github.com/a-h/templ"

// InputType is the native input `type` enum for Input (P12). The zero value is
// "text". An unknown value renders "text" (primitives never panic); Valid lets
// callers fail fast.
type InputType string

const (
	// InputText is the zero value and the documented default.
	InputText     InputType = ""
	InputEmail    InputType = "email"
	InputPassword InputType = "password"
	InputSearch   InputType = "search"
	InputTel      InputType = "tel"
	InputURL      InputType = "url"
	InputNumber   InputType = "number"
	InputFile     InputType = "file"
	InputDate     InputType = "date"
	InputTime     InputType = "time"
)

// Valid reports whether t is a known InputType.
func (t InputType) Valid() bool {
	switch t {
	case InputText, InputEmail, InputPassword, InputSearch, InputTel, InputURL,
		InputNumber, InputFile, InputDate, InputTime:
		return true
	default:
		return false
	}
}

func (t InputType) orDefault() InputType {
	if t.Valid() {
		return t
	}
	return InputText
}

func (t InputType) attr() string {
	if t.orDefault() == InputText {
		return "text"
	}
	return string(t.orDefault())
}

// isSecret reports whether the type must never echo a submitted value back into
// the markup (no secret repopulation): a password field.
func (t InputType) isSecret() bool { return t.orDefault() == InputPassword }

// InputProps configures Input (P12, family F1): a native <input>. The zero value
// is a valid empty text input. Native form submission works with no JavaScript.
// data-slot="input" and, when invalid, data-invalid/aria-invalid are the stable
// emitted surface. There is deliberately NO helper that repopulates a password
// value: a password Input never echoes Value back into the markup.
type InputProps struct {
	Base
	Type        InputType
	Name        string
	Value       string
	Placeholder string
	Required    bool
	Disabled    bool
	Readonly    bool
	// Invalid marks the field invalid for styling and assistive tech
	// (data-invalid + aria-invalid="true"). Wire the describing message with
	// aria-describedby via Base.Attributes (see Field).
	Invalid bool
}

func inputClass(p InputProps) string { return classNames("goth-input", p.Class) }

func inputAttrs(p InputProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "input",
		"type":      p.Type.attr(),
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	// Never echo a password value back into the markup (no secret repopulation).
	if p.Value != "" && !p.Type.isSecret() {
		owned["value"] = p.Value
	}
	if p.Placeholder != "" {
		owned["placeholder"] = p.Placeholder
	}
	if p.Required {
		owned["required"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	if p.Readonly {
		owned["readonly"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
