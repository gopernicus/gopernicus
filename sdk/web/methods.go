package web

import "net/http"

func (h *WebHandler) GET(path string, handler http.HandlerFunc, middleware ...Middleware) {
	h.Handle("GET", path, handler, middleware...)
}

func (h *WebHandler) POST(path string, handler http.HandlerFunc, middleware ...Middleware) {
	h.Handle("POST", path, handler, middleware...)
}

func (h *WebHandler) PUT(path string, handler http.HandlerFunc, middleware ...Middleware) {
	h.Handle("PUT", path, handler, middleware...)
}

func (h *WebHandler) DELETE(path string, handler http.HandlerFunc, middleware ...Middleware) {
	h.Handle("DELETE", path, handler, middleware...)
}

func (h *WebHandler) PATCH(path string, handler http.HandlerFunc, middleware ...Middleware) {
	h.Handle("PATCH", path, handler, middleware...)
}

func (g *RouteGroup) GET(path string, handler http.HandlerFunc, middleware ...Middleware) {
	g.Handle("GET", path, handler, middleware...)
}

func (g *RouteGroup) POST(path string, handler http.HandlerFunc, middleware ...Middleware) {
	g.Handle("POST", path, handler, middleware...)
}

func (g *RouteGroup) PUT(path string, handler http.HandlerFunc, middleware ...Middleware) {
	g.Handle("PUT", path, handler, middleware...)
}

func (g *RouteGroup) DELETE(path string, handler http.HandlerFunc, middleware ...Middleware) {
	g.Handle("DELETE", path, handler, middleware...)
}

func (g *RouteGroup) PATCH(path string, handler http.HandlerFunc, middleware ...Middleware) {
	g.Handle("PATCH", path, handler, middleware...)
}
