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
		return authorization.Repositories{
			Relationships: memstore.NewRelationships(),
			Roles:         memstore.NewRoles(),
		}
	})
}
