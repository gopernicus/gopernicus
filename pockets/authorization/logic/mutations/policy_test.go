package mutations

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func TestDefaultIntegrityPolicyForCoversOnlyTypesDeclaringOwner(t *testing.T) {
	user := decisions.RelationDef{AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}
	model := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"platform": {Relations: map[string]decisions.RelationDef{"admin": user, "developer": user}},
		"project":  {Relations: map[string]decisions.RelationDef{"owner": user, "viewer": user}},
		"report":   {Permissions: map[string]decisions.Expression{"owner": decisions.Role("owner")}},
	}}
	got := DefaultIntegrityPolicyFor(model)
	want := IntegrityPolicy{Rules: []IntegrityRule{{ResourceType: "project", Relation: "owner", MinSubjects: 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultIntegrityPolicyFor = %+v; want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"report", "document"} {
		if got.Protects(rt, "owner") {
			t.Fatalf("%s owner covered; permissions-only and opaque types are documented drops", rt)
		}
		if !DefaultIntegrityPolicy().Protects(rt, "owner") {
			t.Fatalf("unscoped default does not cover %s", rt)
		}
	}

	scope := tuples.Scope{Kind: tuples.ResourceScope, Type: "platform", ID: "global"}
	facts := []tuples.Tuple{{Scope: scope, Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}}
	if err := got.ValidateState(scope, facts); err != nil {
		t.Fatalf("model-derived policy refuses a type without owner: %v", err)
	}
	if err := DefaultIntegrityPolicy().ValidateState(scope, facts); !errors.Is(err, ErrInvariantBlocked) {
		t.Fatalf("unscoped default on a type without owner = %v; want ErrInvariantBlocked", err)
	}
}

func TestDefaultIntegrityPolicyForEmptyModel(t *testing.T) {
	if got := DefaultIntegrityPolicyFor(decisions.Model{}); len(got.Rules) != 0 {
		t.Fatalf("empty model policy = %+v; want no rules", got)
	}
}
