package memstore_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/features/authorization/storetest"
)

// TestConformance runs the shared two-kind conformance suite against the in-core
// memstore reference — the hermetic leg of make check for this feature (the
// dialect stores prove the same suite live). Each newRepos call returns a fresh,
// empty pair wiring BOTH kinds.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) authorization.Repositories {
		// One bundle so the atomic mutation repository shares its lock and snapshot
		// with the relationship/role read path (a grant via Apply is visible to
		// Check and to the raw stores). The default guardian policy protects owner.
		store := memstore.New()
		return authorization.Repositories{
			Relationships: store.Relationships(),
			Roles:         store.Roles(),
			Mutations:     store.Mutations(),
		}
	})
}
