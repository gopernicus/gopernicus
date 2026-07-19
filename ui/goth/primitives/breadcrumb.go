package primitives

import "github.com/a-h/templ"

// Breadcrumb (P05, family F3) is a compound the caller composes: a labelled nav
// wrapping an ordered list of BreadcrumbItem parts, each holding either a
// BreadcrumbLink (a prior page) or a BreadcrumbPage (the current page, not a
// link). BreadcrumbSeparator sits between items and BreadcrumbEllipsis is the
// responsive-collapse slot for hidden middle items. The zero value of each Props
// is valid. data-slot hooks: breadcrumb, breadcrumb-list, breadcrumb-item,
// breadcrumb-link, breadcrumb-page, breadcrumb-separator, breadcrumb-ellipsis.
type BreadcrumbProps struct {
	Base
	// Label is the nav's accessible name. Empty defaults to "Breadcrumb".
	Label string
}

// BreadcrumbListProps configures BreadcrumbList (the ordered list of items).
type BreadcrumbListProps struct{ Base }

// BreadcrumbItemProps configures BreadcrumbItem (one list item).
type BreadcrumbItemProps struct{ Base }

// BreadcrumbLinkProps configures BreadcrumbLink, a link to a prior page. URL is
// the validated destination; the visible label is templ children.
type BreadcrumbLinkProps struct {
	Base
	URL URL
}

// BreadcrumbPageProps configures BreadcrumbPage, the current page. It is not a
// link: it renders aria-current="page" so assistive technology announces the
// user's position.
type BreadcrumbPageProps struct{ Base }

// BreadcrumbSeparatorProps configures BreadcrumbSeparator. It is presentational
// (aria-hidden). When Icon is nil a default chevron glyph is drawn; set Icon to
// override the separator glyph.
type BreadcrumbSeparatorProps struct {
	Base
	Icon templ.Component
}

// BreadcrumbEllipsisProps configures BreadcrumbEllipsis, the collapsed-items
// indicator. It renders an aria-hidden glyph plus a visually-hidden "More" label.
type BreadcrumbEllipsisProps struct{ Base }

func (p BreadcrumbProps) label() string {
	if p.Label == "" {
		return "Breadcrumb"
	}
	return p.Label
}

func breadcrumbClass(p BreadcrumbProps) string { return classNames("goth-breadcrumb", p.Class) }

func breadcrumbAttrs(p BreadcrumbProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "breadcrumb",
		"aria-label": p.label(),
	})
}

func breadcrumbListClass(p BreadcrumbListProps) string {
	return classNames("goth-breadcrumb-list", p.Class)
}

func breadcrumbListAttrs(p BreadcrumbListProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "breadcrumb-list"})
}

func breadcrumbItemClass(p BreadcrumbItemProps) string {
	return classNames("goth-breadcrumb-item", p.Class)
}

func breadcrumbItemAttrs(p BreadcrumbItemProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "breadcrumb-item"})
}

func breadcrumbLinkClass(p BreadcrumbLinkProps) string {
	return classNames("goth-breadcrumb-link", p.Class)
}

func breadcrumbLinkAttrs(p BreadcrumbLinkProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "breadcrumb-link"})
}

func breadcrumbPageClass(p BreadcrumbPageProps) string {
	return classNames("goth-breadcrumb-page", p.Class)
}

func breadcrumbPageAttrs(p BreadcrumbPageProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "breadcrumb-page",
		"role":         "link",
		"aria-disabled": "true",
		"aria-current": "page",
	})
}

func breadcrumbSeparatorClass(p BreadcrumbSeparatorProps) string {
	return classNames("goth-breadcrumb-separator", p.Class)
}

func breadcrumbSeparatorAttrs(p BreadcrumbSeparatorProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "breadcrumb-separator",
		"role":        "presentation",
		"aria-hidden": "true",
	})
}

func breadcrumbEllipsisClass(p BreadcrumbEllipsisProps) string {
	return classNames("goth-breadcrumb-ellipsis", p.Class)
}

func breadcrumbEllipsisAttrs(p BreadcrumbEllipsisProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "breadcrumb-ellipsis",
		"role":        "presentation",
		"aria-hidden": "true",
	})
}
