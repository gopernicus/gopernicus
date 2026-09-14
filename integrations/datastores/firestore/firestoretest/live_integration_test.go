//go:build integration && live

// The live leg. It runs against a REAL Firestore database — never the emulator —
// named by FIRESTORE_LIVE_PROJECT_ID and FIRESTORE_LIVE_DATABASE_ID, with
// Application Default Credentials. Without that configuration it SKIPS loudly;
// with FIRESTORE_LIVE_REQUIRED=1 the same missing configuration FAILS the run,
// which is what the release gate sets so a train cannot pass on skips.
//
//	FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_PROJECT_ID=<project> \
//	  FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//	  GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//	  go test -tags='integration,live' -run 'Live$' ./...
package firestoretest_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
)

// liveFixtureCollection is the ONLY collection this leg writes or clears. Naming
// it here, and passing it to every ResetLive call, is the contract that keeps a
// live run from touching anything else in the database.
const liveFixtureCollection = "firestoretest_live_fixture"

// TestFactoryLive is the live counterpart of TestOpenAndReset: open the real
// database, write through the connector, and prove ResetLive clears exactly the
// collection it was named.
func TestFactoryLive(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.OpenLive(t)

	if db.Emulated() {
		t.Fatalf("the live leg opened an emulator client for %s", db.Target())
	}
	firestoretest.ResetLive(t, db, liveFixtureCollection)
	t.Cleanup(func() { firestoretest.ResetLive(t, db, liveFixtureCollection) })

	writer := db.WriterFrom(ctx)
	const seeded = 3
	for range seeded {
		ref := db.Doc(liveFixtureCollection, firestore.NewID())
		if err := writer.Create(ctx, ref, map[string]any{"created_at": firestore.TruncateTime(time.Now())}); err != nil {
			t.Fatalf("Create in %s: %v", db.Target(), err)
		}
	}
	if got := liveCount(t, ctx, db); got != seeded {
		t.Fatalf("seeded collection holds %d documents, want %d", got, seeded)
	}

	firestoretest.ResetLive(t, db, liveFixtureCollection)

	if got := liveCount(t, ctx, db); got != 0 {
		t.Errorf("collection holds %d documents after ResetLive, want 0", got)
	}
}

func liveCount(t *testing.T, ctx context.Context, db *firestore.DB) int64 {
	t.Helper()
	got, err := db.ReaderFrom(ctx).Count(ctx, db.Collection(liveFixtureCollection).Query)
	if err != nil {
		t.Fatalf("counting %s: %v", liveFixtureCollection, err)
	}
	return got
}
