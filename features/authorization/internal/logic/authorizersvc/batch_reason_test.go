package authorizersvc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// docReadSchema: doc.read = AnyOf(Direct("editor"), Direct("owner")). Both direct
// checks, so CheckBatch takes the optimized batch path. The compiled check order
// is sorted (editor before owner), so a resource granted ONLY via owner exposes
// the old batch bug that always named checks[0]'s relation.
func docReadSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "doc", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("editor"), Direct("owner")),
			},
		}},
	})
}

// TestCheckBatchNamesActualGrantingRelation proves the optimized batch path
// reports the relation that ACTUALLY granted each item, not the first check's
// relation. d1 is granted only via owner (not editor); the debug Reason must name
// owner and the ReasonCode must be the stable ReasonGranted.
func TestCheckBatchNamesActualGrantingRelation(t *testing.T) {
	store := &fakeStore{}
	svc, err := NewService(store, docReadSchema(), Config{IDs: cryptids.IDGenerator{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "doc", ResourceID: "d1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	)

	results, err := svc.CheckBatch(context.Background(), []CheckRequest{
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "read", Resource: Resource{Type: "doc", ID: "d1"}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "read", Resource: Resource{Type: "doc", ID: "d2"}},
	})
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	if !results[0].Allowed || results[0].ReasonCode != ReasonGranted {
		t.Fatalf("d1: allowed=%v code=%q, want granted", results[0].Allowed, results[0].ReasonCode)
	}
	if results[0].Reason != "direct:owner" {
		t.Fatalf("d1: Reason = %q, want direct:owner (the relation that actually granted)", results[0].Reason)
	}
	if results[1].Allowed || results[1].ReasonCode != ReasonDenied {
		t.Fatalf("d2: allowed=%v code=%q, want denied", results[1].Allowed, results[1].ReasonCode)
	}
}

// TestValidateSchemaErrorsDeterministicOrder proves ValidateSchema aggregates its
// errors in a stable, sorted order across repeated runs, so map iteration cannot
// reorder the reported list.
func TestValidateSchemaErrorsDeterministicOrder(t *testing.T) {
	// Two resource types, each with a direct relation that is not defined — the
	// passes range mutable maps, so an unsorted aggregation would vary by run.
	bad := NewSchema([]ResourceSchema{
		{Name: "alpha", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"a": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"read": AnyOf(Direct("missing_a"))},
		}},
		{Name: "beta", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"b": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"read": AnyOf(Direct("missing_b"))},
		}},
	})

	var first []string
	for i := 0; i < 25; i++ {
		var ve *SchemaValidationError
		if !errors.As(ValidateSchema(bad), &ve) {
			t.Fatalf("want *SchemaValidationError")
		}
		if first == nil {
			first = ve.Errors
			continue
		}
		if !reflect.DeepEqual(first, ve.Errors) {
			t.Fatalf("ValidateSchema error order not deterministic:\n first=%v\n run%d=%v", first, i, ve.Errors)
		}
	}
	if len(first) < 2 {
		t.Fatalf("expected at least two aggregated errors, got %v", first)
	}
}
