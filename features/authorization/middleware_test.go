package authorization

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestRequirePermissionPanicsOnRolesOnly proves the registration/boot-time
// fail-fast: a roles-only Service (relationship kind unwired) mounting the
// builder panics rather than deferring to a per-request 500.
func TestRequirePermissionPanicsOnRolesOnly(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service

	defer func() {
		if recover() == nil {
			t.Fatalf("RequirePermission on a roles-only Service must panic at mount")
		}
	}()
	_ = svc.RequirePermission("delete", FixedResource("post", "p1"))
}

// TestRequirePermissionDelegates proves the root builder delegates to the engine
// implementation: no principal → 401, a principal without a grant → 403 (relFake
// denies every Check).
func TestRequirePermissionDelegates(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()})
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
