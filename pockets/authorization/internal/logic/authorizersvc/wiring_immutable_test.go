package authorizersvc

import (
	"context"
	"sync"
	"testing"
)

// TestNewServiceDoesNotRetainCallerSchema proves the engine compiles the schema
// into a private immutable artifact and keeps no reference to the caller's maps:
// mutating every layer of the source after construction changes neither the
// digest nor the snapshot the Service serves.
func TestNewServiceDoesNotRetainCallerSchema(t *testing.T) {
	schema := orgProjectSchema()
	svc, err := NewService(&fakeStore{}, schema, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	digest := svc.SchemaDigest()

	schema.ResourceTypes["injected"] = ResourceTypeDef{
		Relations:   map[string]RelationDef{"x": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
		Permissions: map[string]PermissionRule{"y": AnyOf(Direct("x"))},
	}
	schema.ResourceTypes["org"].Relations["admin"].AllowedSubjects[0].Type = "hacked"

	if svc.SchemaDigest() != digest {
		t.Fatalf("digest changed after source mutation: %q -> %q", digest, svc.SchemaDigest())
	}
	snap := svc.GetSchema()
	if contains(snap.ResourceTypes(), "injected") {
		t.Fatalf("Service leaked an injected resource type: %v", snap.ResourceTypes())
	}
	if subs := snap.AllowedSubjects("org", "admin"); len(subs) != 1 || subs[0].Type != "user" {
		t.Fatalf("Service absorbed an in-place subject mutation: %v", subs)
	}
}

// TestServiceSchemaImmutableUnderConcurrentSourceMutation runs a writer mutating
// the SOURCE schema while readers drive the Service's decision and projection
// surface. Because construction deep-copied the schema, the two touch disjoint
// memory: the decision, digest, and snapshot are stable and the -race detector
// stays silent.
func TestServiceSchemaImmutableUnderConcurrentSourceMutation(t *testing.T) {
	schema := orgProjectSchema()
	svc, err := NewService(&fakeStore{}, schema, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	digest := svc.SchemaDigest()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			schema.ResourceTypes["org"].Relations["admin"].AllowedSubjects[0].Type = "x"
			delete(schema.ResourceTypes, "project")
			schema.ResourceTypes["project"] = ResourceTypeDef{
				Relations:   map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
				Permissions: map[string]PermissionRule{"view": AnyOf(Direct("member"))},
			}
		}
	}()

	ctx := context.Background()
	req := CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "project", ID: "p1"}}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if svc.SchemaDigest() != digest {
					t.Errorf("digest changed under concurrent source mutation")
					return
				}
				_ = svc.GetSchema().ResourceTypes()
				_ = svc.GetPermissionsForRelation("project", "member")
				if _, err := svc.Check(ctx, req); err != nil {
					t.Errorf("Check: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}
