package http

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
)

func TestMenus_ListAndPublicNav(t *testing.T) {
	main := menus.Menu{ID: "m1", Name: "Main", Slug: "main", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	items := []menus.MenuItem{
		{ID: "i1", MenuID: "m1", Label: "Home", URL: "/", Position: 0},
		{ID: "i2", MenuID: "m1", Label: "About", URL: "/page/about", ParentID: "i1", Position: 1},
	}
	svc := &fakeMenuSvc{
		listFn:  func(ctx context.Context) ([]menus.Menu, error) { return []menus.Menu{main}, nil },
		getSlug: func(ctx context.Context, slug string) (menus.Menu, error) { return main, nil },
		itemsFn: func(ctx context.Context, menuID string) ([]menus.MenuItem, error) { return items, nil },
	}

	rec := httptest.NewRecorder()
	menuRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/menus", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Main") {
		t.Fatalf("list menus: status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	menuRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/menu/main", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Home") || !strings.Contains(body, "About") {
		t.Fatalf("public nav: status=%d body=%s", rec.Code, body)
	}
}
