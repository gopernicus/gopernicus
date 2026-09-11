// Package catalog is this host's small public product projection.
package catalog

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// Item includes only fields intended for the public catalog.
type Item struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

// Source supplies the first 100 published products in its default order.
type Source interface {
	ListPublished(context.Context) ([]Item, error)
}

type Service struct {
	source Source
	cache  *cacher.Cache
	ttl    time.Duration
}

// New keeps storage, namespace, freshness and error reporting host-owned.
func New(source Source, cache *cacher.Cache, ttl time.Duration) *Service {
	return &Service{source: source, cache: cache, ttl: ttl}
}

// List caches application data independently of HTML response caching. A cache
// outage still loads the authoritative source; source errors reach the caller.
func (s *Service) List(ctx context.Context) ([]Item, error) {
	return cacher.GetOrLoadJSON(ctx, s.cache, "published:v1", s.ttl, s.source.ListPublished)
}
