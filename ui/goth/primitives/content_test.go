package primitives

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// render renders a component with no children.
func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// renderKids renders a component with the given children content.
func renderKids(t *testing.T, c templ.Component, kids string) string {
	t.Helper()
	var sb strings.Builder
	ctx := templ.WithChildren(context.Background(), templ.Raw(kids))
	if err := c.Render(ctx, &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("output unexpectedly contains %q\n---\n%s", u, out)
		}
	}
}

// TestNoPrimitiveEmitsInlineStyle proves invariant (a): no content/status
// primitive emits an inline style= attribute in any state.
func TestNoPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Badge(BadgeProps{Variant: BadgeDestructive}), "beta"),
		renderKids(t, Alert(AlertProps{Variant: AlertWarning, Title: "T"}), "desc"),
		renderKids(t, AspectRatio(AspectRatioProps{Ratio: AspectVideo}), "<img alt=x>"),
		render(t, AvatarImage(AvatarImageProps{Alt: "a"})),
		renderKids(t, Card(CardProps{}), "x"),
		renderKids(t, Empty(EmptyProps{Title: "t", Description: "d"}), ""),
		renderKids(t, Item(ItemProps{Variant: ItemOutline}), "x"),
		renderKids(t, Kbd(KbdProps{}), "K"),
		renderKids(t, Marker(MarkerProps{Variant: MarkerSeparator}), "or"),
		render(t, Skeleton(SkeletonProps{})),
		render(t, Spinner(SpinnerProps{})),
		renderKids(t, Typography(TypographyProps{Variant: TypographyH1}), "Title"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("primitive emitted an inline style=: %s", o)
		}
	}
}

func TestBadgeRender(t *testing.T) {
	out := renderKids(t, Badge(BadgeProps{Variant: BadgeSecondary}), "new")
	mustContain(t, out, `class="goth-badge"`, `data-slot="badge"`, `data-variant="secondary"`, "<span", ">new<")

	// Unknown variant renders the default without panicking.
	def := renderKids(t, Badge(BadgeProps{Variant: BadgeVariant("nope")}), "x")
	mustContain(t, def, `data-variant="default"`)

	// URL renders an anchor (link form) without a nested interactive element.
	u, err := ParseURL("/tags/go")
	if err != nil {
		t.Fatal(err)
	}
	link := renderKids(t, Badge(BadgeProps{URL: u}), "go")
	mustContain(t, link, `<a href="/tags/go"`, `class="goth-badge"`)
	mustNotContain(t, link, "<span")
}

func TestBadgeVariantValid(t *testing.T) {
	for _, v := range []BadgeVariant{BadgeDefault, BadgeSecondary, BadgeDestructive, BadgeOutline} {
		if !v.Valid() {
			t.Errorf("%q should be valid", v)
		}
	}
	if BadgeVariant("bogus").Valid() {
		t.Error("bogus should be invalid")
	}
}

func TestAlertRolesAndSlots(t *testing.T) {
	// Default/success are polite status regions.
	for _, v := range []AlertVariant{AlertDefault, AlertSuccess} {
		out := renderKids(t, Alert(AlertProps{Variant: v, Title: "Heads up"}), "body")
		mustContain(t, out, `role="status"`, `data-slot="alert"`, `data-slot="alert-title"`, `data-slot="alert-description"`, "Heads up", "body")
	}
	// Destructive/warning are assertive alerts.
	for _, v := range []AlertVariant{AlertDestructive, AlertWarning} {
		out := renderKids(t, Alert(AlertProps{Variant: v}), "body")
		mustContain(t, out, `role="alert"`, `data-variant="`+v.attr()+`"`)
	}
	// Icon and action slots render when provided.
	withSlots := renderKids(t, Alert(AlertProps{
		Icon:   templ.Raw("<svg></svg>"),
		Action: templ.Raw("<button>x</button>"),
	}), "body")
	mustContain(t, withSlots, `data-slot="alert-icon"`, `data-slot="alert-action"`, "<svg>", "<button>x</button>")
}

func TestAspectRatioData(t *testing.T) {
	out := renderKids(t, AspectRatio(AspectRatioProps{Ratio: AspectVideo}), `<img alt="x">`)
	mustContain(t, out, `class="goth-aspect-ratio"`, `data-slot="aspect-ratio"`, `data-ratio="video"`, `<img alt="x">`)
	def := renderKids(t, AspectRatio(AspectRatioProps{}), "x")
	mustContain(t, def, `data-ratio="square"`)
}

func TestAvatar(t *testing.T) {
	src, err := ParseURL("/u/1.png")
	if err != nil {
		t.Fatal(err)
	}
	img := render(t, AvatarImage(AvatarImageProps{Src: src, Alt: "Ada Lovelace"}))
	mustContain(t, img, `<img src="/u/1.png"`, `alt="Ada Lovelace"`, `data-slot="avatar-image"`)

	// A zero Src renders nothing (the fallback carries the name).
	if got := render(t, AvatarImage(AvatarImageProps{})); strings.TrimSpace(got) != "" {
		t.Errorf("zero-Src AvatarImage should render nothing, got %q", got)
	}
	fb := renderKids(t, AvatarFallback(AvatarFallbackProps{}), "AL")
	mustContain(t, fb, `data-slot="avatar-fallback"`, "AL")
	container := renderKids(t, Avatar(AvatarProps{}), "")
	mustContain(t, container, `class="goth-avatar"`, `data-slot="avatar"`)
}

func TestCardParts(t *testing.T) {
	parts := map[string]string{
		"card":             renderKids(t, Card(CardProps{}), "x"),
		"card-header":      renderKids(t, CardHeader(CardHeaderProps{}), "x"),
		"card-title":       renderKids(t, CardTitle(CardTitleProps{}), "x"),
		"card-description": renderKids(t, CardDescription(CardDescriptionProps{}), "x"),
		"card-action":      renderKids(t, CardAction(CardActionProps{}), "x"),
		"card-content":     renderKids(t, CardContent(CardContentProps{}), "x"),
		"card-footer":      renderKids(t, CardFooter(CardFooterProps{}), "x"),
	}
	for slot, out := range parts {
		mustContain(t, out, `data-slot="`+slot+`"`, "x")
	}
	// Title is a div, not a fixed heading level (caller owns the outline).
	mustNotContain(t, parts["card-title"], "<h1", "<h2", "<h3")
}

func TestEmptySlots(t *testing.T) {
	out := renderKids(t, Empty(EmptyProps{
		Media:       templ.Raw("<svg></svg>"),
		Title:       "No results",
		Description: "Try another query.",
		Action:      templ.Raw("<button>Reset</button>"),
	}), "<p>content</p>")
	mustContain(t, out,
		`data-slot="empty"`, `data-slot="empty-media"`, `data-slot="empty-title"`,
		`data-slot="empty-description"`, `data-slot="empty-content"`, `data-slot="empty-action"`,
		"No results", "Try another query.", "<p>content</p>", "Reset")
}

func TestItemLinkAndParts(t *testing.T) {
	div := renderKids(t, Item(ItemProps{Variant: ItemMuted}), "x")
	mustContain(t, div, `class="goth-item"`, `data-slot="item"`, `data-variant="muted"`, "<div")

	u, _ := ParseURL("/rows/1")
	link := renderKids(t, Item(ItemProps{URL: u}), "x")
	mustContain(t, link, `<a href="/rows/1"`, `data-slot="item"`)
	mustNotContain(t, link, "<div class=\"goth-item\"")

	for slot, out := range map[string]string{
		"item-media":       renderKids(t, ItemMedia(ItemMediaProps{}), "x"),
		"item-content":     renderKids(t, ItemContent(ItemContentProps{}), "x"),
		"item-title":       renderKids(t, ItemTitle(ItemTitleProps{}), "x"),
		"item-description": renderKids(t, ItemDescription(ItemDescriptionProps{}), "x"),
		"item-actions":     renderKids(t, ItemActions(ItemActionsProps{}), "x"),
	} {
		mustContain(t, out, `data-slot="`+slot+`"`)
	}
}

func TestKbd(t *testing.T) {
	k := renderKids(t, Kbd(KbdProps{}), "K")
	mustContain(t, k, "<kbd", `class="goth-kbd"`, `data-slot="kbd"`, ">K<")
	g := renderKids(t, KbdGroup(KbdGroupProps{}), "keys")
	mustContain(t, g, `data-slot="kbd-group"`, "keys")
}

func TestMarker(t *testing.T) {
	// Status variant auto-renders a decorative dot.
	status := renderKids(t, Marker(MarkerProps{Tone: MarkerToneSuccess}), "Online")
	mustContain(t, status, `data-slot="marker"`, `data-variant="status"`, `data-tone="success"`, "goth-marker-dot", `data-slot="marker-label"`, "Online")

	// A provided icon replaces the dot.
	withIcon := renderKids(t, Marker(MarkerProps{Variant: MarkerNote, Icon: templ.Raw("<svg></svg>")}), "Note")
	mustContain(t, withIcon, `data-variant="note"`, "<svg>", `data-slot="marker-icon"`)
	mustNotContain(t, withIcon, "goth-marker-dot")

	// Link form.
	u, _ := ParseURL("/n/1")
	link := renderKids(t, Marker(MarkerProps{URL: u}), "go")
	mustContain(t, link, `<a href="/n/1"`, `class="goth-marker"`)
}

func TestSkeletonHiddenAndDecorative(t *testing.T) {
	out := render(t, Skeleton(SkeletonProps{}))
	mustContain(t, out, `class="goth-skeleton"`, `data-slot="skeleton"`, `aria-hidden="true"`)
	// A skeleton never announces: no role, no live region.
	mustNotContain(t, out, "role=", "aria-live")
}

func TestSpinnerAccessibleName(t *testing.T) {
	// Default: a named status region — never nameless.
	out := render(t, Spinner(SpinnerProps{}))
	mustContain(t, out, `role="status"`, `data-slot="spinner"`, `class="goth-sr-only"`, "Loading")

	named := render(t, Spinner(SpinnerProps{Label: "Saving"}))
	mustContain(t, named, "Saving")

	// Decorative: hidden from AT, no role, no visible label (external live region owns it).
	dec := render(t, Spinner(SpinnerProps{Decorative: true}))
	mustContain(t, dec, `aria-hidden="true"`)
	mustNotContain(t, dec, `role="status"`, "goth-sr-only")
}

func TestTypographyElements(t *testing.T) {
	cases := map[TypographyVariant]string{
		TypographyH1:         "<h1",
		TypographyH2:         "<h2",
		TypographyH3:         "<h3",
		TypographyH4:         "<h4",
		TypographyP:          "<p",
		TypographyBlockquote: "<blockquote",
		TypographyCode:       "<code",
		TypographyList:       "<ul",
		TypographySmall:      "<small",
		TypographyLead:       "<p",
		TypographyLarge:      "<p",
		TypographyMuted:      "<p",
	}
	for v, el := range cases {
		out := renderKids(t, Typography(TypographyProps{Variant: v}), "text")
		mustContain(t, out, el, `data-slot="typography"`, `data-variant="`+v.attr()+`"`, "text")
	}
	// Unknown variant falls back to a paragraph.
	def := renderKids(t, Typography(TypographyProps{Variant: TypographyVariant("bogus")}), "x")
	mustContain(t, def, "<p", `data-variant="p"`)
}

// TestBaseAttributesMergeAndClass proves the shared Base contract on a primitive:
// caller Attributes merge (owned wins), caller class in Attributes is dropped,
// Base.Class appends after the stable class, and Base.ID is honored.
func TestBaseAttributesMergeAndClass(t *testing.T) {
	out := renderKids(t, Badge(BadgeProps{
		Base: Base{
			ID:    "b1",
			Class: "ml-2",
			Attributes: templ.Attributes{
				"class":       "sneaky",   // must be dropped
				"data-slot":   "override", // owned wins
				"data-testid": "keep",
				"aria-label":  "Beta feature",
			},
		},
	}), "beta")
	mustContain(t, out, `id="b1"`, `class="goth-badge ml-2"`, `data-slot="badge"`, `data-testid="keep"`, `aria-label="Beta feature"`)
	mustNotContain(t, out, "sneaky", `data-slot="override"`)
}
