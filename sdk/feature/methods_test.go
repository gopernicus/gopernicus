package feature

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// captureRegistrar records full registrations including the middleware slice
// (the shared recordingRegistrar in feature_test.go deliberately discards it).
type captureRegistrar struct {
	method, path string
	middleware   []web.Middleware
	calls        int
}

func (c *captureRegistrar) Handle(method, path string, _ http.HandlerFunc, middleware ...web.Middleware) {
	c.method, c.path, c.middleware = method, path, middleware
	c.calls++
}

func TestMethods_VerbsDelegateToHandle(t *testing.T) {
	rec := &captureRegistrar{}
	m := Methods{Next: rec}
	h := func(http.ResponseWriter, *http.Request) {}

	for _, tc := range []struct {
		verb string
		call func(string, http.HandlerFunc, ...web.Middleware)
	}{
		{"GET", m.GET},
		{"POST", m.POST},
		{"PUT", m.PUT},
		{"DELETE", m.DELETE},
		{"PATCH", m.PATCH},
	} {
		tc.call("/things/{id}", h)
		if rec.method != tc.verb {
			t.Errorf("%s registered method %q", tc.verb, rec.method)
		}
		if rec.path != "/things/{id}" {
			t.Errorf("%s registered path %q", tc.verb, rec.path)
		}
	}
	if rec.calls != 5 {
		t.Fatalf("expected 5 registrations, got %d", rec.calls)
	}
}

func TestMethods_Handle_DelegatesUnchanged(t *testing.T) {
	rec := &captureRegistrar{}
	m := Methods{Next: rec}
	m.Handle("OPTIONS", "/raw", func(http.ResponseWriter, *http.Request) {})
	if rec.method != "OPTIONS" || rec.path != "/raw" {
		t.Fatalf("Handle delegated as %s %s", rec.method, rec.path)
	}
}

func TestMethods_MiddlewarePassesThroughInOrder(t *testing.T) {
	rec := &captureRegistrar{}
	m := Methods{Next: rec}

	var applied []string
	mark := func(name string) web.Middleware {
		return func(next http.Handler) http.Handler {
			applied = append(applied, name)
			return next
		}
	}

	m.POST("/x", func(http.ResponseWriter, *http.Request) {}, mark("a"), mark("b"))
	if len(rec.middleware) != 2 {
		t.Fatalf("expected 2 middleware forwarded, got %d", len(rec.middleware))
	}
	for _, mw := range rec.middleware {
		mw(http.NotFoundHandler())
	}
	if len(applied) != 2 || applied[0] != "a" || applied[1] != "b" {
		t.Fatalf("middleware order not preserved: %v", applied)
	}
}

// Methods composes on either side of the host's wrappers: here it registers
// through a Group (prefix + shared middleware) that itself delegates through
// a PrefixRegistrar, and the innermost recorder sees the fully-wrapped route.
func TestMethods_ComposesWithGroupAndPrefix(t *testing.T) {
	rec := &captureRegistrar{}
	groupMW := func(next http.Handler) http.Handler { return next }
	m := Methods{Next: Group{
		Prefix:     "/admin",
		Middleware: []web.Middleware{groupMW},
		Next:       PrefixRegistrar{Prefix: "/blog", Next: rec},
	}}

	m.GET("/terms", func(http.ResponseWriter, *http.Request) {}, func(next http.Handler) http.Handler { return next })

	if rec.method != "GET" || rec.path != "/blog/admin/terms" {
		t.Fatalf("composed registration was %s %s", rec.method, rec.path)
	}
	if len(rec.middleware) != 2 {
		t.Fatalf("expected group + route middleware (2), got %d", len(rec.middleware))
	}
}

// verbHelperSet collects methods whose signature is the verb-helper shape —
// func(path string, handler http.HandlerFunc, middleware ...web.Middleware) —
// excluding Handle (different arity) by shape alone.
func verbHelperSet(typ reflect.Type) map[string]bool {
	verbs := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		ft := m.Func.Type() // In(0) is the receiver
		if ft.NumIn() == 4 && ft.IsVariadic() && ft.NumOut() == 0 &&
			ft.In(1) == reflect.TypeFor[string]() &&
			ft.In(2) == reflect.TypeFor[http.HandlerFunc]() &&
			ft.In(3) == reflect.TypeFor[[]web.Middleware]() {
			verbs[m.Name] = true
		}
	}
	return verbs
}

// TestMethods_VerbParityWithWebHandler pins D3 (segovia-lessons phase 02):
// feature.Methods mirrors web.WebHandler's verb-helper set one-to-one. If
// web.WebHandler grows or loses a verb helper, this test fails until Methods
// changes in the same commit.
func TestMethods_VerbParityWithWebHandler(t *testing.T) {
	got := verbHelperSet(reflect.TypeFor[Methods]())
	want := verbHelperSet(reflect.TypeFor[*web.WebHandler]())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verb parity broken (D3): feature.Methods has %v, web.WebHandler has %v", got, want)
	}
}
