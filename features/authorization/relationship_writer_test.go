package authorization

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

func baselineModel() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "doc", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"))},
		}},
		{Name: "space", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]PermissionRule{"in_parent": AnyOf(Direct("parent"))},
		}},
	})
}

func newBaseline(t *testing.T) (Components, *memstore.Relationships) {
	t.Helper()
	rels := memstore.NewRelationships()
	comps, err := NewService(Repositories{Relationships: rels}, Config{Model: baselineModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.RelationshipWriter == nil {
		t.Fatal("relationship wiring must return a RelationshipWriter")
	}
	return comps, rels
}

func targets(t *testing.T, svc *Service, resource Resource, relationName string) []SubjectRef {
	t.Helper()
	got, err := svc.GetRelationTargets(context.Background(), resource.Type, resource.ID, relationName)
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	return got
}

// TestBaselineWriterWithoutMutationRepository proves ordinary relationship state
// does not depend on the v3 mutation repository or any command identity.
func TestBaselineWriterWithoutMutationRepository(t *testing.T) {
	comps, _ := newBaseline(t)
	if err := comps.RelationshipWriter.CreateRelationships(context.Background(), []CreateRelationship{{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1",
	}}); err != nil {
		t.Fatalf("CreateRelationships with Mutations=nil: %v", err)
	}
	if got := targets(t, comps.Service, Resource{Type: "doc", ID: "d1"}, "viewer"); len(got) != 1 || got[0].ID != "u1" {
		t.Fatalf("created state not visible: %+v", got)
	}
	if _, err := comps.SystemMutator.GrantRelationship(context.Background(), GrantRelationshipCommand{}); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("advanced path should remain unwired, got %v", err)
	}
}

func TestBaselineInvalidDesiredStateDoesNotChangeState(t *testing.T) {
	comps, _ := newBaseline(t)
	resource := Resource{Type: "doc", ID: "d1"}
	if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), resource, "viewer", []SubjectRef{{Type: "user", ID: "u1"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), resource, "viewer", []SubjectRef{{Type: "service_account", ID: "s1"}}); err == nil {
		t.Fatal("schema-invalid target must fail")
	}
	if got := targets(t, comps.Service, resource, "viewer"); len(got) != 1 || got[0].ID != "u1" {
		t.Fatalf("invalid operation changed state: %+v", got)
	}
}

func TestSetRelationTargetsConvergesAcrossStateHistory(t *testing.T) {
	comps, rels := newBaseline(t)
	ctx := context.Background()
	resource := Resource{Type: "doc", ID: "d1"}
	a := []SubjectRef{{Type: "user", ID: "a"}}
	b := []SubjectRef{{Type: "user", ID: "b"}}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("set A: %v", err)
	}
	first, err := rels.ListRelationshipsByResource(ctx, "doc", "d1", ResourceRelationshipFilter{}, crud.ListRequest{})
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("list first: %+v err=%v", first, err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("repeat A: %v", err)
	}
	repeated, _ := rels.ListRelationshipsByResource(ctx, "doc", "d1", ResourceRelationshipFilter{}, crud.ListRequest{})
	if len(repeated.Items) != 1 || repeated.Items[0].ID != first.Items[0].ID || !repeated.Items[0].CreatedAt.Equal(first.Items[0].CreatedAt) {
		t.Fatalf("repeating A rewrote rather than no-op: first=%+v repeated=%+v", first.Items, repeated.Items)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", b); err != nil {
		t.Fatalf("set B: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("restore A: %v", err)
	}
	if got := targets(t, comps.Service, resource, "viewer"); len(got) != 1 || got[0] != a[0] {
		t.Fatalf("A -> B -> A did not restore A: %+v", got)
	}
}

func TestSetRelationTargetsParentReplacementAndClear(t *testing.T) {
	comps, _ := newBaseline(t)
	ctx := context.Background()
	child := Resource{Type: "space", ID: "child"}
	oldParent := SubjectRef{Type: "space", ID: "old"}
	newParent := SubjectRef{Type: "space", ID: "new"}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", []SubjectRef{oldParent}); err != nil {
		t.Fatalf("old parent: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", []SubjectRef{newParent}); err != nil {
		t.Fatalf("new parent: %v", err)
	}
	if got := targets(t, comps.Service, child, "parent"); len(got) != 1 || got[0] != newParent {
		t.Fatalf("parent replacement left stale/extra targets: %+v", got)
	}
	for i := 0; i < 2; i++ {
		if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", nil); err != nil {
			t.Fatalf("clear %d: %v", i+1, err)
		}
	}
	if got := targets(t, comps.Service, child, "parent"); len(got) != 0 {
		t.Fatalf("clear did not converge to empty: %+v", got)
	}
}

func TestConcurrentParentDesiredStatesNeverAccumulate(t *testing.T) {
	comps, _ := newBaseline(t)
	child := Resource{Type: "space", ID: "child"}
	parents := []SubjectRef{{Type: "space", ID: "a"}, {Type: "space", ID: "b"}}
	var wg sync.WaitGroup
	for _, parent := range parents {
		parent := parent
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), child, "parent", []SubjectRef{parent}); err != nil {
				t.Errorf("SetRelationTargets(%s): %v", parent.ID, err)
			}
		}()
	}
	wg.Wait()
	if got := targets(t, comps.Service, child, "parent"); len(got) != 1 {
		t.Fatalf("concurrent desired states accumulated targets: %+v", got)
	}
}

type countingRouter struct{ calls int }

func (r *countingRouter) Handle(string, string, http.HandlerFunc, ...web.Middleware) { r.calls++ }

func TestBaselineWriterIsNotExposedThroughServiceOrHTTP(t *testing.T) {
	comps, _ := newBaseline(t)
	writerType := reflect.TypeOf(&RelationshipWriter{})
	serviceType := reflect.TypeOf(comps.Service)
	for i := 0; i < serviceType.NumMethod(); i++ {
		method := serviceType.Method(i)
		for j := 0; j < method.Type.NumOut(); j++ {
			if method.Type.Out(j) == writerType {
				t.Fatalf("Service.%s returns the trusted RelationshipWriter", method.Name)
			}
		}
	}
	router := &countingRouter{}
	if err := comps.Service.Register(feature.Mount{Router: router}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if router.calls != 0 {
		t.Fatalf("authorization registered %d HTTP routes; want none", router.calls)
	}
}
