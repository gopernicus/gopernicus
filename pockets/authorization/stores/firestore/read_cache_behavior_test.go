//go:build integration && !live

package firestore

import (
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"testing"
)

func TestCachePublicBehavior(t *testing.T) {
	storetest.RunReadCache(t, func(t *testing.T) authorization.Repositories { _, repos := newCacheFixture(t); return repos }, func(t *testing.T) cacher.Storer { return cacher.NewMemory() })
}
