package web

import (
	"context"
	"html/template"
	"io"
)

// Template adapts a parsed html/template.Template to the Renderer interface, so
// a host that prefers stdlib templates over templ can satisfy a feature's Views
// port in three lines. This is FS3's promise made concrete: the view seam
// (Renderer) is tech-neutral by design — templ is our default, never the
// contract — and because html/template is standard library, the adapter for it
// belongs in the stdlib-only sdk rather than a sibling module.
//
// The returned Renderer executes the named template against data when Render is
// called; t must already contain a template named name (parsed via
// template.ParseFiles, ParseFS, or New(name).Parse). A host implements a Views
// method as:
//
//	func (v myViews) Page(p PageParams) web.Renderer {
//	    return web.Template(v.tmpl, "page.html", p)
//	}
func Template(t *template.Template, name string, data any) Renderer {
	return templateRenderer{tmpl: t, name: name, data: data}
}

// templateRenderer is the unexported Renderer backing Template: the parsed
// template set, the name to execute within it, and the data to render against.
type templateRenderer struct {
	tmpl *template.Template
	name string
	data any
}

// Render executes the named template into w. html/template's ExecuteTemplate is
// context-free and cannot be interrupted mid-render, so the only cancellation
// this can honor is one already signalled before the write begins: a cancelled
// ctx returns its error without touching w. Any template execution error is
// propagated unchanged.
func (r templateRenderer) Render(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.tmpl.ExecuteTemplate(w, r.name, r.data)
}
