package pgx

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func TestCheckSnapshots(t *testing.T) {
	storetest.RunCheckSnapshots(t, checkSnapshotFixture)
}

func TestCheckAmbient(t *testing.T) {
	storetest.RunCheckAmbient(t, checkSnapshotFixture)
}

func checkSnapshotFixture(t *testing.T) (authorization.Repositories, transaction.Transactor) {
	db, cfg := cacheFixture(t, false)
	repos, err := Repositories(t.Context(), db, func(c *config) { *c = cfg })
	if err != nil {
		t.Fatal(err)
	}
	return repos, db
}

func TestLookupSnapshots(t *testing.T) {
	storetest.RunLookupSnapshots(t, func(t *testing.T) authorization.Repositories {
		db, cfg := cacheFixture(t, false)
		repos, err := Repositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestLookupSnapshotLifecycle(t *testing.T) {
	storetest.RunLookupSnapshotLifecycle(t, func(t *testing.T) authorization.Repositories {
		db, cfg := cacheFixture(t, false)
		repos, err := Repositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestLookupAmbient(t *testing.T) {
	storetest.RunLookupAmbient(t, func(t *testing.T) (authorization.Repositories, transaction.Transactor) {
		db, cfg := cacheFixture(t, false)
		repos, err := Repositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos, db
	})
}

func TestLookupConcurrent(t *testing.T) {
	db, cfg := cacheFixture(t, false)
	repos, err := Repositories(t.Context(), db, func(c *config) { *c = cfg })
	if err != nil {
		t.Fatal(err)
	}
	storetest.RunLookupConcurrent(t, repos)
}
