package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// The bundled role-administration proof (issue #20). The pocket's own tests use
// STUB gates — it cannot import pockets/authentication — so THIS is the only
// place the real chain is provable end to end: a real session cookie from the
// auth pocket, the real platform-admin permission decided by the authorization
// pocket, and the real FS9 error bodies a client sees. It is the #6
// MachineRoutesGate precedent applied to role administration.

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
// log the not-mounted WARN lands in.
type roleRoutesHost struct {
	*linkHost
	comps authorization.Components
	logs  *bytes.Buffer
}

// newRoleRoutesHost boots the real composition. withGate false is the
// deny-by-absence posture — the same wiring with Config.RoleRoutesGate nil — so
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
	comps, err := newAuthorization(configured)
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}

	sender := &recordingSender{}
	svc := bootInProcess(t, sender, nil)

	router := web.NewWebHandler(web.WithLogging(quietLog()))
	mount := pocket.Mount{
		Router: router,
		Logger: log,
		Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog())),
	}
	if err := comps.Service.Register(mount); err != nil {
		t.Fatalf("authorization Register: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: router, Logger: quietLog(), Events: mount.Events}); err != nil {
		t.Fatalf("auth Register: %v", err)
	}
	gate.set(roleAdministrationGate(
		svc.RequireAccessTokenLive(),
		comps.Service.RequirePermissionFixed(platformResourceType, "admin", platformResourceID),
	))

	if err := seedAuthorization(context.Background(), comps.SystemMutator); err != nil {
		t.Fatalf("seedAuthorization: %v", err)
	}

	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	origins := allowedOrigins()
	if len(origins) == 0 {
		t.Fatal("host has no allowed origins")
	}
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
	if _, err := h.comps.SystemMutator.GrantRelationship(context.Background(), authorization.GrantRelationshipCommand{
		MutationID:   mustMutationID(h.t),
		ResourceType: platformResourceType,
		ResourceID:   platformResourceID,
		Relation:     "admin",
		Subject:      authorization.SubjectRef{Type: "user", ID: userID},
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

// receiptEnvelope is the assign/unassign response as a client reads it.
type receiptEnvelope struct {
	Receipt struct {
		MutationID string `json:"mutation_id"`
		ScopeKind  string `json:"scope_kind"`
		ScopeType  string `json:"scope_type"`
		ScopeID    string `json:"scope_id"`
		Operation  string `json:"operation"`
		Outcome    string `json:"outcome"`
		Revision   uint64 `json:"revision"`
		Replayed   bool   `json:"replayed"`
		CreatedAt  string `json:"created_at"`
	} `json:"receipt"`
	SameRoleGrantRemains bool `json:"same_role_grant_remains"`
}

// TestRoleRoutesPlatformAdminDrivesTheLifecycle walks the whole administration
// flow over real HTTP with a real session: assign, replay, list, unassign.
func TestRoleRoutesPlatformAdminDrivesTheLifecycle(t *testing.T) {
	host := newRoleRoutesHost(t, true)
	admin := host.signUp(roleAdminEmail)
	host.makePlatformAdmin(admin.userIDFor())

	mutationID := "auth-cms-role-routes-proof-000001"
	body := `{"mutation_id":"` + mutationID + `","subject_type":"user","subject_id":"` + roleGrantee +
		`","role":"` + demoRole + `","resource_type":"` + demoResourceType + `","resource_id":"` + demoResourceID + `"}`

	resp, payload := admin.do("POST", "/authorization/roles", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	first := decodeReceipt(t, payload)
	if first.Receipt.Outcome != "applied" || first.Receipt.Replayed {
		t.Fatalf("first assign receipt = %+v, want applied and not replayed", first.Receipt)
	}
	if first.Receipt.Operation != "role_assign" || first.Receipt.ScopeType != demoResourceType {
		t.Errorf("receipt scope/operation = %+v", first.Receipt)
	}

	resp, payload = admin.do("POST", "/authorization/roles", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	replay := decodeReceipt(t, payload)
	if !replay.Receipt.Replayed {
		t.Error("an exact retry of the same mutation_id did not report replayed")
	}
	if replay.Receipt.Revision != first.Receipt.Revision {
		t.Errorf("replay revision = %d, want the original %d", replay.Receipt.Revision, first.Receipt.Revision)
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
	removed := decodeReceipt(t, payload)
	if removed.Receipt.Outcome != "applied" {
		t.Errorf("unassign outcome = %q, want applied", removed.Receipt.Outcome)
	}
	if removed.SameRoleGrantRemains {
		t.Error("same_role_grant_remains = true, but there is no global grant of this role")
	}
	if removed.Receipt.MutationID == "" {
		t.Error("the server minted no mutation_id for a request that supplied none")
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
// the real host: the same wiring with no gate answers 404 everywhere and says so
// at boot.
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
	if !bytes.Contains(host.logs.Bytes(), []byte("are NOT mounted")) {
		t.Errorf("no not-mounted WARN at boot: %s", host.logs.String())
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

func decodeReceipt(t *testing.T, payload []byte) receiptEnvelope {
	t.Helper()
	var got receiptEnvelope
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode receipt envelope: %v (body=%s)", err, payload)
	}
	return got
}

// authServiceIsTheAuthenticator keeps the gate's first layer named in one place;
// a compile-time assertion that the host's chosen posture is a web.Middleware.
var _ = func(svc *auth.Service) web.Middleware { return svc.RequireAccessTokenLive() }
