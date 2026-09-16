package authorization_test

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// Matching facade inputs denote the same value. This deliberately checks the
// conversion contract, not the still-separate persistence implementations.
func TestCanonicalTupleConversions(t *testing.T) {
	assignment := roles.Assignment{
		SubjectType: "group", SubjectID: " Équipe:one/#. ", Role: "editor", Scope: tuples.On("project", " p:1/# "),
	}
	relationship := relationships.CreateRelationship{
		ResourceType: assignment.Scope.Type, ResourceID: assignment.Scope.ID,
		Relation: assignment.Role, SubjectType: assignment.SubjectType, SubjectID: assignment.SubjectID,
	}
	fact := assignment.Tuple()
	if fact != relationship.Tuple() {
		t.Fatalf("same fact differs by facade: role=%+v relationship=%+v", fact, relationship.Tuple())
	}
	if err := fact.Validate(); err != nil {
		t.Fatal(err)
	}
	if fact.Subject.IsUserset() || fact.Subject.ID != assignment.SubjectID || fact.Scope.ID != assignment.Scope.ID {
		t.Fatalf("opaque references were reinterpreted: %+v", fact)
	}

	relationship.SubjectRelation = "member"
	userset := relationship.Tuple()
	if userset == fact || !userset.Subject.IsUserset() || userset.Subject.Relation != "member" {
		t.Fatalf("userset identity lost in conversion: %+v", userset)
	}
	if err := userset.Validate(); err != nil {
		t.Fatal(err)
	}

	assignment.Scope = tuples.Global()
	global := assignment.Tuple()
	if global.Scope != tuples.Global() || global == fact || global.Subject != fact.Subject {
		t.Fatalf("global scope conversion changed identity: %+v", global)
	}
	if err := assignment.Validate(); err != nil {
		t.Fatalf("explicit conversion of a global assignment: %v", err)
	}
}

func TestCanonicalTupleConversionsDoNotRepairInvalidScope(t *testing.T) {
	for _, resource := range []struct{ typ, id string }{{"", "p1"}, {"project", ""}} {
		assignment := roles.Assignment{
			SubjectType: "user", SubjectID: "alice", Role: "editor", Scope: tuples.On(resource.typ, resource.id),
		}
		if assignment.Tuple().Scope == tuples.Global() {
			t.Fatalf("partial scope became global: %+v", assignment)
		}
		if err := assignment.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("partial role scope accepted: %+v, %v", assignment, err)
		}
	}
	// Empty resource coordinates are invalid for relationship inputs, even
	// though the old role assignment shape uses an empty pair for global scope.
	relationship := relationships.CreateRelationship{Relation: "editor", SubjectType: "user", SubjectID: "alice"}
	if relationship.Tuple().Scope == tuples.Global() {
		t.Fatal("missing relationship resource became global")
	}
	if err := relationship.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing relationship resource accepted: %v", err)
	}
}
