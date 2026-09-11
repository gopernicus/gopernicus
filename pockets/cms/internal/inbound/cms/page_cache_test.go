package cms

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type pageCacheStore struct {
	*cacher.Memory
	ttl time.Duration
}

func (s *pageCacheStore) Set(ctx context.Context, key string, body []byte, ttl time.Duration) error {
	s.ttl = ttl
	return s.Memory.Set(ctx, key, body, ttl)
}

func TestPublicPageCacheConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       cacher.PageConfig
		wantCalls int
		wantTTL   time.Duration
	}{
		{"default", cacher.PageConfig{}, 1, time.Minute},
		{"host ttl", cacher.PageConfig{TTL: 5 * time.Second}, 1, 5 * time.Second},
		{"host body limit", cacher.PageConfig{MaxBodyBytes: 1}, 2, 0},
		{"host disabled", cacher.PageConfig{TTL: -1}, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			entries := &fakeEntrySvc{listFn: func(context.Context, content.EntryQuery) (list.Page[content.Entry], error) {
				calls++
				return list.Page[content.Entry]{}, nil
			}}
			store := &pageCacheStore{Memory: cacher.NewMemory()}
			r := BuildRouter(newTestRegistry(), entries, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, &fakeContactSvc{}, store, slog.New(slog.DiscardHandler), WithViews(stubViews{}), WithPageCache(tc.cfg))
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
				if w.Code != 200 || w.Body.String() != "STUB-HOME" {
					t.Fatalf("%d %q", w.Code, w.Body.String())
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d", calls)
			}
			if tc.wantTTL == 0 {
				if store.ttl != 0 {
					t.Fatal("disabled/oversized page stored")
				}
			} else if store.ttl <= 0 || store.ttl > tc.wantTTL || store.ttl < tc.wantTTL-time.Second {
				t.Fatalf("stored TTL %s, expected near %s", store.ttl, tc.wantTTL)
			}
		})
	}
}
