package feature

import (
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// Methods wraps any RouteRegistrar with the method-named helpers, so a
// feature's routes.go reads like an app-local domain's registration against
// the concrete web.WebHandler (segovia-lessons phase 02, flag #4). It changes
// nothing about the seam: every verb delegates to Next.Handle, so whatever
// the host supplied — its bare router, a PrefixRegistrar, a Group, or its own
// per-route override wrapper — sees exactly the registrations it would have
// seen through stringly Handle calls. Methods itself implements
// RouteRegistrar, so it composes on either side of those wrappers.
//
// The verb set is PARITY with web.WebHandler's helpers (methods.go), never
// coverage of HTTP (D3, 2026-07-08): no HEAD (WebHandler.Handle emits
// method-scoped "GET /path" patterns and Go 1.22+ ServeMux matches HEAD
// against GET registrations), no OPTIONS (CORS preflight is middleware, not
// a route-table concern), no CONNECT/TRACE. If web.WebHandler ever grows a
// verb helper, Methods grows it in the same commit — the parity test in
// methods_test.go pins the two sets to each other.
//
// See features/README.md §4 for the host-side override story the underlying
// RouteRegistrar seam exists to serve.
type Methods struct{ Next RouteRegistrar }

// Handle delegates to Next unchanged, making Methods a RouteRegistrar.
func (m Methods) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle(method, path, handler, middleware...)
}

func (m Methods) GET(path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle("GET", path, handler, middleware...)
}

func (m Methods) POST(path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle("POST", path, handler, middleware...)
}

func (m Methods) PUT(path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle("PUT", path, handler, middleware...)
}

func (m Methods) DELETE(path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle("DELETE", path, handler, middleware...)
}

func (m Methods) PATCH(path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	m.Next.Handle("PATCH", path, handler, middleware...)
}
