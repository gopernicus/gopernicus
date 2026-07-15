package authorization

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
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
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()})
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
	if _, _, err := rolesOnly.Service.CheckExplain(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("unwired CheckExplain: want ErrRelationshipsNotConfigured, got %v", err)
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
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{Model: validModel()}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("model-without-rel: want ErrModelRequired, got %v", err)
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
	_, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: bad})
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
}

func TestNewServiceRolesOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{}); err != nil {
		t.Fatalf("roles-only wiring should succeed with no model: %v", err)
	}
}

func TestNewServiceRelationshipsOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()}); err != nil {
		t.Fatalf("relationships-only wiring should succeed: %v", err)
	}
}

func TestUnwiredRelationshipSentinel(t *testing.T) {
	comps, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	if _, err := svc.Check(context.Background(), CheckRequest{}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("Check: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if _, err := svc.GrantRelationship(context.Background(), Actor{PrincipalRef{Type: "user", ID: "u1"}}, GrantRelationshipCommand{}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("GrantRelationship: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if _, err := svc.GetSchema(); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("GetSchema: want ErrRelationshipsNotConfigured, got %v", err)
	}
}

func TestUnwiredRolesSentinel(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()})
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
	comps, err := NewService(Repositories{Relationships: rel, Roles: roles}, Config{Model: validModel()})
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
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()}); err != nil {
		t.Fatalf("zero Limits should resolve to defaults, got %v", err)
	}
}

// TestConstructionExplicitLimits proves a positive, fully specified Config.Limits
// is accepted.
func TestConstructionExplicitLimits(t *testing.T) {
	cfg := Config{
		Model: validModel(),
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
			_, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel(), Limits: limits})
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
