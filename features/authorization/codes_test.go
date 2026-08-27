package authorization

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestNoDecisionKindIsAWiringFaultNotADeny pins the sentinel's taxonomy and its
// transport mapping. ErrNoDecisionKind is a stable PRECONDITION refusal — the
// ErrMutationsNotConfigured precedent — so it wraps sdk.ErrInvalidInput and NEVER
// sdk.ErrForbidden: a host that wired no model must not be told "denied", which
// would imply some other principal could succeed. Through the feature's web.Error
// seam it therefore surfaces as the same 400 the mutations-not-configured
// sentinel does, never a 403.
func TestNoDecisionKindIsAWiringFaultNotADeny(t *testing.T) {
	if !errors.Is(ErrNoDecisionKind, sdk.ErrInvalidInput) {
		t.Fatalf("ErrNoDecisionKind must wrap sdk.ErrInvalidInput")
	}
	for _, kind := range []error{sdk.ErrForbidden, sdk.ErrUnauthorized, sdk.ErrUnavailable, sdk.ErrConflict} {
		if errors.Is(ErrNoDecisionKind, kind) {
			t.Fatalf("ErrNoDecisionKind must not wrap %v", kind)
		}
	}
	// It is a distinct identity: it does NOT wrap the relationship-kind sentinel,
	// so a host branching on ErrRelationshipsNotConfigured cannot silently catch it.
	if errors.Is(ErrNoDecisionKind, ErrRelationshipsNotConfigured) {
		t.Fatalf("ErrNoDecisionKind must be a clean identity, not a wrap of ErrRelationshipsNotConfigured")
	}

	rec := httptest.NewRecorder()
	RespondError(rec, fmt.Errorf("Check: %w", ErrNoDecisionKind))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("a wiring fault must never surface as a deny (403)")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ErrNoDecisionKind maps to %d, want %d — sdk.ErrInvalidInput's status", rec.Code, http.StatusBadRequest)
	}
	mutations := httptest.NewRecorder()
	RespondError(mutations, ErrMutationsNotConfigured)
	if rec.Code != mutations.Code {
		t.Fatalf("ErrNoDecisionKind maps to %d; its precedent ErrMutationsNotConfigured maps to %d — the two precondition refusals must agree",
			rec.Code, mutations.Code)
	}
}
