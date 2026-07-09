package cms

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
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
	views   Views
	adminMW []web.Middleware
}

// WithViews sets the HTML rendering port. When unset (nil), the HTML surface is
// not registered — only the media byte endpoint mounts (FS3).
func WithViews(v Views) RouterOption {
	return func(c *routerConfig) { c.views = v }
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

// Mount registers CMS routes on the given registrar. Content routes are
// registry-driven: Mount iterates the registered content types and registers a
// generic admin CRUD set per type plus a public route per routable type, instead
// of hand-listing /posts…/pages…. Taxonomy/menus/media/contact routes are fixed.
// A nil cache disables public-page caching. adminMW wraps every admin route (the
// CRUD/management surface) and nothing public; a nil adminMW leaves admin routes
// ungated (current behavior).
//
// A nil views registers ONLY the media byte endpoint (GET /media/{id}/file) and
// returns — the entire HTML surface (public site + admin) is not mounted (FS3).
func Mount(r feature.RouteRegistrar, d Deps, views Views, cache cacher.Storer, adminMW []web.Middleware) {
	rt := feature.Methods{Next: r}
	md := NewMediaHandlers(d.Media, views)
	if views == nil {
		rt.GET("/media/{id}/file", md.Serve)
		return
	}

	eh := NewEntryHandlers(d.Entries, d.Taxo, d.Media, views)
	th := NewTermHandlers(d.Taxo, views)
	mn := NewMenuHandlers(d.Menus, views)
	ct := NewContactHandlers(d.Contact, views)
	pub := NewPublicHandlers(d.Entries, d.Menus, d.Taxo, d.Registry, views)

	// Public pages are cacheable (TTL); admin pages never are.
	var pubMW []web.Middleware
	if cache != nil {
		pubMW = append(pubMW, web.CachePages(cache, publicPageTTL))
	}

	// Public home.
	rt.GET("/{$}", pub.Home, pubMW...)

	// Registry-driven content routes. One generic admin CRUD set per type, plus a
	// public single route per routable type (hierarchical types flat at the root,
	// the rest under their plural). Only one flat (hierarchical, no RoutePrefix)
	// type can own the root "/{slug}" pattern; a second would need a RoutePrefix.
	rootFlatClaimed := false
	for _, t := range d.Registry.Types() {
		t := t
		base := "/" + t.AdminBase()
		rt.GET(base, func(w http.ResponseWriter, req *http.Request) { eh.List(w, req, t) }, adminMW...)
		rt.GET(base+"/new", func(w http.ResponseWriter, req *http.Request) { eh.New(w, req, t) }, adminMW...)
		rt.POST(base, func(w http.ResponseWriter, req *http.Request) { eh.Create(w, req, t) }, adminMW...)
		rt.GET(base+"/{id}/edit", func(w http.ResponseWriter, req *http.Request) { eh.Edit(w, req, t) }, adminMW...)
		rt.POST(base+"/{id}", func(w http.ResponseWriter, req *http.Request) { eh.Update(w, req, t) }, adminMW...)
		rt.POST(base+"/{id}/publish", func(w http.ResponseWriter, req *http.Request) { eh.Publish(w, req, t) }, adminMW...)
		rt.POST(base+"/{id}/unpublish", func(w http.ResponseWriter, req *http.Request) { eh.Unpublish(w, req, t) }, adminMW...)
		rt.POST(base+"/{id}/delete", func(w http.ResponseWriter, req *http.Request) { eh.Delete(w, req, t) }, adminMW...)

		if !t.Routable {
			continue
		}
		single := func(w http.ResponseWriter, req *http.Request) { pub.Single(w, req, t) }
		if pb := t.PublicBase(); pb != "" {
			rt.GET("/"+pb+"/{slug}", single, pubMW...)
		} else if !rootFlatClaimed {
			rt.GET("/{slug}", single, pubMW...)
			rootFlatClaimed = true
		}
	}

	// Taxonomy admin.
	rt.GET("/terms", th.List, adminMW...)
	rt.GET("/terms/new", th.New, adminMW...)
	rt.POST("/terms", th.Create, adminMW...)
	rt.GET("/terms/{id}/edit", th.Edit, adminMW...)
	rt.POST("/terms/{id}", th.Update, adminMW...)
	rt.POST("/terms/{id}/delete", th.Delete, adminMW...)

	// Public taxonomy archives.
	rt.GET("/category/{slug}", pub.Category, pubMW...)
	rt.GET("/tag/{slug}", pub.Tag, pubMW...)

	// Menus (admin) + a public nav render by slug.
	rt.GET("/menus", mn.List, adminMW...)
	rt.GET("/menus/new", mn.New, adminMW...)
	rt.POST("/menus", mn.Create, adminMW...)
	rt.GET("/menus/{id}", mn.Detail, adminMW...)
	rt.POST("/menus/{id}/items", mn.AddItem, adminMW...)
	rt.GET("/menu-items/{id}/edit", mn.EditItem, adminMW...)
	rt.POST("/menu-items/{id}", mn.UpdateItem, adminMW...)
	rt.POST("/menu-items/{id}/delete", mn.DeleteItem, adminMW...)
	rt.GET("/menu/{slug}", mn.PublicNav)

	// Media management (admin) + public asset serving.
	rt.GET("/media", md.Library, adminMW...)
	rt.POST("/media", md.Upload, adminMW...)
	rt.GET("/media/{id}/file", md.Serve)
	rt.POST("/media/{id}/delete", md.Delete, adminMW...)

	// Contact form (public) + inquiry list (admin).
	rt.GET("/contact", ct.Form)
	rt.POST("/contact", ct.Submit)
	rt.GET("/inquiries", ct.Inquiries, adminMW...)
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
	}, cfg.views, cache, cfg.adminMW)

	return h
}
