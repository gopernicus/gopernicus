//go:build integration && !live

// The factory's own emulator leg: it proves Open really opens, Reset really
// clears the database it was given (and only that one), and the C6 id helpers
// produce ids a real Firestore server accepts. Skips loudly without
// FIRESTORE_EMULATOR_HOST.
package firestoretest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
)

// TestOpenAndReset is the factory's contract in one pass: a document written
// through the connector is gone after Reset.
func TestOpenAndReset(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.Open(t)
	firestoretest.Reset(t, db)

	if !db.Emulated() {
		t.Fatalf("firestoretest.Open produced a non-emulator client for %s", db.Target())
	}

	writer := db.WriterFrom(ctx)
	if err := writer.Create(ctx, db.Doc("c6_fixture", firestore.NewID()), map[string]any{"name": "alpha"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := count(t, ctx, db, "c6_fixture"); got != 1 {
		t.Fatalf("seeded collection holds %d documents, want 1", got)
	}

	firestoretest.Reset(t, db)

	if got := count(t, ctx, db, "c6_fixture"); got != 0 {
		t.Errorf("collection holds %d documents after Reset, want 0", got)
	}
}

// TestResetIsScopedToItsDatabase proves the isolation a store harness relies on:
// clearing one database leaves a sibling database untouched.
func TestResetIsScopedToItsDatabase(t *testing.T) {
	ctx := context.Background()

	other := firestoretest.OpenDatabase(t, "c6-sibling")
	if err := other.WriterFrom(ctx).Create(ctx, other.Doc("c6_fixture", "keep"), map[string]any{"name": "keep"}); err != nil {
		t.Skipf("named databases are unavailable on this emulator (%v) — scoping NOT verified", err)
	}
	t.Cleanup(func() { firestoretest.Reset(t, other) })

	main := firestoretest.Open(t)
	if err := main.WriterFrom(ctx).Create(ctx, main.Doc("c6_fixture", "clear"), map[string]any{"name": "clear"}); err != nil {
		t.Fatalf("Create in the default database: %v", err)
	}

	firestoretest.Reset(t, main)

	if got := count(t, ctx, main, "c6_fixture"); got != 0 {
		t.Errorf("the reset database holds %d documents, want 0", got)
	}
	if got := count(t, ctx, other, "c6_fixture"); got != 1 {
		t.Errorf("the sibling database holds %d documents, want 1 — Reset was not scoped", got)
	}
}

// TestGeneratedIDsAreLegalDocumentIDs is compatibility note N1 checked against a
// real server rather than the documentation: a KeyHash over a tuple that
// exceeds Firestore's 1500-byte id limit, contains slashes, and is not valid as
// a raw id, is accepted as a document id — and NewID's shape is too.
func TestGeneratedIDsAreLegalDocumentIDs(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.Open(t)
	firestoretest.Reset(t, db)

	long := strings.Repeat("k", 256)
	ids := map[string]string{
		"NewID":            firestore.NewID(),
		"KeyHash six-part": firestore.KeyHash(long, long, long, long, long, long),
		"KeyHash slashes":  firestore.KeyHash("resource/type", "id/with/slashes", "__reserved__", ".."),
		"KeyHash unicode":  firestore.KeyHash("日本語", "🙂"),
		"KeyHash empty":    firestore.KeyHash(""),
	}
	writer := db.WriterFrom(ctx)
	for name, id := range ids {
		if err := writer.Create(ctx, db.Doc("c6_ids", id), map[string]any{"source": name}); err != nil {
			t.Errorf("%s produced an id Firestore rejected (%q): %v", name, id, err)
		}
	}
	if got, want := count(t, ctx, db, "c6_ids"), int64(len(ids)); got != want {
		t.Errorf("wrote %d distinct ids, want %d", got, want)
	}
}

// TestTimestampsRoundTripAtMicroseconds proves TruncateTime's premise on the
// server: a truncated value comes back equal, and the absent model reads back as
// the zero time.
func TestTimestampsRoundTripAtMicroseconds(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.Open(t)
	firestoretest.Reset(t, db)

	written := firestore.TruncateTime(time.Now().Add(937 * time.Nanosecond))
	ref := db.Doc("c6_times", firestore.NewID())
	if err := db.WriterFrom(ctx).Create(ctx, ref, map[string]any{
		"created_at": written,
		"expires_at": firestore.NullTime(time.Time{}),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data := snap.Data()

	got, err := firestore.ParseTime(data["created_at"])
	if err != nil {
		t.Fatalf("ParseTime: %v", err)
	}
	if !got.Equal(written) {
		t.Errorf("created_at round-tripped as %v, want %v", got, written)
	}
	expires, err := firestore.ParseNullTime(data["expires_at"])
	if err != nil {
		t.Fatalf("ParseNullTime: %v", err)
	}
	if !expires.IsZero() {
		t.Errorf("a null timestamp read back as %v, want the zero time", expires)
	}
}

// count returns the number of documents in a collection through the connector's
// aggregation Count.
func count(t *testing.T, ctx context.Context, db *firestore.DB, collection string) int64 {
	t.Helper()
	got, err := db.ReaderFrom(ctx).Count(ctx, db.Collection(collection).Query)
	if err != nil {
		t.Fatalf("counting %s: %v", collection, err)
	}
	return got
}
