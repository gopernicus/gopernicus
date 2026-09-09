//go:build integration && !live

// The C3 emulator leg: Transact and ReadSnapshot against a real server. Skips
// LOUDLY without FIRESTORE_EMULATOR_HOST (see emulator_integration_test.go for
// the container command).
//
// Two behaviors the emulator cannot prove live in transact_live_test.go: the
// strict one-snapshot assertion, and commit-contention exhaustion mapping to
// sdk.ErrConflict. The emulator documents that "not all transaction behavior"
// is implemented and that its locks may take up to 30 seconds to be released,
// so every case here is bounded and none of them races a lock.
package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// contentionTimeout bounds every case that involves a second goroutine, so a
// missing emulator behavior fails the test instead of hanging the package.
const contentionTimeout = 30 * time.Second

// TestTransactCommitsOnNil is the contract's happy path: the callback writes
// through the ambient Writer, returns nil, and the write is visible afterwards.
func TestTransactCommitsOnNil(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "committed")

	err := db.Transact(ctx, func(txCtx context.Context) error {
		if _, ok := firestore.TxFromContext(txCtx); !ok {
			t.Error("TxFromContext inside Transact returned false")
		}
		return db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"})
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if !documentExists(t, ctx, db, ref) {
		t.Error("the committed document is absent after Transact returned nil")
	}
}

// TestTransactRollsBackAndPreservesTheCallbackError is the sdk contract's
// rollback clause proved from both sides: the error comes back byte-identical
// (== the sentinel, not merely errors.Is), and the write the callback queued is
// not there.
func TestTransactRollsBackAndPreservesTheCallbackError(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "doomed")
	sentinel := errors.New("business rule refused")

	err := db.Transact(ctx, func(txCtx context.Context) error {
		if err := db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"}); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("Transact = %v, want the identical sentinel (unwrapped)", err)
	}
	if documentExists(t, ctx, db, ref) {
		t.Error("the rolled-back document exists after Transact returned the callback's error")
	}
}

// TestTransactPanicPropagatesAndWritesNothing pins the third clause of the sdk
// contract. The connector recovers internally so the vendor rolls back rather
// than leaving the transaction open until the server's lock timeout, then
// re-raises the ORIGINAL panic value.
func TestTransactPanicPropagatesAndWritesNothing(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "panicked")

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = db.Transact(ctx, func(txCtx context.Context) error {
			if err := db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"}); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if recovered != "boom" {
		t.Fatalf("recovered %v, want the original panic value \"boom\"", recovered)
	}
	if documentExists(t, ctx, db, ref) {
		t.Error("the document exists after a panicking callback; the transaction was not rolled back")
	}
}

// TestTransactRefusesNestingInsideACallback proves the refusal against a real
// transaction (the hermetic test plants a zero-value one), and that the OUTER
// transaction is unaffected: refusing to nest is not refusing to commit.
func TestTransactRefusesNestingInsideACallback(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "outer")

	err := db.Transact(ctx, func(txCtx context.Context) error {
		nested := db.Transact(txCtx, func(context.Context) error {
			t.Error("the nested callback ran")
			return nil
		})
		if !errors.Is(nested, firestore.ErrNestedTransact) {
			t.Errorf("nested Transact = %v, want ErrNestedTransact", nested)
		}
		return db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"})
	})
	if err != nil {
		t.Fatalf("outer Transact: %v", err)
	}
	if !documentExists(t, ctx, db, ref) {
		t.Error("the outer transaction did not commit after refusing a nested one")
	}
}

// TestTransactReadAfterWrite pins the vendor's reads-before-writes rule through
// the connector's sentinel, on both paths: the read that fails, and the vendor's
// re-check when the callback swallows that failure. Neither commits.
func TestTransactReadAfterWrite(t *testing.T) {
	t.Run("the failing read", func(t *testing.T) {
		ctx, db, collection := transactFixture(t, 0)
		ref := db.Doc(collection, "raw")

		err := db.Transact(ctx, func(txCtx context.Context) error {
			if err := db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"}); err != nil {
				return err
			}
			_, readErr := db.ReaderFrom(txCtx).Get(txCtx, ref)
			return readErr
		})
		if !errors.Is(err, firestore.ErrReadAfterWrite) {
			t.Fatalf("Transact = %v, want ErrReadAfterWrite", err)
		}
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ErrReadAfterWrite does not wrap sdk.ErrInvalidInput: %v", err)
		}
		if documentExists(t, ctx, db, ref) {
			t.Error("a transaction that read after writing still committed")
		}
	})

	t.Run("the swallowed read", func(t *testing.T) {
		ctx, db, collection := transactFixture(t, 0)
		ref := db.Doc(collection, "swallowed")

		err := db.Transact(ctx, func(txCtx context.Context) error {
			if err := db.WriterFrom(txCtx).Create(txCtx, ref, map[string]any{"name": "alpha"}); err != nil {
				return err
			}
			_, _ = db.ReaderFrom(txCtx).Get(txCtx, ref) // deliberately ignored
			return nil
		})
		if !errors.Is(err, firestore.ErrReadAfterWrite) {
			t.Fatalf("Transact = %v, want ErrReadAfterWrite from the vendor's re-check", err)
		}
		if documentExists(t, ctx, db, ref) {
			t.Error("a transaction whose callback hid a read-after-write still committed")
		}
	})
}

// TestTransactCountIsRefused proves C-D3's refusal reaches a store the way it
// will actually meet it: through ReaderFrom inside a live transaction.
func TestTransactCountIsRefused(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)

	err := db.Transact(ctx, func(txCtx context.Context) error {
		_, countErr := db.ReaderFrom(txCtx).Count(txCtx, db.Collection(collection).Query)
		return countErr
	})
	if !errors.Is(err, firestore.ErrCountInTransaction) {
		t.Fatalf("Count inside Transact = %v, want ErrCountInTransaction", err)
	}
}

// TestTransactRetriesAnAbortedCallbackError is C0 finding N4 made executable:
// the vendor decides whether to retry by asking whether the error is or wraps a
// gRPC Aborted status, INCLUDING one the callback returned. A raw Aborted from
// the callback therefore re-runs it; the second attempt's work commits and the
// first attempt's does not.
func TestTransactRetriesAnAbortedCallbackError(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	first, second := db.Doc(collection, "attempt-1"), db.Doc(collection, "attempt-2")

	attempts := 0
	err := db.Transact(ctx, func(txCtx context.Context) error {
		attempts++
		if attempts == 1 {
			if err := db.WriterFrom(txCtx).Create(txCtx, first, map[string]any{"n": 1}); err != nil {
				return err
			}
			return status.Error(codes.Aborted, "forced contention")
		}
		return db.WriterFrom(txCtx).Create(txCtx, second, map[string]any{"n": 2})
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("the callback ran %d times, want 2 (a raw Aborted must be retried)", attempts)
	}
	if documentExists(t, ctx, db, first) {
		t.Error("a losing attempt's queued write was committed")
	}
	if !documentExists(t, ctx, db, second) {
		t.Error("the winning attempt's write is absent")
	}
}

// TestTransactDoesNotRetryAMappedAbortedError is the other half of N4, and the
// reason the store rule is "map before you return": MapError keeps the server's
// message but not its status, so a mapped Aborted ends the transaction instead
// of silently re-running the callback — and comes back unwrapped.
func TestTransactDoesNotRetryAMappedAbortedError(t *testing.T) {
	ctx, db, _ := transactFixture(t, 0)

	mapped := firestore.MapError(status.Error(codes.Aborted, "already interpreted"))
	attempts := 0
	err := db.Transact(ctx, func(context.Context) error {
		attempts++
		return mapped
	})
	if attempts != 1 {
		t.Fatalf("the callback ran %d times, want 1 (a mapped error must not be retried)", attempts)
	}
	if err != mapped {
		t.Fatalf("Transact = %v, want the identical mapped error", err)
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("the mapped Aborted no longer wraps sdk.ErrConflict: %v", err)
	}
}

// TestTransactExhaustsAttemptsOnAPersistentAbortedCallback bounds the retry
// loop at Config.MaxAttempts and pins what a caller gets when the loop ends on
// a CALLBACK error: that error, unwrapped, never remapped into a generic
// conflict. (Exhaustion on a losing COMMIT is the other case; it maps to
// sdk.ErrConflict — see TestTransactCommitContentionOutcome and the live leg.)
func TestTransactExhaustsAttemptsOnAPersistentAbortedCallback(t *testing.T) {
	ctx, db, _ := transactFixture(t, 3)

	aborted := status.Error(codes.Aborted, "never settles")
	attempts := 0
	err := db.Transact(ctx, func(context.Context) error {
		attempts++
		return aborted
	})
	if attempts != 3 {
		t.Fatalf("the callback ran %d times, want MaxAttempts (3)", attempts)
	}
	if err != aborted {
		t.Fatalf("Transact = %v, want the identical callback error", err)
	}
}

// TestTransactResetsAttemptLocalOutcome is the C-D4 discipline a store must
// follow, proved: a callback that records an outcome in its enclosing scope
// must reset it at the top of every attempt, because a losing attempt's answer
// is not the answer of the transaction that committed. The retry is forced by a
// test-controlled attempt counter, not by a race, so the case is deterministic.
func TestTransactResetsAttemptLocalOutcome(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "subject")

	seedDoc(t, ctx, db, ref, map[string]any{"state": "settled"})

	var (
		outcome  string
		attempts int
	)
	err := db.Transact(ctx, func(txCtx context.Context) error {
		outcome = "" // the discipline under test
		attempts++

		snap, err := db.ReaderFrom(txCtx).Get(txCtx, ref)
		if err != nil {
			return err
		}
		state, err := snap.DataAt("state")
		if err != nil {
			return err
		}
		if attempts == 1 {
			// A losing attempt: it reaches a conclusion and queues a write, and
			// neither may survive.
			outcome = "stale"
			if err := db.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"state": "stale"}); err != nil {
				return err
			}
			return status.Error(codes.Aborted, "forced contention")
		}
		outcome = fmt.Sprintf("observed:%v", state)
		return db.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"state": "final"})
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("the callback ran %d times, want 2", attempts)
	}
	if outcome != "observed:settled" {
		t.Errorf("outcome = %q, want the winning attempt's value; a losing attempt leaked", outcome)
	}
	if got := documentField(t, ctx, db, ref, "state"); got != "final" {
		t.Errorf("state = %v, want final (a losing attempt's write committed)", got)
	}
}

// TestTransactCASContention is the reason the callback may run more than once:
// two goroutines read-modify-write the same counter, and the vendor's retry loop
// makes the increments serialize. Exactly 2N or the transaction seam lost a
// write. Bounded: 2x5 transactions, MaxAttempts 10, one shared document.
func TestTransactCASContention(t *testing.T) {
	ctx, db, collection := transactFixture(t, 10)
	ref := db.Doc(collection, "counter")
	seedDoc(t, ctx, db, ref, map[string]any{"n": int64(0)})

	const (
		writers    = 2
		increments = 5
	)
	ctx, cancel := context.WithTimeout(ctx, contentionTimeout)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range increments {
				err := db.Transact(ctx, func(txCtx context.Context) error {
					snap, err := db.ReaderFrom(txCtx).Get(txCtx, ref)
					if err != nil {
						return err
					}
					n, err := snap.DataAt("n")
					if err != nil {
						return err
					}
					return db.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"n": n.(int64) + 1})
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
	if got := documentField(t, context.Background(), db, ref, "n"); got != int64(writers*increments) {
		t.Errorf("counter = %v after %d concurrent increments, want %d — a read-modify-write was lost",
			got, writers*increments, writers*increments)
	}
}

// TestTransactCommitThenReportsExpired is the first C-D4 outcome pattern: the
// operation's domain answer is NOT the callback's error. Consuming an expired
// single-use token still has to delete it, so the callback returns nil (the
// delete commits) and the caller reports sdk.ErrExpired afterwards.
func TestTransactCommitThenReportsExpired(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "token")
	seedDoc(t, ctx, db, ref, map[string]any{"expires_at": time.Now().Add(-time.Hour)})

	consume := func() error {
		var expired bool
		if err := db.Transact(ctx, func(txCtx context.Context) error {
			expired = false
			snap, err := db.ReaderFrom(txCtx).Get(txCtx, ref)
			if err != nil {
				return err
			}
			at, err := snap.DataAt("expires_at")
			if err != nil {
				return err
			}
			expired = at.(time.Time).Before(time.Now())
			return db.WriterFrom(txCtx).Delete(txCtx, ref)
		}); err != nil {
			return err
		}
		if expired {
			return sdk.ErrExpired
		}
		return nil
	}

	if err := consume(); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("consume = %v, want sdk.ErrExpired", err)
	}
	if documentExists(t, ctx, db, ref) {
		t.Error("the expired token still exists; its deletion was rolled back by a domain outcome")
	}
}

// TestTransactStableRejectionRollsBack is the second C-D4 pattern, and the
// reason the first one is a decision rather than a default: a rejection that
// must leave the database untouched returns its domain error from the callback,
// and everything it queued is discarded.
func TestTransactStableRejectionRollsBack(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "attempt")
	seedDoc(t, ctx, db, ref, map[string]any{"state": "pending"})

	rejected := errors.New("passwordless: stable rejection")
	err := db.Transact(ctx, func(txCtx context.Context) error {
		if _, err := db.ReaderFrom(txCtx).Get(txCtx, ref); err != nil {
			return err
		}
		if err := db.WriterFrom(txCtx).Delete(txCtx, ref); err != nil {
			return err
		}
		return rejected
	})
	if err != rejected {
		t.Fatalf("Transact = %v, want the identical domain error", err)
	}
	if !documentExists(t, ctx, db, ref) {
		t.Error("a stable rejection deleted the document; the rollback did not happen")
	}
}

// TestReadSnapshotIsOneSnapshot runs two queries around a concurrent insert and
// asserts the second query does not see it. The handshake makes the ordering a
// fact rather than a race: the writer starts only after the first query has
// already run INSIDE the transaction, and the second query starts only after
// the write has been acknowledged.
//
// The emulator was measured holding the read-only snapshot on every run of this
// case, so the assertion is strict. It is still not proof about PRODUCTION —
// the emulator documents that not all transaction behavior is implemented — so
// TestReadSnapshotIsOneSnapshotLive asserts the same thing against a real
// database, and that is the one the release gate needs.
func TestReadSnapshotIsOneSnapshot(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	seedDoc(t, ctx, db, db.Doc(collection, "seeded"), map[string]any{"n": int64(1)})

	query := db.Collection(collection).Query
	started, written := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(written)
		<-started
		if err := db.WriterFrom(ctx).Create(ctx, db.Doc(collection, "inserted"), map[string]any{"n": int64(2)}); err != nil {
			t.Errorf("the concurrent writer failed: %v", err)
		}
	}()

	var first, second int
	err := db.ReadSnapshot(ctx, func(snapCtx context.Context, r firestore.Reader) error {
		if got, ok := firestore.TxFromContext(snapCtx); !ok || got == nil {
			t.Error("the snapshot context carries no transaction")
		}
		first = countByIterating(t, snapCtx, r, query)
		close(started)
		select {
		case <-written:
		case <-time.After(contentionTimeout):
			t.Fatal("the concurrent writer did not finish")
		}
		second = countByIterating(t, snapCtx, r, query)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if first != 1 {
		t.Fatalf("the first query saw %d documents, want 1", first)
	}
	if second != first {
		t.Errorf("the second query under the same snapshot saw %d documents, want %d — a document committed "+
			"after the snapshot was taken became visible inside it", second, first)
	}
	if outside := countByIterating(t, ctx, db.ReaderFrom(ctx), query); outside != 2 {
		t.Errorf("outside the snapshot the collection holds %d documents, want 2 — the concurrent insert never landed, "+
			"so the invisibility above proves nothing", outside)
	}
}

// TestReadSnapshotRefusesEveryWrite is the guarantee ReadSnapshot advertises,
// against a real transaction: WriterFrom on the snapshot context refuses all
// four methods, Count stays unavailable as in any transaction, and the snapshot
// still completes.
func TestReadSnapshotRefusesEveryWrite(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "untouched")
	seedDoc(t, ctx, db, ref, map[string]any{"state": "pending"})

	err := db.ReadSnapshot(ctx, func(snapCtx context.Context, r firestore.Reader) error {
		if _, err := r.Get(snapCtx, ref); err != nil {
			return err
		}
		w := db.WriterFrom(snapCtx)
		for name, writeErr := range map[string]error{
			"Create": w.Create(snapCtx, db.Doc(collection, "new"), map[string]any{"n": 1}),
			"Set":    w.Set(snapCtx, ref, map[string]any{"state": "changed"}),
			"Update": w.Update(snapCtx, ref, []gcfs.Update{{Path: "state", Value: "changed"}}),
			"Delete": w.Delete(snapCtx, ref),
		} {
			if !errors.Is(writeErr, firestore.ErrWriteInReadOnlyTransaction) {
				t.Errorf("%s inside ReadSnapshot = %v, want ErrWriteInReadOnlyTransaction", name, writeErr)
			}
		}
		if _, countErr := r.Count(snapCtx, db.Collection(collection).Query); !errors.Is(countErr, firestore.ErrCountInTransaction) {
			t.Errorf("Count inside ReadSnapshot = %v, want ErrCountInTransaction", countErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if got := documentField(t, ctx, db, ref, "state"); got != "pending" {
		t.Errorf("state = %v after a snapshot, want pending — a write got through", got)
	}
	if documentExists(t, ctx, db, db.Doc(collection, "new")) {
		t.Error("a document created inside a snapshot exists")
	}
}

// TestReadSnapshotReturnsTheCallbackErrorUnwrapped: a snapshot has nothing to
// roll back, but the error contract is the same as Transact's.
func TestReadSnapshotReturnsTheCallbackErrorUnwrapped(t *testing.T) {
	ctx, db, _ := transactFixture(t, 0)

	sentinel := errors.New("the reader gave up")
	runs := 0
	err := db.ReadSnapshot(ctx, func(context.Context, firestore.Reader) error {
		runs++
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("ReadSnapshot = %v, want the identical callback error", err)
	}
	if runs != 1 {
		t.Errorf("the snapshot callback ran %d times; a read-only transaction is never retried", runs)
	}
}

// TestReadSnapshotInsideTransactReusesIt proves the C-D3 reuse decision against
// a real transaction: no nested RunTransaction (the vendor refuses those), the
// same reader, and the enclosing transaction still commits.
func TestReadSnapshotInsideTransactReusesIt(t *testing.T) {
	ctx, db, collection := transactFixture(t, 0)
	ref := db.Doc(collection, "subject")
	seedDoc(t, ctx, db, ref, map[string]any{"state": "pending"})

	err := db.Transact(ctx, func(txCtx context.Context) error {
		tx, _ := firestore.TxFromContext(txCtx)
		if err := db.ReadSnapshot(txCtx, func(snapCtx context.Context, r firestore.Reader) error {
			if got, ok := firestore.TxFromContext(snapCtx); !ok || got != tx {
				t.Error("the reused snapshot did not carry the enclosing transaction")
			}
			_, err := r.Get(snapCtx, ref)
			return err
		}); err != nil {
			return err
		}
		return db.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"state": "final"})
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if got := documentField(t, ctx, db, ref, "state"); got != "final" {
		t.Errorf("state = %v, want final", got)
	}
}

// TestTransactCommitContentionOutcome probes whether the EMULATOR can produce
// the commit-side contention loss that maps to sdk.ErrConflict: a transaction
// reads a document, a concurrent client write bumps it, and the transaction
// then commits with MaxAttempts 1 (no retry). Real Firestore aborts that commit.
//
// The emulator's locking may serialize the outside write instead, in which case
// the commit succeeds — so this case ASSERTS only that the outcome is one of the
// two legitimate ones (nil, or an error wrapping sdk.ErrConflict) and never some
// third thing, and LOGS which one happened. The strict assertion is
// TestTransactContentionExhaustionLive.
func TestTransactCommitContentionOutcome(t *testing.T) {
	ctx, db, collection := transactFixture(t, 1)
	ref := db.Doc(collection, "contended")
	seedDoc(t, ctx, db, ref, map[string]any{"n": int64(0)})

	read, bumped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(bumped)
		<-read
		bumpCtx, cancel := context.WithTimeout(context.Background(), contentionTimeout)
		defer cancel()
		if err := db.WriterFrom(bumpCtx).Set(bumpCtx, ref, map[string]any{"n": int64(99)}); err != nil {
			t.Logf("the concurrent bump failed (the emulator may hold a read lock): %v", err)
		}
	}()

	attempts := 0
	err := db.Transact(ctx, func(txCtx context.Context) error {
		attempts++
		if _, err := db.ReaderFrom(txCtx).Get(txCtx, ref); err != nil {
			return err
		}
		close(read)
		select {
		case <-bumped:
		case <-time.After(5 * time.Second):
			t.Log("the concurrent bump did not complete within 5s; the emulator is holding the read lock")
		}
		return db.WriterFrom(txCtx).Set(txCtx, ref, map[string]any{"n": int64(1)})
	})
	<-bumped

	if attempts != 1 {
		t.Errorf("the callback ran %d times with MaxAttempts 1", attempts)
	}
	switch {
	case err == nil:
		t.Log("the emulator did NOT abort the contended commit; commit-contention exhaustion is proven only by the live leg")
	case errors.Is(err, sdk.ErrConflict):
		t.Log("the emulator aborted the contended commit and it mapped to sdk.ErrConflict")
	default:
		t.Fatalf("contended Transact = %v, want nil or an error wrapping sdk.ErrConflict", err)
	}
}

// transactFixture opens the emulator with the given MaxAttempts (0 = the
// connector's default) and returns a collection name unique to this test, so
// the cases stay isolated without a shared reset.
func transactFixture(t *testing.T, maxAttempts int) (context.Context, *firestore.DB, string) {
	t.Helper()
	requireEmulator(t)

	ctx := context.Background()
	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      emulatorProject(),
		ConnectTimeout: 15 * time.Second,
		MaxAttempts:    maxAttempts,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// A subtest's name contains a slash, and a collection id may not: an
	// invalid path makes the vendor hand back a NIL reference whose first use
	// fails as "nil DocumentRef", which reads like a connector bug.
	name := strings.ReplaceAll(t.Name(), "/", "_")
	return ctx, db, fmt.Sprintf("c3_%s_%d", name, time.Now().UnixNano())
}

// seed writes one document outside any transaction.
func seedDoc(t *testing.T, ctx context.Context, db *firestore.DB, ref *gcfs.DocumentRef, data map[string]any) {
	t.Helper()
	if err := db.WriterFrom(ctx).Create(ctx, ref, data); err != nil {
		t.Fatalf("seeding %s: %v", ref.Path, err)
	}
}

// documentExists reports whether ref exists, read outside any transaction.
func documentExists(t *testing.T, ctx context.Context, db *firestore.DB, ref *gcfs.DocumentRef) bool {
	t.Helper()
	snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if errors.Is(err, sdk.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("reading %s: %v", ref.Path, err)
	}
	return snap.Exists()
}

// documentField reads one field of an existing document outside any transaction.
func documentField(t *testing.T, ctx context.Context, db *firestore.DB, ref *gcfs.DocumentRef, field string) any {
	t.Helper()
	snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if err != nil {
		t.Fatalf("reading %s: %v", ref.Path, err)
	}
	value, err := snap.DataAt(field)
	if err != nil {
		t.Fatalf("reading %s.%s: %v", ref.Path, field, err)
	}
	return value
}

// countByIterating counts a query through a Reader, which is how a snapshot
// counts: Count is unavailable inside any transaction (ErrCountInTransaction).
func countByIterating(t *testing.T, ctx context.Context, r firestore.Reader, q gcfs.Query) int {
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
			t.Fatalf("iterating under a snapshot: %v", firestore.MapError(err))
		}
		n++
	}
}
