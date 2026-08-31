package authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authorization/internal/inbound/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// allowRoleRouteGuard is the permissive MutationGuard the role-route wiring
// tests use where the guard's DECISION is not what is under test. The route
// tests that care about denial supply their own.
type allowRoleRouteGuard struct{}

func (allowRoleRouteGuard) AuthorizeMutation(context.Context, MutationAttempt, DecisionView) error {
	return nil
}

// passRoleRouteGate is a no-op host gate: it authenticates and authorizes
// nothing, and exists only so a Config carries a NON-NIL RoleRoutesGate.
func passRoleRouteGate(next http.Handler) http.Handler { return next }

// refuseAssignment is a stand-in AssignmentPolicy for the construction matrix.
func refuseAssignment(context.Context, AssignRoleCommand) error {
	return sdk.ErrForbidden
}

// TestNewServiceRoleRoutesConstructionMatrix pins every row of the bundled
// role-administration wiring matrix: each contradictory posture fails
// construction by its own named sentinel, and each legal posture builds.
func TestNewServiceRoleRoutesConstructionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		repos   func(*memstore.Store) Repositories
		cfg     Config
		wantErr error
	}{
		{
			name: "gate without the roles kind",
			repos: func(s *memstore.Store) Repositories {
				return Repositories{Relationships: &relFake{}, Mutations: s.Mutations()}
			},
			cfg: Config{
				RelationshipModel: validModel(),
				Guard:             allowRoleRouteGuard{},
				RoleRoutesGate:    passRoleRouteGate,
			},
			wantErr: ErrRoleRoutesGateWithoutRoles,
		},
		{
			name:    "gate without a guard",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{RoleRoutesGate: passRoleRouteGate},
			wantErr: ErrRoleRoutesGateWithoutGuard,
		},
		{
			name:    "assignment policy without the routes",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{Guard: allowRoleRouteGuard{}, AssignmentPolicy: refuseAssignment},
			wantErr: ErrAssignmentPolicyWithoutRoutes,
		},
		{
			name:    "unknown list strategy",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{Guard: allowRoleRouteGuard{}, RoleRoutesGate: passRoleRouteGate, ListStrategy: "keyset"},
			wantErr: ErrInvalidListStrategy,
		},
		{
			name:  "unknown list strategy is rejected even when orphaned by no gate",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   Config{ListStrategy: "keyset"},
			// An invalid enum is a typo, never a posture — the orphan rule silences
			// only a VALID unused value.
			wantErr: ErrInvalidListStrategy,
		},
		{
			name:  "gate with roles and a guard",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:   Config{Guard: allowRoleRouteGuard{}, RoleRoutesGate: passRoleRouteGate},
		},
		{
			name:  "gate with an assignment policy and an offset strategy",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg: Config{
				Guard:            allowRoleRouteGuard{},
				RoleRoutesGate:   passRoleRouteGate,
				AssignmentPolicy: refuseAssignment,
				ListStrategy:     crud.StrategyOffset,
			},
		},
		{
			name:  "no gate at all is unchanged",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:   Config{Guard: allowRoleRouteGuard{}},
		},
		{
			name:  "a valid but unused list strategy is a silent cosmetic orphan",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   Config{ListStrategy: crud.StrategyOffset},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			_, err := NewService(tc.repos(store), tc.cfg)
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("NewService error = %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && err != nil:
				t.Fatalf("NewService: %v", err)
			}
		})
	}
}

// TestServiceCapturesRoleRouteConfig proves the three new Config fields reach
// the Service, so Register has everything the mount needs.
func TestServiceCapturesRoleRouteConfig(t *testing.T) {
	store := memstore.New()
	comps, err := NewService(
		Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		Config{
			Guard:            allowRoleRouteGuard{},
			RoleRoutesGate:   passRoleRouteGate,
			AssignmentPolicy: refuseAssignment,
			ListStrategy:     crud.StrategyOffset,
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	if svc.roleRoutesGate == nil {
		t.Error("roleRoutesGate not captured")
	}
	if svc.assignmentPolicy == nil {
		t.Error("assignmentPolicy not captured")
	}
	if svc.listStrategy != crud.StrategyOffset {
		t.Errorf("listStrategy = %q, want %q", svc.listStrategy, crud.StrategyOffset)
	}
}

// TestValidateListStrategy pins the accepted set directly, including the zero
// value that resolves to cursor at the transport.
func TestValidateListStrategy(t *testing.T) {
	for _, ok := range []crud.Strategy{"", crud.StrategyCursor, crud.StrategyOffset} {
		if err := validateListStrategy(ok); err != nil {
			t.Errorf("validateListStrategy(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []crud.Strategy{"keyset", "CURSOR", "page"} {
		if err := validateListStrategy(bad); !errors.Is(err, ErrInvalidListStrategy) {
			t.Errorf("validateListStrategy(%q) = %v, want ErrInvalidListStrategy", bad, err)
		}
	}
}

// TestRoleRouteSentinelsWrapNoSDKKind pins the construction sentinels as
// BOOT-time faults: they carry no sdk taxonomy kind, so an operator sees a
// startup failure rather than an HTTP status class.
func TestRoleRouteSentinelsWrapNoSDKKind(t *testing.T) {
	sentinels := []error{
		ErrRoleRoutesGateWithoutRoles,
		ErrRoleRoutesGateWithoutGuard,
		ErrAssignmentPolicyWithoutRoutes,
		ErrInvalidListStrategy,
		ErrRoleRoutesWithoutRouter,
	}
	kinds := []error{sdk.ErrInvalidInput, sdk.ErrForbidden, sdk.ErrUnauthorized, sdk.ErrNotFound, sdk.ErrConflict}
	for _, s := range sentinels {
		for _, k := range kinds {
			if errors.Is(s, k) {
				t.Errorf("%v wraps sdk kind %v; construction faults carry none", s, k)
			}
		}
	}
}

// webMiddlewareCompiles keeps the Config field's declared type honest: the gate
// is exactly an sdk web.Middleware, assignable from a plain wrapper.
var _ web.Middleware = passRoleRouteGate

// ---------------------------------------------------------------------------
// End-to-end proof: a real Register over memstore
//
// The gate here is a STUB (authenticate + allow/deny): the pocket cannot import
// pockets/authentication or drive a real permission gate against itself, so the
// full host chain and the real FS9 bodies are proven in examples/auth-cms — the
// #6 precedent. What these tests own is everything BETWEEN the gate and the
// store.
// ---------------------------------------------------------------------------

// recordingRoleGuard allows every attempt and records what it saw, so a test can
// prove a refused request never reached the guarded boundary.
type recordingRoleGuard struct {
	attempts []MutationAttempt
	deny     bool
}

func (g *recordingRoleGuard) AuthorizeMutation(_ context.Context, attempt MutationAttempt, _ DecisionView) error {
	g.attempts = append(g.attempts, attempt)
	if g.deny {
		return fmt.Errorf("the host refused this mutation: %w", sdk.ErrForbidden)
	}
	return nil
}

// roleAdminHost is a mounted bundled surface plus the pieces a test needs to
// seed and inspect state directly.
type roleAdminHost struct {
	handler http.Handler
	comps   Components
	guard   *recordingRoleGuard
	logs    *bytes.Buffer
}

// authenticatedGate is the ordinary test gate: it stashes a principal exactly as
// a host's authentication middleware would, then allows.
func authenticatedGate(p identity.Principal) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
		})
	}
}

// denyingRoleGate refuses every request the way a real permission gate does.
func denyingRoleGate(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		web.RespondJSONError(w, web.ErrForbidden("permission denied"))
	})
}

// newRoleAdminHost builds a roles-only memstore host, registers it through the
// real pocket.Mount, and returns the mounted router. A nil gate is the
// deny-by-absence posture.
func newRoleAdminHost(t *testing.T, gate web.Middleware, policy AssignmentPolicy) roleAdminHost {
	t.Helper()
	store := memstore.New()
	guard := &recordingRoleGuard{}
	comps, err := NewService(
		Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		Config{Guard: guard, RoleRoutesGate: gate, AssignmentPolicy: policy},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	logs := &bytes.Buffer{}
	router := web.NewWebHandler()
	mount := pocket.Mount{
		Router: router,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if err := comps.Service.Register(mount); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return roleAdminHost{handler: router, comps: comps, guard: guard, logs: logs}
}

// postRole runs one bundled write.
func postRole(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// getRole runs one bundled listing.
func getRole(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// bundledRoleRoutes is the full surface each posture case sweeps.
var bundledRoleRoutes = []struct{ method, path string }{
	{"POST", "/authorization/roles"},
	{"POST", "/authorization/roles/unassign"},
	{"GET", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1"},
	{"GET", "/authorization/roles/by-resource?resource_type=organization&resource_id=o-1"},
	{"GET", "/authorization/roles/effective?resource_type=organization&resource_id=o-1"},
}

// TestRegisterWithoutGateMountsNothing proves deny-by-absence: with no gate the
// five paths 404 and boot warns that they are not mounted.
func TestRegisterWithoutGateMountsNothing(t *testing.T) {
	host := newRoleAdminHost(t, nil, nil)
	for _, rt := range bundledRoleRoutes {
		var rec *httptest.ResponseRecorder
		if rt.method == "POST" {
			rec = postRole(t, host.handler, rt.path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		} else {
			rec = getRole(t, host.handler, rt.path)
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, rec.Code)
		}
	}
	if !strings.Contains(host.logs.String(), "are NOT mounted") {
		t.Errorf("no not-mounted WARN in the boot log: %s", host.logs.String())
	}
	if !strings.Contains(host.logs.String(), "role_routes=false") {
		t.Errorf("registered line does not report role_routes=false: %s", host.logs.String())
	}
}

// TestRegisterWithGateAndNilRouterIsLoud proves the promised-routes-nowhere-to-go
// wiring fails Register rather than booting route-free.
func TestRegisterWithGateAndNilRouterIsLoud(t *testing.T) {
	store := memstore.New()
	comps, err := NewService(
		Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		Config{Guard: &recordingRoleGuard{}, RoleRoutesGate: passRoleRouteGate},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := comps.Service.Register(pocket.Mount{}); !errors.Is(err, ErrRoleRoutesWithoutRouter) {
		t.Fatalf("Register = %v, want ErrRoleRoutesWithoutRouter", err)
	}
}

// TestRegisterWithoutGateStillToleratesAZeroMount pins the unchanged posture for
// every host that sets no gate.
func TestRegisterWithoutGateStillToleratesAZeroMount(t *testing.T) {
	store := memstore.New()
	comps, err := NewService(Repositories{Roles: store.Roles()}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := comps.Service.Register(pocket.Mount{}); err != nil {
		t.Fatalf("Register with a zero Mount: %v", err)
	}
}

// TestBundledRoutesRefuseThroughADenyingGate proves the gate is the whole
// posture: every route answers the gate's own FS9 403 and no request reaches
// the guarded boundary.
func TestBundledRoutesRefuseThroughADenyingGate(t *testing.T) {
	host := newRoleAdminHost(t, denyingRoleGate, nil)
	for _, rt := range bundledRoleRoutes {
		var rec *httptest.ResponseRecorder
		if rt.method == "POST" {
			rec = postRole(t, host.handler, rt.path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		} else {
			rec = getRole(t, host.handler, rt.path)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", rt.method, rt.path, rec.Code)
			continue
		}
		var body struct{ Code string }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Code != "permission_denied" {
			t.Errorf("%s %s code = %q, want permission_denied", rt.method, rt.path, body.Code)
		}
	}
	if len(host.guard.attempts) != 0 {
		t.Errorf("a gate-denied request reached the MutationGuard: %+v", host.guard.attempts)
	}
}

// TestBundledWritesRequireAStashedPrincipal proves a gate that authorizes but
// does not AUTHENTICATE gets a 401, never a zero actor.
func TestBundledWritesRequireAStashedPrincipal(t *testing.T) {
	host := newRoleAdminHost(t, passRoleRouteGate, nil)
	for _, path := range []string{"/authorization/roles", "/authorization/roles/unassign"} {
		rec := postRole(t, host.handler, path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s = %d, want 401", path, rec.Code)
		}
	}
	if len(host.guard.attempts) != 0 {
		t.Error("an unauthenticated request reached the MutationGuard")
	}
}

// TestBundledRoleLifecycle drives the whole receipted lifecycle through the
// mounted routes: assign, replay the same client id, list, then unassign with an
// honest same_role_grant_remains.
func TestBundledRoleLifecycle(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}), nil)
	clientID := "bundled-lifecycle-mutation-id-0001"

	// A GLOBAL viewer grant, so the later scoped unassign has a fallback to
	// report honestly.
	if rec := postRole(t, host.handler, "/authorization/roles",
		`{"mutation_id":"bundled-lifecycle-global-id-000001","subject_type":"user","subject_id":"u-1","role":"viewer"}`); rec.Code != http.StatusOK {
		t.Fatalf("global assign = %d, body %s", rec.Code, rec.Body.String())
	}

	rec := postRole(t, host.handler, "/authorization/roles",
		`{"mutation_id":"`+clientID+`","subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d, body %s", rec.Code, rec.Body.String())
	}
	first := decodeAssign(t, rec)
	if first.Receipt.Outcome != string(OutcomeApplied) {
		t.Errorf("outcome = %q, want applied", first.Receipt.Outcome)
	}
	if first.Receipt.Replayed {
		t.Error("first application reports replayed")
	}
	if first.Receipt.MutationID != clientID {
		t.Errorf("mutation_id = %q, want the client's %q", first.Receipt.MutationID, clientID)
	}

	replay := postRole(t, host.handler, "/authorization/roles",
		`{"mutation_id":"`+clientID+`","subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay = %d, body %s", replay.Code, replay.Body.String())
	}
	second := decodeAssign(t, replay)
	if !second.Receipt.Replayed {
		t.Error("an exact retry did not report replayed")
	}
	if second.Receipt.Revision != first.Receipt.Revision {
		t.Errorf("replay revision = %d, want the original %d", second.Receipt.Revision, first.Receipt.Revision)
	}

	listing := getRole(t, host.handler, "/authorization/roles/by-subject?subject_type=user&subject_id=u-1")
	if listing.Code != http.StatusOK {
		t.Fatalf("by-subject = %d, body %s", listing.Code, listing.Body.String())
	}
	var page struct {
		Items []struct {
			Role         string `json:"role"`
			ResourceType string `json:"resource_type"`
			ResourceID   string `json:"resource_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listing.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("by-subject items = %d, want the global and the scoped grant", len(page.Items))
	}

	effective := getRole(t, host.handler, "/authorization/roles/effective?resource_type=organization&resource_id=o-1")
	if effective.Code != http.StatusOK {
		t.Fatalf("effective = %d, body %s", effective.Code, effective.Body.String())
	}
	var grants struct {
		Items []struct {
			Role   string `json:"role"`
			Direct bool   `json:"direct"`
			Global bool   `json:"global"`
		} `json:"items"`
	}
	if err := json.Unmarshal(effective.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(grants.Items) != 1 || !grants.Items[0].Direct || !grants.Items[0].Global {
		t.Fatalf("effective grants = %+v, want one grant held both directly and globally", grants.Items)
	}

	unassign := postRole(t, host.handler, "/authorization/roles/unassign",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
	if unassign.Code != http.StatusOK {
		t.Fatalf("unassign = %d, body %s", unassign.Code, unassign.Body.String())
	}
	var removed struct {
		Receipt              receiptWire `json:"receipt"`
		SameRoleGrantRemains bool        `json:"same_role_grant_remains"`
	}
	if err := json.Unmarshal(unassign.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if removed.Receipt.Outcome != string(OutcomeApplied) {
		t.Errorf("unassign outcome = %q, want applied", removed.Receipt.Outcome)
	}
	if !removed.SameRoleGrantRemains {
		t.Error("same_role_grant_remains = false, but the global viewer grant still satisfies the scoped check")
	}
	if removed.Receipt.MutationID == "" {
		t.Error("the server minted no mutation_id for a request that supplied none")
	}
}

// receiptWire is the receipt envelope as a caller sees it.
type receiptWire struct {
	MutationID string `json:"mutation_id"`
	ScopeKind  string `json:"scope_kind"`
	ScopeType  string `json:"scope_type"`
	ScopeID    string `json:"scope_id"`
	Operation  string `json:"operation"`
	Outcome    string `json:"outcome"`
	Revision   uint64 `json:"revision"`
	Replayed   bool   `json:"replayed"`
	CreatedAt  string `json:"created_at"`
}

type assignWire struct {
	Receipt receiptWire `json:"receipt"`
}

func decodeAssign(t *testing.T, rec *httptest.ResponseRecorder) assignWire {
	t.Helper()
	var got assignWire
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode assign envelope: %v", err)
	}
	return got
}

// TestAssignmentPolicyRefusalNeverReachesTheGuard proves the legality hook runs
// before the guarded write and that a refusal wrapping sdk.ErrForbidden lands
// 403 with nothing written.
func TestAssignmentPolicyRefusalNeverReachesTheGuard(t *testing.T) {
	var seen []AssignRoleCommand
	policy := func(_ context.Context, cmd AssignRoleCommand) error {
		seen = append(seen, cmd)
		if cmd.Role == "steward" {
			return fmt.Errorf("steward is not assignable through the bundled route: %w", sdk.ErrForbidden)
		}
		return nil
	}
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}), policy)

	rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"steward","resource_type":"organization","resource_id":"o-1"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("refused assign = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(host.guard.attempts) != 0 {
		t.Error("a policy-refused assign reached the MutationGuard")
	}
	listing := getRole(t, host.handler, "/authorization/roles/by-subject?subject_type=user&subject_id=u-1")
	if !strings.Contains(listing.Body.String(), `"items":[]`) {
		t.Errorf("a policy-refused assign reached the store: %s", listing.Body.String())
	}

	if allowed := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`); allowed.Code != http.StatusOK {
		t.Fatalf("allowed assign = %d, body %s", allowed.Code, allowed.Body.String())
	}
	if len(seen) != 2 {
		t.Fatalf("policy saw %d commands, want both assigns", len(seen))
	}
	if seen[0].MutationID == "" {
		t.Error("the policy saw an unresolved MutationID; it must see the exact command that would apply")
	}
}

// TestAssignmentPolicyIsNotConsultedOnUnassign pins the assign-only scope: the
// unassign route never calls the hook, whatever it would have said.
func TestAssignmentPolicyIsNotConsultedOnUnassign(t *testing.T) {
	var calls int
	policy := func(context.Context, AssignRoleCommand) error {
		calls++
		return nil
	}
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}), policy)

	if rec := postRole(t, host.handler, "/authorization/roles/unassign",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("unassign = %d, body %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Errorf("AssignmentPolicy ran %d times on unassign, want 0", calls)
	}
}

// TestAssignmentPolicyRefusalIsNotAudited pins the documented non-property: the
// AuditSink observes guard outcomes, and a policy refusal never reaches the
// guard, so it is never recorded.
func TestAssignmentPolicyRefusalIsNotAudited(t *testing.T) {
	store := memstore.New()
	sink := &countingRoleAuditSink{}
	comps, err := NewService(
		Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		Config{
			Guard:            &recordingRoleGuard{},
			Audit:            sink,
			RoleRoutesGate:   authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}),
			AssignmentPolicy: func(context.Context, AssignRoleCommand) error { return sdk.ErrForbidden },
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	router := web.NewWebHandler()
	if err := comps.Service.Register(pocket.Mount{Router: router}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if rec := postRole(t, router, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("refused assign = %d, body %s", rec.Code, rec.Body.String())
	}
	if sink.events != 0 {
		t.Errorf("the audit sink recorded %d events for a policy refusal, want 0", sink.events)
	}
}

type countingRoleAuditSink struct{ events int }

func (s *countingRoleAuditSink) RecordMutation(context.Context, AuditEvent) error {
	s.events++
	return nil
}

// TestBundledAssignForwardsTheActorToTheGuard proves the principal the gate
// stashed is the Actor the guard authorizes — the whole point of D2.
func TestBundledAssignForwardsTheActorToTheGuard(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "service_account", ID: "sa-7"}), nil)
	if rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("assign = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(host.guard.attempts) != 1 {
		t.Fatalf("guard saw %d attempts, want 1", len(host.guard.attempts))
	}
	attempt := host.guard.attempts[0]
	if attempt.Actor.Type != "service_account" || attempt.Actor.ID != "sa-7" {
		t.Errorf("actor = %+v, want the stashed principal", attempt.Actor)
	}
	if attempt.Operation != OpRoleAssign {
		t.Errorf("operation = %q, want role_assign", attempt.Operation)
	}
	if attempt.Scope != (ScopeKey{Kind: ScopeResource, Type: "organization", ID: "o-1"}) {
		t.Errorf("scope = %+v, want the resource scope", attempt.Scope)
	}
}

// TestBundledHalfScopedPairIs400 proves the domain's global-or-fully-scoped rule
// surfaces as a 400 rather than a 500.
func TestBundledHalfScopedPairIs400(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}), nil)
	rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("half-scoped assign = %d, body %s", rec.Code, rec.Body.String())
	}
}

// TestBundledStaleRevisionIsAConflict proves expected_revision reaches the
// mutation boundary and a stale value answers the conflict class, not 200.
func TestBundledStaleRevisionIsAConflict(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(identity.Principal{Type: "user", ID: "admin-1"}), nil)
	rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1","expected_revision":42}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale-revision assign = %d, body %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Adapter unit tests — the single conversion site, field for field
// ---------------------------------------------------------------------------

// TestAdapterAssignCommandFieldForField pins every field of the conversion, so
// the transport's duplicated request shape cannot drift from the command
// silently.
func TestAdapterAssignCommandFieldForField(t *testing.T) {
	adapter := roleRouteAdapter{}
	rev := uint64(11)
	cmd, err := adapter.assignCommand(inbound.AssignRoleRequest{
		ActorType: "user", ActorID: "admin-1",
		MutationID:  "adapter-supplied-mutation-id-0001",
		SubjectType: "service_account", SubjectID: "sa-3",
		Role:         "contributor",
		ResourceType: "organization", ResourceID: "o-9",
		ExpectedRevision: &rev,
	})
	if err != nil {
		t.Fatalf("assignCommand: %v", err)
	}
	want := AssignRoleCommand{
		MutationID:       "adapter-supplied-mutation-id-0001",
		Subject:          PrincipalRef{Type: "service_account", ID: "sa-3"},
		Role:             "contributor",
		ResourceType:     "organization",
		ResourceID:       "o-9",
		ExpectedRevision: func() *Revision { r := Revision(11); return &r }(),
	}
	if cmd.MutationID != want.MutationID || cmd.Subject != want.Subject || cmd.Role != want.Role ||
		cmd.ResourceType != want.ResourceType || cmd.ResourceID != want.ResourceID {
		t.Errorf("command = %+v, want %+v", cmd, want)
	}
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision != *want.ExpectedRevision {
		t.Errorf("expected revision = %v, want %v", cmd.ExpectedRevision, *want.ExpectedRevision)
	}
}

// TestAdapterUnassignCommandFieldForField is the symmetric pin.
func TestAdapterUnassignCommandFieldForField(t *testing.T) {
	adapter := roleRouteAdapter{}
	cmd, err := adapter.unassignCommand(inbound.UnassignRoleRequest{
		ActorType: "user", ActorID: "admin-1",
		MutationID:  "adapter-supplied-mutation-id-0002",
		SubjectType: "user", SubjectID: "u-4",
		Role: "viewer",
	})
	if err != nil {
		t.Fatalf("unassignCommand: %v", err)
	}
	if cmd.MutationID != "adapter-supplied-mutation-id-0002" ||
		cmd.Subject != (PrincipalRef{Type: "user", ID: "u-4"}) ||
		cmd.Role != "viewer" || cmd.ResourceType != "" || cmd.ResourceID != "" {
		t.Errorf("command = %+v", cmd)
	}
	if cmd.ExpectedRevision != nil {
		t.Errorf("expected revision = %v, want nil for an absent anchor", cmd.ExpectedRevision)
	}
}

// TestAdapterMintsAndValidatesMutationIDs pins the three id postures: minted
// when absent (and distinct per request), kept when supplied, refused when too
// weak.
func TestAdapterMintsAndValidatesMutationIDs(t *testing.T) {
	adapter := roleRouteAdapter{}

	first, err := adapter.assignCommand(inbound.AssignRoleRequest{SubjectType: "user", SubjectID: "u-1", Role: "viewer"})
	if err != nil {
		t.Fatalf("assignCommand: %v", err)
	}
	second, err := adapter.assignCommand(inbound.AssignRoleRequest{SubjectType: "user", SubjectID: "u-1", Role: "viewer"})
	if err != nil {
		t.Fatalf("assignCommand: %v", err)
	}
	if first.MutationID == "" || first.MutationID == second.MutationID {
		t.Errorf("minted ids %q and %q; each request must get its own", first.MutationID, second.MutationID)
	}
	if err := first.MutationID.Validate(); err != nil {
		t.Errorf("minted id fails validation: %v", err)
	}

	if _, err := adapter.assignCommand(inbound.AssignRoleRequest{MutationID: "short", SubjectType: "user", SubjectID: "u-1", Role: "viewer"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("weak client id = %v, want an invalid-input refusal", err)
	}
	if _, err := adapter.unassignCommand(inbound.UnassignRoleRequest{MutationID: "short", SubjectType: "user", SubjectID: "u-1", Role: "viewer"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("weak client id on unassign = %v, want an invalid-input refusal", err)
	}
}

// TestRevisionFrom pins the optional compare-and-set conversion.
func TestRevisionFrom(t *testing.T) {
	if revisionFrom(nil) != nil {
		t.Error("a nil anchor became non-nil")
	}
	v := uint64(5)
	got := revisionFrom(&v)
	if got == nil || *got != Revision(5) {
		t.Errorf("revisionFrom(&5) = %v, want 5", got)
	}
	v = 6
	if *got != Revision(5) {
		t.Error("revisionFrom aliased the caller's value")
	}
}
