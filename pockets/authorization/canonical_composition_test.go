package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestBoundRoleWriterEnforcesCanonicalShapeConstraints(t *testing.T) {
	store := memory.New()
	policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"doc": {Relations: map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}}}}
	components, err := New(Repositories{Tuples: store.Tuples()}, WithModel(policy))
	if err != nil {
		t.Fatal(err)
	}
	a := roles.Assignment{SubjectType: "machine", SubjectID: "m", Role: "owner", Scope: tuples.On("doc", "d")}
	if err := components.RoleWriter.AssignRole(t.Context(), a); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("role facade bypassed declared shape: %v", err)
	}
	if held, err := store.Tuples().Contains(t.Context(), a.Tuple()); err != nil || held {
		t.Fatalf("invalid fact written: %v %v", held, err)
	}
	a.Role = "opaque"
	if err := components.RoleWriter.AssignRole(t.Context(), a); err != nil {
		t.Fatalf("permission catalog gated opaque label: %v", err)
	}
}

func TestRootDiagnosticOptionIsOptInAndPreservesScopedDeny(t *testing.T) {
	store := memory.New()
	p := model.PrincipalRef{Type: "user", ID: "u"}
	if err := store.Tuples().ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: p.Type, ID: p.ID}}}}); err != nil {
		t.Fatal(err)
	}
	policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"doc": {Permissions: map[string]decisions.Expression{"manage": decisions.RoleIn("admin")}}}}
	var events []decisions.DiagnosticCode
	components, err := New(Repositories{Tuples: store.Tuples()}, WithModel(policy), WithDiagnosticObserver(func(_ context.Context, c decisions.DiagnosticCode) { events = append(events, c) }))
	if err != nil {
		t.Fatal(err)
	}
	result, err := components.Decisions.Check(t.Context(), model.CheckRequest{Principal: p, Permission: "manage", Resource: model.Resource{Type: "doc", ID: "d"}})
	if err != nil || result.Allowed || len(events) != 1 || events[0] != decisions.DiagnosticGlobalGrantNotApplied {
		t.Fatalf("result=%+v events=%v err=%v", result, events, err)
	}
}
