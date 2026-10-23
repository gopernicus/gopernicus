package memory_test

import (
	"testing"
	"time"

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
	storetest.Run(t, func(t *testing.T, policy mutations.IntegrityPolicy) storetest.Repositories {
		// One bundle so the atomic mutation repository shares its lock and snapshot
		// with the relationship/role read path (a grant via Apply is visible to
		// Check and to the raw stores). The default integrity policy protects owner.
		store := memory.New(memory.WithIntegrityPolicy(policy))
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(),
			Mutations: store.Mutations(), Operations: store.Operations(), OperationExpiry: store.OperationExpiry()}, Relationships: store.Relationships(),
			SetOperationCommittedAt: func(t *testing.T, operationID string, committedAt time.Time) {
				if !memory.SetOperationCommittedAt(store.Mutations(), operationID, committedAt) {
					t.Fatalf("no operation record %q", operationID)
				}
			},
		}
	})
}

// TestTransactional registers the ambient-transaction family against the
// memstore with NO transactor: the memstore has no connector and no transaction
// concept (its SetRelationTargets publishes under a shared mutex), so
// the family skips LOUDLY here rather than reporting a green nothing verified.
// The dialect stores run it live over their connector's transaction.Transactor.
func TestTransactional(t *testing.T) {
	storetest.RunTransactional(t, func(t *testing.T) (storetest.Repositories, transaction.Transactor) {
		store := memory.New()
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(),
			Mutations: store.Mutations(), Operations: store.Operations()}, Relationships: store.Relationships(),
		}, nil
	})
}

// TestAudit runs the shared per-fact history contract against the real memory bundle.
func TestAudit(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) storetest.Repositories {
		var opts []memory.Option
		if enabled {
			opts = append(opts, memory.WithAudit())
		}
		store := memory.New(opts...)
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations(), Audit: store.Audit()}, Relationships: store.Relationships()}
	})
}

// TestLookupEquivalence proves the direct-hit short circuit against pure
// subject-first expansion over the memory reference store.
func TestLookupEquivalence(t *testing.T) {
	storetest.RunLookupEquivalence(t, func(t *testing.T) storetest.Repositories {
		store := memory.New()
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(),
			Mutations: store.Mutations(), Operations: store.Operations()}, Relationships: store.Relationships(),
		}
	})
}

// TestAuthorizer runs the sdk/capabilities/authorization conformance suite
// against the engine adapter over the memstore: the hermetic proof that the
// pocket implements the protocol.
func TestAuthorizer(t *testing.T) {
	storetest.RunAuthorizer(t, func(t *testing.T) authorization.Repositories {
		return authorization.Repositories{Tuples: memory.New().Tuples()}
	})
}
