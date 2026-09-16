package memory_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

func TestCheckSnapshots(t *testing.T) {
	storetest.RunCheckSnapshots(t, func(t *testing.T) (authorization.Repositories, transaction.Transactor) {
		store := memory.New()
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations()}, nil
	})
}

func TestLookupSnapshots(t *testing.T) {
	storetest.RunLookupSnapshots(t, func(t *testing.T) authorization.Repositories {
		store := memory.New()
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations()}
	})
}

func TestLookupSnapshotLifecycle(t *testing.T) {
	storetest.RunLookupSnapshotLifecycle(t, func(t *testing.T) authorization.Repositories {
		store := memory.New()
		return authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations()}
	})
}

func TestLookupConcurrent(t *testing.T) {
	store := memory.New()
	repos := authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations()}
	storetest.RunLookupConcurrent(t, repos)
}
