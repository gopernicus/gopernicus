package decisionsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/rolesvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// limitProbe is a roleProbe whose scoped probe exhausts the evaluation budget —
// the 503 leg of the gate ladder, which must be distinguishable from the plain
// 500 an ordinary store failure produces.
type limitProbe struct{}

func (limitProbe) HasRoleWhere(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, string, error) {
	return false, "", authorizersvc.ErrEvaluationLimit
}

func (limitProbe) ListRoleAssignmentsBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, authorizersvc.ErrEvaluationLimit
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
		req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
	}
	return req
}

// TestCompositeGatesLegalityAcrossBothModels proves the registration-time
// legality check consults BOTH models: a relationship-owned pair and a role-owned
// pair both mount, and a pair neither declares panics at mount with the unchanged
// wording.
func TestCompositeGatesLegalityAcrossBothModels(t *testing.T) {
	c, _, _ := newBothKinds(t, authorizersvc.EvaluationLimits{})

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
	limits := resolvedLimits(t, authorizersvc.EvaluationLimits{})
	model := mustCompile(t, compositeRoleModel(), nil)

	roles := rolesvc.NewService(memstore.NewRoles())
	assign(t, roles, "u1", "auditor", "project", "p1")
	live := NewComposite(nil, roles, model, limits)
	failing := NewComposite(nil, errProbe{err: errors.New("store exploded")}, model, limits)
	limited := NewComposite(nil, limitProbe{}, model, limits)

	tests := []struct {
		name          string
		composite     *Composite
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
			gate := tt.composite.RequirePermissionOn("project", "audit", "projectID")
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
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	grant(t, eng, "project", "byRelationship", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "byRole")

	gate := c.RequireAnyPermission(
		authorizersvc.GateSpec{ResourceType: "project", Permission: "view", Resource: authorizersvc.PathResource("project", "projectID")},
		authorizersvc.GateSpec{ResourceType: "project", Permission: "audit", Resource: authorizersvc.PathResource("project", "projectID")},
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
	c, _, _ := newBothKinds(t, authorizersvc.EvaluationLimits{})

	t.Run("a pair from each model mounts", func(t *testing.T) {
		_ = c.RequireAnyPermission(
			authorizersvc.GateSpec{ResourceType: "project", Permission: "view", Resource: authorizersvc.PathResource("project", "projectID")},
			authorizersvc.GateSpec{ResourceType: "platform", Permission: "steward", Resource: authorizersvc.FixedResource("platform", "global")},
		)
	})

	t.Run("declared by neither panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("must panic at registration")
			}
		}()
		c.RequireAnyPermission(
			authorizersvc.GateSpec{ResourceType: "project", Permission: "view", Resource: authorizersvc.PathResource("project", "projectID")},
			authorizersvc.GateSpec{ResourceType: "project", Permission: "fly", Resource: authorizersvc.PathResource("project", "projectID")},
		)
	})
}

// TestCompositeGatesEmptyPathParameterFailsClosed proves the resolver leg: a
// route pattern that does not carry the named parameter is a 500, never a check
// against an empty resource id.
func TestCompositeGatesEmptyPathParameterFailsClosed(t *testing.T) {
	c, _ := newRolesOnly(t, authorizersvc.EvaluationLimits{})
	ran := false
	rec := httptest.NewRecorder()
	c.RequirePermissionOn("project", "audit", "projectID")(ranHandler(&ran)).
		ServeHTTP(rec, principalRequest("/projects/p1", true))

	if rec.Code != http.StatusInternalServerError || ran {
		t.Fatalf("missing path parameter: want 500 with no next, got %d ran=%v", rec.Code, ran)
	}
}
