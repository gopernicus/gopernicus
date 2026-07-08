package templ

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// render drains a web.Renderer to a string for markup assertions.
func render(t *testing.T, r web.Renderer) string {
	t.Helper()
	var b strings.Builder
	if err := r.Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestHome_RendersBundledChrome asserts the bundled default renders the real
// site chrome — the assertions that lived in the feature's handler test before
// the port extraction now live with the templates they exercise.
func TestHome_RendersBundledChrome(t *testing.T) {
	body := render(t, New().Home(nil, []cms.ListItem{{Title: "Hello World", Href: "/articles/hello", Excerpt: "an excerpt"}}))
	for _, want := range []string{"<title>Home</title>", "<h1>Latest posts</h1>", "Hello World", "an excerpt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Home missing %q in:\n%s", want, body)
		}
	}
}

// TestEntriesList_RendersBundledDefault asserts the admin index renders through
// the bundled default.
func TestEntriesList_RendersBundledDefault(t *testing.T) {
	body := render(t, New().EntriesList("Articles", "/articles/new", "/articles", []cms.EntryListItem{{ID: "x1", Title: "First", Status: "published"}}, ""))
	for _, want := range []string{"<h1>Articles</h1>", "First", "/articles/x1/edit"} {
		if !strings.Contains(body, want) {
			t.Fatalf("EntriesList missing %q in:\n%s", want, body)
		}
	}
}

// markerRenderer writes a fixed body, standing in for a host's own component.
type markerRenderer string

func (m markerRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(m))
	return err
}

// overrideHome embeds the bundled default and overrides only Home, proving the
// blessed partial-override path: the override wins for Home, the bundled default
// still renders every non-overridden method.
type overrideHome struct {
	Views
}

func (overrideHome) Home(_ []menus.MenuItem, _ []cms.ListItem) web.Renderer {
	return markerRenderer("CUSTOM-HOME")
}

func TestEmbed_OverrideOneMethod(t *testing.T) {
	var v cms.Views = overrideHome{New()}

	// The overridden method returns the custom marker.
	home := render(t, v.Home(nil, nil))
	if !strings.Contains(home, "CUSTOM-HOME") {
		t.Fatalf("overridden Home did not render custom marker:\n%s", home)
	}

	// A non-overridden method still renders the bundled default chrome.
	list := render(t, v.EntriesList("Pages", "/pages/new", "/pages", nil, ""))
	if !strings.Contains(list, "<h1>Pages</h1>") {
		t.Fatalf("non-overridden EntriesList did not render bundled default:\n%s", list)
	}
}

// TestSeedTemplates_RenderDefaults asserts the seed bindings render the bundled
// article/page bodies.
func TestSeedTemplates_RenderDefaults(t *testing.T) {
	bindings := New().SeedTemplates()
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
		body := render(t, fn(content.Entry{Title: "T-" + typ, Body: "hello"}))
		if !strings.Contains(body, "T-"+typ) {
			t.Fatalf("%s body missing title:\n%s", typ, body)
		}
	}
}
