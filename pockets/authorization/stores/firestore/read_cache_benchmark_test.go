//go:build integration && !live

package firestore

import (
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func cacheBenchmarkFactory(b *testing.B, settings storetest.CacheBenchmarkConfig) authorization.Repositories {
	db := firestoretest.OpenDatabase(b, "authorization-cache-benchmark")
	firestoretest.Reset(b, db)
	options := []Option{WithoutIndexProbe()}
	if settings.Invalidation {
		if err := InitializeCacheInvalidation(b.Context(), db); err != nil {
			b.Fatal(err)
		}
		options = append(options, WithCacheInvalidation())
	}
	if settings.CacheReads {
		options = append(options, WithCacheReads())
	}
	if settings.Audit {
		options = append(options, WithAudit())
	}
	repos, err := Repositories(b.Context(), db, options...)
	if err != nil {
		b.Fatal(err)
	}
	return repos
}
func BenchmarkReadCache(b *testing.B)   { storetest.BenchmarkReadCache(b, cacheBenchmarkFactory) }
func BenchmarkCacheWrites(b *testing.B) { storetest.BenchmarkCacheWrites(b, cacheBenchmarkFactory) }
