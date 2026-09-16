package decisions

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func TestExactLookupChargesTheSameExpressionFramesAsCheck(t *testing.T) {
	fixed := authmodel.Resource{Type: "organization", ID: "one"}
	for _, tc := range []struct {
		name  string
		expr  Expression
		steps int
	}{
		{"global", Role("admin"), 2},
		{"fixed", RoleIn("admin", fixed), 2},
		{"ordered short circuit", Any(Role("admin"), Role("later"), Role("still_later")), 3},
		{"named", Permission("global"), 4},
		{"conjunction", All(Role("admin"), Role("employee")), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, steps := range []int{tc.steps - 1, tc.steps} {
				_, facts := expressionService(t, exact(tuples.Global(), "admin"), exact(tuples.Global(), "employee"), exact(tuples.On(fixed.Type, fixed.ID), "admin"))
				policy := permissionModel(tc.expr)
				policy.ResourceTypes["doc"].Permissions["global"] = Role("admin")
				s, err := NewService(facts, WithModel(policy), WithLimits(authmodel.EvaluationLimits{MaxEvaluationSteps: steps}))
				if err != nil {
					t.Fatal(err)
				}
				checked, checkErr := s.Check(t.Context(), authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "one"}})
				for _, pageSize := range []int{0, 1} {
					var result authmodel.LookupResult
					if pageSize == 0 {
						result, err = s.LookupResources(t.Context(), expressionPrincipal, "view", "doc")
					} else {
						result, err = s.LookupResourcesPage(t.Context(), expressionPrincipal, "view", "doc", "", pageSize)
					}
					if steps < tc.steps {
						if !errors.Is(checkErr, authmodel.ErrEvaluationLimit) || !errors.Is(err, authmodel.ErrEvaluationLimit) || checked.Allowed || result.Unrestricted || len(result.IDs) != 0 {
							t.Fatalf("steps=%d page=%d check=%+v/%v lookup=%+v/%v", steps, pageSize, checked, checkErr, result, err)
						}
					} else if checkErr != nil || err != nil || !checked.Allowed || !result.Unrestricted {
						t.Fatalf("boundary steps=%d page=%d check=%+v/%v lookup=%+v/%v", steps, pageSize, checked, checkErr, result, err)
					}
				}
			}
		})
	}
}

func recursiveRoleLookupModel(grant Expression) Model {
	return Model{ResourceTypes: map[string]ResourceTypeDef{"node": {
		Relations:   map[string]RelationDef{"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "node"}}}},
		Permissions: map[string]Expression{"view": Any(grant, Through("parent", "view"))},
	}}}
}

func TestRecursiveLookupCandidatesShareOneBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits authmodel.EvaluationLimits
		fail   bool
	}{
		{"steps across candidates", authmodel.EvaluationLimits{MaxEvaluationSteps: 15}, true},
		{"states across candidates", authmodel.EvaluationLimits{MaxGraphStates: 2}, true},
		{"complete discovery", authmodel.EvaluationLimits{MaxEvaluationSteps: 19, MaxGraphStates: 3}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, facts := expressionService(t)
			for _, id := range []string{"a", "b", "c"} {
				facts.facts[exact(tuples.On("node", id), "viewer")] = true
			}
			s, err := NewService(facts, WithModel(recursiveRoleLookupModel(RoleIn("viewer"))), WithLimits(tc.limits))
			if err != nil {
				t.Fatal(err)
			}
			// Each root fits independently. Enumeration must still bound their
			// combined discovery work instead of renewing the ledger three times.
			for _, id := range []string{"a", "b", "c"} {
				got, err := s.Check(t.Context(), authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "node", ID: id}})
				if err != nil || !got.Allowed {
					t.Fatalf("individual root %q: %+v/%v", id, got, err)
				}
			}
			result, err := s.LookupResources(t.Context(), expressionPrincipal, "view", "node")
			if tc.fail {
				if !errors.Is(err, authmodel.ErrEvaluationLimit) || result.Unrestricted || len(result.IDs) != 0 {
					t.Fatalf("partial or renewed budget: %+v/%v", result, err)
				}
			} else if err != nil || !reflect.DeepEqual(result.IDs, []string{"a", "b", "c"}) {
				t.Fatalf("complete candidate discovery: %+v/%v", result, err)
			}
		})
	}
}

func TestRecursiveUniversalLookupChargesExpressionBudget(t *testing.T) {
	for _, steps := range []int{2, 3} {
		_, facts := expressionService(t, exact(tuples.Global(), "admin"))
		s, err := NewService(facts, WithModel(recursiveRoleLookupModel(Role("admin"))), WithLimits(authmodel.EvaluationLimits{MaxEvaluationSteps: steps}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.LookupResources(t.Context(), expressionPrincipal, "view", "node")
		if steps == 2 {
			if !errors.Is(err, authmodel.ErrEvaluationLimit) || result.Unrestricted || facts.containsCalls != 0 {
				t.Fatalf("universal branch bypassed budget: %+v/%v calls=%d", result, err, facts.containsCalls)
			}
		} else if err != nil || !result.Unrestricted || facts.containsCalls != 1 {
			t.Fatalf("universal boundary: %+v/%v calls=%d", result, err, facts.containsCalls)
		}
	}
}

func TestExactLookupNamedDAGDoesNotFlattenBeforeBudget(t *testing.T) {
	_, facts := expressionService(t)
	permissions := map[string]Expression{"p0": Role("missing")}
	for i := 1; i <= 12; i++ {
		previous := fmt.Sprintf("p%d", i-1)
		permissions[fmt.Sprintf("p%d", i)] = Any(Permission(previous), Permission(previous))
	}
	s, err := NewService(facts, WithModel(Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Permissions: permissions}}}), WithLimits(authmodel.EvaluationLimits{MaxEvaluationSteps: 20}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.LookupResourcesPage(t.Context(), expressionPrincipal, "p12", "doc", "", 1)
	if !errors.Is(err, authmodel.ErrEvaluationLimit) || result.Unrestricted || len(result.IDs) != 0 || facts.containsCalls != 0 {
		t.Fatalf("DAG evaluation escaped its budget: %+v/%v calls=%d", result, err, facts.containsCalls)
	}
}

func TestThroughUniversalLookupUsesRemainingDiscoveryBudget(t *testing.T) {
	policy := Model{ResourceTypes: map[string]ResourceTypeDef{
		"doc": {
			Relations:   map[string]RelationDef{"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "folder"}}}},
			Permissions: map[string]Expression{"view": Through("parent", "view")},
		},
		"folder": {Permissions: map[string]Expression{"view": Role("admin")}},
	}}
	for _, steps := range []int{6, 7} {
		_, facts := expressionService(t, exact(tuples.Global(), "admin"))
		for _, id := range []string{"a", "b"} {
			facts.facts[tuples.Tuple{Scope: tuples.On("doc", id), Relation: "parent", Subject: tuples.SubjectRef{Type: "folder", ID: "shared"}}] = true
		}
		s, err := NewService(facts, WithModel(policy), WithLimits(authmodel.EvaluationLimits{MaxEvaluationSteps: steps}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.LookupResources(t.Context(), expressionPrincipal, "view", "doc")
		if steps == 6 {
			if !errors.Is(err, authmodel.ErrEvaluationLimit) || result.Unrestricted || len(result.IDs) != 0 {
				t.Fatalf("renewed discovery budget: %+v/%v", result, err)
			}
		} else if err != nil || !reflect.DeepEqual(result.IDs, []string{"a", "b"}) {
			t.Fatalf("discovery boundary: %+v/%v", result, err)
		}
	}
}
