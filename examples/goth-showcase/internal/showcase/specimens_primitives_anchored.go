package showcase

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the showcase specimens for the GOTH-4.3 anchored information/
// selection primitives (P42 Hover Card, P45 Popover, P46 Select, P48 Tooltip).
// Each specimen renders the REAL primitive component so the browser/axe harness
// exercises the actual emitted surface. Tooltip/Hover Card ship both an
// Interactive specimen (the gothTooltip/gothHoverCard controller enhances the
// baseline: intent delay, focus-shows, Escape-hides) and a StylesOnly specimen
// (the pure CSS hover/focus-within reveal — the no-JS baseline). Popover ships an
// Interactive specimen (native popover + runtime anchor enhancement) and a
// StylesOnly specimen (native popover with NO JavaScript at all). Select is a
// styled native <select>, so it ships under StylesOnly plus a real no-JS <form>
// that submits with GET to an echo route.

// registerAnchoredSpecimens registers the four GOTH-4.3 primitives' specimens.
func registerAnchoredSpecimens(r *Registry) {
	interactive := []struct {
		id, title, prim string
		body            func() string
	}{
		{"tooltip", "Tooltip (P48)", "P48", tooltipSpecimen},
		{"hover-card", "Hover Card (P42)", "P42", hoverCardSpecimen},
		{"popover", "Popover (P45)", "P45", popoverSpecimen},
	}
	for _, s := range interactive {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.Interactive,
			Body:      s.body,
		})
	}

	static := []struct {
		id, title, prim string
		body            func() string
	}{
		{"tooltip-css", "Tooltip CSS baseline (P48)", "P48", tooltipCSSSpecimen},
		{"popover-nojs", "Popover no-JS (P45)", "P45", popoverNoJSSpecimen},
		{"select", "Select (P46)", "P46", selectSpecimen},
	}
	for _, s := range static {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.StylesOnly,
			Body:      s.body,
		})
	}

	// The no-JS Select form submits with GET to an echo route (like the selection/
	// compact forms). It carries no Primitive id.
	r.Register(Specimen{
		ID:      "select-form",
		Title:   "Select form (no-JS submission)",
		Section: SectionPrimitive,
		Profile: goth.StylesOnly,
		Body:    selectFormSpecimen,
	})
}

// tooltipBody renders a Tooltip whose trigger is described by its content. The
// same markup is the no-JS CSS baseline and the gothTooltip-enhanced widget; the
// only difference is which profile serves it.
func tooltipBody() string {
	tip := compKids(primitives.Tooltip(primitives.TooltipProps{}),
		compKids(primitives.TooltipTrigger(primitives.TooltipTriggerProps{
			Base:        primitives.Base{ID: "tooltip-trigger"},
			Describedby: "tooltip-desc",
		}), "Keyboard shortcuts")+
			compKids(primitives.TooltipContent(primitives.TooltipContentProps{Base: primitives.Base{ID: "tooltip-desc"}}),
				"Press ? anywhere to open the shortcut help."))
	return `<section data-slot="tooltip-specimen"><p>Focus or hover the button.</p>` + tip + `</section>`
}

func tooltipSpecimen() string    { return page("Tooltip", tooltipBody()) }
func tooltipCSSSpecimen() string { return page("Tooltip (CSS baseline)", tooltipBody()) }

// hoverCardSpecimen renders a Hover Card with a link trigger and interactive
// content (a link), proving the panel does not trap focus.
func hoverCardSpecimen() string {
	card := compKids(primitives.HoverCard(primitives.HoverCardProps{}),
		compKids(primitives.HoverCardTrigger(primitives.HoverCardTriggerProps{
			Base: primitives.Base{ID: "hover-card-trigger"},
			URL:  mustURL("/users/ada"),
		}), "@ada")+
			compKids(primitives.HoverCardContent(primitives.HoverCardContentProps{Base: primitives.Base{ID: "hover-card-content"}}),
				`<h2 data-slot="hovercard-title">Ada Lovelace</h2>`+
					`<p>First programmer. Hover intent keeps this open while you move into it.</p>`+
					`<a href="/users/ada/posts" data-slot="hovercard-link">View posts</a>`))
	return page("Hover Card", `<section data-slot="hover-card-specimen"><p>Hover or focus the link.</p>`+card+`</section>`)
}

// popoverBody renders a Popover: a native popovertarget trigger and a popover=auto
// panel with a focusable control inside.
func popoverBody() string {
	pop := compKids(primitives.Popover(primitives.PopoverProps{}),
		compKids(primitives.PopoverTrigger(primitives.PopoverTriggerProps{
			Base:   primitives.Base{ID: "popover-trigger"},
			Target: "popover-content",
		}), "Open popover")+
			compKids(primitives.PopoverContent(primitives.PopoverContentProps{Base: primitives.Base{ID: "popover-content"}}),
				`<h2 data-slot="popover-title">Dimensions</h2>`+
					`<p>Native popover: click toggles, Escape and an outside press close it.</p>`+
					input("popover-input", "Width")))
	return `<section data-slot="popover-specimen">` + pop + `</section>`
}

func popoverSpecimen() string     { return page("Popover", popoverBody()) }
func popoverNoJSSpecimen() string { return page("Popover (no-JS)", popoverBody()) }

func fruitOptions() string {
	opt := func(v, label string, selected bool) string {
		return compKids(primitives.SelectOption(primitives.SelectOptionProps{Value: v, Selected: selected}), label)
	}
	citrus := compKids(primitives.SelectGroup(primitives.SelectGroupProps{Label: "Citrus"}),
		opt("lime", "Lime", false)+opt("lemon", "Lemon", false))
	pome := compKids(primitives.SelectGroup(primitives.SelectGroupProps{Label: "Pome"}),
		opt("apple", "Apple", false)+opt("pear", "Pear", false))
	return citrus + pome
}

func selectSpecimen() string {
	sel := compKids(primitives.Select(primitives.SelectProps{
		Base:        primitives.Base{ID: "fruit-select"},
		Name:        "fruit",
		Placeholder: "Choose a fruit",
	}), fruitOptions())
	labelled := compKids(primitives.Label(primitives.LabelProps{For: "fruit-select"}), "Favorite fruit") + sel
	return page("Select", `<section data-slot="select-specimen">`+labelled+`</section>`)
}

// selectFormSpecimen is a real native form: the Select submits with GET (no
// JavaScript) to the echo route, which reflects the submitted value back.
func selectFormSpecimen() string {
	sel := compKids(primitives.Select(primitives.SelectProps{
		Base:        primitives.Base{ID: "f-fruit"},
		Name:        "fruit",
		Required:    true,
		Placeholder: "Choose a fruit",
	}), fruitOptions())
	field := compKids(primitives.Label(primitives.LabelProps{For: "f-fruit"}), "Favorite fruit") + sel
	submit := `<button class="goth-button" data-slot="button" data-variant="default" data-size="default" type="submit">Submit</button>`
	form := `<form method="get" action="/anchored/echo" data-slot="select-form">` + field + submit + `</form>`
	return page("Select form", `<section data-slot="select-form-section">`+form+`</section>`)
}

// registerAnchoredFixtures wires the echo route the no-JS Select form submits to.
func (s *Server) registerAnchoredFixtures() {
	s.handler.Handle(http.MethodGet, "/anchored/echo", func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		val := r.URL.Query().Get("fruit")
		var b strings.Builder
		b.WriteString(`<main data-slot="anchored-echo"><h1>Submitted</h1><dl>`)
		b.WriteString(`<dt>fruit</dt><dd data-field="fruit">` + templ.EscapeString(val) + `</dd>`)
		b.WriteString(`</dl><a href="/specimen/select-form">Back</a></main>`)

		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — submitted"}, rawComponent(b.String()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
