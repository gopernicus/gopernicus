package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.3 spatial primitives (P57 Carousel, P61 Resizable, P62 Scroll Area). These
// tests prove: the native/no-controller Carousel and Scroll Area contract (a
// focusable, named scroll region; the scroll-snap track and slide roles; the in-page
// anchor dots; enum defaults); the Resizable server-owned baseline (role=group panes,
// a role=separator handle with aria-valuenow/min/max/controls + the gothResizable
// binding + the data-default-size geometry bucket); attribute-merge ownership; and
// the frozen no-server-rendered-style invariant (dynamic resize geometry is a
// controller-owned CSSOM write, never a rendered style attribute).

// TestNoSpatialPrimitiveEmitsInlineStyle proves invariant (a): no spatial part
// emits an inline style= in any state.
func TestNoSpatialPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, ScrollArea(ScrollAreaProps{Label: "Log"}), "x"),
		renderKids(t, ScrollArea(ScrollAreaProps{Orientation: ScrollHorizontal}), "x"),
		renderKids(t, ScrollArea(ScrollAreaProps{Orientation: ScrollBoth}), "x"),
		renderKids(t, Carousel(CarouselProps{Label: "Photos", Orientation: CarouselVertical}), "x"),
		renderKids(t, CarouselContent(CarouselContentProps{Label: "Slides"}), "x"),
		renderKids(t, CarouselItem(CarouselItemProps{Base: Base{ID: "s1"}, Label: "1 of 2"}), "x"),
		renderKids(t, CarouselDots(CarouselDotsProps{}), "x"),
		renderKids(t, CarouselDot(CarouselDotProps{Target: mustParseURL(t, "#s1"), Label: "Go to slide 1", Current: true}), ""),
		renderKids(t, CarouselDot(CarouselDotProps{Label: "inert"}), ""),
		renderKids(t, Resizable(ResizableProps{DefaultSize: 30}), "x"),
		renderKids(t, Resizable(ResizableProps{Orientation: ResizableVertical, DefaultSize: 70}), "x"),
		renderKids(t, ResizablePane(ResizablePaneProps{Base: Base{ID: "p1"}, Label: "Primary"}), "x"),
		render(t, ResizableHandle(ResizableHandleProps{Value: 30, Min: 15, Max: 85, ControlsID: "p1", Label: "Resize"})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("spatial primitive emitted an inline style=: %s", o)
		}
	}
}

// TestResizableBaseline proves the server-owned split baseline: the root binds the
// controller and carries the geometry bucket; panes are role=group regions; the
// handle is a role=separator with the APG value attributes and aria-controls.
func TestResizableBaseline(t *testing.T) {
	root := renderKids(t, Resizable(ResizableProps{
		Base:        Base{ID: "rz-1"},
		Orientation: ResizableVertical,
		DefaultSize: 32, // snaps to 30
	}), "x")
	mustContain(t, root,
		`class="goth-resizable"`, `data-slot="resizable"`, `id="rz-1"`,
		`data-orientation="vertical"`, `data-default-size="30"`, `x-data="gothResizable"`)

	// Zero value is a horizontal 50/50 split.
	def := renderKids(t, Resizable(ResizableProps{}), "x")
	mustContain(t, def, `data-orientation="horizontal"`, `data-default-size="50"`)

	pane := renderKids(t, ResizablePane(ResizablePaneProps{Base: Base{ID: "primary"}, Label: "Files"}), "content")
	mustContain(t, pane, `class="goth-resizable-pane"`, `data-slot="resizable-pane"`,
		`role="group"`, `id="primary"`, `aria-label="Files"`, "content")

	handle := render(t, ResizableHandle(ResizableHandleProps{
		Orientation: ResizableVertical, Value: 30, Min: 15, Max: 85,
		ControlsID: "primary", Label: "Resize the file list",
	}))
	mustContain(t, handle,
		`class="goth-resizable-handle"`, `data-slot="resizable-handle"`, `role="separator"`,
		`tabindex="0"`, `aria-orientation="horizontal"`, `aria-valuenow="30"`,
		`aria-valuemin="15"`, `aria-valuemax="85"`, `aria-controls="primary"`,
		`aria-label="Resize the file list"`)
}

// TestResizableHandleDefaultsAndClamp proves the handle bound defaults, the
// value clamp into [min,max], the separator-orientation flip, and the default
// accessible name.
func TestResizableHandleDefaultsAndClamp(t *testing.T) {
	// Zero handle: value 50, bounds 10/90, horizontal panes → vertical separator,
	// default "Resize" name.
	zero := render(t, ResizableHandle(ResizableHandleProps{}))
	mustContain(t, zero, `aria-valuenow="50"`, `aria-valuemin="10"`, `aria-valuemax="90"`,
		`aria-orientation="vertical"`, `aria-label="Resize"`)

	// A value outside the bounds is clamped to the max.
	clamped := render(t, ResizableHandle(ResizableHandleProps{Value: 99, Min: 20, Max: 80}))
	mustContain(t, clamped, `aria-valuenow="80"`, `aria-valuemin="20"`, `aria-valuemax="80"`)
}

// TestResizableEnumAndSnap proves the orientation enum and the DefaultSize snap.
func TestResizableEnumAndSnap(t *testing.T) {
	if ResizableHorizontal.attr() != "horizontal" || ResizableHorizontal.separatorOrientation() != "vertical" {
		t.Error("zero-value orientation should be horizontal panes / vertical separator")
	}
	if ResizableVertical.separatorOrientation() != "horizontal" {
		t.Error("vertical panes should use a horizontal separator")
	}
	if !ResizableVertical.Valid() || ResizableOrientation("diagonal").Valid() {
		t.Error("enum membership check failed")
	}
	for in, want := range map[int]int{0: 50, 3: 5, 32: 30, 33: 35, 200: 95, -5: 50} {
		if got := (ResizableProps{DefaultSize: in}).defaultSize(); got != want {
			t.Errorf("defaultSize(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestScrollAreaRegion proves the ScrollArea is a focusable, named scroll region
// with a data-orientation axis hook.
func TestScrollAreaRegion(t *testing.T) {
	out := renderKids(t, ScrollArea(ScrollAreaProps{
		Base:        Base{ID: "sa-1"},
		Orientation: ScrollHorizontal,
		Label:       "Release notes",
	}), "<p>lots of content</p>")
	mustContain(t, out,
		`class="goth-scroll-area"`, `data-slot="scroll-area"`, `id="sa-1"`,
		`data-orientation="horizontal"`, `role="group"`, `tabindex="0"`,
		`aria-label="Release notes"`, "lots of content")

	// Zero value is a vertical scroll region.
	def := renderKids(t, ScrollArea(ScrollAreaProps{}), "x")
	mustContain(t, def, `data-orientation="vertical"`, `tabindex="0"`)
}

// TestScrollAreaEnum proves the orientation enum defaults and membership.
func TestScrollAreaEnum(t *testing.T) {
	if ScrollVertical.attr() != "vertical" {
		t.Error("zero-value ScrollAreaOrientation should map to vertical")
	}
	if !ScrollHorizontal.Valid() || !ScrollBoth.Valid() {
		t.Error("known orientations should be Valid")
	}
	if ScrollAreaOrientation("diagonal").Valid() {
		t.Error("unknown orientation should not be Valid")
	}
	if ScrollAreaOrientation("diagonal").attr() != "vertical" {
		t.Error("unknown orientation should render the safe default")
	}
}

// TestCarouselStructure proves the region/track/slide roles and the keyboard
// scroll region.
func TestCarouselStructure(t *testing.T) {
	root := renderKids(t, Carousel(CarouselProps{
		Base:        Base{ID: "car-1"},
		Orientation: CarouselVertical,
		Label:       "Featured photos",
	}), "x")
	mustContain(t, root,
		`class="goth-carousel"`, `data-slot="carousel"`, `id="car-1"`,
		`data-orientation="vertical"`, `aria-roledescription="carousel"`,
		`aria-label="Featured photos"`)

	content := renderKids(t, CarouselContent(CarouselContentProps{Label: "Slides"}), "x")
	mustContain(t, content,
		`data-slot="carousel-content"`, `role="group"`, `tabindex="0"`, `aria-label="Slides"`)

	item := renderKids(t, CarouselItem(CarouselItemProps{Base: Base{ID: "slide-2"}, Label: "2 of 5"}), "x")
	mustContain(t, item,
		`data-slot="carousel-item"`, `id="slide-2"`, `role="group"`,
		`aria-roledescription="slide"`, `aria-label="2 of 5"`)
}

// TestCarouselDots proves the direct-navigation dots: an in-page anchor link when
// Target is set (the no-JS control), an inert marker otherwise, plus the current
// status and the group's default label.
func TestCarouselDots(t *testing.T) {
	dots := renderKids(t, CarouselDots(CarouselDotsProps{}), "x")
	mustContain(t, dots, `data-slot="carousel-dots"`, `role="group"`, `aria-label="Choose slide"`)

	labelled := renderKids(t, CarouselDots(CarouselDotsProps{Label: "Photo controls"}), "x")
	mustContain(t, labelled, `aria-label="Photo controls"`)

	link := render(t, CarouselDot(CarouselDotProps{
		Target: mustParseURL(t, "#slide-3"), Label: "Go to slide 3", Current: true,
	}))
	mustContain(t, link,
		`<a`, `href="#slide-3"`, `class="goth-carousel-dot"`, `data-slot="carousel-dot"`,
		`aria-label="Go to slide 3"`, `aria-current="true"`, `data-current="true"`)

	inert := render(t, CarouselDot(CarouselDotProps{Label: "no target"}))
	mustContain(t, inert, `<span`, `data-slot="carousel-dot"`, `aria-label="no target"`)
	mustNotContain(t, inert, `<a`, `aria-current`)
}

// TestCarouselOrientationEnum proves the orientation enum defaults and membership.
func TestCarouselOrientationEnum(t *testing.T) {
	if CarouselHorizontal.attr() != "horizontal" {
		t.Error("zero-value CarouselOrientation should map to horizontal")
	}
	if !CarouselVertical.Valid() {
		t.Error("vertical should be Valid")
	}
	if CarouselOrientation("diagonal").Valid() {
		t.Error("unknown orientation should not be Valid")
	}
	if CarouselOrientation("diagonal").attr() != "horizontal" {
		t.Error("unknown orientation should render the safe default")
	}
}

// TestSpatialMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch.
func TestSpatialMergeHonorsOwnership(t *testing.T) {
	sa := renderKids(t, ScrollArea(ScrollAreaProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"data-slot": "hijack",
			"tabindex":  "-1",
			"class":     "dropped",
		},
	}}), "x")
	mustContain(t, sa, `data-slot="scroll-area"`, `tabindex="0"`, `goth-scroll-area custom-x`)
	mustNotContain(t, sa, `hijack`, `tabindex="-1"`, `dropped`)

	car := renderKids(t, Carousel(CarouselProps{Base: Base{
		Attributes: map[string]any{"aria-roledescription": "banner", "data-slot": "hijack"},
	}}), "x")
	mustContain(t, car, `data-slot="carousel"`, `aria-roledescription="carousel"`)
	mustNotContain(t, car, `hijack`, `aria-roledescription="banner"`)

	// The controller binding and the geometry bucket cannot be hijacked.
	rz := renderKids(t, Resizable(ResizableProps{DefaultSize: 40, Base: Base{
		Attributes: map[string]any{"x-data": "evil", "data-default-size": "99"},
	}}), "x")
	mustContain(t, rz, `x-data="gothResizable"`, `data-default-size="40"`)
	mustNotContain(t, rz, `evil`, `data-default-size="99"`)
}
