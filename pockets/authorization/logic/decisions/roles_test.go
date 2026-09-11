package decisions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// errProbe is a roleProbe that fails every call — the fail-closed proof that a
// store error is never an allow.
type errProbe struct{ err error }

func (p errProbe) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, p.err
}

func (p errProbe) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, p.err
}

func (p errProbe) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, p.err
}

// smallPages forces a tiny page size onto the listing so the engine's
// cursor-following walk is exercised over several pages.
type smallPages struct {
	inner roleProbe
	limit int
	pages int
}

func (p *smallPages) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return p.inner.HasExactRole(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
}

func (p *smallPages) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	p.pages++
	req.Limit = p.limit
	return p.inner.ListRoleAssignmentsBySubject(ctx, principal, req)
}

func (p *smallPages) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return p.inner.LookupResourceIDsBySubjectAndRoles(ctx, subjectType, subjectID, resourceType, roles, after, limit)
}

// stallProbe answers every listing with an EMPTY page that claims HasMore — the
// adversarial store shape (no rows, so no MaxGraphStates charge, and a cursor
// that cannot advance) a cursor-following walk must not spin on.
type stallProbe struct{ calls int }

func (p *stallProbe) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, nil
}

func (p *stallProbe) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	p.calls++
	return list.Page[roles.Assignment]{Items: nil, HasMore: true, NextCursor: "stuck"}, nil
}

func (p *stallProbe) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, nil
}

// rawRows answers every listing with the same single page of rows, so a raw-port
// row shape the typed mutation paths cannot produce can still be presented to
// the engine.
type rawRows struct{ items []roles.Assignment }

func (p *rawRows) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, nil
}

func (p *rawRows) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{Items: p.items}, nil
}

func (p *rawRows) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, nil
}

// orgModel: organization/view is granted by three roles, organization/contribute
// by two. Grantor order is deliberately unsorted at the source.
func orgModel() authmodel.RoleModel {
	return authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"organization": {
			Roles: []string{"viewer", "contributor", "steward"},
			Permissions: map[string][]string{
				"view":       {"viewer", "contributor", "steward"},
				"contribute": {"contributor", "steward"},
			},
		},
	}}
}

func resolvedLimits(t *testing.T, in authmodel.EvaluationLimits) authmodel.EvaluationLimits {
	t.Helper()
	out, err := in.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return out
}

// newTestEngine builds the role engine over the in-core role store and a REAL
// roles.Service, so the Q5 scope rule under test is the shipped one.
func newTestEngine(t *testing.T, model authmodel.RoleModel, limits authmodel.EvaluationLimits) (*roleEngine, *testRoles) {
	t.Helper()
	svc := newTestRoles(t, memory.NewRoles())
	return newRoleEngine(svc, mustCompile(t, model, nil), resolvedLimits(t, limits)), svc
}

func assign(t *testing.T, svc *testRoles, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	if err := svc.AssignRole(context.Background(), "user", subjectID, roleName, resourceType, resourceID); err != nil {
		t.Fatalf("AssignRole(%s, %s, %s/%s): %v", subjectID, roleName, resourceType, resourceID, err)
	}
}

func orgRequest(subjectID, permission, orgID string) authmodel.CheckRequest {
	return authmodel.CheckRequest{
		Principal:  authmodel.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authmodel.Resource{Type: "organization", ID: orgID},
	}
}

// =============================================================================
// Check
// =============================================================================

func TestRoleEngineCheckDirectGrant(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "organization", "org-1")

	res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
		t.Fatalf("want granted, got %+v", res)
	}
	if res.Reason != "role:viewer@direct" {
		t.Fatalf("Reason = %q, want role:viewer@direct", res.Reason)
	}

	// A scoped assignment never satisfies a DIFFERENT scope.
	res, err = engine.Check(context.Background(), orgRequest("u1", "view", "org-2"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed || res.Reason != "no matching role" {
		t.Fatalf("want deny on another org, got %+v", res)
	}
}

func TestRoleEngineCheckGlobalGrantSatisfiesScopedCheck(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "steward", "", "")

	res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed || res.Reason != "role:steward@global" {
		t.Fatalf("want role:steward@global, got %+v", res)
	}
}

func TestRoleEngineCheckHeldAtBothScopesReportsDirect(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "steward", "", "")
	assign(t, svc, "u1", "steward", "organization", "org-1")

	res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed || res.Reason != "role:steward@direct" {
		t.Fatalf("held at both scopes must report direct, got %+v", res)
	}
}

func TestRoleEngineCheckSeveralRolesPerSubject(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	// Only the LAST sorted grantor is held, so every earlier probe must miss and
	// the walk must not short-circuit into a deny.
	assign(t, svc, "u1", "viewer", "organization", "org-1")
	assign(t, svc, "u1", "contributor", "organization", "org-2")

	res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed || res.Reason != "role:viewer@direct" {
		t.Fatalf("want role:viewer@direct, got %+v", res)
	}

	// viewer does not grant contribute; contributor does, on org-2 only.
	res, err = engine.Check(context.Background(), orgRequest("u1", "contribute", "org-1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed {
		t.Fatalf("viewer must not grant contribute, got %+v", res)
	}
	res, err = engine.Check(context.Background(), orgRequest("u1", "contribute", "org-2"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed || res.Reason != "role:contributor@direct" {
		t.Fatalf("want role:contributor@direct, got %+v", res)
	}
}

func TestRoleEngineCheckUndeclaredPairDenies(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "steward", "", "")

	for _, req := range []authmodel.CheckRequest{
		orgRequest("u1", "delete", "org-1"),
		{
			Principal:  authmodel.PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   authmodel.Resource{Type: "unknown", ID: "x"},
		},
	} {
		res, err := engine.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Allowed || res.ReasonCode != authmodel.ReasonDenied || res.Reason != "no rules defined" {
			t.Fatalf("undeclared pair must deny with \"no rules defined\", got %+v", res)
		}
	}
}

func TestRoleEngineCheckRejectsMalformedRequest(t *testing.T) {
	engine, _ := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	res, err := engine.Check(context.Background(), orgRequest("", "view", "org-1"))
	if err == nil {
		t.Fatal("want a validation error for an empty principal id")
	}
	if res.Allowed {
		t.Fatalf("a malformed request must never allow, got %+v", res)
	}
}

func TestRoleEngineCheckStoreErrorIsNeverAnAllow(t *testing.T) {
	boom := errors.New("store is down")
	engine := newRoleEngine(errProbe{err: boom}, mustCompile(t, orgModel(), nil),
		resolvedLimits(t, authmodel.EvaluationLimits{}))

	res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
	if !errors.Is(err, boom) {
		t.Fatalf("want the store error, got %v", err)
	}
	if res.Allowed || res != (authmodel.CheckResult{}) {
		t.Fatalf("a store error must return the zero result, got %+v", res)
	}

	if _, err := engine.CheckBatch(context.Background(), []authmodel.CheckRequest{orgRequest("u1", "view", "org-1")}); !errors.Is(err, boom) {
		t.Fatalf("CheckBatch: want the store error, got %v", err)
	}
	if _, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization"); !errors.Is(err, boom) {
		t.Fatalf("LookupResources: want the store error, got %v", err)
	}
}

func TestRoleEngineCheckReasonIsDeterministicAcrossSourceOrder(t *testing.T) {
	// The host may type its grantors in any order; the compiled model sorts them,
	// so the SAME role always wins and the debug Reason never flaps.
	orders := [][]string{
		{"viewer", "contributor", "steward"},
		{"steward", "viewer", "contributor"},
		{"contributor", "steward", "viewer"},
		{"steward", "contributor", "viewer"},
	}
	for i, order := range orders {
		model := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
			"organization": {
				Roles:       []string{"viewer", "contributor", "steward"},
				Permissions: map[string][]string{"view": order, "contribute": {"contributor", "steward"}},
			},
		}}
		svc := newTestRoles(t, memory.NewRoles())
		engine := newRoleEngine(svc, mustCompile(t, model, nil), resolvedLimits(t, authmodel.EvaluationLimits{}))
		// steward and viewer both grant view; sorted order probes steward first.
		assign(t, svc, "u1", "steward", "organization", "org-1")
		assign(t, svc, "u1", "viewer", "organization", "org-1")

		res, err := engine.Check(context.Background(), orgRequest("u1", "view", "org-1"))
		if err != nil {
			t.Fatalf("order %d: Check: %v", i, err)
		}
		if res.Reason != "role:steward@direct" {
			t.Fatalf("order %d: Reason = %q, want role:steward@direct", i, res.Reason)
		}
	}
}

// =============================================================================
// CheckBatch
// =============================================================================

func TestRoleEngineCheckBatch(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "organization", "org-1")

	if results, err := engine.CheckBatch(context.Background(), nil); err != nil || results != nil {
		t.Fatalf("an empty batch is (nil, nil), got (%v, %v)", results, err)
	}

	reqs := []authmodel.CheckRequest{
		orgRequest("u1", "view", "org-1"),
		orgRequest("u1", "view", "org-2"),
		orgRequest("u1", "delete", "org-1"),
	}
	results, err := engine.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if !results[0].Allowed || results[1].Allowed || results[2].Allowed {
		t.Fatalf("results out of order or wrong: %+v", results)
	}
	if results[2].Reason != "no rules defined" {
		t.Fatalf("results[2].Reason = %q", results[2].Reason)
	}
}

// =============================================================================
// CheckExplain
// =============================================================================

func TestRoleEngineCheckExplainStepsPerProbe(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "steward", "", "")

	res, explanation, err := engine.CheckExplain(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("want granted, got %+v", res)
	}
	if explanation.Decision != authmodel.ReasonGranted {
		t.Fatalf("Decision = %q, want %q", explanation.Decision, authmodel.ReasonGranted)
	}
	// Sorted grantors are contributor, steward, viewer: the walk stops at steward.
	if len(explanation.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d (%+v)", len(explanation.Steps), explanation.Steps)
	}
	first, granting := explanation.Steps[0], explanation.Steps[1]
	if first.Kind != authmodel.ExplainKindRole || first.Role != "contributor" ||
		first.Scope != "" || first.Outcome != authmodel.ReasonDenied {
		t.Fatalf("first step = %+v", first)
	}
	if granting.Kind != authmodel.ExplainKindRole || granting.Role != "steward" ||
		granting.Scope != authmodel.ExplainScopeGlobal || granting.Outcome != authmodel.ReasonGranted {
		t.Fatalf("granting step = %+v", granting)
	}
	for _, step := range explanation.Steps {
		if step.ResourceType != "organization" || step.ResourceID != "org-1" || step.Permission != "view" {
			t.Fatalf("step must carry the request coordinates: %+v", step)
		}
		if step.Depth != 0 || step.Relation != "" {
			t.Fatalf("a role step takes no traversal hop: %+v", step)
		}
	}
}

func TestRoleEngineCheckExplainDeniedAndUndeclared(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "organization", "org-2")

	_, explanation, err := engine.CheckExplain(context.Background(), orgRequest("u1", "view", "org-1"))
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if explanation.Decision != authmodel.ReasonDenied {
		t.Fatalf("Decision = %q", explanation.Decision)
	}
	if len(explanation.Steps) != 3 {
		t.Fatalf("a full deny probes every grantor: got %d steps", len(explanation.Steps))
	}
	for _, step := range explanation.Steps {
		if step.Scope != "" || step.Outcome != authmodel.ReasonDenied {
			t.Fatalf("step = %+v", step)
		}
	}

	_, explanation, err = engine.CheckExplain(context.Background(), orgRequest("u1", "delete", "org-1"))
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if len(explanation.Steps) != 0 {
		t.Fatalf("an undeclared pair probes nothing, got %+v", explanation.Steps)
	}
	if explanation.Decision != authmodel.ReasonDenied {
		t.Fatalf("Decision = %q", explanation.Decision)
	}
}

func TestRoleEngineExplainScopeVocabularyMatchesTheRolesKind(t *testing.T) {
	// The explain scopes are the roles kind's OWN provenance words, not a second
	// vocabulary — HasRoleWhere's provenance lands in ExplainStep.Scope verbatim.
	if authmodel.ExplainScopeDirect != roles.ProvenanceDirect {
		t.Fatalf("ExplainScopeDirect = %q, role.ProvenanceDirect = %q", authmodel.ExplainScopeDirect, roles.ProvenanceDirect)
	}
	if authmodel.ExplainScopeGlobal != roles.ProvenanceGlobal {
		t.Fatalf("ExplainScopeGlobal = %q, role.ProvenanceGlobal = %q", authmodel.ExplainScopeGlobal, roles.ProvenanceGlobal)
	}
}

// =============================================================================
// LookupResources
// =============================================================================

func TestRoleEngineLookupResourcesSortedAndDistinct(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	// Two granting roles on the same org fold into one ID; a non-granting role
	// and a foreign resource type contribute nothing.
	assign(t, svc, "u1", "viewer", "organization", "org-c")
	assign(t, svc, "u1", "contributor", "organization", "org-c")
	assign(t, svc, "u1", "viewer", "organization", "org-a")
	assign(t, svc, "u1", "steward", "organization", "org-b")
	assign(t, svc, "u1", "viewer", "project", "p-1")

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.Unrestricted {
		t.Fatal("no global grant: Unrestricted must be false")
	}
	want := []string{"org-a", "org-b", "org-c"}
	if fmt.Sprint(res.IDs) != fmt.Sprint(want) {
		t.Fatalf("IDs = %v, want %v", res.IDs, want)
	}

	// contribute is granted by contributor/steward only.
	res, err = engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "contribute", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if fmt.Sprint(res.IDs) != fmt.Sprint([]string{"org-b", "org-c"}) {
		t.Fatalf("IDs = %v, want [org-b org-c]", res.IDs)
	}
}

func TestRoleEngineLookupResourcesAlwaysNonNilIDs(t *testing.T) {
	engine, _ := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	res, err := engine.LookupResources(context.Background(), principal, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.IDs == nil || len(res.IDs) != 0 || res.Unrestricted {
		t.Fatalf("no assignments: want empty non-nil IDs, got %+v", res)
	}

	// An undeclared pair is the same empty, non-nil answer.
	res, err = engine.LookupResources(context.Background(), principal, "delete", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.IDs == nil || len(res.IDs) != 0 || res.Unrestricted {
		t.Fatalf("undeclared pair: want empty non-nil IDs, got %+v", res)
	}
}

func TestRoleEngineLookupResourcesGlobalGrantingRoleIsUnrestricted(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "steward", "", "")
	assign(t, svc, "u1", "viewer", "organization", "org-1")

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !res.Unrestricted {
		t.Fatalf("a globally held granting role is unrestricted, got %+v", res)
	}
	if res.IDs == nil || len(res.IDs) != 0 {
		t.Fatalf("an unrestricted result carries empty non-nil IDs, got %+v", res)
	}
}

func TestRoleEngineLookupResourcesGlobalNonGrantingRoleIsNotUnrestricted(t *testing.T) {
	// A global assignment is DATA, not a bypass: it widens only the permissions
	// whose grantor list names that role.
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "", "")
	assign(t, svc, "u1", "contributor", "organization", "org-1")

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "contribute", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.Unrestricted {
		t.Fatalf("viewer does not grant contribute, got %+v", res)
	}
	if fmt.Sprint(res.IDs) != fmt.Sprint([]string{"org-1"}) {
		t.Fatalf("IDs = %v, want [org-1]", res.IDs)
	}
}

func TestRoleEngineLookupResourcesWalksEveryPage(t *testing.T) {
	svc := newTestRoles(t, memory.NewRoles())
	paging := &smallPages{inner: svc, limit: 2}
	engine := newRoleEngine(paging, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{}))

	var want []string
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("org-%d", i)
		assign(t, svc, "u1", "viewer", "organization", id)
		want = append(want, id)
	}

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if fmt.Sprint(res.IDs) != fmt.Sprint(want) {
		t.Fatalf("IDs = %v, want %v", res.IDs, want)
	}
	if paging.pages < 4 {
		t.Fatalf("want a multi-page walk over 7 rows at page size 2, got %d pages", paging.pages)
	}
}

func TestRoleEngineLookupResourcesChargesEveryScannedRowAgainstMaxGraphStates(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{MaxGraphStates: 2})
	// Rows that grant NOTHING are still work: every scanned row is charged.
	assign(t, svc, "u1", "viewer", "project", "p-1")
	assign(t, svc, "u1", "viewer", "project", "p-2")
	assign(t, svc, "u1", "viewer", "project", "p-3")

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("want ErrEvaluationLimit, got %v", err)
	}
	if res.IDs != nil || res.Unrestricted {
		t.Fatalf("budget exhaustion returns no partial result, got %+v", res)
	}
}

func TestRoleEngineLookupResourcesOverflowIsNeverAPartialList(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{MaxLookupResults: 2})
	assign(t, svc, "u1", "viewer", "organization", "org-1")
	assign(t, svc, "u1", "viewer", "organization", "org-2")
	assign(t, svc, "u1", "viewer", "organization", "org-3")

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("want ErrEvaluationLimit, got %v", err)
	}
	if res.IDs != nil {
		t.Fatalf("overflow must return no list at all, got %v", res.IDs)
	}

	// Exactly at the bound is a complete result, not an overflow.
	engine, svc = newTestEngine(t, orgModel(), authmodel.EvaluationLimits{MaxLookupResults: 2})
	assign(t, svc, "u1", "viewer", "organization", "org-1")
	assign(t, svc, "u1", "viewer", "organization", "org-2")
	res, err = engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("at the bound: %v", err)
	}
	if fmt.Sprint(res.IDs) != fmt.Sprint([]string{"org-1", "org-2"}) {
		t.Fatalf("IDs = %v", res.IDs)
	}
}

func TestRoleEngineLookupResourcesCancellationBeforeAPage(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "organization", "org-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := engine.LookupResources(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if res.IDs != nil || res.Unrestricted {
		t.Fatalf("cancellation returns no result, got %+v", res)
	}
}

func TestRoleEngineLookupResourcesRejectsMalformedArguments(t *testing.T) {
	engine, _ := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	if _, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{}, "view", "organization"); err == nil {
		t.Fatal("want a validation error for an empty principal")
	}
	if _, err := engine.LookupResources(context.Background(), principal, "", "organization"); err == nil {
		t.Fatal("want a validation error for an empty permission")
	}
	if _, err := engine.LookupResources(context.Background(), principal, "view", ""); err == nil {
		t.Fatal("want a validation error for an empty resource type")
	}
}

func TestRoleEngineLookupResourcesEmptyPageClaimingHasMoreTerminates(t *testing.T) {
	probe := &stallProbe{}
	engine := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{}))

	// The deadline is a safety net only: a correct walk stops on the first
	// empty page and never observes it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := engine.LookupResources(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.IDs == nil || len(res.IDs) != 0 || res.Unrestricted {
		t.Fatalf("want empty non-nil IDs, got %+v", res)
	}
	if probe.calls != 1 {
		t.Fatalf("an empty page ends the walk, got %d listings", probe.calls)
	}
}

func TestRoleEngineLookupResourcesSkipsHalfScopedRows(t *testing.T) {
	// A raw-port row with a resource TYPE but no ID names no resource: it must
	// never land "" in IDs, which Check would reject — Check/Lookup parity.
	probe := &rawRows{items: []roles.Assignment{
		{SubjectType: "user", SubjectID: "u1", Role: "viewer", ResourceType: "organization", ResourceID: ""},
		{SubjectType: "user", SubjectID: "u1", Role: "viewer", ResourceType: "organization", ResourceID: "org-1"},
	}}
	engine := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{}))

	res, err := engine.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if fmt.Sprint(res.IDs) != fmt.Sprint([]string{"org-1"}) {
		t.Fatalf("IDs = %q, want [org-1]", res.IDs)
	}
	if res.Unrestricted {
		t.Fatalf("a half-scoped row is not a global grant, got %+v", res)
	}
}

// =============================================================================
// Pair ownership
// =============================================================================

func TestRoleEngineDeclaresPermission(t *testing.T) {
	engine, _ := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	if !engine.DeclaresPermission("organization", "view") {
		t.Fatal("the engine must own organization/view")
	}
	if engine.DeclaresPermission("organization", "delete") {
		t.Fatal("the engine must not claim an undeclared pair")
	}
}

var _ roleProbe = (*testRoles)(nil)

// =============================================================================
// LookupResourcesPage (the roles kind's paged enumeration)
// =============================================================================

// TestRoleEngineLookupResourcesPagePagesInOrder proves the paged path returns the
// same globally sorted ids the unpaged walk does, one page at a time, and that
// duplicate resource ids reached through TWO granting roles fold into one entry.
func TestRoleEngineLookupResourcesPagePagesInOrder(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	ctx := context.Background()
	for _, id := range []string{"o1", "o2", "o3"} {
		assign(t, svc, "u1", "viewer", "organization", id)
	}
	assign(t, svc, "u1", "contributor", "organization", "o2") // a second granting role on o2

	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}
	plain, err := engine.LookupResources(ctx, principal, "view", "organization")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}

	for _, limit := range []int{1, 2, 7} {
		got := []string{}
		after := ""
		for pages := 0; ; pages++ {
			if pages > 16 {
				t.Fatalf("limit %d: page walk did not terminate", limit)
			}
			res, err := engine.LookupResourcesPage(ctx, principal, "view", "organization", after, limit)
			if err != nil {
				t.Fatalf("LookupResourcesPage(after=%q): %v", after, err)
			}
			if res.IDs == nil {
				t.Fatalf("limit %d: IDs must be non-nil", limit)
			}
			got = append(got, res.IDs...)
			if !res.HasMore {
				break
			}
			after = res.IDs[len(res.IDs)-1]
		}
		if !reflect.DeepEqual(got, plain.IDs) {
			t.Fatalf("limit %d: pages concatenated to %v, want the plain result %v", limit, got, plain.IDs)
		}
	}
}

// TestRoleEngineLookupResourcesPageGlobalGrantIsUnrestricted proves a GLOBALLY
// held granting role ends the query on the paged surface too: unrestricted, no
// ids, and no continuation to follow.
func TestRoleEngineLookupResourcesPageGlobalGrantIsUnrestricted(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "", "")

	res, err := engine.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"},
		"view", "organization", "", 1)
	if err != nil {
		t.Fatalf("LookupResourcesPage: %v", err)
	}
	if !res.Unrestricted || len(res.IDs) != 0 || res.IDs == nil || res.HasMore {
		t.Fatalf("want unrestricted with an empty non-nil page and no continuation, got %+v", res)
	}
}

// TestRoleEngineLookupResourcesPageUndeclaredPairAndBadArguments proves the paged
// path applies the SAME preconditions as the unpaged one: an undeclared pair
// enumerates nothing (non-nil), and malformed arguments are rejected before any
// store read.
func TestRoleEngineLookupResourcesPageUndeclaredPairAndBadArguments(t *testing.T) {
	engine, svc := newTestEngine(t, orgModel(), authmodel.EvaluationLimits{})
	assign(t, svc, "u1", "viewer", "organization", "o1")
	good := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	res, err := engine.LookupResourcesPage(context.Background(), good, "fly", "organization", "", 5)
	if err != nil {
		t.Fatalf("undeclared pair: %v", err)
	}
	if res.IDs == nil || len(res.IDs) != 0 || res.HasMore {
		t.Fatalf("undeclared pair: want an empty non-nil page, got %+v", res)
	}

	cases := map[string]struct {
		principal                authmodel.PrincipalRef
		permission, resourceType string
	}{
		"empty principal":     {authmodel.PrincipalRef{}, "view", "organization"},
		"empty permission":    {good, "", "organization"},
		"empty resource type": {good, "view", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := engine.LookupResourcesPage(context.Background(), tc.principal, tc.permission, tc.resourceType, "", 5); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
}

// TestRoleEngineLookupResourcesPageStoreErrorIsNeverAnAllow proves a store
// failure surfaces as an error, never an empty page a caller might read as "no
// access".
func TestRoleEngineLookupResourcesPageStoreErrorIsNeverAnAllow(t *testing.T) {
	boom := errors.New("store exploded")
	engine := newRoleEngine(errProbe{err: boom}, mustCompile(t, orgModel(), nil),
		resolvedLimits(t, authmodel.EvaluationLimits{}))
	if _, err := engine.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"},
		"view", "organization", "", 2); !errors.Is(err, boom) {
		t.Fatalf("want the store error, got %v", err)
	}
}
