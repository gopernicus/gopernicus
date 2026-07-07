package web

import (
	"net/http"
	"strings"
)

// RouteGroup groups routes under a shared prefix with shared middleware. It is a
// thin convenience over WebHandler.Handle: every registration is forwarded to
// the underlying handler with the accumulated prefix prepended and the group's
// middleware prepended to any per-route middleware.
type RouteGroup struct {
	handler    *WebHandler
	prefix     string
	middleware []Middleware
}

// Group creates a RouteGroup with the given prefix and optional middleware. A
// trailing slash on the prefix is trimmed so joining a route path that begins
// with "/" never produces a doubled slash.
func (h *WebHandler) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{
		handler:    h,
		prefix:     strings.TrimSuffix(prefix, "/"),
		middleware: middleware,
	}
}

// Handle registers a handler on the group, combining the group's middleware
// with any per-route middleware and prepending the group's prefix to the path.
// The method is forwarded to WebHandler.Handle unchanged, so groups inherit the
// same patterns: an empty method matches any method, and the ServeMux "/{$}"
// exact-match segment is a valid path.
func (g *RouteGroup) Handle(method, path string, handler http.HandlerFunc, middleware ...Middleware) {
	allMiddleware := append(g.middleware, middleware...)
	fullPath := g.prefix + path
	g.handler.Handle(method, fullPath, handler, allMiddleware...)
}

// Group creates a nested sub-group. The child accumulates the parent's prefix
// (with the child prefix's trailing slash trimmed) and the parent's middleware,
// so child routes run parent middleware first, then their own.
func (g *RouteGroup) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{
		handler:    g.handler,
		prefix:     g.prefix + strings.TrimSuffix(prefix, "/"),
		middleware: append(g.middleware, middleware...),
	}
}
