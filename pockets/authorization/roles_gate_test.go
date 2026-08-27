package authorization

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
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
func gpsRoleModel() RoleModel {
	return RoleModel{
		ResourceTypes: map[string]RoleTypeDef{
			"platform": {Roles: []string{"steward", "developer"},
				Permissions: map[string][]string{
					"steward": {"steward"}, "developer": {"steward", "developer"},
					"delete": {"steward"}, "partnership_financials": {"steward"}, "changelog_viewer": {"steward"},
				}},
			"organization": {Roles: []string{"viewer", "contributor", "report_editor", "report_publisher", "steward"},
				Permissions: map[string][]string{
					"view":           {"viewer", "contributor", "report_editor", "report_publisher", "steward"},
					"contribute":     {"contributor", "steward"},
					"report_edit":    {"report_editor", "report_publisher", "steward"},
					"report_publish": {"report_publisher", "steward"},
				}},
			"section": {Roles: []string{"member", "steward"}, Permissions: map[string][]string{"enter": {"member", "steward"}}},
			"page":    {Roles: []string{"viewer", "steward"}, Permissions: map[string][]string{"view": {"viewer", "steward"}}},
		},
	}
}

// newGPSHost builds the roles-only host the way gps-360-go wires it: the roles
// kind and the atomic mutation repository, no relationship kind, the D1 model as
// Config.RoleModel. model is passed so the negative half can boot the SAME
// assignments under a model with one grantor removed.
func newGPSHost(t *testing.T, store *memstore.Store, model RoleModel) Components {
	t.Helper()
	comps, err := NewService(Repositories{Roles: store.Roles(), Mutations: store.Mutations()}, Config{RoleModel: model})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// assignGPSRole seeds one assignment through the TRUSTED mutation seam with a
// derived, stable MutationID — the bootstrap path a host actually uses, and the
// path D8's assign-time model validation runs on.
func assignGPSRole(t *testing.T, mutator *SystemMutator, subjectType, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	receipt, err := mutator.AssignRole(context.Background(), AssignRoleCommand{
		MutationID:   DeriveMutationID("roles-gate-test", roleName, subjectType, subjectID, resourceType, resourceID),
		Subject:      PrincipalRef{Type: subjectType, ID: subjectID},
		Role:         roleName,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
	if err != nil {
		t.Fatalf("AssignRole(%s %s:%s on %s/%s): %v", roleName, subjectType, subjectID, resourceType, resourceID, err)
	}
	if receipt.Outcome != OutcomeApplied {
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
				r = r.WithContext(identity.WithPrincipal(r.Context(), identity.Principal{Type: parts[0], ID: parts[1]}))
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
	store := memstore.New()
	comps := newGPSHost(t, store, gpsRoleModel())
	svc := comps.Service

	assignGPSRole(t, comps.SystemMutator, "user", "member", "viewer", "organization", "org-1")
	assignGPSRole(t, comps.SystemMutator, "user", "member", "report_editor", "organization", "org-1") // several roles on one subject is normal
	assignGPSRole(t, comps.SystemMutator, "service_account", "sa-1", "viewer", "organization", "org-1")
	assignGPSRole(t, comps.SystemMutator, "user", "boss", "steward", "", "") // platform roles are global

	router := web.NewWebHandler()
	group := router.Group("/api/v1", injectTestPrincipal)
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	// One route per D1 resource type, each mounted in coordinates.
	group.Handle(http.MethodGet, "/orgs/{id}", noContent, svc.RequirePermissionOn("organization", "view", "id"))
	group.Handle(http.MethodGet, "/orgs/{id}/publish", noContent, svc.RequirePermissionOn("organization", "report_publish", "id"))
	group.Handle(http.MethodGet, "/sections/{id}", noContent, svc.RequirePermissionOn("section", "enter", "id"))
	group.Handle(http.MethodGet, "/pages/{id}", noContent, svc.RequirePermissionOn("page", "view", "id"))
	group.Handle(http.MethodGet, "/platform", noContent, svc.RequirePermissionFixed("platform", "steward", "global"))

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
		member, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "member"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupResources(member): %v", err)
		}
		if member.Unrestricted || len(member.IDs) != 1 || member.IDs[0] != "org-1" {
			t.Fatalf("member ids = %+v, want [org-1] and not unrestricted", member)
		}
		steward, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "boss"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupResources(steward): %v", err)
		}
		if !steward.Unrestricted || len(steward.IDs) != 0 {
			t.Fatalf("steward ids = %+v, want unrestricted with no IDs (the host must skip ID filtering)", steward)
		}
		nobody, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "nobody"}, "view", "organization")
		if err != nil {
			t.Fatalf("LookupResources(nobody): %v", err)
		}
		if nobody.Unrestricted || nobody.IDs == nil || len(nobody.IDs) != 0 {
			t.Fatalf("nobody ids = %+v, want empty non-nil IDs and never unrestricted", nobody)
		}
	})

	t.Run("explain names the global role grant", func(t *testing.T) {
		res, explanation, err := svc.CheckExplain(context.Background(), CheckRequest{
			Principal:  PrincipalRef{Type: "user", ID: "boss"},
			Permission: "view",
			Resource:   Resource{Type: "organization", ID: "org-9"},
		})
		if err != nil || !res.Allowed {
			t.Fatalf("CheckExplain(steward): res=%+v err=%v", res, err)
		}
		if res.Reason != "role:steward@global" {
			t.Fatalf("reason = %q, want role:steward@global", res.Reason)
		}
		if explanation.Decision != res.ReasonCode {
			t.Fatalf("explanation decision = %q, want the final reason code %q", explanation.Decision, res.ReasonCode)
		}
		var granting *ExplainStep
		for i := range explanation.Steps {
			if explanation.Steps[i].Role == "steward" {
				granting = &explanation.Steps[i]
			}
		}
		if granting == nil {
			t.Fatalf("no steward step in the trace: %+v", explanation.Steps)
		}
		if granting.Kind != ExplainKindRole || granting.Scope != ExplainScopeGlobal {
			t.Fatalf("steward step = %+v, want kind %q scope %q", *granting, ExplainKindRole, ExplainScopeGlobal)
		}
	})

	t.Run("an undeclared pair panics at mount", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatalf("a pair the model never declares must panic at registration, not 500 per request")
			}
		}()
		_ = svc.RequirePermissionOn("organization", "fly", "id")
	})
}

// TestRoleGatesRefuseNonsenseAtBoot: a pair no model declares, an unknown
// resource type, or a nameless coordinate is a REGISTRATION bug — it panics when
// the route is mounted, never when a request arrives.
func TestRoleGatesRefuseNonsenseAtBoot(t *testing.T) {
	comps := newGPSHost(t, memstore.New(), gpsRoleModel())
	svc := comps.Service

	for name, mount := range map[string]func(){
		"undeclared pair":  func() { _ = svc.RequirePermissionOn("organization", "steward", "id") },
		"unknown resource": func() { _ = svc.RequirePermissionFixed("galaxy", "view", "x") },
		"empty parameter":  func() { _ = svc.RequirePermissionOn("organization", "view", "") },
		"empty fixed id":   func() { _ = svc.RequirePermissionFixed("platform", "steward", "") },
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
	store := memstore.New()
	full := newGPSHost(t, store, gpsRoleModel())
	assignGPSRole(t, full.SystemMutator, "user", "boss", "steward", "", "")

	narrowed := gpsRoleModel()
	organization := narrowed.ResourceTypes["organization"]
	organization.Permissions["report_publish"] = []string{"report_publisher"}
	narrowed.ResourceTypes["organization"] = organization
	svc := newGPSHost(t, store, narrowed).Service

	ctx := context.Background()
	boss := PrincipalRef{Type: "user", ID: "boss"}
	publish := CheckRequest{Principal: boss, Permission: "report_publish", Resource: Resource{Type: "organization", ID: "org-1"}}
	if res, err := svc.Check(ctx, publish); err != nil || res.Allowed {
		t.Fatalf("report_publish without steward in its grantors: res=%+v err=%v, want denied", res, err)
	}
	look, err := svc.LookupResources(ctx, boss, "report_publish", "organization")
	if err != nil {
		t.Fatalf("LookupResources(report_publish): %v", err)
	}
	if look.Unrestricted || look.IDs == nil || len(look.IDs) != 0 {
		t.Fatalf("report_publish ids = %+v, want empty non-nil IDs and NOT unrestricted", look)
	}

	// Every permission that still names steward is unaffected.
	view := CheckRequest{Principal: boss, Permission: "view", Resource: Resource{Type: "organization", ID: "org-1"}}
	if res, err := svc.Check(ctx, view); err != nil || !res.Allowed || res.Reason != "role:steward@global" {
		t.Fatalf("view under the narrowed model: res=%+v err=%v, want allowed via role:steward@global", res, err)
	}
	if look, err := svc.LookupResources(ctx, boss, "view", "organization"); err != nil || !look.Unrestricted {
		t.Fatalf("view ids = %+v err=%v, want unrestricted", look, err)
	}
}

// TestAssigningAnUndeclaredRoleIsLoud is the D8 trap the model closes: a typo'd
// role name is refused at assign time on the trusted seam instead of becoming a
// permanently silent no-grant.
func TestAssigningAnUndeclaredRoleIsLoud(t *testing.T) {
	comps := newGPSHost(t, memstore.New(), gpsRoleModel())

	_, err := comps.SystemMutator.AssignRole(context.Background(), AssignRoleCommand{
		MutationID:   DeriveMutationID("roles-gate-test", "typo", "user", "member", "organization", "org-1"),
		Subject:      PrincipalRef{Type: "user", ID: "member"},
		Role:         "vewer",
		ResourceType: "organization",
		ResourceID:   "org-1",
	})
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("assigning a role the model does not declare: got %v, want ErrInvalidRoleModel", err)
	}
	if !strings.Contains(err.Error(), "vewer") {
		t.Fatalf("the message must name the offending role, got %v", err)
	}
}
