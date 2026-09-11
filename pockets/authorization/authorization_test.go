package authorization

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// relFake is a trivial relationship.Storer for socket wiring/delegation tests.
type relFake struct{ checkCalls int }

func (f *relFake) ForModel(relationships.ReadModel) relationships.Reader { return f }

func (f *relFake) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	f.checkCalls++
	return false, nil
}
func (f *relFake) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	for range resourceIDs {
		f.checkCalls++
	}
	return nil, nil
}
func (f *relFake) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationships.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}
func (f *relFake) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (f *relFake) CreateRelationships(ctx context.Context, relationships []relationships.CreateRelationship) error {
	return nil
}
func (f *relFake) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, targets []relationships.CreateRelationship) error {
	return nil
}
func (f *relFake) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	return nil
}
func (f *relFake) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return nil
}
func (f *relFake) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return 0, nil
}
func (f *relFake) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
	return list.Page[relationships.SubjectRelationship]{}, nil
}
func (f *relFake) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
	return list.Page[relationships.ResourceRelationship]{}, nil
}
func (f *relFake) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}

// roleFake is a trivial role.Storer for socket wiring/delegation tests.
type roleFake struct {
	hasCalls int
}

func (f *roleFake) Assign(ctx context.Context, a roles.Assignment) error { return nil }
func (f *roleFake) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return nil
}
func (f *roleFake) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	f.hasCalls++
	return false, nil
}
func (f *roleFake) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, nil
}
func (f *roleFake) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, nil
}
func (f *roleFake) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *roleFake) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.EffectiveGrant], error) {
	return list.Page[roles.EffectiveGrant]{}, nil
}

func validModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "post",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"delete": relationships.AnyOf(relationships.Direct("owner"))},
		},
	}})
}

// TestExplainPublicSurface proves CheckExplain is reachable on the public Service
// and returns a coarse Explanation whose Decision matches the CheckResult's stable
// ReasonCode; an unwired relationship kind fails closed with the kind sentinel.
func TestExplainPublicSurface(t *testing.T) {
	comps, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	res, expl, err := svc.Decisions.CheckExplain(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if res.Allowed || res.ReasonCode != authmodel.ReasonDenied {
		t.Fatalf("relFake denies: allowed=%v code=%q", res.Allowed, res.ReasonCode)
	}
	if expl.Decision != res.ReasonCode {
		t.Fatalf("Explanation.Decision %q != ReasonCode %q", expl.Decision, res.ReasonCode)
	}

	rolesOnly, err := New(Repositories{Roles: &roleFake{}})
	if err != nil {
		t.Fatalf("NewService roles-only: %v", err)
	}
	// A roles-only host with NO role model bears no model at all, so the decision
	// surface refuses with ErrNoDecisionKind — "wire a model", not "wire the
	// relationship kind".
	if _, _, err := rolesOnly.Decisions.CheckExplain(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	}); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("unwired CheckExplain: want ErrNoDecisionKind, got %v", err)
	}
}

func TestNewServiceZeroKinds(t *testing.T) {
	_, err := New(Repositories{})
	if !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("want ErrNoKindConfigured, got %v", err)
	}
}

func TestNewServicePartialWiring(t *testing.T) {
	// Relationships without a Model.
	if _, err := New(Repositories{Relationships: &relFake{}}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("rel-without-model: want ErrModelRequired, got %v", err)
	}
	// Model without Relationships.
	if _, err := New(Repositories{Roles: &roleFake{}}, WithRelationshipModel(validModel())); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("model-without-rel: want ErrModelRequired, got %v", err)
	}
}

func TestNewServiceInvalidModel(t *testing.T) {
	bad := relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "post",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"delete": relationships.AnyOf(relationships.Direct("nonexistent"))},
		},
	}})
	_, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(bad))
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
}

// TestNewInvalidModelIsReportedBeforeInvalidLimits pins the v0.2.0
// construction ORDER on a relationship-only host: when both the Model and the
// Limits are bad, the schema is diagnosed first. The decision surface's budget
// is resolved only after the relationship engine is built, so gaining a second
// model-bearing kind did not move the boot error a host already sees.
func TestNewServiceInvalidModelIsReportedBeforeInvalidLimits(t *testing.T) {
	bad := relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "post",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"delete": relationships.AnyOf(relationships.Direct("nonexistent"))},
		},
	}})
	cfg := []Option{WithRelationshipModel(bad), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: -1})}
	_, err := New(Repositories{Relationships: &relFake{}}, cfg...)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
	if errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("the schema must be diagnosed before the budget, got %v", err)
	}
}

func TestNewServiceRolesOnlySucceeds(t *testing.T) {
	if _, err := New(Repositories{Roles: &roleFake{}}); err != nil {
		t.Fatalf("roles-only wiring should succeed with no model: %v", err)
	}
}

func TestNewServiceRelationshipsOnlySucceeds(t *testing.T) {
	if _, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel())); err != nil {
		t.Fatalf("relationships-only wiring should succeed: %v", err)
	}
}

func TestUnwiredRelationshipSentinel(t *testing.T) {
	comps, err := New(Repositories{Roles: &roleFake{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	// Check is a DECISION method: with no model-bearing kind it reports
	// ErrNoDecisionKind. The relationship-kind sentinel below still governs every
	// relationship-only method.
	if _, err := svc.Decisions.Check(context.Background(), authmodel.CheckRequest{}); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("Check: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.Mutations.GrantRelationship(context.Background(), mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u1"}}, mutations.GrantRelationshipCommand{}); !errors.Is(err, relationships.ErrRelationshipsNotConfigured) {
		t.Fatalf("GrantRelationship: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if svc.Relationships != nil {
		t.Fatal("unconfigured relationships must be absent")
	}
}

func TestUnwiredRolesSentinel(t *testing.T) {
	comps, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	if _, err := svc.Mutations.AssignRole(context.Background(), mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u1"}}, mutations.AssignRoleCommand{}); !errors.Is(err, roles.ErrRolesNotConfigured) {
		t.Fatalf("AssignRole: want ErrRolesNotConfigured, got %v", err)
	}
	if _, err := svc.Roles.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "editor", "", ""); !errors.Is(err, roles.ErrRolesNotConfigured) {
		t.Fatalf("HasRole: want ErrRolesNotConfigured, got %v", err)
	}
}

func TestDelegationSmokeBothKinds(t *testing.T) {
	rel := &relFake{}
	roles := &roleFake{}
	comps, err := New(Repositories{Relationships: rel, Roles: roles}, WithRelationshipModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	if _, err := svc.Decisions.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.checkCalls == 0 {
		t.Fatalf("Check did not reach the relationship store")
	}
	// Roles-kind delegation smoke via a READ (HasRole): the raw write path was
	// removed from Service (AZ3-3.4), and the guarded AssignRole needs the atomic
	// mutation repository not wired here. The roles-kind write delegation is proven
	// by the guarded role tests and storetest.
	if _, err := svc.Roles.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "editor", "", ""); err != nil {
		t.Fatalf("HasRole: %v", err)
	}
	if roles.hasCalls == 0 {
		t.Fatalf("HasRole did not reach the role store")
	}
}

// TestConstructionDefaultLimits proves a relationships wiring with a zero
// WithLimits succeeds: every budget field resolves to its safe default.
func TestConstructionDefaultLimits(t *testing.T) {
	if _, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel())); err != nil {
		t.Fatalf("zero Limits should resolve to defaults, got %v", err)
	}
}

// TestConstructionExplicitLimits proves a positive, fully specified WithLimits
// is accepted.
func TestConstructionExplicitLimits(t *testing.T) {
	cfg := []Option{WithRelationshipModel(validModel()), WithLimits(authmodel.EvaluationLimits{
		MaxThroughDepth:    5,
		MaxGraphStates:     500,
		MaxRelationTargets: 50,
		MaxBatchSize:       50,
		MaxLookupResults:   50,
	})}
	if _, err := New(Repositories{Relationships: &relFake{}}, cfg...); err != nil {
		t.Fatalf("explicit positive Limits should be accepted, got %v", err)
	}
}

// TestConstructionNegativeLimitRejected proves EVERY budget field rejects a
// negative value with ErrInvalidLimits when the relationship kind is wired.
func TestConstructionNegativeLimitRejected(t *testing.T) {
	cases := map[string]authmodel.EvaluationLimits{
		"MaxThroughDepth":    {MaxThroughDepth: -1},
		"MaxGraphStates":     {MaxGraphStates: -1},
		"MaxRelationTargets": {MaxRelationTargets: -1},
		"MaxBatchSize":       {MaxBatchSize: -1},
		"MaxLookupResults":   {MaxLookupResults: -1},
	}
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel()), WithLimits(limits))
			if !errors.Is(err, authmodel.ErrInvalidLimits) {
				t.Fatalf("negative %s: want ErrInvalidLimits, got %v", name, err)
			}
		})
	}
}

// TestConstructionOrphanedLimitsUnderRolesOnly proves that WithLimits set on
// a roles-only wiring is a silently orphaned tuning field (the auth MailFrom
// precedent): it is not validated and not an error, because no relationship
// engine consumes it. Even a negative limit is ignored when the kind is off.
func TestConstructionOrphanedLimitsUnderRolesOnly(t *testing.T) {
	cfg := []Option{WithLimits(authmodel.EvaluationLimits{MaxThroughDepth: -1, MaxBatchSize: -1})}
	if _, err := New(Repositories{Roles: &roleFake{}}, cfg...); err != nil {
		t.Fatalf("orphaned Limits under roles-only wiring must be ignored, got %v", err)
	}
}

func TestRegister(t *testing.T) {
	comps, err := New(Repositories{Roles: &roleFake{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	// With a logger.
	if err := svc.Register(pockets.Mount{Logger: slog.Default()}); err != nil {
		t.Fatalf("Register with logger: %v", err)
	}
	// Zero-value Mount (nil logger) is tolerated.
	if err := svc.Register(pockets.Mount{}); err != nil {
		t.Fatalf("Register with zero Mount: %v", err)
	}
}

// -----------------------------------------------------------------------------
// The roles kind's model: construction matrix and the ONE decision surface
// -----------------------------------------------------------------------------

// projectRoleModel is the roles-kind fixture: one type, one role, one permission.
func projectRoleModel() authmodel.RoleModel {
	return authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
}

func assignment(subjectID, roleName, resourceType, resourceID string) roles.Assignment {
	return roles.Assignment{
		SubjectType: "user", SubjectID: subjectID, Role: roleName,
		ResourceType: resourceType, ResourceID: resourceID,
	}
}

// newSeededRoles builds an in-core role store already holding the assignments —
// the decision-surface tests read roles, they do not exercise the write path.
func newSeededRoles(t *testing.T, assignments ...roles.Assignment) roles.Storer {
	t.Helper()
	store := memory.NewRoles()
	for _, a := range assignments {
		if err := store.Assign(context.Background(), a); err != nil {
			t.Fatalf("seed %+v: %v", a, err)
		}
	}
	return store
}

func projectRequest(subjectID, permission, projectID string) authmodel.CheckRequest {
	return authmodel.CheckRequest{
		Principal:  authmodel.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authmodel.Resource{Type: "project", ID: projectID},
	}
}

// TestConstructionRoleModelWithoutRolesRepo proves the one-directional wiring
// rule: a model with no roles repository could never decide anything, so it fails
// boot — while a roles repository with NO model stays the valid opaque posture
// (TestNewRolesOnlySucceeds).
func TestConstructionRoleModelWithoutRolesRepo(t *testing.T) {
	_, err := New(Repositories{Relationships: &relFake{}}, WithRelationshipModel(validModel()), WithRoleModel(projectRoleModel()))
	if !errors.Is(err, ErrRoleModelWithoutRoles) {
		t.Fatalf("want ErrRoleModelWithoutRoles, got %v", err)
	}
}

// TestConstructionInvalidRoleModel proves a structurally invalid model is a loud
// boot failure naming the offending symbol.
func TestConstructionInvalidRoleModel(t *testing.T) {
	bad := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"inspector"}}},
	}}
	_, err := New(Repositories{Roles: &roleFake{}}, WithRoleModel(bad))
	if !errors.Is(err, authmodel.ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !strings.Contains(err.Error(), "inspector") {
		t.Fatalf("the message must name the offending symbol, got %v", err)
	}
}

// TestConstructionModelConflict proves pair ownership is enforced at boot: a
// resource TYPE may appear in both models, but a (type, permission) PAIR may not
// — that overlap is what would make the decision surface a merge.
func TestConstructionModelConflict(t *testing.T) {
	conflicting := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"post": {Roles: []string{"auditor"}, Permissions: map[string][]string{"delete": {"auditor"}}},
	}}
	_, err := New(Repositories{Relationships: &relFake{}, Roles: &roleFake{}}, WithRelationshipModel(validModel()), WithRoleModel(conflicting))
	if !errors.Is(err, authmodel.ErrModelConflict) {
		t.Fatalf("want ErrModelConflict, got %v", err)
	}

	// The same TYPE with a DIFFERENT permission is legal — the auth-cms split.
	split := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"post": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
	if _, err := New(Repositories{Relationships: &relFake{}, Roles: &roleFake{}}, WithRelationshipModel(validModel()), WithRoleModel(split)); err != nil {
		t.Fatalf("a type shared by both models with distinct permissions must construct: %v", err)
	}
}

// TestDecisionSurfaceWithoutAModelBearingKind proves EVERY decision method on a
// roles-only host with no role model reports ErrNoDecisionKind — the honest
// diagnosis ("wire a model"), not the relationship kind's sentinel. Every other
// relationship-kind method keeps ErrRelationshipsNotConfigured
// (TestUnwiredRelationshipSentinel).
func TestDecisionSurfaceWithoutAModelBearingKind(t *testing.T) {
	comps, err := New(Repositories{Roles: &roleFake{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	ctx := context.Background()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	if _, err := svc.Decisions.Check(ctx, projectRequest("u1", "audit", "p1")); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("Check: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.Decisions.CheckBatch(ctx, []authmodel.CheckRequest{projectRequest("u1", "audit", "p1")}); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("CheckBatch: want ErrNoDecisionKind, got %v", err)
	}
	if _, _, err := svc.Decisions.CheckExplain(ctx, projectRequest("u1", "audit", "p1")); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("CheckExplain: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.Decisions.FilterAuthorized(ctx, principal, "audit", "project", []string{"p1"}); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("FilterAuthorized: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, "audit", "project"); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("LookupAllResourceIDs: want ErrNoDecisionKind, got %v", err)
	}
	// The wiring sentinel is reported BEFORE the request is validated: an unwired
	// decider is the operator's fault, not the caller's.
	if _, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{Principal: principal, Permission: "audit", ResourceType: "project", Limit: -1}); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("LookupResourceIDPage: want ErrNoDecisionKind, got %v", err)
	}
	// The wiring sentinel is reported BEFORE the zero-length shortcut, exactly as
	// the relationship-kind sentinel was: a modelless host learns it is
	// misconfigured even on a call with nothing to decide.
	if _, err := svc.Decisions.FilterAuthorized(ctx, principal, "audit", "project", nil); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("FilterAuthorized with no IDs: want ErrNoDecisionKind, got %v", err)
	}
}

// TestLookupResourcesInThroughTheFacade drives the PAGED surface through the
// PUBLIC API: a page smaller than the result carries HasMore plus a continuation
// and a page that holds everything carries
// neither, walking the cursor reproduces the classic LookupAllResourceIDs result
// exactly, and an out-of-range Limit is invalid input a host maps to 400.
func TestLookupResourcesInThroughTheFacade(t *testing.T) {
	roles := newSeededRoles(t,
		assignment("u1", "auditor", "project", "p1"),
		assignment("u1", "auditor", "project", "p2"),
		assignment("u1", "auditor", "project", "p3"),
	)
	comps, err := New(Repositories{Roles: roles}, WithRoleModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	ctx := context.Background()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	for name, tc := range map[string]struct {
		limit       int
		wantIDs     []string
		wantHasMore bool
	}{
		"limit below the count pages":    {2, []string{"p1", "p2"}, true},
		"limit that fits":                {3, []string{"p1", "p2", "p3"}, false},
		"limit zero is the page ceiling": {0, []string{"p1", "p2", "p3"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
				Principal: principal, Permission: "audit", ResourceType: "project", Limit: tc.limit,
			})
			if err != nil {
				t.Fatalf("LookupResourceIDPage: %v", err)
			}
			if !reflect.DeepEqual(got.IDs, tc.wantIDs) {
				t.Fatalf("IDs = %v, want %v", got.IDs, tc.wantIDs)
			}
			if got.HasMore != tc.wantHasMore {
				t.Fatalf("HasMore = %v, want %v", got.HasMore, tc.wantHasMore)
			}
			if (got.NextCursor != "") != tc.wantHasMore {
				t.Fatalf("NextCursor %q disagrees with HasMore %v", got.NextCursor, got.HasMore)
			}
		})
	}

	// Walking the continuation reproduces the classic enumeration exactly.
	var walked []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 8 {
			t.Fatal("page walk did not terminate")
		}
		page, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "audit", ResourceType: "project", Limit: 1, After: cursor,
		})
		if err != nil {
			t.Fatalf("LookupResourceIDPage(after=%q): %v", cursor, err)
		}
		walked = append(walked, page.IDs...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	classic, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, "audit", "project")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs: %v", err)
	}
	if len(classic.IDs) != 3 {
		t.Fatalf("the classic method never pages and never reports a continuation, got %+v", classic)
	}
	if !reflect.DeepEqual(walked, classic.IDs) {
		t.Fatalf("the pages concatenated to %v, want the classic result %v", walked, classic.IDs)
	}

	// A cursor from ANOTHER principal is refused: it is bound to the query that
	// minted it, and a host maps the refusal to 400.
	first, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
		Principal: principal, Permission: "audit", ResourceType: "project", Limit: 1,
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("seed page: %+v, %v", first, err)
	}
	_, err = svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u2"}, Permission: "audit", ResourceType: "project",
		Limit: 1, After: first.NextCursor,
	})
	if !errors.Is(err, authmodel.ErrInvalidCursor) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("a foreign cursor must be ErrInvalidCursor wrapping sdk.ErrInvalidInput, got %v", err)
	}

	for name, req := range map[string]decisions.ResourceIDPageRequest{
		"negative limit": {Principal: principal, Permission: "audit", ResourceType: "project", Limit: -1},
		"limit above MaxLookupResults": {Principal: principal, Permission: "audit", ResourceType: "project",
			Limit: authmodel.DefaultMaxLookupResults + 1},
		"malformed cursor": {Principal: principal, Permission: "audit", ResourceType: "project", After: "not-a-cursor"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Decisions.LookupResourceIDPage(ctx, req); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestRolesOnlyWithModelDecides proves the flagship acceptance: a roles-only host
// that configures a RoleModel gets the whole decision surface, answered from role
// assignments alone.
func TestRolesOnlyWithModelDecides(t *testing.T) {
	roles := newSeededRoles(t,
		assignment("u1", "auditor", "project", "p1"),
		assignment("u2", "auditor", "", ""), // globally held
	)
	comps, err := New(Repositories{Roles: roles}, WithRoleModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	ctx := context.Background()

	res, err := svc.Decisions.Check(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
		t.Fatalf("scoped role holder: got %+v err=%v", res, err)
	}
	res, err = svc.Decisions.Check(ctx, projectRequest("u1", "audit", "p2"))
	if err != nil || res.Allowed || res.ReasonCode != authmodel.ReasonDenied {
		t.Fatalf("another project: got %+v err=%v", res, err)
	}
	res, err = svc.Decisions.Check(ctx, projectRequest("u1", "publish", "p1"))
	if err != nil || res.Allowed || res.Reason != "no rules defined" {
		t.Fatalf("undeclared pair: got %+v err=%v", res, err)
	}

	lookup, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project")
	if err != nil || lookup.Unrestricted || !reflect.DeepEqual(lookup.IDs, []string{"p1"}) {
		t.Fatalf("scoped lookup: got %+v err=%v", lookup, err)
	}
	lookup, err = svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u2"}, "audit", "project")
	if err != nil || !lookup.Unrestricted || len(lookup.IDs) != 0 {
		t.Fatalf("global granting role: want unrestricted with empty IDs, got %+v err=%v", lookup, err)
	}

	ids, err := svc.Decisions.FilterAuthorized(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", []string{"p1", "p2"})
	if err != nil || !reflect.DeepEqual(ids, []string{"p1"}) {
		t.Fatalf("FilterAuthorized: got %v err=%v", ids, err)
	}
	// Zero-length identities are preserved literally on a wired decision surface.
	if ids, err := svc.Decisions.FilterAuthorized(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", nil); ids != nil || err != nil {
		t.Fatalf("FilterAuthorized with no IDs: want (nil, nil), got (%v, %v)", ids, err)
	}
	if results, err := svc.Decisions.CheckBatch(ctx, nil); results != nil || err != nil {
		t.Fatalf("CheckBatch(nil): want (nil, nil), got (%v, %v)", results, err)
	}

	_, expl, err := svc.Decisions.CheckExplain(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || len(expl.Steps) != 1 || expl.Steps[0].Kind != authmodel.ExplainKindRole ||
		expl.Steps[0].Role != "auditor" || expl.Steps[0].Scope != authmodel.ExplainScopeDirect {
		t.Fatalf("role explain trace: got %+v err=%v", expl, err)
	}
}

// TestConstructionLimitsUnderRolesAndModel proves the evaluation budget is the
// DECISION SURFACE's: with a role model wired, a negative limit fails boot (it is
// no longer an orphaned setting) and MaxBatchSize is captured and charged.
func TestConstructionLimitsUnderRolesAndModel(t *testing.T) {
	_, err := New(Repositories{Roles: &roleFake{}}, WithRoleModel(projectRoleModel()), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: -1}))
	if !errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("negative limit under roles+model: want ErrInvalidLimits, got %v", err)
	}

	comps, err := New(Repositories{Roles: newSeededRoles(t)}, WithRoleModel(projectRoleModel()), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 2}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.Decisions.Limits().MaxBatchSize != 2 {
		t.Fatalf("maxBatchSize: want 2, got %d", comps.Decisions.Limits().MaxBatchSize)
	}
	reqs := []authmodel.CheckRequest{
		projectRequest("u1", "audit", "p1"),
		projectRequest("u1", "audit", "p2"),
		projectRequest("u1", "audit", "p3"),
	}
	if _, err := comps.Decisions.CheckBatch(context.Background(), reqs); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("over the batch ceiling: want ErrEvaluationLimit, got %v", err)
	}
}

// TestRegisterLogsRoleModelPresence proves the mount line reports whether a role
// model is configured — a BOOL only; no type, role, or permission name reaches
// the log.
func TestRegisterLogsRoleModelPresence(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  []Option
		want string
	}{
		"with a role model":    {[]Option{WithRoleModel(projectRoleModel())}, `"role_model":true`},
		"without a role model": {[]Option{}, `"role_model":false`},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.cfg = append(tc.cfg, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
			comps, err := New(Repositories{Roles: &roleFake{}}, tc.cfg...)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			if err := comps.Register(pockets.Mount{}); err != nil {
				t.Fatalf("Register: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("mount line %s: want %s, got %s", name, tc.want, buf.String())
			}
			if strings.Contains(buf.String(), "auditor") || strings.Contains(buf.String(), "project") {
				t.Fatalf("policy vocabulary must never reach the log line: %s", buf.String())
			}
		})
	}
}
