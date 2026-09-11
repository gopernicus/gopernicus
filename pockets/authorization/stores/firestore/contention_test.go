package firestore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// The contention classification, hermetically. It decides whether a failed write
// is re-run, and the A7 review found the previous rule wrong in a way no test
// could see: retryability was decided by WHERE the error surfaced (a callback
// return vs. the commit), so a mapped Aborted raised by a transactional READ was
// treated as terminal while the identical condition reported by the COMMIT was
// retried. This pins the replacement — classification by the error's IDENTITY —
// so the two can never diverge again.

// TestRetryableConflictClassifiesByIdentity is the table of every error class
// this store's write paths can end on.
func TestRetryableConflictClassifiesByIdentity(t *testing.T) {
	// A mapped Aborted is what a transactional read hands back after
	// firestoredb.MapError, and it is the row the old rule got wrong.
	mappedAborted := firestoredb.MapError(firestoretest.AbortedError("too much contention"))

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "a mapped Aborted from a transactional read", err: mappedAborted, want: true},
		{name: "a mapped Aborted wrapped by a caller", err: fmt.Errorf("reading the tuple: %w", mappedAborted), want: true},
		{name: "a bare conflict does not prove an abort", err: sdk.ErrConflict, want: false},

		{name: "an invalid command is a shape refusal", err: mutations.ErrInvalidCommand, want: false},
		{name: "a semantic conflict is a permanent refusal", err: mutations.ErrSemanticConflict, want: false},
		{name: "an invariant refusal is permanent", err: mutations.ErrInvariantBlocked, want: false},
		{name: "the reconciliation's relation conflict is deterministic", err: errTargetRelationConflict, want: false},

		{name: "a forbidden guard denial is an answer", err: sdk.ErrForbidden, want: false},
		{name: "row/claim drift is a store-integrity failure", err: sdk.ErrUnavailable, want: false},
		{name: "an invalid input is not contention", err: sdk.ErrInvalidInput, want: false},
		{name: "a cancelled context is not contention", err: context.Canceled, want: false},
		{name: "an unclassified error is not contention", err: errors.New("something else"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableConflict(tc.err); got != tc.want {
				t.Fatalf("retryableConflict(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestGuardReadContentionIsDistinctFromPolicyRefusal(t *testing.T) {
	contention := firestoredb.MapError(firestoretest.AbortedError("guard read contention"))
	readErr := markGuardReadError(contention)
	if !retryableGuardRead(fmt.Errorf("guard lookup: %w", readErr)) || !errors.Is(readErr, contention) {
		t.Fatalf("propagated view read lost retry identity: %v", readErr)
	}
	for _, refusal := range []error{contention, sdk.ErrConflict, mutations.ErrSemanticConflict, mutations.ErrInvariantBlocked,
		errors.Join(readErr, sdk.ErrForbidden)} {
		if retryableGuardRead(refusal) {
			t.Fatalf("policy refusal was classified as retryable: %v", refusal)
		}
	}
	for _, refusal := range []error{mutations.ErrSemanticConflict, mutations.ErrInvariantBlocked} {
		if got := detachVendorRetry(refusal); got != refusal {
			t.Fatalf("refusal identity changed: %v", got)
		}
	}
}

// TestRetryContentionStopsOnTerminalAndDrainsRetryable pins the loop itself: a
// caller-classified terminal error is not re-run however it is spelled, a
// retryable one is, and the budget is bounded.
func TestRetryContentionStopsOnTerminalAndDrainsRetryable(t *testing.T) {
	ctx := context.Background()
	mappedAborted := firestoredb.MapError(firestoretest.AbortedError("contention"))

	t.Run("a guard-returned conflict is terminal", func(t *testing.T) {
		calls := 0
		err := retryContention(ctx, func() (error, bool) {
			calls++
			// A guard may refuse with anything, including a conflict sentinel.
			// It is an authorization ANSWER, so the loop must not re-run it.
			return mappedAborted, true
		})
		if calls != 1 {
			t.Fatalf("fn ran %d times, want 1", calls)
		}
		if !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("err = %v, want the conflict returned unchanged", err)
		}
	})

	t.Run("a retryable conflict clears on a later attempt", func(t *testing.T) {
		calls := 0
		err := retryContention(ctx, func() (error, bool) {
			calls++
			if calls < 3 {
				return mappedAborted, false
			}
			return nil, false
		})
		if err != nil {
			t.Fatalf("retryContention: %v", err)
		}
		if calls != 3 {
			t.Fatalf("fn ran %d times, want 3", calls)
		}
	})

	t.Run("a persistent conflict exhausts the budget and reports it", func(t *testing.T) {
		calls := 0
		err := retryContention(ctx, func() (error, bool) {
			calls++
			return mappedAborted, false
		})
		if calls != contentionMaxRetries+1 {
			t.Fatalf("fn ran %d times, want %d", calls, contentionMaxRetries+1)
		}
		if !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("exhaustion must surface as sdk.ErrConflict, got %v", err)
		}
	})

	t.Run("the final store error is mapped", func(t *testing.T) {
		// A raw vendor status must not leave the store without a sentinel, so
		// the loop runs its answer through MapError once.
		err := retryContention(ctx, func() (error, bool) {
			return firestoretest.AbortedError("raw"), false
		})
		if !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("err = %v, want it mapped to sdk.ErrConflict", err)
		}
	})

	t.Run("a domain refusal is returned byte-identical", func(t *testing.T) {
		refusal := relationships.ErrExpansionBudgetExceeded
		if err := retryContention(ctx, func() (error, bool) { return refusal, false }); err != refusal {
			t.Fatalf("err = %v, want the identical refusal", err)
		}
	})
}

func TestAmbiguousCommitErrorsNeverReplayOperation(t *testing.T) {
	for _, err := range []error{sdk.ErrUnavailable, context.DeadlineExceeded, errors.New("connection lost after commit"), sdk.ErrConflict} {
		calls := 0
		got := retryContention(context.Background(), func() (error, bool) { calls++; return err, false })
		if calls != 1 || got == nil {
			t.Fatalf("ambiguous result replayed: %v calls=%d got=%v", err, calls, got)
		}
	}
}
