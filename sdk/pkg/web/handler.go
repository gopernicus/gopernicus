// Package web is the HTTP transport kernel: a ServeMux-based handler with
// middleware support, domain-error→status mapping, JSON response
// helpers, and a generic HTML render seam. It owns transport policy (recovery,
// logging shape, status mapping) but knows nothing about app routes or views.
package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Middleware wraps an http.Handler. Compatible with any standard Go middleware.
type Middleware func(http.Handler) http.Handler

// Route is one registration recorded by WebHandler: the upper-cased method
// ("" for any method), the final pattern path including any group prefix, and
// whether it came from HandleRaw. It never carries the handler or middleware.
type Route struct {
	Method  string
	Pattern string
	Raw     bool
}

// WebHandler is an HTTP handler built on the standard library ServeMux
// with support for global and per-route middleware.
type WebHandler struct {
	mux              *http.ServeMux
	globalMiddleware []Middleware
	dispatch         http.Handler
	routes           []Route
}

// NewWebHandler creates a ServeMux handler. Install host middleware with Use.
func NewWebHandler() *WebHandler {
	mux := http.NewServeMux()
	return &WebHandler{
		mux:      mux,
		dispatch: mux,
	}
}

// Use adds genuinely global middleware: the stack wraps the whole mux dispatch
// once, so it observes ordinary routes, HandleRaw registrations, and the
// responses the ServeMux generates itself — redirects, 404s, and method-mismatch
// 405s (whose automatic Allow header is preserved, since the mux still routes).
// Middleware runs outermost-first in registration order, ahead of any per-route
// middleware, and may be added before or after routes are registered — but only
// before serving begins: each call rebuilds the dispatch chain without
// synchronization, so registration is boot-time-only, never concurrent with a
// live server.
func (h *WebHandler) Use(middleware ...Middleware) {
	h.globalMiddleware = append(h.globalMiddleware, middleware...)

	var dispatch http.Handler = h.mux
	for i := len(h.globalMiddleware) - 1; i >= 0; i-- {
		dispatch = h.globalMiddleware[i](dispatch)
	}
	h.dispatch = dispatch
}

// Handle registers a handler for the given method and path with optional
// per-route middleware. Route middleware runs inside the global stack installed
// by Use, in the order given. Method may be empty to match any method.
func (h *WebHandler) Handle(method, path string, handler http.HandlerFunc, middleware ...Middleware) {
	var final http.Handler = handler
	for i := len(middleware) - 1; i >= 0; i-- {
		final = middleware[i](final)
	}

	pattern := path
	if method != "" {
		pattern = fmt.Sprintf("%s %s", strings.ToUpper(method), path)
	}
	h.mux.Handle(pattern, final)
	h.routes = append(h.routes, Route{Method: strings.ToUpper(method), Pattern: path})
}

// HandleRaw registers an http.Handler against a raw ServeMux pattern, for the
// rare caller that needs the raw handler/pattern surface instead of Handle's
// method+path form. It is not an escape hatch from host policy: the registration
// sits inside the mux, so global middleware installed by Use — panic recovery,
// request IDs, logging, CORS — still applies. It takes no per-route middleware.
func (h *WebHandler) HandleRaw(pattern string, handler http.Handler) {
	h.mux.Handle(pattern, handler)
	h.routes = append(h.routes, Route{Pattern: pattern, Raw: true})
}

// Routes returns a copy of every registration made through Handle (and so the
// method helpers and RouteGroup) and HandleRaw, in registration order, so a
// host test can inventory what the server mounted. Like Use, it is boot-time
// only and not synchronized. A HandleRaw entry records its raw pattern
// verbatim; routes served behind a HandleRaw subtree are invisible to it.
func (h *WebHandler) Routes() []Route {
	return slices.Clone(h.routes)
}

// ServeHTTP runs the global middleware stack around the underlying ServeMux.
func (h *WebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.dispatch.ServeHTTP(w, r)
}
