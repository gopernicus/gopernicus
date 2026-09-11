package authorizationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

type unguardedRoles struct{ stubRoleAdmin }

func (*unguardedRoles) Guarded() bool { return false }

func TestDirectAdapterRejectsInvalidWiring(t *testing.T) {
	for _, tc := range []struct {
		name     string
		services Services
		routes   RoleRoutes
		want     error
	}{
		{"typed nil roles", Services{Roles: (*stubRoleAdmin)(nil)}, RoleRoutes{}, sdk.ErrInvalidInput},
		{"typed nil writes", Services{Mutations: (*stubRoleAdmin)(nil)}, RoleRoutes{}, sdk.ErrInvalidInput},
		{"typed nil decisions", Services{Decisions: (*decisions.Service)(nil)}, RoleRoutes{}, sdk.ErrInvalidInput},
		{"gate without roles", Services{}, RoleRoutes{Gate: passGate}, ErrRoleRoutesGateWithoutRoles},
		{"gate without writes", Services{Roles: &stubRoleAdmin{}}, RoleRoutes{Gate: passGate}, ErrRoleRoutesGateWithoutGuard},
		{"unguarded writes", Services{Roles: &stubRoleAdmin{}, Mutations: &unguardedRoles{}}, RoleRoutes{Gate: passGate}, ErrRoleRoutesGateWithoutGuard},
		{"invalid list strategy", Services{}, RoleRoutes{ListStrategy: "unknown"}, ErrInvalidListStrategy},
		{"unused assignment policy", Services{}, RoleRoutes{AssignmentPolicy: func(context.Context, mutations.AssignRoleCommand) error { return nil }}, ErrRoleRouteAssignmentPolicyWithoutRoutes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := New(tc.services, WithRoleRoutes(tc.routes))
			if a != nil || !errors.Is(err, tc.want) {
				t.Fatalf("adapter=%v error=%v want=%v", a, err, tc.want)
			}
		})
	}
}

func TestDirectHandlersRetainGateAndDisabledPosture(t *testing.T) {
	var gates int
	stub := &stubRoleAdmin{receipt: testReceipt()}
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gates++; adminGate()(next).ServeHTTP(w, r) })
	}
	a, err := New(Services{Roles: stub, Mutations: stub}, WithRoleRoutes(RoleRoutes{Gate: gate}))
	if err != nil {
		t.Fatal(err)
	}
	h := http.NewServeMux()
	h.Handle("POST /host-owned/roles", a.AssignRole())
	rec := doJSON(t, h, "POST", "/host-owned/roles", `{"subject_type":"user","subject_id":"u-2","role":"viewer"}`)
	if rec.Code != http.StatusOK || gates != 1 || stub.assignActor.ID != "admin-1" {
		t.Fatalf("own route bypassed policy or actor: status=%d gates=%d actor=%+v body=%s", rec.Code, gates, stub.assignActor, rec.Body.String())
	}
	disabled, err := New(Services{Roles: stub, Mutations: stub})
	if err != nil {
		t.Fatal(err)
	}
	if err := disabled.Register(nil); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.Handler{disabled.AssignRole(), disabled.UnassignRole(), disabled.RolesBySubject(), disabled.RolesByResource(), disabled.EffectiveRolesByResource()} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/own-route", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("disabled own route returned %d", response.Code)
		}
	}
}
