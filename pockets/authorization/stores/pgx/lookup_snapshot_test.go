package pgx

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"

	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"

	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func TestCheckSnapshots(t *testing.T) {
	storetest.RunCheckSnapshots(t, checkSnapshotFixture)
}

func TestCheckAmbient(t *testing.T) {
	storetest.RunSnapshotCheckAmbient(t, checkSnapshotFixture)
}

func checkSnapshotFixture(t *testing.T) (storetest.Repositories, transaction.Transactor) {
	db, cfg := cacheFixture(t, false)
	repos, err := testRepositories(t.Context(), db, func(c *config) { *c = cfg })
	if err != nil {
		t.Fatal(err)
	}
	return repos, snapshotTransactor{db}
}

func TestLookupSnapshots(t *testing.T) {
	storetest.RunLookupSnapshots(t, func(t *testing.T) storetest.Repositories {
		db, cfg := cacheFixture(t, false)
		repos, err := testRepositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestLookupSnapshotLifecycle(t *testing.T) {
	storetest.RunLookupSnapshotLifecycle(t, func(t *testing.T) storetest.Repositories {
		db, cfg := cacheFixture(t, false)
		repos, err := testRepositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestLookupAmbient(t *testing.T) {
	storetest.RunLookupAmbient(t, func(t *testing.T) (storetest.Repositories, transaction.Transactor) {
		db, cfg := cacheFixture(t, false)
		repos, err := testRepositories(t.Context(), db, func(c *config) { *c = cfg })
		if err != nil {
			t.Fatal(err)
		}
		return repos, snapshotTransactor{db}
	})
}

func TestLookupConcurrent(t *testing.T) {
	db, cfg := cacheFixture(t, false)
	repos, err := testRepositories(t.Context(), db, func(c *config) { *c = cfg })
	if err != nil {
		t.Fatal(err)
	}
	storetest.RunLookupConcurrent(t, repos)
}

type snapshotTransactor struct{ *pgxdb.DB }

func (s snapshotTransactor) Transact(ctx context.Context, fn func(context.Context) error) error {
	return s.DB.TransactSnapshot(ctx, fn)
}
