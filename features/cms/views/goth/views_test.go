package goth

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// newViews builds the adapter over a real StylesOnly ui/goth bundle.
func newViews(t *testing.T) Views {
	t.Helper()
	bundle, err := uigoth.New(uigoth.Config{AssetBasePath: "/assets/goth"})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(bundle)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// render drains a web.Renderer to a string for markup assertions.
func render(t *testing.T, r web.Renderer) string {
	t.Helper()
	var b strings.Builder
	if err := r.Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func mustContain(t *testing.T, name, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing %q in:\n%s", name, want, body)
		}
	}
}

// TestNew_NilBundle proves construction fails loudly on a nil bundle.
func TestNew_NilBundle(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) = nil error, want a construction error")
	}
}

// TestHome_RendersPublicChromeAndAssets asserts the public home renders the GOTH
// stylesheet (the fingerprinted asset under the bundle base path) and the content.
func TestHome_RendersPublicChromeAndAssets(t *testing.T) {
	body := render(t, newViews(t).Home(nil, []cms.ListItem{{Title: "Hello World", Href: "/articles/hello", Excerpt: "an excerpt"}}))
	mustContain(t, "Home", body,
		"<title>Home</title>", `rel="stylesheet"`, "/assets/goth/", "Latest posts", "Hello World", "an excerpt")
}

// TestEntriesList_RendersDataTableWithHTMX asserts the admin index renders through
// the ui/goth DataTable and that the sort/filter/pagination controls carry both a
// real no-JS URL AND explicit hx-* that swap the stable content-region target.
func TestEntriesList_RendersDataTableWithHTMX(t *testing.T) {
	pager := cms.Pager{NextCursor: "c1", HasPrev: true, PreviousCursor: "", Order: "", Status: "", BaseHref: "/articles"}
	body := render(t, newViews(t).EntriesList("Articles", "/articles/new", "/articles",
		[]cms.EntryListItem{{ID: "x1", Title: "First", Status: "published"}}, pager))

	mustContain(t, "EntriesList", body,
		"goth-data-table", "goth-table", "goth-button",
		`id="cms-entries-content"`, // the swap target
		"/articles/x1/edit",        // no-JS edit link
		"First",
	)
	// The filter form is a real GET form enhanced with hx-* on change.
	mustContain(t, "EntriesList filter", body,
		`method="get"`, `name="status"`, `hx-get`, `hx-target="#cms-entries-content"`, `hx-trigger="change"`)
	// The swap preserves scroll/focus and pushes history.
	mustContain(t, "EntriesList swap", body,
		`hx-swap="outerHTML show:none focus-scroll:false"`, `hx-push-url="true"`)
	// The pager "Older →" is a real link (no-JS) AND hx-enhanced.
	mustContain(t, "EntriesList pager", body, `href="/articles?cursor=c1"`, "Older")
}

// TestEntriesListContent_IsFragmentOnly asserts the HTMX fragment renders ONLY the
// swappable content region — no <html>/document chrome — so an outerHTML swap is
// seamless and the no-JS full page carries the same region.
func TestEntriesListContent_IsFragmentOnly(t *testing.T) {
	pager := cms.Pager{BaseHref: "/articles"}
	body := render(t, newViews(t).EntriesListContent("Articles", "/articles/new", "/articles",
		[]cms.EntryListItem{{ID: "x1", Title: "First", Status: "draft"}}, pager))
	mustContain(t, "fragment", body, `id="cms-entries-content"`, "First", "/articles/x1/edit")
	if strings.Contains(body, "<html") || strings.Contains(body, "<title>") {
		t.Errorf("EntriesListContent leaked document chrome:\n%s", body)
	}
	if strings.Contains(body, "cms-entries-filter") {
		t.Errorf("EntriesListContent leaked the toolbar (it must stay mounted across a swap):\n%s", body)
	}
}

// TestEntriesList_StatusFilterSelectedAndSortToggle asserts the active status
// filter is reflected as the selected option and the sort toggle names the sort it
// switches to, carrying the created_at order in its URL.
func TestEntriesList_StatusFilterSelectedAndSortToggle(t *testing.T) {
	pager := cms.Pager{Order: "created_at:asc", Status: "draft", BaseHref: "/articles"}
	body := render(t, newViews(t).EntriesList("Articles", "/articles/new", "/articles", nil, pager))
	mustContain(t, "filter selected", body, `selected value="draft"`)
	// Active sort is oldest-first, so the toggle offers "Newest first" back to default.
	mustContain(t, "sort toggle", body, "Newest first")
	// The status filter is preserved in the sort toggle URL; the order is dropped
	// (toggling back to default).
	mustContain(t, "sort toggle href", body, `href="/articles?status=draft"`)
}

// TestEntryForm_RendersFieldsThroughGOTH asserts the editor renders through the
// ui/goth Input/Textarea/NativeSelect primitives and preserves every field name.
func TestEntryForm_RendersFieldsThroughGOTH(t *testing.T) {
	m := cms.EntryFormModel{
		Heading: "New Article", Action: "/articles", Title: "T", Status: "draft",
		Fields: []cms.FieldInput{{Key: "subtitle", Label: "Subtitle", Kind: "text", Value: "S"}},
		Terms:  []cms.TermChoice{{ID: "t1", Label: "category: News", Checked: true}},
	}
	body := render(t, newViews(t).EntryForm(m))
	mustContain(t, "EntryForm", body,
		"goth-input", "goth-button", `action="/articles"`, `name="title"`,
		`name="excerpt"`, `name="body"`, `name="author"`, `name="status"`,
		`name="field_subtitle"`, `name="term_id"`, "Save")
}

// TestEntryForm_ShowsFormError asserts a rejected submit surfaces the message.
func TestEntryForm_ShowsFormError(t *testing.T) {
	body := render(t, newViews(t).EntryForm(cms.EntryFormModel{Heading: "Edit", Action: "/articles/x1", FormError: "bad slug"}))
	mustContain(t, "EntryForm error", body, `role="alert"`, "bad slug")
}

// stringRenderer is a fixed-body marker renderer standing in for a host component.
type stringRenderer string

func (s stringRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(s))
	return err
}

// overrideHome embeds the goth default and overrides only Home — the blessed
// partial-override path.
type overrideHome struct {
	Views
}

func (overrideHome) Home(_ []menus.MenuItem, _ []cms.ListItem) web.Renderer {
	return stringRenderer("CUSTOM-HOME")
}

func TestEmbed_OverrideOneMethod(t *testing.T) {
	var v cms.Views = overrideHome{newViews(t)}
	if home := render(t, v.Home(nil, nil)); !strings.Contains(home, "CUSTOM-HOME") {
		t.Fatalf("overridden Home did not render custom marker:\n%s", home)
	}
	// A non-overridden method still renders the bundled GOTH default.
	list := render(t, v.EntriesList("Pages", "/pages/new", "/pages", nil, cms.Pager{BaseHref: "/pages"}))
	if !strings.Contains(list, "goth-data-table") {
		t.Fatalf("non-overridden EntriesList did not render the GOTH default:\n%s", list)
	}
}

// TestSeedTemplates_RenderDefaults asserts the seed bindings render the bundled
// article/page bodies.
func TestSeedTemplates_RenderDefaults(t *testing.T) {
	bindings := newViews(t).SeedTemplates()
	got := map[string]content.TemplateFunc{}
	for _, b := range bindings {
		if b.Template != "default" {
			t.Fatalf("unexpected template %q", b.Template)
		}
		got[b.Type] = b.Fn
	}
	for _, typ := range []string{"article", "page"} {
		fn, ok := got[typ]
		if !ok {
			t.Fatalf("SeedTemplates missing %q default binding", typ)
		}
		if body := render(t, fn(content.Entry{Title: "T-" + typ, Body: "hello"})); !strings.Contains(body, "T-"+typ) {
			t.Fatalf("%s body missing title:\n%s", typ, body)
		}
	}
}
