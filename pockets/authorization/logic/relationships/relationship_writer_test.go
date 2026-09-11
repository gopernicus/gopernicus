package relationships_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"

	"github.com/gopernicus/gopernicus/pockets"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func baselineModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "doc", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
		}},
		{Name: "space", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]relationships.PermissionRule{"in_parent": relationships.AnyOf(relationships.Direct("parent"))},
		}},
	})
}

func newBaseline(t *testing.T) (authorization.Components, *memory.Relationships) {
	t.Helper()
	rels := memory.NewRelationships()
	comps, err := authorization.New(authorization.Repositories{Relationships: rels}, authorization.WithRelationshipModel(baselineModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.RelationshipWriter == nil {
		t.Fatal("relationship wiring must return a RelationshipWriter")
	}
	return comps, rels
}

func targets(t *testing.T, svc authorization.Components, resource authmodel.Resource, relationName string) []relationships.SubjectRef {
	t.Helper()
	got, err := svc.Relationships.GetRelationTargets(context.Background(), resource.Type, resource.ID, relationName)
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	return got
}

// TestBaselineWriterWithoutMutationRepository proves ordinary relationship state
// does not depend on the v3 mutation repository or any command identity.
func TestBaselineWriterWithoutMutationRepository(t *testing.T) {
	comps, _ := newBaseline(t)
	if err := comps.RelationshipWriter.CreateRelationships(context.Background(), []relationships.CreateRelationship{{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1",
	}}); err != nil {
		t.Fatalf("CreateRelationships with Mutations=nil: %v", err)
	}
	if got := targets(t, comps, authmodel.Resource{Type: "doc", ID: "d1"}, "viewer"); len(got) != 1 || got[0].ID != "u1" {
		t.Fatalf("created state not visible: %+v", got)
	}
	if _, err := comps.SystemMutator.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{}); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("advanced path should remain unwired, got %v", err)
	}
}

func TestBaselineInvalidDesiredStateDoesNotChangeState(t *testing.T) {
	comps, _ := newBaseline(t)
	resource := authmodel.Resource{Type: "doc", ID: "d1"}
	if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), resource, "viewer", []relationships.SubjectRef{{Type: "user", ID: "u1"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), resource, "viewer", []relationships.SubjectRef{{Type: "service_account", ID: "s1"}}); err == nil {
		t.Fatal("schema-invalid target must fail")
	}
	if got := targets(t, comps, resource, "viewer"); len(got) != 1 || got[0].ID != "u1" {
		t.Fatalf("invalid operation changed state: %+v", got)
	}
}

func TestSetRelationTargetsConvergesAcrossStateHistory(t *testing.T) {
	comps, rels := newBaseline(t)
	ctx := context.Background()
	resource := authmodel.Resource{Type: "doc", ID: "d1"}
	a := []relationships.SubjectRef{{Type: "user", ID: "a"}}
	b := []relationships.SubjectRef{{Type: "user", ID: "b"}}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("set A: %v", err)
	}
	first, err := rels.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, list.Request{})
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("list first: %+v err=%v", first, err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("repeat A: %v", err)
	}
	repeated, err := rels.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, list.Request{})
	if err != nil || len(repeated.Items) != 1 || repeated.Items[0] != first.Items[0] {
		t.Fatalf("repeating A changed tuple state: first=%+v repeated=%+v err=%v", first.Items, repeated.Items, err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", b); err != nil {
		t.Fatalf("set B: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", a); err != nil {
		t.Fatalf("restore A: %v", err)
	}
	if got := targets(t, comps, resource, "viewer"); len(got) != 1 || got[0] != a[0] {
		t.Fatalf("A -> B -> A did not restore A: %+v", got)
	}
}

func TestSetRelationTargetsParentReplacementAndClear(t *testing.T) {
	comps, _ := newBaseline(t)
	ctx := context.Background()
	child := authmodel.Resource{Type: "space", ID: "child"}
	oldParent := relationships.SubjectRef{Type: "space", ID: "old"}
	newParent := relationships.SubjectRef{Type: "space", ID: "new"}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", []relationships.SubjectRef{oldParent}); err != nil {
		t.Fatalf("old parent: %v", err)
	}
	if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", []relationships.SubjectRef{newParent}); err != nil {
		t.Fatalf("new parent: %v", err)
	}
	if got := targets(t, comps, child, "parent"); len(got) != 1 || got[0] != newParent {
		t.Fatalf("parent replacement left stale/extra targets: %+v", got)
	}
	for i := 0; i < 2; i++ {
		if err := comps.RelationshipWriter.SetRelationTargets(ctx, child, "parent", nil); err != nil {
			t.Fatalf("clear %d: %v", i+1, err)
		}
	}
	if got := targets(t, comps, child, "parent"); len(got) != 0 {
		t.Fatalf("clear did not converge to empty: %+v", got)
	}
}

func TestConcurrentParentDesiredStatesNeverAccumulate(t *testing.T) {
	comps, _ := newBaseline(t)
	child := authmodel.Resource{Type: "space", ID: "child"}
	parents := []relationships.SubjectRef{{Type: "space", ID: "a"}, {Type: "space", ID: "b"}}
	var wg sync.WaitGroup
	for _, parent := range parents {
		parent := parent
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := comps.RelationshipWriter.SetRelationTargets(context.Background(), child, "parent", []relationships.SubjectRef{parent}); err != nil {
				t.Errorf("SetRelationTargets(%s): %v", parent.ID, err)
			}
		}()
	}
	wg.Wait()
	if got := targets(t, comps, child, "parent"); len(got) != 1 {
		t.Fatalf("concurrent desired states accumulated targets: %+v", got)
	}
}

type countingRouter struct{ calls int }

func (r *countingRouter) Handle(string, string, http.HandlerFunc, ...web.Middleware) { r.calls++ }

func TestBaselineWriterIsNotExposedThroughServiceOrHTTP(t *testing.T) {
	comps, _ := newBaseline(t)
	writerType := reflect.TypeOf(&relationships.RelationshipWriter{})
	serviceType := reflect.TypeOf(comps.Relationships)
	for i := 0; i < serviceType.NumMethod(); i++ {
		method := serviceType.Method(i)
		for j := 0; j < method.Type.NumOut(); j++ {
			if method.Type.Out(j) == writerType {
				t.Fatalf("Service.%s returns the trusted RelationshipWriter", method.Name)
			}
		}
	}
	router := &countingRouter{}
	if err := comps.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if router.calls != 0 {
		t.Fatalf("authorization registered %d HTTP routes; want none", router.calls)
	}
}
