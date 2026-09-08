package authorization

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
)

// tenancyModel is the segovia v2 shape that motivated CheckPermission: a
// dashboard's manage is inherited from its space, a space's from its parent
// space or its tenant, and a tenant's admins may be a group userset. The actor
// who legitimately manages a dashboard often holds NO direct tuple on it.
func tenancyModel() Schema {
	return NewSchema([]ResourceSchema{
		{
			Name: "group",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
			},
		},
		{
			Name: "tenant",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
					"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
				Permissions: map[string]PermissionRule{
					"manage": AnyOf(Direct("owner"), Direct("admin")),
				},
			},
		},
		{
			Name: "space",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"tenant":  {AllowedSubjects: []SubjectTypeRef{{Type: "tenant"}}},
					"parent":  {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
					"manager": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]PermissionRule{
					"manage": AnyOf(Direct("manager"), Through("parent", "manage"), Through("tenant", "manage")),
				},
			},
		},
		{
			Name: "dashboard",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"space":  {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
					"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
					"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]PermissionRule{
					"manage": AnyOf(Direct("owner"), Through("space", "manage")),
				},
			},
		},
	})
}

// permissionGuard is the host guard shape segovia v2 leg 3 adopts: ask the view
// whether the actor holds `permission` on the mutated scope; deny otherwise. It
// captures the dependencies the view recorded and the error CheckPermission
// returned so tests can assert on both.
type permissionGuard struct {
	permission string
	scope      *ScopeKey // nil = the attempt's own scope
	deps       []Dependency
	checkErr   error
	allowed    bool
}

func (g *permissionGuard) AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error {
	scope := attempt.Scope
	if g.scope != nil {
		scope = *g.scope
	}
	ok, err := view.CheckPermission(ctx, scope, g.permission, attempt.Actor.Type, attempt.Actor.ID)
	g.deps = view.Dependencies()
	g.checkErr = err
	g.allowed = ok
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("guard: %s on %s denied: %w", g.permission, scope.Canonical(), sdk.ErrForbidden)
	}
	return nil
}

func seedTenancy(t *testing.T, st *memstore.Store) {
	t.Helper()
	rows := []relationship.CreateRelationship{
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

func newTenancyHost(t *testing.T, guard MutationGuard, cfg Config) (*Service, *memstore.Store) {
	t.Helper()
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	seedTenancy(t, st)
	cfg.Guard = guard
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service, st
}

func actor(id string) Actor { return Actor{PrincipalRef: PrincipalRef{Type: "user", ID: id}} }

func grantViewer(t *testing.T, svc *Service, who string) (*Receipt, error) {
	t.Helper()
	return svc.GrantRelationship(context.Background(), actor(who), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "dashboard", ResourceID: "d1", Relation: "viewer", Subject: subjU("reader"),
	})
}

func hasDep(deps []Dependency, kind ScopeKind, typ, id string) bool {
	for _, d := range deps {
		if d.Scope.Kind == kind && d.Scope.Type == typ && d.Scope.ID == id {
			return true
		}
	}
	return false
}

func assertNoViewerWritten(t *testing.T, st *memstore.Store) {
	t.Helper()
	targets, err := st.Relationships().GetRelationTargets(context.Background(), "dashboard", "d1", "viewer")
	if err != nil || len(targets) != 0 {
		t.Fatalf("a refused mutation must write nothing: targets=%+v err=%v", targets, err)
	}
}

func TestCheckPermissionInheritedThroughHierarchy(t *testing.T) {
	cases := []struct {
		name     string
		who      string
		wantDeps [][3]string // kind/type/id the walk must have recorded
	}{
		{"direct owner", "dash-owner", [][3]string{{"resource", "dashboard", "d1"}}},
		{"space manager one hop", "space-manager", [][3]string{{"resource", "dashboard", "d1"}, {"resource", "space", "child"}}},
		{"tenant owner three hops", "tenant-owner", [][3]string{{"resource", "dashboard", "d1"}, {"resource", "space", "child"}, {"resource", "space", "root"}, {"resource", "tenant", "t1"}}},
		{"group admin via userset through container", "group-admin", [][3]string{{"resource", "dashboard", "d1"}, {"resource", "space", "child"}, {"resource", "space", "root"}, {"resource", "tenant", "t1"}, {"resource", "group", "admins"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := &permissionGuard{permission: "manage"}
			svc, _ := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel()})
			rcpt, err := grantViewer(t, svc, tc.who)
			if err != nil {
				t.Fatalf("GrantRelationship: %v (check err %v)", err, guard.checkErr)
			}
			if rcpt == nil || rcpt.Outcome != OutcomeApplied {
				t.Fatalf("want applied receipt, got %+v", rcpt)
			}
			if !guard.allowed {
				t.Fatalf("guard should have observed allow")
			}
			for _, d := range tc.wantDeps {
				if !hasDep(guard.deps, ScopeKind(d[0]), d[1], d[2]) {
					t.Fatalf("dependency %s:%s:%s not recorded; deps=%+v", d[0], d[1], d[2], guard.deps)
				}
			}
		})
	}
}

func TestCheckPermissionDeniesWithoutAuthorityAndWritesNothing(t *testing.T) {
	guard := &permissionGuard{permission: "manage"}
	svc, st := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel()})
	_, err := grantViewer(t, svc, "stranger")
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if guard.checkErr != nil || guard.allowed {
		t.Fatalf("want a clean deny: err=%v allowed=%v", guard.checkErr, guard.allowed)
	}
	// The whole chain was still consulted (and recorded) before the deny.
	if !hasDep(guard.deps, ScopeResource, "tenant", "t1") {
		t.Fatalf("deny must still record the navigated scopes: %+v", guard.deps)
	}
	assertNoViewerWritten(t, st)
}

func TestCheckPermissionRefusesRolesOwnedPairBeforeAnyRead(t *testing.T) {
	roles := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"dashboard": {Roles: []string{"publisher"}, Permissions: map[string][]string{"publish": {"publisher"}}},
	}}
	t.Run("mixed model", func(t *testing.T) {
		guard := &permissionGuard{permission: "publish"}
		svc, st := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel(), RoleModel: roles})
		_, err := grantViewer(t, svc, "dash-owner")
		if !errors.Is(err, ErrPermissionOwnedByRoles) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want ErrPermissionOwnedByRoles (invalid input), got %v", err)
		}
		if len(guard.deps) != 0 {
			t.Fatalf("refusal must precede any store read: deps=%+v", guard.deps)
		}
		assertNoViewerWritten(t, st)
	})
	t.Run("roles only", func(t *testing.T) {
		st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
		guard := &permissionGuard{permission: "publish"}
		comps, err := NewService(Repositories{Roles: st.Roles(), Mutations: st.Mutations()}, Config{RoleModel: roles, Guard: guard})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		_, err = comps.Service.AssignRole(context.Background(), actor("dash-owner"), AssignRoleCommand{
			MutationID: mustID(t), ResourceType: "dashboard", ResourceID: "d1", Role: "publisher", Subject: PrincipalRef{Type: "user", ID: "u2"},
		})
		if !errors.Is(err, ErrPermissionOwnedByRoles) {
			t.Fatalf("roles-only deployment must answer the same sentinel, got %v", err)
		}
		if len(guard.deps) != 0 {
			t.Fatalf("refusal must precede any store read: deps=%+v", guard.deps)
		}
	})
	t.Run("roles only, undeclared pair", func(t *testing.T) {
		st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
		guard := &permissionGuard{permission: "manage"}
		comps, err := NewService(Repositories{Roles: st.Roles(), Mutations: st.Mutations()}, Config{RoleModel: roles, Guard: guard})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		_, err = comps.Service.AssignRole(context.Background(), actor("dash-owner"), AssignRoleCommand{
			MutationID: mustID(t), ResourceType: "dashboard", ResourceID: "d1", Role: "publisher", Subject: PrincipalRef{Type: "user", ID: "u2"},
		})
		if !errors.Is(err, ErrRelationshipsNotConfigured) {
			t.Fatalf("no relationship kind: want ErrRelationshipsNotConfigured, got %v", err)
		}
	})
}

func TestCheckPermissionRejectsMalformedInput(t *testing.T) {
	t.Run("subject scope", func(t *testing.T) {
		subj := ScopeKey{Kind: ScopeSubject, Type: "user", ID: "u1"}
		guard := &permissionGuard{permission: "manage", scope: &subj}
		svc, st := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel()})
		_, err := grantViewer(t, svc, "dash-owner")
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("want ErrInvalidRequest, got %v", err)
		}
		if len(guard.deps) != 0 {
			t.Fatalf("no read on malformed input: %+v", guard.deps)
		}
		assertNoViewerWritten(t, st)
	})
	t.Run("empty permission", func(t *testing.T) {
		guard := &permissionGuard{permission: ""}
		svc, st := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel()})
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
		limits EvaluationLimits
	}{
		// tenant-owner needs three Through hops; one is not enough.
		{"through depth", "tenant-owner", EvaluationLimits{MaxThroughDepth: 1}},
		// dashboard, space child, space root, tenant = four states.
		{"graph states", "tenant-owner", EvaluationLimits{MaxGraphStates: 2}},
		// group-admin's expansion has 2 states (seed + admins#member) on every
		// direct read; a bound of 1 overflows in the STORE adapter, which must
		// surface as the same evaluation-limit outcome the read side reports.
		{"expansion states in the adapter", "group-admin", EvaluationLimits{MaxGraphStates: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := &permissionGuard{permission: "manage"}
			svc, st := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel(), Limits: tc.limits})
			_, err := grantViewer(t, svc, tc.who)
			if !errors.Is(err, ErrEvaluationLimit) || !errors.Is(err, sdk.ErrUnavailable) {
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
		svc, _ := newTenancyHost(t, guard, Config{RelationshipModel: tenancyModel()})
		want, err := svc.Check(context.Background(), CheckRequest{
			Principal: PrincipalRef{Type: "user", ID: who}, Permission: "manage", Resource: Resource{Type: "dashboard", ID: "d1"},
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
