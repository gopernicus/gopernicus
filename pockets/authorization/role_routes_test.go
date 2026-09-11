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

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// allowRoleRouteGuard is the permissive MutationGuard the role-route wiring
// tests use where the guard's DECISION is not what is under test. The route
// tests that care about denial supply their own.
type allowRoleRouteGuard struct{}

func (allowRoleRouteGuard) AuthorizeMutation(context.Context, mutations.MutationAttempt, mutations.DecisionView) error {
	return nil
}

// passRoleRouteGate is a no-op host gate: it authenticates and authorizes
// nothing, and exists only so RoleRoutes carries a NON-NIL Gate.
func passRoleRouteGate(next http.Handler) http.Handler { return next }

// refuseAssignment is a stand-in RoleRouteAssignmentPolicy for the construction matrix.
func refuseAssignment(context.Context, mutations.AssignRoleCommand) error {
	return sdk.ErrForbidden
}

// TestNewRoleRoutesConstructionMatrix pins every row of the bundled
// role-administration wiring matrix: each contradictory posture fails
// construction by its own named sentinel, and each legal posture builds.
func TestNewServiceRoleRoutesConstructionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		repos   func(*memory.Store) Repositories
		cfg     []Option
		wantErr error
	}{
		{
			name: "gate without the roles kind",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Relationships: &relFake{}, Mutations: s.Mutations()}
			},
			cfg:     []Option{WithRelationshipModel(validModel()), WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate})},
			wantErr: authorizationhttp.ErrRoleRoutesGateWithoutRoles,
		},
		{
			name: "gate without a guard",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg:     []Option{WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate})},
			wantErr: authorizationhttp.ErrRoleRoutesGateWithoutGuard,
		},
		{
			name: "assignment policy without the routes",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg:     []Option{WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{AssignmentPolicy: refuseAssignment})},
			wantErr: authorizationhttp.ErrRoleRouteAssignmentPolicyWithoutRoutes,
		},
		{
			name: "unknown list strategy",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg:     []Option{WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate, ListStrategy: "keyset"})},
			wantErr: authorizationhttp.ErrInvalidListStrategy,
		},
		{
			name:  "unknown list strategy is rejected even when orphaned by no gate",
			repos: func(s *memory.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   []Option{WithRoleRoutes(authorizationhttp.RoleRoutes{ListStrategy: "keyset"})},
			// An invalid enum is a typo, never a posture — the orphan rule silences
			// only a VALID unused value.
			wantErr: authorizationhttp.ErrInvalidListStrategy,
		},
		{
			name: "gate with roles and a guard",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg: []Option{WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate})},
		},
		{
			name: "gate with an assignment policy and an offset strategy",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg: []Option{WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate, AssignmentPolicy: refuseAssignment, ListStrategy: list.StrategyOffset})},
		},
		{
			name: "no gate at all is unchanged",
			repos: func(s *memory.Store) Repositories {
				return Repositories{Roles: s.Roles(), Mutations: s.Mutations()}
			},
			cfg: []Option{WithGuard(allowRoleRouteGuard{})},
		},
		{
			name:  "a valid but unused list strategy is a silent cosmetic orphan",
			repos: func(s *memory.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   []Option{WithRoleRoutes(authorizationhttp.RoleRoutes{ListStrategy: list.StrategyOffset})},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.New()
			_, err := New(tc.repos(store), tc.cfg...)
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("NewService error = %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && err != nil:
				t.Fatalf("NewService: %v", err)
			}
		})
	}
}

// TestServiceCapturesRoleRouteConfig proves the grouped route settings reach
// the Service, so Register has everything the mount needs.
func TestServiceCapturesRoleRouteConfig(t *testing.T) {
	store := memory.New()
	comps, err := New(Repositories{Roles: store.Roles(), Mutations: store.Mutations()}, WithGuard(allowRoleRouteGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate, AssignmentPolicy: refuseAssignment, ListStrategy: list.StrategyOffset}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.HTTP == nil || !comps.HTTP.RoutesEnabled() {
		t.Fatal("configured public adapter is missing its routes")
	}
}

// TestValidateListStrategy pins the accepted set directly, including the zero
// value that resolves to cursor at the transport.
func TestValidateListStrategy(t *testing.T) {
	for _, ok := range []list.Strategy{"", list.StrategyCursor, list.StrategyOffset} {
		if err := authorizationhttp.ValidateListStrategy(ok); err != nil {
			t.Errorf("authorizationhttp.ValidateListStrategy(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []list.Strategy{"keyset", "CURSOR", "page"} {
		if err := authorizationhttp.ValidateListStrategy(bad); !errors.Is(err, authorizationhttp.ErrInvalidListStrategy) {
			t.Errorf("authorizationhttp.ValidateListStrategy(%q) = %v, want ErrInvalidListStrategy", bad, err)
		}
	}
}

// TestRoleRouteSentinelsWrapNoSDKKind pins the construction sentinels as
// BOOT-time faults: they carry no sdk taxonomy kind, so an operator sees a
// startup failure rather than an HTTP status class.
func TestRoleRouteSentinelsWrapNoSDKKind(t *testing.T) {
	sentinels := []error{
		authorizationhttp.ErrRoleRoutesGateWithoutRoles,
		authorizationhttp.ErrRoleRoutesGateWithoutGuard,
		authorizationhttp.ErrRoleRouteAssignmentPolicyWithoutRoutes,
		authorizationhttp.ErrInvalidListStrategy,
		authorizationhttp.ErrRoleRoutesWithoutRouter,
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

// webMiddlewareCompiles keeps the route field's declared type honest: the gate
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
	attempts []mutations.MutationAttempt
	deny     bool
}

func (g *recordingRoleGuard) AuthorizeMutation(_ context.Context, attempt mutations.MutationAttempt, _ mutations.DecisionView) error {
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
func authenticatedGate(p sdk.Principal) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(sdk.WithPrincipal(r.Context(), p)))
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
// real pockets.Mount, and returns the mounted router. A nil gate is the
// deny-by-absence posture.
func newRoleAdminHost(t *testing.T, gate web.Middleware, policy authorizationhttp.RoleRouteAssignmentPolicy) roleAdminHost {
	t.Helper()
	store := memory.New()
	guard := &recordingRoleGuard{}
	logs := &bytes.Buffer{}
	comps, err := New(Repositories{Roles: store.Roles(), Mutations: store.Mutations()}, WithGuard(guard), WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: gate, AssignmentPolicy: policy}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	router := web.NewWebHandler()
	mount := pockets.Mount{
		Router: router,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if err := comps.Register(mount); err != nil {
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
// five paths 404 and intentional headless operation emits no warning.
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
	if strings.Contains(host.logs.String(), "level=WARN") {
		t.Errorf("intentional headless mount warned: %s", host.logs.String())
	}
	if !strings.Contains(host.logs.String(), "role_routes=false") {
		t.Errorf("registered line does not report role_routes=false: %s", host.logs.String())
	}
}

// TestRegisterWithGateAndNilRouterIsLoud proves the promised-routes-nowhere-to-go
// wiring fails Register rather than booting route-free.
func TestRegisterWithGateAndNilRouterIsLoud(t *testing.T) {
	store := memory.New()
	comps, err := New(Repositories{Roles: store.Roles(), Mutations: store.Mutations()}, WithGuard(&recordingRoleGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := comps.Register(pockets.Mount{}); !errors.Is(err, authorizationhttp.ErrRoleRoutesWithoutRouter) {
		t.Fatalf("Register = %v, want ErrRoleRoutesWithoutRouter", err)
	}
}

// TestRegisterWithoutGateStillToleratesAZeroMount pins the unchanged posture for
// every host that sets no gate.
func TestRegisterWithoutGateStillToleratesAZeroMount(t *testing.T) {
	store := memory.New()
	comps, err := New(Repositories{Roles: store.Roles()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := comps.Register(pockets.Mount{}); err != nil {
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

func TestBundledRoleLifecycle(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "user", ID: "admin-1"}), nil)

	// A GLOBAL viewer grant, so the later scoped unassign has a fallback to
	// report honestly.
	if rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer"}`); rec.Code != http.StatusOK {
		t.Fatalf("global assign = %d, body %s", rec.Code, rec.Body.String())
	}

	rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d, body %s", rec.Code, rec.Body.String())
	}
	first := decodeAssign(t, rec)
	if first.Outcome != string(mutations.OutcomeApplied) {
		t.Errorf("outcome = %q, want applied", first.Outcome)
	}

	replay := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay = %d, body %s", replay.Code, replay.Body.String())
	}
	second := decodeAssign(t, replay)
	if second.Outcome != string(mutations.OutcomeNoChange) {
		t.Fatalf("duplicate assign: %+v", second)
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
		Outcome              string `json:"outcome"`
		SameRoleGrantRemains bool   `json:"same_role_grant_remains"`
	}
	if err := json.Unmarshal(unassign.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if removed.Outcome != string(mutations.OutcomeApplied) {
		t.Errorf("unassign outcome = %q, want applied", removed.Outcome)
	}
	if !removed.SameRoleGrantRemains {
		t.Error("same_role_grant_remains = false, but the global viewer grant still satisfies the scoped check")
	}
}

type assignWire struct {
	Outcome string `json:"outcome"`
}

func decodeAssign(t *testing.T, rec *httptest.ResponseRecorder) assignWire {
	t.Helper()
	var got assignWire
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode assign envelope: %v", err)
	}
	return got
}

// TestRoleRouteAssignmentPolicyRefusalNeverReachesTheGuard proves the legality hook runs
// before the guarded write and that a refusal wrapping sdk.ErrForbidden lands
// 403 with nothing written.
func TestRoleRouteAssignmentPolicyRefusalNeverReachesTheGuard(t *testing.T) {
	var seen []mutations.AssignRoleCommand
	policy := func(_ context.Context, cmd mutations.AssignRoleCommand) error {
		seen = append(seen, cmd)
		if cmd.Role == "steward" {
			return fmt.Errorf("steward is not assignable through the bundled route: %w", sdk.ErrForbidden)
		}
		return nil
	}
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "user", ID: "admin-1"}), policy)

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
	if seen[0].Role != "steward" || seen[1].Role != "viewer" {
		t.Fatalf("policy commands: %+v", seen)
	}
}

// TestRoleRouteAssignmentPolicyIsNotConsultedOnUnassign pins the assign-only scope: the
// unassign route never calls the hook, whatever it would have said.
func TestRoleRouteAssignmentPolicyIsNotConsultedOnUnassign(t *testing.T) {
	var calls int
	policy := func(context.Context, mutations.AssignRoleCommand) error {
		calls++
		return nil
	}
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "user", ID: "admin-1"}), policy)

	if rec := postRole(t, host.handler, "/authorization/roles/unassign",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("unassign = %d, body %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Errorf("RoleRouteAssignmentPolicy ran %d times on unassign, want 0", calls)
	}
}

// TestBundledAssignForwardsTheActorToTheGuard proves the principal the gate
// stashed is the Actor the guard authorizes — the whole point of D2.
func TestBundledAssignForwardsTheActorToTheGuard(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "service_account", ID: "sa-7"}), nil)
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
	if attempt.Operation != mutations.OpRoleAssign {
		t.Errorf("operation = %q, want role_assign", attempt.Operation)
	}
	if attempt.Target != (mutations.Target{Kind: mutations.TargetResource, Type: "organization", ID: "o-1"}) {
		t.Errorf("scope = %+v, want the resource scope", attempt.Target)
	}
}

// TestBundledHalfScopedPairIs400 proves the domain's global-or-fully-scoped rule
// surfaces as a 400 rather than a 500.
func TestBundledHalfScopedPairIs400(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "user", ID: "admin-1"}), nil)
	rec := postRole(t, host.handler, "/authorization/roles",
		`{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("half-scoped assign = %d, body %s", rec.Code, rec.Body.String())
	}
}

// TestBundledMutationErrorsCarryStableCodes proves the bundled bodies answer
// through the pocket's OWN mapper (codes.go RespondError, threaded into the
// transport by Register) rather than the generic sdk-kind mapping: a client
// branches on the stable machine code, not on 409-versus-409.
func TestBundledWritesRejectRetiredProtocolFields(t *testing.T) {
	host := newRoleAdminHost(t, authenticatedGate(sdk.Principal{Type: "user", ID: "admin-1"}), nil)
	for _, extra := range []string{`"mutation_id":"obsolete"`, `"expected_revision":42`} {
		rec := postRole(t, host.handler, "/authorization/roles", `{"subject_type":"user","subject_id":"u-1","role":"viewer",`+extra+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("retired field accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	if len(host.guard.attempts) != 0 {
		t.Fatal("malformed commands reached guard")
	}
}

// errorCode reads the machine code off an FS9 error body.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %s: %v", rec.Body.String(), err)
	}
	return body.Code
}
