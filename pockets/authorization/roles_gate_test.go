package authorization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// testPrincipalHeader carries "<type>:<id>" for the gate proof's stand-in
// authentication middleware; absent, the request reaches the gate with no
// principal at all.
const testPrincipalHeader = "X-Test-Principal"

// gpsRoleModel is the gps-360-go product model (plan D1), transcribed by
// effective behaviour: every role and permission that host declares today, with
// its globally assigned steward listed EXPLICITLY on each permission it should
// grant. Nothing here is a bypass — delete steward from one permission's grantor
// list and it stops granting that one permission (proved below).
func gpsRoleModel() decisions.Model {
	return decisions.Model{
		ResourceTypes: map[string]decisions.ResourceTypeDef{
			"platform": {
				Permissions: map[string]decisions.Expression{
					"steward": decisions.Any(decisions.RoleIn("steward"), decisions.Role("steward")), "developer": decisions.Any(decisions.RoleIn("steward"), decisions.Role("steward"), decisions.RoleIn("developer"), decisions.Role("developer")),
					"delete": decisions.Any(decisions.RoleIn("steward"), decisions.Role("steward")), "partnership_financials": decisions.Any(decisions.RoleIn("steward"), decisions.Role("steward")), "changelog_viewer": decisions.Any(decisions.RoleIn("steward"), decisions.Role("steward")),
				}},
			"organization": {
				Permissions: map[string]decisions.Expression{
					"view":           decisions.Any(decisions.RoleIn("viewer"), decisions.Role("viewer"), decisions.RoleIn("contributor"), decisions.Role("contributor"), decisions.RoleIn("report_editor"), decisions.Role("report_editor"), decisions.RoleIn("report_publisher"), decisions.Role("report_publisher"), decisions.RoleIn("steward"), decisions.Role("steward")),
					"contribute":     decisions.Any(decisions.RoleIn("contributor"), decisions.Role("contributor"), decisions.RoleIn("steward"), decisions.Role("steward")),
					"report_edit":    decisions.Any(decisions.RoleIn("report_editor"), decisions.Role("report_editor"), decisions.RoleIn("report_publisher"), decisions.Role("report_publisher"), decisions.RoleIn("steward"), decisions.Role("steward")),
					"report_publish": decisions.Any(decisions.RoleIn("report_publisher"), decisions.Role("report_publisher"), decisions.RoleIn("steward"), decisions.Role("steward")),
				}},
			"section": {Permissions: map[string]decisions.Expression{"enter": decisions.Any(decisions.RoleIn("member"), decisions.Role("member"), decisions.RoleIn("steward"), decisions.Role("steward"))}},
			"page":    {Permissions: map[string]decisions.Expression{"view": decisions.Any(decisions.RoleIn("viewer"), decisions.Role("viewer"), decisions.RoleIn("steward"), decisions.Role("steward"))}},
		},
	}
}

// newGPSHost builds the roles-only host the way gps-360-go wires it: the roles
// kind and the atomic mutation repository, no relationship kind, the D1 model as
// WithRoleModel. model is passed so the negative half can boot the SAME
// assignments under a model with one grantor removed.
func newGPSHost(t *testing.T, store *memory.Store, model decisions.Model) Components {
	t.Helper()
	comps, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, WithModel(model))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func assignGPSRole(t *testing.T, mutator *mutations.Service, subjectType, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	receipt, err := mutator.AssignRole(context.Background(), mutations.AssignRoleCommand{

		Subject: authmodel.PrincipalRef{Type: subjectType, ID: subjectID},
		Role:    roleName, Scope: fixtureScope(resourceType,
			resourceID),
	})
	if err != nil {
		t.Fatalf("AssignRole(%s %s:%s on %s/%s): %v", roleName, subjectType, subjectID, resourceType, resourceID, err)
	}
	if receipt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("AssignRole outcome = %q, want applied", receipt.Outcome)
	}
}

// injectTestPrincipal stands in for the host's authentication middleware: it
// puts the header's principal in the request context and otherwise leaves the
// request principal-less, so the gate's own 401 is reachable.
func injectTestPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.Header.Get(testPrincipalHeader); raw != "" {
			if parts := strings.SplitN(raw, ":", 2); len(parts) == 2 {
				r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: parts[0], ID: parts[1]}))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// TestRoleGatesWithRealEngine is the real-engine proof: the gps-360-go host
// shape — a roles-only host, its D1 model, assignments made through the trusted
// mutator — served through web.NewWebHandler and driven with httptest. No fake
// engine, no stub decision: every status below is the composite dispatching to
// the role engine over a live role store.
func TestRoleGatesWithRealEngine(t *testing.T) {
	store := memory.New()
	comps := newGPSHost(t, store, gpsRoleModel())
	svc := comps

	assignGPSRole(t, comps.Mutations, "user", "member", "viewer", "organization", "org-1")
	assignGPSRole(t, comps.Mutations, "user", "member", "report_editor", "organization", "org-1") // several roles on one subject is normal
	assignGPSRole(t, comps.Mutations, "service_account", "sa-1", "viewer", "organization", "org-1")
	assignGPSRole(t, comps.Mutations, "user", "boss", "steward", "", "") // platform roles are global

	router := web.NewWebHandler()
	group := router.Group("/api/v1", injectTestPrincipal)
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	// One route per D1 resource type, each mounted in coordinates.
	group.Handle(http.MethodGet, "/orgs/{id}", noContent, svc.HTTP.Require(authorizationhttp.Can("view", authorizationhttp.Path("organization", "id"))))
	group.Handle(http.MethodGet, "/orgs/{id}/publish", noContent, svc.HTTP.Require(authorizationhttp.Can("report_publish", authorizationhttp.Path("organization", "id"))))
	group.Handle(http.MethodGet, "/sections/{id}", noContent, svc.HTTP.Require(authorizationhttp.Can("enter", authorizationhttp.Path("section", "id"))))
	group.Handle(http.MethodGet, "/pages/{id}", noContent, svc.HTTP.Require(authorizationhttp.Can("view", authorizationhttp.Path("page", "id"))))
	group.Handle(http.MethodGet, "/platform", noContent, svc.HTTP.Require(authorizationhttp.Can("steward", authorizationhttp.Fixed("platform", "global"))))

	do := func(principal, path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if principal != "" {
			req.Header.Set(testPrincipalHeader, principal)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, tc := range []struct {
		name, principal, path string
		want                  int
	}{
		{"no principal is unauthenticated", "", "/api/v1/orgs/org-1", http.StatusUnauthorized},
		{"member on its own org", "user:member", "/api/v1/orgs/org-1", http.StatusNoContent},
		{"member on another org", "user:member", "/api/v1/orgs/org-2", http.StatusForbidden},
		{"member on an absent org", "user:member", "/api/v1/orgs/absent", http.StatusForbidden},
		{"member holds report_edit, not report_publish", "user:member", "/api/v1/orgs/org-1/publish", http.StatusForbidden},
		{"member on the platform gate", "user:member", "/api/v1/platform", http.StatusForbidden},
		{"member on another type", "user:member", "/api/v1/sections/s-1", http.StatusForbidden},
		{"service account on its own org", "service_account:sa-1", "/api/v1/orgs/org-1", http.StatusNoContent},
		{"service account on another org", "service_account:sa-1", "/api/v1/orgs/org-2", http.StatusForbidden},
		{"service account on the platform gate", "service_account:sa-1", "/api/v1/platform", http.StatusForbidden},
		{"unknown principal", "user:nobody", "/api/v1/orgs/org-1", http.StatusForbidden},
		// The global steward passes every gate the model names it on — because it
		// is listed on each of those permissions, not because a role bypasses.
		{"steward on the platform gate", "user:boss", "/api/v1/platform", http.StatusNoContent},
		{"steward on any org", "user:boss", "/api/v1/orgs/absent", http.StatusNoContent},
		{"steward publishing a report", "user:boss", "/api/v1/orgs/org-1/publish", http.StatusNoContent},
		{"steward entering a section", "user:boss", "/api/v1/sections/s-1", http.StatusNoContent},
		{"steward viewing a page", "user:boss", "/api/v1/pages/pg-1", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(tc.principal, tc.path); got != tc.want {
				t.Fatalf("%s %s = %d, want %d", tc.principal, tc.path, got, tc.want)
			}
		})
	}

	t.Run("enumeration matches the gates", func(t *testing.T) {
		ctx := context.Background()
		member, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "member"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupAllResourceIDs(member): %v", err)
		}
		if member.Unrestricted || len(member.IDs) != 1 || member.IDs[0] != "org-1" {
			t.Fatalf("member ids = %+v, want [org-1] and not unrestricted", member)
		}
		steward, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "boss"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupAllResourceIDs(steward): %v", err)
		}
		if !steward.Unrestricted || len(steward.IDs) != 0 {
			t.Fatalf("steward ids = %+v, want unrestricted with no IDs (the host must skip ID filtering)", steward)
		}
		nobody, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "nobody"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupAllResourceIDs(nobody): %v", err)
		}
		if nobody.Unrestricted || nobody.IDs == nil || len(nobody.IDs) != 0 {
			t.Fatalf("nobody ids = %+v, want empty non-nil IDs and never unrestricted", nobody)
		}
	})

	t.Run("explain names the global role grant", func(t *testing.T) {
		res, explanation, err := svc.Decisions.CheckExplain(context.Background(), authmodel.CheckRequest{
			Principal:  authmodel.PrincipalRef{Type: "user", ID: "boss"},
			Permission: "view",
			Resource:   authmodel.Resource{Type: "organization", ID: "org-9"},
		})
		if err != nil || !res.Allowed {
			t.Fatalf("CheckExplain(steward): res=%+v err=%v", res, err)
		}
		if res.ReasonCode != authmodel.ReasonGranted {
			t.Fatalf("reason = %q, want role:steward@global", res.Reason)
		}
		if explanation.Decision != res.ReasonCode {
			t.Fatalf("explanation decision = %q, want the final reason code %q", explanation.Decision, res.ReasonCode)
		}
		var granting *authmodel.ExplainStep
		for i := range explanation.Steps {
			if explanation.Steps[i].Relation == "steward" && explanation.Steps[i].Outcome == authmodel.ReasonGranted {
				granting = &explanation.Steps[i]
			}
		}
		if granting == nil {
			t.Fatalf("no steward step in the trace: %+v", explanation.Steps)
		}
		if granting.Kind != authmodel.ExplainKindExact || granting.Scope != tuples.Global() || granting.ResourceType != "" || granting.ResourceID != "" {
			t.Fatalf("steward step = %+v, want exact global scope", *granting)
		}
	})

	t.Run("an undeclared pair panics at mount", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatalf("a pair the model never declares must panic at registration, not 500 per request")
			}
		}()
		_ = svc.HTTP.Require(authorizationhttp.Can("fly", authorizationhttp.Path("organization", "id")))
	})
}

// TestRoleGatesRefuseNonsenseAtBoot: a pair no model declares, an unknown
// resource type, or a nameless coordinate is a REGISTRATION bug — it panics when
// the route is mounted, never when a request arrives.
func TestRoleGatesRefuseNonsenseAtBoot(t *testing.T) {
	comps := newGPSHost(t, memory.New(), gpsRoleModel())
	svc := comps

	for name, mount := range map[string]func(){
		"undeclared pair": func() {
			_ = svc.HTTP.Require(authorizationhttp.Can("steward", authorizationhttp.Path("organization", "id")))
		},
		"unknown resource": func() { _ = svc.HTTP.Require(authorizationhttp.Can("view", authorizationhttp.Fixed("galaxy", "x"))) },
		"empty parameter": func() {
			_ = svc.HTTP.Require(authorizationhttp.Can("view", authorizationhttp.Path("organization", "")))
		},
		"empty fixed id": func() {
			_ = svc.HTTP.Require(authorizationhttp.Can("steward", authorizationhttp.Fixed("platform", "")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s must panic at registration", name)
				}
			}()
			mount()
		})
	}
}

// TestStewardGrantsOnlyWhatTheModelNames is the negative half of the proof: the
// SAME store and the SAME global steward assignment, under a model with steward
// deleted from ONE permission's grantor list, stop granting exactly that
// permission — and its enumeration returns no IDs rather than Unrestricted. This
// is what "a globally held role is data, not a bypass" means operationally.
func TestStewardGrantsOnlyWhatTheModelNames(t *testing.T) {
	store := memory.New()
	full := newGPSHost(t, store, gpsRoleModel())
	assignGPSRole(t, full.Mutations, "user", "boss", "steward", "", "")

	narrowed := gpsRoleModel()
	organization := narrowed.ResourceTypes["organization"]
	organization.Permissions["report_publish"] = decisions.Any(decisions.RoleIn("report_publisher"), decisions.Role("report_publisher"))
	narrowed.ResourceTypes["organization"] = organization
	svc := newGPSHost(t, store, narrowed)

	ctx := context.Background()
	boss := authmodel.PrincipalRef{Type: "user", ID: "boss"}
	publish := authmodel.CheckRequest{Principal: boss, Permission: "report_publish", Resource: authmodel.Resource{Type: "organization", ID: "org-1"}}
	if res, err := svc.Decisions.Check(ctx, publish); err != nil || res.Allowed {
		t.Fatalf("report_publish without steward in its grantors: res=%+v err=%v, want denied", res, err)
	}
	look, err := svc.Decisions.LookupAllResourceIDs(ctx, boss, "report_publish", "organization")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs(report_publish): %v", err)
	}
	if look.Unrestricted || look.IDs == nil || len(look.IDs) != 0 {
		t.Fatalf("report_publish ids = %+v, want empty non-nil IDs and NOT unrestricted", look)
	}

	// Every permission that still names steward is unaffected.
	view := authmodel.CheckRequest{Principal: boss, Permission: "view", Resource: authmodel.Resource{Type: "organization", ID: "org-1"}}
	if res, err := svc.Decisions.Check(ctx, view); err != nil || !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
		t.Fatalf("view under the narrowed model: res=%+v err=%v, want allowed via role:steward@global", res, err)
	}
	if look, err := svc.Decisions.LookupAllResourceIDs(ctx, boss, "view", "organization"); err != nil || !look.Unrestricted {
		t.Fatalf("view ids = %+v err=%v, want unrestricted", look, err)
	}
}

func TestOpaqueRoleDoesNotGrantNamedPermission(t *testing.T) {
	comps := newGPSHost(t, memory.New(), gpsRoleModel())
	ctx := context.Background()
	_, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{Subject: prinU("member"), Role: "vewer", Scope: tuples.On("organization", "org-1")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := comps.Decisions.Check(ctx, authmodel.CheckRequest{Principal: prinU("member"), Permission: "view", Resource: authmodel.Resource{Type: "organization", ID: "org-1"}})
	if err != nil || result.Allowed {
		t.Fatalf("opaque label implied permission: %+v, %v", result, err)
	}
}
