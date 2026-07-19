package showcase

import (
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// GOTH-6.3 spatial specimens (P57 Carousel, P61 Resizable, P62 Scroll Area).
// Carousel and Scroll Area are StylesOnly (they bind no controller and need no
// JavaScript). Resizable ships a StylesOnly server-split baseline plus Interactive
// (gothResizable drag/keyboard) specimens, including an RTL one.
//
//   - primitive-scroll-area: a native vertical and a native horizontal overflow
//     region, each a focusable, named scroll region (keyboard-scrollable) with a
//     slim themed scrollbar.
//   - primitive-carousel: a CSS scroll-snap carousel navigated with no JavaScript
//     via the in-page anchor dots, plus a keyboard-scrollable snap track and a
//     vertical variant.
//   - primitive-resizable: the server-owned split baseline (StylesOnly, no JS) —
//     the geometry comes from data-default-size + external CSS.
//   - primitive-resizable-drag: horizontal + vertical splits enhanced by
//     gothResizable (Interactive) — pointer drag + keyboard resize + bounds.
//   - primitive-resizable-rtl: the horizontal split under dir=rtl (Interactive).

func registerSpatialSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:        "primitive-scroll-area",
		Title:     "Scroll Area (P62)",
		Section:   SectionPrimitive,
		Primitive: "P62",
		Profile:   goth.StylesOnly,
		Body:      scrollAreaSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-carousel",
		Title:     "Carousel (P57)",
		Section:   SectionPrimitive,
		Primitive: "P57",
		Profile:   goth.StylesOnly,
		Body:      carouselSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-resizable",
		Title:     "Resizable — server split, no JavaScript (P61)",
		Section:   SectionPrimitive,
		Primitive: "P61",
		Profile:   goth.StylesOnly,
		Body:      resizableBaselineSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-resizable-drag",
		Title:     "Resizable — drag + keyboard (P61)",
		Section:   SectionPrimitive,
		Primitive: "P61",
		Profile:   goth.Interactive,
		Body:      resizableDragSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-resizable-rtl",
		Title:     "Resizable RTL (P61)",
		Section:   SectionPrimitive,
		Primitive: "P61",
		Profile:   goth.Interactive,
		Dir:       theme.DirectionRTL,
		Body:      resizableRTLSpecimen,
	})
}

// resizableSplit renders a complete split: a primary pane (with a stable id), a
// handle wired to it (aria-controls + matching value/bounds), and a secondary pane.
func resizableSplit(id string, orientation primitives.ResizableOrientation, def, min, max int, primaryLabel, secondaryLabel, handleLabel, primaryBody, secondaryBody string) string {
	primaryID := id + "-primary"
	primary := compKids(primitives.ResizablePane(primitives.ResizablePaneProps{
		Base: primitives.Base{ID: primaryID}, Label: primaryLabel,
	}), primaryBody)
	handle := comp(primitives.ResizableHandle(primitives.ResizableHandleProps{
		Orientation: orientation, Value: def, Min: min, Max: max,
		ControlsID: primaryID, Label: handleLabel,
	}))
	secondary := compKids(primitives.ResizablePane(primitives.ResizablePaneProps{
		Label: secondaryLabel,
	}), secondaryBody)
	return compKids(primitives.Resizable(primitives.ResizableProps{
		Base: primitives.Base{ID: id}, Orientation: orientation, DefaultSize: def,
	}), primary+handle+secondary)
}

func resizableBaselineSpecimen() string {
	split := resizableSplit("rz-baseline", primitives.ResizableHorizontal, 40, 15, 85,
		"Sidebar", "Detail",
		"Resize the sidebar",
		`<h3>Sidebar</h3><p>The 40% split is rendered by the server (data-default-size) with no JavaScript.</p>`,
		`<h3>Detail</h3><p>No Alpine runs on this StylesOnly page — the separator is a static divider.</p>`)
	return page("Resizable (server split)", `<section data-slot="resizable-baseline-specimen">`+split+`</section>`)
}

func resizableDragSpecimen() string {
	horizontal := resizableSplit("rz-h", primitives.ResizableHorizontal, 40, 15, 85,
		"Files", "Preview",
		"Resize the file list",
		`<h3>Files</h3><p>Drag the divider, or focus it and press the arrow keys / Home / End.</p>`,
		`<h3>Preview</h3><p>The split is clamped to 15%–85%.</p>`)
	vertical := resizableSplit("rz-v", primitives.ResizableVertical, 50, 20, 80,
		"Editor", "Console",
		"Resize the editor",
		`<h3>Editor</h3><p>A vertical split: ArrowUp/ArrowDown resize.</p>`,
		`<h3>Console</h3><p>Bounds 20%–80%.</p>`)
	body := `<h2>Horizontal</h2>` + horizontal + `<h2>Vertical</h2>` + vertical
	return page("Resizable (drag + keyboard)", `<section data-slot="resizable-drag-specimen">`+body+`</section>`)
}

func resizableRTLSpecimen() string {
	split := resizableSplit("rz-rtl", primitives.ResizableHorizontal, 40, 15, 85,
		"لوحة", "تفاصيل",
		"Resize the panel",
		`<h3>Panel</h3><p>Under dir=rtl the primary pane sits on the inline-end (right) edge; arrow direction follows the writing direction.</p>`,
		`<h3>Detail</h3><p>ArrowLeft grows the primary pane in RTL.</p>`)
	return page("Resizable RTL", `<section data-slot="resizable-rtl-specimen">`+split+`</section>`)
}

func scrollAreaSpecimen() string {
	var b strings.Builder

	// Vertical: a tall list of paragraphs overflows the fixed-height region.
	var tall strings.Builder
	for i := 1; i <= 20; i++ {
		tall.WriteString(`<p data-slot="scroll-row">Line ` + strconv.Itoa(i) +
			` — a native, keyboard-scrollable overflow region with a themed scrollbar.</p>`)
	}
	b.WriteString(`<h2>Vertical</h2>`)
	b.WriteString(compKids(primitives.ScrollArea(primitives.ScrollAreaProps{
		Label: "Release notes",
	}), tall.String()))

	// Horizontal: a long row of chips overflows on the inline axis (enough chips to
	// exceed the widest test viewport, so scrollWidth > clientWidth everywhere). The
	// chips are inline and the region carries white-space:nowrap in components.css,
	// so they never wrap — no inline style needed.
	var wide strings.Builder
	for i := 1; i <= 40; i++ {
		wide.WriteString(`<span data-slot="scroll-chip">tag-number-` + strconv.Itoa(i) + `</span> `)
	}
	b.WriteString(`<h2>Horizontal</h2>`)
	b.WriteString(compKids(primitives.ScrollArea(primitives.ScrollAreaProps{
		Orientation: primitives.ScrollHorizontal,
		Label:       "Tags",
	}), wide.String()))

	return page("Scroll Area", `<section data-slot="scroll-area-specimen">`+b.String()+`</section>`)
}

func carouselSpecimen() string {
	slides := []struct {
		id, heading, body, variant string
	}{
		{"carousel-slide-1", "Slide one", "Swipe, drag, or use the arrow keys inside the track.", "primary"},
		{"carousel-slide-2", "Slide two", "Each slide snaps into place on the inline axis.", "secondary"},
		{"carousel-slide-3", "Slide three", "The dots below are real in-page links — no JavaScript.", "accent"},
		{"carousel-slide-4", "Slide four", "The current dot marks the server-chosen slide.", "muted"},
	}
	n := len(slides)

	// Track of snap-aligned slides.
	var track strings.Builder
	for i, s := range slides {
		card := `<div data-slot="carousel-card" data-variant="` + s.variant + `">` +
			`<h3>` + templ.EscapeString(s.heading) + `</h3>` +
			`<p>` + templ.EscapeString(s.body) + `</p></div>`
		track.WriteString(compKids(primitives.CarouselItem(primitives.CarouselItemProps{
			Base:  primitives.Base{ID: s.id},
			Label: strconv.Itoa(i+1) + " of " + strconv.Itoa(n),
		}), card))
	}
	content := compKids(primitives.CarouselContent(primitives.CarouselContentProps{
		Label: "Slides",
	}), track.String())

	// Dots: one in-page anchor per slide; the first is the server-chosen current.
	var dots strings.Builder
	for i, s := range slides {
		dots.WriteString(comp(primitives.CarouselDot(primitives.CarouselDotProps{
			Target:  mustURL("#" + s.id),
			Label:   "Go to slide " + strconv.Itoa(i+1),
			Current: i == 0,
		})))
	}
	dotsGroup := compKids(primitives.CarouselDots(primitives.CarouselDotsProps{
		Label: "Choose slide",
	}), dots.String())

	horizontal := compKids(primitives.Carousel(primitives.CarouselProps{
		Label: "Featured slides",
	}), content+dotsGroup)

	// A vertical variant proves the data-orientation axis switch.
	var vtrack strings.Builder
	for i := 1; i <= 4; i++ {
		vtrack.WriteString(compKids(primitives.CarouselItem(primitives.CarouselItemProps{
			Base:  primitives.Base{ID: "vslide-" + strconv.Itoa(i)},
			Label: strconv.Itoa(i) + " of 4",
		}), `<div data-slot="carousel-card" data-variant="secondary"><h3>Row `+strconv.Itoa(i)+`</h3></div>`))
	}
	vertical := compKids(primitives.Carousel(primitives.CarouselProps{
		Orientation: primitives.CarouselVertical,
		Label:       "Vertical slides",
	}), compKids(primitives.CarouselContent(primitives.CarouselContentProps{Label: "Vertical slides"}), vtrack.String()))

	body := `<h2>Horizontal (scroll-snap + no-JS dots)</h2>` + horizontal +
		`<h2>Vertical</h2>` + vertical
	return page("Carousel", `<section data-slot="carousel-specimen">`+body+`</section>`)
}
