package primitives

import "github.com/a-h/templ"

// Pagination (P19, family F3) is a compound the caller composes: a labelled nav
// wrapping a list of PaginationItem parts, each holding a PaginationLink (a page
// number), PaginationPrevious/PaginationNext (edge links), or PaginationEllipsis
// (a collapsed-range indicator). Page state is server-owned: every link is a real
// URL the server produces, so pagination works with no JavaScript. The zero value
// of each Props is valid. data-slot hooks: pagination, pagination-content,
// pagination-item, pagination-link, pagination-previous, pagination-next,
// pagination-ellipsis.
type PaginationProps struct {
	Base
	// Label is the nav's accessible name. Empty defaults to "Pagination".
	Label string
}

// PaginationContentProps configures PaginationContent (the list of items).
type PaginationContentProps struct{ Base }

// PaginationItemProps configures PaginationItem (one list item).
type PaginationItemProps struct{ Base }

// PaginationLinkProps configures PaginationLink, a link to a page. URL is the
// validated destination; the visible page label is templ children. Active marks
// the current page (aria-current="page"). Label is the accessible name for a
// link whose visible content is not self-describing.
type PaginationLinkProps struct {
	Base
	URL    URL
	Active bool
	Label  string
}

// PaginationPreviousProps / PaginationNextProps configure the edge links. URL is
// the destination; Label overrides the default visible/accessible text.
type PaginationPreviousProps struct {
	Base
	URL   URL
	Label string
}

// PaginationNextProps configures the next-page edge link.
type PaginationNextProps struct {
	Base
	URL   URL
	Label string
}

// PaginationEllipsisProps configures PaginationEllipsis, the collapsed-range
// indicator. It renders an aria-hidden glyph plus a visually-hidden label.
type PaginationEllipsisProps struct{ Base }

func (p PaginationProps) label() string {
	if p.Label == "" {
		return "Pagination"
	}
	return p.Label
}

func (p PaginationPreviousProps) label() string {
	if p.Label == "" {
		return "Previous"
	}
	return p.Label
}

func (p PaginationNextProps) label() string {
	if p.Label == "" {
		return "Next"
	}
	return p.Label
}

func paginationClass(p PaginationProps) string { return classNames("goth-pagination", p.Class) }

func paginationAttrs(p PaginationProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "pagination",
		"role":       "navigation",
		"aria-label": p.label(),
	})
}

func paginationContentClass(p PaginationContentProps) string {
	return classNames("goth-pagination-content", p.Class)
}

func paginationContentAttrs(p PaginationContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "pagination-content"})
}

func paginationItemClass(p PaginationItemProps) string {
	return classNames("goth-pagination-item", p.Class)
}

func paginationItemAttrs(p PaginationItemProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "pagination-item"})
}

func paginationLinkClass(p PaginationLinkProps) string {
	return classNames("goth-pagination-link", p.Class)
}

func paginationLinkAttrs(p PaginationLinkProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "pagination-link"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	if p.Active {
		owned["aria-current"] = "page"
		owned["data-active"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func paginationPreviousClass(p PaginationPreviousProps) string {
	return classNames("goth-pagination-link goth-pagination-previous", p.Class)
}

func paginationPreviousAttrs(p PaginationPreviousProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "pagination-previous",
		"aria-label": p.label(),
	})
}

func paginationNextClass(p PaginationNextProps) string {
	return classNames("goth-pagination-link goth-pagination-next", p.Class)
}

func paginationNextAttrs(p PaginationNextProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "pagination-next",
		"aria-label": p.label(),
	})
}

func paginationEllipsisClass(p PaginationEllipsisProps) string {
	return classNames("goth-pagination-ellipsis", p.Class)
}

func paginationEllipsisAttrs(p PaginationEllipsisProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "pagination-ellipsis",
		"role":        "presentation",
		"aria-hidden": "true",
	})
}
