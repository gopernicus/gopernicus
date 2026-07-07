package http

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
)

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
	return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop(), WithAdminMiddleware(rr.middleware()))
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

// TestAdminMiddleware_NilPreservesBehavior documents the zero-value contract:
// with no AdminMiddleware configured, an admin route still serves (no gating).
func TestAdminMiddleware_NilPreservesBehavior(t *testing.T) {
	h := BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/articles", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /articles with nil AdminMiddleware: status = %d, want %d", w.Code, http.StatusOK)
	}
}
