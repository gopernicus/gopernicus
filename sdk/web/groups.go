package web

import (
	"net/http"
	"strings"
)

// RouteGroup groups routes under a shared prefix with shared middleware.
type RouteGroup struct {
	handler    *WebHandler
	prefix     string
	middleware []Middleware
}

// Group creates a new RouteGroup with the given prefix and optional middleware.
func (h *WebHandler) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{
		handler:    h,
		prefix:     strings.TrimSuffix(prefix, "/"),
		middleware: middleware,
	}
}

// Handle registers a handler on the group with combined group + route middleware.
func (g *RouteGroup) Handle(method, path string, handler http.HandlerFunc, middleware ...Middleware) {
	allMiddleware := append(g.middleware, middleware...)
	fullPath := g.prefix + path
	g.handler.Handle(method, fullPath, handler, allMiddleware...)
}

// Group creates a nested sub-group with accumulated prefix and middleware.
func (g *RouteGroup) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{
		handler:    g.handler,
		prefix:     g.prefix + strings.TrimSuffix(prefix, "/"),
		middleware: append(g.middleware, middleware...),
	}
}
