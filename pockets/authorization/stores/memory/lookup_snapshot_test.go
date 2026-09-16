package memory_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

func TestCheckSnapshots(t *testing.T) {
	storetest.RunCheckSnapshots(t, func(t *testing.T) (storetest.Repositories, transaction.Transactor) {
		store := memory.New()
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, Relationships: store.Relationships()}, nil
	})
}

func TestLookupSnapshots(t *testing.T) {
	storetest.RunLookupSnapshots(t, func(t *testing.T) storetest.Repositories {
		store := memory.New()
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, Relationships: store.Relationships()}
	})
}

func TestLookupSnapshotLifecycle(t *testing.T) {
	storetest.RunLookupSnapshotLifecycle(t, func(t *testing.T) storetest.Repositories {
		store := memory.New()
		return storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, Relationships: store.Relationships()}
	})
}

func TestLookupConcurrent(t *testing.T) {
	store := memory.New()
	repos := storetest.Repositories{Repositories: authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, Relationships: store.Relationships()}
	storetest.RunLookupConcurrent(t, repos)
}
