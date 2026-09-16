package storetest

import (
	"context"
	"fmt"

	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// rolesWalkAssignments is how many scoped assignments the page-walk principal
// holds. LookupAllResourceIDs walks ListBySubject at list.MaxLimit per page, so a
// count above 2·MaxLimit forces the cursor to be followed TWICE — pinning each
// dialect's cursor behaviour past the FIRST page boundary, where a cursor
// derived from a cursor-fetched page is the failure mode — while staying small
// enough for a live remote dialect run.
const rolesWalkAssignments = 2*list.MaxLimit + 5

// rolePolicyModel is the roles-decision fixture. One resource type with two
// permissions of DIFFERENT grantor sets is the whole point: a globally held
// viewer is unrestricted for view and holds nothing on audit, so "a global role
// is data, not a bypass" is provable on every dialect.
func rolePolicyModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"project": {Permissions: map[string]decisions.Expression{
		"audit": decisions.RoleIn("auditor"),
		"view":  decisions.Any(decisions.RoleIn("auditor"), decisions.RoleIn("viewer"), decisions.Role("viewer")),
	}}}}
}

// composedSchema is a graph branch in the unified permission policy.
func composedSchema() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "project", Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view": decisions.AnyOf(decisions.Direct("viewer")),
			},
		}},
	})
}

// newDecisionService constructs the public surface with generous oracle limits.
func newDecisionService(t *testing.T, repos Repositories, opts ...authorization.Option) authorization.Components {
	t.Helper()
	opts = append(opts, authorization.WithLimits(generousLimits()))
	comps, err := authorization.New(repos.Repositories, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// grantRole seeds through guarded mutation when that capability is supplied.
func grantRole(t *testing.T, repos Repositories, mutator *mutations.SystemMutator, subjectType, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	if repos.Mutations == nil {
		assign(t, repos.Tuples, subjectType, subjectID, roleName, resourceType, resourceID)
		return
	}
	result, err := mutator.AssignRole(context.Background(), mutations.AssignRoleCommand{
		Subject: authmodel.PrincipalRef{Type: subjectType, ID: subjectID},
		Role:    roleName, Scope: fixtureScope(resourceType,
			resourceID),
	})
	if err != nil {
		t.Fatalf("AssignRole(%s on %s/%s): %v", roleName, resourceType, resourceID, err)
	}
	if result == nil || result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("AssignRole(%s on %s/%s) result = %+v, want applied", roleName, resourceType, resourceID, result)
	}
}

// projectCheck runs one decision on the fixture's project type.
func projectCheck(t *testing.T, svc authorization.Components, subjectID, permission, projectID string) authmodel.CheckResult {
	t.Helper()
	res, err := svc.Decisions.Check(context.Background(), authmodel.CheckRequest{
		Principal:  authmodel.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authmodel.Resource{Type: "project", ID: projectID},
	})
	if err != nil {
		t.Fatalf("Check(%s, %s, project:%s): %v", subjectID, permission, projectID, err)
	}
	return res
}

// projectLookup enumerates the fixture's project type and asserts the standing
// non-nil IDs contract on every backend.
func projectLookup(t *testing.T, svc authorization.Components, subjectID, permission string) decisions.ResourceSet {
	t.Helper()
	res, err := svc.Decisions.LookupAllResourceIDs(context.Background(), authmodel.PrincipalRef{Type: "user", ID: subjectID}, permission, "project")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs(%s, %s): %v", subjectID, permission, err)
	}
	if res.IDs == nil {
		t.Fatalf("LookupAllResourceIDs(%s, %s) IDs must be non-nil", subjectID, permission)
	}
	return res
}

// runRolesDecision exercises exact leaves in the unified decision surface.
func runRolesDecision(t *testing.T, newRepos func(t *testing.T) Repositories) {
	t.Run("DirectGrantAllows", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p1")

		if res := projectCheck(t, comps, "u1", "audit", "p1"); !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
			t.Fatalf("direct grant: %+v, want allowed with reason role:auditor@direct", res)
		}
		// The grant is scoped: another project of the same type is denied.
		if res := projectCheck(t, comps, "u1", "audit", "p2"); res.Allowed || res.ReasonCode != authmodel.ReasonDenied {
			t.Fatalf("unscoped project: %+v, want denied with reason no matching role", res)
		}
		look := projectLookup(t, comps, "u1", "audit")
		if look.Unrestricted || !idsEqual(look.IDs, []string{"p1"}) {
			t.Fatalf("scoped lookup = %+v, want IDs [p1] and not unrestricted", look)
		}
	})

	t.Run("ExplicitGlobalBranchSatisfiesScopedPermission", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "viewer", "", "")

		for _, projectID := range []string{"p1", "p2"} {
			res := projectCheck(t, comps, "u1", "view", projectID)
			if !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
				t.Fatalf("global grant on project:%s: %+v, want allowed with reason role:viewer@global", projectID, res)
			}
		}
		look := projectLookup(t, comps, "u1", "view")
		if !look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("globally granted lookup = %+v, want unrestricted with empty IDs", look)
		}
	})

	t.Run("UndeclaredPairDenies", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p1")

		// No expression declares delete, so it cannot grant access.
		if res := projectCheck(t, comps, "u1", "delete", "p1"); res.Allowed || res.Reason != "no rules defined" {
			t.Fatalf("undeclared pair: %+v, want denied with reason no rules defined", res)
		}
		if look := projectLookup(t, comps, "u1", "delete"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("undeclared pair lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
	})

	t.Run("GlobalRoleIsUnrestrictedOnlyForItsDeclaredPairs", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		// viewer is held GLOBALLY; the model lists it on view and NOT on audit.
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "viewer", "", "")

		if look := projectLookup(t, comps, "u1", "view"); !look.Unrestricted {
			t.Fatalf("view lookup = %+v, want unrestricted (viewer grants view)", look)
		}
		if res := projectCheck(t, comps, "u1", "audit", "p1"); res.Allowed {
			t.Fatalf("audit: %+v, want denied — a global role grants only the pairs naming it", res)
		}
		if look := projectLookup(t, comps, "u1", "audit"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("audit lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
		// A principal holding nothing at all is empty, never unrestricted.
		if look := projectLookup(t, comps, "nobody", "view"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("nobody's lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
	})
}

// runRolesParity is the roles arm of the bidirectional Check/Lookup oracle: for
// each principal × declared (type, permission) every Check-allow is discoverable
// by LookupAllResourceIDs and every looked-up ID passes Check — the same invariant the
// relationship arm proves, now across a MULTI-PAGE ListBySubject walk so each
// dialect's cursor behaviour is pinned.
func runRolesParity(t *testing.T, newRepos func(t *testing.T) Repositories) {
	ctx := context.Background()
	universe := []string{"p1", "p2", "p3", "p_absent"}

	t.Run("RolesCheckLookupOracle", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		svc := comps

		grantRole(t, repos, comps.SystemMutator, "user", "u_auditor", "auditor", "project", "p1")
		grantRole(t, repos, comps.SystemMutator, "user", "u_auditor", "auditor", "project", "p2")
		grantRole(t, repos, comps.SystemMutator, "user", "u_viewer", "viewer", "project", "p1")
		grantRole(t, repos, comps.SystemMutator, "user", "u_global", "viewer", "", "")

		// Scoped principals: the plain bidirectional oracle over the finite universe.
		for _, principal := range []authmodel.PrincipalRef{
			{Type: "user", ID: "u_auditor"},
			{Type: "user", ID: "u_viewer"},
			{Type: "user", ID: "u_none"},
		} {
			for _, permission := range []string{"audit", "view"} {
				assertCheckLookupParity(t, ctx, svc, principal, permission, "project", universe)
			}
		}

		// The globally granted principal is the ONE shape enumeration cannot list:
		// Check allows every resource of the type and LookupAllResourceIDs says so with
		// Unrestricted rather than a list — while the pair the role does not grant
		// stays an ordinary, empty, bidirectional parity case.
		global := authmodel.PrincipalRef{Type: "user", ID: "u_global"}
		for _, id := range universe {
			if res := projectCheck(t, svc, "u_global", "view", id); !res.Allowed {
				t.Fatalf("global viewer on project:%s: %+v, want allowed", id, res)
			}
		}
		if look := projectLookup(t, svc, "u_global", "view"); !look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("global viewer lookup = %+v, want unrestricted with empty IDs", look)
		}
		assertCheckLookupParity(t, ctx, svc, global, "audit", "project", universe)
	})

	t.Run("RolesMultiPageWalk", func(t *testing.T) {
		repos := newRepos(t)
		comps := newDecisionService(t, repos, authorization.WithModel(rolePolicyModel()))
		svc := comps

		// Seed through the raw role port: this case covers enumeration over many
		// modeled assignments. Other cases exercise the guarded mutation seam.
		walked := make([]string, 0, rolesWalkAssignments)
		for i := 0; i < rolesWalkAssignments; i++ {
			id := walkProjectID(i)
			walked = append(walked, id)
			assign(t, repos.Tuples, "user", "u_walk", "auditor", "project", id)
		}
		// A non-granting assignment inside the same walk: viewer does not grant
		// audit, so the walk must filter by role, not merely by resource type.
		assign(t, repos.Tuples, "user", "u_walk", "viewer", "project", "p_view_only")
		// A granting role on ANOTHER resource type must not leak into this type.
		assign(t, repos.Tuples, "user", "u_walk", "auditor", "dataset", "ds1")

		sweep := append(append([]string(nil), walked...), "p_view_only", "p_absent")
		assertCheckLookupParity(t, ctx, svc, authmodel.PrincipalRef{Type: "user", ID: "u_walk"}, "audit", "project", sweep)

		look := projectLookup(t, svc, "u_walk", "audit")
		if look.Unrestricted || len(look.IDs) != rolesWalkAssignments {
			t.Fatalf("multi-page walk returned %d ids (unrestricted=%v), want %d", len(look.IDs), look.Unrestricted, rolesWalkAssignments)
		}
		if res := projectCheck(t, svc, "u_walk", "audit", "p_view_only"); res.Allowed {
			t.Fatalf("viewer must not grant audit: %+v", res)
		}
	})

	t.Run("RolesPagedParity", func(t *testing.T) {
		runRolesPagedParity(t, newRepos)
	})
}

// walkProjectID names the walk fixture's resources with a FIXED-WIDTH suffix so
// the lexical order the enumeration sorts by is stable and readable regardless of
// the dialect's own row order.
func walkProjectID(i int) string {
	return fmt.Sprintf("pw-%03d", i)
}

// runComposed proves exact and graph policy leaves share one fact authority.
func runComposed(t *testing.T, newRepos func(*testing.T) Repositories) {
	t.Run("SharedFactsAndExpressionAlgebra", func(t *testing.T) {
		repos := newRepos(t)
		model := composedSchema()
		rt := model.ResourceTypes["project"]
		rt.Permissions["audit"] = decisions.RoleIn("auditor")
		rt.Permissions["both"] = decisions.All(decisions.Direct("viewer"), decisions.RoleIn("auditor"))
		rt.Permissions["either"] = decisions.Any(decisions.Direct("viewer"), decisions.RoleIn("auditor"))
		model.ResourceTypes["project"] = rt
		comps := newDecisionService(t, repos, authorization.WithModel(model))
		assign(t, repos.Tuples, "user", "u", "viewer", "project", "p1")
		mustCreate(t, repos.Relationships, ct("project", "p1", "auditor", "user", "u"))
		for _, permission := range []string{"view", "audit", "both", "either"} {
			if result := projectCheck(t, comps, "u", permission, "p1"); !result.Allowed {
				t.Fatalf("%s denied shared facts: %+v", permission, result)
			}
		}
		if err := unassignRole(t.Context(), repos.Tuples, "user", "u", "viewer", "project", "p1"); err != nil {
			t.Fatal(err)
		}
		if projectCheck(t, comps, "u", "view", "p1").Allowed || projectCheck(t, comps, "u", "both", "p1").Allowed {
			t.Fatal("revoked shared viewer remains")
		}
		if !projectCheck(t, comps, "u", "either", "p1").Allowed {
			t.Fatal("independent auditor removed")
		}
	})
}
