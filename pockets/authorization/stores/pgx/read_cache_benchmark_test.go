package pgx

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func cacheBenchmarkFactory(b *testing.B, settings storetest.CacheBenchmarkConfig) authorization.Repositories {
	db, cfg := cacheFixture(b, settings.Invalidation)
	options := []Option{func(c *config) { *c = cfg }}
	if settings.CacheReads {
		options = append(options, WithCacheReads())
	}
	if settings.Audit {
		options = append(options, WithAudit())
	}
	repos, err := Repositories(context.Background(), db, options...)
	if err != nil {
		b.Fatal(err)
	}
	return repos
}
func BenchmarkReadCache(b *testing.B)   { storetest.BenchmarkReadCache(b, cacheBenchmarkFactory) }
func BenchmarkCacheWrites(b *testing.B) { storetest.BenchmarkCacheWrites(b, cacheBenchmarkFactory) }
