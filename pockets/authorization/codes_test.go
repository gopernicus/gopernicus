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
// transport mapping. ErrNoDecisionKind reports a SERVER-SIDE WIRING FAULT — a
// deployment whose decision surface bears no model — so it wraps no sdk taxonomy
// kind and surfaces as a 500, consistent with the RequirePermission gates that
// panic at mount for the same wiring. It is never a 403: a host that wired no
// model must not be told "denied", which would imply some other principal could
// succeed. It deliberately DIFFERS from ErrMutationsNotConfigured (400), the
// precondition an actor can observe on a correctly deployed host.
func TestNoDecisionKindIsAWiringFaultNotADeny(t *testing.T) {
	if errors.Is(ErrNoDecisionKind, sdk.ErrInvalidInput) {
		t.Fatalf("ErrNoDecisionKind must not wrap sdk.ErrInvalidInput — it is a wiring fault, not bad input")
	}
	for _, kind := range []error{sdk.ErrForbidden, sdk.ErrUnauthorized, sdk.ErrUnavailable, sdk.ErrConflict} {
		if errors.Is(ErrNoDecisionKind, kind) {
			t.Fatalf("ErrNoDecisionKind must not wrap %v", kind)
		}
	}
	if sdk.IsExpected(ErrNoDecisionKind) {
		t.Fatalf("ErrNoDecisionKind must not be an expected sdk kind")
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
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ErrNoDecisionKind maps to %d, want %d — an unclassified server-side fault", rec.Code, http.StatusInternalServerError)
	}
	// ReasonFor does not own it: a wiring fault carries no decision reason and the
	// caller treats it as infrastructure.
	if reason, ok := ReasonFor(ErrNoDecisionKind); ok {
		t.Fatalf("ReasonFor(ErrNoDecisionKind) = (%q, true), want ok=false", reason)
	}
	// The actor-observable precondition refusal keeps its 400: the two "not
	// configured" sentinels answer DIFFERENT statuses by design.
	mutations := httptest.NewRecorder()
	RespondError(mutations, ErrMutationsNotConfigured)
	if mutations.Code != http.StatusBadRequest {
		t.Fatalf("ErrMutationsNotConfigured maps to %d, want %d", mutations.Code, http.StatusBadRequest)
	}
	if rec.Code == mutations.Code {
		t.Fatalf("the wiring fault (%d) and the actor-observable precondition (%d) must not share a status",
			rec.Code, mutations.Code)
	}
}
