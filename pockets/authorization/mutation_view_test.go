package authorization

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// tenancyModel is the segovia v2 shape that motivated CheckPermission: a
// dashboard's manage is inherited from its space, a space's from its parent
// space or its tenant, and a tenant's admins may be a group userset. The actor
// who legitimately manages a dashboard often holds NO direct tuple on it.
func tenancyModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{
			Name: "group",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
			},
		},
		{
			Name: "tenant",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
					"admin": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"manage": relationships.AnyOf(relationships.Direct("owner"), relationships.Direct("admin")),
				},
			},
		},
		{
			Name: "space",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"tenant":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "tenant"}}},
					"parent":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
					"manager": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"manage": relationships.AnyOf(relationships.Direct("manager"), relationships.Through("parent", "manage"), relationships.Through("tenant", "manage")),
				},
			},
		},
		{
			Name: "dashboard",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"space":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
					"owner":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
					"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"manage": relationships.AnyOf(relationships.Direct("owner"), relationships.Through("space", "manage")),
				},
			},
		},
	})
}

type permissionGuard struct {
	permission string
	scope      *authmodel.Resource // nil = the attempt's own scope

	checkErr error
	allowed  bool
}

func (g *permissionGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	scope := authmodel.Resource{Type: attempt.Target.Type, ID: attempt.Target.ID}
	if g.scope != nil {
		scope = *g.scope
	}
	result, err := view.Check(ctx, authmodel.CheckRequest{Principal: attempt.Actor.PrincipalRef, Permission: g.permission, Resource: authmodel.Resource{Type: scope.Type, ID: scope.ID}})
	ok := result.Allowed
	g.checkErr = err
	g.allowed = ok
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("guard: %s on %s denied: %w", g.permission, fmt.Sprint(scope), sdk.ErrForbidden)
	}
	return nil
}

func seedTenancy(t *testing.T, st *memory.Store) {
	t.Helper()
	rows := []relationships.CreateRelationship{
		{ResourceType: "tenant", ResourceID: "t1", Relation: "owner", SubjectType: "user", SubjectID: "tenant-owner"},
		{ResourceType: "group", ResourceID: "admins", Relation: "member", SubjectType: "user", SubjectID: "group-admin"},
		{ResourceType: "tenant", ResourceID: "t1", Relation: "admin", SubjectType: "group", SubjectID: "admins", SubjectRelation: "member"},
		{ResourceType: "space", ResourceID: "root", Relation: "tenant", SubjectType: "tenant", SubjectID: "t1"},
		{ResourceType: "space", ResourceID: "child", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "child", Relation: "manager", SubjectType: "user", SubjectID: "space-manager"},
		{ResourceType: "dashboard", ResourceID: "d1", Relation: "space", SubjectType: "space", SubjectID: "child"},
		{ResourceType: "dashboard", ResourceID: "d1", Relation: "owner", SubjectType: "user", SubjectID: "dash-owner"},
	}
	if err := st.Relationships().CreateRelationships(context.Background(), rows); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func newTenancyHost(t *testing.T, guard mutations.MutationGuard, opts ...Option) (Components, *memory.Store) {
	t.Helper()
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	seedTenancy(t, st)
	opts = append(opts, WithGuard(guard))
	comps, err := New(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps, st
}

func actor(id string) mutations.Actor {
	return mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: id}}
}

func grantViewer(t *testing.T, svc Components, who string) (*mutations.Result, error) {
	t.Helper()
	return svc.Mutations.GrantRelationship(context.Background(), actor(who), mutations.GrantRelationshipCommand{
		ResourceType: "dashboard", ResourceID: "d1", Relation: "viewer", Subject: subjU("reader"),
	})
}

func assertNoViewerWritten(t *testing.T, st *memory.Store) {
	t.Helper()
	targets, err := st.Relationships().GetRelationTargets(context.Background(), "dashboard", "d1", "viewer")
	if err != nil || len(targets) != 0 {
		t.Fatalf("a refused mutation must write nothing: targets=%+v err=%v", targets, err)
	}
}

func TestCheckPermissionInheritedThroughHierarchy(t *testing.T) {
	cases := []struct{ name, who string }{
		{"direct owner", "dash-owner"},
		{"space manager one hop", "space-manager"},
		{"tenant owner three hops", "tenant-owner"},
		{"group admin via userset through container", "group-admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := &permissionGuard{permission: "manage"}
			svc, _ := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()))
			rcpt, err := grantViewer(t, svc, tc.who)
			if err != nil {
				t.Fatalf("GrantRelationship: %v (check err %v)", err, guard.checkErr)
			}
			if rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied {
				t.Fatalf("want applied receipt, got %+v", rcpt)
			}
			if !guard.allowed {
				t.Fatalf("guard should have observed allow")
			}
		})
	}
}

func TestCheckPermissionDeniesWithoutAuthorityAndWritesNothing(t *testing.T) {
	guard := &permissionGuard{permission: "manage"}
	svc, st := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()))
	_, err := grantViewer(t, svc, "stranger")
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if guard.checkErr != nil || guard.allowed {
		t.Fatalf("want a clean deny: err=%v allowed=%v", guard.checkErr, guard.allowed)
	}
	assertNoViewerWritten(t, st)
}

func TestCheckPermissionRoleOwnershipDeniesWithoutAssignments(t *testing.T) {
	roles := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"dashboard": {Roles: []string{"publisher"}, Permissions: map[string][]string{"publish": {"publisher"}}},
	}}
	t.Run("mixed model", func(t *testing.T) {
		guard := &permissionGuard{permission: "publish"}
		svc, st := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()), WithRoleModel(roles))
		_, err := grantViewer(t, svc, "dash-owner")
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("want denied role permission, got %v", err)
		}
		assertNoViewerWritten(t, st)
	})
	t.Run("roles only", func(t *testing.T) {
		st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
		guard := &permissionGuard{permission: "publish"}
		comps, err := New(Repositories{Roles: st.Roles(), Mutations: st.Mutations()}, WithRoleModel(roles), WithGuard(guard))
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		_, err = comps.Mutations.AssignRole(context.Background(), actor("dash-owner"), mutations.AssignRoleCommand{
			ResourceType: "dashboard", ResourceID: "d1", Role: "publisher", Subject: authmodel.PrincipalRef{Type: "user", ID: "u2"},
		})
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("roles-only deployment must deny without a granting role, got %v", err)
		}
	})
	t.Run("roles only, undeclared pair", func(t *testing.T) {
		st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
		guard := &permissionGuard{permission: "manage"}
		comps, err := New(Repositories{Roles: st.Roles(), Mutations: st.Mutations()}, WithRoleModel(roles), WithGuard(guard))
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		_, err = comps.Mutations.AssignRole(context.Background(), actor("dash-owner"), mutations.AssignRoleCommand{
			ResourceType: "dashboard", ResourceID: "d1", Role: "publisher", Subject: authmodel.PrincipalRef{Type: "user", ID: "u2"},
		})
		if !errors.Is(err, sdk.ErrForbidden) || guard.checkErr != nil || guard.allowed {
			t.Fatalf("undeclared pair must deny: err=%v, check=%v, allowed=%v", err, guard.checkErr, guard.allowed)
		}
	})
}

func TestCheckPermissionRejectsMalformedInput(t *testing.T) {
	t.Run("empty resource type", func(t *testing.T) {
		subj := authmodel.Resource{ID: "u1"}
		guard := &permissionGuard{permission: "manage", scope: &subj}
		svc, st := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()))
		_, err := grantViewer(t, svc, "dash-owner")
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want ErrInvalidRequest, got %v", err)
		}
		assertNoViewerWritten(t, st)
	})
	t.Run("empty permission", func(t *testing.T) {
		guard := &permissionGuard{permission: ""}
		svc, st := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()))
		_, err := grantViewer(t, svc, "dash-owner")
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want invalid input, got %v", err)
		}
		assertNoViewerWritten(t, st)
	})
}

func TestCheckPermissionBudgetErrorsPropagateWithoutWrites(t *testing.T) {
	cases := []struct {
		name   string
		who    string
		limits authmodel.EvaluationLimits
	}{
		// tenant-owner needs three Through hops; one is not enough.
		{"through depth", "tenant-owner", authmodel.EvaluationLimits{MaxThroughDepth: 1}},
		// dashboard, space child, space root, tenant = four states.
		{"graph states", "tenant-owner", authmodel.EvaluationLimits{MaxGraphStates: 2}},
		// group-admin's expansion has 2 states (seed + admins#member) on every
		// direct read; a bound of 1 overflows in the STORE adapter, which must
		// surface as the same evaluation-limit outcome the read side reports.
		{"expansion states in the adapter", "group-admin", authmodel.EvaluationLimits{MaxGraphStates: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := &permissionGuard{permission: "manage"}
			svc, st := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()), WithLimits(tc.limits))
			_, err := grantViewer(t, svc, tc.who)
			if !errors.Is(err, authmodel.ErrEvaluationLimit) || !errors.Is(err, sdk.ErrUnavailable) {
				t.Fatalf("want ErrEvaluationLimit (unavailable), got %v", err)
			}
			if guard.allowed {
				t.Fatalf("budget exhaustion must never allow")
			}
			assertNoViewerWritten(t, st)
		})
	}
}

// TestCheckPermissionMatchesReadSideCheck is the parity oracle at the host
// surface: for every principal the guard's inside-the-boundary answer equals the
// read-side Check the host would otherwise have consulted (detached).
func TestCheckPermissionMatchesReadSideCheck(t *testing.T) {
	for _, who := range []string{"dash-owner", "space-manager", "tenant-owner", "group-admin", "stranger", "reader"} {
		guard := &permissionGuard{permission: "manage"}
		svc, _ := newTenancyHost(t, guard, WithRelationshipModel(tenancyModel()))
		want, err := svc.Decisions.Check(context.Background(), authmodel.CheckRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: who}, Permission: "manage", Resource: authmodel.Resource{Type: "dashboard", ID: "d1"},
		})
		if err != nil {
			t.Fatalf("Check(%s): %v", who, err)
		}
		_, gerr := grantViewer(t, svc, who)
		if guard.checkErr != nil {
			t.Fatalf("CheckPermission(%s): %v", who, guard.checkErr)
		}
		if guard.allowed != want.Allowed {
			t.Fatalf("%s: read-side Check=%v, guard CheckPermission=%v", who, want.Allowed, guard.allowed)
		}
		if want.Allowed != (gerr == nil) {
			t.Fatalf("%s: mutation outcome err=%v disagrees with decision %v", who, gerr, want.Allowed)
		}
	}
}
