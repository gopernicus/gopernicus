package authorization_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization/authorizationtest"
)

var errResolverDown = errors.New("resolver dependency down")

// parityPolicy is one real Require shape from a host. params are the inputs it
// reads; trips is the most protocol round trips one request may take (one per
// reached level when every input is pure).
type parityPolicy struct {
	name      string
	pred      authorizationhttp.Predicate
	params    []string
	trips     int
	diverge9b bool
}

type parityRequest struct {
	principal sdk.Principal
	values    map[string]string
}

func (r parityRequest) String() string {
	keys := make([]string, 0, len(r.values))
	for k := range r.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(r.principal.Type + ":" + r.principal.ID)
	for _, k := range keys {
		v := r.values[k]
		if len(v) > 20 {
			v = fmt.Sprintf("%s…(%dB)", v[:8], len(v))
		}
		fmt.Fprintf(&b, " %s=%q", k, v)
	}
	return b.String()
}

type parityScenario struct {
	fixture    string
	principals []sdk.Principal
	values     map[string][]string
	policies   []parityPolicy
}

// countingAuthorizer counts protocol round trips. It forwards SchemaReader and
// Limiter, as every decorator must.
type countingAuthorizer struct {
	inner   authorizationEngine
	checks  atomic.Int64
	batches atomic.Int64
	fail    error
}

type authorizationEngine interface {
	sdkauthorization.Authorizer
	sdkauthorization.SchemaReader
	sdkauthorization.Limiter
}

func (c *countingAuthorizer) Check(ctx context.Context, r sdkauthorization.CheckRequest) (sdkauthorization.Decision, error) {
	c.checks.Add(1)
	if c.fail != nil {
		return sdkauthorization.Decision{}, c.fail
	}
	return c.inner.Check(ctx, r)
}

func (c *countingAuthorizer) CheckBatch(ctx context.Context, r []sdkauthorization.CheckRequest) ([]sdkauthorization.Decision, error) {
	c.batches.Add(1)
	if c.fail != nil {
		return nil, c.fail
	}
	return c.inner.CheckBatch(ctx, r)
}

func (c *countingAuthorizer) LookupResources(ctx context.Context, r sdkauthorization.LookupRequest) (sdkauthorization.LookupPage, error) {
	return c.inner.LookupResources(ctx, r)
}

func (c *countingAuthorizer) DeclaresResource(rt string) bool { return c.inner.DeclaresResource(rt) }
func (c *countingAuthorizer) DeclaresPermission(rt, name string) bool {
	return c.inner.DeclaresPermission(rt, name)
}
func (c *countingAuthorizer) DeclaresSubject(rt, rel, st string) bool {
	return c.inner.DeclaresSubject(rt, rel, st)
}
func (c *countingAuthorizer) Limits() sdkauthorization.Limits { return c.inner.Limits() }
func (c *countingAuthorizer) reset()                          { c.checks.Store(0); c.batches.Store(0) }
func (c *countingAuthorizer) trips() int                      { return int(c.checks.Load() + c.batches.Load()) }

// bareChecker hides SchemaReader and Limiter: a Checker with no discovery.
type bareChecker struct{ sdkauthorization.Checker }

func parityUser(id string) sdk.Principal { return sdk.Principal{Type: "user", ID: id} }

// programHub mirrors a host resolver that reads a store: not found is
// ErrAlternativeNotApplicable, a dependency failure is an error.
func programHub(r *http.Request) (authmodel.Resource, error) {
	switch v := r.URL.Query().Get("programId"); v {
	case "p1":
		return authmodel.Resource{Type: "client_hub", ID: "org1"}, nil
	case "p3":
		return authmodel.Resource{Type: "client_hub", ID: "org3"}, nil
	case "boom":
		return authmodel.Resource{}, errResolverDown
	default:
		return authmodel.Resource{}, authorizationhttp.ErrAlternativeNotApplicable
	}
}

func guardStatus(t *testing.T, guard *authorizationhttp.Adapter, pred authorizationhttp.Predicate, req parityRequest, opts ...authorizationhttp.RequireOption) int {
	t.Helper()
	h := guard.Require(pred, opts...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	target := "/x"
	if v, ok := req.values["?programId"]; ok {
		target += "?programId=" + v
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range req.values {
		if !strings.HasPrefix(k, "?") {
			r.SetPathValue(k, v)
		}
	}
	r = r.WithContext(sdk.WithPrincipal(r.Context(), req.principal))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

func parityRequests(principals []sdk.Principal, values map[string][]string, params []string) []parityRequest {
	var out []parityRequest
	var build func(i int, cur map[string]string)
	build = func(i int, cur map[string]string) {
		if i == len(params) {
			for _, p := range principals {
				cp := make(map[string]string, len(cur))
				for k, v := range cur {
					cp[k] = v
				}
				out = append(out, parityRequest{principal: p, values: cp})
			}
			return
		}
		for _, v := range values[params[i]] {
			cur[params[i]] = v
			build(i+1, cur)
		}
		delete(cur, params[i])
	}
	build(0, map[string]string{})
	return out
}

func parityScenarios() []parityScenario {
	H, G := authorizationhttp.HasRole, authorizationhttp.Global
	Can, Path, Fixed, Any, All := authorizationhttp.Can, authorizationhttp.Path, authorizationhttp.Fixed, authorizationhttp.Any, authorizationhttp.All
	HasRel, Res := authorizationhttp.HasRelationship, authorizationhttp.Resource
	hubPath := Path("client_hub", "organizationId")
	hubProgram := Res("client_hub", programHub)
	long := strings.Repeat("a", 250)
	return []parityScenario{
		{
			fixture: "gps-global",
			principals: []sdk.Principal{parityUser("stella"), parityUser("fern"), parityUser("vin"), parityUser("mo"), parityUser("ed"), parityUser("pv"),
				parityUser("half"), parityUser("led"), parityUser("lev"), parityUser("dev"), parityUser("nobody"), {Type: "service_account", ID: "bot"}},
			values: map[string][]string{"organizationId": {"org1", "org2", "org3", "", "org.dot", long}, "?programId": {"p1", "p3", "missing", "boom"}},
			policies: []parityPolicy{
				{name: "G1 hub Any(steward G, firmwide G, viewer Path, member Path)", params: []string{"organizationId"}, trips: 1,
					pred: Any(H("steward", G()), H("firmwide_clients", G()), H("viewer", hubPath), H("member", hubPath))},
				{name: "G2 Can(steward, Fixed(platform, global))", trips: 1, pred: Can("steward", Fixed("platform", "global"))},
				{name: "G3 Can(edit, Fixed(section, operations))", trips: 1, pred: Can("edit", Fixed("section", "operations"))},
				{name: "G4 HasRole(steward, Global())", trips: 1, pred: H("steward", G())},
				{name: "G5 Any(Can(edit section), All(HasRole page ×2))", trips: 2,
					pred: Any(Can("edit", Fixed("section", "operations")), All(H("viewer", Fixed("page", "contact-proposals")), H("viewer", Fixed("page", "mailbox-scan"))))},
				{name: "G6 Any(HasRole viewer ledger, HasRole editor ledger)", trips: 1, pred: Any(H("viewer", Fixed("ledger", "gps")), H("editor", Fixed("ledger", "gps")))},
				{name: "G7 hub Any(… Resource(programHub) …)", params: []string{"?programId"}, trips: 2,
					pred: Any(H("steward", G()), H("firmwide_clients", G()), H("viewer", hubProgram), H("member", hubProgram))},
				{name: "G8 hub Any(…, Can(enter, Fixed(section, operations)))", params: []string{"organizationId"}, trips: 1,
					pred: Any(H("steward", G()), H("firmwide_clients", G()), H("viewer", hubPath), H("member", hubPath), Can("enter", Fixed("section", "operations")))},
				{name: "G9 Any(Can(edit section), steward G, firmwide G, member Path)", params: []string{"organizationId"}, trips: 1,
					pred: Any(Can("edit", Fixed("section", "operations")), H("steward", G()), H("firmwide_clients", G()), H("member", hubPath))},
				{name: "G10 HasRole(editor, Fixed(ledger, gps))", trips: 1, pred: H("editor", Fixed("ledger", "gps"))},
			},
		},
		{
			fixture: "segovia-chain",
			principals: []sdk.Principal{parityUser("vic"), parityUser("ed"), parityUser("vera"), parityUser("sam"), parityUser("gus"), parityUser("gina"),
				parityUser("tom"), parityUser("pat"), parityUser("nobody")},
			values: map[string][]string{"timelineID": {"t1", "t2", ""}, "tenantID": {"acme", "other", ""}, "spaceID": {"s1", "s2", ""}},
			policies: []parityPolicy{
				{name: "S1 Can(view, Path(timeline))", params: []string{"timelineID"}, trips: 1, pred: Can("view", Path("timeline", "timelineID"))},
				{name: "S2 Any(HasRole(platform_admin, G), Can(manage, Path(tenant)))", params: []string{"tenantID"}, trips: 1,
					pred: Any(H("platform_admin", G()), Can("manage", Path("tenant", "tenantID")))},
				{name: "S3 All(Can(manage tenant), Can(manage space))", params: []string{"tenantID", "spaceID"}, trips: 1,
					pred: All(Can("manage", Path("tenant", "tenantID")), Can("manage", Path("space", "spaceID")))},
				{name: "S4 Any(admin G, Can(manage_tool_limits, Path(space)))", params: []string{"spaceID"}, trips: 1,
					pred: Any(H("platform_admin", G()), Can("manage_tool_limits", Path("space", "spaceID")))},
				{name: "S5 HasRole(platform_admin, Global())", trips: 1, pred: H("platform_admin", G())},
				{name: "S6 Can(view_timeline_space, Path(space))", params: []string{"spaceID"}, trips: 1, pred: Can("view_timeline_space", Path("space", "spaceID"))},
			},
		},
		{
			fixture:    "documents",
			principals: []sdk.Principal{parityUser("alice"), parityUser("bob"), parityUser("olivia"), parityUser("mike"), parityUser("root"), parityUser("nobody")},
			values:     map[string][]string{"docID": {authorizationtest.DocID(1), authorizationtest.DocID(0), "", "nope"}, "projectID": {"demo", ""}},
			policies: []parityPolicy{
				{name: "D1 Can(view, Path(document))", params: []string{"docID"}, trips: 1, pred: Can("view", Path("document", "docID"))},
				{name: "D2 HasRelationship(viewer, Path(document))", params: []string{"docID"}, trips: 1, pred: HasRel("viewer", Path("document", "docID"))},
				{name: "D3 Can(admin, Fixed(platform, main))", trips: 1, pred: Can("admin", Fixed("platform", "main"))},
				{name: "D4 Any(Can admin Fixed, Can manage_access Path(project))", params: []string{"projectID"}, trips: 1,
					pred: Any(Can("admin", Fixed("platform", "main")), Can("manage_access", Path("project", "projectID")))},
				{name: "D5 All(Can view project, Can view document)", params: []string{"projectID", "docID"}, trips: 1,
					pred: All(Can("view", Path("project", "projectID")), Can("view", Path("document", "docID")))},
			},
		},
		{
			fixture:    "usersets",
			principals: []sdk.Principal{parityUser("ann"), parityUser("bob"), parityUser("cid"), parityUser("nobody")},
			values:     map[string][]string{"docID": {"spec", "memo", ""}},
			policies: []parityPolicy{
				{name: "U1 Can(view, Path(document))", params: []string{"docID"}, trips: 1, pred: Can("view", Path("document", "docID"))},
				{name: "U2 HasRelationship(viewer, Path(document))", params: []string{"docID"}, trips: 1, pred: HasRel("viewer", Path("document", "docID"))},
				{name: "U3 HasRole(viewer, Path(document)) [exact vs expanded]", params: []string{"docID"}, trips: 1, pred: H("viewer", Path("document", "docID")), diverge9b: true},
			},
		},
	}
}

// parityFixture seeds f over a fresh memstore and returns the native guard and
// a counting decorator over the engine adapter.
func parityFixture(t *testing.T, name string) (authorizationtest.Fixture, *authorizationhttp.Adapter, *countingAuthorizer) {
	t.Helper()
	f, ok := authorizationtest.FixtureNamed(name)
	if !ok {
		t.Fatalf("no fixture %q", name)
	}
	components := storetest.NewAuthorizer(t, authorization.Repositories{Tuples: memory.New().Tuples()}, f)
	native, err := authorizationhttp.New(authorizationhttp.Services{Decisions: components.Decisions})
	if err != nil {
		t.Fatal(err)
	}
	return f, native, &countingAuthorizer{inner: components.Authorizer}
}

func loweredGuard(t *testing.T, f authorizationtest.Fixture, checker sdkauthorization.Checker) (*authorizationhttp.Adapter, *decisions.LoweredEvaluator) {
	t.Helper()
	var opts []decisions.LoweredOption
	if g := f.Global; g != nil {
		opts = append(opts, decisions.WithGlobalResource(g.Type, g.ID, g.RelationFor))
	}
	evaluator, err := decisions.NewLoweredEvaluator(checker, opts...)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := authorizationhttp.New(authorizationhttp.Services{Decisions: evaluator})
	if err != nil {
		t.Fatal(err)
	}
	return guard, evaluator
}

// TestLoweredPolicyParity is F1: every host policy shape, mounted on the real
// Require, gives the same status lowered over the engine adapter as the native
// EvaluateResolved, request by request; and (S1) a pure-input policy takes at
// most one protocol round trip per reached level.
func TestLoweredPolicyParity(t *testing.T) {
	evaluations := 0
	for _, sc := range parityScenarios() {
		t.Run(sc.fixture, func(t *testing.T) {
			f, native, port := parityFixture(t, sc.fixture)
			lowered, _ := loweredGuard(t, f, port)
			for _, pol := range sc.policies {
				t.Run(pol.name, func(t *testing.T) {
					maxTrips := 0
					for _, req := range parityRequests(sc.principals, sc.values, pol.params) {
						want := guardStatus(t, native, pol.pred, req)
						port.reset()
						got := guardStatus(t, lowered, pol.pred, req, authorizationhttp.WithLoweredPolicy())
						maxTrips = max(maxTrips, port.trips())
						evaluations++
						exact9b := pol.diverge9b && req.principal.ID == "ann" && req.values["docID"] == "spec"
						switch {
						case exact9b && (want != http.StatusForbidden || got != http.StatusOK):
							t.Errorf("%s: exact HasRole vs expanded relation divergence not observed: native %d, lowered %d", req, want, got)
						case !exact9b && got != want:
							t.Errorf("F1 mismatch %s: native %d, lowered %d", req, want, got)
						}
					}
					if maxTrips > pol.trips {
						t.Errorf("S1: %d round trips, want at most %d", maxTrips, pol.trips)
					}
				})
			}
		})
	}
	t.Logf("%d lowered evaluations compared with native", evaluations)
}

// TestLoweredShortCircuit pins what the native walk would never reach: an
// unreached input's error is discarded, a reached one surfaces.
func TestLoweredShortCircuit(t *testing.T) {
	f, native, port := parityFixture(t, "gps-global")
	lowered, _ := loweredGuard(t, f, port)
	H, G := authorizationhttp.HasRole, authorizationhttp.Global
	hubPath := authorizationhttp.Path("client_hub", "organizationId")
	hub := authorizationhttp.Resource("client_hub", programHub)
	g1 := authorizationhttp.Any(H("steward", G()), H("firmwide_clients", G()), H("viewer", hubPath), H("member", hubPath))
	g7 := authorizationhttp.Any(H("steward", G()), H("viewer", hub), H("member", hub))
	for _, c := range []struct {
		name string
		pred authorizationhttp.Predicate
		req  parityRequest
		want int
	}{
		{"empty Path behind a granting global role", g1, parityRequest{parityUser("stella"), map[string]string{"organizationId": ""}}, http.StatusOK},
		{"empty Path reached", g1, parityRequest{parityUser("vin"), map[string]string{"organizationId": ""}}, http.StatusInternalServerError},
		{"resolver error on an unreached branch", g7, parityRequest{parityUser("stella"), map[string]string{"?programId": "boom"}}, http.StatusOK},
		{"resolver error reached", g7, parityRequest{parityUser("mo"), map[string]string{"?programId": "boom"}}, http.StatusInternalServerError},
		{"alternative not applicable is false", g7, parityRequest{parityUser("mo"), map[string]string{"?programId": "missing"}}, http.StatusForbidden},
		{"inexpressible input denies", g1, parityRequest{parityUser("mo"), map[string]string{"organizationId": "org.dot"}}, http.StatusForbidden},
	} {
		if got := guardStatus(t, native, c.pred, c.req); got != c.want {
			t.Fatalf("native %s = %d, want %d", c.name, got, c.want)
		}
		if got := guardStatus(t, lowered, c.pred, c.req, authorizationhttp.WithLoweredPolicy()); got != c.want {
			t.Errorf("lowered %s = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestLoweredRequireOptIn(t *testing.T) {
	f, _, port := parityFixture(t, "documents")
	lowered, evaluator := loweredGuard(t, f, port)
	if !evaluator.LoweredPolicy() {
		t.Fatal("LoweredPolicy marker is false")
	}
	pred := authorizationhttp.Can("view", authorizationhttp.Path("document", "docID"))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Require mounted a lowered evaluator without WithLoweredPolicy")
			}
		}()
		lowered.Require(pred)
	}()
	lowered.Require(pred, authorizationhttp.WithLoweredPolicy())

	// A decorator that drops the marker is treated as native, which is why the
	// README tells decorators to forward it.
	type hidden struct {
		authorizationhttp.DecisionService
	}
	plain, err := authorizationhttp.New(authorizationhttp.Services{Decisions: hidden{evaluator}})
	if err != nil {
		t.Fatal(err)
	}
	plain.Require(pred)
}

func TestLoweredValidationAndRewrap(t *testing.T) {
	f, _, port := parityFixture(t, "documents")
	undeclared := decisions.BindResource("document", "resource_0", decisions.Permission("edit"))
	resolve := func(context.Context, string) (authmodel.Resource, error) {
		return authmodel.Resource{Type: "document", ID: authorizationtest.DocID(1)}, nil
	}
	alice := authmodel.PrincipalRef{Type: "user", ID: "alice"}

	_, withSchema := loweredGuard(t, f, port)
	for name, expr := range map[string]decisions.Expression{
		"undeclared permission":         undeclared,
		"Through":                       decisions.Any(decisions.Through("parent", "view")),
		"global role without resource":  decisions.Role("steward"),
		"hyphenated name":               decisions.On(authmodel.Resource{Type: "document", ID: "d1"}, decisions.Permission("can-view")),
		"fixed resource beyond grammar": decisions.On(authmodel.Resource{Type: "document", ID: "a.b"}, decisions.Permission("view")),
		"undeclared role relation":      decisions.RoleIn("janitor", authmodel.Resource{Type: "project", ID: "demo"}),
	} {
		if err := withSchema.ValidateExpression(expr); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: ValidateExpression = %v, want sdk.ErrInvalidInput", name, err)
		}
	}

	noSchema, err := decisions.NewLoweredEvaluator(bareChecker{port})
	if err != nil {
		t.Fatal(err)
	}
	if err := noSchema.ValidateExpression(undeclared); err != nil {
		t.Fatalf("without a SchemaReader the structure is valid: %v", err)
	}
	_, err = noSchema.EvaluateResolved(context.Background(), alice, undeclared, resolve)
	if !errors.Is(err, decisions.ErrLoweredPolicyMisconfigured) || errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("evaluation-time rejection = %v; want server-class, never sdk.ErrInvalidInput", err)
	}
	guard, err := authorizationhttp.New(authorizationhttp.Services{Decisions: noSchema})
	if err != nil {
		t.Fatal(err)
	}
	status := guardStatus(t, guard, authorizationhttp.Can("edit", authorizationhttp.Path("document", "docID")),
		parityRequest{parityUser("alice"), map[string]string{"docID": authorizationtest.DocID(1)}}, authorizationhttp.WithLoweredPolicy())
	if status != http.StatusInternalServerError {
		t.Fatalf("undeclared name at evaluation = %d, want 500", status)
	}
}

func TestLoweredUnavailableIs503(t *testing.T) {
	f, _, port := parityFixture(t, "documents")
	port.fail = fmt.Errorf("vendor deadline: %w", sdk.ErrUnavailable)
	guard, _ := loweredGuard(t, f, port)
	status := guardStatus(t, guard, authorizationhttp.Can("view", authorizationhttp.Path("document", "docID")),
		parityRequest{parityUser("alice"), map[string]string{"docID": authorizationtest.DocID(1)}}, authorizationhttp.WithLoweredPolicy())
	if status != http.StatusServiceUnavailable {
		t.Fatalf("sdk.ErrUnavailable = %d, want 503", status)
	}
}

// A global role lowers to its relation, never to the permission of the same
// name: dev holds the developer permission but not the steward role.
func TestLoweredGlobalRoleUsesRelation(t *testing.T) {
	f, _, port := parityFixture(t, "gps-global")
	guard, _ := loweredGuard(t, f, port)
	pred := authorizationhttp.HasRole("developer", authorizationhttp.Global())
	if got := guardStatus(t, guard, pred, parityRequest{parityUser("stella"), nil}, authorizationhttp.WithLoweredPolicy()); got != http.StatusForbidden {
		t.Fatalf("steward through HasRole(developer, Global()) = %d, want 403 (the permission developer would allow)", got)
	}
	if got := guardStatus(t, guard, pred, parityRequest{parityUser("dev"), nil}, authorizationhttp.WithLoweredPolicy()); got != http.StatusOK {
		t.Fatalf("developer = %d, want 200", got)
	}
}
