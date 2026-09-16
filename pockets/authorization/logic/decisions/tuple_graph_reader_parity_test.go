package decisions_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

type portableTuples struct{ tuples.Storer }
type portableReader struct{ tuples.Reader }

func (p portableTuples) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	return p.Storer.ReadTupleSnapshot(ctx, func(ctx context.Context, reader tuples.Reader) error {
		return fn(ctx, portableReader{reader})
	})
}

func TestPortableGraphMatchesOptimizedFilteringAndBudgets(t *testing.T) {
	policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"doc": {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Direct("viewer")}},
	}}
	fact := func(id, kind, who string) tuples.Tuple {
		return tuples.Tuple{Scope: tuples.On("doc", id), Relation: "viewer", Subject: tuples.SubjectRef{Type: kind, ID: who}}
	}
	for _, tc := range []struct {
		name      string
		max       int
		extra     []tuples.Tuple
		exhausted bool
	}{
		{name: "wide_direct_is_not_navigation_fanout", max: 2},
		{name: "seed_counts_toward_expansion", max: 1, exhausted: true},
		{name: "entire_reachable_closure_counts", max: 2, extra: []tuples.Tuple{fact("other", "user", "alice")}, exhausted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.NewTuples()
			facts := []tuples.Tuple{fact("d", "user", "alice"), fact("d", "user", "bob"), fact("d", "user", "carl"), fact("d", "service", "one"), fact("d", "service", "two")}
			facts = append(facts, tc.extra...)
			global := fact("d", "user", "alice")
			global.Scope = tuples.Global()
			facts = append(facts, global)
			if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
				t.Fatal(err)
			}
			for name, reader := range map[string]tuples.Storer{"optimized": store, "portable": portableTuples{store}} {
				t.Run(name, func(t *testing.T) {
					service, err := decisions.NewService(reader, decisions.WithModel(policy), decisions.WithLimits(authmodel.EvaluationLimits{MaxRelationTargets: 1, MaxGraphStates: tc.max}))
					if err != nil {
						t.Fatal(err)
					}
					result, err := service.Check(t.Context(), authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Resource: authmodel.Resource{Type: "doc", ID: "d"}, Permission: "view"})
					if tc.exhausted {
						if !errors.Is(err, authmodel.ErrEvaluationLimit) || result.Allowed {
							t.Fatalf("expected limit, got %+v/%v", result, err)
						}
					} else if err != nil || !result.Allowed {
						t.Fatalf("expected grant, got %+v/%v", result, err)
					}
				})
			}
		})
	}
}

func TestPortableThroughFiltersBeforeFanout(t *testing.T) {
	store := memory.NewTuples()
	policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"folder": {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Direct("viewer")}},
		"doc":    {Relations: map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "folder"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Through("parent", "view")}},
	}}
	facts := []tuples.Tuple{
		{Scope: tuples.On("folder", "f"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "u"}},
		{Scope: tuples.On("doc", "d"), Relation: "parent", Subject: tuples.SubjectRef{Type: "folder", ID: "f"}},
	}
	for _, id := range []string{"old1", "old2", "old3"} {
		facts = append(facts, tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "parent", Subject: tuples.SubjectRef{Type: "retired", ID: id}})
	}
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	for name, reader := range map[string]tuples.Storer{"optimized": store, "portable": portableTuples{store}} {
		t.Run(name, func(t *testing.T) {
			service, err := decisions.NewService(reader, decisions.WithModel(policy), decisions.WithLimits(authmodel.EvaluationLimits{MaxRelationTargets: 1}))
			if err != nil {
				t.Fatal(err)
			}
			principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
			result, explanation, err := service.CheckExplain(t.Context(), authmodel.CheckRequest{Principal: principal, Resource: authmodel.Resource{Type: "doc", ID: "d"}, Permission: "view"})
			if err != nil || !result.Allowed {
				t.Fatalf("model-filtered Through: %+v/%v", result, err)
			}
			if len(explanation.Steps) != 2 {
				t.Fatalf("missing direct/Through trace: %+v", explanation)
			}
			for _, step := range explanation.Steps {
				if err := step.Scope.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := json.Marshal(explanation); err != nil {
				t.Fatalf("unencodable canonical explanation: %v", err)
			}
			found, err := service.LookupAllResourceIDs(t.Context(), principal, "view", "doc")
			if err != nil || found.Unrestricted || len(found.IDs) != 1 || found.IDs[0] != "d" {
				t.Fatalf("lookup parity: %+v/%v", found, err)
			}
		})
	}
}

func TestBoundUnknownPermissionHonorsCancellation(t *testing.T) {
	store := memory.NewTuples()
	service, err := decisions.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := service.EvaluateWith(ctx, store, authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "u"}, Resource: authmodel.Resource{Type: "doc", ID: "d"}, Permission: "unknown"})
	if !errors.Is(err, context.Canceled) || result != (authmodel.CheckResult{}) {
		t.Fatalf("canceled bound check: %+v/%v", result, err)
	}
}
