package authorizationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// limitProbe is a roleProbe whose scoped probe exhausts the evaluation budget —
// the 503 leg of the gate ladder, which must be distinguishable from the plain
// 500 an ordinary store failure produces.
type limitProbe struct{}

func (limitProbe) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, authmodel.ErrEvaluationLimit
}

func (limitProbe) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, authmodel.ErrEvaluationLimit
}

func (limitProbe) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, authmodel.ErrEvaluationLimit
}

func ranHandler(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

func principalRequest(target string, withPrincipal bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if withPrincipal {
		req = req.WithContext(sdk.WithPrincipal(req.Context(), sdk.Principal{Type: "user", ID: "u1"}))
	}
	return req
}

// TestCompositeGatesLegalityAcrossBothModels proves the registration-time
// legality check consults BOTH models: a relationship-owned pair and a role-owned
// pair both mount, and a pair neither declares panics at mount with the unchanged
// wording.
func TestCompositeGatesLegalityAcrossBothModels(t *testing.T) {
	c, _, _ := newBothKinds(t, authmodel.EvaluationLimits{})

	t.Run("relationship-owned pair mounts", func(t *testing.T) {
		_ = c.RequirePermissionOn("project", "view", "projectID")
	})
	t.Run("role-owned pair mounts", func(t *testing.T) {
		_ = c.RequirePermissionOn("project", "audit", "projectID")
		_ = c.RequirePermissionFixed("platform", "steward", "global")
	})

	for name, mount := range map[string]func(){
		"declared by neither": func() { c.RequirePermissionOn("project", "fly", "projectID") },
		"unknown type":        func() { c.RequirePermissionFixed("comet", "view", "main") },
		"empty parameter":     func() { c.RequirePermissionOn("project", "audit", "") },
		"empty fixed id":      func() { c.RequirePermissionFixed("project", "audit", "") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("must panic at registration")
				}
				if msg, ok := recovered.(string); ok && name == "declared by neither" {
					const want = "authorization: the model declares no permission \"fly\" on resource type \"project\" — fix the gate or the schema"
					if msg != want {
						t.Fatalf("panic message:\n got %q\nwant %q", msg, want)
					}
				}
			}()
			mount()
		})
	}
}

// TestCompositeGatesLadderOnARolesOnlyHost drives the full 401/403/500/503 ladder
// through a roles-only-with-model composite: the shared gate body behaves
// identically when the deciding kind is the role model.
func TestCompositeGatesLadderOnARolesOnlyHost(t *testing.T) {
	limits := resolvedLimits(t, authmodel.EvaluationLimits{})
	model := compositeRoleModel()

	roles := newTestRoles(t, memory.NewRoles())
	assign(t, roles, "u1", "auditor", "project", "p1")
	live := newDecisionFixture(t, nil, roles, model, limits)
	failing := newDecisionFixture(t, nil, errProbe{err: errors.New("store exploded")}, model, limits)
	limited := newDecisionFixture(t, nil, limitProbe{}, model, limits)

	tests := []struct {
		name          string
		composite     *decisions.Service
		path          string
		param         string
		withPrincipal bool
		wantStatus    int
		wantNext      bool
	}{
		{"no principal → 401", live, "/projects/p1", "p1", false, http.StatusUnauthorized, false},
		{"role held → next runs", live, "/projects/p1", "p1", true, http.StatusOK, true},
		{"role not held → 403", live, "/projects/p2", "p2", true, http.StatusForbidden, false},
		{"store failure → 500 fail closed", failing, "/projects/p1", "p1", true, http.StatusInternalServerError, false},
		{"evaluation limit → 503 fail closed", limited, "/projects/p1", "p1", true, http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			gate := NewGates(tt.composite, tt.composite, limits.MaxBatchSize).RequirePermissionOn("project", "audit", "projectID")
			req := principalRequest(tt.path, tt.withPrincipal)
			req.SetPathValue("projectID", tt.param)

			rec := httptest.NewRecorder()
			gate(ranHandler(&ran)).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: want %d, got %d (body %q)", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran: want %v, got %v", tt.wantNext, ran)
			}
		})
	}
}

// TestCompositeRequireAnyPermissionAcrossBothModels proves the disjunction
// dispatches per alternative: one route line disjoins the relationship-owned
// project/view pair with the role-owned project/audit pair, and EITHER grant
// admits — with no grant it is a 403.
func TestCompositeRequireAnyPermissionAcrossBothModels(t *testing.T) {
	c, eng, roles := newBothKinds(t, authmodel.EvaluationLimits{})
	grant(t, eng, "project", "byRelationship", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "byRole")

	gate := c.RequireAnyPermission(
		GateSpec{ResourceType: "project", Permission: "view", Resource: PathResource("project", "projectID")},
		GateSpec{ResourceType: "project", Permission: "audit", Resource: PathResource("project", "projectID")},
	)
	call := func(projectID string) (int, bool) {
		ran := false
		req := principalRequest("/projects/"+projectID, true)
		req.SetPathValue("projectID", projectID)
		rec := httptest.NewRecorder()
		gate(ranHandler(&ran)).ServeHTTP(rec, req)
		return rec.Code, ran
	}

	if code, ran := call("byRelationship"); code != http.StatusOK || !ran {
		t.Fatalf("relationship-owned alternative must admit: %d ran=%v", code, ran)
	}
	if code, ran := call("byRole"); code != http.StatusOK || !ran {
		t.Fatalf("role-owned alternative must admit: %d ran=%v", code, ran)
	}
	if code, ran := call("ungranted"); code != http.StatusForbidden || ran {
		t.Fatalf("neither alternative granted: want 403 with no next, got %d ran=%v", code, ran)
	}
}

// TestCompositeRequireAnyPermissionRegistration: the alternatives are validated
// against BOTH models at mount — a pair either declares is legal, a pair neither
// declares panics.
func TestCompositeRequireAnyPermissionRegistration(t *testing.T) {
	c, _, _ := newBothKinds(t, authmodel.EvaluationLimits{})

	t.Run("a pair from each model mounts", func(t *testing.T) {
		_ = c.RequireAnyPermission(
			GateSpec{ResourceType: "project", Permission: "view", Resource: PathResource("project", "projectID")},
			GateSpec{ResourceType: "platform", Permission: "steward", Resource: FixedResource("platform", "global")},
		)
	})

	t.Run("declared by neither panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("must panic at registration")
			}
		}()
		c.RequireAnyPermission(
			GateSpec{ResourceType: "project", Permission: "view", Resource: PathResource("project", "projectID")},
			GateSpec{ResourceType: "project", Permission: "fly", Resource: PathResource("project", "projectID")},
		)
	})
}

// TestCompositeGatesEmptyPathParameterFailsClosed proves the resolver leg: a
// route pattern that does not carry the named parameter is a 500, never a check
// against an empty resource id.
func TestCompositeGatesEmptyPathParameterFailsClosed(t *testing.T) {
	c, _ := newRolesOnly(t, authmodel.EvaluationLimits{})
	ran := false
	rec := httptest.NewRecorder()
	c.RequirePermissionOn("project", "audit", "projectID")(ranHandler(&ran)).
		ServeHTTP(rec, principalRequest("/projects/p1", true))

	if rec.Code != http.StatusInternalServerError || ran {
		t.Fatalf("missing path parameter: want 500 with no next, got %d ran=%v", rec.Code, ran)
	}
}

func compositeSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{
			Name: "org",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"enter": relationships.AnyOf(relationships.Direct("member")),
				},
			},
		},
		{
			Name: "project",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
					"org":    {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "org"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("org", "enter")),
				},
			},
		},
	})
}

// compositeRoleModel is the roles half of the shared fixture: project/audit on
// the SPLIT type plus a singleton platform type for the resource-type-independent
// permission.
func compositeRoleModel() authmodel.RoleModel {
	return authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"project":  {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
		"platform": {Roles: []string{"steward"}, Permissions: map[string][]string{"steward": {"steward"}}},
	}}
}

func newRelationshipEngine(t *testing.T, store relationships.Storer, limits authmodel.EvaluationLimits) *testRelationships {
	t.Helper()
	eng, err := relationships.NewService(store, compositeSchema(), relationships.WithLimits(limits))
	if err != nil {
		t.Fatalf("authorizersvc.NewService: %v", err)
	}
	return &testRelationships{eng.Service, eng.RelationshipWriter}
}

// newBothKinds builds the both-kinds composite over REAL engines and in-core
// stores, with the pair split over the "project" type.
func newBothKinds(t *testing.T, limits authmodel.EvaluationLimits) (Gates, *testRelationships, *testRoles) {
	t.Helper()
	resolved := resolvedLimits(t, limits)
	eng := newRelationshipEngine(t, memory.NewRelationships(), limits)
	roles := newTestRoles(t, memory.NewRoles())
	c := newDecisionFixture(t, eng, roles, compositeRoleModel(), resolved)
	return NewGates(c, c, resolved.MaxBatchSize), eng, roles
}

// newRolesOnly builds the roles-only-with-model composite: no relationship kind
// at all, so the role model answers every declared pair and owns the fallback.
func newRolesOnly(t *testing.T, limits authmodel.EvaluationLimits) (Gates, *testRoles) {
	t.Helper()
	roles := newTestRoles(t, memory.NewRoles())
	resolved := resolvedLimits(t, limits)
	c := newDecisionFixture(t, nil, roles, compositeRoleModel(), resolved)
	return NewGates(c, c, resolved.MaxBatchSize), roles
}

func grant(t *testing.T, eng *testRelationships, resourceType, resourceID, relation, subjectType, subjectID string) {
	t.Helper()
	err := eng.CreateRelationships(context.Background(), []relationships.CreateRelationship{{
		ResourceType: resourceType, ResourceID: resourceID, Relation: relation,
		SubjectType: subjectType, SubjectID: subjectID,
	}})
	if err != nil {
		t.Fatalf("CreateRelationships(%s:%s#%s): %v", resourceType, resourceID, relation, err)
	}
}

type errProbe struct{ err error }

func (p errProbe) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, p.err
}

func (p errProbe) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, p.err
}

func (p errProbe) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, p.err
}

func resolvedLimits(t *testing.T, in authmodel.EvaluationLimits) authmodel.EvaluationLimits {
	t.Helper()
	out, err := in.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return out
}

func assign(t *testing.T, svc *testRoles, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	if err := svc.AssignRole(context.Background(), "user", subjectID, roleName, resourceType, resourceID); err != nil {
		t.Fatalf("AssignRole(%s, %s, %s/%s): %v", subjectID, roleName, resourceType, resourceID, err)
	}
}
