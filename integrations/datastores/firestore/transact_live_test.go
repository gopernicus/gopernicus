//go:build integration && live

// The C3 live leg. It runs against a REAL Firestore database — never the
// emulator — and carries the two transaction behaviors the emulator cannot be
// trusted to prove, because the emulator documents that not all transaction
// behavior is implemented and that its locks may take up to 30 seconds to be
// released:
//
//   - production read-only snapshot isolation (the emulator holds it today, but
//     an emulator agreeing is not the server agreeing);
//
//   - commit-side contention: a read-write transaction whose commit loses its
//     race is Aborted, and with the retries exhausted that surfaces as
//     sdk.ErrConflict. The emulator serializes the conflicting write behind its
//     transaction lock instead, so it never produces that outcome (recorded by
//     TestTransactCommitContentionOutcome in the emulator leg).
//
//     FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_PROJECT_ID=<project> \
//     FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//     GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//     go test -tags='integration,live' -run 'Live$' -timeout 30m ./...
//
// NOT RUN as of C3: no live GCP project existed in the session that wrote these
// tests. They compile (go vet -tags='integration,live') and skip loudly without
// configuration; FIRESTORE_LIVE_REQUIRED=1 turns that skip into a failure.
package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/sdk"
)

// liveTransactCollection is the ONLY collection this leg writes or clears.
const liveTransactCollection = "firestore_c3_transact_live"

// liveContentionTimeout bounds the concurrent cases so a missing behavior fails
// the run instead of hanging it.
const liveContentionTimeout = 60 * time.Second

// TestReadSnapshotIsOneSnapshotLive is the assertion the emulator cannot make
// on production's behalf: two queries inside one ReadSnapshot, with a committed
// insert in between, must return the same population. The channel handshake
// makes the ordering a fact — the writer starts only after the first query has
// run inside the transaction, and the second query starts only after the write
// is acknowledged — so a failure here is a real isolation failure, not a race.
func TestReadSnapshotIsOneSnapshotLive(t *testing.T) {
	ctx, db := liveTransactFixture(t)
	prefix := livePrefix(t)

	seedLive(t, ctx, db, prefix, prefix+"seeded", map[string]any{"n": int64(1)})
	query := db.Collection(liveTransactCollection).Where("group", "==", prefix)

	started, written := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(written)
		<-started
		if err := db.WriterFrom(ctx).Create(ctx, db.Doc(liveTransactCollection, prefix+"inserted"),
			map[string]any{"n": int64(2), "group": prefix}); err != nil {
			t.Errorf("the concurrent writer failed: %v", err)
		}
	}()

	var first, second int
	err := db.ReadSnapshot(ctx, func(snapCtx context.Context, r firestore.Reader) error {
		first = countLive(t, snapCtx, r, query)
		close(started)
		select {
		case <-written:
		case <-time.After(liveContentionTimeout):
			t.Fatal("the concurrent writer did not finish")
		}
		second = countLive(t, snapCtx, r, query)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if first != 1 {
		t.Fatalf("the first query saw %d documents, want 1", first)
	}
	if second != first {
		t.Errorf("the second query under the same snapshot saw %d documents, want %d — Firestore did not hold "+
			"one snapshot across the read-only transaction", second, first)
	}
	if outside := countLive(t, ctx, db.ReaderFrom(ctx), query); outside != 2 {
		t.Errorf("outside the snapshot the group holds %d documents, want 2 — the concurrent insert never landed, "+
			"so the invisibility above proves nothing", outside)
	}
}

// TestTransactContentionExhaustionLive is the other production-only behavior: a
// read-write transaction that loses its commit race is Aborted, and with
// MaxAttempts 1 there is no retry left, so the caller gets sdk.ErrConflict — the
// Firestore equivalent of a SQL serialization failure, and the signal that the
// whole workflow may be retried.
//
// The conflict is guaranteed rather than hoped for: the transaction reads the
// document, waits for an acknowledged outside commit to the SAME document, and
// only then writes.
func TestTransactContentionExhaustionLive(t *testing.T) {
	ctx, db := liveTransactFixture(t)
	db1 := liveDBWithAttempts(t, 1)
	prefix := livePrefix(t)

	id := prefix + "contended"
	seedLive(t, ctx, db, prefix, id, map[string]any{"n": int64(0)})
	ref := db.Doc(liveTransactCollection, id)

	read, bumped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(bumped)
		<-read
		bumpCtx, cancel := context.WithTimeout(context.Background(), liveContentionTimeout)
		defer cancel()
		if err := db.WriterFrom(bumpCtx).Set(bumpCtx, db.Doc(liveTransactCollection, id),
			map[string]any{"n": int64(99), "group": prefix}); err != nil {
			t.Errorf("the conflicting commit failed: %v", err)
		}
	}()

	attempts := 0
	err := db1.Transact(ctx, func(txCtx context.Context) error {
		attempts++
		if _, err := db1.ReaderFrom(txCtx).Get(txCtx, ref); err != nil {
			return err
		}
		close(read)
		select {
		case <-bumped:
		case <-time.After(liveContentionTimeout):
			t.Fatal("the conflicting commit did not complete")
		}
		return db1.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"n": int64(1), "group": prefix})
	})

	if attempts != 1 {
		t.Errorf("the callback ran %d times with MaxAttempts 1", attempts)
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("the contention loser got %v, want an error wrapping sdk.ErrConflict", err)
	}
}

// TestTransactCASContentionLive is the counterpart with retries ENABLED: the
// same race the previous case refuses to retry is absorbed by the vendor loop,
// and no increment is lost.
func TestTransactCASContentionLive(t *testing.T) {
	ctx, db := liveTransactFixture(t)
	writers := liveDBWithAttempts(t, 10)
	prefix := livePrefix(t)

	id := prefix + "counter"
	seedLive(t, ctx, db, prefix, id, map[string]any{"n": int64(0)})
	ref := db.Doc(liveTransactCollection, id)

	const (
		goroutines = 2
		increments = 5
	)
	ctx, cancel := context.WithTimeout(ctx, liveContentionTimeout)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range increments {
				err := writers.Transact(ctx, func(txCtx context.Context) error {
					snap, err := writers.ReaderFrom(txCtx).Get(txCtx, ref)
					if err != nil {
						return err
					}
					n, err := snap.DataAt("n")
					if err != nil {
						return err
					}
					return writers.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"n": n.(int64) + 1, "group": prefix})
				})
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("a contending Transact failed: %v", err)
	}
	snap, err := db.ReaderFrom(context.Background()).Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("reading the counter: %v", err)
	}
	n, err := snap.DataAt("n")
	if err != nil {
		t.Fatalf("reading n: %v", err)
	}
	if n != int64(goroutines*increments) {
		t.Errorf("counter = %v, want %d — a read-modify-write was lost", n, goroutines*increments)
	}
}

// liveTransactFixture opens the live database (skipping loudly, or failing under
// FIRESTORE_LIVE_REQUIRED=1) and clears the one collection this leg owns, before
// and after.
func liveTransactFixture(t *testing.T) (context.Context, *firestore.DB) {
	t.Helper()
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveTransactCollection)
	t.Cleanup(func() { firestoretest.ResetLive(t, db, liveTransactCollection) })
	return context.Background(), db
}

// liveDBWithAttempts opens a second handle on the same live database with an
// explicit MaxAttempts, since the cap is a Config value. It reads the same
// environment OpenLive validated, so it cannot point somewhere else.
func liveDBWithAttempts(t *testing.T, attempts int) *firestore.DB {
	t.Helper()
	db, err := firestore.Open(context.Background(), firestore.Config{
		ProjectID:      os.Getenv(firestoretest.LiveProjectEnv),
		DatabaseID:     os.Getenv(firestoretest.LiveDatabaseEnv),
		ConnectTimeout: 15 * time.Second,
		MaxAttempts:    attempts,
	})
	if err != nil {
		t.Fatalf("opening a second live handle: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if db.Emulated() {
		t.Fatalf("the second handle is an EMULATOR client for %s", db.Target())
	}
	return db
}

// livePrefix scopes one test's documents inside the shared collection, so the
// cases can run in any order without seeing each other's fixtures. It is both
// the document-id prefix and the value of the group field every query filters
// on.
func livePrefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s_%d_", firestore.KeyHash(t.Name())[:12], time.Now().UnixNano())
}

// seedLive writes one fixture document, tagged with the group its test queries.
func seedLive(t *testing.T, ctx context.Context, db *firestore.DB, group, id string, data map[string]any) {
	t.Helper()
	data["group"] = group
	if err := db.WriterFrom(ctx).Create(ctx, db.Doc(liveTransactCollection, id), data); err != nil {
		t.Fatalf("seeding %s: %v", id, err)
	}
}

func countLive(t *testing.T, ctx context.Context, r firestore.Reader, q gcfs.Query) int {
	t.Helper()
	it := r.Documents(ctx, q)
	defer it.Stop()

	n := 0
	for {
		_, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return n
		}
		if err != nil {
			t.Fatalf("iterating the live query: %v", firestore.MapError(err))
		}
		n++
	}
}
