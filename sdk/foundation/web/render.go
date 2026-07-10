package web

import (
	"context"
	"io"
	"net/http"
)

// Renderer is anything that can render itself to a writer. It is defined here
// with only standard-library types so sdk/foundation/web depends on no third-party
// package — templ.Component (Render(context.Context, io.Writer) error)
// satisfies it implicitly, so concrete views plug in without sdk importing
// templ. sdk is the adapter between the standard library and the app; it never
// imports an external module.
type Renderer interface {
	Render(ctx context.Context, w io.Writer) error
}

// Render writes a Renderer as an HTML response with the given status.
//
// It writes the header before rendering, so callers must choose the status
// before calling (a render failure mid-stream cannot change an already-sent
// status). On render failure the error is offered to the request logger via
// the response writer's error recorder.
//
// Render is the single SSR seam between transport and views: it takes the
// Renderer interface, never concrete CMS pages, so sdk stays ignorant of
// app-specific views.
func Render(ctx context.Context, w http.ResponseWriter, status int, c Renderer) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(ctx, w); err != nil {
		RecordError(w, err)
	}
}
