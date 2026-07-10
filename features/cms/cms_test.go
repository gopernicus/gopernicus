package cms

import (
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// recordingRegistrar captures the routes a feature mounts, standing in for the
// host's real router. It implements feature.RouteRegistrar.
type recordingRegistrar struct{ routes map[string]bool }

func (r *recordingRegistrar) Handle(method, path string, _ http.HandlerFunc, _ ...web.Middleware) {
	r.routes[method+" "+path] = true
}

// stubViews is a nil-rendering Views used to exercise Register's full HTML route
// set without importing the bundled default (which would cycle: views/templ
// imports cms). Handlers are never invoked here, so the renderers can be nil.
type stubViews struct{}

func (stubViews) Home([]menus.MenuItem, []ListItem) web.Renderer { return nil }
func (stubViews) Archive(string, []menus.MenuItem, []ListItem, Pager) web.Renderer {
	return nil
}
func (stubViews) Single(string, string, []menus.MenuItem, web.Renderer) web.Renderer { return nil }
func (stubViews) Error(int, string) web.Renderer                                     { return nil }
func (stubViews) ContactForm(ContactModel) web.Renderer                              { return nil }
func (stubViews) ContactThanks() web.Renderer                                        { return nil }
func (stubViews) MenuNav(menus.Menu, []menus.MenuItem) web.Renderer                  { return nil }
func (stubViews) EntriesList(string, string, string, []EntryListItem, Pager) web.Renderer {
	return nil
}
func (stubViews) EntryForm(EntryFormModel) web.Renderer                   { return nil }
func (stubViews) TermsList([]taxonomy.Term, []taxonomy.Term) web.Renderer { return nil }
func (stubViews) TermForm(TermFormModel) web.Renderer                     { return nil }
func (stubViews) MenusList([]menus.Menu) web.Renderer                     { return nil }
func (stubViews) MenuNew(string) web.Renderer                             { return nil }
func (stubViews) MenuDetail(menus.Menu, []menus.MenuItem) web.Renderer    { return nil }
func (stubViews) MenuItemForm(menus.MenuItem) web.Renderer                { return nil }
func (stubViews) MediaLibrary([]media.Asset, string) web.Renderer         { return nil }
func (stubViews) InquiriesList([]messaging.Inquiry) web.Renderer          { return nil }
func (stubViews) AdminError(int, string) web.Renderer                     { return nil }
func (stubViews) SeedTemplates() []content.TemplateBinding                { return nil }

// TestRegister_MountsRouteSet verifies the public composition path: cms.Register
// wires services from repositories and mounts the feature's full route set on the
// host's RouteRegistrar. Repositories are nil because no handler is invoked here
// — services are constructed (passthrough) but never called, so this exercises
// the Register→Mount wiring without a datastore.
func TestRegister_MountsRouteSet(t *testing.T) {
	rec := &recordingRegistrar{routes: map[string]bool{}}

	err := Register(feature.Mount{Router: rec}, Repositories{}, Config{Views: stubViews{}})
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
