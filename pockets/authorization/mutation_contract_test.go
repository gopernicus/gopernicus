package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestIntegrityPolicyValidatesShapeWithoutRequiringModelCatalog(t *testing.T) {

	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "opaque", Relation: "custodian", MinSubjects: 1}}}))
	comps, err := New(Repositories{Tuples: st.Tuples(), Mutations: st.Mutations()}, WithModel(lifecycleModel()))
	if err != nil {
		t.Fatal(err)
	}
	cmd := mutations.AssignRoleCommand{Subject: prinU("u1"), Role: "custodian", Scope: tuples.On("opaque", "one")}
	if _, err := comps.Mutations.AssignRole(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if _, err := comps.Mutations.UnassignRole(context.Background(), mutations.UnassignRoleCommand(cmd)); !errors.Is(err, mutations.ErrInvariantBlocked) {
		t.Fatalf("opaque integrity bypass: %v", err)
	}
}

func TestEmptyIntegrityIsDefaultAndSupportsReaderOnlyModel(t *testing.T) {
	st := memory.New()
	model := decisions.NewSchema([]decisions.ResourceSchema{{Name: "doc", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"reader": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}}}})
	comps, err := New(Repositories{Mutations: st.Mutations(), Tuples: st.Tuples()}, WithModel(model))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comps.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "reader", Subject: subjU("u1")}); err != nil {
		t.Fatalf("default policy imposed an undeclared owner: %v", err)
	}
}
