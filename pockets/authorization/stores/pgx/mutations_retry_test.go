package pgx

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMutationRetryOnlyTrustedVendorConflicts(t *testing.T) {
	for _, code := range []string{serializationFailure, deadlockDetected} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			err := retryMutation(context.Background(), func() (error, bool) {
				calls++
				if calls == 1 {
					return mapMutationError(fmt.Errorf("commit failed: %w", &pgconn.PgError{Code: code})), false
				}
				return nil, false
			})
			if err != nil || calls != 2 {
				t.Fatalf("trusted conflict: calls=%d err=%v", calls, err)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		err      error
		terminal bool
		want     error
	}{
		{"guarded serialization", &pgconn.PgError{Code: serializationFailure}, true, mutations.ErrConcurrentMutation},
		{"validator refusal", &pgconn.PgError{Code: serializationFailure}, true, mutations.ErrConcurrentMutation},
		{"domain contention without vendor abort", mutations.ErrConcurrentMutation, false, mutations.ErrConcurrentMutation},

		{"policy denial", sdk.ErrForbidden, true, sdk.ErrForbidden},
		{"plain conflict", sdk.ErrConflict, false, sdk.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryMutation(context.Background(), func() (error, bool) { calls++; return tc.err, tc.terminal })
			if calls != 1 || !errors.Is(err, tc.want) {
				t.Fatalf("terminal result: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMutationRetryCancellationAndBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryMutation(ctx, func() (error, bool) {
		calls++
		cancel()
		return &pgconn.PgError{Code: serializationFailure}, false
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry: calls=%d err=%v", calls, err)
	}
	calls = 0
	err = retryMutation(context.Background(), func() (error, bool) {
		calls++
		return &pgconn.PgError{Code: serializationFailure}, false
	})
	if calls != mutationMaxRetries+1 || !errors.Is(err, mutations.ErrConcurrentMutation) {
		t.Fatalf("exhausted retry: calls=%d err=%v", calls, err)
	}
}

func TestDecisionViewHasRoleValidatesBeforeReading(t *testing.T) {
	valid := mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"}
	for _, tc := range []struct {
		name                  string
		scope                 mutations.Target
		role, subjectType, id string
	}{
		{"scope kind", mutations.Target{Kind: "unknown", Type: "doc", ID: "d1"}, "admin", "user", "alice"},
		{"scope id", mutations.Target{Kind: mutations.TargetSubject, Type: "user"}, "admin", "user", "alice"},
		{"role", valid, "", "user", "alice"},
		{"subject type", valid, "admin", "", "alice"},
		{"subject id", valid, "admin", "user", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := newDecisionView(nil, testSchema(t))
			if _, err := view.HasRole(context.Background(), tc.scope, tc.role, tc.subjectType, tc.id); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("invalid role read: %v", err)
			}
		})
	}
}
