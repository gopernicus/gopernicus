package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Middleware wraps an http.Handler. Compatible with any standard Go middleware.
type Middleware func(http.Handler) http.Handler

// WebHandler is an HTTP handler built on the standard library ServeMux
// with support for global and per-route middleware.
type WebHandler struct {
	mux              *http.ServeMux
	log              *slog.Logger
	globalMiddleware []Middleware
}

// HandlerOption configures a WebHandler.
type HandlerOption func(*WebHandler)

// WithLogging sets the logger.
func WithLogging(log *slog.Logger) HandlerOption {
	return func(h *WebHandler) {
		h.log = log
	}
}

// WithCORS adds CORS middleware for the given origins.
func WithCORS(origins []string) HandlerOption {
	return func(h *WebHandler) {
		h.globalMiddleware = append(h.globalMiddleware, CORSMiddleware(origins))
	}
}

// WithDefaultHeaders adds middleware that sets default response headers.
func WithDefaultHeaders(headers map[string]string) HandlerOption {
	return func(h *WebHandler) {
		h.globalMiddleware = append(h.globalMiddleware, DefaultHeadersMiddleware(headers))
	}
}

// NewWebHandler creates a new WebHandler with the given options.
func NewWebHandler(opts ...HandlerOption) *WebHandler {
	h := &WebHandler{
		mux: http.NewServeMux(),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Use adds global middleware. Must be called before registering routes.
func (h *WebHandler) Use(middleware ...Middleware) {
	h.globalMiddleware = append(h.globalMiddleware, middleware...)
}

// Handle registers a handler for the given method and path with optional
// per-route middleware. Global middleware is applied first, then route middleware.
func (h *WebHandler) Handle(method, path string, handler http.HandlerFunc, middleware ...Middleware) {
	allMiddleware := append(h.globalMiddleware, middleware...)

	var final http.Handler = handler
	for i := len(allMiddleware) - 1; i >= 0; i-- {
		final = allMiddleware[i](final)
	}

	pattern := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
	h.mux.Handle(pattern, final)
}

// HandleRaw registers a raw handler, bypassing all middleware.
func (h *WebHandler) HandleRaw(pattern string, handler http.Handler) {
	h.mux.Handle(pattern, handler)
}

// ServeHTTP delegates to the underlying ServeMux.
func (h *WebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}
