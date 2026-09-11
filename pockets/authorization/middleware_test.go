package authorization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestRequirePermissionPanicsWithoutAModelBearingKind proves the
// registration/boot-time fail-fast: a roles-only Service with NO role model bears
// no model at all, so EVERY gate panics at mount rather than deferring to a
// per-request 500 — and the message names the fix (wire a model), not the
// relationship kind.
func TestRequirePermissionPanicsWithoutAModelBearingKind(t *testing.T) {
	comps, err := New(Repositories{Roles: &roleFake{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps

	mounts := map[string]struct {
		mount func()
		want  string
	}{
		"RequirePermission": {
			func() { _ = svc.HTTP.RequirePermission("delete", authorizationhttp.FixedResource("post", "p1")) },
			"authorization: RequirePermission requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it",
		},
		"RequirePermissionOn": {
			func() { _ = svc.HTTP.RequirePermissionOn("post", "delete", "postID") },
			"authorization: RequirePermissionOn requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it",
		},
		"RequirePermissionFixed": {
			func() { _ = svc.HTTP.RequirePermissionFixed("post", "delete", "p1") },
			"authorization: RequirePermissionFixed requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it",
		},
		"RequireAnyPermission": {
			func() {
				_ = svc.HTTP.RequireAnyPermission(authorizationhttp.GateSpec{ResourceType: "post", Permission: "delete", Resource: authorizationhttp.FixedResource("post", "p1")})
			},
			"authorization: RequireAnyPermission requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it",
		},
	}
	for name, tc := range mounts {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatalf("%s on a modelless Service must panic at mount", name)
				}
				msg, ok := recovered.(string)
				if !ok || msg != tc.want {
					t.Fatalf("panic message:\n got %v\nwant %q", recovered, tc.want)
				}
			}()
			tc.mount()
		})
	}
}

// Request-facing services must not expose a path to separately held trusted writers.
func TestPublicServicesKeepTrustedCapabilitiesSeparate(t *testing.T) {
	comps := mustComponents(t, Repositories{Relationships: memory.NewRelationships(), Roles: memory.NewRoles()}, WithRelationshipModel(validModel()))
	trusted := map[reflect.Type]bool{reflect.TypeOf(&relationships.RelationshipWriter{}): true, reflect.TypeOf(&roles.Writer{}): true, reflect.TypeOf(&mutations.SystemMutator{}): true}
	for _, service := range []any{comps.Decisions, comps.Relationships, comps.Roles, comps.Mutations, comps.HTTP} {
		typ := reflect.TypeOf(service)
		for i := 0; i < typ.NumMethod(); i++ {
			method := typ.Method(i)
			for j := 0; j < method.Type.NumOut(); j++ {
				if trusted[method.Type.Out(j)] {
					t.Errorf("%s.%s exposes trusted capability", typ, method.Name)
				}
			}
		}
	}
	for _, service := range []any{comps.Decisions, comps.Relationships, comps.Roles} {
		typ := reflect.TypeOf(service)
		for _, method := range []string{"AssignRole", "UnassignRole", "CreateRelationships", "SetRelationTargets", "Apply", "TeardownResourceAuthorization"} {
			if _, ok := typ.MethodByName(method); ok {
				t.Errorf("read service %s exposes %s", typ, method)
			}
		}
	}
}

// TestGatesOnARolesOnlyModelHost proves the gates are LIVE on a roles-only host
// once it configures a role model: the same ladder, decided by role assignments.
func TestGatesOnARolesOnlyModelHost(t *testing.T) {
	roles := newSeededRoles(t, assignment("u1", "auditor", "project", "p1"))
	comps, err := New(Repositories{Roles: roles}, WithRoleModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	gate := comps.HTTP.RequirePermissionOn("project", "audit", "projectID")
	handler := gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for name, tc := range map[string]struct {
		projectID  string
		wantStatus int
	}{
		"role held → next runs":  {"p1", http.StatusNoContent},
		"role not held → denied": {"p2", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/projects/"+tc.projectID, nil)
			req.SetPathValue("projectID", tc.projectID)
			req = req.WithContext(sdk.WithPrincipal(req.Context(), sdk.Principal{Type: "user", ID: "u1"}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d (body %q)", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}

	// No principal is still 401, and an undeclared pair still panics at mount.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/p1", nil)
	req.SetPathValue("projectID", "p1")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: want 401, got %d", rec.Code)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("an undeclared pair must panic at mount even on a model-bearing roles host")
			}
		}()
		_ = comps.HTTP.RequirePermissionOn("project", "view", "projectID")
	}()

	// The same decision through the plain Check facade.
	res, err := comps.Decisions.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "audit", Resource: authmodel.Resource{Type: "project", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("Check on a roles-only model host: got %+v err=%v", res, err)
	}
}

// TestRequireAnyPermissionDelegates drives the disjunction through the PUBLIC
// facade: alternatives are evaluated in order, the granted one admits, and a
// route line whose alternatives are all ungranted is 403.
func TestRequireAnyPermissionDelegates(t *testing.T) {
	roles := newSeededRoles(t, assignment("u1", "auditor", "project", "p1"))
	comps, err := New(Repositories{Roles: roles}, WithRoleModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	handler := comps.HTTP.RequireAnyPermission(
		authorizationhttp.GateSpec{ResourceType: "project", Permission: "audit", Resource: authorizationhttp.FixedResource("project", "p2")},
		authorizationhttp.GateSpec{ResourceType: "project", Permission: "audit", Resource: authorizationhttp.FixedResource("project", "p1")},
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	req = req.WithContext(sdk.WithPrincipal(req.Context(), sdk.Principal{Type: "user", ID: "u1"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the second alternative is granted: want 204, got %d (body %q)", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/gated", nil)
	req = req.WithContext(sdk.WithPrincipal(req.Context(), sdk.Principal{Type: "user", ID: "u2"}))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no alternative granted: want 403, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: want 401, got %d", rec.Code)
	}
}

// TestRequirePermissionDelegates proves the root builder delegates to the engine
// implementation: no principal → 401, a principal without a grant → 403 (relFake
// denies every Check).
func TestRequirePermissionDelegates(t *testing.T) {
	comps, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps

	gate := svc.HTTP.RequirePermission("delete", authorizationhttp.FixedResource("post", "p1"))
	handler := gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No principal → 401.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: want 401, got %d", rec.Code)
	}

	// Principal without a grant → 403.
	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	req = req.WithContext(sdk.WithPrincipal(req.Context(), sdk.Principal{Type: "user", ID: "u1"}))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("principal without grant: want 403, got %d", rec.Code)
	}
}
