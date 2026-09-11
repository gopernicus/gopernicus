package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// budgetSchema is the layer-(b) traversal schema for the bounded-evaluation
// parity cases: a self-referential folder hierarchy (depth + fan-out), and a doc
// whose view is reachable through TWO sibling relations to the same group#view
// target (the sibling-Through lookup case). It is separate from fixtureSchema so
// the adversarial userset cases are not perturbed.
func budgetSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "folder", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("parent", "view")),
			},
		}},
		{Name: "group", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer")),
			},
		}},
		{Name: "doc", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"primary":   {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group"}}},
				"secondary": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Through("primary", "view"), relationships.Through("secondary", "view")),
			},
		}},
	})
}

func newBudgetService(t *testing.T, repos authorization.Repositories, limits authmodel.EvaluationLimits) authorization.Components {
	t.Helper()
	comps, err := authorization.New(repos, authorization.WithRelationshipModel(budgetSchema()), authorization.WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// runBudget is layer (b): the bounded-evaluation dimensions that must produce the
// SAME allow/deny/error class on every store dialect. Exhaustion is always
// ErrEvaluationLimit — never a deny, never a truncated list.
func runBudget(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("DepthBoundaryParity", func(t *testing.T) {
		// f2 <- f1 <- f0 (grant on f0): Check(f2) needs exactly 2 Through hops.
		tuples := []relationships.CreateRelationship{
			{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
			{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f0"},
			{ResourceType: "folder", ResourceID: "f0", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		}
		check := func(svc authorization.Components) (authmodel.CheckResult, error) {
			return svc.Decisions.Check(ctx, authmodel.CheckRequest{
				Principal:  authmodel.PrincipalRef{Type: "user", ID: "u1"},
				Permission: "view",
				Resource:   authmodel.Resource{Type: "folder", ID: "f2"},
			})
		}

		repos := newRepos(t)
		ok := newBudgetService(t, repos, authmodel.EvaluationLimits{MaxThroughDepth: 2})
		mustSeed(t, repos, tuples)
		res, err := check(ok)
		if err != nil || !res.Allowed {
			t.Fatalf("MaxThroughDepth=2: want allowed at boundary, got allowed=%v err=%v", res.Allowed, err)
		}

		repos = newRepos(t)
		tight := newBudgetService(t, repos, authmodel.EvaluationLimits{MaxThroughDepth: 1})
		mustSeed(t, repos, tuples)
		if _, err := check(tight); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("MaxThroughDepth=1: want ErrEvaluationLimit, got %v", err)
		}
	})

	t.Run("FanoutParity", func(t *testing.T) {
		tuples := []relationships.CreateRelationship{
			{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p1"},
			{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p2"},
			{ResourceType: "folder", ResourceID: "f", Relation: "parent", SubjectType: "folder", SubjectID: "p3"},
		}
		repos := newRepos(t)
		tight := newBudgetService(t, repos, authmodel.EvaluationLimits{MaxRelationTargets: 2})
		mustSeed(t, repos, tuples)
		if _, err := tight.Decisions.Check(ctx, authmodel.CheckRequest{
			Principal:  authmodel.PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   authmodel.Resource{Type: "folder", ID: "f"},
		}); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("3 targets over MaxRelationTargets=2: want ErrEvaluationLimit, got %v", err)
		}
	})

	t.Run("LookupResultCapParity", func(t *testing.T) {
		// 3 accessible docs enumerated via a Through (so the store LIMIT cap on
		// LookupResourceIDsByRelationTarget is exercised, not just direct lookup).
		tuples := []relationships.CreateRelationship{
			{ResourceType: "group", ResourceID: "g", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "doc", ResourceID: "d1", Relation: "primary", SubjectType: "group", SubjectID: "g"},
			{ResourceType: "doc", ResourceID: "d2", Relation: "primary", SubjectType: "group", SubjectID: "g"},
			{ResourceType: "doc", ResourceID: "d3", Relation: "primary", SubjectType: "group", SubjectID: "g"},
		}
		repos := newRepos(t)
		tight := newBudgetService(t, repos, authmodel.EvaluationLimits{MaxLookupResults: 2})
		mustSeed(t, repos, tuples)
		if _, err := tight.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("3 docs over MaxLookupResults=2: want ErrEvaluationLimit, got %v", err)
		}

		repos = newRepos(t)
		ok := newBudgetService(t, repos, authmodel.EvaluationLimits{MaxLookupResults: 3})
		mustSeed(t, repos, tuples)
		res, err := ok.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
		if err != nil {
			t.Fatalf("LookupAllResourceIDs within budget: %v", err)
		}
		if len(res.IDs) != 3 {
			t.Fatalf("want 3 complete IDs within budget, got %v", res.IDs)
		}
	})

	t.Run("SiblingThroughLookupParity", func(t *testing.T) {
		// Two sibling Through relations to the same group#view target: both must
		// enumerate (the memo reuse fix, not the old shared-visited suppression).
		tuples := []relationships.CreateRelationship{
			{ResourceType: "group", ResourceID: "gp", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "group", ResourceID: "gs", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "doc", ResourceID: "dp", Relation: "primary", SubjectType: "group", SubjectID: "gp"},
			{ResourceType: "doc", ResourceID: "ds", Relation: "secondary", SubjectType: "group", SubjectID: "gs"},
		}
		repos := newRepos(t)
		svc := newBudgetService(t, repos, authmodel.EvaluationLimits{})
		mustSeed(t, repos, tuples)
		res, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "doc")
		if err != nil {
			t.Fatalf("LookupAllResourceIDs: %v", err)
		}
		if want := []string{"dp", "ds"}; !idsEqual(res.IDs, want) {
			t.Fatalf("sibling Through suppression: want %v, got %v", want, res.IDs)
		}
	})

	t.Run("GroupExpansionOverflowIsIndeterminate", func(t *testing.T) {
		// A 5-deep userset membership CHAIN whose distinct reachable states
		// (seed + g1..g5#member + the doc grant state = 7) exceed a deliberately
		// small maxExpansionStates. The bounded CHECK-path expansion must report
		// overflow (relationship.ErrExpansionBudgetExceeded) — NOT allow, NOT deny.
		// This is the F4 DoS bound proved equivalent across all three backends.
		chain := []relationships.CreateRelationship{
			{ResourceType: "group", ResourceID: "g1", Relation: "member", SubjectType: "user", SubjectID: "u1"},
			{ResourceType: "group", ResourceID: "g2", Relation: "member", SubjectType: "group", SubjectID: "g1", SubjectRelation: "member"},
			{ResourceType: "group", ResourceID: "g3", Relation: "member", SubjectType: "group", SubjectID: "g2", SubjectRelation: "member"},
			{ResourceType: "group", ResourceID: "g4", Relation: "member", SubjectType: "group", SubjectID: "g3", SubjectRelation: "member"},
			{ResourceType: "group", ResourceID: "g5", Relation: "member", SubjectType: "group", SubjectID: "g4", SubjectRelation: "member"},
			{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "group", SubjectID: "g5", SubjectRelation: "member"},
		}

		t.Run("Overflow", func(t *testing.T) {
			s := newRepos(t).Relationships
			mustSeed2(t, s, chain)
			// maxExpansionStates=3 < 7 reachable states → overflow.
			if _, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "u1", 3); !errors.Is(err, relationships.ErrExpansionBudgetExceeded) {
				t.Fatalf("check over budget: want ErrExpansionBudgetExceeded, got %v", err)
			}
			// CheckBatchDirect shares the same bounded expansion.
			if _, err := s.CheckBatchDirect(ctx, "doc", []string{"d1"}, "viewer", "user", "u1", 3); !errors.Is(err, relationships.ErrExpansionBudgetExceeded) {
				t.Fatalf("batch over budget: want ErrExpansionBudgetExceeded, got %v", err)
			}
			// Optional set reads share the same expansion bound. Their absence
			// does not skip the required check and batch assertions above.
			if sets, ok := s.(relationships.RelationSetReader); ok {
				if _, err := sets.FilterRelation(ctx, "doc", []string{"d1", "d2"}, "viewer", "user", "u1", 3); !errors.Is(err, relationships.ErrExpansionBudgetExceeded) {
					t.Fatalf("set read over budget: want ErrExpansionBudgetExceeded, got %v", err)
				}
			}
		})

		t.Run("WithinBudgetNoFalseOverflow", func(t *testing.T) {
			s := newRepos(t).Relationships
			mustSeed2(t, s, chain)
			// A budget above the reachable-state count returns the correct bool,
			// never a false overflow.
			ok, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "u1", 50)
			if err != nil {
				t.Fatalf("check within budget: unexpected err %v", err)
			}
			if !ok {
				t.Fatalf("check within budget: want allow through the userset chain, got deny")
			}
			batch, err := s.CheckBatchDirect(ctx, "doc", []string{"d1"}, "viewer", "user", "u1", 50)
			if err != nil {
				t.Fatalf("batch within budget: unexpected err %v", err)
			}
			if !batch["d1"] {
				t.Fatalf("batch within budget: want d1 allowed, got %v", batch)
			}
			if sets, ok := s.(relationships.RelationSetReader); ok {
				filtered, err := sets.FilterRelation(ctx, "doc", []string{"d1", "d2"}, "viewer", "user", "u1", 50)
				if err != nil {
					t.Fatalf("set read within budget: unexpected err %v", err)
				}
				if len(filtered) != 1 || filtered[0] != "d1" {
					t.Fatalf("set read within budget: want [d1], got %v", filtered)
				}
			}
			// A non-member within budget is a clean deny, not an error.
			deny, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "nobody", 50)
			if err != nil || deny {
				t.Fatalf("non-member within budget: want (false,nil), got (%v,%v)", deny, err)
			}
			if sets, ok := s.(relationships.RelationSetReader); ok {
				none, err := sets.FilterRelation(ctx, "doc", []string{"d1"}, "viewer", "user", "nobody", 50)
				if err != nil || len(none) != 0 {
					t.Fatalf("non-member set read within budget: want an empty result, got (%v,%v)", none, err)
				}
			}
		})
	})
}

// mustSeed2 seeds valid tuples through the relationship.Storer PORT directly
// (the raw fixture-seeding path). It differs from mustSeed only in taking the
// Storer rather than the full Repositories.
func mustSeed2(t *testing.T, s relationships.Storer, tuples []relationships.CreateRelationship) {
	t.Helper()
	if err := s.CreateRelationships(context.Background(), tuples); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
}

// mustSeed seeds valid tuples through the relationship.Storer PORT (the sanctioned
// fixture-seeding path — the raw write path was removed from Service at AZ3-3.4). The
// port skips schema validation, which is fine for known-valid budget fixtures.
func mustSeed(t *testing.T, repos authorization.Repositories, tuples []relationships.CreateRelationship) {
	t.Helper()
	if err := repos.Relationships.CreateRelationships(context.Background(), tuples); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
}

func idsEqual(got, want []string) bool {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}
