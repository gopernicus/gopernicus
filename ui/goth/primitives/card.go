package primitives

import "github.com/a-h/templ"

// Card (P08, family F3) is a compound of caller-composed parts: Card, CardHeader,
// CardTitle, CardDescription, CardAction, CardContent, CardFooter. Each part
// takes a Props value with the shared Base and renders its children. The zero
// value of each Props is valid.
type CardProps struct{ Base }

// CardHeaderProps configures CardHeader.
type CardHeaderProps struct{ Base }

// CardTitleProps configures CardTitle. It renders a div (not a fixed heading
// level) so the caller owns the document outline.
type CardTitleProps struct{ Base }

// CardDescriptionProps configures CardDescription.
type CardDescriptionProps struct{ Base }

// CardActionProps configures CardAction, a header-aligned action slot.
type CardActionProps struct{ Base }

// CardContentProps configures CardContent.
type CardContentProps struct{ Base }

// CardFooterProps configures CardFooter.
type CardFooterProps struct{ Base }

func cardPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func cardPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}
