package primitives

import "github.com/a-h/templ"

// TypographyVariant selects a semantic text recipe for Typography (P26). Heading
// variants render the matching native heading element, so the recipe never
// replaces the document's heading levels. The zero value is a paragraph.
type TypographyVariant string

const (
	// TypographyP is the zero value: a paragraph recipe.
	TypographyP          TypographyVariant = ""
	TypographyH1         TypographyVariant = "h1"
	TypographyH2         TypographyVariant = "h2"
	TypographyH3         TypographyVariant = "h3"
	TypographyH4         TypographyVariant = "h4"
	TypographyLead       TypographyVariant = "lead"
	TypographyLarge      TypographyVariant = "large"
	TypographySmall      TypographyVariant = "small"
	TypographyMuted      TypographyVariant = "muted"
	TypographyBlockquote TypographyVariant = "blockquote"
	TypographyCode       TypographyVariant = "code"
	TypographyList       TypographyVariant = "list"
)

// Valid reports whether v is a known TypographyVariant.
func (v TypographyVariant) Valid() bool {
	switch v {
	case TypographyP, TypographyH1, TypographyH2, TypographyH3, TypographyH4,
		TypographyLead, TypographyLarge, TypographySmall, TypographyMuted,
		TypographyBlockquote, TypographyCode, TypographyList:
		return true
	default:
		return false
	}
}

func (v TypographyVariant) orDefault() TypographyVariant {
	if v.Valid() {
		return v
	}
	return TypographyP
}

func (v TypographyVariant) attr() string {
	if v.orDefault() == TypographyP {
		return "p"
	}
	return string(v.orDefault())
}

// TypographyProps configures Typography (P26, family F2): a semantic text recipe
// wrapping a principal content region passed as templ children. The zero value
// renders a paragraph.
type TypographyProps struct {
	Base
	Variant TypographyVariant
}

func typographyClass(p TypographyProps) string { return classNames("goth-typography", p.Class) }

func typographyAttrs(p TypographyProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "typography",
		"data-variant": p.Variant.attr(),
	})
}
