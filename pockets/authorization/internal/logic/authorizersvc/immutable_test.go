package authorizersvc

import (
	"sync"
	"testing"
)

func TestCompileImmutableAfterSourceMutation(t *testing.T) {
	schema := orgProjectSchema()

	c, err := Compile(schema)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	digest := c.Digest()
	before := c.Snapshot()

	// Mutate every layer of the source AFTER compilation: add a resource type,
	// add a relation, add a permission, and mutate an existing subject in place.
	schema.ResourceTypes["injected"] = ResourceTypeDef{
		Relations:   map[string]RelationDef{"x": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
		Permissions: map[string]PermissionRule{"y": AnyOf(Direct("x"))},
	}
	schema.ResourceTypes["org"].Relations["injected"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}
	schema.ResourceTypes["org"].Permissions["injected"] = AnyOf(Direct("admin"))
	subs := schema.ResourceTypes["org"].Relations["admin"].AllowedSubjects
	subs[0].Type = "hacked"

	if c.Digest() != digest {
		t.Fatalf("digest changed after source mutation: %q -> %q", digest, c.Digest())
	}

	after := c.Snapshot()
	if got := after.ResourceTypes(); contains(got, "injected") {
		t.Fatalf("compiled schema leaked an injected resource type: %v", got)
	}
	if got := after.Relations("org"); contains(got, "injected") {
		t.Fatalf("compiled schema leaked an injected relation: %v", got)
	}
	if got := after.Permissions("org"); contains(got, "injected") {
		t.Fatalf("compiled schema leaked an injected permission: %v", got)
	}
	if subs := after.AllowedSubjects("org", "admin"); len(subs) != 1 || subs[0].Type != "user" {
		t.Fatalf("compiled schema absorbed an in-place subject mutation: %v", subs)
	}

	// The snapshot taken before mutation is identical to one taken after: both
	// project the same immutable compiled schema.
	if !sliceEqual(before.ResourceTypes(), after.ResourceTypes()) {
		t.Fatalf("snapshot changed across mutation: %v vs %v", before.ResourceTypes(), after.ResourceTypes())
	}
}

func TestSchemaSnapshotAccessorsReturnCopies(t *testing.T) {
	c, err := Compile(orgProjectSchema())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	snap := c.Snapshot()

	subs := snap.AllowedSubjects("project", "org")
	if len(subs) != 1 {
		t.Fatalf("want 1 subject, got %v", subs)
	}
	subs[0].Type = "tampered"

	if again := snap.AllowedSubjects("project", "org"); again[0].Type != "org" {
		t.Fatalf("snapshot accessor returned an aliased slice: %v", again)
	}

	checks := snap.Checks("project", "view")
	if len(checks) == 0 {
		t.Fatal("want checks for project.view")
	}
	checks[0] = PermissionCheck{Relation: "tampered"}
	if again := snap.Checks("project", "view"); again[0].Relation == "tampered" {
		t.Fatalf("snapshot accessor returned an aliased check slice: %v", again)
	}
}

func TestCompileConcurrentReadsDoNotRaceEngine(t *testing.T) {
	schema := orgProjectSchema()
	c, err := Compile(schema)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	digest := c.Digest()

	var wg sync.WaitGroup

	// A writer mutates the SOURCE schema concurrently. Because the compiled
	// schema deep-copied everything, this touches disjoint memory and cannot
	// race the readers below.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			schema.ResourceTypes["org"].Relations["admin"].AllowedSubjects[0].Type = "x"
			delete(schema.ResourceTypes, "project")
			schema.ResourceTypes["project"] = ResourceTypeDef{
				Relations:   map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
				Permissions: map[string]PermissionRule{"view": AnyOf(Direct("member"))},
			}
		}
	}()

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if c.Digest() != digest {
					t.Errorf("digest changed under concurrent reads")
					return
				}
				snap := c.Snapshot()
				_ = snap.ResourceTypes()
				_ = snap.AllowedSubjects("project", "org")
				_ = snap.Checks("project", "view")
				if _, ok := snap.RelationIsNavigational("project", "org"); !ok {
					t.Errorf("expected project.org relation in snapshot")
					return
				}
			}
		}()
	}

	wg.Wait()
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
