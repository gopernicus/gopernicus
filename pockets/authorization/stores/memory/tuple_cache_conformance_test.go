package memory_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func TestTupleCacheBackendConformance(t *testing.T) {
	storetest.RunTupleCacheBackend(t, func(*testing.T) tuplecache.Backend { return memory.NewTupleCache() })
}
