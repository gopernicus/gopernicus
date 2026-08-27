package authorization

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// relFake is a trivial relationship.Storer for socket wiring/delegation tests.
type relFake struct{ checkCalls int }

func (f *relFake) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	f.checkCalls++
	return false, nil
}
func (f *relFake) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}
func (f *relFake) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (f *relFake) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	return nil
}
func (f *relFake) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, targets []relationship.CreateRelationship) error {
	return nil
}
func (f *relFake) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationship.SubjectRef) error {
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
func (f *relFake) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	return crud.Page[relationship.SubjectRelationship]{}, nil
}
func (f *relFake) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	return crud.Page[relationship.ResourceRelationship]{}, nil
}
func (f *relFake) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string, limit int) ([]string, error) {
	return nil, nil
}

// roleFake is a trivial role.Storer for socket wiring/delegation tests.
type roleFake struct {
	hasCalls int
}

func (f *roleFake) Assign(ctx context.Context, a role.Assignment) error { return nil }
func (f *roleFake) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return nil
}
func (f *roleFake) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	f.hasCalls++
	return false, nil
}
func (f *roleFake) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, nil
}
func (f *roleFake) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, nil
}
func (f *roleFake) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	return crud.Page[role.EffectiveGrant]{}, nil
}

func validModel() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "post",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("owner"))},
		},
	}})
}

// TestExplainPublicSurface proves CheckExplain is reachable on the public Service
// and returns a coarse Explanation whose Decision matches the CheckResult's stable
// ReasonCode; an unwired relationship kind fails closed with the kind sentinel.
func TestExplainPublicSurface(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	res, expl, err := svc.CheckExplain(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if res.Allowed || res.ReasonCode != ReasonDenied {
		t.Fatalf("relFake denies: allowed=%v code=%q", res.Allowed, res.ReasonCode)
	}
	if expl.Decision != res.ReasonCode {
		t.Fatalf("Explanation.Decision %q != ReasonCode %q", expl.Decision, res.ReasonCode)
	}

	rolesOnly, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService roles-only: %v", err)
	}
	// A roles-only host with NO role model bears no model at all, so the decision
	// surface refuses with ErrNoDecisionKind — "wire a model", not "wire the
	// relationship kind".
	if _, _, err := rolesOnly.Service.CheckExplain(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	}); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("unwired CheckExplain: want ErrNoDecisionKind, got %v", err)
	}
}

func TestNewServiceZeroKinds(t *testing.T) {
	_, err := NewService(Repositories{}, Config{})
	if !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("want ErrNoKindConfigured, got %v", err)
	}
}

func TestNewServicePartialWiring(t *testing.T) {
	// Relationships without a Model.
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("rel-without-model: want ErrModelRequired, got %v", err)
	}
	// Model without Relationships.
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{RelationshipModel: validModel()}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("model-without-rel: want ErrModelRequired, got %v", err)
	}
}

// TestConfigModelDeprecatedPassThrough pins the one-release deprecation window:
// the old Config.Model still wires the relationship kind exactly as
// Config.RelationshipModel does, and a host that sets BOTH is refused with
// ErrConfigConflict rather than having one silently win. It also pins the
// construction check ORDER — zero kinds first, then the config conflict, then the
// partial-wiring error — so a misconfigured host reads the most structural fault.
func TestConfigModelDeprecatedPassThrough(t *testing.T) {
	deprecated, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()})
	if err != nil {
		t.Fatalf("deprecated Config.Model must still wire the relationship kind: %v", err)
	}
	current, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	wantDigest, err := current.Service.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest: %v", err)
	}
	gotDigest, err := deprecated.Service.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest: %v", err)
	}
	if gotDigest != wantDigest {
		t.Fatalf("deprecated Config.Model compiled a different schema: %q != %q", gotDigest, wantDigest)
	}

	both := Config{Model: validModel(), RelationshipModel: validModel()}
	if _, err := NewService(Repositories{Relationships: &relFake{}}, both); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("both models set: want ErrConfigConflict, got %v", err)
	}
	if !errors.Is(ErrConfigConflict, sdk.ErrInvalidInput) {
		t.Fatalf("ErrConfigConflict must wrap sdk.ErrInvalidInput")
	}
	// Zero kinds outranks the conflict...
	if _, err := NewService(Repositories{}, both); !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("zero kinds with both models: want ErrNoKindConfigured, got %v", err)
	}
	// ...and the conflict outranks the partial wiring it would otherwise report.
	if _, err := NewService(Repositories{Roles: &roleFake{}}, both); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("both models on a roles-only wiring: want ErrConfigConflict, got %v", err)
	}
}

func TestNewServiceInvalidModel(t *testing.T) {
	bad := NewSchema([]ResourceSchema{{
		Name: "post",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("nonexistent"))},
		},
	}})
	_, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: bad})
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
}

// TestNewServiceInvalidModelIsReportedBeforeInvalidLimits pins the v0.2.0
// construction ORDER on a relationship-only host: when both the Model and the
// Limits are bad, the schema is diagnosed first. The decision surface's budget
// is resolved only after the relationship engine is built, so gaining a second
// model-bearing kind did not move the boot error a host already sees.
func TestNewServiceInvalidModelIsReportedBeforeInvalidLimits(t *testing.T) {
	bad := NewSchema([]ResourceSchema{{
		Name: "post",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("nonexistent"))},
		},
	}})
	cfg := Config{RelationshipModel: bad, Limits: EvaluationLimits{MaxBatchSize: -1}}
	_, err := NewService(Repositories{Relationships: &relFake{}}, cfg)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
	if errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("the schema must be diagnosed before the budget, got %v", err)
	}
}

func TestNewServiceRolesOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{}); err != nil {
		t.Fatalf("roles-only wiring should succeed with no model: %v", err)
	}
}

func TestNewServiceRelationshipsOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()}); err != nil {
		t.Fatalf("relationships-only wiring should succeed: %v", err)
	}
}

func TestUnwiredRelationshipSentinel(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	// Check is a DECISION method: with no model-bearing kind it reports
	// ErrNoDecisionKind. The relationship-kind sentinel below still governs every
	// relationship-only method.
	if _, err := svc.Check(context.Background(), CheckRequest{}); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("Check: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.GrantRelationship(context.Background(), Actor{PrincipalRef{Type: "user", ID: "u1"}}, GrantRelationshipCommand{}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("GrantRelationship: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if _, err := svc.GetSchema(); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("GetSchema: want ErrRelationshipsNotConfigured, got %v", err)
	}
}

func TestUnwiredRolesSentinel(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	if _, err := svc.AssignRole(context.Background(), Actor{PrincipalRef{Type: "user", ID: "u1"}}, AssignRoleCommand{}); !errors.Is(err, ErrRolesNotConfigured) {
		t.Fatalf("AssignRole: want ErrRolesNotConfigured, got %v", err)
	}
	if _, err := svc.HasRole(context.Background(), PrincipalRef{Type: "user", ID: "u1"}, "editor", "", ""); !errors.Is(err, ErrRolesNotConfigured) {
		t.Fatalf("HasRole: want ErrRolesNotConfigured, got %v", err)
	}
}

func TestDelegationSmokeBothKinds(t *testing.T) {
	rel := &relFake{}
	roles := &roleFake{}
	comps, err := NewService(Repositories{Relationships: rel, Roles: roles}, Config{RelationshipModel: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	if _, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
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
	if _, err := svc.HasRole(context.Background(), PrincipalRef{Type: "user", ID: "u1"}, "editor", "", ""); err != nil {
		t.Fatalf("HasRole: %v", err)
	}
	if roles.hasCalls == 0 {
		t.Fatalf("HasRole did not reach the role store")
	}
}

// TestConstructionDefaultLimits proves a relationships wiring with a zero
// Config.Limits succeeds: every budget field resolves to its safe default.
func TestConstructionDefaultLimits(t *testing.T) {
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel()}); err != nil {
		t.Fatalf("zero Limits should resolve to defaults, got %v", err)
	}
}

// TestConstructionExplicitLimits proves a positive, fully specified Config.Limits
// is accepted.
func TestConstructionExplicitLimits(t *testing.T) {
	cfg := Config{
		RelationshipModel: validModel(),
		Limits: EvaluationLimits{
			MaxThroughDepth:    5,
			MaxGraphStates:     500,
			MaxRelationTargets: 50,
			MaxBatchSize:       50,
			MaxLookupResults:   50,
		},
	}
	if _, err := NewService(Repositories{Relationships: &relFake{}}, cfg); err != nil {
		t.Fatalf("explicit positive Limits should be accepted, got %v", err)
	}
}

// TestConstructionNegativeLimitRejected proves EVERY budget field rejects a
// negative value with ErrInvalidLimits when the relationship kind is wired.
func TestConstructionNegativeLimitRejected(t *testing.T) {
	cases := map[string]EvaluationLimits{
		"MaxThroughDepth":    {MaxThroughDepth: -1},
		"MaxGraphStates":     {MaxGraphStates: -1},
		"MaxRelationTargets": {MaxRelationTargets: -1},
		"MaxBatchSize":       {MaxBatchSize: -1},
		"MaxLookupResults":   {MaxLookupResults: -1},
	}
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel(), Limits: limits})
			if !errors.Is(err, ErrInvalidLimits) {
				t.Fatalf("negative %s: want ErrInvalidLimits, got %v", name, err)
			}
		})
	}
}

// TestConstructionOrphanedLimitsUnderRolesOnly proves that Config.Limits set on
// a roles-only wiring is a silently orphaned tuning field (the auth MailFrom
// precedent): it is not validated and not an error, because no relationship
// engine consumes it. Even a negative limit is ignored when the kind is off.
func TestConstructionOrphanedLimitsUnderRolesOnly(t *testing.T) {
	cfg := Config{Limits: EvaluationLimits{MaxThroughDepth: -1, MaxBatchSize: -1}}
	if _, err := NewService(Repositories{Roles: &roleFake{}}, cfg); err != nil {
		t.Fatalf("orphaned Limits under roles-only wiring must be ignored, got %v", err)
	}
}

func TestRegister(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	// With a logger.
	if err := svc.Register(feature.Mount{Logger: slog.Default()}); err != nil {
		t.Fatalf("Register with logger: %v", err)
	}
	// Zero-value Mount (nil logger) is tolerated.
	if err := svc.Register(feature.Mount{}); err != nil {
		t.Fatalf("Register with zero Mount: %v", err)
	}
}

// -----------------------------------------------------------------------------
// The roles kind's model: construction matrix and the ONE decision surface
// -----------------------------------------------------------------------------

// projectRoleModel is the roles-kind fixture: one type, one role, one permission.
func projectRoleModel() RoleModel {
	return RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
}

func assignment(subjectID, roleName, resourceType, resourceID string) role.Assignment {
	return role.Assignment{
		SubjectType: "user", SubjectID: subjectID, Role: roleName,
		ResourceType: resourceType, ResourceID: resourceID,
	}
}

// newSeededRoles builds an in-core role store already holding the assignments —
// the decision-surface tests read roles, they do not exercise the write path.
func newSeededRoles(t *testing.T, assignments ...role.Assignment) role.Storer {
	t.Helper()
	store := memstore.NewRoles()
	for _, a := range assignments {
		if err := store.Assign(context.Background(), a); err != nil {
			t.Fatalf("seed %+v: %v", a, err)
		}
	}
	return store
}

func projectRequest(subjectID, permission, projectID string) CheckRequest {
	return CheckRequest{
		Principal:  PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   Resource{Type: "project", ID: projectID},
	}
}

// TestConstructionRoleModelWithoutRolesRepo proves the one-directional wiring
// rule: a model with no roles repository could never decide anything, so it fails
// boot — while a roles repository with NO model stays the valid opaque posture
// (TestNewServiceRolesOnlySucceeds).
func TestConstructionRoleModelWithoutRolesRepo(t *testing.T) {
	_, err := NewService(Repositories{Relationships: &relFake{}}, Config{RelationshipModel: validModel(), RoleModel: projectRoleModel()})
	if !errors.Is(err, ErrRoleModelWithoutRoles) {
		t.Fatalf("want ErrRoleModelWithoutRoles, got %v", err)
	}
}

// TestConstructionInvalidRoleModel proves a structurally invalid model is a loud
// boot failure naming the offending symbol.
func TestConstructionInvalidRoleModel(t *testing.T) {
	bad := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"inspector"}}},
	}}
	_, err := NewService(Repositories{Roles: &roleFake{}}, Config{RoleModel: bad})
	if !errors.Is(err, ErrInvalidRoleModel) {
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
	conflicting := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"post": {Roles: []string{"auditor"}, Permissions: map[string][]string{"delete": {"auditor"}}},
	}}
	_, err := NewService(Repositories{Relationships: &relFake{}, Roles: &roleFake{}},
		Config{RelationshipModel: validModel(), RoleModel: conflicting})
	if !errors.Is(err, ErrModelConflict) {
		t.Fatalf("want ErrModelConflict, got %v", err)
	}

	// The same TYPE with a DIFFERENT permission is legal — the auth-cms split.
	split := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"post": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
	if _, err := NewService(Repositories{Relationships: &relFake{}, Roles: &roleFake{}},
		Config{RelationshipModel: validModel(), RoleModel: split}); err != nil {
		t.Fatalf("a type shared by both models with distinct permissions must construct: %v", err)
	}
}

// TestDecisionSurfaceWithoutAModelBearingKind proves EVERY decision method on a
// roles-only host with no role model reports ErrNoDecisionKind — the honest
// diagnosis ("wire a model"), not the relationship kind's sentinel. Every other
// relationship-kind method keeps ErrRelationshipsNotConfigured
// (TestUnwiredRelationshipSentinel).
func TestDecisionSurfaceWithoutAModelBearingKind(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	ctx := context.Background()
	principal := PrincipalRef{Type: "user", ID: "u1"}

	if _, err := svc.Check(ctx, projectRequest("u1", "audit", "p1")); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("Check: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.CheckBatch(ctx, []CheckRequest{projectRequest("u1", "audit", "p1")}); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("CheckBatch: want ErrNoDecisionKind, got %v", err)
	}
	if _, _, err := svc.CheckExplain(ctx, projectRequest("u1", "audit", "p1")); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("CheckExplain: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.FilterAuthorized(ctx, principal, "audit", "project", []string{"p1"}); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("FilterAuthorized: want ErrNoDecisionKind, got %v", err)
	}
	if _, err := svc.LookupResources(ctx, principal, "audit", "project"); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("LookupResources: want ErrNoDecisionKind, got %v", err)
	}
	// The wiring sentinel is reported BEFORE the zero-length shortcut, exactly as
	// the relationship-kind sentinel was: a modelless host learns it is
	// misconfigured even on a call with nothing to decide.
	if _, err := svc.FilterAuthorized(ctx, principal, "audit", "project", nil); !errors.Is(err, ErrNoDecisionKind) {
		t.Fatalf("FilterAuthorized with no IDs: want ErrNoDecisionKind, got %v", err)
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
	comps, err := NewService(Repositories{Roles: roles}, Config{RoleModel: projectRoleModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	ctx := context.Background()

	res, err := svc.Check(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || !res.Allowed || res.ReasonCode != ReasonGranted {
		t.Fatalf("scoped role holder: got %+v err=%v", res, err)
	}
	res, err = svc.Check(ctx, projectRequest("u1", "audit", "p2"))
	if err != nil || res.Allowed || res.ReasonCode != ReasonDenied {
		t.Fatalf("another project: got %+v err=%v", res, err)
	}
	res, err = svc.Check(ctx, projectRequest("u1", "publish", "p1"))
	if err != nil || res.Allowed || res.Reason != "no rules defined" {
		t.Fatalf("undeclared pair: got %+v err=%v", res, err)
	}

	lookup, err := svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u1"}, "audit", "project")
	if err != nil || lookup.Unrestricted || !reflect.DeepEqual(lookup.IDs, []string{"p1"}) {
		t.Fatalf("scoped lookup: got %+v err=%v", lookup, err)
	}
	lookup, err = svc.LookupResources(ctx, PrincipalRef{Type: "user", ID: "u2"}, "audit", "project")
	if err != nil || !lookup.Unrestricted || len(lookup.IDs) != 0 {
		t.Fatalf("global granting role: want unrestricted with empty IDs, got %+v err=%v", lookup, err)
	}

	ids, err := svc.FilterAuthorized(ctx, PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", []string{"p1", "p2"})
	if err != nil || !reflect.DeepEqual(ids, []string{"p1"}) {
		t.Fatalf("FilterAuthorized: got %v err=%v", ids, err)
	}
	// Zero-length identities are preserved literally on a wired decision surface.
	if ids, err := svc.FilterAuthorized(ctx, PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", nil); ids != nil || err != nil {
		t.Fatalf("FilterAuthorized with no IDs: want (nil, nil), got (%v, %v)", ids, err)
	}
	if results, err := svc.CheckBatch(ctx, nil); results != nil || err != nil {
		t.Fatalf("CheckBatch(nil): want (nil, nil), got (%v, %v)", results, err)
	}

	_, expl, err := svc.CheckExplain(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || len(expl.Steps) != 1 || expl.Steps[0].Kind != ExplainKindRole ||
		expl.Steps[0].Role != "auditor" || expl.Steps[0].Scope != ExplainScopeDirect {
		t.Fatalf("role explain trace: got %+v err=%v", expl, err)
	}
}

// TestConstructionLimitsUnderRolesAndModel proves the evaluation budget is the
// DECISION SURFACE's: with a role model wired, a negative limit fails boot (it is
// no longer an orphaned setting) and MaxBatchSize is captured and charged.
func TestConstructionLimitsUnderRolesAndModel(t *testing.T) {
	_, err := NewService(Repositories{Roles: &roleFake{}},
		Config{RoleModel: projectRoleModel(), Limits: EvaluationLimits{MaxBatchSize: -1}})
	if !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("negative limit under roles+model: want ErrInvalidLimits, got %v", err)
	}

	comps, err := NewService(Repositories{Roles: newSeededRoles(t)},
		Config{RoleModel: projectRoleModel(), Limits: EvaluationLimits{MaxBatchSize: 2}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.Service.maxBatchSize != 2 {
		t.Fatalf("maxBatchSize: want 2, got %d", comps.Service.maxBatchSize)
	}
	reqs := []CheckRequest{
		projectRequest("u1", "audit", "p1"),
		projectRequest("u1", "audit", "p2"),
		projectRequest("u1", "audit", "p3"),
	}
	if _, err := comps.Service.CheckBatch(context.Background(), reqs); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("over the batch ceiling: want ErrEvaluationLimit, got %v", err)
	}
}

// TestRegisterLogsRoleModelPresence proves the mount line reports whether a role
// model is configured — a BOOL only; no type, role, or permission name reaches
// the log.
func TestRegisterLogsRoleModelPresence(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  Config
		want string
	}{
		"with a role model":    {Config{RoleModel: projectRoleModel()}, `"role_model":true`},
		"without a role model": {Config{}, `"role_model":false`},
	} {
		t.Run(name, func(t *testing.T) {
			comps, err := NewService(Repositories{Roles: &roleFake{}}, tc.cfg)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			var buf bytes.Buffer
			if err := comps.Service.Register(feature.Mount{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}); err != nil {
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
