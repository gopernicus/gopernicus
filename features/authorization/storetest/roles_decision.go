package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// rolesWalkAssignments is how many scoped assignments the page-walk principal
// holds. LookupResources walks ListBySubject at crud.MaxLimit per page, so a
// count above 2·MaxLimit forces the cursor to be followed TWICE — pinning each
// dialect's cursor behaviour past the FIRST page boundary, where a cursor
// derived from a cursor-fetched page is the failure mode — while staying small
// enough for a live remote dialect run.
const rolesWalkAssignments = 2*crud.MaxLimit + 5

// decisionRoleModel is the roles-decision fixture. One resource type with two
// permissions of DIFFERENT grantor sets is the whole point: a globally held
// viewer is unrestricted for view and holds nothing on audit, so "a global role
// is data, not a bypass" is provable on every dialect.
func decisionRoleModel() authorization.RoleModel {
	return authorization.RoleModel{ResourceTypes: map[string]authorization.RoleTypeDef{
		"project": {
			Roles: []string{"auditor", "viewer"},
			Permissions: map[string][]string{
				"audit": {"auditor"},
				"view":  {"auditor", "viewer"},
			},
		},
	}}
}

// composedSchema is the pair-split fixture's RELATIONSHIP half: project/view is
// relationship-owned (auth-cms's shape).
func composedSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "project", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"viewer": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"view": authorization.AnyOf(authorization.Direct("viewer")),
			},
		}},
	})
}

// composedRoleModel is the ROLE half of the SAME resource type: project/audit is
// role-owned. The two halves share the type and declare disjoint permissions —
// the only split D1 rule 4 permits, and the shape the composite dispatches on.
func composedRoleModel() authorization.RoleModel {
	return authorization.RoleModel{ResourceTypes: map[string]authorization.RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
}

// newRoleModelService builds the feature Service over the stores under test with
// a role model configured, so the decision surface is answered by the ROLES kind
// on whichever dialect is running. cfg carries the model(s); the budget is the
// oracle's generous one so no assertion is masked by exhaustion.
func newRoleModelService(t *testing.T, repos authorization.Repositories, cfg authorization.Config) authorization.Components {
	t.Helper()
	cfg.Limits = generousLimits()
	comps, err := authorization.NewService(repos, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// rolesOnly is repos with the relationship kind UNWIRED. The roles-decision legs
// assert what the roles kind decides ALONE, so they run the same roles-only host
// wiring on a both-kinds backend as on a roles-only one (Model and Relationships
// are wired together or not at all).
func rolesOnly(repos authorization.Repositories) authorization.Repositories {
	repos.Relationships = nil
	return repos
}

// grantRole seeds ONE modeled (resourceType, role) assignment through the
// high-integrity mutation path — the seam D8's assign-time model validation runs
// on — falling back to the raw role.Storer port (the runRoles precedent) when a
// backend wires the roles kind without a mutation repository, since the family
// is gated on Repositories.Roles alone.
func grantRole(t *testing.T, repos authorization.Repositories, mutator *authorization.SystemMutator, subjectType, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	if repos.Mutations == nil {
		assign(t, repos.Roles, subjectType, subjectID, roleName, resourceType, resourceID)
		return
	}
	receipt, err := mutator.AssignRole(context.Background(), authorization.AssignRoleCommand{
		MutationID:   authorization.DeriveMutationID("storetest/roles-decision", roleName, subjectType, subjectID, resourceType, resourceID),
		Subject:      authorization.PrincipalRef{Type: subjectType, ID: subjectID},
		Role:         roleName,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
	if err != nil {
		t.Fatalf("AssignRole(%s on %s/%s): %v", roleName, resourceType, resourceID, err)
	}
	if receipt == nil || receipt.Outcome != authorization.OutcomeApplied {
		t.Fatalf("AssignRole(%s on %s/%s) receipt = %+v, want applied", roleName, resourceType, resourceID, receipt)
	}
}

// projectCheck runs one decision on the fixture's project type.
func projectCheck(t *testing.T, svc *authorization.Service, subjectID, permission, projectID string) authorization.CheckResult {
	t.Helper()
	res, err := svc.Check(context.Background(), authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authorization.Resource{Type: "project", ID: projectID},
	})
	if err != nil {
		t.Fatalf("Check(%s, %s, project:%s): %v", subjectID, permission, projectID, err)
	}
	return res
}

// projectLookup enumerates the fixture's project type and asserts the standing
// non-nil IDs contract on every backend.
func projectLookup(t *testing.T, svc *authorization.Service, subjectID, permission string) authorization.LookupResult {
	t.Helper()
	res, err := svc.LookupResources(context.Background(), authorization.PrincipalRef{Type: "user", ID: subjectID}, permission, "project")
	if err != nil {
		t.Fatalf("LookupResources(%s, %s): %v", subjectID, permission, err)
	}
	if res.IDs == nil {
		t.Fatalf("LookupResources(%s, %s) IDs must be non-nil", subjectID, permission)
	}
	return res
}

// runRolesDecision is the Roles/Decision family: the roles kind answering the
// DECISION surface (layer (b)) over the store under test. It runs wherever the
// roles kind is wired, with or without the relationship kind.
func runRolesDecision(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	t.Run("DirectGrantAllows", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p1")

		if res := projectCheck(t, comps.Service, "u1", "audit", "p1"); !res.Allowed || res.Reason != "role:auditor@direct" {
			t.Fatalf("direct grant: %+v, want allowed with reason role:auditor@direct", res)
		}
		// The grant is scoped: another project of the same type is denied.
		if res := projectCheck(t, comps.Service, "u1", "audit", "p2"); res.Allowed || res.Reason != "no matching role" {
			t.Fatalf("unscoped project: %+v, want denied with reason no matching role", res)
		}
		look := projectLookup(t, comps.Service, "u1", "audit")
		if look.Unrestricted || !idsEqual(look.IDs, []string{"p1"}) {
			t.Fatalf("scoped lookup = %+v, want IDs [p1] and not unrestricted", look)
		}
	})

	t.Run("GlobalGrantSatisfiesScopedCheck", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "viewer", "", "")

		for _, projectID := range []string{"p1", "p2"} {
			res := projectCheck(t, comps.Service, "u1", "view", projectID)
			if !res.Allowed || res.Reason != "role:viewer@global" {
				t.Fatalf("global grant on project:%s: %+v, want allowed with reason role:viewer@global", projectID, res)
			}
		}
		look := projectLookup(t, comps.Service, "u1", "view")
		if !look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("globally granted lookup = %+v, want unrestricted with empty IDs", look)
		}
	})

	t.Run("UndeclaredPairDenies", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p1")

		// "delete" is declared by no model, so no role can ever grant it — the
		// relationship engine's wording, from the roles kind.
		if res := projectCheck(t, comps.Service, "u1", "delete", "p1"); res.Allowed || res.Reason != "no rules defined" {
			t.Fatalf("undeclared pair: %+v, want denied with reason no rules defined", res)
		}
		if look := projectLookup(t, comps.Service, "u1", "delete"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("undeclared pair lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
	})

	t.Run("GlobalRoleIsUnrestrictedOnlyForItsDeclaredPairs", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		// viewer is held GLOBALLY; the model lists it on view and NOT on audit.
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "viewer", "", "")

		if look := projectLookup(t, comps.Service, "u1", "view"); !look.Unrestricted {
			t.Fatalf("view lookup = %+v, want unrestricted (viewer grants view)", look)
		}
		if res := projectCheck(t, comps.Service, "u1", "audit", "p1"); res.Allowed {
			t.Fatalf("audit: %+v, want denied — a global role grants only the pairs naming it", res)
		}
		if look := projectLookup(t, comps.Service, "u1", "audit"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("audit lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
		// A principal holding nothing at all is empty, never unrestricted.
		if look := projectLookup(t, comps.Service, "nobody", "view"); look.Unrestricted || len(look.IDs) != 0 {
			t.Fatalf("nobody's lookup = %+v, want empty non-nil IDs and not unrestricted", look)
		}
	})
}

// runRolesParity is the roles arm of the bidirectional Check/Lookup oracle: for
// each principal × declared (type, permission) every Check-allow is discoverable
// by LookupResources and every looked-up ID passes Check — the same invariant the
// relationship arm proves, now across a MULTI-PAGE ListBySubject walk so each
// dialect's cursor behaviour is pinned.
func runRolesParity(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	universe := []string{"p1", "p2", "p3", "p_absent"}

	t.Run("RolesCheckLookupOracle", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		svc := comps.Service

		grantRole(t, repos, comps.SystemMutator, "user", "u_auditor", "auditor", "project", "p1")
		grantRole(t, repos, comps.SystemMutator, "user", "u_auditor", "auditor", "project", "p2")
		grantRole(t, repos, comps.SystemMutator, "user", "u_viewer", "viewer", "project", "p1")
		grantRole(t, repos, comps.SystemMutator, "user", "u_global", "viewer", "", "")

		// Scoped principals: the plain bidirectional oracle over the finite universe.
		for _, principal := range []authorization.PrincipalRef{
			{Type: "user", ID: "u_auditor"},
			{Type: "user", ID: "u_viewer"},
			{Type: "user", ID: "u_none"},
		} {
			for _, permission := range []string{"audit", "view"} {
				assertCheckLookupParity(t, ctx, svc, principal, permission, "project", universe)
			}
		}

		// The globally granted principal is the ONE shape enumeration cannot list:
		// Check allows every resource of the type and LookupResources says so with
		// Unrestricted rather than a list — while the pair the role does not grant
		// stays an ordinary, empty, bidirectional parity case.
		global := authorization.PrincipalRef{Type: "user", ID: "u_global"}
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
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.Config{RoleModel: decisionRoleModel()})
		svc := comps.Service

		// Seeded through the role.Storer PORT: this leg's subject holds more
		// assignments than two enumeration pages, and driving each row through the
		// atomic mutation path would pay a receipt + revision anchor per row on
		// every live dialect for no additional coverage (grantRole's mutation seam
		// is exercised by every other case here). Only MODELED pairs are seeded.
		walked := make([]string, 0, rolesWalkAssignments)
		for i := 0; i < rolesWalkAssignments; i++ {
			id := walkProjectID(i)
			walked = append(walked, id)
			assign(t, repos.Roles, "user", "u_walk", "auditor", "project", id)
		}
		// A non-granting assignment inside the same walk: viewer does not grant
		// audit, so the walk must filter by role, not merely by resource type.
		assign(t, repos.Roles, "user", "u_walk", "viewer", "project", "p_view_only")
		// A granting role on ANOTHER resource type must not leak into this type.
		assign(t, repos.Roles, "user", "u_walk", "auditor", "dataset", "ds1")

		sweep := append(append([]string(nil), walked...), "p_view_only", "p_absent")
		assertCheckLookupParity(t, ctx, svc, authorization.PrincipalRef{Type: "user", ID: "u_walk"}, "audit", "project", sweep)

		look := projectLookup(t, svc, "u_walk", "audit")
		if look.Unrestricted || len(look.IDs) != rolesWalkAssignments {
			t.Fatalf("multi-page walk returned %d ids (unrestricted=%v), want %d", len(look.IDs), look.Unrestricted, rolesWalkAssignments)
		}
		if res := projectCheck(t, svc, "u_walk", "audit", "p_view_only"); res.Allowed {
			t.Fatalf("viewer must not grant audit: %+v", res)
		}
	})
}

// walkProjectID names the walk fixture's resources with a FIXED-WIDTH suffix so
// the lexical order the enumeration sorts by is stable and readable regardless of
// the dialect's own row order.
func walkProjectID(i int) string {
	return fmt.Sprintf("pw-%03d", i)
}

// runComposed is the Composed family: ONE resource type split by PERMISSION
// across the two kinds (auth-cms's shape). It proves the composite dispatches
// each pair to the model that declares it on the store under test — never a
// union, never a cross-kind widening.
func runComposed(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	t.Run("PairOwnershipDispatch", func(t *testing.T) {
		repos := newRepos(t)
		comps := newRoleModelService(t, repos, authorization.Config{
			RelationshipModel: composedSchema(),
			RoleModel:         composedRoleModel(),
		})
		svc := comps.Service

		// u_tuple holds the relationship and NO role; u_role holds the role and NO
		// relationship.
		mustCreate(t, repos.Relationships, ct("project", "p1", "viewer", "user", "u_tuple"))
		grantRole(t, repos, comps.SystemMutator, "user", "u_role", "auditor", "project", "p1")

		for _, tc := range []struct {
			subject, permission string
			want                bool
		}{
			{"u_tuple", "view", true},   // relationship-owned pair, tuple held
			{"u_tuple", "audit", false}, // role-owned pair, no role
			{"u_role", "audit", true},   // role-owned pair, role held
			{"u_role", "view", false},   // relationship-owned pair, no tuple
		} {
			if res := projectCheck(t, svc, tc.subject, tc.permission, "p1"); res.Allowed != tc.want {
				t.Fatalf("Check(%s, %s, project:p1) = %v (reason %q), want %v", tc.subject, tc.permission, res.Allowed, res.Reason, tc.want)
			}
		}

		// Enumeration dispatches the same way: each pair returns ONLY its owning
		// kind's IDs.
		for _, tc := range []struct {
			subject, permission string
			want                []string
		}{
			{"u_tuple", "view", []string{"p1"}},
			{"u_tuple", "audit", nil},
			{"u_role", "audit", []string{"p1"}},
			{"u_role", "view", nil},
		} {
			look := projectLookup(t, svc, tc.subject, tc.permission)
			if look.Unrestricted || !idsEqual(look.IDs, tc.want) {
				t.Fatalf("LookupResources(%s, %s) = %+v, want IDs %v and not unrestricted", tc.subject, tc.permission, look, tc.want)
			}
		}
	})
}
