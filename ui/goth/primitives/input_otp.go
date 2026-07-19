package primitives

import "github.com/a-h/templ"

// InputOTPProps configures the InputOTP container (P30, catalog family F4). The
// caller composes one InputOTPSlot per character and optional InputOTPSeparator
// parts. The baseline is a group of native single-character <input> elements:
// each slot accepts one character, native Tab/Shift-Tab move between slots, native
// Backspace clears within a slot, and every slot submits its value in an ordinary
// HTML form with NO JavaScript. autocomplete="one-time-code" (set on the first
// slot) lets the browser offer an SMS/authenticator code.
//
// Auto-advance and paste-distribution across slots are a deliberate deferral: they
// require a named controller (new runtime surface), which is out of this
// milestone's frozen controller set, so no controller is bound here. Native paste
// into a slot and native navigation are the shipped interaction model.
// data-slot="input-otp"; the container is role="group" — label it via
// Base.Attributes["aria-label"]/["aria-labelledby"].
type InputOTPProps struct {
	Base
}

// InputOTPSlotProps configures one InputOTPSlot: a native single-character
// <input maxlength="1" inputmode="numeric">. It submits its value natively.
type InputOTPSlotProps struct {
	Base
	// Name is the submitted field name. Slots in one code commonly share a Name so
	// the browser submits the code as an ordered field list.
	Name string
	// Value is the server-rendered character (submitted). OTP validation specimens
	// must NOT echo an entered code back here.
	Value string
	// Autocomplete adds autocomplete="one-time-code"; set it on the first slot so
	// the browser can offer a received code.
	Autocomplete bool
	Disabled     bool
	// Invalid marks the slot invalid (data-invalid + aria-invalid="true").
	Invalid bool
}

// InputOTPSeparatorProps configures a decorative separator between OTP groups. It
// is aria-hidden and conveys no state.
type InputOTPSeparatorProps struct {
	Base
}

func inputOTPClass(p InputOTPProps) string { return classNames("goth-input-otp", p.Class) }

func inputOTPAttrs(p InputOTPProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot": "input-otp",
		"role":      "group",
	})
}

func inputOTPSlotClass(p InputOTPSlotProps) string { return classNames("goth-input-otp-slot", p.Class) }

func inputOTPSlotAttrs(p InputOTPSlotProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "input-otp-slot",
		"type":      "text",
		"inputmode": "numeric",
		"maxlength": "1",
	}
	if p.Autocomplete {
		owned["autocomplete"] = "one-time-code"
	}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Value != "" {
		owned["value"] = p.Value
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

func inputOTPSeparatorClass(p InputOTPSeparatorProps) string {
	return classNames("goth-input-otp-separator", p.Class)
}

func inputOTPSeparatorAttrs(p InputOTPSeparatorProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "input-otp-separator",
		"aria-hidden": "true",
	})
}
