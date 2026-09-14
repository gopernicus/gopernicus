package memory_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func BenchmarkCacheWrites(b *testing.B) {
	storetest.BenchmarkCacheWrites(b, func(b *testing.B, cfg storetest.CacheBenchmarkConfig) authorization.Repositories {
		var opts []memory.Option
		if cfg.Invalidation {
			opts = append(opts, memory.WithCacheReads())
		}
		if cfg.Audit {
			opts = append(opts, memory.WithAudit())
		}
		store := memory.New(opts...)
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations(), Audit: store.Audit(), CacheSource: store.CacheSource()}
	})
}
