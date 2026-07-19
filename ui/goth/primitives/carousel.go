package primitives

import "github.com/a-h/templ"

// Carousel (P57, family F4 shipped native/no-controller) is a scroll-snap slide
// viewport. The baseline is CSS scroll-snap over a native overflow track: it
// swipes on touch, drags on a trackpad, and — because the track is a focusable
// region with an accessible name (tabindex=0 + aria-label, the P24/P60 keyboard
// precedent) — scrolls slide-by-slide with the arrow keys, and each snap point
// settles a slide into place. Direct navigation and current-slide status are the
// CarouselDots: real in-page anchor links (CarouselDot Target = the slide's #id)
// that scroll the corresponding slide into view with NO JavaScript, so the
// controls work on the StylesOnly profile. Smooth scrolling is gated on
// prefers-reduced-motion in CSS.
//
// Deferred (recorded, not shipped): relative previous/next arrow stepping,
// current-dot tracking as the reader manually scrolls, and opt-in autoplay all
// require scroll-position observation that no frozen §8 controller provides and
// CSS cannot express. They are flagged for an owner decision rather than added
// silently (the GOTH-6.2 gothMessageScroller precedent).
//
// data-slot hooks: carousel, carousel-content, carousel-item, carousel-dots,
// carousel-dot.

// CarouselOrientation is the typed slide-axis enum. The zero value lays slides out
// horizontally; vertical stacks them.
type CarouselOrientation string

const (
	// CarouselHorizontal is the zero value and the documented default.
	CarouselHorizontal CarouselOrientation = ""
	CarouselVertical   CarouselOrientation = "vertical"
)

// Valid reports whether o is a known CarouselOrientation.
func (o CarouselOrientation) Valid() bool {
	switch o {
	case CarouselHorizontal, CarouselVertical:
		return true
	default:
		return false
	}
}

func (o CarouselOrientation) attr() string {
	if o == CarouselVertical {
		return "vertical"
	}
	return "horizontal"
}

// CarouselProps configures the Carousel root (P57). The zero value is a valid
// horizontal carousel. Label is the accessible name of the carousel region.
type CarouselProps struct {
	Base
	// Orientation selects the slide axis. Zero value is horizontal.
	Orientation CarouselOrientation
	// Label is the carousel region's accessible name (aria-label). Recommended so
	// assistive technology announces it as a carousel with a name.
	Label string
}

// CarouselContentProps configures CarouselContent, the scroll-snap track. It is
// the focusable, keyboard-scrollable region.
type CarouselContentProps struct {
	Base
	// Label is the accessible name (aria-label) of the focusable scroll region.
	Label string
}

// CarouselItemProps configures CarouselItem, one slide. ID is the slide's stable
// element id so a CarouselDot can link to it (Target = "#" + that id). Label is the
// per-slide accessible name (e.g. "1 of 5").
type CarouselItemProps struct {
	Base
	// Label is the slide's accessible name (aria-label), commonly "n of m".
	Label string
}

// CarouselDotsProps configures CarouselDots, the group of direct-navigation dots.
type CarouselDotsProps struct {
	Base
	// Label is the dot group's accessible name (aria-label). Zero value falls back
	// to "Choose slide".
	Label string
}

// CarouselDotProps configures one CarouselDot: an in-page anchor that scrolls its
// target slide into view with no JavaScript.
type CarouselDotProps struct {
	Base
	// Target is the slide's in-page URL ("#" + the CarouselItem id). Required for a
	// dot to navigate; an empty Target renders an inert marker.
	Target URL
	// Label is the dot's accessible name (aria-label), commonly "Go to slide n".
	Label string
	// Current marks the server-chosen active slide (aria-current="true" +
	// data-current). Without JavaScript the current dot does not auto-update as the
	// reader manually scrolls; the server owns the initial selection.
	Current bool
}

func carouselClass(p CarouselProps) string { return classNames("goth-carousel", p.Class) }

func carouselAttrs(p CarouselProps) templ.Attributes {
	// aria-roledescription="carousel" on a named section (implicit role=region)
	// announces the widget as a carousel per the W3C carousel pattern.
	owned := templ.Attributes{
		"data-slot":            "carousel",
		"data-orientation":     p.Orientation.attr(),
		"aria-roledescription": "carousel",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func carouselContentClass(p CarouselContentProps) string {
	return classNames("goth-carousel-content", p.Class)
}

func carouselContentAttrs(p CarouselContentProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "carousel-content",
		"role":      "group",
		"tabindex":  "0",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func carouselItemClass(p CarouselItemProps) string {
	return classNames("goth-carousel-item", p.Class)
}

func carouselItemAttrs(p CarouselItemProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":            "carousel-item",
		"role":                 "group",
		"aria-roledescription": "slide",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func carouselDotsClass(p CarouselDotsProps) string {
	return classNames("goth-carousel-dots", p.Class)
}

func carouselDotsAttrs(p CarouselDotsProps) templ.Attributes {
	label := p.Label
	if label == "" {
		label = "Choose slide"
	}
	owned := templ.Attributes{
		"data-slot":  "carousel-dots",
		"role":       "group",
		"aria-label": label,
	}
	return ownedAttrs(p.Base, owned)
}

func carouselDotClass(p CarouselDotProps) string {
	return classNames("goth-carousel-dot", p.Class)
}

func carouselDotAttrs(p CarouselDotProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "carousel-dot"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	if p.Current {
		owned["aria-current"] = "true"
		owned["data-current"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}
