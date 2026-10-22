package cms

import (
	"log/slog"
	"net/http"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RouterOption customizes router construction. It is a convenience for the
// standalone BuildRouter helper (used by tests); the pocket's own Register
// path passes overrides through cms.Config instead.
type RouterOption func(*routerConfig)

// routerConfig collects host overrides applied during BuildRouter.
type routerConfig struct {
	views        Views
	adminMW      []web.Middleware
	adminUngated bool
	pageCache    cacher.PageConfig
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

// WithPageCache configures public-page caching in the standalone router.
// Pocket hosts use cms.Config.PageCache instead.
func WithPageCache(cfg cacher.PageConfig) RouterOption {
	return func(c *routerConfig) { c.pageCache = cfg }
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
// CRUD/management surface) and nothing public. adminEnabled false leaves every
// admin route unregistered (deny-by-absence); admin routes register only through
// handleAdmin, so none can be added outside that switch.
//
// A nil views registers ONLY the media byte endpoint (GET /media/{id}/file) and
// returns — the entire HTML surface (public site + admin) is not mounted (FS3).
func Mount(r pockets.RouteRegistrar, d Deps, views Views, cache cacher.Storer, pageCache cacher.PageConfig, adminMW []web.Middleware, adminEnabled bool) {
	md := NewMediaHandlers(d.Media, views)
	if views == nil {
		r.Handle("GET", "/media/{id}/file", md.Serve)
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
		pubMW = append(pubMW, cacher.Pages(cache, pageCache))
	}

	handleAdmin := func(method, path string, h http.HandlerFunc) {
		if !adminEnabled {
			return
		}
		r.Handle(method, path, h, adminMW...)
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
		handleAdmin("GET", base, func(w http.ResponseWriter, req *http.Request) { eh.List(w, req, t) })
		handleAdmin("GET", base+"/new", func(w http.ResponseWriter, req *http.Request) { eh.New(w, req, t) })
		handleAdmin("POST", base, func(w http.ResponseWriter, req *http.Request) { eh.Create(w, req, t) })
		handleAdmin("GET", base+"/{id}/edit", func(w http.ResponseWriter, req *http.Request) { eh.Edit(w, req, t) })
		handleAdmin("POST", base+"/{id}", func(w http.ResponseWriter, req *http.Request) { eh.Update(w, req, t) })
		handleAdmin("POST", base+"/{id}/publish", func(w http.ResponseWriter, req *http.Request) { eh.Publish(w, req, t) })
		handleAdmin("POST", base+"/{id}/unpublish", func(w http.ResponseWriter, req *http.Request) { eh.Unpublish(w, req, t) })
		handleAdmin("POST", base+"/{id}/delete", func(w http.ResponseWriter, req *http.Request) { eh.Delete(w, req, t) })

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
	handleAdmin("GET", "/terms", th.List)
	handleAdmin("GET", "/terms/new", th.New)
	handleAdmin("POST", "/terms", th.Create)
	handleAdmin("GET", "/terms/{id}/edit", th.Edit)
	handleAdmin("POST", "/terms/{id}", th.Update)
	handleAdmin("POST", "/terms/{id}/delete", th.Delete)

	// Public taxonomy archives.
	r.Handle("GET", "/category/{slug}", pub.Category, pubMW...)
	r.Handle("GET", "/tag/{slug}", pub.Tag, pubMW...)

	// Menus (admin) + a public nav render by slug.
	handleAdmin("GET", "/menus", mn.List)
	handleAdmin("GET", "/menus/new", mn.New)
	handleAdmin("POST", "/menus", mn.Create)
	handleAdmin("GET", "/menus/{id}", mn.Detail)
	handleAdmin("POST", "/menus/{id}/items", mn.AddItem)
	handleAdmin("GET", "/menu-items/{id}/edit", mn.EditItem)
	handleAdmin("POST", "/menu-items/{id}", mn.UpdateItem)
	handleAdmin("POST", "/menu-items/{id}/delete", mn.DeleteItem)
	r.Handle("GET", "/menu/{slug}", mn.PublicNav)

	// Media management (admin) + public asset serving.
	handleAdmin("GET", "/media", md.Library)
	handleAdmin("POST", "/media", md.Upload)
	r.Handle("GET", "/media/{id}/file", md.Serve)
	handleAdmin("POST", "/media/{id}/delete", md.Delete)

	// Contact form (public) + inquiry list (admin).
	r.Handle("GET", "/contact", ct.Form)
	r.Handle("POST", "/contact", ct.Submit)
	handleAdmin("GET", "/inquiries", ct.Inquiries)
}

// BuildRouter assembles a standalone CMS router with the global middleware stack
// (request-id, request logger, panic recovery). It is a convenience for tests
// and standalone use; the host composition path uses cms.Register + Mount.
func BuildRouter(registry *content.Registry, entries entryService, taxo taxonomyService, menusvc menuService, mediasvc mediaService, contact messagingService, cache cacher.Storer, log *slog.Logger, opts ...RouterOption) http.Handler {
	var cfg routerConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	h := web.NewWebHandler()
	h.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	Mount(h, Deps{
		Registry: registry,
		Entries:  entries,
		Taxo:     taxo,
		Menus:    menusvc,
		Media:    mediasvc,
		Contact:  contact,
	}, cfg.views, cache, cfg.pageCache, cfg.adminMW, len(cfg.adminMW) > 0 || cfg.adminUngated)

	return h
}
