package showcase

import (
	"context"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// registerPrimitiveSpecimens registers one showcase page per GOTH-2.1
// content/status primitive. Every implemented primitive MUST have a specimen —
// TestEveryImplementedPrimitiveHasSpecimen enforces it against
// primitives.ImplementedPrimitives — and each specimen renders the real
// primitive component (not hand-written markup) so the browser/axe harness
// exercises the actual emitted surface. All twelve are presentational: they need
// only the StylesOnly profile (no controller/JS).
func registerPrimitiveSpecimens(r *Registry) {
	primitiveSpecs := []struct {
		id, title, prim string
		body            func() string
	}{
		{"badge", "Badge (P04)", "P04", badgeSpecimen},
		{"alert", "Alert (P01)", "P01", alertSpecimen},
		{"aspect-ratio", "Aspect Ratio (P02)", "P02", aspectRatioSpecimen},
		{"avatar", "Avatar (P03)", "P03", avatarSpecimen},
		{"card", "Card (P08)", "P08", cardSpecimen},
		{"empty", "Empty (P10)", "P10", emptySpecimen},
		{"item", "Item (P14)", "P14", itemSpecimen},
		{"kbd", "Kbd (P15)", "P15", kbdSpecimen},
		{"marker", "Marker (P17)", "P17", markerSpecimen},
		{"skeleton", "Skeleton (P22)", "P22", skeletonSpecimen},
		{"spinner", "Spinner (P23)", "P23", spinnerSpecimen},
		{"typography", "Typography (P26)", "P26", typographySpecimen},
		{"breadcrumb", "Breadcrumb (P05)", "P05", breadcrumbSpecimen},
		{"button", "Button (P06)", "P06", buttonSpecimen},
		{"button-group", "Button Group (P07)", "P07", buttonGroupSpecimen},
		{"direction", "Direction (P09)", "P09", directionSpecimen},
		{"pagination", "Pagination (P19)", "P19", paginationSpecimen},
		{"separator", "Separator (P21)", "P21", separatorSpecimen},
		{"field", "Field (P11)", "P11", fieldSpecimen},
		{"input", "Input (P12)", "P12", inputSpecimen},
		{"input-group", "Input Group (P13)", "P13", inputGroupSpecimen},
		{"label", "Label (P16)", "P16", labelSpecimen},
		{"native-select", "Native Select (P18)", "P18", nativeSelectSpecimen},
		{"progress", "Progress (P20)", "P20", progressSpecimen},
		{"table", "Table (P24)", "P24", tableSpecimen},
		{"textarea", "Textarea (P25)", "P25", textareaSpecimen},
	}
	for _, s := range primitiveSpecs {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.StylesOnly,
			Body:      s.body,
		})
	}
}

// comp renders a primitive component (no children) to an HTML string.
func comp(c templ.Component) string {
	var sb strings.Builder
	_ = c.Render(context.Background(), &sb)
	return sb.String()
}

// compKids renders a primitive component with the given (trusted, host-authored)
// children markup to an HTML string.
func compKids(c templ.Component, kids string) string {
	var sb strings.Builder
	_ = c.Render(templ.WithChildren(context.Background(), templ.Raw(kids)), &sb)
	return sb.String()
}

// page wraps specimen sections in a labelled main with a page heading so each
// specimen is a coherent, crawlable document for the browser/axe harness.
func page(title, inner string) string {
	return `<main data-slot="specimen"><h1>` + templ.EscapeString(title) + `</h1>` + inner + `</main>`
}

func mustURL(s string) primitives.URL {
	u, _ := primitives.ParseURL(s)
	return u
}

func badgeSpecimen() string {
	var b strings.Builder
	for _, v := range []primitives.BadgeVariant{
		primitives.BadgeDefault, primitives.BadgeSecondary,
		primitives.BadgeDestructive, primitives.BadgeOutline,
	} {
		b.WriteString(compKids(primitives.Badge(primitives.BadgeProps{Variant: v}), string(v)+" badge"))
		b.WriteString(" ")
	}
	// Link form (renders an anchor styled as a badge).
	b.WriteString(compKids(primitives.Badge(primitives.BadgeProps{URL: mustURL("/specimen/primitive-badge")}), "linked"))
	return page("Badge", `<section data-slot="badges">`+b.String()+`</section>`)
}

func alertSpecimen() string {
	icon := `<svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="12" cy="12" r="10"/></svg>`
	var b strings.Builder
	b.WriteString(compKids(
		primitives.Alert(primitives.AlertProps{Variant: primitives.AlertDefault, Title: "Heads up", Icon: templ.Raw(icon)}),
		"This is an informational status alert.",
	))
	b.WriteString(compKids(
		primitives.Alert(primitives.AlertProps{Variant: primitives.AlertSuccess, Title: "Saved"}),
		"Your changes were saved.",
	))
	b.WriteString(compKids(
		primitives.Alert(primitives.AlertProps{Variant: primitives.AlertWarning, Title: "Careful"}),
		"This action needs review.",
	))
	b.WriteString(compKids(
		primitives.Alert(primitives.AlertProps{Variant: primitives.AlertDestructive, Title: "Failed"}),
		"Something went wrong.",
	))
	return page("Alert", `<section data-slot="alerts">`+b.String()+`</section>`)
}

func aspectRatioSpecimen() string {
	var b strings.Builder
	ratios := []struct {
		r     primitives.Ratio
		label string
	}{
		{primitives.AspectSquare, "1:1 square"},
		{primitives.AspectVideo, "16:9 video"},
		{primitives.AspectFourThree, "4:3"},
		{primitives.AspectPortrait, "3:4 portrait"},
		{primitives.AspectWide, "21:9 wide"},
	}
	for _, x := range ratios {
		fill := `<div class="goth-item" data-variant="muted">` + templ.EscapeString(x.label) + `</div>`
		b.WriteString(compKids(primitives.AspectRatio(primitives.AspectRatioProps{Ratio: x.r}), fill))
	}
	return page("Aspect Ratio", `<section data-slot="ratios">`+b.String()+`</section>`)
}

func avatarSpecimen() string {
	withImage := compKids(primitives.Avatar(primitives.AvatarProps{}),
		compKids(primitives.AvatarFallback(primitives.AvatarFallbackProps{}), "AL")+
			comp(primitives.AvatarImage(primitives.AvatarImageProps{Src: mustURL("/avatar-ada.png"), Alt: "Ada Lovelace"})))
	fallbackOnly := compKids(primitives.Avatar(primitives.AvatarProps{}),
		compKids(primitives.AvatarFallback(primitives.AvatarFallbackProps{}), "GT"))
	return page("Avatar", `<section data-slot="avatars">`+withImage+" "+fallbackOnly+`</section>`)
}

func cardSpecimen() string {
	card := compKids(primitives.Card(primitives.CardProps{}),
		compKids(primitives.CardHeader(primitives.CardHeaderProps{}),
			compKids(primitives.CardTitle(primitives.CardTitleProps{}), "Project status")+
				compKids(primitives.CardDescription(primitives.CardDescriptionProps{}), "Updated moments ago")+
				compKids(primitives.CardAction(primitives.CardActionProps{}),
					compKids(primitives.Badge(primitives.BadgeProps{Variant: primitives.BadgeSecondary}), "live")))+
			compKids(primitives.CardContent(primitives.CardContentProps{}), "<p>Card content sits between the header and footer.</p>")+
			compKids(primitives.CardFooter(primitives.CardFooterProps{}), "<span>Footer note</span>"))
	return page("Card", `<section data-slot="cards">`+card+`</section>`)
}

func emptySpecimen() string {
	media := `<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>`
	e := compKids(primitives.Empty(primitives.EmptyProps{
		Media:       templ.Raw(media),
		Title:       "No results",
		Description: "Try adjusting your filters or search term.",
		Action:      templ.Raw(compKids(primitives.Badge(primitives.BadgeProps{URL: mustURL("/")}), "Reset filters")),
	}), "")
	return page("Empty", `<section data-slot="empties">`+e+`</section>`)
}

func itemSpecimen() string {
	row := func(variant primitives.ItemVariant, asLink bool) string {
		inner := compKids(primitives.ItemMedia(primitives.ItemMediaProps{}),
			compKids(primitives.Avatar(primitives.AvatarProps{}),
				compKids(primitives.AvatarFallback(primitives.AvatarFallbackProps{}), "GT"))) +
			compKids(primitives.ItemContent(primitives.ItemContentProps{}),
				compKids(primitives.ItemTitle(primitives.ItemTitleProps{}), "Gopernicus")+
					compKids(primitives.ItemDescription(primitives.ItemDescriptionProps{}), "A server-rendered UI kit.")) +
			compKids(primitives.ItemActions(primitives.ItemActionsProps{}),
				compKids(primitives.Badge(primitives.BadgeProps{Variant: primitives.BadgeOutline}), "v1"))
		p := primitives.ItemProps{Variant: variant}
		if asLink {
			p.URL = mustURL("/specimen/primitive-item")
			// A whole-row link cannot contain nested interactive actions; drop them.
			inner = compKids(primitives.ItemMedia(primitives.ItemMediaProps{}),
				compKids(primitives.Avatar(primitives.AvatarProps{}),
					compKids(primitives.AvatarFallback(primitives.AvatarFallbackProps{}), "GT"))) +
				compKids(primitives.ItemContent(primitives.ItemContentProps{}),
					compKids(primitives.ItemTitle(primitives.ItemTitleProps{}), "Open item")+
						compKids(primitives.ItemDescription(primitives.ItemDescriptionProps{}), "This whole row is a link."))
		}
		return compKids(primitives.Item(p), inner)
	}
	body := row(primitives.ItemDefault, false) + row(primitives.ItemOutline, false) + row(primitives.ItemMuted, false) + row(primitives.ItemDefault, true)
	return page("Item", `<section data-slot="items">`+body+`</section>`)
}

func kbdSpecimen() string {
	single := compKids(primitives.Kbd(primitives.KbdProps{}), "Esc")
	group := compKids(primitives.KbdGroup(primitives.KbdGroupProps{}),
		compKids(primitives.Kbd(primitives.KbdProps{}), "Ctrl")+
			compKids(primitives.Kbd(primitives.KbdProps{}), "K"))
	return page("Kbd", `<section data-slot="kbds"><p>Press `+group+` to search, or `+single+` to close.</p></section>`)
}

func markerSpecimen() string {
	icon := `<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="12" cy="12" r="10"/></svg>`
	var b strings.Builder
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{Tone: primitives.MarkerToneSuccess}), "Online"))
	b.WriteString("<br>")
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{Tone: primitives.MarkerToneDestructive}), "Offline"))
	b.WriteString("<br>")
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{Variant: primitives.MarkerNote, Icon: templ.Raw(icon)}), "A short note marker."))
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{Variant: primitives.MarkerRow, Icon: templ.Raw(icon)}), "A full-width row marker."))
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{Variant: primitives.MarkerSeparator}), "or"))
	b.WriteString(compKids(primitives.Marker(primitives.MarkerProps{URL: mustURL("/specimen/primitive-marker")}), "Linked marker"))
	return page("Marker", `<section data-slot="markers">`+b.String()+`</section>`)
}

func skeletonSpecimen() string {
	var b strings.Builder
	b.WriteString(comp(primitives.Skeleton(primitives.SkeletonProps{Base: primitives.Base{Class: "goth-skeleton-line-a"}})))
	b.WriteString(comp(primitives.Skeleton(primitives.SkeletonProps{Base: primitives.Base{Class: "goth-skeleton-line-b"}})))
	b.WriteString(comp(primitives.Skeleton(primitives.SkeletonProps{})))
	return page("Skeleton", `<section data-slot="skeletons">`+b.String()+`</section>`)
}

func spinnerSpecimen() string {
	named := comp(primitives.Spinner(primitives.SpinnerProps{}))
	labelled := comp(primitives.Spinner(primitives.SpinnerProps{Label: "Saving changes"}))
	decorative := `<button type="button" data-slot="busy-button">` +
		comp(primitives.Spinner(primitives.SpinnerProps{Decorative: true})) + ` Saving…</button>`
	return page("Spinner", `<section data-slot="spinners">`+named+" "+labelled+" "+decorative+`</section>`)
}

func typographySpecimen() string {
	var b strings.Builder
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyH2}), "Heading two"))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyH3}), "Heading three"))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyLead}), "A lead paragraph introduces the section."))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyP}), "An ordinary paragraph of body copy with a comfortable line length and rhythm."))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyBlockquote}), "A blockquote recites something notable."))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyList}),
		"<li>First item</li><li>Second item</li>"))
	b.WriteString(compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyMuted}), "Muted supporting text."))
	b.WriteString(`<p>Inline ` + compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyCode}), "code()") + ` sample.</p>`)
	return page("Typography", `<section data-slot="typography">`+b.String()+`</section>`)
}
