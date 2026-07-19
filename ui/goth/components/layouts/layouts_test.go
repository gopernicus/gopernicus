package layouts

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

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

// TestNoLayoutEmitsInlineStyle proves invariant §6: no layout component emits a
// server-rendered style attribute or inline <style> in any state.
func TestNoLayoutEmitsInlineStyle(t *testing.T) {
	sample := templ.Raw(`<span>x</span>`)
	outs := []string{
		renderKids(t, DocumentShell(DocumentShellProps{Header: sample, Footer: sample}), "body"),
		renderKids(t, AppShell(AppShellProps{Sidebar: sample, Header: sample}), "body"),
		renderKids(t, AuthShell(AuthShellProps{Title: "Sign in", Description: "welcome", Brand: sample, Footer: sample}), "form"),
		render(t, PageHeader(PageHeaderProps{Title: "People", Description: "manage", Breadcrumb: sample, Actions: sample})),
		render(t, ActionBar(ActionBarProps{Label: "Row actions", Start: sample, End: sample})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") || strings.Contains(o, "<style") {
			t.Errorf("layout component emitted inline style: %s", o)
		}
	}
}

func TestDocumentShell(t *testing.T) {
	out := renderKids(t, DocumentShell(DocumentShellProps{
		Base:   primitives.Base{ID: "shell", Class: "wide"},
		Header: templ.Raw(`<nav>top</nav>`),
		Footer: templ.Raw(`<small>foot</small>`),
	}), `<p>content</p>`)
	mustContain(t, out,
		`class="goth-document-shell wide"`, `data-slot="document-shell"`, `id="shell"`,
		`data-slot="document-shell-header"`, `<nav>top</nav>`,
		`data-slot="document-shell-main"`, `<p>content</p>`,
		`data-slot="document-shell-footer"`, `<small>foot</small>`)

	// Optional slots omitted render only the main region.
	bare := renderKids(t, DocumentShell(DocumentShellProps{}), `x`)
	mustNotContain(t, bare, `document-shell-header`, `document-shell-footer`)
	mustContain(t, bare, `data-slot="document-shell-main"`)
}

func TestAppShell(t *testing.T) {
	out := renderKids(t, AppShell(AppShellProps{
		Sidebar: templ.Raw(`<aside>nav</aside>`),
		Header:  templ.Raw(`<div>bar</div>`),
	}), `<article>main</article>`)
	mustContain(t, out,
		`class="goth-app-shell"`, `data-slot="app-shell"`,
		`data-slot="app-shell-sidebar"`, `<aside>nav</aside>`,
		`data-slot="app-shell-header"`, `<div>bar</div>`,
		`data-slot="app-shell-main"`, `<article>main</article>`)
}

func TestAuthShellComposesCard(t *testing.T) {
	out := renderKids(t, AuthShell(AuthShellProps{
		Title:       "Sign in",
		Description: "Use your account",
		Brand:       templ.Raw(`<img alt="brand">`),
		Footer:      templ.Raw(`<a href="/register">Register</a>`),
	}), `<form>fields</form>`)
	// Centered wrapper + composed Card family.
	mustContain(t, out,
		`class="goth-auth-shell"`, `data-slot="auth-shell"`,
		`goth-card`, `goth-auth-shell-card`,
		`data-slot="auth-shell-brand"`, `<img alt="brand">`,
		`data-slot="card-title"`, "Sign in",
		`data-slot="card-description"`, "Use your account",
		`data-slot="card-content"`, `<form>fields</form>`,
		`data-slot="card-footer"`, `<a href="/register">Register</a>`)
}

func TestPageHeader(t *testing.T) {
	out := render(t, PageHeader(PageHeaderProps{
		Title:       "People",
		Description: "Manage your team",
		Breadcrumb:  templ.Raw(`<nav>crumbs</nav>`),
		Actions:     templ.Raw(`<button>New</button>`),
	}))
	mustContain(t, out,
		`class="goth-page-header"`, `data-slot="page-header"`,
		`data-slot="page-header-breadcrumb"`, `<nav>crumbs</nav>`,
		`<h1 data-slot="page-header-title">People</h1>`,
		`<p data-slot="page-header-description">Manage your team</p>`,
		`data-slot="page-header-actions"`, `<button>New</button>`)
}

func TestActionBarToolbarRole(t *testing.T) {
	out := render(t, ActionBar(ActionBarProps{
		Label: "Row actions",
		Start: templ.Raw(`<button>a</button>`),
		End:   templ.Raw(`<button>b</button>`),
	}))
	mustContain(t, out,
		`role="toolbar"`, `aria-label="Row actions"`, `data-slot="action-bar"`,
		`data-slot="action-bar-start"`, `<button>a</button>`,
		`data-slot="action-bar-end"`, `<button>b</button>`)
}

// TestCallerAttributesMergeWithoutDroppingOwned proves the frozen merge: a caller
// data-* rides through while the component's owned data-slot/role win, and a
// caller "class" key is dropped (Base.Class is the only class channel).
func TestCallerAttributesMergeWithoutDroppingOwned(t *testing.T) {
	out := render(t, ActionBar(ActionBarProps{
		Base: primitives.Base{Attributes: templ.Attributes{
			"data-testid": "bar",
			"data-slot":   "hacked",
			"class":       "dropped",
		}},
	}))
	mustContain(t, out, `data-testid="bar"`, `data-slot="action-bar"`)
	mustNotContain(t, out, `data-slot="hacked"`, `class="dropped"`)
}
