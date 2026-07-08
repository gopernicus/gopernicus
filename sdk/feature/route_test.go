package feature

import (
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// capturingRegistrar is a fake RouteRegistrar that records each Handle call
// including the per-route middleware, so tests can assert both the path a route
// lands on and the middleware stack it carries.
type capturingRegistrar struct {
	calls []capturedCall
}

type capturedCall struct {
	method     string
	path       string
	middleware []web.Middleware
}

func (c *capturingRegistrar) Handle(method, path string, _ http.HandlerFunc, middleware ...web.Middleware) {
	c.calls = append(c.calls, capturedCall{method: method, path: path, middleware: middleware})
}

func TestRegisterRoutes_ForwardsEachRouteInOrder(t *testing.T) {
	reg := &capturingRegistrar{}
	routes := []Route{
		{Name: "widgets.list", Method: http.MethodGet, Path: "/widgets"},
		{Name: "widgets.create", Method: http.MethodPost, Path: "/widgets"},
		{Name: "widgets.show", Method: http.MethodGet, Path: "/widgets/{id}"},
	}

	RegisterRoutes(reg, routes)

	want := []string{"GET /widgets", "POST /widgets", "GET /widgets/{id}"}
	if len(reg.calls) != len(want) {
		t.Fatalf("recorded %d calls, want %d", len(reg.calls), len(want))
	}
	for i, w := range want {
		if got := reg.calls[i].method + " " + reg.calls[i].path; got != w {
			t.Errorf("call %d = %q, want %q", i, got, w)
		}
	}
}

func TestRegisterRoutes_ForwardsRouteMiddleware(t *testing.T) {
	reg := &capturingRegistrar{}
	mw := func(next http.Handler) http.Handler { return next }
	routes := []Route{
		{Name: "guarded", Method: http.MethodGet, Path: "/guarded", Middleware: []web.Middleware{mw, mw}},
		{Name: "open", Method: http.MethodGet, Path: "/open"},
	}

	RegisterRoutes(reg, routes)

	if n := len(reg.calls[0].middleware); n != 2 {
		t.Errorf("guarded route middleware count = %d, want 2", n)
	}
	if n := len(reg.calls[1].middleware); n != 0 {
		t.Errorf("open route middleware count = %d, want 0", n)
	}
}

func TestRegisterRoutes_EmptyTableIsANoOp(t *testing.T) {
	reg := &capturingRegistrar{}

	RegisterRoutes(reg, nil)

	if len(reg.calls) != 0 {
		t.Errorf("recorded %d calls for an empty table, want 0", len(reg.calls))
	}
}
