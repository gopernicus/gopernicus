package decisionsvc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/rolesvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
)

// errRelationships is a relationship.Storer whose every read fails — the proof
// that a relationship-store outage cannot reach a ROLE-owned pair.
type errRelationships struct {
	relationship.Storer
	err error
}

func (s errRelationships) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return false, s.err
}

func (s errRelationships) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return nil, s.err
}

func (s errRelationships) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	return nil, s.err
}

func (s errRelationships) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error) {
	return nil, s.err
}

// compositeSchema is the relationship half of the shared fixture: "org" is
// relationship-only, "project" is a SPLIT type — its view permission is
// relationship-owned while its audit permission belongs to the role model.
func compositeSchema() authorizersvc.Schema {
	return authorizersvc.NewSchema([]authorizersvc.ResourceSchema{
		{
			Name: "org",
			Def: authorizersvc.ResourceTypeDef{
				Relations: map[string]authorizersvc.RelationDef{
					"member": {AllowedSubjects: []authorizersvc.SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]authorizersvc.PermissionRule{
					"enter": authorizersvc.AnyOf(authorizersvc.Direct("member")),
				},
			},
		},
		{
			Name: "project",
			Def: authorizersvc.ResourceTypeDef{
				Relations: map[string]authorizersvc.RelationDef{
					"viewer": {AllowedSubjects: []authorizersvc.SubjectTypeRef{{Type: "user"}}},
					"org":    {AllowedSubjects: []authorizersvc.SubjectTypeRef{{Type: "org"}}},
				},
				Permissions: map[string]authorizersvc.PermissionRule{
					"view": authorizersvc.AnyOf(authorizersvc.Direct("viewer"), authorizersvc.Through("org", "enter")),
				},
			},
		},
	})
}

// compositeRoleModel is the roles half of the shared fixture: project/audit on
// the SPLIT type plus a singleton platform type for the resource-type-independent
// permission.
func compositeRoleModel() RoleModel {
	return RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"project":  {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
		"platform": {Roles: []string{"steward"}, Permissions: map[string][]string{"steward": {"steward"}}},
	}}
}

func newRelationshipEngine(t *testing.T, store relationship.Storer, limits authorizersvc.EvaluationLimits) *authorizersvc.Service {
	t.Helper()
	eng, err := authorizersvc.NewService(store, compositeSchema(), authorizersvc.Config{Limits: limits})
	if err != nil {
		t.Fatalf("authorizersvc.NewService: %v", err)
	}
	return eng
}

// newBothKinds builds the both-kinds composite over REAL engines and in-core
// stores, with the pair split over the "project" type.
func newBothKinds(t *testing.T, limits authorizersvc.EvaluationLimits) (*Composite, *authorizersvc.Service, *rolesvc.Service) {
	t.Helper()
	resolved := resolvedLimits(t, limits)
	eng := newRelationshipEngine(t, memstore.NewRelationships(), limits)
	roles := rolesvc.NewService(memstore.NewRoles())
	return NewComposite(eng, roles, mustCompile(t, compositeRoleModel(), eng), resolved), eng, roles
}

// newRolesOnly builds the roles-only-with-model composite: no relationship kind
// at all, so the role model answers every declared pair and owns the fallback.
func newRolesOnly(t *testing.T, limits authorizersvc.EvaluationLimits) (*Composite, *rolesvc.Service) {
	t.Helper()
	roles := rolesvc.NewService(memstore.NewRoles())
	return NewComposite(nil, roles, mustCompile(t, compositeRoleModel(), nil), resolvedLimits(t, limits)), roles
}

func grant(t *testing.T, eng *authorizersvc.Service, resourceType, resourceID, relation, subjectType, subjectID string) {
	t.Helper()
	err := eng.CreateRelationships(context.Background(), []relationship.CreateRelationship{{
		ResourceType: resourceType, ResourceID: resourceID, Relation: relation,
		SubjectType: subjectType, SubjectID: subjectID,
	}})
	if err != nil {
		t.Fatalf("CreateRelationships(%s:%s#%s): %v", resourceType, resourceID, relation, err)
	}
}

func request(subjectID, permission, resourceType, resourceID string) authorizersvc.CheckRequest {
	return authorizersvc.CheckRequest{
		Principal:  authorizersvc.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authorizersvc.Resource{Type: resourceType, ID: resourceID},
	}
}

// =============================================================================
// Construction
// =============================================================================

// TestNewCompositeWithoutAModelBearingKindIsNil pins the "no decision-capable
// kind" state at the constructor: a roles service with NO model bears no model,
// so there is nothing to dispatch to and the caller reports its own sentinel
// rather than receiving a composite that would deny everything.
func TestNewCompositeWithoutAModelBearingKindIsNil(t *testing.T) {
	roles := rolesvc.NewService(memstore.NewRoles())
	if c := NewComposite(nil, roles, nil, resolvedLimits(t, authorizersvc.EvaluationLimits{})); c != nil {
		t.Fatalf("a roles service with no model must not yield a decider, got %+v", c)
	}
	if c := NewComposite(nil, nil, nil, resolvedLimits(t, authorizersvc.EvaluationLimits{})); c != nil {
		t.Fatalf("no kind at all must not yield a decider, got %+v", c)
	}
}

// TestCompositeDeclaresPermission proves the registration-time predicate answers
// for EITHER model, across the split resource type.
func TestCompositeDeclaresPermission(t *testing.T) {
	c, _, _ := newBothKinds(t, authorizersvc.EvaluationLimits{})
	cases := map[string]struct {
		resourceType, permission string
		want                     bool
	}{
		"relationship-owned":         {"project", "view", true},
		"role-owned on a split type": {"project", "audit", true},
		"role-owned singleton":       {"platform", "steward", true},
		"relationship-only type":     {"org", "enter", true},
		"declared by neither":        {"project", "fly", false},
		"unknown type":               {"comet", "view", false},
	}
	for name, tc := range cases {
		if got := c.DeclaresPermission(tc.resourceType, tc.permission); got != tc.want {
			t.Fatalf("%s: DeclaresPermission(%q, %q) = %v, want %v", name, tc.resourceType, tc.permission, got, tc.want)
		}
	}
}

// =============================================================================
// Dispatch
// =============================================================================

// TestCompositeDispatchByPair proves the ONE surface routes each pair to the
// model that declares it, including two permissions of the SAME resource type
// answered by different kinds.
func TestCompositeDispatchByPair(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u2", "auditor", "project", "p1")

	cases := []struct {
		name       string
		req        authorizersvc.CheckRequest
		wantAllow  bool
		wantReason string
	}{
		{"relationship-owned pair, tuple holder", request("u1", "view", "project", "p1"), true, "direct:viewer"},
		{"relationship-owned pair, role holder is not a viewer", request("u2", "view", "project", "p1"), false, "no matching rule"},
		{"role-owned pair, role holder", request("u2", "audit", "project", "p1"), true, "role:auditor@direct"},
		{"role-owned pair, tuple holder has no role", request("u1", "audit", "project", "p1"), false, "no matching role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.Check(ctx, tc.req)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if res.Allowed != tc.wantAllow || res.Reason != tc.wantReason {
				t.Fatalf("got %+v, want allowed=%v reason=%q", res, tc.wantAllow, tc.wantReason)
			}
		})
	}
}

// TestCompositeUndeclaredPairDenies proves a pair NO model declares is the
// engines' shipped "no rules defined" deny — spoken by the relationship engine
// when it is wired and by the roles engine on a roles-only host.
func TestCompositeUndeclaredPairDenies(t *testing.T) {
	ctx := context.Background()
	want := authorizersvc.CheckResult{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no rules defined"}

	both, _, _ := newBothKinds(t, authorizersvc.EvaluationLimits{})
	got, err := both.Check(ctx, request("u1", "fly", "project", "p1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != want {
		t.Fatalf("both kinds: got %+v, want %+v", got, want)
	}

	rolesOnly, _ := newRolesOnly(t, authorizersvc.EvaluationLimits{})
	got, err = rolesOnly.Check(ctx, request("u1", "fly", "project", "p1"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != want {
		t.Fatalf("roles only: got %+v, want %+v", got, want)
	}
}

// TestCompositeValidatesBeforeTouchingAStore proves the request is validated
// first: with BOTH stores failing every read, a malformed request still returns
// its validation error rather than an availability error.
func TestCompositeValidatesBeforeTouchingAStore(t *testing.T) {
	boom := errors.New("store exploded")
	eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng),
		resolvedLimits(t, authorizersvc.EvaluationLimits{}))

	for name, req := range map[string]authorizersvc.CheckRequest{
		"no principal":  {Permission: "view", Resource: authorizersvc.Resource{Type: "project", ID: "p1"}},
		"no permission": {Principal: authorizersvc.PrincipalRef{Type: "user", ID: "u1"}, Resource: authorizersvc.Resource{Type: "project", ID: "p1"}},
		"no resource":   {Principal: authorizersvc.PrincipalRef{Type: "user", ID: "u1"}, Permission: "audit"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Check(context.Background(), req); err == nil || errors.Is(err, boom) {
				t.Fatalf("want a validation error before any store call, got %v", err)
			}
		})
	}
}

// TestCompositeOwnerOnlyErrors is the availability-decoupling proof: only the
// OWNING kind runs, so one kind's store outage cannot make the other kind's pair
// indeterminate.
func TestCompositeOwnerOnlyErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("store exploded")
	limits := resolvedLimits(t, authorizersvc.EvaluationLimits{})

	t.Run("failing roles probe cannot affect a relationship-owned pair", func(t *testing.T) {
		eng := newRelationshipEngine(t, memstore.NewRelationships(), authorizersvc.EvaluationLimits{})
		grant(t, eng, "project", "p1", "viewer", "user", "u1")
		c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng), limits)

		res, err := c.Check(ctx, request("u1", "view", "project", "p1"))
		if err != nil || !res.Allowed {
			t.Fatalf("relationship-owned pair: got %+v err=%v, want an allow", res, err)
		}
		if _, err := c.Check(ctx, request("u1", "audit", "project", "p1")); !errors.Is(err, boom) {
			t.Fatalf("role-owned pair must surface the roles store error, got %v", err)
		}
	})

	t.Run("failing relationship store cannot affect a role-owned pair", func(t *testing.T) {
		eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
		roles := rolesvc.NewService(memstore.NewRoles())
		assign(t, roles, "u2", "auditor", "project", "p1")
		c := NewComposite(eng, roles, mustCompile(t, compositeRoleModel(), eng), limits)

		res, err := c.Check(ctx, request("u2", "audit", "project", "p1"))
		if err != nil || !res.Allowed {
			t.Fatalf("role-owned pair: got %+v err=%v, want an allow", res, err)
		}
		if _, err := c.Check(ctx, request("u2", "view", "project", "p1")); !errors.Is(err, boom) {
			t.Fatalf("relationship-owned pair must surface the relationship store error, got %v", err)
		}
	})
}

// =============================================================================
// Batch
// =============================================================================

// TestCompositeCheckBatchGroupsAndMerges proves an interleaved batch is grouped
// by owning kind and merged back BY INDEX — every answer lands on its own
// request.
func TestCompositeCheckBatchGroupsAndMerges(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "p2")

	reqs := []authorizersvc.CheckRequest{
		request("u1", "audit", "project", "p2"), // role-owned, allowed
		request("u1", "view", "project", "p1"),  // relationship-owned, allowed
		request("u1", "audit", "project", "p1"), // role-owned, denied
		request("u1", "view", "project", "p2"),  // relationship-owned, denied
		request("u1", "fly", "project", "p1"),   // declared by neither
	}
	results, err := c.CheckBatch(ctx, reqs)
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	want := []authorizersvc.CheckResult{
		{Allowed: true, ReasonCode: authorizersvc.ReasonGranted, Reason: "role:auditor@direct"},
		{Allowed: true, ReasonCode: authorizersvc.ReasonGranted, Reason: "direct:viewer"},
		{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no matching role"},
		{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no matching rule"},
		{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no rules defined"},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("merged batch:\n got %+v\nwant %+v", results, want)
	}
}

// TestCompositeCheckBatchZeroLength pins the literal zero-length identities: nil
// and empty both answer (nil, nil) without touching a kind (both stores here
// fail every read).
func TestCompositeCheckBatchZeroLength(t *testing.T) {
	boom := errors.New("store exploded")
	eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng),
		resolvedLimits(t, authorizersvc.EvaluationLimits{}))

	for name, reqs := range map[string][]authorizersvc.CheckRequest{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			results, err := c.CheckBatch(context.Background(), reqs)
			if err != nil || results != nil {
				t.Fatalf("want (nil, nil), got (%v, %v)", results, err)
			}
		})
	}

	ids, err := c.FilterAuthorized(context.Background(), authorizersvc.PrincipalRef{Type: "user", ID: "u1"}, "view", "project", nil)
	if err != nil || ids != nil {
		t.Fatalf("FilterAuthorized with no IDs: want (nil, nil), got (%v, %v)", ids, err)
	}
}

// TestCompositeCheckBatchChargesMaxBatchSizeOnce proves the ceiling is the
// composite's, charged over the WHOLE batch and not per subset: a batch at the
// ceiling passes even when it splits across both kinds, and one over it is
// ErrEvaluationLimit before any store call.
func TestCompositeCheckBatchChargesMaxBatchSizeOnce(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "p1")

	atCeiling := []authorizersvc.CheckRequest{
		request("u1", "view", "project", "p1"),
		request("u1", "audit", "project", "p1"),
	}
	results, err := c.CheckBatch(ctx, atCeiling)
	if err != nil {
		t.Fatalf("a batch AT the ceiling must be accepted, got %v", err)
	}
	if !results[0].Allowed || !results[1].Allowed {
		t.Fatalf("both halves should allow, got %+v", results)
	}

	over := append(atCeiling, request("u1", "view", "project", "p2"))
	if _, err := c.CheckBatch(ctx, over); !errors.Is(err, authorizersvc.ErrEvaluationLimit) {
		t.Fatalf("over the ceiling: want ErrEvaluationLimit, got %v", err)
	}
}

// TestCompositeCheckBatchValidatesEveryRequestFirst proves a single malformed
// request fails the whole batch with its validation error before any store is
// touched.
func TestCompositeCheckBatchValidatesEveryRequestFirst(t *testing.T) {
	boom := errors.New("store exploded")
	eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng),
		resolvedLimits(t, authorizersvc.EvaluationLimits{}))

	reqs := []authorizersvc.CheckRequest{
		request("u1", "view", "project", "p1"),
		{Permission: "audit", Resource: authorizersvc.Resource{Type: "project", ID: "p1"}}, // no principal
	}
	_, err := c.CheckBatch(context.Background(), reqs)
	if err == nil || errors.Is(err, boom) {
		t.Fatalf("want a validation error before any store call, got %v", err)
	}
}

// TestCompositeFilterAuthorizedDispatches proves the ID filter rides the same
// dispatch: a role-owned permission filters by role assignments alone.
func TestCompositeFilterAuthorizedDispatches(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "p2")
	principal := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}

	ids, err := c.FilterAuthorized(ctx, principal, "audit", "project", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("FilterAuthorized: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"p2"}) {
		t.Fatalf("role-owned filter: got %v, want [p2]", ids)
	}

	ids, err = c.FilterAuthorized(ctx, principal, "view", "project", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("FilterAuthorized: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"p1"}) {
		t.Fatalf("relationship-owned filter: got %v, want [p1]", ids)
	}
}

// =============================================================================
// Explain
// =============================================================================

// TestCompositeCheckExplainDelegatesToTheOwner proves the trace is the owning
// kind's alone and Decision is the FINAL ReasonCode.
func TestCompositeCheckExplainDelegatesToTheOwner(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u2", "auditor", "project", "p1")

	res, expl, err := c.CheckExplain(ctx, request("u1", "view", "project", "p1"))
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if expl.Decision != res.ReasonCode || len(expl.Steps) == 0 {
		t.Fatalf("relationship trace: %+v (decision %q vs %q)", expl, expl.Decision, res.ReasonCode)
	}
	for _, step := range expl.Steps {
		if step.Kind == authorizersvc.ExplainKindRole {
			t.Fatalf("a relationship-owned pair must not emit role steps: %+v", expl.Steps)
		}
	}

	res, expl, err = c.CheckExplain(ctx, request("u2", "audit", "project", "p1"))
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if expl.Decision != res.ReasonCode {
		t.Fatalf("Decision %q != ReasonCode %q", expl.Decision, res.ReasonCode)
	}
	want := []authorizersvc.ExplainStep{{
		ResourceType: "project", ResourceID: "p1", Permission: "audit",
		Kind: authorizersvc.ExplainKindRole, Outcome: authorizersvc.ReasonGranted,
		Role: "auditor", Scope: authorizersvc.ExplainScopeDirect,
	}}
	if !reflect.DeepEqual(expl.Steps, want) {
		t.Fatalf("role trace:\n got %+v\nwant %+v", expl.Steps, want)
	}
}

// =============================================================================
// Lookup
// =============================================================================

// TestCompositeLookupResourcesDispatches proves enumeration goes to the owning
// kind verbatim, including the roles kind's Unrestricted answer for a GLOBALLY
// held granting role — which the relationship kind never produces.
func TestCompositeLookupResourcesDispatches(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	assign(t, roles, "u1", "auditor", "project", "p2")
	principal := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}

	got, err := c.LookupResources(ctx, principal, "view", "project")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !reflect.DeepEqual(got, authorizersvc.LookupResult{IDs: []string{"p1"}}) {
		t.Fatalf("relationship-owned lookup: got %+v", got)
	}

	got, err = c.LookupResources(ctx, principal, "audit", "project")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !reflect.DeepEqual(got, authorizersvc.LookupResult{IDs: []string{"p2"}}) {
		t.Fatalf("role-owned lookup: got %+v", got)
	}

	// The same role held GLOBALLY reaches every project of the type.
	assign(t, roles, "u2", "auditor", "", "")
	got, err = c.LookupResources(ctx, authorizersvc.PrincipalRef{Type: "user", ID: "u2"}, "audit", "project")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !got.Unrestricted || len(got.IDs) != 0 {
		t.Fatalf("global granting role: want unrestricted with empty IDs, got %+v", got)
	}

	// A pair NO model declares enumerates nothing, non-nil.
	got, err = c.LookupResources(ctx, principal, "fly", "project")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if got.Unrestricted || got.IDs == nil || len(got.IDs) != 0 {
		t.Fatalf("undeclared pair: want an empty non-nil list, got %+v", got)
	}
}

// TestCompositeLookupResourcesValidatesArguments proves principal/permission/type
// are validated before any store is touched.
func TestCompositeLookupResourcesValidatesArguments(t *testing.T) {
	boom := errors.New("store exploded")
	eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng),
		resolvedLimits(t, authorizersvc.EvaluationLimits{}))
	good := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}

	cases := map[string]struct {
		principal                authorizersvc.PrincipalRef
		permission, resourceType string
	}{
		"empty principal":     {authorizersvc.PrincipalRef{}, "audit", "project"},
		"empty permission":    {good, "", "project"},
		"empty resource type": {good, "audit", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.LookupResources(context.Background(), tc.principal, tc.permission, tc.resourceType); err == nil || errors.Is(err, boom) {
				t.Fatalf("want a validation error before any store call, got %v", err)
			}
		})
	}
}

// TestCompositeLookupResourcesInTruncates proves the ONE truncation body: the
// owning kind enumerates unchanged and the Limit caps the sorted prefix, on a
// relationship-owned and a role-owned pair alike. Limit 0 / Limit >= len is
// today's untruncated behavior, and Truncated is set only when IDs were dropped.
func TestCompositeLookupResourcesInTruncates(t *testing.T) {
	c, eng, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	ctx := context.Background()
	for _, id := range []string{"p1", "p2", "p3"} {
		grant(t, eng, "project", id, "viewer", "user", "u1")
		assign(t, roles, "u1", "auditor", "project", id)
	}
	principal := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}

	for _, permission := range []string{"view", "audit"} {
		t.Run(permission, func(t *testing.T) {
			cases := map[string]struct {
				limit int
				want  authorizersvc.LookupResult
			}{
				"limit below the count truncates to the sorted prefix": {2, authorizersvc.LookupResult{IDs: []string{"p1", "p2"}, Truncated: true}},
				"limit of one":                  {1, authorizersvc.LookupResult{IDs: []string{"p1"}, Truncated: true}},
				"limit equal to the count":      {3, authorizersvc.LookupResult{IDs: []string{"p1", "p2", "p3"}}},
				"limit above the count":         {10, authorizersvc.LookupResult{IDs: []string{"p1", "p2", "p3"}}},
				"limit zero is today's ceiling": {0, authorizersvc.LookupResult{IDs: []string{"p1", "p2", "p3"}}},
			}
			for name, tc := range cases {
				t.Run(name, func(t *testing.T) {
					got, err := c.LookupResourcesIn(ctx, authorizersvc.LookupRequest{
						Principal: principal, Permission: permission, ResourceType: "project", Limit: tc.limit,
					})
					if err != nil {
						t.Fatalf("LookupResourcesIn: %v", err)
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("got %+v, want %+v", got, tc.want)
					}
					if got.IDs == nil {
						t.Fatal("IDs must stay non-nil through truncation")
					}
				})
			}
		})
	}
}

// TestCompositeLookupResourcesInDoesNotAliasTheDroppedTail proves the truncated
// slice is CLIPPED: appending to what a caller received cannot reach — or
// overwrite — the IDs this answer deliberately withheld.
func TestCompositeLookupResourcesInDoesNotAliasTheDroppedTail(t *testing.T) {
	c, eng, _ := newBothKinds(t, authorizersvc.EvaluationLimits{})
	for _, id := range []string{"p1", "p2", "p3"} {
		grant(t, eng, "project", id, "viewer", "user", "u1")
	}

	got, err := c.LookupResourcesIn(context.Background(), authorizersvc.LookupRequest{
		Principal: authorizersvc.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", ResourceType: "project", Limit: 1,
	})
	if err != nil {
		t.Fatalf("LookupResourcesIn: %v", err)
	}
	if cap(got.IDs) != len(got.IDs) {
		t.Fatalf("cap(IDs) = %d, len = %d; the truncated slice still reaches the dropped tail", cap(got.IDs), len(got.IDs))
	}

	// A caller appending to its own result must not disturb a second enumeration.
	appended := append(got.IDs, "caller-owned")
	if appended[1] != "caller-owned" {
		t.Fatalf("append landed on %q", appended[1])
	}
	again, err := c.LookupResourcesIn(context.Background(), authorizersvc.LookupRequest{
		Principal: authorizersvc.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", ResourceType: "project", Limit: 3,
	})
	if err != nil {
		t.Fatalf("LookupResourcesIn: %v", err)
	}
	if !reflect.DeepEqual(again.IDs, []string{"p1", "p2", "p3"}) {
		t.Fatalf("second enumeration = %v, want the untouched full set", again.IDs)
	}
}

// TestCompositeLookupResourcesInPassesUnrestrictedThrough proves an Unrestricted
// answer IGNORES the Limit: it names no IDs to cap, so it must not come back
// looking truncated.
func TestCompositeLookupResourcesInPassesUnrestrictedThrough(t *testing.T) {
	c, _, roles := newBothKinds(t, authorizersvc.EvaluationLimits{})
	assign(t, roles, "u2", "auditor", "", "")

	got, err := c.LookupResourcesIn(context.Background(), authorizersvc.LookupRequest{
		Principal: authorizersvc.PrincipalRef{Type: "user", ID: "u2"}, Permission: "audit", ResourceType: "project", Limit: 1,
	})
	if err != nil {
		t.Fatalf("LookupResourcesIn: %v", err)
	}
	if !got.Unrestricted || got.Truncated || len(got.IDs) != 0 {
		t.Fatalf("a globally held granting role must pass through untouched, got %+v", got)
	}
}

// TestCompositeLookupResourcesInValidates proves the request is validated before
// any store is touched — the negative Limit included.
func TestCompositeLookupResourcesInValidates(t *testing.T) {
	boom := errors.New("store exploded")
	eng := newRelationshipEngine(t, errRelationships{err: boom}, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, errProbe{err: boom}, mustCompile(t, compositeRoleModel(), eng),
		resolvedLimits(t, authorizersvc.EvaluationLimits{}))
	good := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}

	cases := map[string]authorizersvc.LookupRequest{
		"empty principal":     {Permission: "audit", ResourceType: "project"},
		"empty permission":    {Principal: good, ResourceType: "project"},
		"empty resource type": {Principal: good, Permission: "audit"},
		"negative limit":      {Principal: good, Permission: "audit", ResourceType: "project", Limit: -1},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.LookupResourcesIn(context.Background(), req); err == nil || errors.Is(err, boom) {
				t.Fatalf("want a validation error before any store call, got %v", err)
			}
		})
	}
}

// =============================================================================
// Single-kind equivalence
// =============================================================================

// TestCompositeSingleKindEquivalence is the compatibility pin for every
// relationship-only host: over ONE shared fixture the composite and the
// relationship engine return byte-equal decisions, reasons, traces, enumerations,
// and zero-length values — including for invalid and undeclared requests. If this
// drifts, an existing host's behaviour changed.
func TestCompositeSingleKindEquivalence(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewRelationships()
	eng := newRelationshipEngine(t, store, authorizersvc.EvaluationLimits{})
	c := NewComposite(eng, nil, nil, resolvedLimits(t, authorizersvc.EvaluationLimits{}))

	grant(t, eng, "project", "p1", "viewer", "user", "u1")
	grant(t, eng, "project", "p2", "org", "org", "o1")
	grant(t, eng, "org", "o1", "member", "user", "u2")

	reqs := []authorizersvc.CheckRequest{
		request("u1", "view", "project", "p1"),                                            // direct grant
		request("u2", "view", "project", "p2"),                                            // granted through the org hop
		request("u2", "view", "project", "p1"),                                            // denied
		request("u1", "fly", "project", "p1"),                                             // declared by neither model
		request("u1", "enter", "org", "o1"),                                               // another type
		{Permission: "view", Resource: authorizersvc.Resource{Type: "project", ID: "p1"}}, // invalid: no principal
	}

	for i, req := range reqs {
		wantRes, wantErr := eng.Check(ctx, req)
		gotRes, gotErr := c.Check(ctx, req)
		if !reflect.DeepEqual(gotRes, wantRes) || !sameError(gotErr, wantErr) {
			t.Fatalf("Check[%d]: got (%+v, %v), want (%+v, %v)", i, gotRes, gotErr, wantRes, wantErr)
		}

		wantRes, wantExpl, wantErr := eng.CheckExplain(ctx, req)
		gotRes, gotExpl, gotErr := c.CheckExplain(ctx, req)
		if !reflect.DeepEqual(gotRes, wantRes) || !reflect.DeepEqual(gotExpl, wantExpl) || !sameError(gotErr, wantErr) {
			t.Fatalf("CheckExplain[%d]: got (%+v, %+v, %v), want (%+v, %+v, %v)", i, gotRes, gotExpl, gotErr, wantRes, wantExpl, wantErr)
		}
	}

	// Batches, including the zero-length identities and a batch carrying the
	// invalid request.
	batches := map[string][]authorizersvc.CheckRequest{
		"nil":          nil,
		"empty":        {},
		"all valid":    reqs[:5],
		"with invalid": reqs,
	}
	for name, batch := range batches {
		wantResults, wantErr := eng.CheckBatch(ctx, batch)
		gotResults, gotErr := c.CheckBatch(ctx, batch)
		if !reflect.DeepEqual(gotResults, wantResults) || !sameError(gotErr, wantErr) {
			t.Fatalf("CheckBatch(%s): got (%+v, %v), want (%+v, %v)", name, gotResults, gotErr, wantResults, wantErr)
		}
	}

	principal := authorizersvc.PrincipalRef{Type: "user", ID: "u1"}
	for name, ids := range map[string][]string{
		"nil ids":   nil,
		"empty ids": {},
		"some ids":  {"p1", "p2"},
	} {
		wantIDs, wantErr := eng.FilterAuthorized(ctx, principal, "view", "project", ids)
		gotIDs, gotErr := c.FilterAuthorized(ctx, principal, "view", "project", ids)
		if !reflect.DeepEqual(gotIDs, wantIDs) || !sameError(gotErr, wantErr) {
			t.Fatalf("FilterAuthorized(%s): got (%v, %v), want (%v, %v)", name, gotIDs, gotErr, wantIDs, wantErr)
		}
	}

	lookups := []struct{ principal, permission, resourceType string }{
		{"u1", "view", "project"},
		{"u2", "view", "project"},
		{"u1", "fly", "project"}, // declared by neither model
		{"", "view", "project"},  // invalid principal
	}
	for _, l := range lookups {
		p := authorizersvc.PrincipalRef{Type: "user", ID: l.principal}
		if l.principal == "" {
			p = authorizersvc.PrincipalRef{}
		}
		wantRes, wantErr := eng.LookupResources(ctx, p, l.permission, l.resourceType)
		gotRes, gotErr := c.LookupResources(ctx, p, l.permission, l.resourceType)
		if !reflect.DeepEqual(gotRes, wantRes) || !sameError(gotErr, wantErr) {
			t.Fatalf("LookupResources(%+v): got (%+v, %v), want (%+v, %v)", l, gotRes, gotErr, wantRes, wantErr)
		}
	}
}

// sameError compares two errors by nil-ness and message: the composite returns
// the engine's own error values, so equal messages plus equal nil-ness is the
// honest identity check for a pass-through.
func sameError(got, want error) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	return got == nil || got.Error() == want.Error()
}

// compile-time proof that both kinds satisfy the composite's dispatch contract.
var (
	_ kind      = (*authorizersvc.Service)(nil)
	_ kind      = (*roleEngine)(nil)
	_ roleProbe = (*rolesvc.Service)(nil)
)
