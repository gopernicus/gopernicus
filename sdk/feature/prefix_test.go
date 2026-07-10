package feature

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// PrefixRegistrar must keep satisfying RouteRegistrar structurally — it is
// itself a valid Mount.Router value.
var _ RouteRegistrar = PrefixRegistrar{}

// Group is likewise a RouteRegistrar, so a host can pass it as Mount.Router.
var _ RouteRegistrar = Group{}

// capturingRegistrar records every registration in order (re-homed from the
// deleted route_test.go — phase 02 task-2 — because these tests consume it).
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

func TestPrefixRegistrar_Handle(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{"prefix without trailing slash", "/blog", "/{$}", "/blog/{$}"},
		{"prefix with trailing slash", "/blog/", "/{$}", "/blog/{$}"},
		{"root prefix is a no-op", "/", "/{$}", "/{$}"},
		{"empty prefix is a no-op", "", "/{$}", "/{$}"},
		{"prefix missing leading slash is normalized", "blog", "/{$}", "/blog/{$}"},
		{"bare root path (subtree pattern)", "/blog", "/", "/blog/"},
		{"wildcard segments pass through untouched", "/blog", "/terms/{id}/edit", "/blog/terms/{id}/edit"},
		{"nested prefix", "/x/y", "/widgets", "/x/y/widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := &recordingRegistrar{}
			p := PrefixRegistrar{Prefix: tt.prefix, Next: next}

			p.Handle(http.MethodGet, tt.path, func(w http.ResponseWriter, r *http.Request) {})

			want := "GET " + tt.want
			if len(next.calls) != 1 || next.calls[0] != want {
				t.Errorf("Handle(%q under prefix %q) recorded %v, want [%q]", tt.path, tt.prefix, next.calls, want)
			}
		})
	}
}

func TestPrefixRegistrar_MethodPassesThroughUnchanged(t *testing.T) {
	next := &recordingRegistrar{}
	p := PrefixRegistrar{Prefix: "/admin", Next: next}

	p.Handle(http.MethodPost, "/widgets/{id}/publish", func(w http.ResponseWriter, r *http.Request) {})

	want := "POST /admin/widgets/{id}/publish"
	if len(next.calls) != 1 || next.calls[0] != want {
		t.Errorf("calls = %v, want [%q]", next.calls, want)
	}
}

func TestGroup_Handle_PrefixesPath(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{"prefix without trailing slash", "/admin", "/{$}", "/admin/{$}"},
		{"prefix with trailing slash", "/admin/", "/{$}", "/admin/{$}"},
		{"root prefix is a no-op", "/", "/{$}", "/{$}"},
		{"empty prefix is a no-op", "", "/{$}", "/{$}"},
		{"prefix missing leading slash is normalized", "admin", "/{$}", "/admin/{$}"},
		{"bare root path (subtree pattern)", "/admin", "/", "/admin/"},
		{"wildcard segments pass through untouched", "/admin", "/users/{id}/edit", "/admin/users/{id}/edit"},
		{"nested prefix", "/x/y", "/widgets", "/x/y/widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := &recordingRegistrar{}
			g := Group{Prefix: tt.prefix, Next: next}

			g.Handle(http.MethodGet, tt.path, func(w http.ResponseWriter, r *http.Request) {})

			want := "GET " + tt.want
			if len(next.calls) != 1 || next.calls[0] != want {
				t.Errorf("Handle(%q under prefix %q) recorded %v, want [%q]", tt.path, tt.prefix, next.calls, want)
			}
		})
	}
}

func TestGroup_Handle_CombinesMiddlewareGroupFirst(t *testing.T) {
	next := &capturingRegistrar{}
	var order []string
	tag := func(label string) web.Middleware {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, label)
				h.ServeHTTP(w, r)
			})
		}
	}
	g := Group{
		Prefix:     "/admin",
		Middleware: []web.Middleware{tag("group-a"), tag("group-b")},
		Next:       next,
	}

	g.Handle(http.MethodGet, "/users", func(w http.ResponseWriter, r *http.Request) {}, tag("route"))

	if len(next.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(next.calls))
	}
	mw := next.calls[0].middleware
	if len(mw) != 3 {
		t.Fatalf("combined middleware count = %d, want 3", len(mw))
	}

	// Compose outermost-first (index 0 is the outer wrapper), exactly as
	// web.WebHandler applies a middleware slice, and run it to observe order.
	var final http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for i := len(mw) - 1; i >= 0; i-- {
		final = mw[i](final)
	}
	final.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"group-a", "group-b", "route"}
	if len(order) != len(want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("execution order = %v, want %v", order, want)
			break
		}
	}
}

func TestGroup_Handle_DoesNotMutateGroupMiddlewareAcrossCalls(t *testing.T) {
	next := &capturingRegistrar{}
	shared := func(h http.Handler) http.Handler { return h }
	g := Group{Prefix: "/admin", Middleware: []web.Middleware{shared}, Next: next}
	routeMW := func(h http.Handler) http.Handler { return h }

	g.Handle(http.MethodGet, "/a", func(w http.ResponseWriter, r *http.Request) {}, routeMW)
	g.Handle(http.MethodGet, "/b", func(w http.ResponseWriter, r *http.Request) {})

	if n := len(g.Middleware); n != 1 {
		t.Errorf("group Middleware len = %d after registrations, want 1 (unchanged)", n)
	}
	if n := len(next.calls[1].middleware); n != 1 {
		t.Errorf("second route middleware count = %d, want 1 (no leak from first route)", n)
	}
}
