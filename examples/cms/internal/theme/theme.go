// Package theme is a host-owned custom public-site theme for the cms example. It
// implements the cms.Views port by EMBEDDING the bundled default from
// features/cms/views/templ and overriding only the four public-chrome methods
// (Home, Archive, Single, Error) with ACME-branded html/template chrome — the
// blessed partial-override path (FS3). Every non-chrome method (admin pages,
// contact form, seed templates) falls through to the embedded default. Under the
// Registry model the theme is CHROME ONLY; per-entry bodies come from the content
// types' registered TemplateFuncs and are passed to Single as a web.Renderer this
// theme simply wraps.
//
// It uses html/template (no templ codegen) to show that a web.Renderer can be
// anything that writes itself to an io.Writer.
package theme

import (
	"bytes"
	"context"
	"html/template"
	"io"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	cmstempl "github.com/gopernicus/gopernicus/features/cms/views/templ"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// pages is the parsed template set: a shared header/footer plus one template per
// public page. Distinct "ACME" branding makes it obvious the custom theme is in
// effect rather than the bundled one.
var pages = template.Must(template.New("theme").Parse(`
{{define "header"}}<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · ACME</title>
<style>
 :root{--ink:#10243e;--accent:#e8541e}
 body{font-family:Georgia,serif;max-width:46rem;margin:0 auto;padding:0 1.25rem;color:var(--ink)}
 header.acme{display:flex;gap:1.5rem;align-items:baseline;border-bottom:3px solid var(--accent);padding:1rem 0;margin-bottom:2rem}
 header.acme .brand{font-weight:800;letter-spacing:.18em;color:var(--accent);text-decoration:none}
 header.acme nav a{margin-right:1rem;color:var(--ink);text-decoration:none}
 article{margin:1.5rem 0;padding-bottom:1.5rem;border-bottom:1px solid #eee}
 footer{margin:3rem 0;color:#789;font-size:.85rem;border-top:1px solid #eee;padding-top:1rem}
</style></head><body>
<header class="acme"><a class="brand" href="/">ACME</a><nav>{{range .Nav}}<a href="{{.URL}}">{{.Label}}</a>{{end}}</nav></header>
<main>{{end}}

{{define "footer"}}</main>
<footer>ACME custom theme · powered by the gopernicus CMS feature module</footer>
</body></html>{{end}}

{{define "home"}}{{template "header" .}}
<h1>{{.Heading}}</h1>
{{if .Items}}{{range .Items}}
<article><h2><a href="{{.Href}}">{{.Title}}</a></h2>{{if .Excerpt}}<p>{{.Excerpt}}</p>{{end}}</article>
{{end}}{{else}}<p>Nothing published yet.</p>{{end}}
{{template "footer" .}}{{end}}

{{define "archive"}}{{template "header" .}}
<h1>{{.Heading}}</h1>
{{range .Items}}<article><h2><a href="{{.Href}}">{{.Title}}</a></h2>{{if .Excerpt}}<p>{{.Excerpt}}</p>{{end}}</article>{{end}}
{{if .Next}}<p><a href="{{.Base}}?cursor={{.Next}}">Older →</a></p>{{end}}
{{template "footer" .}}{{end}}

{{define "single"}}{{template "header" .}}
{{.Body}}
{{template "footer" .}}{{end}}

{{define "error"}}{{template "header" .}}
<h1>{{.Status}}</h1><p>{{.Message}}</p><p><a href="/">Back home →</a></p>
{{template "footer" .}}{{end}}
`))

// Theme is the host's custom cms.Views implementation. It embeds the bundled
// default so every non-chrome method falls through, and overrides the four
// public-chrome methods with ACME markup.
type Theme struct {
	cmstempl.Views
}

// New returns the custom theme. Pass it to cms.Config.Views to replace the
// public site chrome while keeping the bundled admin/forms defaults.
func New() Theme { return Theme{} }

var _ cms.Views = Theme{}

// rendererFunc adapts a render closure to web.Renderer.
type rendererFunc func(context.Context, io.Writer) error

func (f rendererFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func render(name string, data any) web.Renderer {
	return rendererFunc(func(_ context.Context, w io.Writer) error {
		return pages.ExecuteTemplate(w, name, data)
	})
}

type base struct {
	Title string
	Nav   []menus.MenuItem
}

// Home renders the landing page with recent entries.
func (Theme) Home(nav []menus.MenuItem, items []cms.ListItem) web.Renderer {
	return render("home", struct {
		base
		Heading string
		Items   []cms.ListItem
	}{base{"Home", nav}, "Welcome to ACME", items})
}

// Archive renders a taxonomy archive listing.
func (Theme) Archive(heading string, nav []menus.MenuItem, items []cms.ListItem, nextCursor, baseHref string) web.Renderer {
	return render("archive", struct {
		base
		Heading string
		Items   []cms.ListItem
		Next    string
		Base    string
	}{base{heading, nav}, heading, items, nextCursor, baseHref})
}

// Single wraps a registered per-entry body in the ACME chrome.
func (Theme) Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer {
	return render("single", struct {
		base
		Body template.HTML
	}{base{title, nav}, renderToHTML(body)})
}

// Error renders the themed error page.
func (Theme) Error(status int, message string) web.Renderer {
	return render("error", struct {
		base
		Status  int
		Message string
	}{base{"Error", nil}, status, message})
}

// renderToHTML runs a web.Renderer to a string and marks it safe for embedding
// in the chrome. The per-entry body is feature-produced (sanitized markdown),
// not user-injected raw HTML.
func renderToHTML(r web.Renderer) template.HTML {
	var buf bytes.Buffer
	if err := r.Render(context.Background(), &buf); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}
