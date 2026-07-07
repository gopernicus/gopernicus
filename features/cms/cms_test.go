package cms

import (
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// recordingRegistrar captures the routes a feature mounts, standing in for the
// host's real router. It implements feature.RouteRegistrar.
type recordingRegistrar struct{ routes map[string]bool }

func (r *recordingRegistrar) Handle(method, path string, _ http.HandlerFunc, _ ...web.Middleware) {
	r.routes[method+" "+path] = true
}

// TestRegister_MountsRouteSet verifies the public composition path: cms.Register
// wires services from repositories and mounts the feature's full route set on the
// host's RouteRegistrar. Repositories are nil because no handler is invoked here
// — services are constructed (passthrough) but never called, so this exercises
// the Register→Mount wiring without a datastore.
func TestRegister_MountsRouteSet(t *testing.T) {
	rec := &recordingRegistrar{routes: map[string]bool{}}

	err := Register(feature.Mount{Router: rec}, Repositories{}, Config{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// A representative slice across the feature's surfaces: registry-driven content
	// routes (Article + Page seed types), public site, admin, and the folded-in
	// contact form (decision 1 — messaging is part of CMS).
	want := []string{
		"GET /{$}",                // public home
		"GET /articles",           // admin article list (registry-driven)
		"POST /articles",          // admin article create
		"GET /articles/{id}/edit", // admin article editor
		"GET /articles/{slug}",    // public article (plural-prefixed)
		"GET /pages",              // admin page list
		"GET /{slug}",             // public page (hierarchical, flat at root)
		"GET /menu/{slug}",        // public nav
		"GET /category/{slug}",    // public taxonomy archive
		"GET /contact",            // contact form (folded-in messaging)
		"POST /contact",           // contact submit
		"GET /inquiries",          // inquiry admin
	}
	for _, route := range want {
		if !rec.routes[route] {
			t.Errorf("Register did not mount route %q", route)
		}
	}
	if len(rec.routes) == 0 {
		t.Fatal("Register mounted no routes")
	}
}
