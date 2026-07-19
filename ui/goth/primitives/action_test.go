package primitives

import (
	"errors"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// TestNoActionPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-2.2
// action/navigation primitive emits an inline style= attribute in any state.
func TestNoActionPrimitiveEmitsInlineStyle(t *testing.T) {
	u, _ := ParseURL("/p/2")
	outs := []string{
		render(t, Separator(SeparatorProps{Orientation: OrientationVertical, Semantic: true})),
		renderKids(t, Direction(DirectionProps{Dir: theme.DirectionRTL}), "rtl text"),
		renderKids(t, Button(ButtonProps{Variant: ButtonDestructive, Size: ButtonIcon, Label: "Delete"}), ""),
		renderKids(t, Button(ButtonProps{URL: u}), "Next"),
		renderKids(t, ButtonGroup(ButtonGroupProps{Orientation: OrientationVertical}), "x"),
		renderKids(t, Breadcrumb(BreadcrumbProps{}), "x"),
		render(t, BreadcrumbSeparator(BreadcrumbSeparatorProps{})),
		render(t, BreadcrumbEllipsis(BreadcrumbEllipsisProps{})),
		renderKids(t, Pagination(PaginationProps{}), "x"),
		renderKids(t, PaginationLink(PaginationLinkProps{URL: u, Active: true}), "2"),
		render(t, PaginationPrevious(PaginationPreviousProps{URL: u})),
		render(t, PaginationEllipsis(PaginationEllipsisProps{})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("primitive emitted an inline style=: %s", o)
		}
	}
}

func TestSeparator(t *testing.T) {
	// Decorative (zero value): role none + aria-hidden, no separator role.
	dec := render(t, Separator(SeparatorProps{}))
	mustContain(t, dec, `class="goth-separator"`, `data-slot="separator"`, `data-orientation="horizontal"`, `role="none"`, `aria-hidden="true"`)
	mustNotContain(t, dec, `role="separator"`)

	// Semantic vertical: role separator + aria-orientation, no aria-hidden.
	sem := render(t, Separator(SeparatorProps{Orientation: OrientationVertical, Semantic: true}))
	mustContain(t, sem, `role="separator"`, `aria-orientation="vertical"`, `data-orientation="vertical"`)
	mustNotContain(t, sem, `aria-hidden="true"`, `role="none"`)

	// Unknown orientation renders the default horizontal without panicking.
	def := render(t, Separator(SeparatorProps{Orientation: Orientation("diagonal")}))
	mustContain(t, def, `data-orientation="horizontal"`)
}

func TestOrientationValid(t *testing.T) {
	for _, o := range []Orientation{OrientationHorizontal, OrientationVertical} {
		if !o.Valid() {
			t.Errorf("%q should be valid", o)
		}
	}
	if Orientation("diagonal").Valid() {
		t.Error("diagonal should be invalid")
	}
}

func TestDirection(t *testing.T) {
	rtl := renderKids(t, Direction(DirectionProps{Dir: theme.DirectionRTL}), "مرحبا")
	mustContain(t, rtl, `class="goth-direction"`, `data-slot="direction"`, `dir="rtl"`, "مرحبا")

	// Zero value propagates LTR.
	ltr := renderKids(t, Direction(DirectionProps{}), "hello")
	mustContain(t, ltr, `dir="ltr"`)

	// Unknown direction falls back to LTR.
	def := renderKids(t, Direction(DirectionProps{Dir: theme.Direction("sideways")}), "x")
	mustContain(t, def, `dir="ltr"`)
}

func TestButtonRender(t *testing.T) {
	// Default: a native <button type="button"> (safe default, never implicit submit).
	out := renderKids(t, Button(ButtonProps{}), "Save")
	mustContain(t, out, `<button`, `class="goth-button"`, `data-slot="button"`, `data-variant="default"`, `data-size="default"`, `type="button"`, ">Save<")

	// Variant/size hooks.
	styled := renderKids(t, Button(ButtonProps{Variant: ButtonOutline, Size: ButtonSizeLarge, Type: ButtonTypeSubmit}), "Go")
	mustContain(t, styled, `data-variant="outline"`, `data-size="lg"`, `type="submit"`)

	// Disabled native button.
	dis := renderKids(t, Button(ButtonProps{Disabled: true}), "Nope")
	mustContain(t, dis, `disabled`)

	// Unknown variant/size render the defaults without panicking.
	def := renderKids(t, Button(ButtonProps{Variant: ButtonVariant("x"), Size: ButtonSize("y")}), "z")
	mustContain(t, def, `data-variant="default"`, `data-size="default"`)
}

func TestButtonLinkForm(t *testing.T) {
	u, err := ParseURL("/next")
	if err != nil {
		t.Fatal(err)
	}
	link := renderKids(t, Button(ButtonProps{URL: u}), "Next")
	mustContain(t, link, `<a href="/next"`, `class="goth-button"`, `data-slot="button"`, ">Next<")
	// A link form is not a submit control and carries no type/disabled.
	mustNotContain(t, link, "<button", `type=`, `disabled`)
}

func TestButtonIconOnlyRequiresName(t *testing.T) {
	// Icon-only: the accessible name comes from Label, emitted as aria-label.
	icon := renderKids(t, Button(ButtonProps{
		Size:        ButtonIcon,
		Label:       "Close",
		LeadingIcon: templ.Raw(`<svg></svg>`),
	}), "")
	mustContain(t, icon, `data-size="icon"`, `aria-label="Close"`, `data-slot="button-icon"`, "<svg>")
}

func TestButtonURLConflictGuard(t *testing.T) {
	u, _ := ParseURL("/go")

	// URL + submit type is invalid.
	if err := (ButtonProps{URL: u, Type: ButtonTypeSubmit}).Validate(); !errors.Is(err, ErrButtonURLConflict) {
		t.Errorf("URL+submit should be ErrButtonURLConflict, got %v", err)
	}
	// URL + disabled is invalid.
	if err := (ButtonProps{URL: u, Disabled: true}).Validate(); !errors.Is(err, ErrButtonURLConflict) {
		t.Errorf("URL+disabled should be ErrButtonURLConflict, got %v", err)
	}
	// URL + loading is invalid.
	if err := (ButtonProps{URL: u, Loading: true}).Validate(); !errors.Is(err, ErrButtonURLConflict) {
		t.Errorf("URL+loading should be ErrButtonURLConflict, got %v", err)
	}
	// A plain link and a plain button are valid.
	if err := (ButtonProps{URL: u}).Validate(); err != nil {
		t.Errorf("plain link should be valid, got %v", err)
	}
	if err := (ButtonProps{Type: ButtonTypeSubmit, Disabled: true}).Validate(); err != nil {
		t.Errorf("plain submit button should be valid, got %v", err)
	}

	// Render-time: the invalid combo degrades to a native <button> honoring the
	// submit/disabled intent and is marked (not a silent anchor drop).
	degraded := renderKids(t, Button(ButtonProps{URL: u, Type: ButtonTypeSubmit, Disabled: true}), "Submit")
	mustContain(t, degraded, "<button", `type="submit"`, `disabled`, `data-goth-invalid="button-url-conflict"`)
	mustNotContain(t, degraded, `<a href="/go"`)
}

func TestButtonLoading(t *testing.T) {
	out := renderKids(t, Button(ButtonProps{Loading: true}), "Saving")
	mustContain(t, out, `aria-busy="true"`, `disabled`, `goth-button-spinner`, `aria-hidden="true"`)
}

func TestButtonGroup(t *testing.T) {
	g := renderKids(t, ButtonGroup(ButtonGroupProps{Orientation: OrientationVertical}), "x")
	mustContain(t, g, `class="goth-button-group"`, `data-slot="button-group"`, `role="group"`, `data-orientation="vertical"`, "x")

	txt := renderKids(t, ButtonGroupText(ButtonGroupTextProps{}), "https://")
	mustContain(t, txt, `data-slot="button-group-text"`, "https://")
}

func TestBreadcrumb(t *testing.T) {
	nav := renderKids(t, Breadcrumb(BreadcrumbProps{}), "x")
	mustContain(t, nav, `<nav`, `class="goth-breadcrumb"`, `data-slot="breadcrumb"`, `aria-label="Breadcrumb"`)

	labelled := renderKids(t, Breadcrumb(BreadcrumbProps{Label: "You are here"}), "x")
	mustContain(t, labelled, `aria-label="You are here"`)

	list := renderKids(t, BreadcrumbList(BreadcrumbListProps{}), "x")
	mustContain(t, list, `<ol`, `data-slot="breadcrumb-list"`)

	item := renderKids(t, BreadcrumbItem(BreadcrumbItemProps{}), "x")
	mustContain(t, item, `<li`, `data-slot="breadcrumb-item"`)

	u, _ := ParseURL("/docs")
	link := renderKids(t, BreadcrumbLink(BreadcrumbLinkProps{URL: u}), "Docs")
	mustContain(t, link, `<a href="/docs"`, `data-slot="breadcrumb-link"`, ">Docs<")

	// The current page is not a link and is announced with aria-current.
	page := renderKids(t, BreadcrumbPage(BreadcrumbPageProps{}), "Current")
	mustContain(t, page, `data-slot="breadcrumb-page"`, `aria-current="page"`, `aria-disabled="true"`, "Current")
	mustNotContain(t, page, "<a ")

	// Separator: default chevron, presentational.
	sep := render(t, BreadcrumbSeparator(BreadcrumbSeparatorProps{}))
	mustContain(t, sep, `data-slot="breadcrumb-separator"`, `aria-hidden="true"`, "<svg")

	// Separator with a custom icon.
	sepIcon := render(t, BreadcrumbSeparator(BreadcrumbSeparatorProps{Icon: templ.Raw("/")}))
	mustContain(t, sepIcon, `data-slot="breadcrumb-separator"`, "/")

	// Ellipsis: decorative glyph + visually-hidden label.
	ell := render(t, BreadcrumbEllipsis(BreadcrumbEllipsisProps{}))
	mustContain(t, ell, `data-slot="breadcrumb-ellipsis"`, `aria-hidden="true"`, `class="goth-sr-only"`, "More")
}

func TestPagination(t *testing.T) {
	nav := renderKids(t, Pagination(PaginationProps{}), "x")
	mustContain(t, nav, `<nav`, `class="goth-pagination"`, `data-slot="pagination"`, `role="navigation"`, `aria-label="Pagination"`)

	content := renderKids(t, PaginationContent(PaginationContentProps{}), "x")
	mustContain(t, content, `<ul`, `data-slot="pagination-content"`)

	item := renderKids(t, PaginationItem(PaginationItemProps{}), "x")
	mustContain(t, item, `<li`, `data-slot="pagination-item"`)

	u, _ := ParseURL("/p/2")

	// Active link is announced as the current page.
	active := renderKids(t, PaginationLink(PaginationLinkProps{URL: u, Active: true}), "2")
	mustContain(t, active, `<a href="/p/2"`, `data-slot="pagination-link"`, `aria-current="page"`, `data-active="true"`, ">2<")

	// Inactive link carries no aria-current.
	inactive := renderKids(t, PaginationLink(PaginationLinkProps{URL: u}), "3")
	mustNotContain(t, inactive, `aria-current`)

	// Previous/Next carry an accessible name (default text) and a chevron.
	prev := render(t, PaginationPrevious(PaginationPreviousProps{URL: u}))
	mustContain(t, prev, `data-slot="pagination-previous"`, `aria-label="Previous"`, "<svg", "Previous")

	next := render(t, PaginationNext(PaginationNextProps{URL: u, Label: "More"}))
	mustContain(t, next, `data-slot="pagination-next"`, `aria-label="More"`, "More")

	// Ellipsis: decorative with a visually-hidden label.
	ell := render(t, PaginationEllipsis(PaginationEllipsisProps{}))
	mustContain(t, ell, `data-slot="pagination-ellipsis"`, `aria-hidden="true"`, `class="goth-sr-only"`, "More pages")
}

// TestActionBaseMergeAndClass proves the shared Base contract on an action
// primitive: owned attributes win, caller class in Attributes is dropped,
// Base.Class appends after the stable class, and Base.ID is honored.
func TestActionBaseMergeAndClass(t *testing.T) {
	out := renderKids(t, Button(ButtonProps{
		Base: Base{
			ID:    "save",
			Class: "w-full",
			Attributes: templ.Attributes{
				"class":       "sneaky",   // dropped
				"data-slot":   "override", // owned wins
				"data-testid": "keep",
			},
		},
	}), "Save")
	mustContain(t, out, `id="save"`, `class="goth-button w-full"`, `data-slot="button"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", `data-slot="override"`)
}
