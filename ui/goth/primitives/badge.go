package primitives

import "github.com/a-h/templ"

// BadgeVariant is the typed style enum for Badge (P04). The zero value renders
// the documented default variant.
type BadgeVariant string

const (
	// BadgeDefault is the zero value and the documented default.
	BadgeDefault     BadgeVariant = ""
	BadgeSecondary   BadgeVariant = "secondary"
	BadgeDestructive BadgeVariant = "destructive"
	BadgeOutline     BadgeVariant = "outline"
)

// Valid reports whether v is a known BadgeVariant. An unknown value renders the
// default (primitives never panic); Valid lets callers fail fast.
func (v BadgeVariant) Valid() bool {
	switch v {
	case BadgeDefault, BadgeSecondary, BadgeDestructive, BadgeOutline:
		return true
	default:
		return false
	}
}

func (v BadgeVariant) orDefault() BadgeVariant {
	if v.Valid() {
		return v
	}
	return BadgeDefault
}

func (v BadgeVariant) attr() string {
	if v.orDefault() == BadgeDefault {
		return "default"
	}
	return string(v.orDefault())
}

// BadgeProps configures Badge (P04, family F1). The zero value is a valid default
// badge. When URL is set the badge renders as an anchor (a link styled as a
// badge) rather than a span, without nesting an interactive element.
type BadgeProps struct {
	Base
	Variant BadgeVariant
	// URL, when non-zero, renders the badge as an <a> link.
	URL URL
}

func badgeClass(p BadgeProps) string {
	return classNames("goth-badge", p.Class)
}

func badgeAttrs(p BadgeProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "badge",
		"data-variant": p.Variant.attr(),
	})
}
