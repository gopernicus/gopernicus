package memstore_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/pockets/authorization/storetest"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// TestConformance runs the shared two-kind conformance suite against the in-core
// memstore reference — the hermetic leg of make check for this pocket (the
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

// TestTransactional registers the ambient-transaction family against the
// memstore with NO transactor: the memstore has no connector and no transaction
// concept (its SetRelationTargets is mutex-atomic and ignores the context), so
// the family skips LOUDLY here rather than reporting a green nothing verified.
// The dialect stores run it live over their connector's crud.Transactor.
func TestTransactional(t *testing.T) {
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, crud.Transactor) {
		store := memstore.New()
		return authorization.Repositories{
			Relationships: store.Relationships(),
			Roles:         store.Roles(),
			Mutations:     store.Mutations(),
		}, nil
	})
}
