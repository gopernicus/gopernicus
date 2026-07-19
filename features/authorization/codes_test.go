package authorization

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestVocabularyErrorKinds pins each stable sentinel to the sdk taxonomy kind
// that fixes its transport mapping. Limit exhaustion wraps sdk.ErrUnavailable —
// never sdk.ErrConflict and never a new kind (default #9).
func TestVocabularyErrorKinds(t *testing.T) {
	cases := []struct {
		err  error
		kind error
	}{
		{ErrInvalidRequest, sdk.ErrInvalidInput},
		{ErrUnknownSymbol, sdk.ErrInvalidInput},
		{ErrEvaluationLimit, sdk.ErrUnavailable},
		{ErrStaleRevision, sdk.ErrConflict},
		{ErrInvariantConflict, sdk.ErrConflict},
		{ErrMutationMismatch, sdk.ErrConflict},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.kind) {
			t.Fatalf("%v must wrap %v", tc.err, tc.kind)
		}
	}
	// Evaluation-limit exhaustion must NOT be a conflict.
	if errors.Is(ErrEvaluationLimit, sdk.ErrConflict) {
		t.Fatalf("ErrEvaluationLimit must not wrap sdk.ErrConflict")
	}
	// Infrastructure failure wraps no expected sentinel → maps to 500.
	if sdk.IsExpected(ErrInfrastructure) {
		t.Fatalf("ErrInfrastructure must not be an expected sdk kind")
	}
}

// TestVocabularyReasonFor proves the shared classifier maps wrapped sentinels to
// their stable Reason, and returns ok=false for an unowned error.
func TestVocabularyReasonFor(t *testing.T) {
	cases := []struct {
		err  error
		want Reason
	}{
		{fmt.Errorf("ctx: %w", ErrEvaluationLimit), ReasonEvaluationLimit},
		{fmt.Errorf("ctx: %w", ErrStaleRevision), ReasonStaleRevision},
		{fmt.Errorf("ctx: %w", ErrMutationMismatch), ReasonMutationMismatch},
		{fmt.Errorf("ctx: %w", ErrInvariantConflict), ReasonInvariantConflict},
		{fmt.Errorf("ctx: %w", ErrUnknownSymbol), ReasonUnknownSymbol},
		{fmt.Errorf("ctx: %w", ErrInvalidRequest), ReasonInvalidRequest},
	}
	for _, tc := range cases {
		got, ok := ReasonFor(tc.err)
		if !ok || got != tc.want {
			t.Fatalf("ReasonFor(%v) = (%q, %v), want (%q, true)", tc.err, got, ok, tc.want)
		}
	}
	if _, ok := ReasonFor(errors.New("unowned")); ok {
		t.Fatalf("ReasonFor(unowned) must return ok=false")
	}
	if _, ok := ReasonFor(nil); ok {
		t.Fatalf("ReasonFor(nil) must return ok=false")
	}
}
