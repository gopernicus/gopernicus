package showcase

import (
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/primitives"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// This file holds the showcase specimens for the GOTH-2.2 action/navigation
// primitives (P05 Breadcrumb, P06 Button, P07 Button Group, P09 Direction,
// P19 Pagination, P21 Separator). Each specimen renders the real primitive
// component so the browser/axe harness exercises the actual emitted surface. All
// six are presentational (StylesOnly profile — native links/buttons, no JS).

func breadcrumbSpecimen() string {
	docs := mustURL("/specimen/primitive-breadcrumb")
	crumb := compKids(primitives.Breadcrumb(primitives.BreadcrumbProps{}),
		compKids(primitives.BreadcrumbList(primitives.BreadcrumbListProps{}),
			compKids(primitives.BreadcrumbItem(primitives.BreadcrumbItemProps{}),
				compKids(primitives.BreadcrumbLink(primitives.BreadcrumbLinkProps{URL: docs}), "Home"))+
				comp(primitives.BreadcrumbSeparator(primitives.BreadcrumbSeparatorProps{}))+
				compKids(primitives.BreadcrumbItem(primitives.BreadcrumbItemProps{}),
					comp(primitives.BreadcrumbEllipsis(primitives.BreadcrumbEllipsisProps{})))+
				comp(primitives.BreadcrumbSeparator(primitives.BreadcrumbSeparatorProps{}))+
				compKids(primitives.BreadcrumbItem(primitives.BreadcrumbItemProps{}),
					compKids(primitives.BreadcrumbLink(primitives.BreadcrumbLinkProps{URL: docs}), "Components"))+
				comp(primitives.BreadcrumbSeparator(primitives.BreadcrumbSeparatorProps{}))+
				compKids(primitives.BreadcrumbItem(primitives.BreadcrumbItemProps{}),
					compKids(primitives.BreadcrumbPage(primitives.BreadcrumbPageProps{}), "Breadcrumb"))))
	return page("Breadcrumb", `<section data-slot="breadcrumbs">`+crumb+`</section>`)
}

func buttonSpecimen() string {
	closeIcon := `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18"></path><path d="m6 6 12 12"></path></svg>`
	var b strings.Builder
	for _, v := range []primitives.ButtonVariant{
		primitives.ButtonDefault, primitives.ButtonSecondary, primitives.ButtonDestructive,
		primitives.ButtonOutline, primitives.ButtonGhost, primitives.ButtonLink,
	} {
		b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Variant: v}), string(v)+" button"))
		b.WriteString(" ")
	}
	b.WriteString("<br>")
	// Sizes.
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Size: primitives.ButtonSizeSmall}), "Small"))
	b.WriteString(" ")
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Size: primitives.ButtonSizeLarge}), "Large"))
	b.WriteString(" ")
	// Icon-only requires an accessible name (Label → aria-label).
	b.WriteString(comp(primitives.Button(primitives.ButtonProps{
		Size:        primitives.ButtonIcon,
		Variant:     primitives.ButtonOutline,
		Label:       "Close",
		LeadingIcon: templ.Raw(closeIcon),
	})))
	b.WriteString(" ")
	// Link form (renders an anchor styled as a button).
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{URL: mustURL("/specimen/primitive-button")}), "Link button"))
	b.WriteString("<br>")
	// States.
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Disabled: true}), "Disabled"))
	b.WriteString(" ")
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Loading: true}), "Saving…"))
	return page("Button", `<section data-slot="buttons">`+b.String()+`</section>`)
}

func buttonGroupSpecimen() string {
	horizontal := compKids(primitives.ButtonGroup(primitives.ButtonGroupProps{}),
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Left")+
			compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Center")+
			compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Right"))
	withAddon := compKids(primitives.ButtonGroup(primitives.ButtonGroupProps{}),
		compKids(primitives.ButtonGroupText(primitives.ButtonGroupTextProps{}), "https://")+
			compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Copy"))
	vertical := compKids(primitives.ButtonGroup(primitives.ButtonGroupProps{Orientation: primitives.OrientationVertical}),
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Top")+
			compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Middle")+
			compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Bottom"))
	return page("Button Group", `<section data-slot="button-groups"><p>`+horizontal+`</p><p>`+withAddon+`</p><p>`+vertical+`</p></section>`)
}

func directionSpecimen() string {
	rtl := compKids(primitives.Direction(primitives.DirectionProps{Dir: theme.DirectionRTL}),
		`<p class="goth-typography" data-variant="p">مرحبا بك في جوث — this region propagates dir="rtl" to its subtree.</p>`)
	ltr := compKids(primitives.Direction(primitives.DirectionProps{Dir: theme.DirectionLTR}),
		`<p class="goth-typography" data-variant="p">This region propagates dir="ltr" to its subtree.</p>`)
	return page("Direction", `<section data-slot="directions">`+rtl+ltr+`</section>`)
}

func paginationSpecimen() string {
	link := func(n string, active bool) string {
		return compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
			compKids(primitives.PaginationLink(primitives.PaginationLinkProps{
				URL:    mustURL("/specimen/primitive-pagination"),
				Active: active,
				Label:  "Go to page " + n,
			}), n))
	}
	content := compKids(primitives.PaginationContent(primitives.PaginationContentProps{}),
		compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
			comp(primitives.PaginationPrevious(primitives.PaginationPreviousProps{URL: mustURL("/specimen/primitive-pagination")})))+
			link("1", false)+
			link("2", true)+
			link("3", false)+
			compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
				comp(primitives.PaginationEllipsis(primitives.PaginationEllipsisProps{})))+
			link("10", false)+
			compKids(primitives.PaginationItem(primitives.PaginationItemProps{}),
				comp(primitives.PaginationNext(primitives.PaginationNextProps{URL: mustURL("/specimen/primitive-pagination")}))))
	pag := compKids(primitives.Pagination(primitives.PaginationProps{}), content)
	return page("Pagination", `<section data-slot="paginations">`+pag+`</section>`)
}

func separatorSpecimen() string {
	horizontal := `<p>Above</p>` +
		comp(primitives.Separator(primitives.SeparatorProps{})) +
		`<p>Below</p>`
	semantic := `<p>Section one</p>` +
		comp(primitives.Separator(primitives.SeparatorProps{Semantic: true})) +
		`<p>Section two</p>`
	vertical := `<span>Left</span>` +
		comp(primitives.Separator(primitives.SeparatorProps{Orientation: primitives.OrientationVertical})) +
		`<span>Right</span>`
	return page("Separator", `<section data-slot="separators">`+horizontal+semantic+`<p>`+vertical+`</p></section>`)
}
