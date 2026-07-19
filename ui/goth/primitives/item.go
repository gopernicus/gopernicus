package primitives

import "github.com/a-h/templ"

// ItemVariant is the typed style enum for Item (P14). The zero value is the
// default (flat) variant.
type ItemVariant string

const (
	// ItemDefault is the zero value and the documented default.
	ItemDefault ItemVariant = ""
	ItemOutline ItemVariant = "outline"
	ItemMuted   ItemVariant = "muted"
)

// Valid reports whether v is a known ItemVariant.
func (v ItemVariant) Valid() bool {
	switch v {
	case ItemDefault, ItemOutline, ItemMuted:
		return true
	default:
		return false
	}
}

func (v ItemVariant) orDefault() ItemVariant {
	if v.Valid() {
		return v
	}
	return ItemDefault
}

func (v ItemVariant) attr() string {
	if v.orDefault() == ItemDefault {
		return "default"
	}
	return string(v.orDefault())
}

// ItemProps configures the Item container (P14, family F3). The caller composes
// ItemMedia, ItemContent (with ItemTitle/ItemDescription), and ItemActions as
// children. When URL is set the Item renders as an anchor (a whole-row link);
// otherwise a div. The zero value is a valid default item.
type ItemProps struct {
	Base
	Variant ItemVariant
	// URL, when non-zero, renders the item as a whole-row <a> link.
	URL URL
}

// ItemMediaProps configures ItemMedia (leading avatar/icon slot).
type ItemMediaProps struct{ Base }

// ItemContentProps configures ItemContent (the title/description column).
type ItemContentProps struct{ Base }

// ItemTitleProps configures ItemTitle.
type ItemTitleProps struct{ Base }

// ItemDescriptionProps configures ItemDescription.
type ItemDescriptionProps struct{ Base }

// ItemActionsProps configures ItemActions (trailing controls slot).
type ItemActionsProps struct{ Base }

func itemClass(p ItemProps) string { return classNames("goth-item", p.Class) }

func itemAttrs(p ItemProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "item",
		"data-variant": p.Variant.attr(),
	})
}

func itemPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func itemPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}
