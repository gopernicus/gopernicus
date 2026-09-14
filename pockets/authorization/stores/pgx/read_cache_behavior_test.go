package pgx

import (
	"context"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"testing"
)

func TestCachePublicBehavior(t *testing.T) {
	storetest.RunReadCache(t, func(t *testing.T) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	}, func(t *testing.T) cacher.Storer { return cacher.NewMemory() })
}
