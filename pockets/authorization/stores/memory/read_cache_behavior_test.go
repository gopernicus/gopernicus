package memory_test

import (
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"testing"
)

func TestCachePublicBehavior(t *testing.T) {
	storetest.RunReadCache(t, func(t *testing.T) authorization.Repositories {
		store := memory.New(memory.WithCacheReads())
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), CacheSource: store.CacheSource()}
	}, func(t *testing.T) cacher.Storer { return cacher.NewMemory() })
}
