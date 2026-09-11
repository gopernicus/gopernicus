package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"

	// The bundled role-administration proof (issue #20). The pocket's own tests use
	// STUB gates — it cannot import pockets/authentication — so THIS is the only
	// place the real chain is provable end to end: a real session cookie from the
	// auth pocket, the real platform-admin permission decided by the authorization
	// pocket, and the real FS9 error bodies a client sees. It is the #6
	// MachineRoutesGate precedent applied to role administration.
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

const (
	roleAdminEmail = "role-admin@example.com"
	rolePlainEmail = "role-plain@example.com"

	// roleGrantee is the subject the proof grants and revokes. It is a synthetic
	// principal: what is under test is the ADMINISTRATION surface, not who holds
	// the role.
	roleGrantee = "grantee-1"
)

// roleRoutesHost is a host whose router carries BOTH pockets — the auth surface
// the proof signs in through and the bundled role-administration routes it then
// drives — plus the trusted mutator that seeds the platform admin and the boot
// constructor-owned authorization log.
type roleRoutesHost struct {
	*linkHost
	comps authorization.Components
	logs  *bytes.Buffer
}

// newRoleRoutesHost boots the real composition. withGate false is the
// deny-by-absence posture — the same wiring with RoleRoutes.Gate nil — so
// one fixture proves both halves.
func newRoleRoutesHost(t *testing.T, withGate bool) *roleRoutesHost {
	t.Helper()

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// The ordering seam run() uses: the gate is NAMED before either service
	// exists and resolved per request once both do.
	gate := &deferredMiddleware{}
	var configured web.Middleware
	if withGate {
		configured = gate.middleware
	}
	comps, err := newAuthorization(configured, log)
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}

	sender := &recordingSender{}
	svc := bootInProcess(t, sender, nil)

	router := web.NewWebHandler()
	mount := pockets.Mount{
		Router: router,
		Logger: log,
		Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog())),
	}
	if err := comps.Register(mount); err != nil {
		t.Fatalf("authorization Register: %v", err)
	}
	if err := svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog(), Events: mount.Events}); err != nil {
		t.Fatalf("auth Register: %v", err)
	}
	gate.set(roleAdministrationGate(
		svc.HTTP.RequireAccessTokenLive(),
		comps.HTTP.RequirePermissionFixed(platformResourceType, "admin", platformResourceID),
	))

	if err := seedAuthorization(context.Background(), comps.SystemMutator); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}

	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	origins := hostAllowedOrigins(t)
	return &roleRoutesHost{
		linkHost: &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: origins[0]},
		comps:    comps,
		logs:     logs,
	}
}

// makePlatformAdmin grants the platform:main#admin data tuple through the
// trusted SystemMutator — the host recipe the gate's permission resolves
// against. Platform admin stays DATA, never Config.
func (h *roleRoutesHost) makePlatformAdmin(userID string) {
	h.t.Helper()
	if _, err := h.comps.SystemMutator.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{

		ResourceType: platformResourceType,
		ResourceID:   platformResourceID,
		Relation:     "admin",
		Subject:      relationships.SubjectRef{Type: "user", ID: userID},
	}); err != nil {
		h.t.Fatalf("seed platform admin %s: %v", userID, err)
	}
}

// roleAdminRoutes is the full bundled surface, as a client addresses it.
var roleAdminRoutes = []struct{ method, path, body string }{
	{"POST", "/authorization/roles", `{"subject_type":"user","subject_id":"` + roleGrantee + `","role":"` + demoRole + `","resource_type":"` + demoResourceType + `","resource_id":"` + demoResourceID + `"}`},
	{"POST", "/authorization/roles/unassign", `{"subject_type":"user","subject_id":"` + roleGrantee + `","role":"` + demoRole + `","resource_type":"` + demoResourceType + `","resource_id":"` + demoResourceID + `"}`},
	{"GET", "/authorization/roles/by-subject?subject_type=user&subject_id=" + roleGrantee, ""},
	{"GET", "/authorization/roles/by-resource?resource_type=" + demoResourceType + "&resource_id=" + demoResourceID, ""},
	{"GET", "/authorization/roles/effective?resource_type=" + demoResourceType + "&resource_id=" + demoResourceID, ""},
}

// mutationEnvelope is the assign/unassign response as a client reads it.
type mutationEnvelope struct {
	Outcome              string `json:"outcome"`
	SameRoleGrantRemains bool   `json:"same_role_grant_remains"`
}

func TestRoleRoutesPlatformAdminDrivesTheLifecycle(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	admin := host.signUp(roleAdminEmail)
	host.makePlatformAdmin(admin.userIDFor())

	body := `{"subject_type":"user","subject_id":"` + roleGrantee +
		`","role":"` + demoRole + `","resource_type":"` + demoResourceType + `","resource_id":"` + demoResourceID + `"}`

	resp, payload := admin.do("POST", "/authorization/roles", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	first := decodeMutation(t, payload)
	if first.Outcome != "applied" {
		t.Fatalf("first assign receipt = %+v, want applied and not replayed", first)
	}

	resp, payload = admin.do("POST", "/authorization/roles", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	replay := decodeMutation(t, payload)
	if replay.Outcome != "no_change" {
		t.Fatalf("duplicate assign: %+v", replay)
	}

	resp, payload = admin.do("GET", "/authorization/roles/by-subject?subject_type=user&subject_id="+roleGrantee, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("by-subject = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	var listing struct {
		Items []struct {
			Role       string `json:"role"`
			ResourceID string `json:"resource_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Items) != 1 || listing.Items[0].Role != demoRole || listing.Items[0].ResourceID != demoResourceID {
		t.Fatalf("by-subject items = %+v, want the one grant just assigned", listing.Items)
	}

	resp, payload = admin.do("GET", "/authorization/roles/effective?resource_type="+demoResourceType+"&resource_id="+demoResourceID, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("effective = %d, want 200; body=%s", resp.StatusCode, payload)
	}

	unassignBody := `{"subject_type":"user","subject_id":"` + roleGrantee +
		`","role":"` + demoRole + `","resource_type":"` + demoResourceType + `","resource_id":"` + demoResourceID + `"}`
	resp, payload = admin.do("POST", "/authorization/roles/unassign", unassignBody, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unassign = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	removed := decodeMutation(t, payload)
	if removed.Outcome != "applied" {
		t.Errorf("unassign outcome = %q, want applied", removed.Outcome)
	}
	if removed.SameRoleGrantRemains {
		t.Error("same_role_grant_remains = true, but there is no global grant of this role")
	}

	resp, payload = admin.do("GET", "/authorization/roles/by-subject?subject_type=user&subject_id="+roleGrantee, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("by-subject after unassign = %d; body=%s", resp.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Items) != 0 {
		t.Errorf("by-subject after unassign = %+v, want empty", listing.Items)
	}
}

// TestRoleRoutesRefuseANonAdmin proves the REAL FS9 denial body a signed-in
// non-admin sees on every route — the thing a stub gate cannot demonstrate.
func TestRoleRoutesRefuseANonAdmin(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	plain := host.signUp(rolePlainEmail)

	for _, rt := range roleAdminRoutes {
		resp, payload := plain.do(rt.method, rt.path, rt.body, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403; body=%s", rt.method, rt.path, resp.StatusCode, payload)
			continue
		}
		var body struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatalf("decode denial: %v", err)
		}
		if body.Code != "permission_denied" {
			t.Errorf("%s %s code = %q, want permission_denied", rt.method, rt.path, body.Code)
		}
	}
}

// TestRoleRoutesRefuseAnAnonymousCaller proves the authenticating layer of the
// gate: no credential, no route.
func TestRoleRoutesRefuseAnAnonymousCaller(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	anonymous := host.newClient()

	for _, rt := range roleAdminRoutes {
		resp, payload := anonymous.do(rt.method, rt.path, rt.body, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401; body=%s", rt.method, rt.path, resp.StatusCode, payload)
		}
	}
}

// TestRoleRoutesAreNotMountedWithoutAGate proves the deny-by-absence posture on
// the real host: the same wiring with no gate answers 404 everywhere, and
// intentional headless use is logged as ordinary configuration, not a warning.
func TestRoleRoutesAreNotMountedWithoutAGate(t *testing.T) {
	host := newRoleRoutesHost(t, false)
	admin := host.signUp(roleAdminEmail)
	host.makePlatformAdmin(admin.userIDFor())

	for _, rt := range roleAdminRoutes {
		resp, payload := admin.do(rt.method, rt.path, rt.body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404; body=%s", rt.method, rt.path, resp.StatusCode, payload)
		}
	}
	if !bytes.Contains(host.logs.Bytes(), []byte("role_routes=false")) || bytes.Contains(host.logs.Bytes(), []byte("level=WARN")) {
		t.Errorf("expected informational headless configuration: %s", host.logs.String())
	}
}

// TestRoleRoutesRejectAnUndeclaredRole proves the RoleModel's assign-time rule
// reaches a client as a 400, not a 500: the bundled route cannot store a role the
// host's model does not declare.
func TestRoleRoutesRejectAnUndeclaredRole(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	admin := host.signUp(roleAdminEmail)
	host.makePlatformAdmin(admin.userIDFor())

	resp, payload := admin.do("POST", "/authorization/roles",
		`{"subject_type":"user","subject_id":"`+roleGrantee+`","role":"not-in-the-model","resource_type":"`+
			demoResourceType+`","resource_id":"`+demoResourceID+`"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("undeclared role = %d, want 400; body=%s", resp.StatusCode, payload)
	}
}

// TestRoleRoutesRejectAHalfScopedPair proves the global-or-fully-scoped rule
// surfaces as a 400 through the real chain.
func TestRoleRoutesRejectAHalfScopedPair(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	admin := host.signUp(roleAdminEmail)
	host.makePlatformAdmin(admin.userIDFor())

	resp, payload := admin.do("POST", "/authorization/roles",
		`{"subject_type":"user","subject_id":"`+roleGrantee+`","role":"`+demoRole+`","resource_type":"`+demoResourceType+`"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("half-scoped pair = %d, want 400; body=%s", resp.StatusCode, payload)
	}
}

// TestDeferredMiddlewareInstalledReportsAssignment pins the boot assertion run()
// makes right after wiring the chain: unassigned is reported, assigned is not.
func TestDeferredMiddlewareInstalledReportsAssignment(t *testing.T) {
	gate := &deferredMiddleware{}
	if gate.installed() {
		t.Fatal("a fresh deferredMiddleware reports installed")
	}
	gate.set(func(next http.Handler) http.Handler { return next })
	if !gate.installed() {
		t.Error("an assigned deferredMiddleware reports NOT installed")
	}
}

// TestDeferredMiddlewareFailsClosed pins the ordering seam's posture: a gate that
// was never assigned refuses rather than admits.
func TestDeferredMiddlewareFailsClosed(t *testing.T) {
	var reached bool
	unassigned := &deferredMiddleware{}
	handler := unassigned.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/authorization/roles", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unassigned gate = %d, want 500", rec.Code)
	}
	if reached {
		t.Error("an unassigned gate admitted the request")
	}
}

func decodeMutation(t *testing.T, payload []byte) mutationEnvelope {
	t.Helper()
	var got mutationEnvelope
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode receipt envelope: %v (body=%s)", err, payload)
	}
	return got
}

// authServiceIsTheAuthenticator keeps the gate's first layer named in one place;
// a compile-time assertion that the host's chosen posture is a web.Middleware.
var _ = func(svc *auth.Components) web.Middleware { return svc.HTTP.RequireAccessTokenLive() }
