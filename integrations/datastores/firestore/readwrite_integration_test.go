//go:build integration && !live

// The C2 emulator leg: the client-backed Reader/Writer against a real server.
// Skips LOUDLY without FIRESTORE_EMULATOR_HOST (see emulator_integration_test.go
// for the container command). The transaction-backed halves are proven by C3,
// which owns Transact; what is provable here without it — the refusal of Count
// inside a transaction and the ReaderFrom selection — is hermetic and lives in
// reader_internal_test.go.
package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// widget is the document shape these tests round-trip.
type widget struct {
	Name  string    `firestore:"name"`
	Kind  string    `firestore:"kind"`
	MadeA time.Time `firestore:"made_at"`
}

// TestReaderWriterRoundTrip walks the whole client-backed lifecycle in one
// document: Create, Get, Update, Delete, and the Get that must come back
// NotFound — plus the vendor shape C0 recorded, where that failing Get ALSO
// hands back a snapshot whose Exists() is false.
func TestReaderWriterRoundTrip(t *testing.T) {
	ctx, db, collection := emulatorFixture(t)
	r, w := db.ReaderFrom(ctx), db.WriterFrom(ctx)
	ref := db.Doc(collection, "w1")

	made := time.Now().UTC().Truncate(time.Microsecond)
	if err := w.Create(ctx, ref, widget{Name: "alpha", Kind: "gear", MadeA: made}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	snap, err := r.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	var got widget
	if err := snap.DataTo(&got); err != nil {
		t.Fatalf("DataTo: %v", err)
	}
	if got.Name != "alpha" || got.Kind != "gear" {
		t.Errorf("read back %+v, want name=alpha kind=gear", got)
	}
	if !got.MadeA.Equal(made) {
		t.Errorf("read back made_at %v, want %v (microsecond round trip)", got.MadeA, made)
	}

	if err := w.Update(ctx, ref, []gcfs.Update{{Path: "name", Value: "beta"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	snap, err = r.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if name, err := snap.DataAt("name"); err != nil || name != "beta" {
		t.Errorf("name after Update = %v (%v), want beta", name, err)
	}

	if err := w.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	snap, err = r.Get(ctx, ref)
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want sdk.ErrNotFound", err)
	}
	if snap == nil {
		t.Fatal("Get on a missing document returned a nil snapshot; the vendor's snapshot must be preserved")
	}
	if snap.Exists() {
		t.Error("the snapshot of a missing document reports Exists() = true")
	}
}

// TestCreateTwiceIsAlreadyExists is the uniqueness mechanism ruling R3 builds on:
// a deterministic document id makes a duplicate a server-side AlreadyExists.
func TestCreateTwiceIsAlreadyExists(t *testing.T) {
	ctx, db, collection := emulatorFixture(t)
	w := db.WriterFrom(ctx)
	ref := db.Doc(collection, "claim")

	if err := w.Create(ctx, ref, widget{Name: "first"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	err := w.Create(ctx, ref, widget{Name: "second"})
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("second Create = %v, want sdk.ErrAlreadyExists", err)
	}
}

// TestGetAllReportsMissingPerEntry pins the documented GetAll contract: a
// missing document is NOT an error, it is a snapshot with Exists() == false, in
// the order of the refs.
func TestGetAllReportsMissingPerEntry(t *testing.T) {
	ctx, db, collection := emulatorFixture(t)
	r, w := db.ReaderFrom(ctx), db.WriterFrom(ctx)

	present := db.Doc(collection, "present")
	if err := w.Create(ctx, present, widget{Name: "here"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	absent := db.Doc(collection, "absent")

	snaps, err := r.GetAll(ctx, []*gcfs.DocumentRef{present, absent})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("GetAll returned %d snapshots, want 2", len(snaps))
	}
	if !snaps[0].Exists() {
		t.Error("snapshot[0] (present) reports Exists() = false")
	}
	if snaps[1].Exists() {
		t.Error("snapshot[1] (absent) reports Exists() = true")
	}
}

// TestCountAggregation proves the server-side count the List helper's WithCount
// path (C4) will lean on, over a base query and over a filtered one.
func TestCountAggregation(t *testing.T) {
	ctx, db, collection := emulatorFixture(t)
	r, w := db.ReaderFrom(ctx), db.WriterFrom(ctx)

	seed(t, ctx, w, db, collection, map[string]string{
		"a": "gear", "b": "gear", "c": "spring",
	})

	all, err := r.Count(ctx, db.Collection(collection).Query)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if all != 3 {
		t.Errorf("Count over the collection = %d, want 3", all)
	}

	gears, err := r.Count(ctx, db.Collection(collection).Where("kind", "==", "gear"))
	if err != nil {
		t.Fatalf("filtered Count: %v", err)
	}
	if gears != 2 {
		t.Errorf("Count over kind == gear = %d, want 2", gears)
	}

	none, err := r.Count(ctx, db.Collection(collection).Where("kind", "==", "nothing"))
	if err != nil {
		t.Fatalf("empty Count: %v", err)
	}
	if none != 0 {
		t.Errorf("Count over an empty result = %d, want 0", none)
	}
}

// TestDocumentsIterator exercises the iteration boundary the mediation
// discipline describes: defer Stop, iterator.Done terminates, any other error
// goes through MapError.
func TestDocumentsIterator(t *testing.T) {
	ctx, db, collection := emulatorFixture(t)
	r, w := db.ReaderFrom(ctx), db.WriterFrom(ctx)

	seed(t, ctx, w, db, collection, map[string]string{
		"a": "gear", "b": "gear", "c": "spring",
	})

	it := r.Documents(ctx, db.Collection(collection).Where("kind", "==", "gear").OrderBy("name", gcfs.Asc))
	defer it.Stop()

	var names []string
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("iterating: %v", firestore.MapError(err))
		}
		name, err := snap.DataAt("name")
		if err != nil {
			t.Fatalf("DataAt: %v", err)
		}
		names = append(names, name.(string))
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("iterated %v, want [a b]", names)
	}
}

// emulatorFixture opens the emulator and returns a collection name unique to
// this test, so the legs stay isolated without a shared reset (C6's factory owns
// the reset endpoint).
func emulatorFixture(t *testing.T) (context.Context, *firestore.DB, string) {
	t.Helper()
	requireEmulator(t)

	ctx := context.Background()
	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      emulatorProject(),
		ConnectTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return ctx, db, fmt.Sprintf("c2_%s_%d", t.Name(), time.Now().UnixNano())
}

// seed writes one widget per (id, kind) pair, using the id as the name so
// ordering assertions are readable.
func seed(t *testing.T, ctx context.Context, w firestore.Writer, db *firestore.DB, collection string, docs map[string]string) {
	t.Helper()
	for id, kind := range docs {
		if err := w.Create(ctx, db.Doc(collection, id), widget{Name: id, Kind: kind}); err != nil {
			t.Fatalf("seeding %s: %v", id, err)
		}
	}
}
