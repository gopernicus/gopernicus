package firestore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestDBIsATransactor restates the compile-time assertion as a runtime one and
// calls the seam through the interface, so the sdk contract is exercised the
// way a composition would exercise it — by type, not by concrete method.
func TestDBIsATransactor(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	var transactor transaction.Transactor = openForTest(t)
	ctx := withTx(context.Background(), &gcfs.Transaction{}, false)

	if err := transactor.Transact(ctx, func(context.Context) error {
		t.Fatal("the callback ran despite an ambient transaction")
		return nil
	}); !errors.Is(err, ErrNestedTransact) {
		t.Fatalf("nested Transact through transaction.Transactor = %v, want ErrNestedTransact", err)
	}
}

// TestTransactRefusesNestingBeforeTheVendor proves the refusal is a decision
// this package makes, not one it discovers: both a read-write and a read-only
// ambient transaction refuse, the callback never runs, and no RPC is attempted
// (the client points at a dead address, so a wire call would hang or fail
// differently).
func TestTransactRefusesNestingBeforeTheVendor(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")
	db := openForTest(t)

	for name, ctx := range map[string]context.Context{
		"inside Transact":     withTx(context.Background(), &gcfs.Transaction{}, false),
		"inside ReadSnapshot": withTx(context.Background(), &gcfs.Transaction{}, true),
	} {
		ran := false
		err := db.Transact(ctx, func(context.Context) error {
			ran = true
			return nil
		})
		if !errors.Is(err, ErrNestedTransact) {
			t.Errorf("Transact %s = %v, want ErrNestedTransact", name, err)
		}
		if ran {
			t.Errorf("Transact %s ran its callback before refusing", name)
		}
	}
}

// TestReadSnapshotReusesTheAmbientTransaction is the C-D3 decision in isolation:
// called inside a connector transaction, ReadSnapshot does NOT begin a second
// one (the vendor would refuse) — it hands the caller the ambient transaction's
// Reader, on the ambient context, and returns the callback's error unwrapped.
// Hermetic: reuse means no RunTransaction, so nothing reaches the wire.
func TestReadSnapshotReusesTheAmbientTransaction(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")
	db := openForTest(t)

	tx := &gcfs.Transaction{}
	sentinel := errors.New("the callback refused")

	for name, readOnly := range map[string]bool{"read-write": false, "read-only": true} {
		ctx := withTx(context.Background(), tx, readOnly)
		ran := false

		err := db.ReadSnapshot(ctx, func(snapCtx context.Context, r Reader) error {
			ran = true
			tr, ok := r.(txReader)
			if !ok || tr.tx != tx {
				t.Errorf("%s: ReadSnapshot passed %T, want the ambient transaction's Reader", name, r)
			}
			if got, ok := TxFromContext(snapCtx); !ok || got != tx {
				t.Errorf("%s: the snapshot context lost the ambient transaction", name)
			}
			if tr, ok := ReaderFromIsTx(db.ReaderFrom(snapCtx)); !ok || tr.tx != tx {
				t.Errorf("%s: ReaderFrom inside the reused snapshot did not resolve to it", name)
			}
			// C8 fold: the reused context is re-stashed READ-ONLY whichever
			// kind it reused, so WriterFrom refuses at the seam. A snapshot
			// that inherited a read-write stash would let a helper written
			// against ReadSnapshot queue writes into its caller's transaction.
			if _, ok := db.WriterFrom(snapCtx).(readOnlyWriter); !ok {
				t.Errorf("%s: WriterFrom inside the reused snapshot returned %T, want readOnlyWriter", name, db.WriterFrom(snapCtx))
			}
			return sentinel
		})
		if !ran {
			t.Fatalf("%s: ReadSnapshot did not run its callback", name)
		}
		if err != sentinel {
			t.Errorf("%s: ReadSnapshot = %v, want the identical callback error", name, err)
		}
	}
}

// TestAttemptStateOutcomes pins the three-way decision the retry loop needs,
// without a server: a callback error comes back byte-identical, a vendor error
// is mapped, and state from a losing attempt never leaks into the next one.
func TestAttemptStateOutcomes(t *testing.T) {
	callbackErr := errors.New("domain refusal")

	for _, failure := range []error{callbackErr, fmt.Errorf("passwordless rejected: %w", sdk.ErrForbidden)} {
		t.Run(failure.Error(), func(t *testing.T) {
			s := &attemptState{}
			returned := failure
			s.beginAttempt(&returned)()
			if got := s.result(returned); got != failure {
				t.Fatalf("result = %v, want the identical callback error", got)
			}
		})
	}

	t.Run("callback Aborted remains retryable and classified", func(t *testing.T) {
		s := &attemptState{}
		var returned error = status.Error(codes.Aborted, "forced")
		s.beginAttempt(&returned)()
		if code := status.Code(returned); code != codes.Aborted {
			t.Fatalf("wrapped callback status = %s, want Aborted", code)
		}
		got := s.result(returned)
		if !errors.Is(got, sdk.ErrConflict) || status.Code(got) != codes.Aborted {
			t.Fatalf("result = %v, want retryable mapped contention", got)
		}
	})

	t.Run("vendor error is mapped", func(t *testing.T) {
		s := &attemptState{}
		got := s.result(status.Error(codes.Aborted, "commit contention"))
		if !errors.Is(got, sdk.ErrConflict) {
			t.Fatalf("result = %v, want sdk.ErrConflict", got)
		}
	})

	t.Run("a losing attempt does not mask a terminal error", func(t *testing.T) {
		s := &attemptState{}
		var returned error = status.Error(codes.Aborted, "read contention")
		s.beginAttempt(&returned)()
		if got := s.result(status.Error(codes.PermissionDenied, "next begin refused")); !errors.Is(got, sdk.ErrForbidden) {
			t.Fatalf("result = %v, want sdk.ErrForbidden", got)
		}
		if got := s.result(context.Canceled); !errors.Is(got, context.Canceled) {
			t.Fatalf("result = %v, want context.Canceled", got)
		}
		if got := s.result(nil); got != nil {
			t.Fatalf("successful retry result = %v", got)
		}
	})

	t.Run("a panic is converted for the vendor and re-raised", func(t *testing.T) {
		s := &attemptState{}
		var handed error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("beginAttempt let the panic escape the callback: %v", r)
				}
			}()
			var rerr error
			defer func() { handed = rerr }()
			defer s.beginAttempt(&rerr)()
			panic("boom")
		}()
		if !errors.Is(handed, errPanicInTransact) {
			t.Fatalf("the vendor was handed %v, want errPanicInTransact so it rolls back", handed)
		}

		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("re-raised %v, want the original panic value", r)
			}
		}()
		_ = s.result(errPanicInTransact)
		t.Fatal("result did not re-panic")
	})
}

// TestAttemptsDefaults covers the retry cap resolution, including a DB that
// never went through Open (whose zero cap would make the vendor's loop run zero
// times and return nil having committed nothing).
func TestAttemptsDefaults(t *testing.T) {
	for name, tc := range map[string]struct{ set, want int }{
		"zero":     {0, DefaultMaxAttempts},
		"negative": {-3, DefaultMaxAttempts},
		"one":      {1, 1},
		"seven":    {7, 7},
	} {
		if got := (&DB{maxAttempts: tc.set}).attempts(); got != tc.want {
			t.Errorf("%s: attempts() = %d, want %d", name, got, tc.want)
		}
	}
	if DefaultMaxAttempts != gcfs.DefaultTransactionMaxAttempts {
		t.Errorf("DefaultMaxAttempts = %d, want the vendor's %d", DefaultMaxAttempts, gcfs.DefaultTransactionMaxAttempts)
	}
}

// TestOpenDefaultsMaxAttempts proves Config's default reaches the DB, so a host
// that never names MaxAttempts still gets the vendor's contention retry.
func TestOpenDefaultsMaxAttempts(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db := openForTest(t)
	if db.maxAttempts != DefaultMaxAttempts {
		t.Errorf("Open with no MaxAttempts set maxAttempts = %d, want %d", db.maxAttempts, DefaultMaxAttempts)
	}

	configured, err := Open(context.Background(), Config{ProjectID: "gopernicus-test", MaxAttempts: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { configured.Close() })
	if configured.maxAttempts != 2 {
		t.Errorf("Open with MaxAttempts 2 set maxAttempts = %d", configured.maxAttempts)
	}
}
