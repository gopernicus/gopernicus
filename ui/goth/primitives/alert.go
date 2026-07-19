package primitives

import "github.com/a-h/templ"

// AlertVariant is the typed style enum for Alert (P01). The zero value renders
// the default (informational) variant.
type AlertVariant string

const (
	// AlertDefault is the zero value and the documented default.
	AlertDefault     AlertVariant = ""
	AlertDestructive AlertVariant = "destructive"
	AlertSuccess     AlertVariant = "success"
	AlertWarning     AlertVariant = "warning"
)

// Valid reports whether v is a known AlertVariant.
func (v AlertVariant) Valid() bool {
	switch v {
	case AlertDefault, AlertDestructive, AlertSuccess, AlertWarning:
		return true
	default:
		return false
	}
}

func (v AlertVariant) orDefault() AlertVariant {
	if v.Valid() {
		return v
	}
	return AlertDefault
}

func (v AlertVariant) attr() string {
	if v.orDefault() == AlertDefault {
		return "default"
	}
	return string(v.orDefault())
}

// role derives the ARIA role from the variant: an assertive alert for
// destructive/warning, a polite status region otherwise (plan P01 "semantic
// status/alert roles").
func (v AlertVariant) role() string {
	switch v.orDefault() {
	case AlertDestructive, AlertWarning:
		return "alert"
	default:
		return "status"
	}
}

// AlertProps configures Alert (P01, family F2). The principal content is the
// description passed as templ children; Icon, Title, and Action are auxiliary
// slots the primitive arranges in its fixed layout. The zero value is a valid
// default status alert.
type AlertProps struct {
	Base
	Variant AlertVariant
	// Icon is an optional leading status glyph (templ.Component slot).
	Icon templ.Component
	// Title is an optional short heading rendered above the description.
	Title string
	// Action is an optional trailing slot (e.g. a dismiss control or link).
	Action templ.Component
}

func alertClass(p AlertProps) string {
	return classNames("goth-alert", p.Class)
}

func alertAttrs(p AlertProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"role":         p.Variant.role(),
		"data-slot":    "alert",
		"data-variant": p.Variant.attr(),
	})
}
