package memory_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

// TestConformance runs the shared two-kind conformance suite against the in-core
// memstore reference — the hermetic leg of make check for this pocket (the
// dialect stores prove the same suite live). Each newRepos call returns a fresh,
// empty pair wiring BOTH kinds.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		// One bundle so the atomic mutation repository shares its lock and snapshot
		// with the relationship/role read path (a grant via Apply is visible to
		// Check and to the raw stores). The default guardian policy protects owner.
		store := memory.New(memory.WithGuardianPolicy(policy))
		return authorization.Repositories{
			Relationships: store.Relationships(),
			Roles:         store.Roles(),
			Mutations:     store.Mutations(),
		}
	})
}

// TestTransactional registers the ambient-transaction family against the
// memstore with NO transactor: the memstore has no connector and no transaction
// concept (its SetRelationTargets publishes under a shared mutex), so
// the family skips LOUDLY here rather than reporting a green nothing verified.
// The dialect stores run it live over their connector's transaction.Transactor.
func TestTransactional(t *testing.T) {
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, transaction.Transactor) {
		store := memory.New()
		return authorization.Repositories{
			Relationships: store.Relationships(),
			Roles:         store.Roles(),
			Mutations:     store.Mutations(),
		}, nil
	})
}

// TestAudit runs the shared per-fact history contract against the real memory bundle.
func TestAudit(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		var opts []memory.Option
		if enabled {
			opts = append(opts, memory.WithAudit())
		}
		store := memory.New(opts...)
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations(), Audit: store.Audit()}
	})
}
