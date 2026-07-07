package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/theme"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// publicPageTTL is how long rendered public pages stay cached.
const publicPageTTL = 60 * time.Second

// RouterOption customizes router construction. It is a convenience for the
// standalone BuildRouter helper (used by tests); the feature's own Register
// path passes overrides through cms.Config instead.
type RouterOption func(*routerConfig)

// routerConfig collects host overrides applied during BuildRouter.
type routerConfig struct {
	publicViews theme.PublicViews
	adminMW     []web.Middleware
}

// WithPublicViews overrides the public-site chrome theme. When unset, the
// bundled default chrome is used.
func WithPublicViews(v theme.PublicViews) RouterOption {
	return func(c *routerConfig) { c.publicViews = v }
}

// WithAdminMiddleware wraps every admin route with the given middleware (see
// Mount's adminMW parameter). Public routes are never wrapped.
func WithAdminMiddleware(mw ...web.Middleware) RouterOption {
	return func(c *routerConfig) { c.adminMW = mw }
}

// Deps are the domain services + registry the CMS handlers need. cms.Register
// builds the Registry and services and hands them here.
type Deps struct {
	Registry *content.Registry
	Entries  entryService
	Taxo     taxonomyService
	Menus    menuService
	Media    mediaService
	Contact  messagingService
}

// Mount registers all CMS routes on the given registrar. Content routes are
// registry-driven: Mount iterates the registered content types and registers a
// generic admin CRUD set per type plus a public route per routable type, instead
// of hand-listing /posts…/pages…. Taxonomy/menus/media/contact routes are fixed.
// A nil views uses the bundled chrome; a nil cache disables public-page caching.
// adminMW wraps every admin route (the CRUD/management surface) and nothing
// public; a nil adminMW leaves admin routes ungated (current behavior).
func Mount(r feature.RouteRegistrar, d Deps, views theme.PublicViews, cache cacher.Storer, adminMW []web.Middleware) {
	if views == nil {
		views = theme.Default()
	}

	eh := NewEntryHandlers(d.Entries, d.Taxo, d.Media)
	th := NewTermHandlers(d.Taxo)
	mn := NewMenuHandlers(d.Menus)
	md := NewMediaHandlers(d.Media)
	ct := NewContactHandlers(d.Contact)
	pub := NewPublicHandlers(d.Entries, d.Menus, d.Taxo, d.Registry, views)

	// Public pages are cacheable (TTL); admin pages never are.
	var pubMW []web.Middleware
	if cache != nil {
		pubMW = append(pubMW, web.CachePages(cache, publicPageTTL))
	}

	// Public home.
	r.Handle("GET", "/{$}", pub.Home, pubMW...)

	// Registry-driven content routes. One generic admin CRUD set per type, plus a
	// public single route per routable type (hierarchical types flat at the root,
	// the rest under their plural). Only one flat (hierarchical, no RoutePrefix)
	// type can own the root "/{slug}" pattern; a second would need a RoutePrefix.
	rootFlatClaimed := false
	for _, t := range d.Registry.Types() {
		t := t
		base := "/" + t.AdminBase()
		r.Handle("GET", base, func(w http.ResponseWriter, req *http.Request) { eh.List(w, req, t) }, adminMW...)
		r.Handle("GET", base+"/new", func(w http.ResponseWriter, req *http.Request) { eh.New(w, req, t) }, adminMW...)
		r.Handle("POST", base, func(w http.ResponseWriter, req *http.Request) { eh.Create(w, req, t) }, adminMW...)
		r.Handle("GET", base+"/{id}/edit", func(w http.ResponseWriter, req *http.Request) { eh.Edit(w, req, t) }, adminMW...)
		r.Handle("POST", base+"/{id}", func(w http.ResponseWriter, req *http.Request) { eh.Update(w, req, t) }, adminMW...)
		r.Handle("POST", base+"/{id}/publish", func(w http.ResponseWriter, req *http.Request) { eh.Publish(w, req, t) }, adminMW...)
		r.Handle("POST", base+"/{id}/unpublish", func(w http.ResponseWriter, req *http.Request) { eh.Unpublish(w, req, t) }, adminMW...)
		r.Handle("POST", base+"/{id}/delete", func(w http.ResponseWriter, req *http.Request) { eh.Delete(w, req, t) }, adminMW...)

		if !t.Routable {
			continue
		}
		single := func(w http.ResponseWriter, req *http.Request) { pub.Single(w, req, t) }
		if pb := t.PublicBase(); pb != "" {
			r.Handle("GET", "/"+pb+"/{slug}", single, pubMW...)
		} else if !rootFlatClaimed {
			r.Handle("GET", "/{slug}", single, pubMW...)
			rootFlatClaimed = true
		}
	}

	// Taxonomy admin.
	r.Handle("GET", "/terms", th.List, adminMW...)
	r.Handle("GET", "/terms/new", th.New, adminMW...)
	r.Handle("POST", "/terms", th.Create, adminMW...)
	r.Handle("GET", "/terms/{id}/edit", th.Edit, adminMW...)
	r.Handle("POST", "/terms/{id}", th.Update, adminMW...)
	r.Handle("POST", "/terms/{id}/delete", th.Delete, adminMW...)

	// Public taxonomy archives.
	r.Handle("GET", "/category/{slug}", pub.Category, pubMW...)
	r.Handle("GET", "/tag/{slug}", pub.Tag, pubMW...)

	// Menus (admin) + a public nav render by slug.
	r.Handle("GET", "/menus", mn.List, adminMW...)
	r.Handle("GET", "/menus/new", mn.New, adminMW...)
	r.Handle("POST", "/menus", mn.Create, adminMW...)
	r.Handle("GET", "/menus/{id}", mn.Detail, adminMW...)
	r.Handle("POST", "/menus/{id}/items", mn.AddItem, adminMW...)
	r.Handle("GET", "/menu-items/{id}/edit", mn.EditItem, adminMW...)
	r.Handle("POST", "/menu-items/{id}", mn.UpdateItem, adminMW...)
	r.Handle("POST", "/menu-items/{id}/delete", mn.DeleteItem, adminMW...)
	r.Handle("GET", "/menu/{slug}", mn.PublicNav)

	// Media management (admin) + public asset serving.
	r.Handle("GET", "/media", md.Library, adminMW...)
	r.Handle("POST", "/media", md.Upload, adminMW...)
	r.Handle("GET", "/media/{id}/file", md.Serve)
	r.Handle("POST", "/media/{id}/delete", md.Delete, adminMW...)

	// Contact form (public) + inquiry list (admin).
	r.Handle("GET", "/contact", ct.Form)
	r.Handle("POST", "/contact", ct.Submit)
	r.Handle("GET", "/inquiries", ct.Inquiries, adminMW...)
}

// BuildRouter assembles a standalone CMS router with the global middleware stack
// (request-id, request logger, panic recovery). It is a convenience for tests
// and standalone use; the host composition path uses cms.Register + Mount.
func BuildRouter(registry *content.Registry, entries entryService, taxo taxonomyService, menusvc menuService, mediasvc mediaService, contact messagingService, cache cacher.Storer, log *slog.Logger, opts ...RouterOption) http.Handler {
	var cfg routerConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	h := web.NewWebHandler(web.WithLogging(log))
	h.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	Mount(h, Deps{
		Registry: registry,
		Entries:  entries,
		Taxo:     taxo,
		Menus:    menusvc,
		Media:    mediasvc,
		Contact:  contact,
	}, cfg.publicViews, cache, cfg.adminMW)

	return h
}
