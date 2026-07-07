package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/features/cms/theme"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// overrideViews embeds the PublicViews interface (the decorator pattern) and
// replaces only Home, exercising the chrome seam the way a host would: swap one
// page, inherit the rest via the embedded default theme.
type overrideViews struct {
	theme.PublicViews
}

func (overrideViews) Home(_ []menus.MenuItem, _ []theme.ListItem) web.Renderer {
	return stringRenderer("CUSTOM-HOME-THEME")
}

// TestPublicViews_HostOverridesHome proves a host can replace the Home page by
// passing a different PublicViews impl through WithPublicViews, and that the
// feature's own handler renders the override (it calls through the interface).
func TestPublicViews_HostOverridesHome(t *testing.T) {
	entries := &fakeEntrySvc{listFn: func(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
		return crud.Page[content.Entry]{Items: []content.Entry{{Type: "article", Slug: "x", Title: "X", Status: content.StatusPublished}}}, nil
	}}
	r := BuildRouter(newTestRegistry(), entries, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop(), WithPublicViews(overrideViews{theme.Default()}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CUSTOM-HOME-THEME") {
		t.Fatalf("home body did not use the override theme:\n%s", rec.Body.String())
	}
}

// TestPublicViews_DefaultThemeUnaffected confirms the default chrome still
// renders when no override is wired (the seam is opt-in).
func TestPublicViews_DefaultThemeUnaffected(t *testing.T) {
	entries := &fakeEntrySvc{listFn: func(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
		return crud.Page[content.Entry]{}, nil
	}}
	r := BuildRouter(newTestRegistry(), entries, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop())

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "CUSTOM-HOME-THEME") {
		t.Fatalf("default theme unexpectedly rendered the override body")
	}
	// Assert the real bundled templ chrome rendered through the seam.
	for _, want := range []string{"<title>Home</title>", "<h1>Latest posts</h1>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("default home missing %q in rendered body:\n%s", want, body)
		}
	}
}
