package cms

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// adminNavMarker is the admin-chrome slot the bundled goth views render on every
// admin page (pockets/cms/views/goth/layout.templ adminNav).
const adminNavMarker = `data-slot="cms-admin-nav"`

// routeRecorder is a counting middleware: it records, per request, the method+
// path it was invoked on. Wiring it as AdminMiddleware lets a test prove the
// hook wraps exactly the admin surface and nothing public.
type routeRecorder struct {
	mu   sync.Mutex
	hits map[string]int
}

func newRouteRecorder() *routeRecorder { return &routeRecorder{hits: map[string]int{}} }

func (rr *routeRecorder) middleware() web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr.mu.Lock()
			rr.hits[r.Method+" "+r.URL.Path]++
			rr.mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}
}

func (rr *routeRecorder) count(method, path string) int {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.hits[method+" "+path]
}

// adminRoutes are the concrete requests that must pass through AdminMiddleware.
// They cover the registry-driven CRUD set (article + page seeds) plus the fixed
// taxonomy/menus/media-management/inquiry admin surface.
var adminRoutes = []struct{ method, path string }{
	// registry-driven per-type CRUD (article seed)
	{"GET", "/articles"},
	{"GET", "/articles/new"},
	{"POST", "/articles"},
	{"GET", "/articles/x1/edit"},
	{"POST", "/articles/x1"},
	{"POST", "/articles/x1/publish"},
	{"POST", "/articles/x1/unpublish"},
	{"POST", "/articles/x1/delete"},
	// registry-driven per-type CRUD (page seed)
	{"GET", "/pages"},
	{"GET", "/pages/new"},
	{"POST", "/pages"},
	{"GET", "/pages/p1/edit"},
	{"POST", "/pages/p1"},
	{"POST", "/pages/p1/publish"},
	{"POST", "/pages/p1/unpublish"},
	{"POST", "/pages/p1/delete"},
	// taxonomy admin
	{"GET", "/terms"},
	{"GET", "/terms/new"},
	{"POST", "/terms"},
	{"GET", "/terms/t1/edit"},
	{"POST", "/terms/t1"},
	{"POST", "/terms/t1/delete"},
	// menus admin
	{"GET", "/menus"},
	{"GET", "/menus/new"},
	{"POST", "/menus"},
	{"GET", "/menus/m1"},
	{"POST", "/menus/m1/items"},
	{"GET", "/menu-items/i1/edit"},
	{"POST", "/menu-items/i1"},
	{"POST", "/menu-items/i1/delete"},
	// media management
	{"GET", "/media"},
	{"POST", "/media"},
	{"POST", "/media/a1/delete"},
	// inquiries admin
	{"GET", "/inquiries"},
}

// publicRoutes must NOT pass through AdminMiddleware: site pages, taxonomy
// archives, public nav render, public asset serving, and the contact form.
var publicRoutes = []struct{ method, path string }{
	{"GET", "/"},
	{"GET", "/articles/hello"}, // article public single
	{"GET", "/some-page"},      // page public single (flat root)
	{"GET", "/category/news"},
	{"GET", "/tag/go"},
	{"GET", "/menu/main"},     // public nav
	{"GET", "/media/a1/file"}, // public asset serving
	{"GET", "/contact"},       // contact form
	{"POST", "/contact"},      // contact submit
}

func newAdminRouter(rr *routeRecorder) http.Handler {
	return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, slog.New(slog.DiscardHandler), WithViews(stubViews{}), WithAdminMiddleware(rr.middleware()))
}

func TestAdminMiddleware_WrapsEveryAdminRoute(t *testing.T) {
	rr := newRouteRecorder()
	h := newAdminRouter(rr)

	for _, rt := range adminRoutes {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(rt.method, rt.path, nil))
		if got := rr.count(rt.method, rt.path); got != 1 {
			t.Errorf("admin route %s %s: AdminMiddleware invoked %d times, want 1", rt.method, rt.path, got)
		}
	}
}

func TestAdminMiddleware_SkipsEveryPublicRoute(t *testing.T) {
	rr := newRouteRecorder()
	h := newAdminRouter(rr)

	for _, rt := range publicRoutes {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(rt.method, rt.path, nil))
		if got := rr.count(rt.method, rt.path); got != 0 {
			t.Errorf("public route %s %s: AdminMiddleware invoked %d times, want 0", rt.method, rt.path, got)
		}
	}
}

// adminChromeViews renders every admin view inside the admin nav slot, as the
// bundled goth views do, so a test can prove no admin handler and no admin
// chrome answered a request.
type adminChromeViews struct{ stubViews }

func adminChrome(page string) web.Renderer {
	return stringRenderer("<nav " + adminNavMarker + "></nav>" + page)
}

func (adminChromeViews) EntriesList(string, string, string, []EntryListItem, Pager) web.Renderer {
	return adminChrome("entries")
}
func (adminChromeViews) EntriesListContent(string, string, string, []EntryListItem, Pager) web.Renderer {
	return adminChrome("entries-content")
}
func (adminChromeViews) EntryForm(EntryFormModel) web.Renderer { return adminChrome("entry-form") }
func (adminChromeViews) TermsList(_, _ []taxonomy.Term) web.Renderer {
	return adminChrome("terms")
}
func (adminChromeViews) TermForm(TermFormModel) web.Renderer { return adminChrome("term-form") }
func (adminChromeViews) MenusList([]menus.Menu) web.Renderer { return adminChrome("menus") }
func (adminChromeViews) MenuNew(string) web.Renderer         { return adminChrome("menu-new") }
func (adminChromeViews) MenuDetail(menus.Menu, []menus.MenuItem) web.Renderer {
	return adminChrome("menu-detail")
}
func (adminChromeViews) MenuItemForm(menus.MenuItem) web.Renderer { return adminChrome("menu-item") }
func (adminChromeViews) MediaLibrary([]media.Asset, string) web.Renderer {
	return adminChrome("media")
}
func (adminChromeViews) InquiriesList([]messaging.Inquiry) web.Renderer {
	return adminChrome("inquiries")
}
func (adminChromeViews) AdminError(int, string) web.Renderer { return adminChrome("admin-error") }

// TestAdminMiddleware_NilUnmountsAdmin documents deny-by-absence: with no
// AdminMiddleware and no acknowledgement, no admin handler is reachable — every
// admin path answers 404 or 405 (ServeMux falls through to the public patterns)
// and renders no admin chrome — while the public site still serves. The
// acknowledged router is the control: the same GETs do render the admin chrome.
func TestAdminMiddleware_NilUnmountsAdmin(t *testing.T) {
	newRouter := func(opts ...RouterOption) http.Handler {
		opts = append([]RouterOption{WithViews(adminChromeViews{})}, opts...)
		return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, slog.New(slog.DiscardHandler), opts...)
	}
	unmounted, ungated := newRouter(), newRouter(WithAdminUngated())

	for _, rt := range adminRoutes {
		w := httptest.NewRecorder()
		unmounted.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s with nil AdminMiddleware: status = %d, want 404 or 405", rt.method, rt.path, w.Code)
		}
		if strings.Contains(w.Body.String(), adminNavMarker) {
			t.Errorf("%s %s with nil AdminMiddleware rendered the admin chrome: %q", rt.method, rt.path, w.Body.String())
		}

		if rt.method != http.MethodGet {
			continue
		}
		w = httptest.NewRecorder()
		ungated.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), adminNavMarker) {
			t.Errorf("control: acknowledged %s %s = %d %q, want 200 with the admin chrome", rt.method, rt.path, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	unmounted.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET / with nil AdminMiddleware: status = %d, want 200", w.Code)
	}
}

// TestPublicErrors_RenderNoAdminChrome pins the public routes' error paths to
// the public error view: GET /menu/{slug} and POST /contact never render
// AdminError, whose admin nav would leak onto public pages, whether or not the
// admin surface is mounted.
func TestPublicErrors_RenderNoAdminChrome(t *testing.T) {
	menuSvc := &fakeMenuSvc{getSlug: func(context.Context, string) (menus.Menu, error) {
		return menus.Menu{}, sdk.ErrNotFound
	}}
	contactSvc := &fakeContactSvc{submitFn: func(context.Context, string, string, string) (messaging.Inquiry, error) {
		return messaging.Inquiry{}, errors.New("smtp down")
	}}

	for name, opts := range map[string][]RouterOption{
		"unmounted": {WithViews(adminChromeViews{})},
		"ungated":   {WithViews(adminChromeViews{}), WithAdminUngated()},
	} {
		h := BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, menuSvc, &fakeMediaSvc{}, contactSvc, nil, slog.New(slog.DiscardHandler), opts...)

		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/menu/nope", nil))
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), adminNavMarker) || !strings.Contains(w.Body.String(), "STUB-ERROR:404") {
			t.Errorf("%s: GET /menu/nope = %d %q, want 404 through the public error view", name, w.Code, w.Body.String())
		}

		form := url.Values{"name": {"Alice"}, "email": {"a@b.com"}, "message": {"Hi"}}
		req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), adminNavMarker) || !strings.Contains(w.Body.String(), "STUB-ERROR:500") {
			t.Errorf("%s: failed POST /contact = %d %q, want 500 through the public error view", name, w.Code, w.Body.String())
		}
	}
}
