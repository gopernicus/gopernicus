package theme

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/sdk/web"
)

func renderToString(t *testing.T, r web.Renderer) string {
	t.Helper()
	var b strings.Builder
	if err := r.Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// bodyRenderer is a stand-in for a registered per-entry render result.
type bodyRenderer string

func (s bodyRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(s))
	return err
}

func TestTheme_HomeUsesCustomMarkup(t *testing.T) {
	nav := []menus.MenuItem{{Label: "Blog", URL: "/articles"}}
	items := []cms.ListItem{{Title: "Hello", Href: "/articles/hello", Excerpt: "hi there"}}

	out := renderToString(t, New().Home(nav, items))

	for _, want := range []string{
		`class="brand" href="/">ACME`,  // custom branding (not the bundled theme)
		"ACME custom theme",            // custom footer
		`href="/articles/hello">Hello`, // the entry links through
		`<a href="/articles">Blog</a>`, // nav item rendered
	} {
		if !strings.Contains(out, want) {
			t.Errorf("home output missing %q\n---\n%s", want, out)
		}
	}
}

func TestTheme_SingleWrapsBodyInChrome(t *testing.T) {
	out := renderToString(t, New().Single("Deep Dive", "", nil, bodyRenderer("<article><h1>Deep Dive</h1></article>")))

	if !strings.Contains(out, "<h1>Deep Dive</h1>") {
		t.Errorf("entry body not rendered:\n%s", out)
	}
	// The per-entry body is wrapped in the ACME chrome.
	if !strings.Contains(out, `class="brand" href="/">ACME`) || !strings.Contains(out, "ACME custom theme") {
		t.Errorf("single not wrapped in chrome:\n%s", out)
	}
}

func TestTheme_ErrorPage(t *testing.T) {
	out := renderToString(t, New().Error(404, "not found"))
	if !strings.Contains(out, "<h1>404</h1>") || !strings.Contains(out, "not found") {
		t.Errorf("error page missing status/message:\n%s", out)
	}
}

// TestTheme_NonOverriddenMethodUsesBundledDefault proves the partial-override
// path: EntriesList is not overridden, so it falls through the embedded
// cmstempl.Views and renders the bundled admin default — not ACME chrome.
func TestTheme_NonOverriddenMethodUsesBundledDefault(t *testing.T) {
	out := renderToString(t, New().EntriesList("Articles", "/articles/new", "/articles", []cms.EntryListItem{{ID: "x1", Title: "First", Status: "published"}}, ""))
	for _, want := range []string{"<h1>Articles</h1>", "First", "/articles/x1/edit"} {
		if !strings.Contains(out, want) {
			t.Errorf("bundled EntriesList missing %q\n---\n%s", want, out)
		}
	}
}
