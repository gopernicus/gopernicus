package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/media"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// overrideViews embeds the Views port (the decorator pattern) and replaces only
// Home, exercising the blessed partial-override path the way a host would: swap
// one page, inherit the rest via the embedded default.
type overrideViews struct {
	Views
}

func (overrideViews) Home(_ []menus.MenuItem, _ []ListItem) web.Renderer {
	return stringRenderer("CUSTOM-HOME-THEME")
}

// TestViews_HostOverridesHome proves a host can replace the Home page by passing
// a Views that embeds the default and overrides Home, and that the feature's own
// handler renders the override (it calls through the interface).
func TestViews_HostOverridesHome(t *testing.T) {
	entries := &fakeEntrySvc{listFn: func(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
		return crud.Page[content.Entry]{Items: []content.Entry{{Type: "article", Slug: "x", Title: "X", Status: content.StatusPublished}}}, nil
	}}
	r := BuildRouter(newTestRegistry(), entries, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop(), WithViews(overrideViews{stubViews{}}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CUSTOM-HOME-THEME") {
		t.Fatalf("home body did not use the override:\n%s", rec.Body.String())
	}
}

// TestViews_BaseRendersWhenNotOverridden confirms the embedded default still
// renders when Home is not overridden (the seam is opt-in).
func TestViews_BaseRendersWhenNotOverridden(t *testing.T) {
	entries := &fakeEntrySvc{listFn: func(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
		return crud.Page[content.Entry]{}, nil
	}}
	r := BuildRouter(newTestRegistry(), entries, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop(), WithViews(stubViews{}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "CUSTOM-HOME-THEME") {
		t.Fatalf("base views unexpectedly rendered the override body")
	}
	if !strings.Contains(body, "STUB-HOME") {
		t.Fatalf("base Home did not render through the seam:\n%s", body)
	}
}

// TestViews_NilRegistersOnlyMediaServe proves the FS3 nil-Views contract (D-4):
// with a nil Views the HTML surface is not mounted (public home + admin 404),
// but the media byte endpoint still serves.
func TestViews_NilRegistersOnlyMediaServe(t *testing.T) {
	asset := media.Asset{ID: "a1", Filename: "logo.png", ContentType: "image/png"}
	svc := &fakeMediaSvc{openFn: func(ctx context.Context, id string) (media.Asset, io.ReadCloser, error) {
		return asset, io.NopCloser(strings.NewReader("PNG-BYTES")), nil
	}}
	r := BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, svc, &fakeContactSvc{}, nil, logging.NewNoop())

	for _, path := range []string{"/", "/articles", "/contact", "/media"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("nil Views: GET %s = %d, want 404", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/a1/file", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "PNG-BYTES" {
		t.Fatalf("nil Views: GET /media/{id}/file = %d %q, want 200 PNG-BYTES", rec.Code, rec.Body.String())
	}
}
