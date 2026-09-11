package authorization

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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
		{authmodel.ErrInvalidRequest, sdk.ErrInvalidInput},
		{authmodel.ErrUnknownSymbol, sdk.ErrInvalidInput},
		{authmodel.ErrEvaluationLimit, sdk.ErrUnavailable},
		{mutations.ErrConcurrentMutation, sdk.ErrConflict},
		{mutations.ErrInvariantBlocked, sdk.ErrConflict},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.kind) {
			t.Fatalf("%v must wrap %v", tc.err, tc.kind)
		}
	}
	// Evaluation-limit exhaustion must NOT be a conflict.
	if errors.Is(authmodel.ErrEvaluationLimit, sdk.ErrConflict) {
		t.Fatalf("ErrEvaluationLimit must not wrap sdk.ErrConflict")
	}
	// Infrastructure failure wraps no expected sentinel → maps to 500.
	if sdk.IsExpected(authmodel.ErrInfrastructure) {
		t.Fatalf("ErrInfrastructure must not be an expected sdk kind")
	}
}

// TestVocabularyReasonFor proves the shared classifier maps wrapped sentinels to
// their stable Reason, and returns ok=false for an unowned error.
func TestVocabularyReasonFor(t *testing.T) {
	cases := []struct {
		err  error
		want authmodel.Reason
	}{
		{fmt.Errorf("ctx: %w", authmodel.ErrEvaluationLimit), authmodel.ReasonEvaluationLimit},
		{fmt.Errorf("ctx: %w", mutations.ErrConcurrentMutation), authmodel.ReasonConcurrentMutation},
		{fmt.Errorf("ctx: %w", mutations.ErrInvariantBlocked), authmodel.ReasonInvariantConflict},
		{fmt.Errorf("ctx: %w", authmodel.ErrUnknownSymbol), authmodel.ReasonUnknownSymbol},
		{fmt.Errorf("ctx: %w", authmodel.ErrInvalidRequest), authmodel.ReasonInvalidRequest},
	}
	for _, tc := range cases {
		got, ok := mutations.ReasonFor(tc.err)
		if !ok || got != tc.want {
			t.Fatalf("ReasonFor(%v) = (%q, %v), want (%q, true)", tc.err, got, ok, tc.want)
		}
	}
	if _, ok := mutations.ReasonFor(errors.New("unowned")); ok {
		t.Fatalf("ReasonFor(unowned) must return ok=false")
	}
	if _, ok := mutations.ReasonFor(nil); ok {
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
	if errors.Is(authmodel.ErrNoDecisionKind, sdk.ErrInvalidInput) {
		t.Fatalf("ErrNoDecisionKind must not wrap sdk.ErrInvalidInput — it is a wiring fault, not bad input")
	}
	for _, kind := range []error{sdk.ErrForbidden, sdk.ErrUnauthorized, sdk.ErrUnavailable, sdk.ErrConflict} {
		if errors.Is(authmodel.ErrNoDecisionKind, kind) {
			t.Fatalf("ErrNoDecisionKind must not wrap %v", kind)
		}
	}
	if sdk.IsExpected(authmodel.ErrNoDecisionKind) {
		t.Fatalf("ErrNoDecisionKind must not be an expected sdk kind")
	}
	// It is a distinct identity: it does NOT wrap the relationship-kind sentinel,
	// so a host branching on ErrRelationshipsNotConfigured cannot silently catch it.
	if errors.Is(authmodel.ErrNoDecisionKind, relationships.ErrRelationshipsNotConfigured) {
		t.Fatalf("ErrNoDecisionKind must be a clean identity, not a wrap of ErrRelationshipsNotConfigured")
	}

	rec := httptest.NewRecorder()
	authorizationhttp.RespondError(rec, fmt.Errorf("Check: %w", authmodel.ErrNoDecisionKind))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("a wiring fault must never surface as a deny (403)")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ErrNoDecisionKind maps to %d, want %d — an unclassified server-side fault", rec.Code, http.StatusInternalServerError)
	}
	// ReasonFor does not own it: a wiring fault carries no decision reason and the
	// caller treats it as infrastructure.
	if reason, ok := mutations.ReasonFor(authmodel.ErrNoDecisionKind); ok {
		t.Fatalf("ReasonFor(ErrNoDecisionKind) = (%q, true), want ok=false", reason)
	}
	// The actor-observable precondition refusal keeps its 400: the two "not
	// configured" sentinels answer DIFFERENT statuses by design.
	mutationResponse := httptest.NewRecorder()
	authorizationhttp.RespondError(mutationResponse, mutations.ErrMutationsNotConfigured)
	if mutationResponse.Code != http.StatusBadRequest {
		t.Fatalf("ErrMutationsNotConfigured maps to %d, want %d", mutationResponse.Code, http.StatusBadRequest)
	}
	if rec.Code == mutationResponse.Code {
		t.Fatalf("the wiring fault (%d) and the actor-observable precondition (%d) must not share a status",
			rec.Code, mutationResponse.Code)
	}
}
