package authorizationhttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRefuseSelfTarget(t *testing.T) {
	actor := sdk.Principal{Type: "user", ID: "u1"}
	for _, tc := range []struct {
		name    string
		op      mutations.Operation
		subject authmodel.PrincipalRef
		refused bool
	}{
		{"self assign", mutations.OpRoleAssign, authmodel.PrincipalRef{Type: "user", ID: "u1"}, true},
		{"self unassign passes", mutations.OpRoleUnassign, authmodel.PrincipalRef{Type: "user", ID: "u1"}, false},
		{"other subject", mutations.OpRoleAssign, authmodel.PrincipalRef{Type: "user", ID: "u2"}, false},
		{"same id other type", mutations.OpRoleAssign, authmodel.PrincipalRef{Type: "service", ID: "u1"}, false},
		{"group the actor may belong to", mutations.OpRoleAssign, authmodel.PrincipalRef{Type: "group", ID: "g1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RefuseSelfTarget(t.Context(), RoleWriteRequest{Principal: actor, Operation: tc.op, Subject: tc.subject, Role: "admin", Scope: tuples.Global()})
			if tc.refused != (err != nil) {
				t.Fatalf("err=%v want refused=%v", err, tc.refused)
			}
			if tc.refused && (!errors.Is(err, ErrSelfTargetRefused) || !errors.Is(err, sdk.ErrForbidden)) {
				t.Fatalf("refusal kind: %v", err)
			}
		})
	}
}

func TestAllOf(t *testing.T) {
	var order []string
	named := func(name string, err error) RoleWritePolicy {
		return func(context.Context, RoleWriteRequest) error { order = append(order, name); return err }
	}
	denied := errors.New("denied")
	if err := AllOf(named("a", nil), named("b", nil))(t.Context(), RoleWriteRequest{}); err != nil || strings.Join(order, ",") != "a,b" {
		t.Fatalf("admit: %v order=%v", err, order)
	}
	order = nil
	if err := AllOf(named("a", nil), named("b", denied), named("c", nil))(t.Context(), RoleWriteRequest{}); !errors.Is(err, denied) || strings.Join(order, ",") != "a,b" {
		t.Fatalf("first refusal: %v order=%v", err, order)
	}
	for name, build := range map[string]func(){
		"zero policies": func() { AllOf() },
		"nil entry":     func() { AllOf(RefuseSelfTarget, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			build()
		})
	}
}

// TestSelfTargetRefusalRidesTheRefusedRail proves the helper's refusal uses the
// existing WARN and OutcomeRefused path and never reaches the writer.
func TestSelfTargetRefusalRidesTheRefusedRail(t *testing.T) {
	stub := &stubRoleAdmin{receipt: testReceipt()}
	logger, logs := auditLogger()
	var events []decisions.DecisionEvent
	a, err := New(Services{Roles: stub, Mutations: stub},
		WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: AllOf(RefuseSelfTarget, allowWrite)}),
		WithDecisionObserver(func(_ context.Context, ev decisions.DecisionEvent) { events = append(events, ev) }),
		WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, a.AssignRole(), "POST", pathAssignRole, `{"subject_type":"user","subject_id":"admin-1","role":"admin","scope":{"kind":"global"}}`)
	if rec.Code != 403 || stub.assignActor.ID != "" {
		t.Fatalf("status=%d writer actor=%+v", rec.Code, stub.assignActor)
	}
	if !strings.Contains(logs.String(), "role write refused") || !strings.Contains(logs.String(), "error_kind=forbidden") {
		t.Fatalf("missing WARN: %s", logs.String())
	}
	if len(events) != 1 || events[0].Outcome != decisions.OutcomeRefused || events[0].ErrorKind != "forbidden" {
		t.Fatalf("events=%+v", events)
	}
	rec = doJSON(t, a.UnassignRole(), "POST", pathUnassignRole, `{"subject_type":"user","subject_id":"admin-1","role":"admin","scope":{"kind":"global"}}`)
	if rec.Code != 200 || stub.unassignActor.ID != "admin-1" {
		t.Fatalf("self unassign: status=%d actor=%+v", rec.Code, stub.unassignActor)
	}
}
