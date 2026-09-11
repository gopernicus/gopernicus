package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cataloghttp "github.com/gopernicus/gopernicus/examples/minimal/internal/inbound/domains/catalog"
	"github.com/gopernicus/gopernicus/examples/minimal/internal/logic/domains/catalog"
	"github.com/gopernicus/gopernicus/examples/minimal/internal/memstore"
	catalogstore "github.com/gopernicus/gopernicus/examples/minimal/internal/outbound/domains/catalog"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Drive the real source/service/HTTP route, with a draft alongside a published
// product. Only the public projection is cached, and invalidation reloads it.
func TestCatalogCacheOverHTTP(t *testing.T) {
	ctx := context.Background()
	repos := memstore.New().Repositories()
	if err := seed(ctx, repos); err != nil {
		t.Fatal(err)
	}
	draft, err := content.NewEntry(ids, "product", "Secret draft", "", "", "", content.StatusDraft, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repos.Entries.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	source := catalogstore.NewCMS(repos.Entries)
	cache := cacher.New(cacher.NewMemory(), cacher.WithNamespace("minimal-catalog:v1"))
	router := web.NewWebHandler()
	cataloghttp.Mount(router, catalog.New(source, cache, time.Minute))
	server := httptest.NewServer(router)
	defer server.Close()
	read := func() []catalog.Item {
		t.Helper()
		res, err := server.Client().Get(server.URL + "/catalog.json")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v", res.StatusCode, res.Header)
		}
		var items []catalog.Item
		if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	items := read()
	if len(items) != 1 || items[0].Title != "Widget 3000" {
		t.Fatalf("public products=%+v", items)
	}
	product, err := repos.Entries.GetBySlug(ctx, "product", items[0].Slug)
	if err != nil {
		t.Fatal(err)
	}
	product.Title = "Updated widget"
	if _, err := repos.Entries.Update(ctx, product.ID, product); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[0].Title != "Widget 3000" {
		t.Fatalf("expected cached projection, got %+v", got)
	}
	if err := cache.InvalidatePrefix(ctx, "published:"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[0].Title != "Updated widget" {
		t.Fatalf("invalidation did not reload: %+v", got)
	}
}

type catalogSourceFunc func(context.Context) ([]catalog.Item, error)

func (f catalogSourceFunc) ListPublished(ctx context.Context) ([]catalog.Item, error) { return f(ctx) }

type unavailableCatalogCache struct{ cacher.Noop }

func (unavailableCatalogCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, io.ErrUnexpectedEOF
}
func (unavailableCatalogCache) Set(context.Context, string, []byte, time.Duration) error {
	return io.ErrUnexpectedEOF
}

func TestCatalogCacheFallbackAndSourceErrors(t *testing.T) {
	for _, kind := range []string{"disabled", "outage", "corrupt", "source error"} {
		t.Run(kind, func(t *testing.T) {
			var store cacher.Storer = cacher.NewMemory()
			if kind == "disabled" {
				store = cacher.Noop{}
			}
			if kind == "outage" {
				store = unavailableCatalogCache{}
			}
			reports := 0
			cache := cacher.New(store, cacher.WithNamespace("test"), cacher.WithOnError(func(context.Context, string, error) { reports++ }))
			if kind == "corrupt" {
				if err := cache.Set(context.Background(), "published:v1", []byte("broken"), 0); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			sourceErr := errors.New("source down")
			source := catalogSourceFunc(func(context.Context) ([]catalog.Item, error) {
				calls++
				if kind == "source error" {
					return nil, sourceErr
				}
				return []catalog.Item{{Title: "live"}}, nil
			})
			service := catalog.New(source, cache, time.Minute)
			for i := 0; i < 2; i++ {
				got, err := service.List(context.Background())
				if kind == "source error" {
					if !errors.Is(err, sourceErr) {
						t.Fatal(err)
					}
				} else if err != nil || len(got) != 1 || got[0].Title != "live" {
					t.Fatalf("%+v %v", got, err)
				}
			}
			wantCalls := 2
			if kind == "corrupt" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("source calls=%d", calls)
			}
			if (kind == "outage" || kind == "corrupt") && reports == 0 {
				t.Fatal("cache failure went unreported")
			}
		})
	}
}
