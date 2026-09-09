package firestore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestRetryStopsOnPermanentErrors is the C8 fold: boot validation retries a
// database that is not reachable YET, and gives up immediately on one that will
// never accept this credential or this request. Sleeping five backoffs over a
// PermissionDenied delays the failure and buries its cause under a retry count.
func TestRetryStopsOnPermanentErrors(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantAttempts int
	}{
		{"forbidden is permanent", fmt.Errorf("firestore: denied: %w", sdk.ErrForbidden), 1},
		{"unauthorized is permanent", fmt.Errorf("firestore: no credential: %w", sdk.ErrUnauthorized), 1},
		{"invalid input is permanent", fmt.Errorf("firestore: bad database id: %w", sdk.ErrInvalidInput), 1},
		{"unavailable is the race the retry exists for", fmt.Errorf("firestore: dial: %w", sdk.ErrUnavailable), 4},
		{"an unclassified error is retried too", errors.New("firestore: something else"), 4},
	}

	policy := RetryPolicy{Attempts: 4, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			err := retry(context.Background(), policy, func(context.Context) error {
				attempts++
				return tc.err
			})

			if attempts != tc.wantAttempts {
				t.Errorf("fn ran %d times, want %d", attempts, tc.wantAttempts)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("retry returned %v, want the last error %v", err, tc.err)
			}
		})
	}
}

// TestRetrySucceedsAfterATransientFailure keeps the other half honest: a
// permanent-error short circuit must not turn into "never retry".
func TestRetrySucceedsAfterATransientFailure(t *testing.T) {
	attempts := 0
	err := retry(context.Background(), RetryPolicy{Attempts: 3, MinBackoff: time.Millisecond}, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("firestore: not up yet: %w", sdk.ErrUnavailable)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry = %v, want nil once the database came up", err)
	}
	if attempts != 3 {
		t.Errorf("fn ran %d times, want 3", attempts)
	}
}
