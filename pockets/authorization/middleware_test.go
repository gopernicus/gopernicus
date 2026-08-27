package authorization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestRequirePermissionPanicsWithoutAModelBearingKind proves the
// registration/boot-time fail-fast: a roles-only Service with NO role model bears
// no model at all, so EVERY gate panics at mount rather than deferring to a
// per-request 500 — and the message names the fix (wire a model), not the
// relationship kind.
func TestRequirePermissionPanicsWithoutAModelBearingKind(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service

	mounts := map[string]struct {
		mount func()
		want  string
	}{
		"RequirePermission": {
			func() { _ = svc.RequirePermission("delete", FixedResource("post", "p1")) },
			"authorization: RequirePermission requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it",
		},
		"RequirePermissionOn": {
			func() { _ = svc.RequirePermissionOn("post", "delete", "postID") },
			"authorization: RequirePermissionOn requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it",
		},
		"RequirePermissionFixed": {
			func() { _ = svc.RequirePermissionFixed("post", "delete", "p1") },
			"authorization: RequirePermissionFixed requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it",
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

// TestServicePublicMethodSetUnchanged pins the host-facing surface across the
// composite decider's introduction: the decision methods and the gates moved to
// a new implementation BEHIND the same names, so no method may appear or vanish
// here without a deliberate compatibility decision.
func TestServicePublicMethodSetUnchanged(t *testing.T) {
	want := []string{
		"AssignRole",
		"Check",
		"CheckBatch",
		"CheckExplain",
		"FilterAuthorized",
		"GetPermissionsForRelation",
		"GetRelationTargets",
		"GetSchema",
		"GrantRelationship",
		"HasRole",
		"ListEffectiveRoleGrantsByResource",
		"ListRelationshipsByResource",
		"ListRelationshipsBySubject",
		"ListRoleAssignmentsByResource",
		"ListRoleAssignmentsBySubject",
		"LookupResources",
		"PurgeResourceAuthorization",
		"Register",
		"ReplaceRelationship",
		"RequirePermission",
		"RequirePermissionFixed",
		"RequirePermissionOn",
		"RevokeRelationship",
		"SchemaDigest",
		"UnassignRole",
		"ValidateRelation",
		"ValidateRelationships",
	}
	typ := reflect.TypeOf(&Service{})
	got := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public method set changed:\n got %s\nwant %s", strings.Join(got, ", "), strings.Join(want, ", "))
	}
}

// TestGatesOnARolesOnlyModelHost proves the gates are LIVE on a roles-only host
// once it configures a role model: the same ladder, decided by role assignments.
func TestGatesOnARolesOnlyModelHost(t *testing.T) {
	roles := newSeededRoles(t, assignment("u1", "auditor", "project", "p1"))
	comps, err := NewService(Repositories{Roles: roles}, Config{RoleModel: projectRoleModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	gate := comps.Service.RequirePermissionOn("project", "audit", "projectID")
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
			req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
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
		_ = comps.Service.RequirePermissionOn("project", "view", "projectID")
	}()

	// The same decision through the plain Check facade.
	res, err := comps.Service.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "audit", Resource: Resource{Type: "project", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("Check on a roles-only model host: got %+v err=%v", res, err)
	}
}

// TestRequirePermissionDelegates proves the root builder delegates to the engine
// implementation: no principal → 401, a principal without a grant → 403 (relFake
// denies every Check).
func TestRequirePermissionDelegates(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service

	gate := svc.RequirePermission("delete", FixedResource("post", "p1"))
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
	req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("principal without grant: want 403, got %d", rec.Code)
	}
}
