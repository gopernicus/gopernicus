package authorizersvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// TestReasonCodeStableGrantedDenied proves CheckResult carries the stable coarse
// ReasonCode: ReasonGranted on allow, ReasonDenied on deny — independent of the
// free-text debug Reason.
func TestReasonCodeStableGrantedDenied(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples, relationship.CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	})

	grant, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !grant.Allowed || grant.ReasonCode != ReasonGranted {
		t.Fatalf("granted decision: allowed=%v code=%q err=%v", grant.Allowed, grant.ReasonCode, err)
	}

	deny, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u2"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || deny.Allowed || deny.ReasonCode != ReasonDenied {
		t.Fatalf("denied decision: allowed=%v code=%q err=%v", deny.Allowed, deny.ReasonCode, err)
	}

	// A permission with no rules on the type is still a stable deny.
	none, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "nonexistent", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || none.Allowed || none.ReasonCode != ReasonDenied {
		t.Fatalf("no-rules decision: allowed=%v code=%q err=%v", none.Allowed, none.ReasonCode, err)
	}
}

// TestExplainCannotChangeDecision proves CheckExplain returns the SAME decision
// (Allowed and ReasonCode) as Check across direct-grant, through-grant, and deny
// fixtures — the explain path is not a separate, more permissive evaluator.
func TestExplainCannotChangeDecision(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u2"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)

	cases := []struct {
		name string
		req  CheckRequest
	}{
		{"direct grant", CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"}}},
		{"through grant", CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u2"}, Permission: "view", Resource: Resource{Type: "post", ID: "p2"}}},
		{"deny", CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u3"}, Permission: "view", Resource: Resource{Type: "post", ID: "p2"}}},
	}
	for _, tc := range cases {
		plain, perr := svc.Check(context.Background(), tc.req)
		expl, expExpl, eerr := svc.CheckExplain(context.Background(), tc.req)
		if perr != nil || eerr != nil {
			t.Fatalf("%s: Check err=%v CheckExplain err=%v", tc.name, perr, eerr)
		}
		if plain.Allowed != expl.Allowed || plain.ReasonCode != expl.ReasonCode {
			t.Fatalf("%s: explain changed decision: plain(%v,%q) explain(%v,%q)",
				tc.name, plain.Allowed, plain.ReasonCode, expl.Allowed, expl.ReasonCode)
		}
		if expExpl.Decision != expl.ReasonCode {
			t.Fatalf("%s: Explanation.Decision %q != result code %q", tc.name, expExpl.Decision, expl.ReasonCode)
		}
	}
}

// TestExplainTraceRecordsCoarseSteps proves a granted through decision produces
// a bounded trace of coarse rule/path steps with a stable Outcome and no error
// text, including the nested direct step and the parent Through step.
func TestExplainTraceRecordsCoarseSteps(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)

	res, expl, err := svc.CheckExplain(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("through grant: allowed=%v err=%v", res.Allowed, err)
	}
	if expl.Decision != ReasonGranted {
		t.Fatalf("Explanation.Decision = %q, want granted", expl.Decision)
	}
	if len(expl.Steps) == 0 {
		t.Fatalf("expected recorded steps, got none")
	}
	var sawThroughGrant, sawDirectGrant bool
	for _, st := range expl.Steps {
		if st.Outcome != ReasonGranted && st.Outcome != ReasonDenied {
			t.Fatalf("step Outcome %q is not a coarse decision code", st.Outcome)
		}
		if st.Kind == ExplainKindThrough && st.Relation == "org" && st.Outcome == ReasonGranted {
			sawThroughGrant = true
		}
		if st.Kind == ExplainKindDirect && st.Relation == "admin" && st.Outcome == ReasonGranted {
			sawDirectGrant = true
		}
	}
	if !sawThroughGrant || !sawDirectGrant {
		t.Fatalf("trace missing expected steps: through-grant=%v direct-grant=%v steps=%+v", sawThroughGrant, sawDirectGrant, expl.Steps)
	}
}

// TestExplainFailsWithSameLimitClass proves that when evaluation exhausts the
// shared budget, Check and CheckExplain BOTH return ErrEvaluationLimit — the
// explain request shares the budget and cannot evade the limit.
func TestExplainFailsWithSameLimitClass(t *testing.T) {
	store := &fakeStore{}
	// A tight graph-state budget: the top state is charged, then the Through hop
	// into org exhausts it.
	svc, err := NewService(store, testSchema(), Config{Limits: EvaluationLimits{MaxGraphStates: 1}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	req := CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}}

	_, perr := svc.Check(context.Background(), req)
	_, _, eerr := svc.CheckExplain(context.Background(), req)
	if !errors.Is(perr, ErrEvaluationLimit) {
		t.Fatalf("Check: want ErrEvaluationLimit, got %v", perr)
	}
	if !errors.Is(eerr, ErrEvaluationLimit) {
		t.Fatalf("CheckExplain: want ErrEvaluationLimit, got %v", eerr)
	}
}
